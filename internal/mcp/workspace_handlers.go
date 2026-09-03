package mcp

import (
	"context"
	"fmt"
	"path/filepath"

	"codeflow/internal/workspace"
)

func (s *Server) getSnapshotEngine(absTarget string) (*workspace.SnapshotEngine, error) {
	if val, ok := s.engines.Load(absTarget); ok {
		return val.(*workspace.SnapshotEngine), nil
	}
	engine, err := workspace.NewSnapshotEngine(absTarget, "")
	if err != nil {
		return nil, err
	}
	actual, _ := s.engines.LoadOrStore(absTarget, engine)
	return actual.(*workspace.SnapshotEngine), nil
}

func (s *Server) handleGetWorkspaceActivity(ctx context.Context, args map[string]any) (any, error) {
	target := "."
	if t, ok := args["target"].(string); ok && t != "" {
		target = t
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return nil, fmt.Errorf("resolve target: %w", err)
	}

	engine, err := s.getSnapshotEngine(absTarget)
	if err != nil {
		return nil, fmt.Errorf("get snapshot engine: %w", err)
	}

	act := engine.CurrentActivity()
	var liveHeadSnap *workspace.WorkspaceSnapshot
	if head := engine.LiveHead(); head != nil {
		liveHeadSnap = head
	}

	return map[string]any{
		"activity":          act.Activity,
		"analysisLagMs":     act.AnalysisLagMs,
		"pendingRevisions":  act.PendingRevisions,
		"currentSnapshotId": act.CurrentSnapshotID,
		"workspaceEpoch":    act.WorkspaceEpoch,
		"timestamp":         act.Timestamp,
		"scope":             act.Scope,
		"liveHead":          liveHeadSnap,
	}, nil
}

func (s *Server) handleSubmitVersionedEdit(ctx context.Context, args map[string]any) (any, error) {
	target := "."
	if t, ok := args["target"].(string); ok && t != "" {
		target = t
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return nil, fmt.Errorf("resolve target: %w", err)
	}

	path, _ := args["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("missing required field 'path'")
	}

	contentStr, _ := args["content"].(string)
	docVerFloat, ok := args["documentVersion"].(float64)
	if !ok || docVerFloat < 1 {
		return nil, fmt.Errorf("documentVersion must be a positive integer >= 1")
	}
	docVer := int(docVerFloat)

	source, _ := args["source"].(string)
	if source == "" {
		source = workspace.SourceAgentTransaction
	}

	engine, err := s.getSnapshotEngine(absTarget)
	if err != nil {
		return nil, fmt.Errorf("get snapshot engine: %w", err)
	}

	rev, snap, err := engine.ApplyVersionedEdit(ctx, workspace.EditRequest{
		Path:            path,
		Content:         []byte(contentStr),
		DocumentVersion: docVer,
		Source:          source,
	})
	if err != nil {
		return nil, fmt.Errorf("apply versioned edit: %w", err)
	}

	return map[string]any{
		"revision": rev,
		"snapshot": snap,
	}, nil
}

func (s *Server) handleGetGenerationProof(ctx context.Context, args map[string]any) (any, error) {
	if err := s.checkAuth(args["token"]); err != nil {
		return nil, err
	}

	target := s.resolveTarget(args["target"])
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return nil, fmt.Errorf("resolve target: %w", err)
	}

	st, err := s.getStorage(absTarget)
	if err != nil {
		return nil, fmt.Errorf("get storage: %w", err)
	}

	ptr, err := st.ReadActivePointer()
	if err != nil {
		return nil, fmt.Errorf("read active pointer: %w", err)
	}
	manifest, err := st.ReadActiveProofManifest()
	if err != nil {
		return nil, fmt.Errorf("read proof manifest: %w", err)
	}

	var ptrVal any
	if ptr != nil {
		ptrVal = ptr
	}
	var manifestVal any
	if manifest != nil {
		manifestVal = manifest
	}

	return map[string]any{
		"pointer":  ptrVal,
		"manifest": manifestVal,
	}, nil
}

func (s *Server) handleGetVerifiedGap(ctx context.Context, args map[string]any) (any, error) {
	if err := s.checkAuth(args["token"]); err != nil {
		return nil, err
	}

	target := s.resolveTarget(args["target"])
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return nil, fmt.Errorf("resolve target: %w", err)
	}

	st, err := s.getStorage(absTarget)
	if err != nil {
		return nil, fmt.Errorf("get storage: %w", err)
	}
	engine, err := s.getSnapshotEngine(absTarget)
	if err != nil {
		return nil, fmt.Errorf("get snapshot engine: %w", err)
	}

	ptr, err := st.ReadActivePointer()
	if err != nil {
		return nil, fmt.Errorf("read active pointer: %w", err)
	}
	if ptr == nil {
		return map[string]any{
			"status": "no_generation_published",
		}, nil
	}

	liveHead := engine.LiveHead()
	if liveHead == nil || liveHead.SnapshotID == ptr.ExpectedLiveHeadSnapshotID {
		return map[string]any{
			"freshness":    "current",
			"generationId": ptr.GenerationID,
			"settlement":   "evaluated",
		}, nil
	}

	delta, _ := engine.ComputeDelta(ptr.ComputedBasisID, liveHead.SnapshotID)
	curAct := engine.CurrentActivity()

	changedPaths := []string{}
	if delta != nil {
		changedPaths = delta.ChangedPaths
	}

	return map[string]any{
		"freshness":         "last_verified",
		"activity":          curAct.Activity,
		"lastVerifiedGenId": ptr.GenerationID,
		"latestSnapshotId":  liveHead.SnapshotID,
		"affectedScope":     changedPaths,
		"analysisLagMs":     curAct.AnalysisLagMs,
		"pendingRevisions":  curAct.PendingRevisions,
	}, nil
}

