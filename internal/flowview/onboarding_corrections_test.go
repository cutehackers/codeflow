package flowview

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeflow/internal/contractharness"
	"codeflow/internal/fusion"
	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
	"codeflow/internal/storage"
)

func TestOnboardingRESTRejectsExplicitZeroBudgetAndProjectsPositiveBudget(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codeflow-onboarding-budget-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	srv, err := NewServer(Config{RepoRoot: tmpDir, Port: 0})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	mapIR := onboardingBudgetTestMap()
	srv.mapCache = map[string]*semantic.SemanticMapIR{mapIR.GenerationID: mapIR}
	base := "repositoryId=repo-budget&freshness=historical&computedBasisId=basis-budget&generationId=generation-budget&validatedAgainstSnapshotId=snapshot-budget&token=" + srv.AuthToken()

	zeroBody := bytes.NewBufferString(`{"maxVisibleCoreSteps":0}`)
	zeroReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/task/onboarding?"+base, zeroBody)
	zeroRec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(zeroRec, zeroReq)
	if zeroRec.Code != http.StatusBadRequest {
		t.Fatalf("expected explicit zero budget to be rejected, got %d: %s", zeroRec.Code, zeroRec.Body.String())
	}
	var zeroErr map[string]any
	if err := json.Unmarshal(zeroRec.Body.Bytes(), &zeroErr); err != nil {
		t.Fatalf("decode zero-budget error: %v", err)
	}
	if zeroErr["code"] != "invalid_precondition" {
		t.Fatalf("expected invalid_precondition for explicit zero budget, got %v", zeroErr)
	}

	strictBody := bytes.NewBufferString(`{"displayBudget":{"targetMin":3,"targetMax":5,"enforcement":"strict"}}`)
	strictReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/task/onboarding?"+base+"&level=2&domain=Orders", strictBody)
	strictRec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(strictRec, strictReq)
	if strictRec.Code != http.StatusBadRequest {
		t.Fatalf("expected strict display budget to be rejected, got %d: %s", strictRec.Code, strictRec.Body.String())
	}

	softBody := bytes.NewBufferString(`{"displayBudget":{"targetMin":3,"targetMax":5,"enforcement":"soft"}}`)
	softReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/task/onboarding?"+base+"&level=2&domain=Orders", softBody)
	softRec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(softRec, softReq)
	if softRec.Code != http.StatusOK {
		t.Fatalf("expected complete soft display budget to succeed, got %d: %s", softRec.Code, softRec.Body.String())
	}
	var softCatalog semantic.RepresentativeFlowCatalogV2
	if err := json.Unmarshal(softRec.Body.Bytes(), &softCatalog); err != nil {
		t.Fatalf("decode soft-budget catalog: %v", err)
	}
	if len(softCatalog.Flows) == 0 || softCatalog.Flows[0].Projection == nil {
		t.Fatal("expected budgeted projection in soft-budget catalog")
	}
	if got := softCatalog.Flows[0].Projection.DisplayBudget; got.TargetMin != 3 || got.TargetMax != 5 || got.Enforcement != "soft" {
		t.Fatalf("supplied display budget was not preserved: %+v", got)
	}

	url := "http://127.0.0.1/api/task/onboarding?" + base + "&level=2&maxVisibleCoreSteps=2&domain=Orders"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected positive budget request to succeed, got %d: %s", rec.Code, rec.Body.String())
	}
	var catalog semantic.RepresentativeFlowCatalogV2
	if err := json.Unmarshal(rec.Body.Bytes(), &catalog); err != nil {
		t.Fatalf("decode budgeted catalog: %v", err)
	}
	if len(catalog.Flows) == 0 {
		t.Fatal("expected at least one representative flow")
	}
	projection := catalog.Flows[0].Projection
	if projection == nil {
		t.Fatal("expected budgeted canonical projection in representative flow")
	}
	if projection.DisplayBudget.TargetMax != 2 || projection.DisplayBudget.TargetMin < 1 || projection.DisplayBudget.Enforcement != "soft" {
		t.Fatalf("unexpected normalized display budget: %+v", projection.DisplayBudget)
	}
	if projection.GenerationID != mapIR.GenerationID || projection.ComputedBasisID != mapIR.ComputedBasisID {
		t.Fatalf("projection changed canonical identity: %+v", projection)
	}
	visible := make(map[string]bool, len(projection.VisibleStepRefs))
	for _, ref := range projection.VisibleStepRefs {
		visible[ref] = true
	}
	for _, required := range projection.PreservedStepRefs {
		if !visible[required] {
			t.Fatalf("preserved step %q was dropped by budgeted projection", required)
		}
	}
}

func onboardingBudgetTestMap() *semantic.SemanticMapIR {
	steps := make([]semantic.SemanticStep, 0, 6)
	kinds := []string{"user_action", "call", "call", "call", "call", "mutation"}
	for i, kind := range kinds {
		entry := "src/orders/checkout.ts#Checkout.step" + string(rune('A'+i))
		stepID := "step-budget-" + string(rune('a'+i))
		ref := "evidence-budget-" + string(rune('a'+i))
		steps = append(steps, semantic.SemanticStep{
			StepID: stepID, StructuralIdentity: entry, Ordinal: i + 1, Name: kind, TechnicalName: kind,
			Kind: kind, Anchor: slicing.Anchor{RepoRelativePath: "src/orders/checkout.ts", EnclosingSymbolPath: "Checkout.step" + string(rune('A'+i)), ByteRange: [2]int{i, i + 1}}, EvidenceRefs: []string{ref},
		})
	}
	evidence := make([]semantic.SemanticEvidence, 0, len(steps))
	for i, step := range steps {
		evidence = append(evidence, semantic.SemanticEvidence{
			EvidenceID: "evidence-budget-" + string(rune('a'+i)), Kind: "source", SourceAuthority: "code",
			ComputedBasisID: "basis-budget", SnapshotID: "snapshot-budget", ValidationStatus: "verified", RedactionStatus: "clean", Anchor: step.Anchor,
		})
	}
	return &semantic.SemanticMapIR{
		SchemaID: semantic.SemanticMapSchemaID, SchemaVersion: semantic.SemanticSchemaVersion,
		MapID: "map-budget", GenerationID: "generation-budget", ComputedBasisID: "basis-budget",
		ValidatedAgainstSnapshotID: "snapshot-budget", Freshness: "historical", Authority: "historical",
		Basis: semantic.MapBasisContext{RepositoryID: "repo-budget", ComputedWorkspaceSnapshotID: "snapshot-budget", ComputedBasisID: "basis-budget", SnapshotTreeID: "tree-budget"},
		Task:  semantic.MapTaskContext{Mode: "feature"}, Steps: steps, Evidence: evidence,
		Coverage: &semantic.CoverageBoundary{IncludedSourceRoots: []string{"src"}, ExcludedReasons: []string{}},
	}
}

func TestOnboardingCandidateRationaleUsesReferencedEvidenceWithoutValidationClaim(t *testing.T) {
	mapIR := onboardingBudgetTestMap()
	mapIR.Evidence[0].ValidationStatus = "pending"
	mapIR.Evidence[1].ValidationStatus = "verified"
	step := mapIR.Steps[0]
	flow := onboardingFlowSource{entry: canonicalOnboardingEntry(step)}
	rationale := onboardingCandidateRationale(flow, step)
	if !strings.Contains(rationale, "referenced map Evidence items") {
		t.Fatalf("rationale did not describe referenced Evidence: %q", rationale)
	}
	if strings.Contains(strings.ToLower(rationale), "verified") || strings.Contains(strings.ToLower(rationale), "unresolved") {
		t.Fatalf("rationale independently classified Evidence validation: %q", rationale)
	}
}

func TestOnboardingEgressRejectsInvalidPayloadAfterRedaction(t *testing.T) {
	invalid := map[string]any{
		"schemaId":      semantic.DomainOverviewSchemaID,
		"schemaVersion": 2,
		"repositoryId":  "repo-budget",
		"secret":        "password: should-not-leak",
	}
	if _, err := redactOnboardingJSON(invalid); err == nil {
		t.Fatal("redaction egress accepted an invalid v2 payload")
	}

	valid := onboardingBudgetTestMap()
	request := semantic.OnboardingRequestV2{
		Query: semantic.OnboardingQueryV2{
			SchemaID: semantic.OnboardingQuerySchemaID, SchemaVersion: semantic.SemanticSchemaVersion,
			RepositoryID: valid.Basis.RepositoryID, ComputedBasisID: valid.ComputedBasisID,
			GenerationID: valid.GenerationID, ValidatedAgainstSnapshotID: valid.ValidatedAgainstSnapshotID,
			Freshness: valid.Freshness, Level: 1,
		},
		Map:        valid,
		Candidates: []semantic.CandidateEntry{{EntrySymbolPath: canonicalOnboardingEntry(valid.Steps[0]), EvidenceRefs: valid.Steps[0].EvidenceRefs}},
	}
	output, err := semantic.ExploreDomainsV2(request)
	if err != nil {
		t.Fatalf("build valid onboarding output: %v", err)
	}
	payload, err := redactOnboardingJSON(output)
	if err != nil {
		t.Fatalf("valid onboarding output failed egress validation: %v", err)
	}
	if err := contractharness.ValidateDomainOverviewV2(payload); err != nil {
		t.Fatalf("redacted output did not pass exact v2 validator: %v", err)
	}
}

func TestHistoricalOnboardingReadsRequestedGenerationIndex(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codeflow-onboarding-history-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)
	srv, err := NewServer(Config{RepoRoot: tmpDir, Port: 0})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	mapIR := onboardingBudgetTestMap()
	mapIR.GenerationID = "generation-old"
	mapIR.ComputedBasisID = "basis-old"
	mapIR.ValidatedAgainstSnapshotID = "snapshot-old"
	mapIR.Basis.ComputedBasisID = "basis-old"
	mapIR.Basis.ComputedWorkspaceSnapshotID = "snapshot-old"
	for index := range mapIR.Evidence {
		mapIR.Evidence[index].ComputedBasisID = "basis-old"
		mapIR.Evidence[index].SnapshotID = "snapshot-old"
	}
	srv.mapCache = map[string]*semantic.SemanticMapIR{mapIR.GenerationID: mapIR}
	oldEntry := canonicalOnboardingEntry(mapIR.Steps[1])
	newEntry := canonicalOnboardingEntry(mapIR.Steps[0])
	writeOnboardingIndex(t, srv, "generation-old", storage.FlowSummary{FlowID: fusion.ComputeFlowID(oldEntry), Title: "old historical flow", EntrySymbolPath: oldEntry, StepCount: 1})
	writeOnboardingIndex(t, srv, "generation-new", storage.FlowSummary{FlowID: fusion.ComputeFlowID(newEntry), Title: "latest flow", EntrySymbolPath: newEntry, StepCount: 1})
	pointer, err := json.Marshal(storage.Pointer{GenerationID: "generation-new"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srv.storage.BaseDir(), "pointer.json"), pointer, 0o644); err != nil {
		t.Fatal(err)
	}

	query := "repositoryId=repo-budget&freshness=historical&computedBasisId=basis-old&generationId=generation-old&validatedAgainstSnapshotId=snapshot-old&level=2&domain=Orders&token=" + srv.AuthToken()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/onboarding?"+query, nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("requested historical generation failed: %d: %s", rec.Code, rec.Body.String())
	}
	var catalog semantic.RepresentativeFlowCatalogV2
	if err := json.Unmarshal(rec.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Flows) != 1 || catalog.Flows[0].Title != "old historical flow" || catalog.Flows[0].EntrySymbol != oldEntry {
		t.Fatalf("historical query used latest or pseudo fallback instead of requested index: %+v", catalog.Flows)
	}
}

func writeOnboardingIndex(t *testing.T, srv *Server, generation string, summary storage.FlowSummary) {
	t.Helper()
	dir := filepath.Join(srv.storage.BaseDir(), "generations", generation)
	if err := os.MkdirAll(filepath.Join(dir, "flows"), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(storage.GenerationIndex{GenerationID: generation, Flows: []storage.FlowSummary{summary}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}
