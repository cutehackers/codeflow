package mcp

import (
	"context"

	"codeflow/internal/flowview"
	"codeflow/internal/semantic"
)

// handleGetSemanticApprovalHistoryJSON is the MCP read-only approval-history
// boundary. Strict decoding runs before authentication, while no trusted
// workspace or actor value is accepted from the request.
func (s *Server) handleGetSemanticApprovalHistoryJSON(ctx context.Context, data []byte) (any, error) {
	envelope, err := semantic.ParseApprovalHistoryQueryEnvelopeJSON(data)
	if err != nil {
		return nil, err
	}
	var token any
	if envelope.Token != nil {
		token = *envelope.Token
	}
	if err := s.checkAuth(token); err != nil {
		return nil, err
	}
	var targetArg any
	if envelope.Target != nil {
		targetArg = *envelope.Target
	}
	access, err := s.authorizeSemanticApproval(ctx, s.rawSemanticApprovalTarget(targetArg))
	if err != nil {
		return nil, err
	}
	targetRoot := access.Workspace().CanonicalRepoRoot()
	if s.approvalHistoryBeforeQueryHook != nil {
		if err := s.approvalHistoryBeforeQueryHook(ctx); err != nil {
			return nil, err
		}
	}
	if value, ok := s.liveServers.Load(targetRoot); ok {
		if coordinator, ok := value.(*flowview.Server); ok && coordinator != nil {
			return coordinator.QuerySemanticApprovalHistory(ctx, access, envelope.Query)
		}
	}
	service, err := semantic.NewApprovalHistoryService(targetRoot)
	if err != nil {
		return nil, err
	}
	return service.QueryApprovalHistory(ctx, access, envelope.Query)
}
