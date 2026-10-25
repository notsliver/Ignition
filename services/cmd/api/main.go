// Command api serves the IGNITION HTTP API (and the built dashboard when IGNITION_WEB_DIR is set).
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
	_ "time/tzdata" // schedules use IANA timezones; the image may not ship them

	"github.com/notsliver/Ignition/services/internal/api"
	"github.com/notsliver/Ignition/services/internal/config"
	"github.com/notsliver/Ignition/services/internal/database"
	"github.com/notsliver/Ignition/services/internal/proxy"
	"github.com/notsliver/Ignition/services/internal/runtime"
	"github.com/notsliver/Ignition/services/internal/secrets"
	"github.com/notsliver/Ignition/services/internal/store"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("api stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		return err
	}
	box, err := secrets.New(cfg.SecretKey)
	if err != nil {
		return err
	}
	srv := &api.Server{
		Store: &store.Store{DB: pool}, Runtime: runtime.NewDocker(cfg.DockerSocket), Secrets: box,
		PublicURL: cfg.PublicURL, SecureCookies: cfg.SecureCookies, WebDir: cfg.WebDir, PublishHost: cfg.PublishHost,
		Proxy: proxy.New(cfg.ProxySocket, cfg.ProxyAddr, cfg.ProxyHTTPPort, cfg.ProxyHTTPSPort),
	}
	store.ProxyHTTPPort, store.ProxyHTTPSPort = cfg.ProxyHTTPPort, cfg.ProxyHTTPSPort
	if err := srv.PrepareSetup(ctx); err != nil {
		return err
	}
	httpServer := &http.Server{Addr: cfg.ListenAddr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}

	errc := make(chan error, 1)
	go func() {
		slog.Info("api listening", "addr", cfg.ListenAddr)
		errc <- httpServer.ListenAndServe()
	}()
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
}
