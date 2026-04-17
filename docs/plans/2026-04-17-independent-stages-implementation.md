# Independent Pipeline Stages (Phase 5) Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Extract embed and summarize as standalone CLI commands, and add `index all` to run every stage in sequence. This eliminates the need to run the full reindex pipeline just to embed or summarize.

**Architecture:** Each `index *` command reads from DB, does its work, writes to DB. No in-memory coupling between stages. The existing monolithic `reindex` continues to work but `index all` becomes the preferred way to run everything.

**Tech Stack:** Go 1.26, existing storage/provider interfaces.

**Design doc:** `docs/plans/2026-04-17-independent-stages-design.md`

---

### Task 1: Implement standalone `index embed` command

**Files:**
- Create: `internal/embed/embed.go`
- Modify: `cmd/projectlens/main.go`

**What it does:** Find all chunks without embeddings, embed them using the configured provider (Ollama/OpenAI).

```go
package embed

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/hman-pro/projectlens/internal/embeddings"
    "github.com/hman-pro/projectlens/internal/storage"
    "github.com/pgvector/pgvector-go"
)

// EmbedMissing finds all chunks without embeddings and embeds them.
func EmbedMissing(ctx context.Context, db *storage.DB, embedder embeddings.Embedder) error {
    startTime := time.Now()
    log.Println("── Embed missing chunks ──")

    // Query chunks that don't have embeddings yet
    // SQL: SELECT c.id, c.content FROM chunks c
    //      LEFT JOIN embeddings e ON e.chunk_id = c.id
    //      WHERE e.id IS NULL
    //      ORDER BY c.id
    unembedded, err := db.GetUnembeddedChunks(ctx)
    if err != nil {
        return fmt.Errorf("embed: get unembedded chunks: %w", err)
    }

    if len(unembedded) == 0 {
        log.Println("all chunks already have embeddings — nothing to do")
        return nil
    }

    log.Printf("found %d chunks missing embeddings", len(unembedded))

    // Prepare content for embedding
    contents := make([]string, len(unembedded))
    for i, c := range unembedded {
        contents[i] = c.Content
    }

    // Embed using the existing EmbedChunks pipeline (handles batching + truncation)
    results, err := embeddings.EmbedChunks(ctx, embedder, contents)
    if err != nil {
        return fmt.Errorf("embed: embed chunks: %w", err)
    }

    // Store embeddings
    for _, r := range results {
        chunk := unembedded[r.ChunkIndex]
        rec := &storage.EmbeddingRecord{
            ChunkID:      chunk.ID,
            ModelVersion: "embedding-model",
            Embedding:    pgvector.NewHalfVector(r.Vector),
        }
        if err := db.UpsertEmbedding(ctx, rec); err != nil {
            return fmt.Errorf("embed: upsert embedding for chunk %d: %w", chunk.ID, err)
        }
    }

    log.Printf("embedded %d chunks (%s)", len(results), time.Since(startTime).Round(time.Millisecond))
    return nil
}
```

**Storage helper needed** — add to `internal/storage/chunks.go`:
```go
// UnembeddedChunk is a chunk that has no embedding yet.
type UnembeddedChunk struct {
    ID      int64
    Content string
}

// GetUnembeddedChunks returns all chunks that don't have an embedding.
func (db *DB) GetUnembeddedChunks(ctx context.Context) ([]UnembeddedChunk, error) {
    const query = `
        SELECT c.id, c.content FROM chunks c
        LEFT JOIN embeddings e ON e.chunk_id = c.id
        WHERE e.id IS NULL
        ORDER BY c.id
    `
    // scan into []UnembeddedChunk
}
```

**CLI command:**
```go
func newIndexEmbedCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "index-embed",
        Short: "Embed all chunks that are missing embeddings",
        RunE: func(cmd *cobra.Command, args []string) error {
            ctx := context.Background()
            cfg, _, err := loadCmdConfig(cmd)
            if err != nil {
                return err
            }
            db, err := storage.Connect(ctx, cfg.DatabaseURL)
            if err != nil {
                return fmt.Errorf("connecting to database: %w", err)
            }
            defer db.Close()

            embedder, _, err := buildProviders(cfg)
            if err != nil {
                return fmt.Errorf("initializing providers: %w", err)
            }

            return embed.EmbedMissing(ctx, db, embedder)
        },
    }
}
```

Register: `rootCmd.AddCommand(newIndexEmbedCmd())`

**Commit:** `"feat: implement standalone index-embed command"`

---

### Task 2: Implement standalone `index summarize` command

**Files:**
- Create: `internal/summarize/summarize.go`
- Modify: `cmd/projectlens/main.go`

**What it does:** Find all packages without summaries, generate them using the configured provider (Claude/OpenAI).

```go
package summarize

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/hman-pro/projectlens/internal/storage"
    "github.com/hman-pro/projectlens/internal/summaries"
)

// SummarizeMissing finds packages without summaries and generates them.
func SummarizeMissing(ctx context.Context, db *storage.DB, summarizer summaries.PackageSummarizer) error {
    startTime := time.Now()
    log.Println("── Summarize missing packages ──")

    // Get all distinct package names from symbols
    // Get all existing summaries
    // Diff → packages needing summaries
    allPackages, err := db.GetDistinctPackageNames(ctx)
    if err != nil {
        return fmt.Errorf("summarize: get packages: %w", err)
    }

    existing, err := db.GetAllSummaryPackageNames(ctx)
    if err != nil {
        return fmt.Errorf("summarize: get existing summaries: %w", err)
    }

    existingSet := make(map[string]bool, len(existing))
    for _, name := range existing {
        existingSet[name] = true
    }

    var missing []string
    for _, pkg := range allPackages {
        if !existingSet[pkg] {
            missing = append(missing, pkg)
        }
    }

    if len(missing) == 0 {
        log.Println("all packages already have summaries — nothing to do")
        return nil
    }

    log.Printf("found %d packages missing summaries", len(missing))

    // For each missing package, get its symbols and summarize
    for i, pkgName := range missing {
        log.Printf("summarizing package %q (%d of %d)", pkgName, i+1, len(missing))

        syms, err := db.GetSymbolsByPackage(ctx, pkgName)
        if err != nil {
            log.Printf("warning: could not get symbols for %s: %v", pkgName, err)
            continue
        }

        // Extract exported signatures
        var sigs []string
        for _, s := range syms {
            if len(s.Name) > 0 && s.Name[0] >= 'A' && s.Name[0] <= 'Z' {
                sig := s.Signature
                if sig == "" {
                    sig = s.Kind + " " + s.Name
                }
                sigs = append(sigs, sig)
            }
        }

        if len(sigs) == 0 {
            rec := &storage.SummaryRecord{
                PackageName:  pkgName,
                SummaryText:  "Package has no exported symbols.",
                ModelVersion: "heuristic",
            }
            _ = db.UpsertSummary(ctx, rec)
            continue
        }

        summary, err := summarizer.GeneratePackageSummary(ctx, pkgName, sigs)
        if err != nil {
            log.Printf("warning: could not summarize %s: %v", pkgName, err)
            continue
        }

        rec := &storage.SummaryRecord{
            PackageName:  pkgName,
            SummaryText:  summary,
            ModelVersion: "llm",
        }
        if err := db.UpsertSummary(ctx, rec); err != nil {
            log.Printf("warning: could not store summary for %s: %v", pkgName, err)
        }
    }

    log.Printf("summarized %d packages (%s)", len(missing), time.Since(startTime).Round(time.Millisecond))
    return nil
}
```

**Storage helpers needed** — add to `internal/storage/symbols.go` and `internal/storage/summaries.go`:
```go
// GetDistinctPackageNames returns all unique package names from the symbols table.
func (db *DB) GetDistinctPackageNames(ctx context.Context) ([]string, error)

// GetAllSummaryPackageNames returns all package names that have summaries.
func (db *DB) GetAllSummaryPackageNames(ctx context.Context) ([]string, error)
```

**CLI command:** Same pattern as index-embed. Register as `newIndexSummarizeCmd()`.

**Commit:** `"feat: implement standalone index-summarize command"`

---

### Task 3: Implement `index all` command

**Files:**
- Modify: `cmd/projectlens/main.go`

**What it does:** Run all stages in order: code → datastore → history → summarize → embed.

```go
func newIndexAllCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "index-all",
        Short: "Run all indexing stages: code, datastore, history, summarize, embed",
        RunE: func(cmd *cobra.Command, args []string) error {
            ctx := context.Background()
            cfg, repoPath, err := loadCmdConfig(cmd)
            if err != nil {
                return err
            }

            db, err := storage.Connect(ctx, cfg.DatabaseURL)
            if err != nil {
                return fmt.Errorf("connecting to database: %w", err)
            }
            defer db.Close()

            embedder, summarizer, err := buildProviders(cfg)
            if err != nil {
                return fmt.Errorf("initializing providers: %w", err)
            }

            startTime := time.Now()
            log.Println("═══ Running all indexing stages ═══")

            // Stage 1: Code
            log.Println("\n═══ Stage 1: Code ═══")
            idx := indexer.New(db, embedder, summarizer, repoPath, classifier.DefaultConfig())
            full, _ := cmd.Flags().GetBool("full")
            if _, err := idx.Run(ctx, full); err != nil {
                return fmt.Errorf("code indexing: %w", err)
            }

            // Stage 2: Datastore
            log.Println("\n═══ Stage 2: Datastore ═══")
            dsCfg := datastore.Config{...} // from cfg.Datastore
            if err := datastore.IndexDatastore(ctx, db, repoPath, dsCfg); err != nil {
                log.Printf("warning: datastore indexing failed: %v", err)
                // non-fatal, continue
            }

            // Stage 3: History
            log.Println("\n═══ Stage 3: History ═══")
            hCfg := history.Config{...} // from cfg.History
            if err := history.IndexHistory(ctx, db, repoPath, hCfg); err != nil {
                log.Printf("warning: history indexing failed: %v", err)
            }

            // Stage 4: Summarize (only missing)
            log.Println("\n═══ Stage 4: Summarize ═══")
            if summarizer != nil {
                if err := summarize.SummarizeMissing(ctx, db, summarizer); err != nil {
                    log.Printf("warning: summarization failed: %v", err)
                }
            }

            // Stage 5: Embed (only missing)
            log.Println("\n═══ Stage 5: Embed ═══")
            if err := embed.EmbedMissing(ctx, db, embedder); err != nil {
                log.Printf("warning: embedding failed: %v", err)
            }

            log.Printf("\n═══ All stages complete (%s) ═══", time.Since(startTime).Round(time.Millisecond))
            return nil
        },
    }
}
```

Add `--full` flag. Register command.

Note: `index all` calls the code indexer's `Run()` which still includes embed+summarize steps internally. To avoid double-work, the `Run()` method should be updated to skip embed+summarize when called from `index all`. The simplest approach: add a `SkipEmbedSummarize` option to `Run()`, or just let the existing skip-if-exists logic handle it (summaries skip existing, embeddings upsert).

Actually, the existing logic already handles it:
- Summaries: we just added the skip-existing check
- Embeddings: the standalone `EmbedMissing` only embeds chunks without embeddings — if `Run()` already embedded them, there's nothing to do

So no changes needed to `Run()`. The stages are naturally idempotent.

**Commit:** `"feat: implement index-all command running all stages in sequence"`

---

## Task Summary

| Task | What | CLI Command |
|------|------|-------------|
| 1 | Standalone embed | `projectlens index-embed` |
| 2 | Standalone summarize | `projectlens index-summarize` |
| 3 | Run all stages | `projectlens index-all [--full]` |
