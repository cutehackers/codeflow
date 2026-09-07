package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"codeflow/internal/protocol"
	"codeflow/internal/semantic"
	"codeflow/internal/storage"
	"time"
)

// handleSemanticEnrichment is the MCP counterpart of FlowView's optional Q4
// enrichment endpoint. The map is resolved from the server-side deterministic
// cache and the snapshot is captured by the shared workspace engine. Request
// arguments never carry source bytes, a repository path, or a model result.
func (s *Server) handleSemanticEnrichment(ctx context.Context, args map[string]any) (any, error) {
	if err := s.checkAuth(args["token"]); err != nil {
		return nil, err
	}

	var requestedTarget string
	if rawTarget, ok := args["target"]; ok && rawTarget != nil {
		var validType bool
		requestedTarget, validType = rawTarget.(string)
		if !validType {
			return nil, &semantic.ApprovalUnauthorizedError{Reason: "requested workspace target is invalid"}
		}
	}
	access, err := s.approvalGate.AuthenticateAndAuthorize(ctx, s.approvalWorkspaceID, requestedTarget)
	if err != nil {
		return nil, err
	}
	workspace := access.Workspace()
	targetRoot := workspace.CanonicalRepoRoot()
	workspaceID := workspace.WorkspaceID()
	generationID := stringArgument(args, "generationId")
	basisID := stringArgument(args, "computedBasisId")
	mapIR, ok := s.loadEnrichmentSemanticMap(targetRoot, generationID, basisID)
	if !ok {
		return &semantic.EnrichmentResult{State: unavailableEnrichmentState("no current deterministic semantic map is available")}, nil
	}

	requestCtx, modelHostFactory, releaseModelHost, err := s.beginModelHostRequest(ctx)
	if err != nil {
		fallback := semantic.BuildDeterministicFallback(mapIR, stringArgument(args, "targetStepId"), err.Error())
		return &semantic.EnrichmentResult{State: unavailableEnrichmentStateWithFallback(err.Error(), fallback), Fallback: fallback}, nil
	}
	defer releaseModelHost()
	snapshot, releaseSnapshot, err := s.captureAnalysisSnapshot(requestCtx, targetRoot)
	if err != nil {
		fallback := semantic.BuildDeterministicFallback(mapIR, stringArgument(args, "targetStepId"), err.Error())
		return &semantic.EnrichmentResult{
			State:    unavailableEnrichmentStateWithFallback(err.Error(), fallback),
			Fallback: fallback,
		}, nil
	}
	defer releaseSnapshot()
	var currentProof *storage.GenerationProofManifest
	var currentProofBytes, semanticMapBytes []byte
	var currentPointer *storage.ActivePointer
	if st, storageErr := s.getStorage(targetRoot); storageErr == nil {
		if publication, readErr := st.ReadValidatedActiveProofBundle(); readErr == nil && publication != nil {
			currentProof, currentProofBytes, currentPointer, semanticMapBytes = publication.Manifest, publication.ManifestBytes, publication.Pointer, publication.SemanticMap
		}
	}

	targetIDs := enrichmentStringList(args["targetStepIds"])
	if targetID := stringArgument(args, "targetStepId"); targetID != "" {
		targetIDs = append(targetIDs, targetID)
	}
	request := semantic.EnrichmentRequest{
		EvidencePack: semantic.EvidencePackRequest{
			Map:                mapIR,
			Snapshot:           snapshot,
			CurrentProof:       currentProof,
			CurrentProofBytes:  currentProofBytes,
			CurrentPointer:     currentPointer,
			SemanticMapBytes:   semanticMapBytes,
			LiveHeadSnapshotID: snapshot.SnapshotID,
			SnapshotID:         snapshot.SnapshotID,
			ComputedBasisID:    snapshot.ComputedBasisID,
			GenerationID:       mapIR.GenerationID,
			RepositoryID:       mapIR.Basis.RepositoryID,
			WorktreeID:         mapIR.Basis.WorktreeID,
			TargetStepIDs:      targetIDs,
			TargetSymbolPath:   firstEnrichmentArgument(args, "targetSymbolPath", "symbolPath"),
			ScopePaths:         enrichmentStringList(args["scopePaths"]),
			PromptRevision:     stringArgument(args, "promptRevision"),
		},
		ModelHostFactory: modelHostFactory,
		PromptRevision:   stringArgument(args, "promptRevision"),
	}
	result, persistErr := semantic.RunSemanticEnrichmentAndPersist(requestCtx, request, s.proposalStore, workspaceID)
	if persistErr != nil {
		if errors.Is(persistErr, context.Canceled) || errors.Is(persistErr, context.DeadlineExceeded) {
			return nil, persistErr
		}
		return nil, fmt.Errorf("persist available semantic enrichment: %w", semantic.ErrProposalInvalid)
	}
	return &result, nil
}

func firstEnrichmentArgument(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringArgument(args, key); value != "" {
			return value
		}
	}
	return ""
}

func enrichmentStringList(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, text)
			}
		}
		return out
	case nil:
		return nil
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return nil
		}
		var out []string
		if json.Unmarshal(data, &out) != nil {
			return nil
		}
		return out
	}
}

func unavailableEnrichmentState(reason string) semantic.EnrichmentState {
	return unavailableEnrichmentStateWithFallback(reason, nil)
}

func unavailableEnrichmentStateWithFallback(reason string, fallback *semantic.DeterministicFallback) semantic.EnrichmentState {
	if strings.TrimSpace(reason) == "" {
		reason = "semantic enrichment is unavailable"
	}
	return semantic.EnrichmentState{
		SchemaID:      semantic.EnrichmentStateV2SchemaID,
		SchemaVersion: 2,
		Status:        "unavailable",
		Reason:        reason,
		Fallback:      fallback,
		Capability:    protocolUnavailableModelCapability(),
		UpdatedAt:     enrichmentTimestamp(),
	}
}

// Keep protocol-specific construction in this file's small boundary helper so
// the handler's public result does not expose a synthetic measured capability.
func protocolUnavailableModelCapability() protocol.ModelHostCapability {
	return protocol.ModelHostCapability{Status: "unavailable"}
}

func enrichmentTimestamp() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
