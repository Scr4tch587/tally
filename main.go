package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"tally/internal/api"
	"tally/internal/logger"
	"tally/internal/observe"
	"tally/internal/reconcile"
	"tally/internal/store"
	"time"
)

func main() {
	sentryEnabled, sentryErr := observe.InitSentry()
	defer observe.FlushSentry()
	log := logger.New()
	log.Info().Msg("Tally Ready")
	if sentryErr != nil {
		log.Error().Err(sentryErr).Msg("sentry init failed")
	}
	if sentryEnabled {
		log.Info().Msg("sentry error monitoring enabled")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool, err := store.Connect(ctx)
	if err != nil {
		log.Fatal().Err(err).Msg("postgres connection failed")
		return
	}
	defer pool.Close()

	err = pool.Ping(ctx)
	if err != nil {
		log.Fatal().Err(err).Msg("postgres ping failed")
		return
	}

	log.Info().Msg("postgres ok")
	client := store.NewRedisClient()
	engine := reconcile.NewEngine(pool, log, client)
	go reconcile.StartPendingWorker(ctx, engine, 250*time.Millisecond, 500)
	h := api.NewHandler(pool, log, client, engine)
	r := api.NewRouter(h)
	log.Info().Msg("server listening on :8080")

	srv := &http.Server{Addr: ":8080", Handler: r}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("server error")
		}
	}()

	<-quit
	cancel()
	log.Info().Msg("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatal().Err(err).Msg("shutdown failed")
	}
	log.Info().Msg("server stopped")
}
