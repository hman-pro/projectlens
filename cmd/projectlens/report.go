package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hman-pro/projectlens/internal/report"
)

func newReportCmd() *cobra.Command {
	var format string
	var out string
	var topN int

	cmd := &cobra.Command{
		Use:   "report",
		Short: "Generate a summary report of the indexed state",
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolvedFormat, err := resolveFormat(format, out)
			if err != nil {
				return err
			}
			if topN < 1 || topN > 200 {
				return fmt.Errorf("--top out of range (1..200): %d", topN)
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

			r, err := report.NewBuilder(db, insp, repoPath, report.Options{TopN: topN}).Build(ctx)
			if err != nil {
				return err
			}

			return writeReport(out, resolvedFormat, r)
		},
	}
	cmd.Flags().StringVar(&format, "format", "", "output format: markdown|json (default markdown; inferred from --out extension)")
	cmd.Flags().StringVar(&out, "out", "", "write to this file (default stdout)")
	cmd.Flags().IntVar(&topN, "top", 10, "top-N for packages, tables, coupling, recent knowledge")
	return cmd
}

func resolveFormat(format, out string) (string, error) {
	if format != "" {
		switch format {
		case "markdown", "json":
			return format, nil
		default:
			return "", fmt.Errorf("invalid --format %q (want markdown|json)", format)
		}
	}
	if out == "" {
		return "markdown", nil
	}
	switch strings.ToLower(filepath.Ext(out)) {
	case ".md", ".markdown":
		return "markdown", nil
	case ".json":
		return "json", nil
	default:
		return "", fmt.Errorf("cannot infer --format from extension %q; pass --format", filepath.Ext(out))
	}
}

func writeReport(out, format string, r *report.Report) error {
	render := func(w io.Writer) error {
		switch format {
		case "json":
			return report.JSONRenderer{}.Render(w, r)
		default:
			return report.MarkdownRenderer{}.Render(w, r)
		}
	}
	if out == "" {
		return render(os.Stdout)
	}
	dir := filepath.Dir(out)
	tmp, err := os.CreateTemp(dir, ".projectlens-report-*")
	if err != nil {
		return fmt.Errorf("report: temp file: %w", err)
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
		return fmt.Errorf("report: rename: %w", err)
	}
	return nil
}
