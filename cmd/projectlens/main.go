package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hman-pro/projectlens/internal/census"
	"github.com/hman-pro/projectlens/internal/classifier"
	"github.com/hman-pro/projectlens/internal/config"
	"github.com/hman-pro/projectlens/internal/indexer"
	"github.com/hman-pro/projectlens/internal/openai"
	"github.com/hman-pro/projectlens/internal/storage"
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

			var oaiClient *openai.Client
			if cfg.OpenAIKey != "" {
				oaiClient = openai.NewClient(cfg.OpenAIKey)
			}

			idx := indexer.New(db, oaiClient, repoPath, classifier.DefaultConfig())
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

			var oaiClient *openai.Client
			if cfg.OpenAIKey != "" {
				oaiClient = openai.NewClient(cfg.OpenAIKey)
			}

			idx := indexer.New(db, oaiClient, repoPath, classifier.DefaultConfig())

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
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("not yet implemented")
		},
	}
}

func newInspectSymbolCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "inspect-symbol [symbol]",
		Short: "Inspect a symbol in the index",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("not yet implemented")
		},
	}
}

func newInspectPackageCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "inspect-package [package]",
		Short: "Inspect a package in the index",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("not yet implemented")
		},
	}
}

func newQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "query [query]",
		Short: "Query the index",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("not yet implemented")
		},
	}
	cmd.Flags().String("mode", "", "query mode")
	return cmd
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
