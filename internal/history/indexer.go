package history

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/hman-pro/projectlens/internal/storage"
)

// Config controls history indexing parameters.
type Config struct {
	WindowMonths         int `yaml:"window_months"`
	MinCommitsPerFile    int `yaml:"min_commits_per_file"`
	CouplingMinCoChanges int `yaml:"coupling_min_cochanges"`
	CouplingMaxFiles     int `yaml:"coupling_exclude_max_files"`
}

// maxBackfillFiles is the threshold above which per-file backfill is skipped
// to avoid slow git-log invocations for every low-activity file.
const maxBackfillFiles = 100

// IndexHistory runs the full history indexing pipeline.
func IndexHistory(ctx context.Context, db *storage.DB, repoPath string, cfg Config) error {
	startTime := time.Now()
	log.Println("── History indexing ──")

	// Step 1: Parse git log
	window := fmt.Sprintf("%d months", cfg.WindowMonths)
	log.Printf("parsing git log (window: %s)...", window)
	commits, err := ParseGitLog(repoPath, window)
	if err != nil {
		return fmt.Errorf("history: parse git log: %w", err)
	}
	log.Printf("found %d commits in window", len(commits))

	// Step 2: Load indexed file paths from DB
	files, err := db.ListFiles(ctx)
	if err != nil {
		return fmt.Errorf("history: list files: %w", err)
	}
	fileIDMap := make(map[string]int64) // path -> file DB ID
	for _, f := range files {
		fileIDMap[f.Path] = f.ID
	}
	log.Printf("loaded %d indexed files from DB", len(fileIDMap))

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
	log.Printf("filtered to %d commits with indexed files", len(filteredCommits))

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
		log.Printf("WARNING: %d files have fewer than %d commits — skipping per-file backfill (threshold: %d)",
			len(lowActivityFiles), cfg.MinCommitsPerFile, maxBackfillFiles)
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
			log.Printf("backfilled history for %d low-activity files", backfillCount)
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
	log.Printf("stored %d file_history records", historyCount)

	// Step 6: Compute coupling
	log.Println("computing co-change coupling...")
	pairs := ComputeCoupling(filteredCommits, cfg.CouplingMinCoChanges, cfg.CouplingMaxFiles)
	log.Printf("found %d coupling pairs (min %d co-changes)", len(pairs), cfg.CouplingMinCoChanges)

	// Step 7: Store coupling as edges
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

	if len(edges) > 0 {
		if err := db.InsertEdges(ctx, edges); err != nil {
			return fmt.Errorf("history: insert coupling edges: %w", err)
		}
	}
	log.Printf("stored %d co-change coupling edges", len(edges))

	log.Printf("history indexing complete (%s)", time.Since(startTime).Round(time.Millisecond))
	return nil
}
