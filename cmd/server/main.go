// Command server runs the song quiz game server.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/bunjonglak123/songquiz-server/internal/clock"
	"github.com/bunjonglak123/songquiz-server/internal/config"
	"github.com/bunjonglak123/songquiz-server/internal/deck"
	"github.com/bunjonglak123/songquiz-server/internal/game"
	"github.com/bunjonglak123/songquiz-server/internal/httpapi"
	"github.com/bunjonglak123/songquiz-server/internal/room"
	"github.com/bunjonglak123/songquiz-server/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var st store.Store = store.NewMemory()
	if cfg.DatabaseURL != "" {
		pg, err := store.NewPG(ctx, cfg.DatabaseURL)
		if err != nil {
			return err
		}
		st = pg
		log.Info("postgres connected")
	} else {
		log.Warn("DATABASE_URL empty: leaderboard kept in memory only")
	}
	defer st.Close()

	save := func(res game.FinalResult, code string) {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := st.SaveGame(c, store.RecordFrom(res, code)); err != nil {
			log.Error("save game", "room", code, "err", err)
		}
	}
	hub := room.NewHub(room.DefaultConfig(deck.Key(cfg.DeckSecret)), clock.Real(), save)

	srv := &http.Server{
		Addr: ":" + cfg.Port,
		Handler: httpapi.NewRouter(httpapi.Deps{
			Hub: hub, Board: st, AllowedOrigins: cfg.AllowedOrigins, Clock: clock.Real(), Log: log,
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "port", cfg.Port, "origins", cfg.AllowedOrigins)
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	log.Info("shutting down")
	hub.Close()
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(sctx)
}
