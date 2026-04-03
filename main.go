package main

import (
	"tally/internal/store"
	"context"
	"tally/internal/logger"
	"tally/internal/api"
	"net/http"
	"time"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	log := logger.New()
	log.Info().Msg("Tally Ready")
	ctx := context.Background()
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
	h := api.NewHandler(pool, log, client)
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
	log.Info().Msg("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatal().Err(err).Msg("shutdown failed")
	}
	log.Info().Msg("server stopped")
}