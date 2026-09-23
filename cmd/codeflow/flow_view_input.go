package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"codeflow/internal/collector/contractharness"
	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/secret"
	"codeflow/internal/curator/semantic"
)

// decodeFlowViewInput adapts the CLI curate format without regrouping its frames.
// Import identities describe input bytes, not verified source or a new analysis.
func decodeFlowViewInput(input []byte) (map[string]any, error) {
	clean, _, err := secret.RedactJSON(input)
	if err != nil {
		return nil, fmt.Errorf("invalid FlowView JSON input")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(clean, &fields); err != nil || fields == nil {
		return nil, fmt.Errorf("FlowView input must be a JSON object")
	}
	var model semantic.SemanticMapIR
	var sequence semantic.FlowSequence
	var result map[string]any
	if _, full := fields["semanticMap"]; full {
		if err := json.Unmarshal(fields["semanticMap"], &model); err != nil {
			return nil, fmt.Errorf("decode analysis: %w", err)
		}
		if err := json.Unmarshal(fields["flowSequence"], &sequence); err != nil {
			return nil, fmt.Errorf("decode hierarchy: %w", err)
		}
		if err := json.Unmarshal(clean, &result); err != nil {
			return nil, fmt.Errorf("decode view: %w", err)
		}
	} else {
		var flat struct {
			FlowID             string                           `json:"flowId"`
			Frames             []semantic.FlowSequenceFrame     `json:"frames"`
			Steps              []semantic.SemanticStep          `json:"steps"`
			Edges              []semantic.SemanticEdge          `json:"edges"`
			Unknowns           []fusion.Unknown                 `json:"unknowns"`
			SummaryLimitations []semantic.FlowSummaryLimitation `json:"summaryLimitations"`
		}
		if err := json.Unmarshal(clean, &flat); err != nil {
			return nil, fmt.Errorf("decode curated flow: %w", err)
		}
		if flat.FlowID == "" || flat.Frames == nil {
			return nil, fmt.Errorf("curated flow requires flowId and frames")
		}
		if len(flat.Frames) > 0 && len(flat.Steps) == 0 {
			return nil, fmt.Errorf("curated flow requires source steps to validate frame references")
		}
		digest := sha256.Sum256(clean)
		identity := "import-" + hex.EncodeToString(digest[:])
		model = semantic.SemanticMapIR{MapID: "map-" + flat.FlowID, GenerationID: identity, ComputedBasisID: identity, Steps: flat.Steps, Edges: flat.Edges, Unknowns: flat.Unknowns, Authority: "historical", Freshness: "historical"}
		sequence = semantic.FlowSequence{SchemaID: semantic.FlowSequenceSchemaID, SchemaVersion: semantic.FlowSequenceSchemaVersion, FlowID: flat.FlowID, GenerationID: identity, ComputedBasisID: identity, SnapshotID: identity, Frames: flat.Frames, SummaryLimitations: flat.SummaryLimitations}
		entry := ""
		if len(model.Steps) > 0 {
			first := model.Steps[0].Anchor
			if first.RepoRelativePath != "" && first.EnclosingSymbolPath != "" {
				entry = first.RepoRelativePath + "#" + first.EnclosingSymbolPath
			}
		}
		result = map[string]any{"semanticMap": &model, "flowSequence": &sequence, "flowId": flat.FlowID,
			"request":              map[string]any{"flowId": flat.FlowID, "entrySymbol": entry},
			"sourceContextMissing": true, "needsReanalysis": true,
			"sourceNotice": "입력에 소스 문맥이 포함되어 있지 않습니다. 현재 코드를 확인하려면 다시 분석을 실행하세요."}
	}
	encoded, err := json.Marshal(sequence)
	if err != nil {
		return nil, fmt.Errorf("encode hierarchy: %w", err)
	}
	if err := contractharness.ValidateFlowSequence(encoded); err != nil {
		return nil, fmt.Errorf("invalid FlowSequence: %w", err)
	}
	if err := semantic.ValidateFlowSequenceReferences(&model, &sequence); err != nil {
		return nil, fmt.Errorf("invalid FlowSequence: %w", err)
	}
	ids := make(map[string]bool, len(model.Steps))
	for _, step := range model.Steps {
		ids[step.StepID] = true
	}
	for _, edge := range model.Edges {
		if !ids[edge.FromStepID] || (edge.ResolutionStatus == "resolved" && !ids[edge.ToStepID]) {
			return nil, fmt.Errorf("invalid execution relation reference")
		}
	}
	// External JSON is not a trusted snapshot. Import only the source graph and
	// hierarchy as historical claims, never rendered code or proof assertions.
	digest := sha256.Sum256(clean)
	identity := "import-" + hex.EncodeToString(digest[:])
	model = semantic.SemanticMapIR{
		SchemaID: semantic.SemanticMapSchemaID, SchemaVersion: semantic.SemanticSchemaVersion,
		MapID: "map-" + sequence.FlowID, GenerationID: identity, ComputedBasisID: identity,
		Summary: model.Summary, Steps: model.Steps, Edges: model.Edges, Unknowns: model.Unknowns,
		BoundaryTargets: model.BoundaryTargets, Authority: "historical", Freshness: "historical", Settlement: "pending",
	}
	for i := range model.Steps {
		model.Steps[i].EvidenceRefs = nil
	}
	sequence.GenerationID, sequence.ComputedBasisID, sequence.SnapshotID = identity, identity, identity
	for i := range sequence.Frames {
		if sequence.Frames[i].Status == "verified" {
			sequence.Frames[i].Status = "partial"
		}
	}
	request := map[string]any{"flowId": sequence.FlowID}
	if existing, ok := result["request"].(map[string]any); ok {
		for _, key := range []string{"request", "entrySymbol", "domain"} {
			if value, ok := existing[key].(string); ok {
				request[key] = value
			}
		}
	}
	result = map[string]any{"semanticMap": &model, "flowSequence": &sequence, "flowId": sequence.FlowID, "request": request,
		"sourceContextMissing": true, "needsReanalysis": true,
		"sourceNotice": "입력한 분석의 소스 근거를 이 환경에서 검증하지 않았습니다. 현재 코드를 확인하려면 다시 분석을 실행하세요."}
	if err := semantic.ValidateFlowSequenceReferences(&model, &sequence); err != nil {
		return nil, fmt.Errorf("invalid imported hierarchy: %w", err)
	}
	return result, nil
}
