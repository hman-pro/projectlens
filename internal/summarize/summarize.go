package summarize

import (
	"context"
	"fmt"
	"time"

	"github.com/hman-pro/projectlens/internal/logger"
	"github.com/hman-pro/projectlens/internal/storage"
	"github.com/hman-pro/projectlens/internal/summaries"
)

// SummarizeMissing finds packages without summaries and generates them.
// Returns the number of packages successfully summarized (heuristic + LLM).
func SummarizeMissing(ctx context.Context, db *storage.DB, summarizer summaries.PackageSummarizer) (int, error) {
	startTime := time.Now()
	logger.Step("Summarize missing packages")

	allPackages, err := db.GetDistinctPackageNames(ctx)
	if err != nil {
		return 0, fmt.Errorf("summarize: get packages: %w", err)
	}

	existing, err := db.GetAllSummaryPackageNames(ctx)
	if err != nil {
		return 0, fmt.Errorf("summarize: get existing summaries: %w", err)
	}

	existingSet := make(map[string]bool, len(existing))
	for _, name := range existing {
		existingSet[name] = true
	}

	var missing []string
	for _, pkg := range allPackages {
		if !existingSet[pkg] {
			missing = append(missing, pkg)
		}
	}

	if len(missing) == 0 {
		logger.Info("all packages already have summaries — nothing to do")
		return 0, nil
	}

	logger.Info("found packages missing summaries", "count", len(missing))

	summarized := 0
	for i, pkgName := range missing {
		logger.Progress("summarizing packages", i+1, len(missing), "package", pkgName)

		syms, err := db.GetSymbolsByPackage(ctx, pkgName)
		if err != nil {
			logger.Warn("could not get symbols", "package", pkgName, "err", err)
			continue
		}

		var sigs []string
		for _, s := range syms {
			if len(s.Name) > 0 && s.Name[0] >= 'A' && s.Name[0] <= 'Z' {
				sig := s.Signature
				if sig == "" {
					sig = s.Kind + " " + s.Name
				}
				sigs = append(sigs, sig)
			}
		}

		if len(sigs) == 0 {
			rec := &storage.SummaryRecord{
				PackageName:  pkgName,
				SummaryText:  "Package has no exported symbols.",
				ModelVersion: "heuristic",
			}
			if err := db.UpsertSummary(ctx, rec); err == nil {
				summarized++
			}
			continue
		}

		summary, err := summarizer.GeneratePackageSummary(ctx, pkgName, sigs)
		if err != nil {
			logger.Warn("could not summarize package", "package", pkgName, "err", err)
			continue
		}

		rec := &storage.SummaryRecord{
			PackageName:  pkgName,
			SummaryText:  summary,
			ModelVersion: "llm",
		}
		if err := db.UpsertSummary(ctx, rec); err != nil {
			logger.Warn("could not store summary", "package", pkgName, "err", err)
			continue
		}
		summarized++
	}

	logger.Info("summarized packages", "count", summarized, "elapsed", time.Since(startTime).Round(time.Millisecond))
	return summarized, nil
}
