package mcp

import (
	"context"
	"fmt"

	"codeflow/internal/analyzer/protocol"
)

// captureAnalysisSnapshot establishes one snapshot lease for the complete MCP
// analysis request. The lease is retained until every adapter call and
// evidence extraction has finished.
func (s *Server) captureAnalysisSnapshot(ctx context.Context, targetRoot string) (protocol.Snapshot, func(), error) {
	engine, err := s.getSnapshotEngine(targetRoot)
	if err != nil {
		return protocol.Snapshot{}, nil, fmt.Errorf("get snapshot engine: %w", err)
	}
	head, err := engine.ReconcileIfChanged(ctx, nil)
	if err != nil {
		return protocol.Snapshot{}, nil, fmt.Errorf("capture workspace snapshot: %w", err)
	}
	lease, err := engine.SnapshotVFS(head.SnapshotID)
	if err != nil {
		return protocol.Snapshot{}, nil, fmt.Errorf("retain workspace snapshot: %w", err)
	}
	snapshot, err := protocol.SnapshotFromLease(lease)
	if err != nil {
		_ = lease.Close()
		return protocol.Snapshot{}, nil, fmt.Errorf("convert workspace snapshot: %w", err)
	}
	return snapshot, func() { _ = lease.Close() }, nil
}
