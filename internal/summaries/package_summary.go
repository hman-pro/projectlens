package summaries

import (
	"context"
	"fmt"
	"unicode"

	"github.com/hman-pro/projectlens/internal/logger"
	"github.com/hman-pro/projectlens/internal/parser"
	"github.com/hman-pro/projectlens/internal/providers/identity"
)

// PackageSummarizer generates a summary for a single package given its name
// and exported symbol signatures. This interface allows mocking in tests.
type PackageSummarizer interface {
	GeneratePackageSummary(ctx context.Context, packageName string, exportedSymbols []string) (string, error)
	SummaryIdentity() identity.ProviderIdentity
}

// GeneratePackageSummaries produces an LLM-generated summary for each package
// in the provided map. Only exported symbols are sent to the LLM. Progress is
// logged as "package N of M".
//
// The packages map is keyed by package name (e.g., "parser") with a slice of
// all symbols extracted from that package.
func GeneratePackageSummaries(ctx context.Context, summarizer PackageSummarizer, packages map[string][]parser.Symbol) (map[string]string, error) {
	return generatePackageSummariesWith(ctx, summarizer, packages)
}

// generatePackageSummariesWith is the internal implementation that accepts the
// PackageSummarizer interface so it can be tested with a mock.
func generatePackageSummariesWith(ctx context.Context, summarizer PackageSummarizer, packages map[string][]parser.Symbol) (map[string]string, error) {
	result := make(map[string]string, len(packages))
	total := len(packages)
	i := 0

	for pkgName, symbols := range packages {
		i++
		logger.Progress("generating package summaries", i, total, "package", pkgName)

		sigs := exportedSignatures(symbols)
		if len(sigs) == 0 {
			result[pkgName] = "Package has no exported symbols."
			continue
		}

		summary, err := summarizer.GeneratePackageSummary(ctx, pkgName, sigs)
		if err != nil {
			return nil, fmt.Errorf("generating summary for package %q: %w", pkgName, err)
		}

		result[pkgName] = summary
	}

	return result, nil
}

// exportedSignatures filters symbols to exported ones and returns their
// signature strings.
func exportedSignatures(symbols []parser.Symbol) []string {
	var sigs []string
	for _, sym := range symbols {
		if len(sym.Name) > 0 && unicode.IsUpper(rune(sym.Name[0])) {
			sig := sym.Signature
			if sig == "" {
				sig = sym.Kind + " " + sym.Name
			}
			sigs = append(sigs, sig)
		}
	}
	return sigs
}
