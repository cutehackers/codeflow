package flowview

import (
	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
	"encoding/json"
)

// Derived adapter metadata is separate from canonical map and Evidence bytes.
// Its capability is obtained from initialize, never inferred from language.
type flowContextGeneration struct {
	SnapshotID string
	Capability bool
	Steps      map[string]*slicing.FlowContextMetadata
}

func (s *Server) rememberFlowContexts(m *semantic.SemanticMapIR, payload *slicing.SlicedPayload, capability bool) flowContextGeneration {
	result := flowContextGeneration{SnapshotID: flowSourceSnapshotID(m), Capability: capability, Steps: map[string]*slicing.FlowContextMetadata{}}
	if payload != nil && payload.SnapshotID == result.SnapshotID && capability {
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
					result.Steps[step.StepID] = &copied
				}
			}
		}
	}
	s.mu.Lock()
	if s.flowContextMetadata == nil {
		s.flowContextMetadata = map[string]flowContextGeneration{}
	}
	s.flowContextMetadata[m.GenerationID] = result
	s.mu.Unlock()
	return result
}
