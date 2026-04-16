// Package indexer orchestrates the full indexing pipeline: census, parsing,
// chunking, graph building, summarization, and embedding.
package indexer

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"os/exec"
	"strings"

	"github.com/hman-pro/projectlens/internal/census"
	"github.com/hman-pro/projectlens/internal/chunks"
	"github.com/hman-pro/projectlens/internal/classifier"
	"github.com/hman-pro/projectlens/internal/embeddings"
	"github.com/hman-pro/projectlens/internal/graph"
	"github.com/hman-pro/projectlens/internal/openai"
	"github.com/hman-pro/projectlens/internal/parser"
	"github.com/hman-pro/projectlens/internal/storage"
	"github.com/hman-pro/projectlens/internal/summaries"
	"github.com/pgvector/pgvector-go"
)

// Indexer ties together all pipeline stages and stores results in the database.
type Indexer struct {
	db   *storage.DB
	oai  *openai.Client
	repo string // path to the target repo
	cfg  classifier.Config
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

// New creates an Indexer. The openai client may be nil if summarization and
// embedding should be skipped (e.g. dry-run or tests).
func New(db *storage.DB, oai *openai.Client, repoPath string, cfg classifier.Config) *Indexer {
	return &Indexer{
		db:   db,
		oai:  oai,
		repo: repoPath,
		cfg:  cfg,
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
	stats := &Stats{}

	// ── Step 1: Census ──────────────────────────────────────────────────
	censusResult, err := census.Walk(idx.repo, idx.cfg)
	if err != nil {
		return nil, fmt.Errorf("indexer: census: %w", err)
	}
	log.Printf("census complete: %d handwritten, %d test, %d generated, %d excluded",
		censusResult.Handwritten, censusResult.Test, censusResult.Generated, censusResult.Excluded)

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
		log.Printf("work list: %d files to process (%d new, %d changed)", len(work), newCount, changedCount)

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
			log.Printf("deleted %d stale files", deleted)
		}
	}

	if full {
		log.Printf("work list: %d files to process (full reindex)", len(work))
	}

	if len(work) == 0 {
		log.Println("nothing to do — index is up to date")
		return stats, nil
	}

	// ── Step 3: Git state ───────────────────────────────────────────────
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
	// On failure, mark the run as failed.
	var runCompleted bool
	defer func() {
		if !runCompleted {
			_ = idx.db.FailRun(ctx, runID)
		}
	}()

	// ── Step 4: Parse ───────────────────────────────────────────────────
	parseResult, err := parser.Parse(ctx, idx.repo, []string{"./..."})
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
	log.Printf("parsed %d symbols from %d files", symbolCount, len(filteredFiles))
	stats.SymbolsExtracted = symbolCount
	stats.FilesProcessed = len(work)

	// ── Step 5: Store files and symbols ─────────────────────────────────
	// fileID tracks the database ID for each work-list file (keyed by relPath).
	fileIDMap := make(map[string]int64)
	// fileSymbols maps relPath → its parsed symbols.
	fileSymbols := make(map[string][]parser.Symbol)

	for _, fr := range filteredFiles {
		relPath, _ := relativeToRepo(idx.repo, fr.Path)
		fileSymbols[relPath] = fr.Symbols
	}

	for _, w := range work {
		rp := w.entry.RelPath
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
			}
		}
		if err := idx.db.InsertSymbols(ctx, records); err != nil {
			return nil, fmt.Errorf("indexer: insert symbols for %s: %w", rp, err)
		}
	}

	// ── Step 6: Chunk ───────────────────────────────────────────────────
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
	log.Printf("created %d chunks", stats.ChunksCreated)

	// ── Step 7: Graph ───────────────────────────────────────────────────
	graphResult, err := graph.Build(ctx, idx.repo, []string{"./..."})
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
	log.Printf("created %d edges", stats.EdgesCreated)

	// ── Step 8: Summarize packages ──────────────────────────────────────
	if idx.oai != nil {
		pkgSymMap := make(map[string][]parser.Symbol)
		for _, fr := range filteredFiles {
			pkgSymMap[fr.Package] = append(pkgSymMap[fr.Package], fr.Symbols...)
		}

		pkgSummaries, err := summaries.GeneratePackageSummaries(ctx, idx.oai, pkgSymMap)
		if err != nil {
			return nil, fmt.Errorf("indexer: generate package summaries: %w", err)
		}

		for pkgName, text := range pkgSummaries {
			rec := &storage.SummaryRecord{
				PackageName:  pkgName,
				SummaryText:  text,
				ModelVersion: "gpt-4o-mini",
			}
			if err := idx.db.UpsertSummary(ctx, rec); err != nil {
				return nil, fmt.Errorf("indexer: upsert summary for %s: %w", pkgName, err)
			}
		}
		stats.PackagesSummarized = len(pkgSummaries)
		log.Printf("summarized %d packages", stats.PackagesSummarized)
	} else {
		log.Println("skipping package summarization (no OpenAI client)")
	}

	// ── Step 9: Embed ───────────────────────────────────────────────────
	if idx.oai != nil && len(allChunks) > 0 {
		contents := make([]string, len(allChunks))
		for i, ci := range allChunks {
			contents[i] = ci.chunk.Content
		}

		embResults, err := embeddings.EmbedChunks(ctx, idx.oai, contents)
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
				ModelVersion: "text-embedding-3-large",
				Embedding:    pgvector.NewHalfVector(er.Vector),
			}
			if err := idx.db.UpsertEmbedding(ctx, rec); err != nil {
				return nil, fmt.Errorf("indexer: upsert embedding: %w", err)
			}
		}
		stats.ChunksEmbedded = len(embResults)
		log.Printf("embedded %d chunks", stats.ChunksEmbedded)
	} else if idx.oai == nil {
		log.Println("skipping embedding (no OpenAI client)")
	}

	// ── Step 10: Complete ───────────────────────────────────────────────
	if err := idx.db.CompleteRun(ctx, runID, stats.FilesProcessed, stats.SymbolsExtracted, stats.EdgesCreated); err != nil {
		return nil, fmt.Errorf("indexer: complete run: %w", err)
	}
	runCompleted = true

	if err := idx.db.UpsertGitRef(ctx, branch, commitSHA); err != nil {
		return nil, fmt.Errorf("indexer: upsert git ref: %w", err)
	}

	log.Println("indexing complete")
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
