// Package mcpserver exposes ProjectLens's retrieval capabilities via the
// Model Context Protocol (MCP) over Streamable HTTP. It registers 10
// tools (see toolRegistry) that an agent can call to search symbols,
// query code semantically, inspect symbol context, summarize packages,
// look up database table schemas, check index freshness, show change
// history, analyse co-change coupling, and save/search captured
// knowledge.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/hman-pro/projectlens/internal/retrieval"
	"github.com/hman-pro/projectlens/internal/storage"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// SummarizerProber reports the state of the configured summarization
// provider. The returned provider string is surfaced as
// ProviderHealth.Provider. State must be one of:
//   - "reachable":     a probe ran and the provider responded.
//   - "configured":    credentials are present but no probe ran
//     (the probe is too expensive to invoke on every status call —
//     e.g. Anthropic, where pinging costs tokens).
//   - "not_configured": credentials missing or no provider wired.
//   - "error":         a probe ran and failed; err carries the cause.
//
// err is non-nil only when state == "error".
type SummarizerProber interface {
	ProbeSummarizer(ctx context.Context) (provider string, state string, err error)
}

// Server wraps the dependencies needed by the MCP tool handlers.
type Server struct {
	db         *storage.DB
	router     *retrieval.Router
	port       int
	repoPath   string
	summarizer SummarizerProber // optional; may be nil
}

// New creates a new MCP server with the given dependencies.
func New(db *storage.DB, router *retrieval.Router, port int, repoPath string) *Server {
	return &Server{
		db:       db,
		router:   router,
		port:     port,
		repoPath: repoPath,
	}
}

// WithSummarizer attaches a SummarizerProber. Returns the same Server
// for chaining at wire-up time. Safe to call before Start.
func (s *Server) WithSummarizer(p SummarizerProber) *Server {
	s.summarizer = p
	return s
}

// Start creates the MCP server, registers all tools, and starts serving over
// Streamable HTTP. It blocks until ctx is cancelled, then shuts down gracefully.
func (s *Server) Start(ctx context.Context) error {
	mcpServer := server.NewMCPServer("projectlens", "1.0.0",
		server.WithHooks(s.loggingHooks()),
	)

	for _, r := range s.toolRegistry() {
		mcpServer.AddTool(r.tool, r.handler)
	}

	httpServer := server.NewStreamableHTTPServer(mcpServer)

	addr := fmt.Sprintf(":%d", s.port)
	log.Printf("projectlens MCP server listening on %s", addr)

	// Start in a goroutine so we can wait for context cancellation.
	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.Start(addr)
	}()

	select {
	case <-ctx.Done():
		log.Println("shutting down MCP server...")
		return httpServer.Shutdown(context.Background())
	case err := <-errCh:
		return err
	}
}

// loggingHooks returns MCP hooks that log tool calls with their arguments,
// duration, and any errors for debugging.
func (s *Server) loggingHooks() *server.Hooks {
	// Track call start times keyed by request ID.
	starts := &sync.Map{}

	hooks := &server.Hooks{}

	hooks.AddBeforeCallTool(func(_ context.Context, id any, msg *mcp.CallToolRequest) {
		starts.Store(id, time.Now())
		args, _ := json.Marshal(msg.Params.Arguments)
		log.Printf("tool call  %-25s args=%s", msg.Params.Name, args)
	})

	hooks.AddAfterCallTool(func(_ context.Context, id any, msg *mcp.CallToolRequest, _ any) {
		dur := "?"
		if t, ok := starts.LoadAndDelete(id); ok {
			dur = time.Since(t.(time.Time)).Round(time.Millisecond).String()
		}
		log.Printf("tool done  %-25s duration=%s", msg.Params.Name, dur)
	})

	hooks.AddOnError(func(_ context.Context, id any, method mcp.MCPMethod, _ any, err error) {
		starts.Delete(id)
		log.Printf("tool error method=%-25s err=%v", method, err)
	})

	hooks.AddOnRegisterSession(func(_ context.Context, _ server.ClientSession) {
		log.Println("session connected")
	})

	hooks.AddOnUnregisterSession(func(_ context.Context, _ server.ClientSession) {
		log.Println("session disconnected")
	})

	return hooks
}

// MCPServer returns the internal mcp-go MCPServer for testing purposes.
// It creates a new server with all tools registered but does not start HTTP.
func (s *Server) MCPServer() *server.MCPServer {
	mcpServer := server.NewMCPServer("projectlens", "1.0.0")
	for _, r := range s.toolRegistry() {
		mcpServer.AddTool(r.tool, r.handler)
	}
	return mcpServer
}
