package api

import (
	"github.com/go-chi/chi/v5"
	"net/http"
)

func NewRouter(h *Handler) http.Handler {
	r := chi.NewRouter()
	r.Post("/events", h.PostEvent)
	r.Get("/events/{eventID}", h.GetEvent)
	return r
}