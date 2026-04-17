package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
	"unicode"

	"github.com/hman-pro/projectlens/internal/census"
	"github.com/hman-pro/projectlens/internal/embed"
	"github.com/hman-pro/projectlens/internal/classifier"
	"github.com/hman-pro/projectlens/internal/config"
	"github.com/hman-pro/projectlens/internal/datastore"
	"github.com/hman-pro/projectlens/internal/embeddings"
	"github.com/hman-pro/projectlens/internal/history"
	"github.com/hman-pro/projectlens/internal/indexer"
	"github.com/hman-pro/projectlens/internal/providers/anthropic"
	"github.com/hman-pro/projectlens/internal/providers/ollama"
	"github.com/hman-pro/projectlens/internal/providers/openai"
	"github.com/hman-pro/projectlens/internal/retrieval"
	"github.com/hman-pro/projectlens/internal/storage"
	"github.com/hman-pro/projectlens/internal/summarize"
	"github.com/hman-pro/projectlens/internal/summaries"
	"github.com/spf13/cobra"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "projectlens",
		Short: "Repository intelligence layer for Go codebases",
	}

	rootCmd.PersistentFlags().String("config", "configs/index.yaml", "path to config file")
	rootCmd.PersistentFlags().String("db", "", "database URL override")
	rootCmd.PersistentFlags().String("repo", "", "repository path override")

	rootCmd.AddCommand(
		newCensusCmd(),
		newBootstrapCmd(),
		newReindexCmd(),
		newStatusCmd(),
		newInspectSymbolCmd(),
		newInspectPackageCmd(),
		newQueryCmd(),
		newIndexDatastoreCmd(),
		newIndexHistoryCmd(),
		newIndexEmbedCmd(),
		newIndexSummarizeCmd(),
		newIndexAllCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newCensusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "census",
		Short: "Run a census of the repository",
		RunE: func(cmd *cobra.Command, args []string) error {
			repoPath, _ := cmd.Flags().GetString("repo")

			// Fall back to config's RepoPath if --repo not provided.
			if repoPath == "" {
				cfgPath, _ := cmd.Flags().GetString("config")
				cfg, err := config.Load(cfgPath)
				if err != nil {
					return fmt.Errorf("loading config: %w", err)
				}
				repoPath = cfg.RepoPath
			}

			if repoPath == "" {
				return fmt.Errorf("repository path required: use --repo flag or set repo_path in config")
			}

			result, err := census.Walk(repoPath, classifier.DefaultConfig())
			if err != nil {
				return err
			}

			fmt.Printf("Repository Census: %s\n", repoPath)
			fmt.Println("─────────────────────────────────")
			fmt.Printf("Total .go files:  %6d\n", result.Total)
			fmt.Printf("Handwritten:      %6d\n", result.Handwritten)
			fmt.Printf("Test:             %6d\n", result.Test)
			fmt.Printf("Generated:        %6d\n", result.Generated)
			fmt.Printf("Excluded:         %6d\n", result.Excluded)
			return nil
		},
	}
}

func newBootstrapCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bootstrap",
		Short: "Bootstrap the database schema and run a full index",
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

			// Run migrations.
			migrationsDir := findMigrationsDir()
			if err := db.Migrate(ctx, migrationsDir); err != nil {
				return fmt.Errorf("running migrations: %w", err)
			}
			log.Println("migrations applied successfully")

			embedder, summarizer, err := buildProviders(cfg)
			if err != nil {
				return fmt.Errorf("initializing providers: %w", err)
			}

			idx := indexer.New(db, embedder, summarizer, repoPath, classifier.DefaultConfig())
			stats, err := idx.Run(ctx, true)
			if err != nil {
				return fmt.Errorf("bootstrap indexing: %w", err)
			}

			printStats(stats)
			return nil
		},
	}
}

func newReindexCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reindex",
		Short: "Reindex the repository",
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

			idx := indexer.New(db, embedder, summarizer, repoPath, classifier.DefaultConfig())

			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if dryRun {
				return idx.DryRun(ctx)
			}

			full, _ := cmd.Flags().GetBool("full")
			stats, err := idx.Run(ctx, full)
			if err != nil {
				return fmt.Errorf("reindex: %w", err)
			}

			printStats(stats)
			return nil
		},
	}
	cmd.Flags().Bool("full", false, "perform a full reindex")
	cmd.Flags().Bool("dry-run", false, "show what would be reindexed without making changes")
	return cmd
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show index status",
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

			run, err := db.GetLatestRun(ctx)
			if err != nil {
				return fmt.Errorf("getting latest run: %w", err)
			}

			if run == nil {
				fmt.Println("No index runs found. Run 'projectlens bootstrap' first.")
				return nil
			}

			ts := run.StartedAt.UTC().Format("2006-01-02 15:04:05 UTC")
			commit := run.CommitSHA
			if len(commit) > 7 {
				commit = commit[:7]
			}

			staleness := formatStaleness(run.StartedAt)

			fmt.Println("ProjectLens Status")
			fmt.Println("─────────────────")
			fmt.Printf("Last indexed:     %s\n", ts)
			fmt.Printf("Commit:           %s\n", commit)
			fmt.Printf("Stage:            %s\n", run.Stage)
			fmt.Printf("Files processed:  %d\n", run.FilesProcessed)
			fmt.Printf("Symbols:          %d\n", run.SymbolsExtracted)
			fmt.Printf("Edges:            %d\n", run.EdgesCreated)
			fmt.Printf("Status:           %s\n", run.Status)
			fmt.Printf("Staleness:        %s\n", staleness)
			return nil
		},
	}
}

func newInspectSymbolCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "inspect-symbol [symbol]",
		Short: "Inspect a symbol in the index",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			symbolName := args[0]

			cfg, _, err := loadCmdConfig(cmd)
			if err != nil {
				return err
			}

			db, err := storage.Connect(ctx, cfg.DatabaseURL)
			if err != nil {
				return fmt.Errorf("connecting to database: %w", err)
			}
			defer db.Close()

			results, err := retrieval.LexicalSearch(ctx, db, symbolName, 10)
			if err != nil {
				return fmt.Errorf("searching for symbol: %w", err)
			}

			if len(results) == 0 {
				fmt.Printf("No symbol found matching %q.\n", symbolName)
				return nil
			}

			top := results[0]

			fmt.Printf("Symbol: %s\n", top.SymbolName)
			fmt.Printf("Kind:   %s\n", top.Kind)
			fmt.Printf("File:   %s:%d-%d\n", top.FilePath, top.LineStart, top.LineEnd)
			fmt.Printf("Package: %s\n", top.PackageName)
			fmt.Printf("Signature: %s\n", top.Signature)
			if top.DocComment != "" {
				fmt.Printf("Doc: %s\n", top.DocComment)
			}

			// Look up SCIP symbol.
			symRecords, _ := db.GetSymbolByName(ctx, top.SymbolName)
			for _, sr := range symRecords {
				if sr.ID == top.SymbolID {
					if sr.ScipSymbol != nil {
						fmt.Printf("SCIP:     %s\n", *sr.ScipSymbol)
					}
					break
				}
			}

			// Get callers.
			callers, err := retrieval.GetCallers(ctx, db, top.SymbolID, 2)
			if err != nil {
				return fmt.Errorf("getting callers: %w", err)
			}
			fmt.Printf("\nCallers (%d):\n", len(callers))
			if len(callers) == 0 {
				fmt.Println("  (none)")
			} else {
				for _, c := range callers {
					fmt.Printf("  - %s (%s:%d)\n", c.SymbolName, c.FilePath, c.LineStart)
				}
			}

			// Get callees.
			callees, err := retrieval.GetCallees(ctx, db, top.SymbolID, 2)
			if err != nil {
				return fmt.Errorf("getting callees: %w", err)
			}
			fmt.Printf("\nCallees (%d):\n", len(callees))
			if len(callees) == 0 {
				fmt.Println("  (none)")
			} else {
				for _, c := range callees {
					fmt.Printf("  - %s (%s:%d)\n", c.SymbolName, c.FilePath, c.LineStart)
				}
			}

			// Get implementors.
			implementors, err := retrieval.GetImplementors(ctx, db, top.SymbolID)
			if err != nil {
				return fmt.Errorf("getting implementors: %w", err)
			}
			fmt.Printf("\nImplements: ")
			if len(implementors) == 0 {
				fmt.Println("(none)")
			} else {
				fmt.Println()
				for _, impl := range implementors {
					fmt.Printf("  - %s (%s:%d)\n", impl.SymbolName, impl.FilePath, impl.LineStart)
				}
			}

			return nil
		},
	}
}

func newInspectPackageCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "inspect-package [package]",
		Short: "Inspect a package in the index",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			packageName := args[0]

			cfg, _, err := loadCmdConfig(cmd)
			if err != nil {
				return err
			}

			db, err := storage.Connect(ctx, cfg.DatabaseURL)
			if err != nil {
				return fmt.Errorf("connecting to database: %w", err)
			}
			defer db.Close()

			summary, err := db.GetSummaryByPackage(ctx, packageName)
			if err != nil {
				return fmt.Errorf("getting package summary: %w", err)
			}

			symbols, err := db.GetSymbolsByPackage(ctx, packageName)
			if err != nil {
				return fmt.Errorf("getting package symbols: %w", err)
			}

			fmt.Printf("Package: %s\n", packageName)
			if summary != nil {
				fmt.Printf("Summary: %s\n", summary.SummaryText)
			} else {
				fmt.Println("Summary: (no summary available)")
			}

			// Filter to exported symbols (name starts with uppercase).
			var exported []storage.SymbolRecord
			for _, s := range symbols {
				if len(s.Name) > 0 && unicode.IsUpper(rune(s.Name[0])) {
					exported = append(exported, s)
				}
			}

			fmt.Printf("\nExported Symbols (%d):\n", len(exported))
			if len(exported) == 0 {
				fmt.Println("  (none)")
			} else {
				for _, s := range exported {
					fmt.Printf("  %s %s\n", s.Kind, s.Signature)
				}
			}

			return nil
		},
	}
}

func newQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "query [query]",
		Short: "Query the index",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			queryText := args[0]

			cfg, _, err := loadCmdConfig(cmd)
			if err != nil {
				return err
			}

			db, err := storage.Connect(ctx, cfg.DatabaseURL)
			if err != nil {
				return fmt.Errorf("connecting to database: %w", err)
			}
			defer db.Close()

			var embedder retrieval.QueryEmbedder
			switch cfg.Embeddings.Provider {
			case "ollama":
				embedder = ollama.NewClient(cfg.Embeddings.Endpoint, cfg.Embeddings.Model)
			case "openai":
				if cfg.OpenAIKey != "" {
					embedder = openai.NewClient(cfg.OpenAIKey)
				}
			}

			mode, _ := cmd.Flags().GetString("mode")

			var results []retrieval.SearchResult
			var queryType retrieval.QueryType

			switch mode {
			case "lexical":
				queryType = retrieval.ExactSymbol
				results, err = retrieval.LexicalSearch(ctx, db, queryText, 10)
			case "semantic":
				if embedder == nil {
					return fmt.Errorf("semantic search requires an OpenAI API key (set openai_api_key in config or OPENAI_API_KEY env)")
				}
				queryType = retrieval.ImplementationSearch
				results, err = retrieval.SemanticSearch(ctx, db, embedder, queryText, 10)
			default:
				// Auto mode: use the router.
				router := retrieval.NewRouter(db, embedder)
				qr, routerErr := router.Query(ctx, queryText, 10)
				if routerErr != nil {
					return fmt.Errorf("query: %w", routerErr)
				}
				queryType = qr.QueryType
				results = qr.Results
			}
			if err != nil {
				return fmt.Errorf("query: %w", err)
			}

			fmt.Printf("Query: %q\n", queryText)
			fmt.Printf("Type:  %s\n", queryType)
			fmt.Printf("\nResults (%d):\n", len(results))

			for i, r := range results {
				fmt.Printf("%d. [%.2f] %s %s — %s:%d-%d\n",
					i+1, r.Score, r.Kind, r.SymbolName, r.FilePath, r.LineStart, r.LineEnd)
				if r.DocComment != "" {
					fmt.Printf("   %s\n", r.DocComment)
				}
			}

			if len(results) == 0 {
				fmt.Println("  (no results)")
			}

			return nil
		},
	}
	cmd.Flags().String("mode", "", "query mode: lexical, semantic, or auto (default)")
	return cmd
}

func newIndexDatastoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "index-datastore",
		Short: "Index database schemas and SQL queries",
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

			dsCfg := datastore.Config{
				SQLScanPaths: cfg.Datastore.SQLScanPaths,
			}
			for _, e := range cfg.Datastore.Engines {
				dsCfg.Engines = append(dsCfg.Engines, datastore.EngineConfig{
					Name:           e.Name,
					MigrationPaths: e.MigrationPaths,
				})
			}

			return datastore.IndexDatastore(ctx, db, repoPath, dsCfg)
		},
	}
}

func newIndexHistoryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "index-history",
		Short: "Index git change history and compute co-change coupling",
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

			return history.IndexHistory(ctx, db, repoPath, history.Config{
				WindowMonths:         cfg.History.WindowMonths,
				MinCommitsPerFile:    cfg.History.MinCommitsPerFile,
				CouplingMinCoChanges: cfg.History.CouplingMinCoChanges,
				CouplingMaxFiles:     cfg.History.CouplingMaxFiles,
			})
		},
	}
}

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

func newIndexSummarizeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "index-summarize",
		Short: "Generate summaries for packages that don't have one",
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

			_, summarizer, err := buildProviders(cfg)
			if err != nil {
				return fmt.Errorf("initializing providers: %w", err)
			}

			return summarize.SummarizeMissing(ctx, db, summarizer)
		},
	}
}

func newIndexAllCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "index-all",
		Short: "Run all indexing stages: code, datastore, history, summarize, embed",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			startTime := time.Now()

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

			log.Println("═══ Running all indexing stages ═══")

			// Stage 1: Code
			log.Println("\n═══ Stage 1: Code ═══")
			full, _ := cmd.Flags().GetBool("full")
			idx := indexer.New(db, nil, nil, repoPath, classifier.DefaultConfig())
			if _, err := idx.Run(ctx, full); err != nil {
				return fmt.Errorf("code indexing: %w", err)
			}

			// Stage 2: Datastore
			log.Println("\n═══ Stage 2: Datastore ═══")
			dsCfg := datastore.Config{}
			for _, e := range cfg.Datastore.Engines {
				dsCfg.Engines = append(dsCfg.Engines, datastore.EngineConfig{
					Name:           e.Name,
					MigrationPaths: e.MigrationPaths,
				})
			}
			dsCfg.SQLScanPaths = cfg.Datastore.SQLScanPaths
			if err := datastore.IndexDatastore(ctx, db, repoPath, dsCfg); err != nil {
				log.Printf("warning: datastore indexing failed: %v", err)
			}

			// Stage 3: History
			log.Println("\n═══ Stage 3: History ═══")
			hCfg := history.Config{
				WindowMonths:         cfg.History.WindowMonths,
				MinCommitsPerFile:    cfg.History.MinCommitsPerFile,
				CouplingMinCoChanges: cfg.History.CouplingMinCoChanges,
				CouplingMaxFiles:     cfg.History.CouplingMaxFiles,
			}
			if err := history.IndexHistory(ctx, db, repoPath, hCfg); err != nil {
				log.Printf("warning: history indexing failed: %v", err)
			}

			// Stage 4: Summarize (only missing)
			log.Println("\n═══ Stage 4: Summarize ═══")
			if summarizer != nil {
				if err := summarize.SummarizeMissing(ctx, db, summarizer); err != nil {
					log.Printf("warning: summarization failed: %v", err)
				}
			} else {
				log.Println("skipping summarization (no summarizer configured)")
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
	cmd.Flags().Bool("full", false, "perform a full code reindex (other stages are always full)")
	return cmd
}

// buildProviders constructs the Embedder and PackageSummarizer based on config.
func buildProviders(cfg *config.Config) (embeddings.Embedder, summaries.PackageSummarizer, error) {
	var embedder embeddings.Embedder
	switch cfg.Embeddings.Provider {
	case "ollama":
		embedder = ollama.NewClient(cfg.Embeddings.Endpoint, cfg.Embeddings.Model)
	case "openai":
		if cfg.OpenAIKey == "" {
			return nil, nil, fmt.Errorf("OPENAI_API_KEY required when embeddings.provider is 'openai'")
		}
		if cfg.Embeddings.Dimensions > 0 {
			embedder = openai.NewClientWithDims(cfg.OpenAIKey, cfg.Embeddings.Dimensions)
		} else {
			embedder = openai.NewClient(cfg.OpenAIKey)
		}
	default:
		return nil, nil, fmt.Errorf("unknown embeddings provider: %s", cfg.Embeddings.Provider)
	}

	var summarizer summaries.PackageSummarizer
	switch cfg.Summarization.Provider {
	case "anthropic":
		summarizer = anthropic.NewClient(cfg.Summarization.Model)
	case "openai":
		if cfg.OpenAIKey == "" {
			return nil, nil, fmt.Errorf("OPENAI_API_KEY required when summarization.provider is 'openai'")
		}
		summarizer = openai.NewClient(cfg.OpenAIKey)
	default:
		return nil, nil, fmt.Errorf("unknown summarization provider: %s", cfg.Summarization.Provider)
	}

	return embedder, summarizer, nil
}

// loadCmdConfig loads configuration and resolves the repo path from flags.
func loadCmdConfig(cmd *cobra.Command) (*config.Config, string, error) {
	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, "", fmt.Errorf("loading config: %w", err)
	}

	// Override database URL if --db flag was given.
	if dbURL, _ := cmd.Flags().GetString("db"); dbURL != "" {
		cfg.DatabaseURL = dbURL
	}

	repoPath, _ := cmd.Flags().GetString("repo")
	if repoPath == "" {
		repoPath = cfg.RepoPath
	}
	if repoPath == "" {
		return nil, "", fmt.Errorf("repository path required: use --repo flag or set repo_path in config")
	}

	return cfg, repoPath, nil
}

// findMigrationsDir resolves the migrations directory relative to the
// current working directory or the binary location.
func findMigrationsDir() string {
	// Try common locations.
	candidates := []string{
		"migrations",
		"../migrations",
		"../../migrations",
	}
	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err != nil {
			continue
		}
		if info, err := os.Stat(abs); err == nil && info.IsDir() {
			return abs
		}
	}
	// Default to "migrations" — Migrate will fail with a clear error if missing.
	return "migrations"
}

// formatStaleness returns a human-readable string describing how long ago a
// timestamp was, e.g. "2 hours ago" or "3 days ago".
func formatStaleness(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		mins := int(d.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", mins)
	case d < 24*time.Hour:
		hours := int(d.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

// printStats formats and prints indexing statistics.
func printStats(s *indexer.Stats) {
	fmt.Println("\nIndexing Statistics")
	fmt.Println("───────────────────")
	fmt.Printf("Files processed:      %d\n", s.FilesProcessed)
	fmt.Printf("Symbols extracted:    %d\n", s.SymbolsExtracted)
	fmt.Printf("Chunks created:       %d\n", s.ChunksCreated)
	fmt.Printf("Edges created:        %d\n", s.EdgesCreated)
	fmt.Printf("Packages summarized:  %d\n", s.PackagesSummarized)
	fmt.Printf("Chunks embedded:      %d\n", s.ChunksEmbedded)
}
