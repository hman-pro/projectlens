package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hman-pro/projectlens/internal/export"
	"github.com/hman-pro/projectlens/internal/storage"
)

func newExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export indexed state to portable artifacts",
	}
	cmd.AddCommand(newExportGraphCmd())
	return cmd
}

func newExportGraphCmd() *cobra.Command {
	var out string
	var edges string
	var includeEvidence bool

	cmd := &cobra.Command{
		Use:   "graph",
		Short: "Stream a full graph dump as native-schema JSON",
		RunE: func(cmd *cobra.Command, _ []string) error {
			parsed, err := parseEdges(edges)
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			cfg, repoPath, err := loadCmdConfig(cmd)
			if err != nil {
				return err
			}
			db, err := storage.Connect(ctx, cfg.DatabaseURL)
			if err != nil {
				return fmt.Errorf("connecting to database: %w", err)
			}
			defer db.Close()

			insp := buildInspector(cfg, db, repoPath)

			return writeExport(out, func(w io.Writer) error {
				return export.NewGraphExporter(db, insp).Export(ctx, w, export.Options{
					Edges:           parsed,
					IncludeEvidence: includeEvidence,
				})
			})
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "write to this file (default stdout)")
	cmd.Flags().StringVar(&edges, "edges", "all", "comma-separated edge types or 'all'")
	cmd.Flags().BoolVar(&includeEvidence, "include-evidence", false, "include properties.evidence blobs")
	return cmd
}

func parseEdges(spec string) ([]string, error) {
	if spec == "" || spec == "all" {
		return nil, nil
	}
	parts := strings.Split(spec, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if !export.IsValidEdgeType(p) {
			return nil, fmt.Errorf("invalid --edges value %q", p)
		}
		out = append(out, p)
	}
	return out, nil
}

func writeExport(out string, render func(io.Writer) error) error {
	if out == "" {
		return render(os.Stdout)
	}
	dir := filepath.Dir(out)
	tmp, err := os.CreateTemp(dir, ".projectlens-export-*")
	if err != nil {
		return fmt.Errorf("export: temp: %w", err)
	}
	tmpName := tmp.Name()
	if err := render(tmp); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, out); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("export: rename: %w", err)
	}
	return nil
}
