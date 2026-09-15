package flowview

import (
	"encoding/json"

	"codeflow/internal/protocol"
	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
)

// Derived adapter metadata is separate from canonical map and Evidence bytes.
// Its capability is obtained from initialize, never inferred from language.
type flowContextGeneration struct {
	SnapshotID string
	Capability bool
	Steps      map[string]*slicing.FlowContextMetadata
}

// ExtractFlowContextMetadata extracts adapter flow context metadata for each step in a map.
func ExtractFlowContextMetadata(m *semantic.SemanticMapIR, payload *slicing.SlicedPayload, capability bool) map[string]*slicing.FlowContextMetadata {
	steps := map[string]*slicing.FlowContextMetadata{}
	if m == nil {
		return steps
	}
	snapshotID := flowSourceSnapshotID(m)
	if payload != nil && payload.SnapshotID == snapshotID && capability {
		for _, step := range m.Steps {
			for _, source := range payload.Steps {
				if source.Ordinal != step.Ordinal || source.FlowContext == nil ||
					source.Anchor.RepoRelativePath != step.Anchor.RepoRelativePath ||
					source.Anchor.ByteRange != step.Anchor.ByteRange || source.Anchor.SpanHash != step.Anchor.SpanHash ||
					source.Anchor.FileHash != step.Anchor.FileHash {
					continue
				}
				// Own the bytes independently of mutable producer objects.
				raw, err := json.Marshal(source.FlowContext)
				if err != nil {
					continue
				}
				var copied slicing.FlowContextMetadata
				if json.Unmarshal(raw, &copied) == nil {
					steps[step.StepID] = &copied
				}
			}
		}
	}
	return steps
}

// SnapshotSourceBytes converts snapshot string files to byte slices for context extraction.
func SnapshotSourceBytes(snapshot protocol.Snapshot) map[string][]byte {
	files := snapshot.Files
	if len(files) == 0 {
		files = snapshot.ContentOverlay
	}
	out := make(map[string][]byte, len(files))
	for path, content := range files {
		out[path] = []byte(content)
	}
	return out
}

// BuildFlowContexts derives the FlowContextProjection for each step in a SemanticMapIR.
func BuildFlowContexts(m *semantic.SemanticMapIR, payload *slicing.SlicedPayload, snapshot protocol.Snapshot, adapterHasFlowContext bool) map[string]*FlowContextProjection {
	if m == nil {
		return nil
	}
	metaSteps := ExtractFlowContextMetadata(m, payload, adapterHasFlowContext)
	sourceBytes := SnapshotSourceBytes(snapshot)
	contexts := make(map[string]*FlowContextProjection, len(m.Steps))
	for _, step := range m.Steps {
		contexts[step.StepID] = DeriveFlowContext(DeriveFlowContextParams{
			Step:                  step,
			SemanticMap:           m,
			SnapshotFiles:         sourceBytes,
			SourceSnapshotID:      snapshot.SnapshotID,
			Metadata:              metaSteps[step.StepID],
			AdapterHasFlowContext: adapterHasFlowContext,
			Expansion:             ExpansionFlowContext,
		})
	}
	return contexts
}

func (s *Server) rememberFlowContexts(m *semantic.SemanticMapIR, payload *slicing.SlicedPayload, capability bool) flowContextGeneration {
	steps := ExtractFlowContextMetadata(m, payload, capability)
	result := flowContextGeneration{
		SnapshotID: flowSourceSnapshotID(m),
		Capability: capability,
		Steps:      steps,
	}
	s.mu.Lock()
	if s.flowContextMetadata == nil {
		s.flowContextMetadata = map[string]flowContextGeneration{}
	}
	s.flowContextMetadata[m.GenerationID] = result
	s.mu.Unlock()
	return result
}
