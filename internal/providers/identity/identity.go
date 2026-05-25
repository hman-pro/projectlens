// Package identity defines the ProviderIdentity type used to identify which
// vendor and model handled a specific role (embedding or summarization).
package identity

import "fmt"

// ProviderIdentity describes the vendor, model, and optional output dimensions
// for a provider role. Dimensions is 0 when not applicable (e.g. summarizers).
type ProviderIdentity struct {
	Vendor     string // "openai", "azure-openai", "ollama", "anthropic"
	Model      string // executed model, not configured alias
	Dimensions int    // 0 when not applicable (summarizers)
}

// String returns a compact human-readable label, e.g.
// "openai:text-embedding-3-large@1024" or "anthropic:claude-sonnet-4-6".
// Returns "" when Vendor or Model is empty.
func (p ProviderIdentity) String() string {
	if p.Vendor == "" || p.Model == "" {
		return ""
	}
	if p.Dimensions > 0 {
		return fmt.Sprintf("%s:%s@%d", p.Vendor, p.Model, p.Dimensions)
	}
	return fmt.Sprintf("%s:%s", p.Vendor, p.Model)
}
