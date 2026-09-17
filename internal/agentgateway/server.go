// Package agentgateway exposes the 8 core Model Context Protocol (MCP) tools over stdio JSON-RPC.
package agentgateway

import (
	"context"
	"io"

	"codeflow/internal/mcp"
)

// Config configures the MCP agent gateway.
type Config = mcp.Config

// Server handles JSON-RPC tool calls for AI agents.
type Server struct {
	inner *mcp.Server
}

// NewServer creates a new agent gateway server.
func NewServer(cfg Config) (*Server, error) {
	s, err := mcp.NewServer(cfg)
	if err != nil {
		return nil, err
	}
	return &Server{inner: s}, nil
}

// Serve reads JSON-RPC requests from in and writes responses to out until EOF.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	return s.inner.Serve(ctx, in, out)
}

// Close releases server resources and adapters.
func (s *Server) Close() error {
	return s.inner.Close()
}
