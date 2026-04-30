package store

import "time"

type HealthSnapshot struct {
	StartedAt        time.Time
	CompletedAt      *time.Time
	CommitSHA        string
	Stage            string
	Status           string
	FilesProcessed   int
	SymbolsExtracted int
	EdgesCreated     int
	HeadCommit       string
	Staleness        time.Duration
}

func (h HealthSnapshot) Duration() time.Duration {
	if h.CompletedAt == nil {
		return 0
	}
	return h.CompletedAt.Sub(h.StartedAt)
}

type StageStat struct {
	Name             string
	LastRunStartedAt time.Time
	Status           string
	FilesProcessed   int
	Duration         time.Duration
}

type PipelineSnapshot struct {
	Stages []StageStat
}

type TableStat struct {
	Name    string
	EstRows int64
	Bytes   int64
}

type ChunkStats struct {
	Total    int64
	Embedded int64
	ByType   map[string]int64
}

type StorageSnapshot struct {
	Tables []TableStat
	Chunks ChunkStats
}

type IndexRun struct {
	ID               int64
	StartedAt        time.Time
	CompletedAt      *time.Time
	CommitSHA        string
	Stage            string
	Status           string
	FilesProcessed   int
	SymbolsExtracted int
	EdgesCreated     int
}

func (r IndexRun) Duration() time.Duration {
	if r.CompletedAt == nil {
		return 0
	}
	return r.CompletedAt.Sub(r.StartedAt)
}

type RunsSnapshot struct {
	Runs []IndexRun
}

type ConfigSnapshot struct {
	EmbeddingProvider     string
	EmbeddingModel        string
	EmbeddingDims         int
	EmbeddingEndpoint     string
	SummarizationProvider string
	SummarizationModel    string
	DBHost                string
	DBName                string
	MCPURL                string
	MCPStatus             string
	MCPLatency            time.Duration
	MCPError              string
}
