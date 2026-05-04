// Package indexer orchestrates the full indexing pipeline: census, parsing,
// chunking, graph building, summarization, and embedding.
package indexer

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hman-pro/projectlens/internal/census"
	"github.com/hman-pro/projectlens/internal/chunks"
	"github.com/hman-pro/projectlens/internal/classifier"
	"github.com/hman-pro/projectlens/internal/embeddings"
	"github.com/hman-pro/projectlens/internal/graph"
	"github.com/hman-pro/projectlens/internal/logger"
	"github.com/hman-pro/projectlens/internal/parser"
	"github.com/hman-pro/projectlens/internal/storage"
	"github.com/hman-pro/projectlens/internal/summaries"
	"github.com/pgvector/pgvector-go"
)

// Indexer ties together all pipeline stages and stores results in the database.
type Indexer struct {
	db         *storage.DB
	embedder   embeddings.Embedder
	summarizer summaries.PackageSummarizer
	repo       string // path to the target repo
	cfg        classifier.Config
}

// Stats records what the pipeline produced.
type Stats struct {
	FilesProcessed     int
	SymbolsExtracted   int
	ChunksCreated      int
	EdgesCreated       int
	PackagesSummarized int
	ChunksEmbedded     int
}

// New creates an Indexer. The embedder and summarizer may be nil if embedding
// and summarization should be skipped (e.g. dry-run or tests).
func New(db *storage.DB, embedder embeddings.Embedder, summarizer summaries.PackageSummarizer, repoPath string, cfg classifier.Config) *Indexer {
	return &Indexer{
		db:         db,
		embedder:   embedder,
		summarizer: summarizer,
		repo:       repoPath,
		cfg:        cfg,
	}
}

// workItem tracks a census file entry together with the reason it was selected.
type workItem struct {
	entry census.FileEntry
	isNew bool
}

// Run executes the full indexing pipeline. When full is true every handwritten
// file is processed; otherwise only new or changed files are included.
func (idx *Indexer) Run(ctx context.Context, full bool) (*Stats, error) {
	pipelineStart := time.Now()
	stats := &Stats{}

	// ── Step 1: Census ──────────────────────────────────────────────────
	logger.Step("Step 1: Census")
	stepStart := time.Now()
	censusResult, err := census.Walk(idx.repo, idx.cfg)
	if err != nil {
		return nil, fmt.Errorf("indexer: census: %w", err)
	}
	logger.Info("census complete",
		"handwritten", censusResult.Handwritten,
		"test", censusResult.Test,
		"generated", censusResult.Generated,
		"excluded", censusResult.Excluded,
		"elapsed", time.Since(stepStart).Round(time.Millisecond))

	// ── Step 2: Determine work list ─────────────────────────────────────
	// Only handwritten, non-test, non-generated files are candidates.
	var candidates []census.FileEntry
	for _, f := range censusResult.Files {
		if f.Classification.IsTest || f.Classification.IsGenerated {
			continue
		}
		candidates = append(candidates, f)
	}

	var work []workItem
	if full {
		for _, c := range candidates {
			work = append(work, workItem{entry: c, isNew: true})
		}
	} else {
		var newCount, changedCount int
		for _, c := range candidates {
			existing, err := idx.db.GetFileByPath(ctx, c.RelPath)
			if err != nil {
				return nil, fmt.Errorf("indexer: checking file %s: %w", c.RelPath, err)
			}
			if existing == nil {
				work = append(work, workItem{entry: c, isNew: true})
				newCount++
			} else if existing.Checksum != c.Checksum {
				work = append(work, workItem{entry: c, isNew: false})
				changedCount++
			}
		}
		logger.Info("work list", "files", len(work), "new", newCount, "changed", changedCount)

		// Remove files that no longer exist.
		allPaths := make([]string, len(candidates))
		for i, c := range candidates {
			allPaths[i] = c.RelPath
		}
		deleted, err := idx.db.DeleteStaleFiles(ctx, allPaths)
		if err != nil {
			return nil, fmt.Errorf("indexer: deleting stale files: %w", err)
		}
		if deleted > 0 {
			logger.Info("deleted stale files", "count", deleted)
		}
	}

	if full {
		logger.Info("work list", "files", len(work), "mode", "full reindex")
	}

	if len(work) == 0 {
		logger.Info("nothing to do — index is up to date")
		return stats, nil
	}

	// ── Step 3: Git state ───────────────────────────────────────────────
	logger.Step("Step 3: Git state")
	stepStart = time.Now()
	commitSHA, err := gitOutput(idx.repo, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("indexer: git rev-parse HEAD: %w", err)
	}
	branch, err := gitOutput(idx.repo, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("indexer: git branch: %w", err)
	}

	runID, err := idx.db.StartRun(ctx, commitSHA)
	if err != nil {
		return nil, fmt.Errorf("indexer: start run: %w", err)
	}
	logger.Info("git state", "branch", branch, "commit", commitSHA, "elapsed", time.Since(stepStart).Round(time.Millisecond))
	// On failure, mark the run as failed. Use a background context
	// here so cancellation/signal teardown can't prevent the UPDATE
	// from reaching the database — otherwise rows stay "running"
	// forever after Ctrl+C or a TUI quit.
	var runCompleted bool
	defer func() {
		if !runCompleted {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = idx.db.FailRun(cleanup, runID)
		}
	}()

	// ── Step 4: Parse ───────────────────────────────────────────────────
	logger.Step("Step 4: Parse")
	stepStart = time.Now()
	logger.Info("parsing all packages via go/packages (this may take a while)...")
	parseDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-parseDone:
				return
			case <-ticker.C:
				logger.Info("still parsing...", "elapsed", time.Since(stepStart).Round(time.Second))
			}
		}
	}()
	parseResult, err := parser.Parse(ctx, idx.repo, []string{"./..."})
	close(parseDone)
	if err != nil {
		return nil, fmt.Errorf("indexer: parse: %w", err)
	}

	// Build a set of work-list relative paths for filtering.
	workSet := make(map[string]bool, len(work))
	for _, w := range work {
		workSet[w.entry.RelPath] = true
	}

	// Filter parsed files to only those in the work list (match by relative path).
	var filteredFiles []parser.FileResult
	for _, fr := range parseResult.Files {
		relPath, err := relativeToRepo(idx.repo, fr.Path)
		if err != nil {
			continue
		}
		if workSet[relPath] {
			filteredFiles = append(filteredFiles, fr)
		}
	}

	symbolCount := 0
	for _, fr := range filteredFiles {
		symbolCount += len(fr.Symbols)
	}
	logger.Info("parsed symbols", "symbols", symbolCount, "files", len(filteredFiles), "elapsed", time.Since(stepStart).Round(time.Millisecond))
	stats.SymbolsExtracted = symbolCount
	stats.FilesProcessed = len(work)

	// ── Step 5: Store files and symbols ─────────────────────────────────
	logger.Step("Step 5: Store files and symbols")
	stepStart = time.Now()
	// fileID tracks the database ID for each work-list file (keyed by relPath).
	fileIDMap := make(map[string]int64)
	// fileSymbols maps relPath → its parsed symbols.
	fileSymbols := make(map[string][]parser.Symbol)

	for _, fr := range filteredFiles {
		relPath, _ := relativeToRepo(idx.repo, fr.Path)
		fileSymbols[relPath] = fr.Symbols
	}

	for fileNum, w := range work {
		rp := w.entry.RelPath
		if (fileNum+1)%500 == 0 || fileNum == len(work)-1 {
			logger.Progress("storing files and symbols", fileNum+1, len(work))
		}
		summary := summaries.HeuristicFileSummary(fileSymbols[rp], "")
		rec := &storage.FileRecord{
			Path:             rp,
			PackageName:      w.entry.PackageName,
			Checksum:         w.entry.Checksum,
			Language:         w.entry.Classification.Language,
			IsGenerated:      w.entry.Classification.IsGenerated,
			IsTest:           w.entry.Classification.IsTest,
			LineCount:        w.entry.LineCount,
			HeuristicSummary: &summary,
			CommitSHA:        commitSHA,
		}
		fid, err := idx.db.UpsertFile(ctx, rec)
		if err != nil {
			return nil, fmt.Errorf("indexer: upsert file %s: %w", rp, err)
		}
		fileIDMap[rp] = fid

		// Clean old symbols for this file, then insert new ones.
		if err := idx.db.DeleteSymbolsByFileID(ctx, fid); err != nil {
			return nil, fmt.Errorf("indexer: delete symbols for %s: %w", rp, err)
		}

		syms := fileSymbols[rp]
		if len(syms) == 0 {
			continue
		}
		records := make([]storage.SymbolRecord, len(syms))
		for i, s := range syms {
			var recv *string
			if s.Receiver != "" {
				r := s.Receiver
				recv = &r
			}
			var doc *string
			if s.DocComment != "" {
				d := s.DocComment
				doc = &d
			}

			// Build SCIP symbol ID
			dir := filepath.Dir(rp) // e.g., "internal/indexer"
			var scipSymbol string
			switch s.Kind {
			case "method":
				scipSymbol = fmt.Sprintf("go . %s . %s.%s()", dir, s.Receiver, s.Name)
			default:
				scipSymbol = fmt.Sprintf("go . %s . %s", dir, s.Name)
			}
			scipStr := scipSymbol

			records[i] = storage.SymbolRecord{
				FileID:      fid,
				Name:        s.Name,
				Kind:        s.Kind,
				PackageName: s.Package,
				Receiver:    recv,
				Signature:   s.Signature,
				DocComment:  doc,
				LineStart:   s.LineStart,
				LineEnd:     s.LineEnd,
				Checksum:    sha256Hex(s.Body),
				ScipSymbol:  &scipStr,
				Roles:       1, // Definition
			}
		}
		if err := idx.db.InsertSymbols(ctx, records); err != nil {
			return nil, fmt.Errorf("indexer: insert symbols for %s: %w", rp, err)
		}
	}

	logger.Info("stored files and symbols", "count", len(work), "elapsed", time.Since(stepStart).Round(time.Millisecond))

	// ── Step 6: Chunk ───────────────────────────────────────────────────
	logger.Step("Step 6: Chunk")
	stepStart = time.Now()
	// We need to read back symbol IDs from the database so we can link chunks.
	// chunkInfo tracks the mapping between a chunk and its symbol ID.
	type chunkInfo struct {
		symbolID int64
		chunk    chunks.Chunk
	}
	var allChunks []chunkInfo

	for _, w := range work {
		rp := w.entry.RelPath
		fid := fileIDMap[rp]
		syms := fileSymbols[rp]
		if len(syms) == 0 {
			continue
		}

		batch := chunks.CreateBatch(syms, "")

		// Read back stored symbol IDs for this file.
		storedSymbols, err := idx.db.GetSymbolsByFileID(ctx, fid)
		if err != nil {
			return nil, fmt.Errorf("indexer: get symbols for chunking %s: %w", rp, err)
		}

		// Match chunks to stored symbols by name + line range.
		symIDByKey := make(map[string]int64)
		for _, ss := range storedSymbols {
			key := fmt.Sprintf("%s:%d:%d", ss.Name, ss.LineStart, ss.LineEnd)
			symIDByKey[key] = ss.ID
		}

		for i, ch := range batch {
			if i >= len(syms) {
				break
			}
			sym := syms[i]
			key := fmt.Sprintf("%s:%d:%d", sym.Name, sym.LineStart, sym.LineEnd)
			sid, ok := symIDByKey[key]
			if !ok {
				continue
			}
			allChunks = append(allChunks, chunkInfo{symbolID: sid, chunk: ch})
		}
	}

	// Store chunks.
	chunkIDMap := make(map[int]int64) // allChunks index → chunk DB ID
	for i, ci := range allChunks {
		sid := ci.symbolID
		rec := &storage.ChunkRecord{
			SymbolID:   &sid,
			Content:    ci.chunk.Content,
			TokenCount: ci.chunk.TokenCount,
			SourceType: "code",
		}
		cid, err := idx.db.UpsertChunk(ctx, rec)
		if err != nil {
			return nil, fmt.Errorf("indexer: upsert chunk: %w", err)
		}
		chunkIDMap[i] = cid
	}
	stats.ChunksCreated = len(allChunks)
	logger.Info("created chunks", "count", stats.ChunksCreated, "elapsed", time.Since(stepStart).Round(time.Millisecond))

	// ── Step 7: Graph ───────────────────────────────────────────────────
	logger.Step("Step 7: Graph")
	stepStart = time.Now()
	logger.Info("building call graph via go/packages (this may take a while)...")
	graphDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-graphDone:
				return
			case <-ticker.C:
				logger.Info("still building graph...", "elapsed", time.Since(stepStart).Round(time.Second))
			}
		}
	}()
	graphResult, err := graph.Build(ctx, idx.repo, []string{"./..."})
	close(graphDone)
	if err != nil {
		return nil, fmt.Errorf("indexer: graph build: %w", err)
	}

	var edgeRecords []storage.EdgeRecord
	for _, e := range graphResult.Edges {
		sourceSyms, err := idx.db.GetSymbolByName(ctx, e.SourceName)
		if err != nil {
			continue
		}
		targetSyms, err := idx.db.GetSymbolByName(ctx, e.TargetName)
		if err != nil {
			continue
		}

		sourceID := matchSymbol(sourceSyms, e.SourcePackage)
		targetID := matchSymbol(targetSyms, e.TargetPackage)
		if sourceID == 0 || targetID == 0 {
			continue
		}
		edgeRecords = append(edgeRecords, storage.EdgeRecord{
			SourceType: "symbol",
			SourceID:   sourceID,
			TargetType: "symbol",
			TargetID:   targetID,
			EdgeType:   e.EdgeType,
		})
	}

	if err := idx.db.InsertEdges(ctx, edgeRecords); err != nil {
		return nil, fmt.Errorf("indexer: insert edges: %w", err)
	}
	stats.EdgesCreated = len(edgeRecords)
	logger.Info("created edges", "count", stats.EdgesCreated, "elapsed", time.Since(stepStart).Round(time.Millisecond))

	// ── Step 8: Summarize packages ──────────────────────────────────────
	logger.Step("Step 8: Summarize packages")
	stepStart = time.Now()
	summarizeStart := stepStart
	if idx.summarizer != nil {
		pkgSymMap := make(map[string][]parser.Symbol)
		for _, fr := range filteredFiles {
			pkgSymMap[fr.Package] = append(pkgSymMap[fr.Package], fr.Symbols...)
		}

		// Skip packages that already have summaries.
		skipped := 0
		for pkgName := range pkgSymMap {
			existing, _ := idx.db.GetSummaryByPackage(ctx, pkgName)
			if existing != nil {
				delete(pkgSymMap, pkgName)
				skipped++
			}
		}
		if skipped > 0 {
			logger.Info("skipping packages with existing summaries", "count", skipped)
		}

		pkgSummaries, err := summaries.GeneratePackageSummaries(ctx, idx.summarizer, pkgSymMap)
		if err != nil {
			return nil, fmt.Errorf("indexer: generate package summaries: %w", err)
		}

		for pkgName, text := range pkgSummaries {
			rec := &storage.SummaryRecord{
				PackageName:  pkgName,
				SummaryText:  text,
				ModelVersion: "llm",
			}
			if err := idx.db.UpsertSummary(ctx, rec); err != nil {
				return nil, fmt.Errorf("indexer: upsert summary for %s: %w", pkgName, err)
			}
		}
		stats.PackagesSummarized = len(pkgSummaries)
		logger.Info("summarized packages", "count", stats.PackagesSummarized, "elapsed", time.Since(stepStart).Round(time.Millisecond))
		if err := idx.db.RecordStageRun(ctx, commitSHA, "summarize", "completed", summarizeStart, time.Now(), stats.PackagesSummarized); err != nil {
			logger.Warn("record summarize stage run failed", "err", err)
		}
	} else {
		logger.Warn("skipping package summarization (no summarizer configured)")
	}

	// ── Step 9: Embed ───────────────────────────────────────────────────
	logger.Step("Step 9: Embed")
	stepStart = time.Now()
	embedStart := stepStart
	if idx.embedder != nil && len(allChunks) > 0 {
		logger.Info("embedding chunks...", "count", len(allChunks))
		contents := make([]string, len(allChunks))
		for i, ci := range allChunks {
			contents[i] = ci.chunk.Content
		}

		embResults, err := embeddings.EmbedChunks(ctx, idx.embedder, contents)
		if err != nil {
			return nil, fmt.Errorf("indexer: embed chunks: %w", err)
		}

		for _, er := range embResults {
			cid, ok := chunkIDMap[er.ChunkIndex]
			if !ok {
				continue
			}
			rec := &storage.EmbeddingRecord{
				ChunkID:      cid,
				ModelVersion: "embedding-model",
				Embedding:    pgvector.NewHalfVector(er.Vector),
			}
			if err := idx.db.UpsertEmbedding(ctx, rec); err != nil {
				return nil, fmt.Errorf("indexer: upsert embedding: %w", err)
			}
		}
		stats.ChunksEmbedded = len(embResults)
		logger.Info("embedded chunks", "count", stats.ChunksEmbedded, "elapsed", time.Since(stepStart).Round(time.Millisecond))
		if err := idx.db.RecordStageRun(ctx, commitSHA, "embed", "completed", embedStart, time.Now(), stats.ChunksEmbedded); err != nil {
			logger.Warn("record embed stage run failed", "err", err)
		}
	} else if idx.embedder == nil {
		logger.Warn("skipping embedding (no embedder configured)")
	}

	// ── Step 10: Complete ───────────────────────────────────────────────
	logger.Step("Step 10: Complete")
	if err := idx.db.CompleteRun(ctx, runID, stats.FilesProcessed, stats.SymbolsExtracted, stats.EdgesCreated); err != nil {
		return nil, fmt.Errorf("indexer: complete run: %w", err)
	}
	runCompleted = true

	if err := idx.db.UpsertGitRef(ctx, branch, commitSHA); err != nil {
		return nil, fmt.Errorf("indexer: upsert git ref: %w", err)
	}

	logger.Info("indexing complete", "total_time", time.Since(pipelineStart).Round(time.Millisecond))
	return stats, nil
}

// DryRun runs only the census and work-list determination and returns what
// would be indexed without making any changes.
func (idx *Indexer) DryRun(ctx context.Context) error {
	censusResult, err := census.Walk(idx.repo, idx.cfg)
	if err != nil {
		return fmt.Errorf("indexer: census: %w", err)
	}

	fmt.Printf("Census: %d handwritten, %d test, %d generated, %d excluded\n",
		censusResult.Handwritten, censusResult.Test, censusResult.Generated, censusResult.Excluded)

	var candidates []census.FileEntry
	for _, f := range censusResult.Files {
		if f.Classification.IsTest || f.Classification.IsGenerated {
			continue
		}
		candidates = append(candidates, f)
	}

	var newFiles, changedFiles, unchangedFiles int
	for _, c := range candidates {
		existing, err := idx.db.GetFileByPath(ctx, c.RelPath)
		if err != nil {
			return fmt.Errorf("indexer: checking file %s: %w", c.RelPath, err)
		}
		if existing == nil {
			newFiles++
			fmt.Printf("  NEW      %s\n", c.RelPath)
		} else if existing.Checksum != c.Checksum {
			changedFiles++
			fmt.Printf("  CHANGED  %s\n", c.RelPath)
		} else {
			unchangedFiles++
		}
	}

	fmt.Printf("\nSummary: %d new, %d changed, %d unchanged\n", newFiles, changedFiles, unchangedFiles)
	return nil
}

// gitOutput runs a git command in the repo directory and returns trimmed stdout.
func gitOutput(repoPath string, args ...string) (string, error) {
	cmdArgs := append([]string{"-C", repoPath}, args...)
	cmd := exec.Command("git", cmdArgs...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// relativeToRepo computes the path of absPath relative to repoPath.
func relativeToRepo(repoPath, absPath string) (string, error) {
	// filepath.Rel can be used but for safety we do a simple prefix strip.
	repoPath = strings.TrimSuffix(repoPath, "/") + "/"
	if strings.HasPrefix(absPath, repoPath) {
		return absPath[len(repoPath):], nil
	}
	return "", fmt.Errorf("path %s not under repo %s", absPath, repoPath)
}

// matchSymbol finds the symbol ID in candidates whose package name matches
// the given package path (comparing the last segment).
func matchSymbol(candidates []storage.SymbolRecord, pkgPath string) int64 {
	if len(candidates) == 0 {
		return 0
	}
	// pkgPath is a full path like "github.com/foo/bar/internal/parser".
	// The stored package_name is just the short name like "parser".
	shortPkg := pkgPath
	if idx := strings.LastIndex(pkgPath, "/"); idx >= 0 {
		shortPkg = pkgPath[idx+1:]
	}

	for _, c := range candidates {
		if c.PackageName == shortPkg {
			return c.ID
		}
	}
	// Fallback: return the first match.
	return candidates[0].ID
}

// sha256Hex returns the hex SHA-256 digest of s.
func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h)
}
