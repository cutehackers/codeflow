package harvest

import "encoding/json"

// IntentSignals mirrors schemas/candidate.schema.json §intentSignals: the
// REQUIRED agent-facing signal bundle (design-v2 §8.1).
type IntentSignals struct {
	ClassName   string  `json:"className"`
	DerivedName string  `json:"derivedName"`
	DocLine     *string `json:"docLine"`
	PackageName string  `json:"packageName"`
}

// Candidate mirrors schemas/candidate.schema.json one-to-one. Field order
// matches the schema property order so json.Marshal output is byte-stable
// across runs (the determinism guarantee of ticket 06). CORE owns
// score/fanIn/boundaryReachable (recomputed over the adapter's
// placeholders), dedupedInto/tieBreakRank (R11 dedup), and
// manifestOverride (codeflow.flows.yaml, 항상 우선).
type Candidate struct {
	CandidateID        string        `json:"candidateId"`
	TriggerClass       string        `json:"triggerClass"`
	MarkerKind         string        `json:"markerKind"`
	EntrySymbolPath    string        `json:"entrySymbolPath"`
	IntentSignals      IntentSignals `json:"intentSignals"`
	Score              float64       `json:"score"`
	FanIn              int           `json:"fanIn"`
	BoundaryReachable  bool          `json:"boundaryReachable"`
	RootEquivalenceKey string        `json:"rootEquivalenceKey"`
	DedupedInto        *string       `json:"dedupedInto"`
	TieBreakRank       int           `json:"tieBreakRank"`
	ManifestOverride   string        `json:"manifestOverride"`
}

// MarshalCandidate renders the wire/contract form of one candidate.
func MarshalCandidate(c Candidate) ([]byte, error) {
	return json.Marshal(c)
}
