package compose

import (
	"strings"
	"testing"
)

const sample = `
services:
  web:
    build:
      context: ./app
      dockerfile: Dockerfile.prod
    ports: ["8080:3000", "9229:9229"]
    environment:
      DATABASE_URL: postgres://app:${DB_PASSWORD}@db:5432/app
      NODE_ENV: production
    depends_on: [db, cache]
    deploy: { resources: { limits: { memory: 1g, cpus: "0.5" } } }
  db:
    image: postgres:16
    environment:
      - POSTGRES_PASSWORD=${DB_PASSWORD:-changeme}
    volumes:
      - pgdata:/var/lib/postgresql/data
      - ./init.sql:/docker-entrypoint-initdb.d/init.sql
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
  cache:
    image: redis:7-alpine
    command: ["redis-server", "--appendonly", "yes"]
  Queue_Worker:
    image: ghcr.io/acme/worker:1.2
    restart: "no"
    sysctls: { net.core.somaxconn: 1024 }
volumes:
  pgdata:
`

func TestParse(t *testing.T) {
	plan, err := Parse([]byte(sample), map[string]string{"DB_PASSWORD": "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Service{}
	for _, s := range plan.Services {
		by[s.Name] = s
	}
	web := by["web"]
	if web.Type != "application" || *web.Port != 3000 || web.SourceType != "git" || web.Build.Context != "./app" || web.Build.Dockerfile != "Dockerfile.prod" {
		t.Errorf("web: %+v", web)
	}
	if web.MemoryMB != 1024 || web.CPUs != 0.5 || strings.Join(web.DependsOn, ",") != "cache,db" {
		t.Errorf("web limits/deps: %+v", web)
	}
	if web.Env[0].Value != "postgres://app:s3cret@db:5432/app" {
		t.Errorf("interpolation: %q", web.Env[0].Value)
	}
	db := by["db"]
	if db.Type != "database" || db.Port != nil || len(db.Volumes) != 1 || db.Volumes[0].Name != "pgdata" || db.Health != "pg_isready -U postgres" {
		t.Errorf("db: %+v", db)
	}
	if db.Env[0].Value != "s3cret" || !db.Env[0].Secret {
		t.Errorf("db env: %+v", db.Env)
	}
	if !strings.Contains(strings.Join(db.Warnings, " "), "Bind mount ./init.sql") {
		t.Errorf("bind mounts must be reported: %v", db.Warnings)
	}
	if by["cache"].Command != "redis-server --appendonly yes" || by["cache"].Type != "database" {
		t.Errorf("cache: %+v", by["cache"])
	}
	q := by["queue-worker"]
	if q.Type != "worker" || q.Restart != "never" || !strings.Contains(strings.Join(q.Warnings, " "), "Not imported: sysctls") || !strings.Contains(q.Warnings[0], "Renamed") {
		t.Errorf("worker: %+v", q)
	}
}

func TestMissingVarsAndErrors(t *testing.T) {
	plan, err := Parse([]byte("services:\n  a:\n    image: nginx:${TAG}\n"), nil)
	if err != nil || len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], "TAG") {
		t.Errorf("missing vars should be reported: %v %v", plan, err)
	}
	for _, bad := range []string{"nope: [", "services: {}", "services:\n  A_b: {image: x}\n  a-b: {image: y}\n"} {
		if _, err := Parse([]byte(bad), nil); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestEnvFile(t *testing.T) {
	v := ParseEnvFile("# c\nexport A=1\nB=\"two words\"\nC='x'\n\nbad\n")
	if v["A"] != "1" || v["B"] != "two words" || v["C"] != "x" || len(v) != 3 {
		t.Errorf("%v", v)
	}
}
