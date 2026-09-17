package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"codeflow/internal/analyzer/workspace"
	"codeflow/internal/presenter/flowview"
)

func (s *Server) getSnapshotEngine(absTarget string) (*workspace.SnapshotEngine, error) {
	if val, ok := s.liveServers.Load(absTarget); ok {
		if server, ok := val.(*flowview.Server); ok && server != nil && server.SnapshotEngine() != nil {
			return server.SnapshotEngine(), nil
		}
	}
	coordinator, err := s.getLiveCoordinator(absTarget)
	if err != nil {
		return nil, err
	}
	return coordinator.SnapshotEngine(), nil
}

// getLiveCoordinator lazily creates the FlowView coordinator for a repository.
func (s *Server) getLiveCoordinator(absTarget string) (*flowview.Server, error) {
	canonicalTarget, err := filepath.EvalSymlinks(absTarget)
	if err != nil {
		return nil, fmt.Errorf("resolve live coordinator workspace: %w", err)
	}
	absTarget = filepath.Clean(canonicalTarget)

	if val, ok := s.liveServers.Load(absTarget); ok {
		server, ok := val.(*flowview.Server)
		if !ok || server == nil {
			return nil, fmt.Errorf("invalid live coordinator for %s", absTarget)
		}
		return server, nil
	}

	server, err := flowview.NewServer(flowview.Config{
		RepoRoot: absTarget,
		Port:     0,
		SvelteUI: true,
	})
	if err != nil {
		return nil, fmt.Errorf("start live coordinator: %w", err)
	}
	server.Start()
	s.liveServers.Store(absTarget, server)
	return server, nil
}

func (s *Server) closeLiveServers() {
	s.liveServers.Range(func(key, val any) bool {
		if server, ok := val.(*flowview.Server); ok && server != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = server.Shutdown(ctx)
			cancel()
		}
		return true
	})
}
