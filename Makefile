# IGNITION tasks. Everything runs in Docker, so only Docker (and make) is needed on your machine.
COMPOSE = docker compose --env-file .env -f deploy/docker-compose.yml
GO      = docker run --rm -v "$(CURDIR):/repo" -v ignition-gomod:/go/pkg/mod -v ignition-gocache:/root/.cache/go-build -w /repo/services golang:1.23-alpine

install: ## Set up and start everything (same as ./install.sh)
	./install.sh

.PHONY: install env up down logs setup-code test test-go test-db-up test-db-down e2e web-dev build fmt

env: ## Create .env with random secrets (won't overwrite an existing one)
	@test -f .env && echo ".env already exists" || (cp .env.example .env && \
	  sed -i "s|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD=$$(openssl rand -hex 24)|; s|^IGNITION_SECRET_KEY=.*|IGNITION_SECRET_KEY=$$(openssl rand -base64 32)|" .env && \
	  echo "Created .env. Back up IGNITION_SECRET_KEY: without it, stored secrets can't be decrypted.")

up: ## Build and start IGNITION on http://localhost:8080
	$(COMPOSE) up -d --build

down: ## Stop IGNITION (data is kept)
	$(COMPOSE) down

logs: ## Follow API and worker logs
	$(COMPOSE) logs -f api worker

setup-code: ## Print the one-time code for first-run setup
	@$(COMPOSE) logs api | grep -o '"setup_code":"[A-F0-9]*"' | tail -1 | cut -d'"' -f4

fmt: ## Format Go code
	$(GO) gofmt -w .

test: test-db-up test-go test-db-down ## Go unit + integration tests against a throwaway Postgres

test-db-up:
	-docker network create ignition-test >/dev/null 2>&1
	-docker rm -f ignition-test-db >/dev/null 2>&1
	docker run -d --name ignition-test-db --network ignition-test -e POSTGRES_PASSWORD=test -e POSTGRES_DB=ignition_test postgres:16-alpine >/dev/null
	sleep 3

test-go:
	docker run --rm --network ignition-test -v "$(CURDIR):/repo" -v ignition-gomod:/go/pkg/mod -v ignition-gocache:/root/.cache/go-build -w /repo/services \
	  -e IGNITION_TEST_DATABASE_URL="postgres://postgres:test@ignition-test-db:5432/ignition_test?sslmode=disable" \
	  golang:1.23-alpine sh -c 'test -z "$$(gofmt -l .)" || (gofmt -l . && echo "run make fmt" && exit 1); go vet ./... && go test -count=1 ./...'

test-db-down:
	-docker rm -f ignition-test-db >/dev/null 2>&1

e2e: ## Browser end-to-end test against the running stack. Needs IGNITION_EMAIL and IGNITION_PASSWORD.
	docker run --rm --network ignition_default -v "$(CURDIR)/e2e:/e2e" -v "$(CURDIR)/e2e/screenshots:/shots" -v ignition-e2e-npm:/root/.npm -w /work \
	  -e IGNITION_URL=http://api:8080 -e IGNITION_EMAIL -e IGNITION_PASSWORD \
	  mcr.microsoft.com/playwright:v1.48.2-jammy sh -c "npm init -y >/dev/null && npm i playwright@1.48.2 --silent && cp /e2e/full-flow.mjs . && node full-flow.mjs"

web-dev: ## Run the dashboard with hot reload on :5173 (API must be running on :8080)
	cd apps/web && npm install && npm run dev

build: ## Build the production image
	docker build -t notsliver/ignition:local .
