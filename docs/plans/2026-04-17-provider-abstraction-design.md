# Provider Abstraction Design — Claude + Ollama

**Date:** 2026-04-17
**Status:** Draft
**Goal:** Replace OpenAI dependency with Ollama (local embeddings) and Claude (summarization), keeping OpenAI as a fallback option.

## Motivation

- No sustainable OpenAI API access (enterprise account is ChatGPT OAuth, not API platform)
- User has enterprise Anthropic plan — higher quality, existing access
- Local embeddings via Ollama eliminates API costs and external data transfer
- Consolidation: fewer providers, simpler operational model

## Provider Interfaces

Two interfaces, already partially defined in the codebase:

```go
// Embedder generates vector embeddings for text chunks.
type Embedder interface {
    EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

// Summarizer generates package summaries from exported symbols.
type Summarizer interface {
    GeneratePackageSummary(ctx context.Context, packageName string, exportedSymbols []string) (string, error)
}
```

## Provider Implementations

### Ollama (Embeddings)

- Package: `internal/providers/ollama/`
- Model: `mxbai-embed-large` (1024 dimensions)
- API: `POST http://localhost:11434/api/embed`
- Implements: `Embedder`
- No API key needed, runs locally

### Anthropic (Summarization)

- Package: `internal/providers/anthropic/`
- Model: `claude-sonnet-4-6`
- SDK: `github.com/anthropics/anthropic-sdk-go`
- Implements: `Summarizer`
- Requires: `ANTHROPIC_API_KEY`

### OpenAI (Fallback — Both)

- Package: `internal/providers/openai/` (move from `internal/openai/`)
- Models: `text-embedding-3-large` (3072-dim), `gpt-4o-mini` (summaries)
- Implements: `Embedder` + `Summarizer`
- Requires: `OPENAI_API_KEY`

## Configuration

```yaml
embeddings:
  provider: ollama              # ollama | openai
  model: mxbai-embed-large
  dimensions: 1024
  endpoint: http://localhost:11434

summarization:
  provider: anthropic           # anthropic | openai
  model: claude-sonnet-4-6
```

### Environment Variables

| Variable | When Required |
|----------|--------------|
| `ANTHROPIC_API_KEY` | summarization.provider = anthropic |
| `OPENAI_API_KEY` | either provider = openai |
| `OLLAMA_ENDPOINT` | optional override (default: http://localhost:11434) |

## Migration 003 — Vector Dimension Change

Changing from `halfvec(3072)` (OpenAI) to `halfvec(1024)` (Ollama mxbai-embed-large).

```sql
-- 003_vector_dimensions.up.sql
DROP INDEX IF EXISTS idx_embeddings_hnsw;
TRUNCATE embeddings;
ALTER TABLE embeddings ALTER COLUMN embedding TYPE halfvec(1024);
CREATE INDEX idx_embeddings_hnsw ON embeddings
  USING hnsw (embedding halfvec_cosine_ops) WITH (m = 16, ef_construction = 64);
```

```sql
-- 003_vector_dimensions.down.sql
DROP INDEX IF EXISTS idx_embeddings_hnsw;
TRUNCATE embeddings;
ALTER TABLE embeddings ALTER COLUMN embedding TYPE halfvec(3072);
CREATE INDEX idx_embeddings_hnsw ON embeddings
  USING hnsw (embedding halfvec_cosine_ops) WITH (m = 16, ef_construction = 64);
```

Existing 3072-dim embeddings are incompatible — TRUNCATE is required. Re-embed via `index embed` after migration.

## Provider Resolution

At startup, the indexer reads config and constructs the appropriate provider:

```
provider = config.Embeddings.Provider
switch provider {
case "ollama":
    embedder = ollama.NewClient(endpoint, model)
    // ping to verify Ollama is running
case "openai":
    embedder = openai.NewClient(apiKey)
default:
    error: unknown embedding provider
}
```

Same pattern for summarization provider.

## Performance Comparison

| | OpenAI 3072-dim | Ollama mxbai 1024-dim |
|---|---|---|
| Quality | ~2-5% better on benchmarks | Very good for code search |
| Storage | 6KB/vector | 2KB/vector |
| Index size (23K vectors) | ~140MB | ~47MB |
| Search speed | Baseline | ~3x faster |
| Cost | $0.13/1M tokens | Free (local) |
| Latency | Network round-trip | Local, ~100 chunks/sec |

## Design Decisions

- **1024-dim via halfvec** — accommodates mxbai-embed-large exactly. If switching to a smaller model (768-dim), padding to 1024 is trivial.
- **Provider interfaces, not abstractions** — no generic "Provider" type. Two focused interfaces (Embedder, Summarizer) that each provider implements independently.
- **Config-driven selection** — no runtime auto-detection. Explicit provider choice in YAML.
- **OpenAI as fallback** — kept for users who have API access or for future use. Switching requires re-embedding (dimension change).
