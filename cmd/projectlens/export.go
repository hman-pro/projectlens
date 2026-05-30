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
			cs, err := openCmdStorage(ctx, cmd)
			if err != nil {
				return err
			}
			defer cs.Close()
			db := cs.DB()
			cfg := cs.Config()
			repoPath := cs.RepoPath()

			if slug := cs.Slug(); slug != "" {
				fmt.Fprintf(os.Stderr, "project: %s (storage_schema=%s)\n", slug, cs.StorageSchema())
			}

			insp := buildInspector(cfg, db, repoPath)

			var diag export.Diagnostics
			if err := writeExport(out, func(w io.Writer) error {
				d, e := export.NewGraphExporter(db, insp).Export(ctx, w, export.Options{
					Edges:           parsed,
					IncludeEvidence: includeEvidence,
				})
				diag = d
				return e
			}); err != nil {
				return err
			}
			reportSkippedEdges(os.Stderr, diag)
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "write to this file (default stdout)")
	cmd.Flags().StringVar(&edges, "edges", "all", "comma-separated edge types or 'all'")
	cmd.Flags().BoolVar(&includeEvidence, "include-evidence", false, "include properties.evidence blobs")
	return cmd
}

// reportSkippedEdges surfaces dropped edges loudly on stderr so a partial
// graph is never mistaken for a complete one. It prints a count plus up to
// a few samples; the full list rides along in the artifact's "diagnostics".
func reportSkippedEdges(w io.Writer, diag export.Diagnostics) {
	n := len(diag.SkippedEdges)
	if n == 0 {
		return
	}
	const maxSamples = 5
	fmt.Fprintf(w, "WARN: %d edge(s) skipped to keep the graph closed (see diagnostics.skipped_edges)\n", n)
	for i, e := range diag.SkippedEdges {
		if i >= maxSamples {
			fmt.Fprintf(w, "  ... and %d more\n", n-maxSamples)
			break
		}
		fmt.Fprintf(w, "  - %s edge %s -> %s: %s\n", e.Type, e.Source, e.Target, e.Reason)
	}
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
