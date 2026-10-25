// Command worker executes queued jobs (deployments, lifecycle actions, removals) against Docker.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata" // schedules use IANA timezones; the image may not ship them

	"github.com/notsliver/Ignition/services/internal/config"
	"github.com/notsliver/Ignition/services/internal/database"
	"github.com/notsliver/Ignition/services/internal/jobs"
	"github.com/notsliver/Ignition/services/internal/proxy"
	"github.com/notsliver/Ignition/services/internal/runtime"
	"github.com/notsliver/Ignition/services/internal/secrets"
	"github.com/notsliver/Ignition/services/internal/store"
	"github.com/notsliver/Ignition/services/internal/worker"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("worker stopped", "error", err)
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
	docker := runtime.NewDocker(cfg.DockerSocket)
	if err := docker.Ping(ctx); err != nil {
		slog.Warn("docker is not reachable; jobs will fail and retry until it is", "socket", cfg.DockerSocket, "error", err)
	}
	w := &worker.Worker{
		ID: cfg.WorkerID, Queue: &jobs.Queue{DB: pool}, Store: &store.Store{DB: pool}, Runtime: docker,
		Secrets: box, Concurrency: cfg.Concurrency, Lease: cfg.JobLease,
		Proxy: proxy.New(cfg.ProxySocket, cfg.ProxyAddr, cfg.ProxyHTTPPort, cfg.ProxyHTTPSPort),
	}
	go w.ProxyLoop(ctx)
	slog.Info("worker started", "id", cfg.WorkerID, "concurrency", cfg.Concurrency)
	w.Run(ctx)
	slog.Info("worker stopped cleanly")
	return nil
}
