// Package config loads settings from environment variables. Every value has a
// documented default in .env.example; missing required values fail fast.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	DatabaseURL   string
	ListenAddr    string
	SecretKey     []byte // 32 bytes, used for AES-256-GCM encryption of secrets
	DockerSocket  string
	PublicURL     string // e.g. http://localhost:8080, used for origin checks
	SecureCookies bool
	WebDir        string // built frontend, served by the API when present
	PublishHost   string // host shown in URLs for published container ports
	WorkerID      string
	Concurrency   int
	JobLease      time.Duration
}

func Load() (Config, error) {
	c := Config{
		DatabaseURL:   os.Getenv("IGNITION_DATABASE_URL"),
		ListenAddr:    env("IGNITION_LISTEN_ADDR", ":8080"),
		DockerSocket:  env("IGNITION_DOCKER_SOCKET", "/var/run/docker.sock"),
		PublicURL:     env("IGNITION_PUBLIC_URL", "http://localhost:8080"),
		SecureCookies: env("IGNITION_SECURE_COOKIES", "false") == "true",
		WebDir:        env("IGNITION_WEB_DIR", ""),
		PublishHost:   env("IGNITION_PUBLISH_HOST", "localhost"),
		WorkerID:      env("IGNITION_WORKER_ID", hostname()),
		Concurrency:   envInt("IGNITION_WORKER_CONCURRENCY", 2),
		JobLease:      time.Duration(envInt("IGNITION_JOB_LEASE_SECONDS", 60)) * time.Second,
	}
	if c.DatabaseURL == "" {
		return c, errors.New("IGNITION_DATABASE_URL is required")
	}
	raw := os.Getenv("IGNITION_SECRET_KEY")
	if raw == "" {
		return c, errors.New("IGNITION_SECRET_KEY is required (32 random bytes, base64). Generate one with: openssl rand -base64 32")
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(key) != 32 {
		return c, fmt.Errorf("IGNITION_SECRET_KEY must be 32 bytes encoded as base64")
	}
	c.SecretKey = key
	return c, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v > 0 {
		return v
	}
	return fallback
}

func hostname() string {
	h, _ := os.Hostname()
	if h == "" {
		return "worker"
	}
	return h
}
