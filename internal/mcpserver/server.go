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
	"net/http"
	"sync"
	"time"

	"github.com/hman-pro/projectlens/internal/indexstate"
	"github.com/hman-pro/projectlens/internal/retrieval"
	"github.com/hman-pro/projectlens/internal/storage"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Server wraps the dependencies needed by the MCP tool handlers.
type Server struct {
	db         *storage.DB
	router     *retrieval.Router
	port       int
	repoPath   string
	summarizer SummarizerProber // optional; may be nil
	inspector  indexstate.Inspector
}

// New creates a new MCP server with the given dependencies.
func New(db *storage.DB, router *retrieval.Router, port int, repoPath string) *Server {
	di := &indexstate.DefaultInspector{RepoPath: repoPath}
	if router != nil {
		di.Embedder = router
	}
	return &Server{
		db:        db,
		router:    router,
		port:      port,
		repoPath:  repoPath,
		inspector: di,
	}
}

// WithSummarizer attaches a SummarizerProber. Returns the same Server
// for chaining at wire-up time. Safe to call before Start.
func (s *Server) WithSummarizer(p SummarizerProber) *Server {
	s.summarizer = p
	if di, ok := s.inspector.(*indexstate.DefaultInspector); ok {
		di.Summarizer = p
	}
	return s
}

// handlerTimeout caps every tool handler so a slow query or stuck DB
// connection cannot wedge the MCP request indefinitely.
const handlerTimeout = 30 * time.Second

// withTimeout wraps a tool handler with a context deadline.
func withTimeout(d time.Duration, h server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ctx, cancel := context.WithTimeout(ctx, d)
		defer cancel()
		return h(ctx, req)
	}
}

// Start creates the MCP server, registers all tools, and starts serving over
// Streamable HTTP. It blocks until ctx is cancelled, then shuts down gracefully.
func (s *Server) Start(ctx context.Context) error {
	mcpServer := server.NewMCPServer("projectlens", "1.0.0",
		server.WithHooks(s.loggingHooks()),
	)

	for _, r := range s.toolRegistry() {
		mcpServer.AddTool(r.tool, withTimeout(handlerTimeout, r.handler))
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
		mcpServer.AddTool(r.tool, withTimeout(handlerTimeout, r.handler))
	}
	return mcpServer
}

// Handler returns an http.Handler that serves this Server's MCP tools over
// Streamable HTTP at the caller-supplied mount point. Each invocation creates
// a fresh MCPServer + session manager so multiple projects can be mounted
// independently in one process.
func (s *Server) Handler() http.Handler {
	mcpServer := server.NewMCPServer("projectlens", "1.0.0",
		server.WithHooks(s.loggingHooks()),
	)
	for _, r := range s.toolRegistry() {
		mcpServer.AddTool(r.tool, withTimeout(handlerTimeout, r.handler))
	}
	return server.NewStreamableHTTPServer(mcpServer)
}
