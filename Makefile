SHELL := /bin/bash
BIN_DIR := bin
CLI := $(BIN_DIR)/lens
MCP := $(BIN_DIR)/lens-mcp
TUI := $(BIN_DIR)/lens-tui

# Load repo-root .env so recipes (and docker compose, which otherwise only sees
# docker/.env) inherit PROJECTLENS_REPO_PATH and friends. Optional: absent on
# fresh clones, where compose defaults below take over.
-include .env
export

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
.PHONY: build build-lens build-lens-mcp build-lens-tui install clean fmt vet test test-int

build: build-lens build-lens-mcp build-lens-tui ## Build all binaries into ./bin/

build-lens: ## Build the CLI (./bin/lens)
	@mkdir -p $(BIN_DIR)
	go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(CLI) ./cmd/lens/

build-lens-mcp: ## Build the MCP server (./bin/lens-mcp)
	@mkdir -p $(BIN_DIR)
	go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(MCP) ./cmd/lens-mcp/

build-lens-tui: ## Build the TUI (./bin/lens-tui)
	@mkdir -p $(BIN_DIR)
	go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(TUI) ./cmd/lens-tui/

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

tui: build-lens build-lens-tui ## Run the TUI dashboard
	./$(TUI)

mcp: build-lens-mcp ## Run the MCP server
	./$(MCP)

# Forward arbitrary args: make cli ARGS="status"
cli: build-lens ## Run the CLI (use ARGS="...")
	./$(CLI) $(ARGS)

# ────────────────────────────────────────────────────────────────────
# Indexer shortcuts (require REPO=... or PROJECTLENS_REPO_PATH env, and PROJECTLENS_DATABASE_URL)
# ────────────────────────────────────────────────────────────────────
.PHONY: bootstrap reindex reindex-full reindex-dry status query \
        index-all index-history index-datastore index-embed index-summarize \
        graph-export graph-gephi understand-dashboard

bootstrap: build-lens ## Bootstrap (init DB + full index)
	./$(CLI) bootstrap --repo "$(REPO)" --db "$(PROJECTLENS_DATABASE_URL)"

reindex: build-lens ## Incremental reindex
	./$(CLI) reindex --repo "$(REPO)" --db "$(PROJECTLENS_DATABASE_URL)"

reindex-full: build-lens ## Full reindex
	./$(CLI) reindex --full --repo "$(REPO)" --db "$(PROJECTLENS_DATABASE_URL)"

reindex-dry: build-lens ## Dry-run reindex
	./$(CLI) reindex --dry-run --repo "$(REPO)" --db "$(PROJECTLENS_DATABASE_URL)"

status: build-lens ## Show index status
	./$(CLI) status --db "$(PROJECTLENS_DATABASE_URL)"

# Use ARGS="ReserveInventory" for a custom query.
query: build-lens ## Run a retrieval query (ARGS="...")
	./$(CLI) query $(ARGS) --db "$(PROJECTLENS_DATABASE_URL)"

index-all: build-lens ## Run all indexing stages
	./$(CLI) index-all --repo "$(REPO)" --db "$(PROJECTLENS_DATABASE_URL)"

index-history: build-lens ## Index git history + coupling
	./$(CLI) index-history --repo "$(REPO)" --db "$(PROJECTLENS_DATABASE_URL)"

index-datastore: build-lens ## Index migrations + SQL
	./$(CLI) index-datastore --repo "$(REPO)" --db "$(PROJECTLENS_DATABASE_URL)"

index-embed: build-lens ## Embed missing chunks
	./$(CLI) index-embed --db "$(PROJECTLENS_DATABASE_URL)"

index-summarize: build-lens ## Summarize missing packages
	./$(CLI) index-summarize --db "$(PROJECTLENS_DATABASE_URL)"

# Graph export and conversion
GRAPH_JSON   ?= projectlens-graph.json
GRAPH_OUT    ?= projectlens.graphml
GRAPH_EDGES  ?= all
GRAPH_FORMAT ?= graphml
PYTHON       ?= python3

graph-export: build-lens ## Export graph JSON (GRAPH_JSON, GRAPH_EDGES=all|calls,implements,...)
	./$(CLI) export graph --edges $(GRAPH_EDGES) --out "$(GRAPH_JSON)" --db "$(PROJECTLENS_DATABASE_URL)"

graph-gephi: ## Convert graph JSON to GraphML/GEXF for Gephi (GRAPH_JSON, GRAPH_OUT, GRAPH_FORMAT, EDGES)
	$(PYTHON) scripts/graph_to_gephi.py "$(GRAPH_JSON)" -o "$(GRAPH_OUT)" -f $(GRAPH_FORMAT) $(if $(EDGES),-e $(EDGES))

# Understand-Anything dashboard for the architecture knowledge graph in
# .understand-anything/ (generated by the /understand Claude Code skill).
# UNDERSTAND_GLOB resolves the dashboard bundled with the installed plugin.
UNDERSTAND_GLOB ?= $(HOME)/.claude/plugins/cache/understand-anything/understand-anything/*/packages/dashboard

understand-dashboard: ## Serve the Understand-Anything knowledge-graph dashboard for this repo
	@test -f .understand-anything/knowledge-graph.json || { \
	  echo "No knowledge graph found at .understand-anything/knowledge-graph.json."; \
	  echo "Run the /understand skill in Claude Code first to generate it."; exit 1; }
	@DASH=$$(ls -d $(UNDERSTAND_GLOB) 2>/dev/null | sort -V | tail -1); \
	if [ -z "$$DASH" ]; then \
	  echo "Understand-Anything dashboard not found under ~/.claude/plugins/cache/."; \
	  echo "Install the understand-anything plugin, or set UNDERSTAND_GLOB to its packages/dashboard path."; \
	  exit 1; \
	fi; \
	echo "Serving dashboard from $$DASH (graph: $(CURDIR)/.understand-anything)"; \
	cd "$$DASH" && GRAPH_DIR="$(CURDIR)" npx vite --host 127.0.0.1

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

migrate: build-lens ## Apply pending SQL migrations (uses lens migrate)
	./$(CLI) migrate --db "$(PROJECTLENS_DATABASE_URL)"
