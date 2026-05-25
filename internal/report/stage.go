package report

import "github.com/hman-pro/projectlens/internal/indexstate"

// StageDetail is the report-only DTO that embeds StageFreshness and adds
// provider identity, run metrics, error text, and duration. MCP continues
// to use indexstate.StageFreshness directly; this type is never exposed
// through the MCP index_status handler.
type StageDetail struct {
	indexstate.StageFreshness
	Providers       StageProviders `json:"providers"`
	Metrics         map[string]any `json:"metrics,omitempty"`
	Error           string         `json:"error,omitempty"`
	DurationSeconds float64        `json:"duration_seconds,omitempty"`
}

// StageProviders records which embedding and summarization providers were
// active during the run. Either field may be empty when a stage does not
// use that provider role.
type StageProviders struct {
	Embed     string `json:"embed,omitempty"`
	Summarize string `json:"summarize,omitempty"`
}
