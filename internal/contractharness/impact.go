package contractharness

import (
	"encoding/json"
	"fmt"
)

// ValidateChangeImpactGraph validates raw JSON against change-impact-graph.schema.json
// and enforces impact graph invariants (SID-C2, SID-06, Raw §8.6, VS-06).
func ValidateChangeImpactGraph(data []byte) error {
	schemaID := BaseURL + "change-impact-graph.schema.json"
	if err := Validate(schemaID, data); err != nil {
		return fmt.Errorf("change-impact-graph schema violation: %w", err)
	}

	var graph struct {
		ImpactGraphID string `json:"impactGraphId"`
		DirectImpact  struct {
			Callers []struct {
				SymbolPath string `json:"symbolPath"`
			} `json:"callers"`
		} `json:"directImpact"`
		IndirectImpact struct {
			MaxDepth       int  `json:"maxDepth"`
			TotalNodeCount int  `json:"totalNodeCount"`
			Bounded        bool `json:"bounded"`
		} `json:"indirectImpact"`
		UnresolvedBoundaries []struct {
			BoundaryType string `json:"boundaryType"`
			Target       string `json:"target"`
		} `json:"unresolvedBoundaries"`
		UnknownCount int `json:"unknownCount"`
	}

	if err := json.Unmarshal(data, &graph); err != nil {
		return fmt.Errorf("parse change-impact-graph JSON: %w", err)
	}

	// Invariant (SID-C5, SID-06): indirect impact bounded to depth <= 5, nodes <= 50
	if graph.IndirectImpact.MaxDepth > 5 {
		return fmt.Errorf("indirectImpact maxDepth %d exceeds limit of 5", graph.IndirectImpact.MaxDepth)
	}
	if graph.IndirectImpact.TotalNodeCount > 50 {
		return fmt.Errorf("indirectImpact totalNodeCount %d exceeds soft budget of 50", graph.IndirectImpact.TotalNodeCount)
	}

	// Invariant (INV-04): unresolved boundaries must be reflected in unknownCount >= 1
	if len(graph.UnresolvedBoundaries) > 0 && graph.UnknownCount < len(graph.UnresolvedBoundaries) {
		return fmt.Errorf("impact graph has %d unresolved boundaries but unknownCount is %d", len(graph.UnresolvedBoundaries), graph.UnknownCount)
	}

	return nil
}
