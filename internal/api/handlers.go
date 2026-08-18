package api

import (
	"encoding/json"
	"github.com/getsentry/sentry-go"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"net/http"
	"tally/internal/event"
	"tally/internal/reconcile"
	"tally/internal/store"
	"time"
)

type Handler struct {
	Pool   *pgxpool.Pool
	Log    zerolog.Logger
	Client *redis.Client
	rec    *reconcile.Engine
}

type PostEventRequest struct {
	TenantID        string
	EventID         string
	SourceType      string
	SourceEventID   string
	AmountMinor     int64
	AssetCode       string
	Currency        string
	Timestamp       time.Time
	Direction       string
	AccountRef      string
	CounterpartyRef string
	Metadata        map[string]string
}

func (h *Handler) PostEvent(w http.ResponseWriter, r *http.Request) {
	var req PostEventRequest
	err := json.NewDecoder(r.Body).Decode(&req)

	if err != nil {
		http.Error(w, "Failed to decode request", http.StatusBadRequest)
		h.Log.Info().Err(err).Msg("Failed to decode request")
		return
	}

	if hub := sentry.GetHubFromContext(r.Context()); hub != nil {
		hub.Scope().SetTag("tenant_id", req.TenantID)
		hub.Scope().SetTag("source_type", req.SourceType)
		hub.Scope().SetContext("ingestion", sentry.Context{"event_id": req.EventID, "source_event_id": req.SourceEventID})
	}

	ev, err := event.NewCanonicalEvent(req.TenantID, req.EventID, req.SourceType, req.SourceEventID, req.AmountMinor, req.AssetCode, req.Currency, req.Timestamp, req.Direction, req.AccountRef, req.CounterpartyRef, req.Metadata)
	if err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		h.Log.Info().
			Err(err).
			Str("tenant_id", req.TenantID).
			Str("event_id", req.EventID).
			Str("source_type", req.SourceType).
			Str("source_event_id", req.SourceEventID).
			Msg("invalid canonical event request")
		return
	}

	notSeen, err := store.InsertEvent(r.Context(), h.Pool, ev)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		h.Log.Error().Err(err).Msg("Failed to insert event")
		return
	}

	if !notSeen {
		w.WriteHeader(http.StatusOK)
		h.Log.Info().Msg("Event already exists")
		return
	}

	w.WriteHeader(http.StatusCreated)
	h.Log.Info().Msg("Successfully inserted event")

	err = store.AddCandidate(r.Context(), h.Client, ev)
	if err != nil {
		h.Log.Error().Err(err).Msg("Adding event failed")
		return
	}
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

func NewHandler(p *pgxpool.Pool, l zerolog.Logger, r *redis.Client, rec *reconcile.Engine) *Handler {
	return &Handler{Pool: p, Log: l, Client: r, rec: rec}
}

func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	err := h.Pool.Ping(r.Context())
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusServiceUnavailable)
		h.Log.Error().Err(err).Msg("Pgxpool unhealthy")
		return
	}

	err = h.Client.Ping(r.Context()).Err()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusServiceUnavailable)
		h.Log.Error().Err(err).Msg("Redis client unhealthy")
		return
	}

	w.WriteHeader(http.StatusOK)
	h.Log.Info().Msg("All services healthy")
	return
}
