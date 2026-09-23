// Package presenter hosts the interactive FlowView workbench and serves the 12 essential REST APIs.
package presenter

import (
	"context"
	"net/http"

	"codeflow/internal/presenter/flowview"
)

// Config configures the FlowView presentation server.
type Config = flowview.Config

// Server coordinates HTTP presentation and UI asset delivery.
type Server struct {
	inner *flowview.Server
}

// NewServer creates a presenter server wrapping flowview.Server.
func NewServer(cfg Config) (*Server, error) {
	s, err := flowview.NewServer(cfg)
	if err != nil {
		return nil, err
	}
	return &Server{inner: s}, nil
}

// Start launches the loopback HTTP listener.
func (s *Server) Start() {
	s.inner.Start()
}

// Shutdown gracefully terminates the HTTP listener.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.inner.Shutdown(ctx)
}

// Addr returns the listening host:port address.
func (s *Server) Addr() string {
	return s.inner.Addr()
}

// URL returns the local browser URL for the workbench.
func (s *Server) URL() string {
	return s.inner.URL()
}

// AuthToken returns the active session security token.
func (s *Server) AuthToken() string {
	return s.inner.AuthToken()
}

// Handler returns the underlying http.Handler for testing or mounting.
func (s *Server) Handler() http.Handler {
	return s.inner.Handler()
}

// SaveTaskView preserves a validated imported hierarchy in the local view store.
func (s *Server) SaveTaskView(ctx context.Context, view map[string]any) (map[string]any, error) {
	return s.inner.SaveTaskView(ctx, view)
}

// TaskViewURL opens one immutable stored analysis without starting analysis.
func (s *Server) TaskViewURL(id string) string {
	return s.inner.TaskViewURL(id)
}
