SHELL := /bin/bash
BIN_DIR := bin
CLI := $(BIN_DIR)/projectlens
MCP := $(BIN_DIR)/projectlens-mcp
TUI := $(BIN_DIR)/projectlens-tui

# Single source of truth for the database URL. PROJECTLENS_DATABASE_URL from
# .env wins; otherwise fall back to the local-defaults compose URL.
PROJECTLENS_DATABASE_URL ?= postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable
REPO ?= $(PROJECTLENS_REPO_PATH)
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
.PHONY: build build-projectlens build-projectlens-mcp build-projectlens-tui install clean fmt vet test test-int

build: build-projectlens build-projectlens-mcp build-projectlens-tui ## Build all binaries into ./bin/

build-projectlens: ## Build the CLI (./bin/projectlens)
	@mkdir -p $(BIN_DIR)
	go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(CLI) ./cmd/projectlens/

build-projectlens-mcp: ## Build the MCP server (./bin/projectlens-mcp)
	@mkdir -p $(BIN_DIR)
	go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(MCP) ./cmd/projectlens-mcp/

build-projectlens-tui: ## Build the TUI (./bin/projectlens-tui)
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

tui: build-projectlens build-projectlens-tui ## Run the TUI dashboard
	./$(TUI)

mcp: build-projectlens-mcp ## Run the MCP server
	./$(MCP)

# Forward arbitrary args: make cli ARGS="status"
cli: build-projectlens ## Run the CLI (use ARGS="...")
	./$(CLI) $(ARGS)

# ────────────────────────────────────────────────────────────────────
# Indexer shortcuts (require REPO=... or PROJECTLENS_REPO_PATH env, and PROJECTLENS_DATABASE_URL)
# ────────────────────────────────────────────────────────────────────
.PHONY: bootstrap reindex reindex-full reindex-dry status query \
        index-all index-history index-datastore index-embed index-summarize \
        graph-export graph-gephi

bootstrap: build-projectlens ## Bootstrap (init DB + full index)
	./$(CLI) bootstrap --repo "$(REPO)" --db "$(PROJECTLENS_DATABASE_URL)"

reindex: build-projectlens ## Incremental reindex
	./$(CLI) reindex --repo "$(REPO)" --db "$(PROJECTLENS_DATABASE_URL)"

reindex-full: build-projectlens ## Full reindex
	./$(CLI) reindex --full --repo "$(REPO)" --db "$(PROJECTLENS_DATABASE_URL)"

reindex-dry: build-projectlens ## Dry-run reindex
	./$(CLI) reindex --dry-run --repo "$(REPO)" --db "$(PROJECTLENS_DATABASE_URL)"

status: build-projectlens ## Show index status
	./$(CLI) status --db "$(PROJECTLENS_DATABASE_URL)"

# Use ARGS="ReserveInventory" for a custom query.
query: build-projectlens ## Run a retrieval query (ARGS="...")
	./$(CLI) query $(ARGS) --db "$(PROJECTLENS_DATABASE_URL)"

index-all: build-projectlens ## Run all indexing stages
	./$(CLI) index-all --repo "$(REPO)" --db "$(PROJECTLENS_DATABASE_URL)"

index-history: build-projectlens ## Index git history + coupling
	./$(CLI) index-history --repo "$(REPO)" --db "$(PROJECTLENS_DATABASE_URL)"

index-datastore: build-projectlens ## Index migrations + SQL
	./$(CLI) index-datastore --repo "$(REPO)" --db "$(PROJECTLENS_DATABASE_URL)"

index-embed: build-projectlens ## Embed missing chunks
	./$(CLI) index-embed --db "$(PROJECTLENS_DATABASE_URL)"

index-summarize: build-projectlens ## Summarize missing packages
	./$(CLI) index-summarize --db "$(PROJECTLENS_DATABASE_URL)"

# Graph export and conversion
GRAPH_JSON   ?= projectlens-graph.json
GRAPH_OUT    ?= projectlens.graphml
GRAPH_EDGES  ?= all
GRAPH_FORMAT ?= graphml
PYTHON       ?= python3

graph-export: build-projectlens ## Export graph JSON (GRAPH_JSON, GRAPH_EDGES=all|calls,implements,...)
	./$(CLI) export graph --edges $(GRAPH_EDGES) --out "$(GRAPH_JSON)" --db "$(PROJECTLENS_DATABASE_URL)"

graph-gephi: ## Convert graph JSON to GraphML/GEXF for Gephi (GRAPH_JSON, GRAPH_OUT, GRAPH_FORMAT, EDGES)
	$(PYTHON) scripts/graph_to_gephi.py "$(GRAPH_JSON)" -o "$(GRAPH_OUT)" -f $(GRAPH_FORMAT) $(if $(EDGES),-e $(EDGES))

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

migrate: build-projectlens ## Apply pending SQL migrations (uses projectlens migrate)
	./$(CLI) migrate --db "$(PROJECTLENS_DATABASE_URL)"
