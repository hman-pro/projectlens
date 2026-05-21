// Package indexstate holds read-only, MCP-server-free types and helpers
// for inspecting ProjectLens's indexed state. Both internal/mcpserver and
// internal/report depend on this package; it must not import either.
package indexstate

// ProviderHealth reports the state of one configured provider. State is
// one of four values:
//   - "reachable":      the provider responded to a cheap probe.
//   - "configured":     credentials/endpoint are set but no probe was run.
//   - "not_configured": no provider is wired, or credentials are missing.
//   - "error":          a probe ran and failed; Error carries the message.
type ProviderHealth struct {
	Role     string `json:"role"`
	Provider string `json:"provider"`
	State    string `json:"state"`
	Error    string `json:"error,omitempty"`
}

// StageFreshness mirrors the per-stage shape used in index_status.
// AgeMinutes is computed at response time from CompletedAt.
type StageFreshness struct {
	Stage          string  `json:"stage"`
	Status         string  `json:"status"`
	CommitSHA      string  `json:"commit_sha,omitempty"`
	StartedAt      string  `json:"started_at,omitempty"`
	CompletedAt    string  `json:"completed_at,omitempty"`
	AgeMinutes     float64 `json:"age_minutes,omitempty"`
	FilesProcessed int     `json:"files_processed,omitempty"`
}

// GitState is the working-tree snapshot at query time.
type GitState struct {
	Head  string `json:"head,omitempty"`
	Dirty bool   `json:"dirty"`
}
