package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"codeflow/internal/contractharness"
	"codeflow/internal/fusion"
	"codeflow/internal/protocol"
	"codeflow/internal/slicing"
)

func TestExploreDomainsV2RequiresExactRepositoryAndBasis(t *testing.T) {
	fixture := newOnboardingV2Fixture(t)
	query := onboardingV2Query(fixture, 1)
	query.RepositoryID = "repo-does-not-resolve"
	if got, err := ExploreDomainsV2(OnboardingRequestV2{Query: query, Map: fixture.mapIR, Candidates: fixture.candidates}); err == nil || got != nil || !strings.Contains(err.Error(), ErrCodeMissingPrecondition) {
		t.Fatalf("unresolved repository was accepted: result=%+v err=%v", got, err)
	}
	query = onboardingV2Query(fixture, 1)
	query.ComputedBasisID = "different-basis"
	if got, err := ExploreDomainsV2(OnboardingRequestV2{Query: query, Map: fixture.mapIR, Candidates: fixture.candidates}); err == nil || got != nil || !strings.Contains(err.Error(), ErrCodeInvalidOnboardingIdentity) {
		t.Fatalf("basis mismatch was accepted: result=%+v err=%v", got, err)
	}
	query = onboardingV2Query(fixture, 1)
	query.ValidatedAgainstSnapshotID = "different-snapshot"
	if got, err := ExploreDomainsV2(OnboardingRequestV2{Query: query, Map: fixture.mapIR, Candidates: fixture.candidates}); err == nil || got != nil || !strings.Contains(err.Error(), ErrCodeMissingPrecondition) {
		t.Fatalf("snapshot mismatch was accepted: result=%+v err=%v", got, err)
	}
}

func TestExploreDomainsV2DerivesDeterministicEvidenceBackedDomains(t *testing.T) {
	fixture := newOnboardingV2Fixture(t)
	request := OnboardingRequestV2{Query: onboardingV2Query(fixture, 1), Map: fixture.mapIR, Candidates: fixture.candidates}
	first, err := ExploreDomainsV2(request)
	if err != nil {
		t.Fatalf("ExploreDomainsV2 failed: %v", err)
	}
	secondRequest := request
	secondRequest.Candidates = append([]CandidateEntry(nil), request.Candidates...)
	for left, right := 0, len(secondRequest.Candidates)-1; left < right; left, right = left+1, right-1 {
		secondRequest.Candidates[left], secondRequest.Candidates[right] = secondRequest.Candidates[right], secondRequest.Candidates[left]
	}
	second, err := ExploreDomainsV2(secondRequest)
	if err != nil {
		t.Fatalf("reordered ExploreDomainsV2 failed: %v", err)
	}
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("domain projection is input-order dependent:\nfirst=%s\nsecond=%s", firstJSON, secondJSON)
	}
	if len(first.Domains) != 2 || len(first.UnmappedModules) != 1 || first.Summary.CoverageRatio != 2.0/3.0 {
		t.Fatalf("domain coverage projection is incomplete: %+v", first)
	}
	for _, domain := range first.Domains {
		if domain.DomainID == "" || domain.Name == "" || domain.Rationale == "" || len(domain.EvidenceRefs) == 0 || domain.EpistemicState != "candidate" || domain.Confidence != 0.75 || len(domain.CoverageBoundary.IncludedSourceRoots) == 0 {
			t.Fatalf("domain lacks evidence-backed metadata: %+v", domain)
		}
	}
	if first.Domains[0].Name != "Billing" || first.Domains[1].Name != "Telemetry" {
		t.Fatalf("domains are not deterministically ordered: %+v", first.Domains)
	}
}

func TestGetRepresentativeFlowCatalogV2UsesCanonicalMapAndResultEvidence(t *testing.T) {
	fixture := newOnboardingV2Fixture(t)
	request := OnboardingRequestV2{Query: onboardingV2Query(fixture, 2), Map: fixture.mapIR, Candidates: fixture.candidates}
	first, err := GetRepresentativeFlowCatalogV2(request, "Billing")
	if err != nil {
		t.Fatalf("GetRepresentativeFlowCatalogV2 failed: %v", err)
	}
	if first.DomainID == "" || first.GroundedMapIDForTest() != fixture.mapIR.MapID || first.GenerationID != fixture.mapIR.GenerationID || len(first.Flows) != 1 {
		t.Fatalf("catalog lost canonical identity: %+v", first)
	}
	flow := first.Flows[0]
	if flow.FlowID == "" || flow.GroundedMapID != fixture.mapIR.MapID || len(flow.EntryEvidenceRefs) != 1 || len(flow.ResultEvidenceRefs) != 1 || len(flow.EvidenceRefs) != 2 || flow.EpistemicState != "confirmed" {
		t.Fatalf("catalog flow lacks entry/result Evidence: %+v", flow)
	}
	secondRequest := request
	secondRequest.Candidates = append([]CandidateEntry(nil), request.Candidates...)
	secondRequest.Candidates[0], secondRequest.Candidates[1] = secondRequest.Candidates[1], secondRequest.Candidates[0]
	second, err := GetRepresentativeFlowCatalogV2(secondRequest, "Billing")
	if err != nil {
		t.Fatalf("reordered catalog failed: %v", err)
	}
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("catalog changed after candidate reorder:\nfirst=%s\nsecond=%s", firstJSON, secondJSON)
	}
}

func TestDrilldownRepresentativeFlowV2SharesCanonicalGeneration(t *testing.T) {
	fixture := newOnboardingV2Fixture(t)
	request := OnboardingRequestV2{Query: onboardingV2Query(fixture, 2), Map: fixture.mapIR, Candidates: fixture.candidates}
	catalog, err := GetRepresentativeFlowCatalogV2(request, "Billing")
	if err != nil {
		t.Fatal(err)
	}
	drilldown, err := DrilldownRepresentativeFlowV2(request, "Billing", catalog.Flows[0].FlowID)
	if err != nil {
		t.Fatalf("drilldown failed: %v", err)
	}
	if drilldown.CanonicalMapID != fixture.mapIR.MapID || drilldown.ComputedBasisID != fixture.mapIR.ComputedBasisID || drilldown.GenerationID != fixture.mapIR.GenerationID || drilldown.ValidatedAgainstSnapshotID != fixture.mapIR.ValidatedAgainstSnapshotID || len(drilldown.StepRefs) != 1 || len(drilldown.EvidenceRefs) != 2 {
		t.Fatalf("drilldown does not reference same canonical map generation: %+v", drilldown)
	}
	invalid := *fixture.mapIR
	invalid.Evidence = append([]SemanticEvidence(nil), fixture.mapIR.Evidence...)
	invalid.Evidence[0].Anchor.RepoRelativePath = "../outside.go"
	got, err := ExploreDomainsV2(OnboardingRequestV2{Query: onboardingV2Query(fixture, 1), Map: &invalid, Candidates: fixture.candidates})
	if err != nil || got == nil {
		t.Fatalf("item-level invalid evidence failed the whole overview: result=%+v err=%v", got, err)
	}
	raw, marshalErr := json.Marshal(got)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(string(raw), "../outside.go") || len(got.Domains) != 1 || len(got.UnmappedModules) == 0 {
		t.Fatalf("invalid anchor was emitted or affected item was not quarantined: %s", raw)
	}
}

func TestDrilldownRepresentativeFlowV2UsesDedicatedContract(t *testing.T) {
	fixture := newOnboardingV2Fixture(t)
	request := OnboardingRequestV2{Query: onboardingV2Query(fixture, 2), Map: fixture.mapIR, Candidates: fixture.candidates}
	catalog, err := GetRepresentativeFlowCatalogV2(request, "Billing")
	if err != nil || len(catalog.Flows) == 0 {
		t.Fatalf("catalog failed: %v", err)
	}
	drilldown, err := DrilldownRepresentativeFlowV2(request, "Billing", catalog.Flows[0].FlowID)
	if err != nil {
		t.Fatalf("drilldown failed: %v", err)
	}
	if drilldown.SchemaID == RepresentativeFlowCatalogSchemaID {
		t.Fatal("drilldown was mislabeled as representative flow catalog")
	}
	raw, err := json.Marshal(drilldown)
	if err != nil {
		t.Fatal(err)
	}
	if err := contractharness.ValidateOnboardingFlowDrilldownV2(raw); err != nil {
		t.Fatalf("dedicated drilldown contract rejected output: %v", err)
	}
}

func TestExploreDomainsV2CurrentRequiresExactSuppliedProof(t *testing.T) {
	fixture := newOnboardingV2Fixture(t)
	query := onboardingV2Query(fixture, 1)
	query.Freshness = "current"
	request := OnboardingRequestV2{Query: query, Map: fixture.mapIR, Candidates: fixture.candidates}
	if got, err := ExploreDomainsV2(request); err == nil || got != nil || !strings.Contains(err.Error(), ErrCodeMissingPrecondition) {
		t.Fatalf("current query without proof was accepted: result=%+v err=%v", got, err)
	}
	request.CurrentProof = onboardingV2CurrentProof(fixture)
	got, err := ExploreDomainsV2(request)
	if err != nil || got == nil || got.Freshness != "current" {
		t.Fatalf("exact proof did not authorize current projection: result=%+v err=%v", got, err)
	}
	request.CurrentProof.ExpectedLiveHeadSnapshotID = "different-live-head"
	if got, err := ExploreDomainsV2(request); err == nil || got != nil || !strings.Contains(err.Error(), ErrCodeInvalidOnboardingIdentity) {
		t.Fatalf("proof with a different live head was accepted: result=%+v err=%v", got, err)
	}
}

func TestCanonicalStepPathUsesValidatedAnchorForCompilerIdentity(t *testing.T) {
	fixture := newOnboardingV2Fixture(t)
	step := fixture.mapIR.Steps[0]
	step.StructuralIdentity = step.Anchor.RepoRelativePath + "\x00" + step.Anchor.EnclosingSymbolPath + "\x00compiler-internal-suffix"
	if got, want := canonicalStepPath(step), step.Anchor.RepoRelativePath+"#"+step.Anchor.EnclosingSymbolPath; got != want {
		t.Fatalf("compiler structural identity was exposed as public entry identity: got %q want %q", got, want)
	}
}

func TestExploreDomainsV2DoesNotConfirmArbitraryLabels(t *testing.T) {
	fixture := newOnboardingV2Fixture(t)
	request := OnboardingRequestV2{Query: onboardingV2Query(fixture, 1), Map: fixture.mapIR, Candidates: fixture.candidates}
	request.Candidates[1].Domain = "Payments"
	first, err := ExploreDomainsV2(request)
	if err != nil || first == nil {
		t.Fatalf("unsupported label should degrade to an unknown item, result=%+v err=%v", first, err)
	}
	for _, domain := range first.Domains {
		if domain.Name == "Payments" {
			t.Fatalf("arbitrary business label was confirmed without domain Evidence: %+v", domain)
		}
	}
	if len(first.Unknowns) == 0 || len(first.UnmappedModules) == 0 {
		t.Fatalf("unsupported label did not remain visible as unknown/unmapped: %+v", first)
	}

	withDomainEvidence := *fixture.mapIR
	withDomainEvidence.Evidence = append([]SemanticEvidence(nil), fixture.mapIR.Evidence...)
	withDomainEvidence.Evidence = append(withDomainEvidence.Evidence, SemanticEvidence{
		EvidenceID: "ev-billing-domain", Kind: "domain_label", SourceAuthority: "code",
		ComputedBasisID: fixture.mapIR.ComputedBasisID, SnapshotID: fixture.mapIR.ValidatedAgainstSnapshotID,
		DocumentRevisionID: "rev-billing-domain", Anchor: fixture.mapIR.Steps[1].Anchor,
		ValidationStatus: "verified", RedactionStatus: "clean",
	})
	candidates := append([]CandidateEntry(nil), fixture.candidates...)
	candidates[1].Domain = "Billing"
	candidates[1].DomainEvidenceRefs = []string{"ev-billing-domain"}
	second, err := ExploreDomainsV2(OnboardingRequestV2{Query: onboardingV2Query(fixture, 1), Map: &withDomainEvidence, Candidates: candidates})
	if err != nil || second == nil {
		t.Fatalf("explicit domain Evidence did not project: result=%+v err=%v", second, err)
	}
	for _, domain := range second.Domains {
		if domain.Name == "Billing" {
			if domain.EpistemicState != "confirmed" || domain.Confidence != 1 || len(domain.OwnershipEvidenceRefs) != 1 {
				t.Fatalf("explicit domain Evidence did not confirm label: %+v", domain)
			}
			return
		}
	}
	t.Fatalf("explicit Billing domain was not projected: %+v", second)
}

func TestExploreDomainsV2PreservesClosureOpenEvidenceAsUnknown(t *testing.T) {
	fixture := newOnboardingV2Fixture(t)
	mapIR := *fixture.mapIR
	mapIR.Evidence = append([]SemanticEvidence(nil), fixture.mapIR.Evidence...)
	for index := range mapIR.Evidence {
		mapIR.Evidence[index].ValidationStatus = "unknown"
	}
	result, err := ExploreDomainsV2(OnboardingRequestV2{Query: onboardingV2Query(fixture, 1), Map: &mapIR, Candidates: fixture.candidates})
	if err != nil || result == nil {
		t.Fatalf("closure-open evidence failed the whole overview: result=%+v err=%v", result, err)
	}
	if len(result.Domains) != 2 {
		t.Fatalf("closure-open evidence was dropped instead of projected as low-confidence domains: %+v", result)
	}
	for _, domain := range result.Domains {
		if domain.EpistemicState != "unknown" || domain.Confidence != 0.25 || len(domain.MissingEvidence) == 0 {
			t.Fatalf("closure-open domain was incorrectly promoted: %+v", domain)
		}
	}
}

func TestCatalogRationaleMarksNonVerifiedEvidenceUnresolved(t *testing.T) {
	fixture := newOnboardingV2Fixture(t)
	mapIR := *fixture.mapIR
	mapIR.Evidence = append([]SemanticEvidence(nil), fixture.mapIR.Evidence...)
	mapIR.Evidence[1].ValidationStatus = "pending"
	candidates := append([]CandidateEntry(nil), fixture.candidates...)
	candidates[1].Rationale = "selected from referenced source items"
	request := OnboardingRequestV2{Query: onboardingV2Query(fixture, 2), Map: &mapIR, Candidates: candidates}
	catalog, err := GetRepresentativeFlowCatalogV2(request, "Billing")
	if err != nil || len(catalog.Flows) != 1 {
		t.Fatalf("pending-evidence catalog failed: %v %+v", err, catalog)
	}
	if !strings.Contains(strings.ToLower(catalog.Flows[0].Rationale), "unresolved") {
		t.Fatalf("non-verified Evidence rationale omitted unresolved status: %q", catalog.Flows[0].Rationale)
	}
}

func TestCatalogRationaleAppendsExplicitValidationGapForPendingStaleAndUnsafeRefs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SemanticEvidence)
	}{
		{name: "pending", mutate: func(evidence *SemanticEvidence) { evidence.ValidationStatus = "pending" }},
		{name: "stale", mutate: func(evidence *SemanticEvidence) { evidence.ValidationStatus = "stale" }},
		{name: "unsafe", mutate: func(evidence *SemanticEvidence) { evidence.Anchor.RepoRelativePath = "../outside.go" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newOnboardingV2Fixture(t)
			mapIR := *fixture.mapIR
			mapIR.Evidence = append([]SemanticEvidence(nil), fixture.mapIR.Evidence...)
			// Keep entry Evidence verified so the candidate remains in the
			// catalog. The final result refs carry the validation gap under test.
			test.mutate(&mapIR.Evidence[2])
			candidates := append([]CandidateEntry(nil), fixture.candidates...)
			candidates[1].Rationale = "candidate rationale mentions unresolved source notes"
			request := OnboardingRequestV2{Query: onboardingV2Query(fixture, 2), Map: &mapIR, Candidates: candidates}
			catalog, err := GetRepresentativeFlowCatalogV2(request, "Billing")
			if err != nil || len(catalog.Flows) != 1 {
				t.Fatalf("catalog failed: %v %+v", err, catalog)
			}
			if !strings.Contains(catalog.Flows[0].Rationale, "Evidence validation remains unresolved") {
				t.Fatalf("rationale omitted explicit validation gap: %q", catalog.Flows[0].Rationale)
			}
		})
	}
}

func TestCatalogDoesNotReAdmitQuarantinedSameDomainCandidate(t *testing.T) {
	fixture := newOnboardingV2Fixture(t)
	mapIR := *fixture.mapIR
	mapIR.Steps = append([]SemanticStep(nil), fixture.mapIR.Steps...)
	badAnchor := onboardingAnchor("pkg/billing.go", "billing.Refund", "func Refund() {}")
	mapIR.Steps = append(mapIR.Steps, SemanticStep{
		StepID: "step-billing-refund", StructuralIdentity: "pkg/billing.go#billing.Refund", Ordinal: 4,
		Name: "billing.Refund", TechnicalName: "billing.Refund", Kind: "effect", Anchor: badAnchor,
		// The canonical step has a valid source item, but the caller explicitly
		// supplied a different invalid candidate ref below. The invalid list must
		// not fall back to this generic step Evidence.
		EvidenceRefs: []string{"ev-billing-entry"},
	})
	mapIR.Evidence = append([]SemanticEvidence(nil), fixture.mapIR.Evidence...)
	mapIR.Evidence = append(mapIR.Evidence, SemanticEvidence{
		EvidenceID: "ev-billing-invalid", Kind: "source", SourceAuthority: "code",
		ComputedBasisID: mapIR.ComputedBasisID, SnapshotID: mapIR.ValidatedAgainstSnapshotID,
		DocumentRevisionID: "rev-billing-invalid", Anchor: badAnchor,
		ValidationStatus: "invalid", RedactionStatus: "clean",
	})
	candidates := append([]CandidateEntry(nil), fixture.candidates...)
	badEntry := "pkg/billing.go#billing.Refund"
	candidates = append(candidates, CandidateEntry{
		CandidateID: "cand-billing-invalid", EntrySymbolPath: badEntry, Domain: "Billing",
		SourceRoot: "pkg", Module: "billing", Package: "billing", EvidenceRefs: []string{"ev-billing-invalid"}, SelectionScore: 9,
	})
	request := OnboardingRequestV2{Query: onboardingV2Query(fixture, 2), Map: &mapIR, Candidates: candidates}
	catalog, err := GetRepresentativeFlowCatalogV2(request, "Billing")
	if err != nil {
		t.Fatalf("catalog failed: %v", err)
	}
	if len(catalog.Flows) != 1 || catalog.Flows[0].EntrySymbol == badEntry || len(catalog.Flows[0].EntryEvidenceRefs) == 0 {
		t.Fatalf("quarantined same-domain candidate was re-admitted: %+v", catalog.Flows)
	}
	foundUnmapped := false
	for _, item := range catalog.UnmappedModules {
		if item == badEntry {
			foundUnmapped = true
		}
	}
	if !foundUnmapped {
		t.Fatalf("quarantined candidate was not retained in recovery: %+v", catalog.UnmappedModules)
	}
	raw, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := contractharness.ValidateRepresentativeFlowCatalogV2(raw); err != nil {
		t.Fatalf("catalog with quarantined candidate failed v2 validation: %v", err)
	}
}

func TestCatalogDoesNotFallbackForExplicitEmptyCandidateEvidenceRefs(t *testing.T) {
	fixture := newOnboardingV2Fixture(t)
	mapIR := *fixture.mapIR
	mapIR.Steps = append([]SemanticStep(nil), fixture.mapIR.Steps...)
	badEntry := "pkg/billing.go#billing.Refund"
	mapIR.Steps = append(mapIR.Steps, SemanticStep{
		StepID: "step-billing-refund-empty", StructuralIdentity: badEntry, Ordinal: 4,
		Name: "billing.Refund", TechnicalName: "billing.Refund", Kind: "effect",
		Anchor:       onboardingAnchor("pkg/billing.go", "billing.Refund", "func Refund() {}"),
		EvidenceRefs: []string{"ev-billing-entry"},
	})
	candidates := append([]CandidateEntry(nil), fixture.candidates...)
	// A present, explicit empty array is not the same as omitted EvidenceRefs.
	candidates = append(candidates, CandidateEntry{
		CandidateID: "cand-billing-empty", EntrySymbolPath: badEntry, Domain: "Billing",
		SourceRoot: "pkg", Module: "billing", Package: "billing", EvidenceRefs: []string{}, SelectionScore: 9,
	})
	request := OnboardingRequestV2{Query: onboardingV2Query(fixture, 2), Map: &mapIR, Candidates: candidates}
	catalog, err := GetRepresentativeFlowCatalogV2(request, "Billing")
	if err != nil {
		t.Fatalf("catalog failed: %v", err)
	}
	if len(catalog.Flows) != 1 || catalog.Flows[0].EntrySymbol == badEntry {
		t.Fatalf("explicit empty EvidenceRefs fell back to map step Evidence: %+v", catalog.Flows)
	}
	raw, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := contractharness.ValidateRepresentativeFlowCatalogV2(raw); err != nil {
		t.Fatalf("catalog with quarantined explicit-empty candidate failed schema validation: %v", err)
	}
}

func TestValidateOnboardingAnchorRejectsNonCanonicalRangesAndCharacters(t *testing.T) {
	base := onboardingAnchor("src/telemetry.go", "telemetry.Start", "package telemetry\n")
	tests := []struct {
		name   string
		mutate func(*slicing.Anchor)
	}{
		{name: "zero-length", mutate: func(anchor *slicing.Anchor) { anchor.ByteRange = [2]int{0, 0} }},
		{name: "nul-path", mutate: func(anchor *slicing.Anchor) { anchor.RepoRelativePath = "src/telemetry\x00.go" }},
		{name: "control-symbol", mutate: func(anchor *slicing.Anchor) { anchor.EnclosingSymbolPath = "telemetry.\nStart" }},
		{name: "whitespace-path", mutate: func(anchor *slicing.Anchor) { anchor.RepoRelativePath = "src//telemetry.go" }},
		{name: "dot-path", mutate: func(anchor *slicing.Anchor) { anchor.RepoRelativePath = "src/./telemetry.go" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			anchor := base
			test.mutate(&anchor)
			if err := validateOnboardingAnchor(anchor); err == nil {
				t.Fatalf("invalid anchor was accepted: %+v", anchor)
			}
		})
	}
}

// GroundedMapIDForTest keeps the assertion readable without exposing a
// mutable alternate map model to callers.
func (catalog *RepresentativeFlowCatalogV2) GroundedMapIDForTest() string {
	if catalog == nil || len(catalog.Flows) == 0 {
		return ""
	}
	return catalog.Flows[0].GroundedMapID
}

type onboardingV2FixtureData struct {
	snapshot   protocol.Snapshot
	mapIR      *SemanticMapIR
	candidates []CandidateEntry
}

func newOnboardingV2Fixture(t *testing.T) onboardingV2FixtureData {
	t.Helper()
	files := map[string]string{
		"src/telemetry.go":  "package telemetry\nfunc Start() {}\n",
		"pkg/billing.go":    "package billing\nfunc Charge() {}\nfunc Result() {}\n",
		"unknown/plugin.go": "package plugin\nfunc Dispatch() {}\n",
	}
	snapshot, err := protocol.NewSnapshot(4, files, "basis-onboarding-v2")
	if err != nil {
		t.Fatal(err)
	}
	telemetryAnchor := onboardingAnchor("src/telemetry.go", "telemetry.Start", files["src/telemetry.go"])
	billingAnchor := onboardingAnchor("pkg/billing.go", "billing.Charge", files["pkg/billing.go"])
	resultAnchor := onboardingAnchor("pkg/billing.go", "billing.Result", files["pkg/billing.go"])
	evidence := []SemanticEvidence{
		{EvidenceID: "ev-telemetry", Kind: "source", SourceAuthority: "code", ComputedBasisID: snapshot.ComputedBasisID, SnapshotID: snapshot.SnapshotID, DocumentRevisionID: "rev-telemetry", Anchor: telemetryAnchor, ValidationStatus: "verified", RedactionStatus: "clean"},
		{EvidenceID: "ev-billing-entry", Kind: "source", SourceAuthority: "code", ComputedBasisID: snapshot.ComputedBasisID, SnapshotID: snapshot.SnapshotID, DocumentRevisionID: "rev-billing-entry", Anchor: billingAnchor, ValidationStatus: "verified", RedactionStatus: "clean"},
		{EvidenceID: "ev-billing-result", Kind: "source", SourceAuthority: "code", ComputedBasisID: snapshot.ComputedBasisID, SnapshotID: snapshot.SnapshotID, DocumentRevisionID: "rev-billing-result", Anchor: resultAnchor, ValidationStatus: "verified", RedactionStatus: "clean"},
	}
	steps := []SemanticStep{
		{StepID: "step-telemetry", StructuralIdentity: "src/telemetry.go#telemetry.Start", Ordinal: 1, Name: "telemetry.Start", TechnicalName: "telemetry.Start", Kind: "entry", Anchor: telemetryAnchor, EvidenceRefs: []string{"ev-telemetry"}},
		{StepID: "step-billing", StructuralIdentity: "pkg/billing.go#billing.Charge", Ordinal: 2, Name: "billing.Charge", TechnicalName: "billing.Charge", Kind: "effect", Anchor: billingAnchor, EvidenceRefs: []string{"ev-billing-entry"}},
		{StepID: "step-result", StructuralIdentity: "pkg/billing.go#billing.Result", Ordinal: 3, Name: "billing.Result", TechnicalName: "billing.Result", Kind: "result", Anchor: resultAnchor, EvidenceRefs: []string{"ev-billing-result"}},
	}
	mapIR := &SemanticMapIR{
		SchemaID: SemanticMapSchemaID, SchemaVersion: SemanticSchemaVersion, MapID: "map-onboarding-v2", GenerationID: "generation-onboarding-v2", ComputedBasisID: snapshot.ComputedBasisID, ValidatedAgainstSnapshotID: snapshot.SnapshotID,
		PublicationKind: "checkpoint", Freshness: "historical", Settlement: "pending", EnrichmentStatus: "available", Authority: "historical",
		Quality: MapQuality{Stage: "Q2", UnresolvedCriticalCount: 1}, Task: MapTaskContext{TaskID: "task-onboarding-v2", IntentRevision: 1, Mode: "onboarding"},
		Basis:   MapBasisContext{RepositoryID: "repo-onboarding-v2", ComputedWorkspaceSnapshotID: snapshot.SnapshotID, ComputedBasisID: snapshot.ComputedBasisID, SnapshotTreeID: snapshot.RootTreeID, WorkspaceEpoch: snapshot.WorkspaceEpoch, DependencyFingerprint: snapshot.DependencyFingerprint},
		Summary: MapSummary{Requested: "explore domains", Current: "historical onboarding"}, Steps: steps,
		Edges: []SemanticEdge{{FromStepID: "step-billing", ToStepID: "step-result", ToSymbolPath: "pkg/billing.go#billing.Result", Kind: "calls", ResolutionStatus: "verified"}}, Evidence: evidence,
		Unknowns: []fusion.Unknown{{Subject: "unknown/plugin.go#plugin.Dispatch", Reason: "dynamic dispatch is unresolved"}}, Coverage: &CoverageBoundary{IncludedSourceRoots: []string{"src", "pkg"}, ExcludedReasons: []string{"unknown/plugin.go is not mapped"}},
	}
	candidates := []CandidateEntry{
		{CandidateID: "cand-telemetry", EntrySymbolPath: "src/telemetry.go#telemetry.Start", Domain: "Telemetry", Title: "telemetry startup", SourceRoot: "src", Module: "telemetry", Package: "telemetry", EvidenceRefs: []string{"ev-telemetry"}, SelectionScore: 2},
		{CandidateID: "cand-billing", EntrySymbolPath: "pkg/billing.go#billing.Charge", Domain: "Billing", Title: "billing charge", SourceRoot: "pkg", Module: "billing", Package: "billing", EvidenceRefs: []string{"ev-billing-entry"}, ResultEvidenceRefs: []string{"ev-billing-result"}, SelectionScore: 3},
		{CandidateID: "cand-unknown", EntrySymbolPath: "unknown/plugin.go#plugin.Dispatch"},
	}
	return onboardingV2FixtureData{snapshot: snapshot, mapIR: mapIR, candidates: candidates}
}

func onboardingV2Query(fixture onboardingV2FixtureData, level int) OnboardingQueryV2 {
	return OnboardingQueryV2{SchemaID: OnboardingQuerySchemaID, SchemaVersion: 2, RepositoryID: fixture.mapIR.Basis.RepositoryID, ComputedBasisID: fixture.mapIR.ComputedBasisID, GenerationID: fixture.mapIR.GenerationID, ValidatedAgainstSnapshotID: fixture.mapIR.ValidatedAgainstSnapshotID, Freshness: fixture.mapIR.Freshness, Level: level}
}

func onboardingV2CurrentProof(fixture onboardingV2FixtureData) *GenerationProofManifest {
	return &GenerationProofManifest{
		SchemaID: GenerationProofSchemaID, SchemaVersion: SemanticSchemaVersion, ProofID: "proof-onboarding-v2",
		GenerationID: fixture.mapIR.GenerationID, ComputedBasisID: fixture.mapIR.ComputedBasisID,
		ComputedSnapshotID:         fixture.mapIR.Basis.ComputedWorkspaceSnapshotID,
		ValidatedAgainstSnapshotID: fixture.mapIR.ValidatedAgainstSnapshotID,
		WorkspaceEpoch:             fixture.mapIR.Basis.WorkspaceEpoch, ExpectedLiveHeadSnapshotID: fixture.mapIR.ValidatedAgainstSnapshotID,
		CurrentPublication: CurrentPublicationResult{Eligibility: "passed", SnapshotGate: "passed", ClosureGate: "passed", EvidenceGate: "passed", SemanticAtomicityGate: "passed", TaskRelevanceGate: "passed", ComprehensionGate: "passed"},
	}
}

func onboardingAnchor(path, symbol, content string) slicing.Anchor {
	fileHash := sha256.Sum256([]byte(content))
	spanHash := sha256.Sum256([]byte(symbol + "\x00" + content))
	astHash := sha256.Sum256([]byte("ast:" + symbol))
	return slicing.Anchor{RepoRelativePath: path, ByteRange: [2]int{0, len([]byte(content))}, FileHash: hex.EncodeToString(fileHash[:]), SpanHash: hex.EncodeToString(spanHash[:]), EnclosingSymbolPath: symbol, CanonicalAstFingerprint: hex.EncodeToString(astHash[:])}
}
