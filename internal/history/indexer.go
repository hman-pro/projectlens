package history

import (
	"context"
	"fmt"
	"time"

	"github.com/hman-pro/projectlens/internal/logger"
	"github.com/hman-pro/projectlens/internal/storage"
)

// incrementalSafetyMargin is subtracted from the last indexed file_history
// timestamp when computing the git --since value for incremental runs. It
// protects against missing commits whose recorded committer date is slightly
// earlier than the one we already stored (e.g., different files in the same
// commit recorded at marginally different wall-clock moments, or a rebase
// shifting dates by a few minutes). Widen cautiously: values above ~1 hour
// re-parse a meaningful amount of work on every incremental run, while
// full reindex remains the remedy for rebases that rewrite history beyond
// this margin.
const incrementalSafetyMargin = 5 * time.Minute

// Config controls history indexing parameters.
type Config struct {
	WindowMonths         int  `yaml:"window_months"`
	MinCommitsPerFile    int  `yaml:"min_commits_per_file"`
	CouplingMinCoChanges int  `yaml:"coupling_min_cochanges"`
	CouplingMaxFiles     int  `yaml:"coupling_exclude_max_files"`
	FullReindex          bool `yaml:"-"` // CLI-only; not persisted
}

// maxBackfillFiles is the threshold above which per-file backfill is skipped
// to avoid slow git-log invocations for every low-activity file.
const maxBackfillFiles = 100

// IndexHistory runs the full history indexing pipeline.
func IndexHistory(ctx context.Context, db *storage.DB, repoPath string, cfg Config) error {
	startTime := time.Now()
	logger.Step("History indexing")

	// Step 1: Parse git log.
	// Incremental by default: if file_history already has rows, pass
	// git's --since the latest observed committed_at (minus a small buffer
	// to handle late-arriving commits with the same or slightly-earlier ts).
	// With FullReindex, fall back to the configured WindowMonths window.
	window := fmt.Sprintf("%d months", cfg.WindowMonths)
	if !cfg.FullReindex {
		last, ok, err := db.GetLatestFileHistoryTimestamp(ctx)
		if err != nil {
			return fmt.Errorf("history: latest timestamp: %w", err)
		}
		if ok {
			since := last.Add(-incrementalSafetyMargin).UTC().Format(time.RFC3339)
			window = since
			logger.Info("parsing git log (incremental)", "since", since)
		} else {
			logger.Info("parsing git log (no prior history, full window)", "window", window)
		}
	} else {
		logger.Info("parsing git log (full reindex)", "window", window)
	}
	commits, err := ParseGitLog(repoPath, window)
	if err != nil {
		return fmt.Errorf("history: parse git log: %w", err)
	}
	logger.Info("found commits in window", "count", len(commits))

	// Step 2: Load indexed file paths from DB
	files, err := db.ListFiles(ctx)
	if err != nil {
		return fmt.Errorf("history: list files: %w", err)
	}
	fileIDMap := make(map[string]int64) // path -> file DB ID
	for _, f := range files {
		fileIDMap[f.Path] = f.ID
	}
	logger.Info("loaded indexed files from DB", "count", len(fileIDMap))

	// Step 3: Filter commits to only indexed files
	var filteredCommits []Commit
	for _, c := range commits {
		var indexedFiles []string
		for _, f := range c.Files {
			if _, ok := fileIDMap[f]; ok {
				indexedFiles = append(indexedFiles, f)
			}
		}
		if len(indexedFiles) > 0 {
			fc := c
			fc.Files = indexedFiles
			filteredCommits = append(filteredCommits, fc)
		}
	}
	logger.Info("filtered commits with indexed files", "count", len(filteredCommits))

	// Step 4: Count commits per file, backfill files with < minCommits
	fileCommitCount := make(map[string]int)
	for _, c := range filteredCommits {
		for _, f := range c.Files {
			fileCommitCount[f]++
		}
	}

	var lowActivityFiles []string
	for path := range fileIDMap {
		if fileCommitCount[path] < cfg.MinCommitsPerFile {
			lowActivityFiles = append(lowActivityFiles, path)
		}
	}

	if len(lowActivityFiles) > maxBackfillFiles {
		logger.Warn("skipping per-file backfill",
			"low_activity_files", len(lowActivityFiles),
			"min_commits", cfg.MinCommitsPerFile,
			"threshold", maxBackfillFiles)
	} else if len(lowActivityFiles) > 0 {
		backfillCount := 0
		for _, path := range lowActivityFiles {
			extra, err := ParseGitLogForFile(repoPath, path, cfg.MinCommitsPerFile)
			if err != nil {
				continue // not fatal
			}
			for _, c := range extra {
				// Deduplicate: check if we already have this commit for this file
				alreadyHave := false
				for _, existing := range filteredCommits {
					if existing.Hash == c.Hash {
						alreadyHave = true
						break
					}
				}
				if !alreadyHave {
					c.Files = []string{path}
					filteredCommits = append(filteredCommits, c)
				}
			}
			backfillCount++
		}
		if backfillCount > 0 {
			logger.Info("backfilled history for low-activity files", "count", backfillCount)
		}
	}

	// Step 5: Store file_history records
	historyCount := 0
	for _, c := range filteredCommits {
		for _, f := range c.Files {
			fid, ok := fileIDMap[f]
			if !ok {
				continue
			}
			msg := c.Message
			rec := &storage.FileHistoryRecord{
				FileID:      fid,
				CommitHash:  c.Hash,
				Author:      c.Author,
				CommittedAt: time.Unix(c.Timestamp, 0),
				ChangeType:  "modified",
				DiffSnippet: &msg, // store commit message, not full diff
			}
			if err := db.InsertFileHistory(ctx, rec); err != nil {
				continue // ON CONFLICT DO NOTHING handles duplicates
			}
			historyCount++
		}
	}
	logger.Info("stored file_history records", "count", historyCount)

	// Step 6: Compute coupling over the full WindowMonths window from DB state.
	// Sourcing from the DB (rather than just the commits we parsed this run)
	// means incremental runs still compute coupling over the whole window,
	// and the result is deterministic with respect to accumulated history.
	logger.Info("computing co-change coupling from DB...")
	windowCommits, err := db.ListCommitsInWindow(ctx, cfg.WindowMonths)
	if err != nil {
		return fmt.Errorf("history: list commits in window: %w", err)
	}
	adapted := make([]Commit, len(windowCommits))
	for i, w := range windowCommits {
		adapted[i] = Commit{Hash: w.Hash, Timestamp: w.Timestamp.Unix(), Files: w.Files}
	}
	pairs := ComputeCoupling(adapted, cfg.CouplingMinCoChanges, cfg.CouplingMaxFiles)
	logger.Info("found coupling pairs", "count", len(pairs), "min_co_changes", cfg.CouplingMinCoChanges)

	// Step 7: Store coupling as edges. Clear the existing set first so the
	// result reflects the latest window exactly (no accumulation of stale
	// pairs that fell out of the window or were invalidated by file renames).
	var edges []storage.EdgeRecord
	for _, p := range pairs {
		fidA, okA := fileIDMap[p.FileA]
		fidB, okB := fileIDMap[p.FileB]
		if !okA || !okB {
			continue
		}
		edges = append(edges, storage.EdgeRecord{
			SourceType: "file",
			SourceID:   fidA,
			TargetType: "file",
			TargetID:   fidB,
			EdgeType:   "co_changes",
		})
	}

	// NOTE: this delete+insert pair is intentionally non-transactional. On partial
	// failure (delete succeeds, insert fails) the DB is left with no coupling edges
	// until the next successful run. For an offline indexer this is acceptable;
	// if consumers of coupling edges ever become latency-sensitive, wrap Steps 6-7
	// in a pgx.BeginFunc transaction.
	removed, err := db.DeleteEdgesByType(ctx, "file", "file", "co_changes")
	if err != nil {
		return fmt.Errorf("history: clear coupling edges: %w", err)
	}
	logger.Info("cleared stale coupling edges", "count", removed)

	if len(edges) > 0 {
		if err := db.InsertEdges(ctx, edges); err != nil {
			return fmt.Errorf("history: insert coupling edges: %w", err)
		}
	}
	logger.Info("stored co-change coupling edges", "count", len(edges))

	logger.Info("history indexing complete", "elapsed", time.Since(startTime).Round(time.Millisecond))
	return nil
}
