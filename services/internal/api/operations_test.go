package api

import (
	"testing"

	"github.com/notsliver/Ignition/services/internal/store"
)

func ptr[T any](v T) *T { return &v }

func TestValidateHealth(t *testing.T) {
	port := 3000
	ok := store.DefaultHealth
	if f := validateHealth(&ok, &port); len(f) > 0 {
		t.Fatalf("defaults should be valid: %v", f)
	}
	bad := map[string]store.HealthConfig{
		"path":            {Kind: "http", Path: "health", ExpectStatus: "200", IntervalSeconds: 10, TimeoutSeconds: 5, Retries: 3},
		"expectStatus":    {Kind: "http", Path: "/", ExpectStatus: "ok", IntervalSeconds: 10, TimeoutSeconds: 5, Retries: 3},
		"command":         {Kind: "command", IntervalSeconds: 10, TimeoutSeconds: 5, Retries: 3},
		"timeoutSeconds":  {Kind: "tcp", IntervalSeconds: 5, TimeoutSeconds: 10, Retries: 3},
		"retries":         {Kind: "tcp", IntervalSeconds: 10, TimeoutSeconds: 5, Retries: 0},
		"kind":            {Kind: "grpc", IntervalSeconds: 10, TimeoutSeconds: 5, Retries: 3},
		"intervalSeconds": {Kind: "tcp", IntervalSeconds: 1, TimeoutSeconds: 1, Retries: 3},
	}
	for field, c := range bad {
		if f := validateHealth(&c, &port); f[field] == "" {
			t.Errorf("expected an error on %s, got %v", field, f)
		}
	}
	noPort := store.HealthConfig{Kind: "tcp", IntervalSeconds: 10, TimeoutSeconds: 5, Retries: 3}
	if f := validateHealth(&noPort, nil); f["port"] == "" {
		t.Error("a TCP check needs a port when the resource has none")
	}
}

func TestTaskInput(t *testing.T) {
	task := store.Task{TimeoutSeconds: 600, Enabled: true}
	in := taskInput{Name: ptr("  Prune sessions "), Schedule: ptr("0  3 * * *"), Timezone: ptr("Europe/Amsterdam"), Command: ptr("npm run prune")}
	if f := in.apply(&task); len(f) > 0 {
		t.Fatalf("valid task rejected: %v", f)
	}
	if task.Name != "Prune sessions" || task.Schedule != "0 3 * * *" || task.NextRunAt == nil {
		t.Errorf("not normalised: %+v", task)
	}
	disabled := taskInput{Enabled: ptr(false)}
	disabled.apply(&task)
	if task.NextRunAt != nil {
		t.Error("a disabled task has no next run")
	}
	bad := store.Task{TimeoutSeconds: 5}
	f := taskInput{Name: ptr(""), Schedule: ptr("61 * * * *"), Timezone: ptr("Nowhere/Land"), Command: ptr("")}.apply(&bad)
	for _, k := range []string{"name", "schedule", "timezone", "command", "timeoutSeconds"} {
		if f[k] == "" {
			t.Errorf("expected an error on %s", k)
		}
	}
}

func TestAdvancedInput(t *testing.T) {
	good := advancedInput{Name: "api", Advanced: store.Advanced{Entrypoint: `sh -c "node server.js"`, Workdir: "/app", User: "1000:1000", Labels: map[string]string{"team": "core", "traefik.enable": "false"}}}
	if f := good.validate(); len(f) > 0 {
		t.Fatalf("valid settings rejected: %v", f)
	}
	cases := map[string]advancedInput{
		"name":       {Name: "API"},
		"entrypoint": {Name: "api", Advanced: store.Advanced{Entrypoint: `sh -c "unclosed`}},
		"workdir":    {Name: "api", Advanced: store.Advanced{Workdir: "app"}},
		"user":       {Name: "api", Advanced: store.Advanced{User: "root; rm"}},
		"labels":     {Name: "api", Advanced: store.Advanced{Labels: map[string]string{"ignition.resource": "x"}}},
	}
	for field, in := range cases {
		if f := in.validate(); f[field] == "" {
			t.Errorf("expected an error on %s, got %v", field, f)
		}
	}
}
