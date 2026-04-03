package api

import (
	"tally/internal/event"
	"tally/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"net/http"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
)

type Handler struct {
	Pool *pgxpool.Pool 
	Log zerolog.Logger
	Client *redis.Client
}

func (h *Handler) PostEvent(w http.ResponseWriter, r *http.Request) {
	var ev event.CanonicalEvent
	err := json.NewDecoder(r.Body).Decode(&ev)

	if err != nil {
		http.Error(w, "Failed to decode request", http.StatusBadRequest)
		h.Log.Info().Err(err).Msg("Failed to decode request")
		return
	}

	notSeen, err := store.InsertEvent(r.Context(), h.Pool, &ev)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		h.Log.Info().Err(err).Msg("Failed to insert event")
		return
	} 
	
	if notSeen {
		w.WriteHeader(http.StatusCreated)
		h.Log.Info().Msg("Successfully inserted event")
	} else {
		w.WriteHeader(http.StatusOK)
		h.Log.Info().Msg("Event already exists")
	}

	err = store.AddCandidate(r.Context(), h.Client, &ev)
	if err != nil {
		h.Log.Error().Err(err).Msg("Adding event failed")
		return
	}

	candidates, _ := store.FindCandidates(r.Context(), h.Client, ev.Currency, ev.AmountMinor, 60)
	h.Log.Info().Int("candidate_count", len(candidates)).Msg("candidates found")  

	for _, id := range candidates {
		h.Log.Info().Str("candidate_id", id).Str("current_event", ev.EventID).Msg("candidate found")
		if id == ev.EventID {
			continue
		}
		err = store.ConfirmMatch(r.Context(), h.Pool, ev.EventID, id)
		if err != nil {
			h.Log.Error().Err(err).Msg("Confirming match failed")
			return
		}
		err = store.RemoveCandidate(r.Context(), h.Client, &ev)
		if err != nil {
			h.Log.Error().Err(err).Msg("Removing candidate failed")
			return
		}

		tempEv, err := store.GetEvent(r.Context(), h.Pool, id)
		if err != nil {
			h.Log.Error().Err(err).Msg("Fetching event failed")
			return
		}
		err = store.RemoveCandidate(r.Context(), h.Client, tempEv)
		if err != nil {
			h.Log.Error().Err(err).Msg("Removing candidate failed")
			return
		}
		break
	}

	return
}

func (h *Handler) GetEvent(w http.ResponseWriter, r *http.Request) {
	ev, err := store.GetEvent(r.Context(), h.Pool, chi.URLParam(r, "eventID"))
	if err != nil {
		http.Error(w, "Event not found", http.StatusNotFound)
		return
	} 

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ev)
}

func NewHandler(p *pgxpool.Pool, l zerolog.Logger, r *redis.Client) (*Handler) {
	return &Handler{p, l, r}
}

func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	err := h.Pool.Ping(r.Context())
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusServiceUnavailable)
		h.Log.Info().Err(err).Msg("Pgxpool unhealthy")
		return
	}

	err = h.Client.Ping(r.Context()).Err()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusServiceUnavailable)
		h.Log.Info().Err(err).Msg("Redis client unhealthy")
		return
	}

	w.WriteHeader(http.StatusOK)
	h.Log.Info().Msg("All services healthy")
	return
}