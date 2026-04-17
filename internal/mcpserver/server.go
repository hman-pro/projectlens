// Package mcpserver exposes ProjectLens's retrieval capabilities via the
// Model Context Protocol (MCP) over Streamable HTTP. It registers 8 tools
// that Claude Code can call to search symbols, query code semantically,
// inspect symbol context, summarize packages, look up database table schemas,
// check index freshness, show change history, and analyse co-change coupling.
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

// Server wraps the dependencies needed by the MCP tool handlers.
type Server struct {
	db       *storage.DB
	router   *retrieval.Router
	port     int
	repoPath string
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

// Start creates the MCP server, registers all tools, and starts serving over
// Streamable HTTP. It blocks until ctx is cancelled, then shuts down gracefully.
func (s *Server) Start(ctx context.Context) error {
	mcpServer := server.NewMCPServer("projectlens", "1.0.0",
		server.WithHooks(s.loggingHooks()),
	)

	// Register all tools.
	mcpServer.AddTool(findSymbolTool(), s.handleFindSymbol)
	mcpServer.AddTool(searchGoContextTool(), s.handleSearchGoContext)
	mcpServer.AddTool(getSymbolContextTool(), s.handleGetSymbolContext)
	mcpServer.AddTool(getPackageSummaryTool(), s.handleGetPackageSummary)
	mcpServer.AddTool(getTableContextTool(), s.handleGetTableContext)
	mcpServer.AddTool(indexStatusTool(), s.handleIndexStatus)
	mcpServer.AddTool(getChangeHistoryTool(), s.handleGetChangeHistory)
	mcpServer.AddTool(getCouplingTool(), s.handleGetCoupling)

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
	mcpServer.AddTool(findSymbolTool(), s.handleFindSymbol)
	mcpServer.AddTool(searchGoContextTool(), s.handleSearchGoContext)
	mcpServer.AddTool(getSymbolContextTool(), s.handleGetSymbolContext)
	mcpServer.AddTool(getPackageSummaryTool(), s.handleGetPackageSummary)
	mcpServer.AddTool(getTableContextTool(), s.handleGetTableContext)
	mcpServer.AddTool(indexStatusTool(), s.handleIndexStatus)
	mcpServer.AddTool(getChangeHistoryTool(), s.handleGetChangeHistory)
	mcpServer.AddTool(getCouplingTool(), s.handleGetCoupling)
	return mcpServer
}
