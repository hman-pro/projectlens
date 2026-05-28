#!/usr/bin/env bash
# release-demo.sh — runs the public quick-start path end to end against this
# repo as the demo target. Covers Task 4.2 of the public-migration plan.
#
# Prerequisites:
#   - Ollama running on http://localhost:11434
#   - Docker available (for Postgres)
#   - Go 1.26+
#
# What it does:
#   1. Pull the embed model.
#   2. Bring up Postgres via docker compose.
#   3. Apply migrations.
#   4. Run index-all against this repo.
#   5. Build the MCP server and smoke a tools/list call.

set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_DIR"

if [[ ! -f .env ]]; then
  cp .env.example .env
fi

set -a
# shellcheck disable=SC1091
source .env
set +a

echo "== Step 1: pull embed model =="
ollama pull qwen3-embedding:0.6b

echo "== Step 2: postgres up =="
(cd docker && docker compose up -d postgres)

echo "== Step 3: migrate =="
make migrate

echo "== Step 4: index this repo =="
make index-all REPO="$REPO_DIR"

echo "== Step 5: MCP smoke =="
make build-projectlens-mcp
./bin/projectlens-mcp >/tmp/projectlens-mcp.log 2>&1 &
MCP_PID=$!
trap 'kill "$MCP_PID" 2>/dev/null || true' EXIT
sleep 2

curl -sS http://localhost:8484/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \
  | head -c 400
echo
echo "== Demo PASS =="
echo "MCP server log: /tmp/projectlens-mcp.log"
