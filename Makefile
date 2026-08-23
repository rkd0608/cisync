GO      ?= go
COMPOSE ?= docker compose -f platform/docker-compose.yml

.DEFAULT_GOAL := help
help:
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk '{print $$1, $$2}'

.PHONY: build test vet lint hygiene up down logs storm test-integration keys clean

build: ## Build all Go services + web
	$(GO) build ./services/...
	cd apps/web && pnpm build

vet: ## go vet all services
	$(GO) vet ./...

test: ## unit + property tests (Go)
	$(GO) test ./... 

lint: ## golangci-lint + eslint/tsc
	golangci-lint run ./services/...
	cd apps/web && pnpm lint && pnpm tsc --noEmit

hygiene: lint ## REPO_STANDARDS gate: lint + structure check
	node scripts/check-structure.mjs

up: ## docker-compose up full stack
	$(COMPOSE) up --build -d
	@echo "web http://localhost:3000  api http://localhost:8081"

down: ## stop stack
	$(COMPOSE) down -v

logs: ## tail service logs
	$(COMPOSE) logs -f --tail=100

keys: ## generate dev ed25519 ledger key (gitignored)
	mkdir -p platform/dev-keys
	test -f platform/dev-keys/ledger_ed25519.dev.key || \
	openssl genpkey -algorithm ed25519 -out platform/dev-keys/ledger_ed25519.dev.key

storm: ## run concurrency storm against running stack
	cd tests/scenarios && pnpm exec tsx storm.ts --concurrency 500

test-integration: ## black-box compose-up suites
	cd tests/e2e && pnpm exec vitest run

clean:
	$(GO) clean -cache -testcache 2>/dev/null || true
