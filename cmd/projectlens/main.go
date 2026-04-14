package main

import (
	"fmt"
	"os"

	"github.com/hman-pro/projectlens/internal/census"
	"github.com/hman-pro/projectlens/internal/classifier"
	"github.com/hman-pro/projectlens/internal/config"
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
		Short: "Bootstrap the database schema and initial index",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("not yet implemented")
		},
	}
}

func newReindexCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reindex",
		Short: "Reindex the repository",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("not yet implemented")
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
