// Package mcpserver exposes ProjectLens's retrieval capabilities via the
// Model Context Protocol (MCP) over Streamable HTTP. It registers 5 tools
// that Claude Code can call to search symbols, query code semantically,
// inspect symbol context, summarize packages, and check index freshness.
package mcpserver

import (
	"context"
	"fmt"
	"log"

	"github.com/hman-pro/projectlens/internal/openai"
	"github.com/hman-pro/projectlens/internal/retrieval"
	"github.com/hman-pro/projectlens/internal/storage"
	"github.com/mark3labs/mcp-go/server"
)

// Server wraps the dependencies needed by the MCP tool handlers.
type Server struct {
	db     *storage.DB
	router *retrieval.Router
	oai    *openai.Client
	port   int
}

// New creates a new MCP server with the given dependencies.
func New(db *storage.DB, router *retrieval.Router, oai *openai.Client, port int) *Server {
	return &Server{
		db:     db,
		router: router,
		oai:    oai,
		port:   port,
	}
}

// Start creates the MCP server, registers all tools, and starts serving over
// Streamable HTTP. It blocks until ctx is cancelled, then shuts down gracefully.
func (s *Server) Start(ctx context.Context) error {
	mcpServer := server.NewMCPServer("projectlens", "1.0.0")

	// Register all tools.
	mcpServer.AddTool(findSymbolTool(), s.handleFindSymbol)
	mcpServer.AddTool(searchGoContextTool(), s.handleSearchGoContext)
	mcpServer.AddTool(getSymbolContextTool(), s.handleGetSymbolContext)
	mcpServer.AddTool(getPackageSummaryTool(), s.handleGetPackageSummary)
	mcpServer.AddTool(indexStatusTool(), s.handleIndexStatus)

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

// MCPServer returns the internal mcp-go MCPServer for testing purposes.
// It creates a new server with all tools registered but does not start HTTP.
func (s *Server) MCPServer() *server.MCPServer {
	mcpServer := server.NewMCPServer("projectlens", "1.0.0")
	mcpServer.AddTool(findSymbolTool(), s.handleFindSymbol)
	mcpServer.AddTool(searchGoContextTool(), s.handleSearchGoContext)
	mcpServer.AddTool(getSymbolContextTool(), s.handleGetSymbolContext)
	mcpServer.AddTool(getPackageSummaryTool(), s.handleGetPackageSummary)
	mcpServer.AddTool(indexStatusTool(), s.handleIndexStatus)
	return mcpServer
}
