SHELL := /bin/bash
BIN_DIR := bin
CLI := $(BIN_DIR)/projectlens
MCP := $(BIN_DIR)/projectlens-mcp
TUI := $(BIN_DIR)/projectlens-tui

DB_URL ?= postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable
REPO ?= $(REPO_PATH)
GOFLAGS ?=
LDFLAGS ?=

# Pass extra arguments to a target like: make cli ARGS="status"
ARGS ?=

.DEFAULT_GOAL := help

# ────────────────────────────────────────────────────────────────────
# Help
# ────────────────────────────────────────────────────────────────────
.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "; printf "Targets:\n"} /^[a-zA-Z0-9_-]+:.*?## / {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# ────────────────────────────────────────────────────────────────────
# Build (always re-runs go build; Go's own cache makes it cheap)
# ────────────────────────────────────────────────────────────────────
.PHONY: build build-cli build-mcp build-tui install clean fmt vet test test-int

build: build-cli build-mcp build-tui ## Build all binaries into ./bin/

build-cli: ## Build the CLI (./bin/projectlens)
	@mkdir -p $(BIN_DIR)
	go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(CLI) ./cmd/projectlens/

build-mcp: ## Build the MCP server (./bin/projectlens-mcp)
	@mkdir -p $(BIN_DIR)
	go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(MCP) ./cmd/projectlens-mcp/

build-tui: ## Build the TUI (./bin/projectlens-tui)
	@mkdir -p $(BIN_DIR)
	go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(TUI) ./cmd/projectlens-tui/

install: ## go install all commands into $$GOBIN
	go install $(GOFLAGS) -ldflags '$(LDFLAGS)' ./cmd/...

clean: ## Remove ./bin and Go test caches
	rm -rf $(BIN_DIR)
	go clean -testcache

fmt: ## go fmt
	go fmt ./...

vet: ## go vet
	go vet ./...

test: ## Run unit tests
	go test ./...

test-int: ## Run integration tests (requires DB)
	go test -tags integration ./...

# ────────────────────────────────────────────────────────────────────
# Run binaries (always builds first, then execs from ./bin/)
# ────────────────────────────────────────────────────────────────────
.PHONY: tui mcp cli

tui: build-cli build-tui ## Run the TUI dashboard
	./$(TUI)

mcp: build-mcp ## Run the MCP server
	./$(MCP)

# Forward arbitrary args: make cli ARGS="status"
cli: build-cli ## Run the CLI (use ARGS="...")
	./$(CLI) $(ARGS)

# ────────────────────────────────────────────────────────────────────
# Indexer shortcuts (require REPO=... or REPO_PATH env, and DB_URL)
# ────────────────────────────────────────────────────────────────────
.PHONY: bootstrap reindex reindex-full reindex-dry status query \
        index-all index-history index-datastore index-embed index-summarize

bootstrap: build-cli ## Bootstrap (init DB + full index)
	./$(CLI) bootstrap --repo "$(REPO)" --db "$(DB_URL)"

reindex: build-cli ## Incremental reindex
	./$(CLI) reindex --repo "$(REPO)" --db "$(DB_URL)"

reindex-full: build-cli ## Full reindex
	./$(CLI) reindex --full --repo "$(REPO)" --db "$(DB_URL)"

reindex-dry: build-cli ## Dry-run reindex
	./$(CLI) reindex --dry-run --repo "$(REPO)" --db "$(DB_URL)"

status: build-cli ## Show index status
	./$(CLI) status --db "$(DB_URL)"

# Use ARGS="ReserveInventory" for a custom query.
query: build-cli ## Run a retrieval query (ARGS="...")
	./$(CLI) query $(ARGS) --db "$(DB_URL)"

index-all: build-cli ## Run all indexing stages
	./$(CLI) index-all --repo "$(REPO)" --db "$(DB_URL)"

index-history: build-cli ## Index git history + coupling
	./$(CLI) index-history --repo "$(REPO)" --db "$(DB_URL)"

index-datastore: build-cli ## Index migrations + SQL
	./$(CLI) index-datastore --repo "$(REPO)" --db "$(DB_URL)"

index-embed: build-cli ## Embed missing chunks
	./$(CLI) index-embed --db "$(DB_URL)"

index-summarize: build-cli ## Summarize missing packages
	./$(CLI) index-summarize --db "$(DB_URL)"

# ────────────────────────────────────────────────────────────────────
# Docker
# ────────────────────────────────────────────────────────────────────
.PHONY: docker-up docker-down docker-logs docker-build docker-rebuild docker-clean docker-index

docker-up: ## docker compose up -d (postgres + mcp)
	cd docker && docker compose up -d

docker-down: ## docker compose down
	cd docker && docker compose down

docker-logs: ## Tail docker logs
	cd docker && docker compose logs -f

docker-build: ## Build docker images
	cd docker && docker compose build

docker-rebuild: docker-down docker-build docker-up ## Rebuild + restart containers

docker-clean: ## Stop and delete volumes (DESTRUCTIVE)
	cd docker && docker compose down -v

docker-index: ## Run indexer profile container on demand
	cd docker && docker compose --profile index run --rm projectlens-indexer

# ────────────────────────────────────────────────────────────────────
# Migrations (manual; bootstrap applies them automatically)
# ────────────────────────────────────────────────────────────────────
.PHONY: migrate

migrate: ## Apply all SQL migrations against $$DB_URL
	@for f in migrations/*.up.sql; do \
		echo "→ applying $$f"; \
		psql "$(DB_URL)" -f "$$f" || exit 1; \
	done
