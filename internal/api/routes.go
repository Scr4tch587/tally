package api

import (
	"net/http"

	sentryhttp "github.com/getsentry/sentry-go/http"
	"github.com/go-chi/chi/v5"
)

func NewRouter(h *Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(sentryhttp.New(sentryhttp.Options{Repanic: true}).Handle)
	r.Post("/events", h.PostEvent)
	r.Get("/events/{eventID}", h.GetEvent)
	r.Get("/health", h.HealthCheck)
	return r
}
