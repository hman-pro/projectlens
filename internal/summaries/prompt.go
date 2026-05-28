package summaries

import "strings"

// BuildPackageSummaryPrompt constructs the LLM prompt used to summarise a Go
// package given its name and exported symbol signatures.
func BuildPackageSummaryPrompt(packageName string, exportedSymbols []string) string {
	var b strings.Builder
	b.WriteString("You are a Go package documentation expert. Given the following exported symbols from a Go package, write a 2-4 sentence summary of what this package does, when a developer would use it, and its main responsibilities.\n\n")
	b.WriteString("Package: ")
	b.WriteString(packageName)
	b.WriteString("\n\nExported symbols:\n")
	for _, sym := range exportedSymbols {
		b.WriteString(sym)
		b.WriteString("\n")
	}
	b.WriteString("\nWrite a concise summary focused on purpose and usage, not implementation details.")
	return b.String()
}
