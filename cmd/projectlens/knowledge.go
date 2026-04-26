package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hman-pro/projectlens/internal/storage"
)

func newKnowledgeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "knowledge",
		Short: "Query the captured knowledge layer (read-only; capture is MCP-only)",
	}
	cmd.AddCommand(newKnowledgeListCmd(), newKnowledgeShowCmd(),
		newKnowledgeDeleteCmd(), newKnowledgeSearchCmd())
	return cmd
}

func newKnowledgeListCmd() *cobra.Command {
	var category, tag string
	var limit int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List knowledge entries (most recent first)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			cfg, _, err := loadCmdConfig(cmd)
			if err != nil {
				return err
			}
			db, err := storage.Connect(ctx, cfg.DatabaseURL)
			if err != nil {
				return err
			}
			defer db.Close()

			entries, err := db.ListKnowledgeEntries(ctx, storage.KnowledgeListFilters{
				Category: category, Tag: tag, Limit: limit,
			})
			if err != nil {
				return err
			}

			for _, e := range entries {
				fmt.Printf("#%d [%s] %s\n", e.ID, e.Category, e.Title)
				if len(e.Tags) > 0 {
					fmt.Printf("    tags: %s\n", strings.Join(e.Tags, ", "))
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&category, "category", "", "filter by category")
	cmd.Flags().StringVar(&tag, "tag", "", "filter by tag")
	cmd.Flags().IntVar(&limit, "limit", 50, "max entries")
	return cmd
}

func newKnowledgeShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Print a knowledge entry as JSON",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid id: %w", err)
			}

			ctx := context.Background()
			cfg, _, err := loadCmdConfig(cmd)
			if err != nil {
				return err
			}
			db, err := storage.Connect(ctx, cfg.DatabaseURL)
			if err != nil {
				return err
			}
			defer db.Close()

			e, err := db.GetKnowledgeEntry(ctx, id)
			if err != nil {
				return err
			}
			if e == nil {
				return fmt.Errorf("no entry with id %d", id)
			}
			out, _ := json.MarshalIndent(e, "", "  ")
			fmt.Println(string(out))
			return nil
		},
	}
}

func newKnowledgeDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a knowledge entry (and its chunk + anchor edges)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid id: %w", err)
			}

			ctx := context.Background()
			cfg, _, err := loadCmdConfig(cmd)
			if err != nil {
				return err
			}
			db, err := storage.Connect(ctx, cfg.DatabaseURL)
			if err != nil {
				return err
			}
			defer db.Close()

			n, err := db.DeleteKnowledgeEntry(ctx, id)
			if err != nil {
				return err
			}
			if n == 0 {
				return fmt.Errorf("no entry with id %d", id)
			}
			fmt.Printf("deleted entry %d\n", id)
			return nil
		},
	}
}

func newKnowledgeSearchCmd() *cobra.Command {
	var category, anchor string
	var limit int
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search knowledge by query and/or anchor",
		Args:  cobra.MinimumNArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := strings.Join(args, " ")
			if query == "" && anchor == "" {
				return fmt.Errorf("provide a query or --anchor")
			}

			ctx := context.Background()
			cfg, _, err := loadCmdConfig(cmd)
			if err != nil {
				return err
			}
			db, err := storage.Connect(ctx, cfg.DatabaseURL)
			if err != nil {
				return err
			}
			defer db.Close()

			if anchor != "" {
				parts := strings.SplitN(anchor, ":", 2)
				if len(parts) != 2 {
					return fmt.Errorf("--anchor must be type:ref")
				}
				hits, err := db.KnowledgeForAnchor(ctx,
					storage.AnchorRequest{Type: parts[0], Ref: parts[1]}, limit)
				if err != nil {
					return err
				}
				for _, e := range hits {
					fmt.Printf("#%d [%s] %s\n", e.ID, e.Category, e.Title)
				}
				return nil
			}

			return fmt.Errorf("vector search is MCP-only; use --anchor here, or call search_knowledge via MCP")
		},
	}
	cmd.Flags().StringVar(&category, "category", "", "filter by category")
	cmd.Flags().StringVar(&anchor, "anchor", "", "anchor in form type:ref (e.g., symbol:Foo)")
	cmd.Flags().IntVar(&limit, "limit", 10, "max results")
	return cmd
}
