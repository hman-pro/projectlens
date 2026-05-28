#!/usr/bin/env bash
# release-smoke.sh — verifies the 1024-dim local-embedding contract end to end.
# Covers Task 1.10 of the public-migration plan.
#
# Prerequisites:
#   - Ollama running on http://localhost:11434
#   - Postgres-capable Docker available
#   - PROJECTLENS_DATABASE_URL set (or .env present)
#
# What it does:
#   1. Pull qwen3-embedding:0.6b.
#   2. Embed one text via the local provider, assert len == 1024.
#   3. Round-trip a halfvec(1024) row through Postgres.
#
# Exit codes:
#   0 = pass, 1 = ollama/embed failure, 2 = wrong vector length, 3 = DB failure.

set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_DIR"

if [[ -f .env ]]; then
  set -a
  # shellcheck disable=SC1091
  source .env
  set +a
fi

: "${PROJECTLENS_DATABASE_URL:?must be set, e.g. via .env}"

echo "== Step 1: pull model =="
ollama pull qwen3-embedding:0.6b

echo "== Step 2: embed via Go client =="
SMOKE_DIR="$(mktemp -d)"
trap 'rm -rf "$SMOKE_DIR"' EXIT

cat >"$SMOKE_DIR/smoke_embed.go" <<'GO'
//go:build ignore

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/hman-pro/projectlens/internal/providers/ollama"
)

func main() {
	c := ollama.NewClient("http://localhost:11434", "qwen3-embedding:0.6b", 1024)
	vecs, err := c.EmbedBatch(context.Background(), []string{"hello world"})
	if err != nil {
		fmt.Println("ERR:", err)
		os.Exit(1)
	}
	fmt.Println("len=", len(vecs[0]))
	if len(vecs[0]) != 1024 {
		os.Exit(2)
	}
}
GO

go run -tags ignore "$SMOKE_DIR/smoke_embed.go"

echo "== Step 3: halfvec(1024) round-trip =="
psql "$PROJECTLENS_DATABASE_URL" -c "
CREATE TABLE IF NOT EXISTS smoke (id int primary key, v halfvec(1024));
INSERT INTO smoke (id, v)
  SELECT 1, array_fill(0.001::real, ARRAY[1024])::halfvec
  ON CONFLICT DO NOTHING;
SELECT id, vector_dims(v::vector) FROM smoke;
DROP TABLE smoke;
" >/dev/null || { echo "DB round-trip failed"; exit 3; }

echo "== Smoke PASS =="
