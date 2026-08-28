package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codeflow/internal/contractharness"
	"codeflow/internal/fusion"
	"codeflow/internal/slicing"
	"codeflow/internal/secret"
	"codeflow/internal/storage"
)

const maxCoreArtifactBytes = 512 * 1024 // 512 KiB per spec §5.2

// coreArtifact mirrors schemas/core-artifact.schema.json for MCP input.
type coreArtifact struct {
	FlowID          string     `json:"flowId,omitempty"`
	EntrySymbolPath string     `json:"entrySymbolPath"`
	Title           string     `json:"title"`
	Description     string     `json:"description,omitempty"`
	Layers          []string   `json:"layers,omitempty"`
	Steps           []coreStep `json:"steps"`
	Edges           []coreEdge `json:"edges,omitempty"`
	Unknowns        []fusion.Unknown `json:"unknowns,omitempty"`
}

type coreStep struct {
	Ordinal    int            `json:"ordinal"`
	Name       string         `json:"name"`
	Layer      string         `json:"layer"`
	Kind       string         `json:"kind"`
	Description string        `json:"description,omitempty"`
	Anchor     slicing.Anchor `json:"anchor"`
	StateDelta *struct {
		Before string `json:"before"`
		After  string `json:"after"`
	} `json:"stateDelta,omitempty"`
	SideEffect *string  `json:"sideEffect,omitempty"`
	Branch     *string  `json:"branch,omitempty"`
	Rules      []string `json:"rules,omitempty"`
}

type coreEdge struct {
	StepOrdinal      int    `json:"stepOrdinal"`
	ToSymbolPath     string `json:"toSymbolPath"`
	ToLayer          string `json:"toLayer,omitempty"`
	Kind             string `json:"kind"`
	ResolutionStatus string `json:"resolutionStatus"`
}

// structured error payload per spec §7
type coreFlowErrorPayload struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   []map[string]any `json:"details,omitempty"`
	Retryable bool           `json:"retryable"`
}

func coreFlowError(code, message string, details []map[string]any, retryable bool) error {
	payload := coreFlowErrorPayload{
		Code:      code,
		Message:   message,
		Details:   details,
		Retryable: retryable,
	}
	b, _ := json.Marshal(payload)
	return fmt.Errorf("%s", string(b))
}

func (s *Server) handlePublishCoreFlow(ctx context.Context, args map[string]any) (any, error) {
	if err := s.checkAuth(args["token"]); err != nil {
		return nil, coreFlowError("unauthorized", err.Error(), nil, false)
	}
	artRaw, ok := args["artifact"]
	if !ok || artRaw == nil {
		return nil, coreFlowError("schema_validation_failed", "artifact is required", nil, true)
	}
	rawBytes, err := json.Marshal(artRaw)
	if err != nil {
		return nil, coreFlowError("schema_validation_failed", fmt.Sprintf("marshal artifact: %v", err), nil, true)
	}
	if len(rawBytes) > maxCoreArtifactBytes {
		return nil, coreFlowError("artifact_too_large", fmt.Sprintf("artifact JSON %d bytes exceeds %d", len(rawBytes), maxCoreArtifactBytes), []map[string]any{{"field": "artifact", "reason": "artifact_too_large", "limit": maxCoreArtifactBytes, "actual": len(rawBytes)}}, false)
	}
	// Secret gate before validation (same as slicing/fusion entrances)
	sanitized, _, err := secret.RedactJSON(rawBytes)
	if err != nil {
		return nil, coreFlowError("schema_validation_failed", fmt.Sprintf("redact artifact: %v", err), nil, true)
	}
	if len(sanitized) > maxCoreArtifactBytes {
		return nil, coreFlowError("artifact_too_large", fmt.Sprintf("artifact JSON after redaction %d bytes exceeds %d", len(sanitized), maxCoreArtifactBytes), []map[string]any{{"field": "artifact", "reason": "artifact_too_large", "limit": maxCoreArtifactBytes, "actual": len(sanitized)}}, false)
	}
	if err := contractharness.Validate(contractharness.BaseURL+"core-artifact.schema.json", sanitized); err != nil {
		return nil, coreFlowError("schema_validation_failed", fmt.Sprintf("core-artifact schema validation failed: %v", err), []map[string]any{{"reason": "schema_validation_failed", "details": err.Error()}}, true)
	}
	var artifact coreArtifact
	if err := json.Unmarshal(sanitized, &artifact); err != nil {
		return nil, coreFlowError("schema_validation_failed", fmt.Sprintf("unmarshal artifact: %v", err), nil, true)
	}
	if len(artifact.Steps) == 0 {
		return nil, coreFlowError("schema_validation_failed", "steps must not be empty", nil, true)
	}
	if len(artifact.Steps) > 64 || len(artifact.Edges) > 64 {
		return nil, coreFlowError("artifact_too_large", fmt.Sprintf("steps %d edges %d exceed 64", len(artifact.Steps), len(artifact.Edges)), []map[string]any{{"reason": "artifact_too_large", "steps": len(artifact.Steps), "edges": len(artifact.Edges)}}, false)
	}
	// Verify ordinals are 1..N contiguous and sorted
	for i, st := range artifact.Steps {
		if st.Ordinal != i+1 {
			return nil, coreFlowError("schema_validation_failed", fmt.Sprintf("steps ordinal gap at index %d: expected %d got %d", i, i+1, st.Ordinal), []map[string]any{{"ordinal": st.Ordinal, "expected": i + 1, "reason": "ordinal_gap"}}, true)
		}
	}

	// 6. Anchor verification per step (in ordinal order) — first error aborts without persisting.
	for _, st := range artifact.Steps {
		if err := verifyAnchor(s.cfg.RepoRoot, st.Anchor, st.Ordinal); err != nil {
			return nil, err
		}
	}

	// 7. Load and apply codeflow.layers.yaml (D6 B)
	layersCfg, err := fusion.LoadLayersConfig(s.cfg.RepoRoot)
	if err != nil {
		return nil, coreFlowError("layers_config_invalid", fmt.Sprintf("codeflow.layers.yaml invalid: %v", err), []map[string]any{{"reason": "layers_config_invalid", "details": err.Error()}}, false)
	}
	var warnings []string
	// Detect missing file fallback as warning (when default config was used but file exists? Actually Load returns default when absent; we warn when absent)
	if _, statErr := os.Stat(filepath.Join(s.cfg.RepoRoot, "codeflow.layers.yaml")); os.IsNotExist(statErr) {
		warnings = append(warnings, "codeflow.layers.yaml not found — using built-in 8 canonical layers")
	}

	// 7b. Validate raw layers against config when allowUnknownLayer == false
	if !layersCfg.AllowUnknownLayer {
		for _, st := range artifact.Steps {
			_, unknown := fusion.NormalizeLayer(st.Layer, layersCfg)
			if unknown {
				return nil, coreFlowError("layer_order_violation", fmt.Sprintf("anchor verification failed at ordinal %d: layer %q unknown", st.Ordinal, st.Layer), []map[string]any{
					{"ordinal": st.Ordinal, "field": "layer", "reason": "layer_order_violation", "hint": "add alias to codeflow.layers.yaml or use a canonical layer", "layer": st.Layer},
				}, true)
			}
		}
		for _, e := range artifact.Edges {
			if e.ToLayer != "" {
				_, unknown := fusion.NormalizeLayer(e.ToLayer, layersCfg)
				if unknown && !layersCfg.AllowUnknownLayer {
					return nil, coreFlowError("layer_order_violation", fmt.Sprintf("edge toLayer %q unknown at step %d", e.ToLayer, e.StepOrdinal), []map[string]any{
						{"ordinal": e.StepOrdinal, "field": "toLayer", "reason": "layer_order_violation", "layer": e.ToLayer},
					}, true)
				}
			}
		}
	}

	// Normalize and collect warnings for allowUnknownLayer==true case
	normalizedLayers := make([]string, len(artifact.Steps))
	for i, st := range artifact.Steps {
		canon, unknown := fusion.NormalizeLayer(st.Layer, layersCfg)
		if unknown {
			// When allowUnknownLayer true, map to unknown with warning; when false we already errored.
			warnings = append(warnings, fmt.Sprintf("step %d layer %q normalized to unknown", st.Ordinal, st.Layer))
			normalizedLayers[i] = fusion.LayerUnknown
		} else {
			normalizedLayers[i] = canon
		}
	}
	// Also normalize artifact.Layers when present
	normDeclaredLayers := make([]string, 0, len(artifact.Layers))
	for _, l := range artifact.Layers {
		canon, unknown := fusion.NormalizeLayer(l, layersCfg)
		if unknown {
			if !layersCfg.AllowUnknownLayer {
				return nil, coreFlowError("layer_order_violation", fmt.Sprintf("declared layers contains unknown %q", l), []map[string]any{{"reason": "layer_order_violation", "layer": l}}, true)
			}
			warnings = append(warnings, fmt.Sprintf("declared layer %q normalized to unknown", l))
			normDeclaredLayers = append(normDeclaredLayers, fusion.LayerUnknown)
		} else {
			normDeclaredLayers = append(normDeclaredLayers, canon)
		}
	}

	// 8. Validate layer traversal monotonicity
	// Build stepsForValidation with normalized layers
	stepsForOrder := make([]struct {
		Layer string
		Kind  string
	}, len(artifact.Steps))
	for i, st := range artifact.Steps {
		stepsForOrder[i].Layer = normalizedLayers[i]
		stepsForOrder[i].Kind = st.Kind
	}
	if orderWarnings, err := fusion.ValidateLayerOrder(stepsForOrder, normDeclaredLayers, layersCfg); err != nil {
		return nil, coreFlowError("layer_order_violation", err.Error(), []map[string]any{{"reason": "layer_order_violation", "details": err.Error()}}, true)
	} else {
		warnings = append(warnings, orderWarnings...)
	}
	// Path-pattern advisory warnings as unknowns-style warnings
	stepsForPath := make([]struct {
		Layer            string
		RepoRelativePath string
	}, len(artifact.Steps))
	for i, st := range artifact.Steps {
		stepsForPath[i].Layer = normalizedLayers[i]
		stepsForPath[i].RepoRelativePath = st.Anchor.RepoRelativePath
	}
	pathWarnings := fusion.ValidatePathPatterns(stepsForPath, layersCfg)
	warnings = append(warnings, pathWarnings...)

	// 10. Compute basisSha
	// Unique file parts from steps + entrySymbolPath file part + codeflow.layers.yaml when present
	uniqueFiles := map[string]bool{}
	for _, st := range artifact.Steps {
		uniqueFiles[st.Anchor.RepoRelativePath] = true
	}
	if idx := strings.Index(artifact.EntrySymbolPath, "#"); idx >= 0 {
		uniqueFiles[artifact.EntrySymbolPath[:idx]] = true
	} else {
		uniqueFiles[artifact.EntrySymbolPath] = true
	}
	if _, err := os.Stat(filepath.Join(s.cfg.RepoRoot, "codeflow.layers.yaml")); err == nil {
		uniqueFiles["codeflow.layers.yaml"] = true
	}
	relPaths := make([]string, 0, len(uniqueFiles))
	for p := range uniqueFiles {
		relPaths = append(relPaths, p)
	}
	sort.Strings(relPaths)
	basisSha, err := storage.ComputeWorktreeFingerprint(s.cfg.RepoRoot, relPaths)
	if err != nil {
		return nil, coreFlowError("storage_commit_failed", fmt.Sprintf("compute basisSha: %v", err), nil, true)
	}

	// 11. Build slicing.SlicedPayload in-memory (no adapter call)
	sliced := &slicing.SlicedPayload{
		CandidateID:     artifact.FlowID,
		Language:        "dart",
		EntrySymbolPath: artifact.EntrySymbolPath,
		Steps:           make([]slicing.SliceStep, 0, len(artifact.Steps)),
		Edges:           make([]slicing.SliceEdge, 0, len(artifact.Edges)),
		Truncated:       false,
	}
	if sliced.CandidateID == "" {
		h := sha256.Sum256([]byte(artifact.EntrySymbolPath))
		sliced.CandidateID = "cand-" + hex.EncodeToString(h[:8])
	}
	for i, st := range artifact.Steps {
		canonLayer := normalizedLayers[i]
		var stateBefore, stateAfter *string
		if st.StateDelta != nil {
			b := st.StateDelta.Before
			a := st.StateDelta.After
			stateBefore = &b
			stateAfter = &a
		}
		var guardCond *string
		if st.Branch != nil {
			gc := *st.Branch
			guardCond = &gc
		}
		desc := st.Name
		if st.Description != "" {
			desc = st.Description
		}
		// Normalize anchor's enclosingSymbolPath is already there
		sliced.Steps = append(sliced.Steps, slicing.SliceStep{
			Ordinal:        st.Ordinal,
			Kind:           st.Kind,
			Description:    desc,
			SymbolPath:     st.Anchor.EnclosingSymbolPath,
			Anchor:         st.Anchor,
			GuardCondition: guardCond,
			StateBefore:    stateBefore,
			StateAfter:     stateAfter,
			EffectTarget:   st.SideEffect,
			Layer:          canonLayer,
		})
	}
	for _, e := range artifact.Edges {
		// Normalize toLayer if present
		toLayerNorm := ""
		if e.ToLayer != "" {
			if canon, unknown := fusion.NormalizeLayer(e.ToLayer, layersCfg); !unknown {
				toLayerNorm = canon
			} else {
				toLayerNorm = fusion.LayerUnknown
			}
		}
		depth := 0
		if toLayerNorm != "" {
			depth = fusion.LayerIndex(toLayerNorm)
		} else {
			// fallback: layer distance not critical
			depth = e.StepOrdinal
		}
		ord := e.StepOrdinal
		edge := slicing.SliceEdge{
			Kind:             e.Kind,
			ToSymbolPath:     e.ToSymbolPath,
			ResolutionStatus: e.ResolutionStatus,
			Depth:            depth,
			StepOrdinal:      &ord,
			ToLayer:          toLayerNorm,
		}
		sliced.Edges = append(sliced.Edges, edge)
	}

	// 12. Fuse
	spec, err := fusion.Fuse(sliced, fusion.FuseOptions{
		RepoRoot:          s.cfg.RepoRoot,
		CustomTitle:       artifact.Title,
		CustomDescription: artifact.Description,
		BasisSha:          basisSha,
	})
	if err != nil {
		return nil, coreFlowError("storage_commit_failed", fmt.Sprintf("fuse error: %v", err), []map[string]any{{"reason": "fuse_failed", "details": err.Error()}}, true)
	}
	// Merge artifact unknowns and path warnings into spec.Unknowns
	if len(artifact.Unknowns) > 0 {
		spec.Unknowns = append(spec.Unknowns, artifact.Unknowns...)
	}
	for _, pw := range pathWarnings {
		spec.Unknowns = append(spec.Unknowns, fusion.Unknown{Subject: pw, Reason: "unresolved_type"})
	}
	// Order warnings are not unknowns per se — but spec says advisory warning → unknowns[] entry with reason unresolved_type for path mismatches only. Layer order warnings stay as warnings slice.
	// Ensure spec.Unknowns is non-nil (schema requires array, may be empty)
	if spec.Unknowns == nil {
		spec.Unknowns = []fusion.Unknown{}
	}

	// 13. Atomically publish as new generation
	existingPtr, _ := s.storage.ReadPointer()
	var existingIdx *storage.GenerationIndex
	if existingPtr != nil {
		existingIdx, _ = s.storage.ReadLatestIndex()
	}
	sess, err := s.storage.BeginGeneration(basisSha)
	if err != nil {
		return nil, coreFlowError("storage_commit_failed", fmt.Sprintf("begin generation: %v", err), nil, true)
	}
	defer sess.Discard()
	if existingIdx != nil && existingPtr != nil {
		for _, sum := range existingIdx.Flows {
			if sum.FlowID == spec.FlowID {
				continue
			}
			raw, err := s.storage.ReadFlowSpec(existingPtr.GenerationID, sum.FlowID)
			if err != nil {
				continue
			}
			_ = sess.AddFlowSpec(sum.FlowID, raw, sum)
		}
	}
	specBytes, _ := json.Marshal(spec)
	if err := sess.AddFlowSpec(spec.FlowID, specBytes, storage.FlowSummary{
		FlowID:          spec.FlowID,
		Title:           spec.Title,
		Description:     spec.Description,
		EntrySymbolPath: artifact.EntrySymbolPath,
		StepCount:       len(spec.Steps),
	}); err != nil {
		return nil, coreFlowError("storage_commit_failed", fmt.Sprintf("add flow: %v", err), nil, true)
	}
	if err := sess.Commit(); err != nil {
		return nil, coreFlowError("storage_commit_failed", fmt.Sprintf("commit: %v", err), nil, true)
	}

	// 14. Return success payload
	url := ""
	token := ""
	s.fvMu.Lock()
	fv := s.fv
	s.fvMu.Unlock()
	if fv != nil {
		url = fv.URL() + "&flow=" + spec.FlowID
		token = fv.AuthToken()
	}
	return map[string]any{
		"status":    "published",
		"flowId":    spec.FlowID,
		"title":     spec.Title,
		"stepCount": len(spec.Steps),
		"basisSha":  spec.BasisSha,
		"url":       url,
		"token":     token,
		"warnings":  warnings,
	}, nil
}

func verifyAnchor(repoRoot string, anchor slicing.Anchor, ordinal int) error {
	if anchor.RepoRelativePath == "" {
		return coreFlowError("anchor_verification_failed", fmt.Sprintf("anchor verification failed at ordinal %d", ordinal), []map[string]any{
			{"ordinal": ordinal, "field": "anchor.repoRelativePath", "reason": "file_not_found", "path": anchor.RepoRelativePath},
		}, true)
	}
	fullPath := filepath.Join(repoRoot, anchor.RepoRelativePath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return coreFlowError("anchor_verification_failed", fmt.Sprintf("anchor verification failed at ordinal %d", ordinal), []map[string]any{
				{"ordinal": ordinal, "field": "anchor.repoRelativePath", "reason": "file_not_found", "path": anchor.RepoRelativePath, "hint": "file does not exist"},
			}, true)
		}
		return coreFlowError("anchor_verification_failed", fmt.Sprintf("anchor verification failed at ordinal %d", ordinal), []map[string]any{
			{"ordinal": ordinal, "field": "anchor.repoRelativePath", "reason": "file_not_found", "details": err.Error()},
		}, true)
	}
	// b. fileHash check is not an error — just compute but continue (stale is handled later via freshness)
	// We optionally verify but don't fail on mismatch; only spanHash must match.

	// c. byteRange bounds
	if len(anchor.ByteRange) != 2 {
		return coreFlowError("anchor_verification_failed", fmt.Sprintf("anchor verification failed at ordinal %d", ordinal), []map[string]any{
			{"ordinal": ordinal, "field": "anchor.byteRange", "reason": "byte_range_out_of_bounds", "hint": "byteRange must be [start,end]"},
		}, true)
	}
	start := anchor.ByteRange[0]
	end := anchor.ByteRange[1]
	if start < 0 || end < 0 || start > end || end > len(data) {
		return coreFlowError("anchor_verification_failed", fmt.Sprintf("anchor verification failed at ordinal %d", ordinal), []map[string]any{
			{"ordinal": ordinal, "field": "anchor.byteRange", "reason": "byte_range_out_of_bounds", "expected": len(data), "actual": anchor.ByteRange, "hint": "byteRange out of bounds for file"},
		}, true)
	}
	span := data[start:end]
	spanHash := sha256.Sum256(span)
	if hex.EncodeToString(spanHash[:]) != anchor.SpanHash {
		return coreFlowError("anchor_verification_failed", fmt.Sprintf("anchor verification failed at ordinal %d", ordinal), []map[string]any{
			{"ordinal": ordinal, "field": "anchor.spanHash", "reason": "span_hash_mismatch", "expected": hex.EncodeToString(spanHash[:]), "actual": anchor.SpanHash, "hint": fmt.Sprintf("re-read %s around %s and recompute byteRange/fileHash/spanHash; enclosingSymbolPath must be %q", anchor.RepoRelativePath, lastSegment(anchor.EnclosingSymbolPath), anchor.EnclosingSymbolPath), "path": anchor.RepoRelativePath},
		}, true)
	}
	// Also verify fileHash optionally? Spec says recompute fileHash but do not fail — we skip Strict.

	// f. enclosingSymbolPath must be non-empty and found
	if strings.TrimSpace(anchor.EnclosingSymbolPath) == "" {
		return coreFlowError("anchor_verification_failed", fmt.Sprintf("anchor verification failed at ordinal %d", ordinal), []map[string]any{
			{"ordinal": ordinal, "field": "anchor.enclosingSymbolPath", "reason": "enclosing_symbol_not_found", "path": anchor.RepoRelativePath},
		}, true)
	}
	lastSeg := lastSegment(anchor.EnclosingSymbolPath)
	if lastSeg == "" {
		lastSeg = anchor.EnclosingSymbolPath
	}
	contentStr := string(data)
	if !strings.Contains(contentStr, lastSeg) {
		return coreFlowError("anchor_verification_failed", fmt.Sprintf("anchor verification failed at ordinal %d", ordinal), []map[string]any{
			{"ordinal": ordinal, "field": "anchor.enclosingSymbolPath", "reason": "enclosing_symbol_not_found", "hint": fmt.Sprintf("symbol %q not found in %s", anchor.EnclosingSymbolPath, anchor.RepoRelativePath), "path": anchor.RepoRelativePath},
		}, true)
	}
	// Optionally also check full dotted chain exists? Simple scan for last segment is sufficient per spec.
	return nil
}

func lastSegment(dotted string) string {
	if idx := strings.LastIndex(dotted, "."); idx >= 0 {
		return dotted[idx+1:]
	}
	return dotted
}
