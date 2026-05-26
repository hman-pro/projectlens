package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/hman-pro/projectlens/internal/projects"
)

func newProjectsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "projects",
		Short: "Inspect the project registry",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List configured projects",
			RunE: func(cmd *cobra.Command, _ []string) error {
				regPath, _ := cmd.Flags().GetString("projects")
				reg, err := projects.LoadRegistry(regPath)
				if err != nil {
					return err
				}
				fmt.Printf("registry: %s\n", regPath)
				fmt.Printf("database: %s\n", reg.DatabaseURL)
				if reg.DefaultProject != "" {
					fmt.Printf("default:  %s\n", reg.DefaultProject)
				}
				fmt.Println()
				fmt.Printf("%-16s %-20s %s\n", "SLUG", "STORAGE_SCHEMA", "REPO_PATH")
				for _, p := range reg.Projects {
					fmt.Printf("%-16s %-20s %s\n", p.Slug, p.StorageSchema, p.RepoPath)
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "validate",
			Short: "Validate the project registry",
			RunE: func(cmd *cobra.Command, _ []string) error {
				regPath, _ := cmd.Flags().GetString("projects")
				if _, err := projects.LoadRegistry(regPath); err != nil {
					return err
				}
				fmt.Printf("registry %s is valid\n", regPath)
				return nil
			},
		},
	)
	return cmd
}
