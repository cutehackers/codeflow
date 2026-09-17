package contractharness

import (
	"encoding/json"
	"fmt"
)

// VS05ContractRegistryEntry records the producer, consumer, and fixture
// evidence for one public change-impact contract.
type VS05ContractRegistryEntry struct {
	ID                      string
	SchemaID                string
	Producer                string
	Consumer                string
	ValidFixture            string
	InvalidDirectionFixture string
	InvalidIdentityFixture  string
	InvalidEvidenceFixture  string
}

const (
	ImpactQueryV2SchemaID       = BaseURL + "rflsc.impact-query.v2.schema.json"
	ChangeImpactGraphV2SchemaID = BaseURL + "rflsc.change-impact-graph.v2.schema.json"
	ImpactFrontierV1SchemaID    = BaseURL + "rflsc.impact-frontier.v1.schema.json"
)

// VS05EvidenceRegistryID is the stable evidence registry identity from the
// approved VS-05 contract.
const VS05EvidenceRegistryID = "rflsc-r2-vs-05"

// VS05ContractRegistry is ordered to match the required public contract list.
// Fixture paths are relative to schemas/fixtures.
var VS05ContractRegistry = []VS05ContractRegistryEntry{
	{
		ID: "rflsc.impact-query.v2", SchemaID: ImpactQueryV2SchemaID,
		Producer: "impact query decoder", Consumer: "semantic impact query and MCP/FlowView impact seams",
		ValidFixture:            "rflsc.impact-query.v2/valid/query.json",
		InvalidDirectionFixture: "rflsc.impact-query.v2/invalid/both-targets.json",
		InvalidIdentityFixture:  "rflsc.impact-query.v2/invalid/missing-basis.json",
		InvalidEvidenceFixture:  "rflsc.impact-query.v2/invalid/current-without-proof.json",
	},
	{
		ID: "rflsc.change-impact-graph.v2", SchemaID: ChangeImpactGraphV2SchemaID,
		Producer: "semantic impact graph projection", Consumer: "Change Impact Task View, MCP and FlowView impact endpoint",
		ValidFixture:            "rflsc.change-impact-graph.v2/valid/graph.json",
		InvalidDirectionFixture: "rflsc.change-impact-graph.v2/invalid/reversed-caller.json",
		InvalidIdentityFixture:  "rflsc.change-impact-graph.v2/invalid/mismatched-basis.json",
		InvalidEvidenceFixture:  "rflsc.change-impact-graph.v2/invalid/missing-evidence-anchor.json",
	},
	{
		ID: "rflsc.impact-frontier.v1", SchemaID: ImpactFrontierV1SchemaID,
		Producer: "bounded impact traversal", Consumer: "ChangeImpactGraph frontier and explicit expansion controls",
		ValidFixture:            "rflsc.impact-frontier.v1/valid/frontier.json",
		InvalidDirectionFixture: "rflsc.impact-frontier.v1/invalid/bad-boundary.json",
		InvalidIdentityFixture:  "rflsc.impact-frontier.v1/invalid/zero-target.json",
		InvalidEvidenceFixture:  "rflsc.impact-frontier.v1/invalid/missing-evidence-ref.json",
	},
}

// ValidateVS05RegistryEntry validates one valid fixture and each distinct
// invalid direction, identity, and evidence fixture through its strict
// production validator.
func ValidateVS05RegistryEntry(entry VS05ContractRegistryEntry, valid, invalidDirection, invalidIdentity, invalidEvidence []byte) error {
	if entry.ID == "" || entry.SchemaID == "" || entry.Producer == "" || entry.Consumer == "" || entry.ValidFixture == "" || entry.InvalidDirectionFixture == "" || entry.InvalidIdentityFixture == "" || entry.InvalidEvidenceFixture == "" {
		return fmt.Errorf("VS-05 contract registry entry is incomplete: %+v", entry)
	}
	expected := BaseURL + entry.ID + ".schema.json"
	if entry.SchemaID != expected {
		return fmt.Errorf("%s schema identity mismatch: got %q want %q", entry.ID, entry.SchemaID, expected)
	}
	validator := ValidatorForVS05Contract(entry.ID)
	if validator == nil {
		return fmt.Errorf("%s has no VS-05 semantic validator", entry.ID)
	}
	if err := validator(valid); err != nil {
		return fmt.Errorf("%s valid fixture: %w", entry.ID, err)
	}
	for label, data := range map[string][]byte{
		"direction": invalidDirection,
		"identity":  invalidIdentity,
		"evidence":  invalidEvidence,
	} {
		if err := validator(data); err == nil {
			return fmt.Errorf("%s invalid %s fixture unexpectedly passed validation", entry.ID, label)
		}
	}
	return nil
}

// ValidatorForVS05Contract exposes the strict validators used by the registry.
func ValidatorForVS05Contract(id string) func([]byte) error {
	switch id {
	case "rflsc.impact-query.v2":
		return ValidateImpactQueryV2
	case "rflsc.change-impact-graph.v2":
		return ValidateChangeImpactGraphV2
	case "rflsc.impact-frontier.v1":
		return ValidateImpactFrontierV1
	default:
		return nil
	}
}

func validatorForVS05Contract(id string) func([]byte) error {
	return ValidatorForVS05Contract(id)
}

func validateVS05Document(schemaID string, data []byte) (map[string]any, error) {
	if err := Validate(schemaID, data); err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", schemaID, err)
	}
	return doc, nil
}

func extractImpactString(doc map[string]any, key string) string {
	v, _ := doc[key].(string)
	return v
}

func extractImpactNumber(doc map[string]any, key string) (float64, bool) {
	v, ok := doc[key].(float64)
	return v, ok
}

func extractImpactObject(doc map[string]any, key string) map[string]any {
	v, _ := doc[key].(map[string]any)
	return v
}

func extractImpactArray(doc map[string]any, key string) []any {
	v, _ := doc[key].([]any)
	return v
}

// ValidateImpactQueryV2 validates target preconditions and query bounds that
// JSON Schema alone cannot safely express at the public seam.
func ValidateImpactQueryV2(data []byte) error {
	doc, err := validateVS05Document(ImpactQueryV2SchemaID, data)
	if err != nil {
		return fmt.Errorf("impact-query schema: %w", err)
	}
	target := extractImpactObject(doc, "target")
	if target == nil {
		return fmt.Errorf("impact query target is required")
	}
	_, hasSymbol := target["symbolId"]
	_, hasBatch := target["changeBatchId"]
	if hasSymbol == hasBatch {
		return fmt.Errorf("impact query requires exactly one target identity")
	}
	if basis := extractImpactString(doc, "computedBasisId"); basis == "" {
		return fmt.Errorf("impact query computedBasisId is required")
	}
	if generation := extractImpactString(doc, "generationId"); generation == "" {
		return fmt.Errorf("impact query generationId is required")
	}
	depth, depthOK := extractImpactNumber(doc, "maxDepth")
	maxNodes, nodesOK := extractImpactNumber(doc, "maxNodes")
	if !depthOK || depth < 1 || depth > 5 || !nodesOK || maxNodes < 1 || maxNodes > 50 {
		return fmt.Errorf("impact query budget is outside the declared bounds")
	}
	if len(extractImpactArray(doc, "relationKinds")) == 0 {
		return fmt.Errorf("impact query relationKinds must not be empty")
	}
	if extractImpactString(doc, "freshness") == "current" {
		verified, _ := doc["currentProofVerified"].(bool)
		if !verified {
			return fmt.Errorf("current impact query requires currentProofVerified")
		}
	}
	return nil
}

// ValidateImpactFrontierV1 validates frontier identity and expansion state.
func ValidateImpactFrontierV1(data []byte) error {
	doc, err := validateVS05Document(ImpactFrontierV1SchemaID, data)
	if err != nil {
		return fmt.Errorf("impact-frontier schema: %w", err)
	}
	depth, ok := extractImpactNumber(doc, "depth")
	if !ok || depth < 0 || depth > 5 {
		return fmt.Errorf("impact frontier depth is outside the declared bounds")
	}
	if extractImpactString(doc, "target") == "" || extractImpactString(doc, "reason") == "" {
		return fmt.Errorf("impact frontier target and reason are required")
	}
	if refs := extractImpactArray(doc, "evidenceRefs"); refs == nil {
		return fmt.Errorf("impact frontier evidenceRefs is required")
	}
	return nil
}

// ValidateChangeImpactGraphV2 validates same-generation identity, edge
// direction, bounded traversal state, frontier accounting, and Evidence refs.
func ValidateChangeImpactGraphV2(data []byte) error {
	doc, err := validateVS05Document(ChangeImpactGraphV2SchemaID, data)
	if err != nil {
		return fmt.Errorf("change-impact-graph.v2 schema: %w", err)
	}
	target := extractImpactObject(doc, "target")
	if target == nil {
		return fmt.Errorf("impact graph target is required")
	}
	_, hasSymbol := target["symbolId"]
	_, hasBatch := target["changeBatchId"]
	if hasSymbol == hasBatch {
		return fmt.Errorf("impact graph target requires exactly one identity")
	}
	basis := extractImpactString(doc, "computedBasisId")
	generation := extractImpactString(doc, "generationId")
	snapshot := extractImpactString(doc, "validatedAgainstSnapshotId")
	if basis == "" || generation == "" || snapshot == "" {
		return fmt.Errorf("impact graph basis and generation identities are required")
	}

	evidenceByID := make(map[string]map[string]any)
	for _, raw := range extractImpactArray(doc, "evidence") {
		ev, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("impact graph evidence is not an object")
		}
		id := extractImpactString(ev, "evidenceId")
		if id == "" {
			return fmt.Errorf("impact graph evidence id is required")
		}
		if _, exists := evidenceByID[id]; exists {
			return fmt.Errorf("duplicate impact evidence %q", id)
		}
		if extractImpactString(ev, "computedBasisId") != basis || extractImpactString(ev, "snapshotId") != snapshot {
			return fmt.Errorf("impact evidence %q is not bound to the graph basis/snapshot", id)
		}
		if extractImpactString(ev, "validationStatus") != "verified" {
			return fmt.Errorf("impact evidence %q is not verified", id)
		}
		anchor := extractImpactObject(ev, "anchor")
		if anchor == nil || extractImpactString(anchor, "repoRelativePath") == "" {
			return fmt.Errorf("impact evidence %q is missing its source anchor", id)
		}
		if !validVS05ByteRange(anchor["byteRange"]) {
			return fmt.Errorf("impact evidence %q has an invalid anchor byte range", id)
		}
		evidenceByID[id] = ev
	}

	validateSet := func(name string, set map[string]any, indirect bool) error {
		if set == nil {
			return fmt.Errorf("impact set %s is required", name)
		}
		if indirect {
			depth, depthOK := extractImpactNumber(set, "maxDepth")
			nodes, nodesOK := extractImpactNumber(set, "maxNodes")
			explored, exploredOK := extractImpactNumber(set, "exploredDepth")
			count, countOK := extractImpactNumber(set, "totalNodeCount")
			bounded, boundedOK := set["bounded"].(bool)
			if !depthOK || depth < 1 || depth > 5 || !nodesOK || nodes < 1 || nodes > 50 || !exploredOK || explored < 0 || explored > depth || !countOK || count < 0 || count > nodes || !boundedOK || !bounded {
				return fmt.Errorf("indirect impact budget/count is inconsistent")
			}
		}
		if err := validateVS05ClaimArray(extractImpactArray(set, "callers"), "caller", evidenceByID, target, indirect); err != nil {
			return err
		}
		if err := validateVS05ClaimArray(extractImpactArray(set, "stateMutations"), "stateMutation", evidenceByID, target, indirect); err != nil {
			return err
		}
		if err := validateVS05ClaimArray(extractImpactArray(set, "externalEffects"), "externalEffect", evidenceByID, target, indirect); err != nil {
			return err
		}
		if err := validateVS05ClaimArray(extractImpactArray(set, "relatedFlows"), "relatedFlow", evidenceByID, target, indirect); err != nil {
			return err
		}
		if err := validateVS05ClaimArray(extractImpactArray(set, "tests"), "test", evidenceByID, target, indirect); err != nil {
			return err
		}
		return nil
	}
	if err := validateSet("directImpact", extractImpactObject(doc, "directImpact"), false); err != nil {
		return err
	}
	if err := validateSet("indirectImpact", extractImpactObject(doc, "indirectImpact"), true); err != nil {
		return err
	}

	frontiers := extractImpactArray(doc, "frontiers")
	unknown, unknownOK := extractImpactNumber(doc, "unknownCount")
	if !unknownOK || unknown != float64(len(frontiers)) {
		return fmt.Errorf("unknownCount must equal the number of frontiers")
	}
	additional, _ := doc["additionalExplorationAvailable"].(bool)
	expandableCount := 0
	for _, raw := range frontiers {
		frontier, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("impact frontier is not an object")
		}
		if extractImpactString(frontier, "schemaId") != ImpactFrontierV1SchemaID || extractImpactNumberValue(frontier, "schemaVersion") != 1 {
			return fmt.Errorf("impact graph contains a non-canonical frontier")
		}
		expandable, _ := frontier["expandable"].(bool)
		if expandable {
			expandableCount++
		}
		for _, rawRef := range extractImpactArray(frontier, "evidenceRefs") {
			ref, _ := rawRef.(string)
			if _, exists := evidenceByID[ref]; !exists {
				return fmt.Errorf("frontier references missing evidence %q", ref)
			}
		}
	}
	if additional != (expandableCount > 0) {
		return fmt.Errorf("additional exploration state does not match expandable frontiers")
	}
	indirect := extractImpactObject(doc, "indirectImpact")
	completed, _ := indirect["completedWithinCoverage"].(bool)
	if completed && len(frontiers) != 0 {
		return fmt.Errorf("completed bounded impact cannot contain frontiers")
	}
	unresolved := extractImpactArray(doc, "unresolvedBoundaries")
	if len(unresolved) != len(frontiers) {
		return fmt.Errorf("unresolved boundaries must mirror frontiers")
	}
	for i, raw := range unresolved {
		boundary, ok := raw.(map[string]any)
		frontier, frontierOK := frontiers[i].(map[string]any)
		if !ok || !frontierOK || extractImpactString(boundary, "boundaryType") != extractImpactString(frontier, "boundaryType") || extractImpactString(boundary, "target") != extractImpactString(frontier, "target") || extractImpactString(boundary, "description") != extractImpactString(frontier, "reason") {
			return fmt.Errorf("unresolved boundary %d does not mirror its frontier", i)
		}
	}
	return nil
}

func validVS05ByteRange(raw any) bool {
	values, ok := raw.([]any)
	if !ok || len(values) != 2 {
		return false
	}
	start, startOK := values[0].(float64)
	end, endOK := values[1].(float64)
	return startOK && endOK && start >= 0 && end > start
}

func extractImpactNumberValue(doc map[string]any, key string) float64 {
	v, _ := extractImpactNumber(doc, key)
	return v
}

func validateVS05ClaimArray(values []any, kind string, evidenceByID map[string]map[string]any, target map[string]any, indirect bool) error {
	for _, raw := range values {
		claim, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("impact %s claim is not an object", kind)
		}
		depth, depthOK := extractImpactNumber(claim, "depth")
		path := extractImpactArray(claim, "path")
		if !depthOK || depth < 0 || depth > 5 || !validVS05Path(path) || depth != float64(len(path)-1) {
			return fmt.Errorf("impact %s claim has inconsistent depth/path", kind)
		}
		if indirect && depth < 2 {
			return fmt.Errorf("indirect impact %s claim is not indirect", kind)
		}
		if !indirect && depth > 1 {
			return fmt.Errorf("direct impact %s claim is not direct", kind)
		}
		if symbol, ok := target["symbolId"].(string); ok && path[0] != symbol {
			return fmt.Errorf("impact %s claim path does not start at changed symbol %q", kind, symbol)
		}
		terminal, err := claimTerminalSymbol(kind, claim)
		if err != nil {
			return err
		}
		if path[len(path)-1] != terminal {
			return fmt.Errorf("impact %s claim path must terminate at terminalSymbolPath %q", kind, terminal)
		}
		if !indirect && kind == "caller" && depth != 1 {
			return fmt.Errorf("direct caller claim must have depth 1")
		}
		if kind == "caller" {
			caller := extractImpactString(claim, "symbolPath")
			if caller == "" {
				return fmt.Errorf("impact caller source identity is empty")
			}
			if symbol, ok := target["symbolId"].(string); ok && caller == symbol {
				return fmt.Errorf("impact caller source identity equals the changed callee")
			}
		}
		for _, rawRef := range extractImpactArray(claim, "evidenceRefs") {
			ref, refOK := rawRef.(string)
			if !refOK || ref == "" {
				return fmt.Errorf("impact %s claim has an invalid Evidence ref", kind)
			}
			evidence, exists := evidenceByID[ref]
			if !exists {
				return fmt.Errorf("impact %s claim references missing evidence %q", kind, ref)
			}
			anchor := extractImpactObject(evidence, "anchor")
			if anchor == nil || extractImpactString(anchor, "enclosingSymbolPath") != terminal {
				return fmt.Errorf("impact %s claim Evidence %q is not anchored to terminal symbol %q", kind, ref, terminal)
			}
			if kind == "caller" && extractImpactString(anchor, "repoRelativePath") != extractImpactString(claim, "filePath") {
				return fmt.Errorf("impact caller claim Evidence %q is not anchored to filePath %q", ref, extractImpactString(claim, "filePath"))
			}
		}
	}
	return nil
}

func validVS05Path(path []any) bool {
	if len(path) == 0 {
		return false
	}
	for _, raw := range path {
		value, ok := raw.(string)
		if !ok || value == "" {
			return false
		}
	}
	return true
}

func claimTerminalSymbol(kind string, claim map[string]any) (string, error) {
	key := "terminalSymbolPath"
	if kind == "caller" {
		key = "symbolPath"
	}
	terminal := extractImpactString(claim, key)
	if terminal == "" {
		return "", fmt.Errorf("impact %s claim terminal symbol identity is required", kind)
	}
	return terminal, nil
}
