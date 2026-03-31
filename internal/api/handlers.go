package api

import (
	"tally/internal/event"
	"tally/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"net/http"
	"encoding/json"
	"github.com/go-chi/chi/v5"
)

type Handler struct {
	Pool *pgxpool.Pool 
	Log zerolog.Logger
}

func (h *Handler) PostEvent(w http.ResponseWriter, r *http.Request) {
	var ev event.CanonicalEvent
	err := json.NewDecoder(r.Body).Decode(&ev)

	if err != nil {
		http.Error(w, "Failed to decode request", http.StatusBadRequest)
		h.Log.Info().Err(err).Msg("Failed to decode request")
		return
	}

	err = store.InsertEvent(r.Context(), h.Pool, &ev)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		h.Log.Info().Err(err).Msg("Failed to insert event")
	} else {
		w.WriteHeader(http.StatusCreated)
		h.Log.Info().Msg("Successfully inserted event")
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