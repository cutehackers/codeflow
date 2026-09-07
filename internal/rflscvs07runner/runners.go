// Package rflscvs07runner contains the executable acceptance runners for the
// R2 VS-07 evidence-backed onboarding contract.  The runners intentionally
// call the public semantic onboarding seams.  They do not maintain a second
// domain/fact model: the corpus is represented by the canonical
// semantic.SemanticMapIR and semantic.CandidateEntry values consumed by the
// production projector.
package rflscvs07runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"codeflow/internal/fusion"
	"codeflow/internal/protocol"
	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
)

const (
	implementationPackage = "codeflow/internal/rflscvs07runner"
	repositoryID          = "repo-vs07-polyglot"
	basisID               = "basis-vs07-polyglot"
	generationID          = "generation-vs07-01"
)

// Evidence is the immutable result returned by one acceptance runner.  The
// contract harness adds the execution package, binary and subtest identity
// after the runner returns.  Every criterion carries references into one
// snapshot and one canonical generation.
type Evidence struct {
	Criterion             string
	ImplementationTestID  string
	ImplementationPackage string
	SnapshotID            string
	SnapshotTreeDigest    string
	ComputedBasisID       string
	GenerationID          string
	ObjectRefs            []string
}

var implementationTestIDs = map[string]string{
	"VS07-A1": "codeflow/internal/rflscvs07runner.TestRFLSCR2VS07_A01",
	"VS07-A2": "codeflow/internal/rflscvs07runner.TestRFLSCR2VS07_A02",
	"VS07-A3": "codeflow/internal/rflscvs07runner.TestRFLSCR2VS07_A03",
	"VS07-A4": "codeflow/internal/rflscvs07runner.TestRFLSCR2VS07_A04",
	"VS07-A5": "codeflow/internal/rflscvs07runner.TestRFLSCR2VS07_A05",
	"VS07-A6": "codeflow/internal/rflscvs07runner.TestRFLSCR2VS07_A06",
	"VS07-A7": "codeflow/internal/rflscvs07runner.TestRFLSCR2VS07_A07",
}

// Fixture is built from immutable source bytes and the canonical semantic
// graph.  Candidates are projections of graph steps, not a production list of
// named domains.  The corpus deliberately uses unrelated, non-DDD domain
// names and includes an unmapped dynamic module.
type Fixture struct {
	Snapshot   protocol.Snapshot
	Map        *semantic.SemanticMapIR
	Candidates []semantic.CandidateEntry
}

// NewFixture constructs one polyglot corpus with source-root, module/package,
// graph and Evidence signals.  NewSnapshot gives the registry a content-bound
// tree identity without exposing a repository path to the analyzer.
func NewFixture() (*Fixture, error) {
	files := map[string]string{
		"cmd/telemetry/main.go":       "package main\n\nfunc Start() { collect() }\nfunc collect() {}\n",
		"services/billing/charge.rb":  "class Billing\n  def charge\n  end\nend\n",
		"packages/media/thumbnail.ts": "export function renderThumbnail() {}\n",
		"src/i18n/catalog.rs":         "pub fn resolve_locale() {}\n",
		"scripts/reconcile.py":        "def reconcile():\n    return True\n",
		"plugins/dynamic_router.go":   "package plugins\n\nfunc Dispatch() {}\n",
	}
	snapshot, err := protocol.NewSnapshot(17, files, basisID)
	if err != nil {
		return nil, err
	}

	type stepSpec struct {
		id, symbol, file, kind, module, packageName, title string
	}
	specs := []stepSpec{
		{"telemetry-start", "main.Start", "cmd/telemetry/main.go", "entry", "telemetry", "main", "telemetry startup"},
		{"billing-charge", "Billing#charge", "services/billing/charge.rb", "effect", "billing", "Billing", "billing reconciliation"},
		{"media-thumbnail", "renderThumbnail", "packages/media/thumbnail.ts", "transform", "media", "media", "thumbnail rendering"},
		{"locale-resolve", "resolve_locale", "src/i18n/catalog.rs", "lookup", "localization", "i18n", "locale resolution"},
		{"script-reconcile", "reconcile", "scripts/reconcile.py", "worker", "maintenance", "scripts", "reconciliation worker"},
	}

	steps := make([]semantic.SemanticStep, 0, len(specs)+1)
	evidence := make([]semantic.SemanticEvidence, 0, len(specs)+1)
	candidates := make([]semantic.CandidateEntry, 0, len(specs)+1)
	for index, spec := range specs {
		anchor := fixtureAnchor(spec.file, spec.symbol, snapshot, index)
		evidenceID := "ev-vs07-" + spec.id
		steps = append(steps, semantic.SemanticStep{
			StepID:             spec.id,
			StructuralIdentity: strings.Join([]string{spec.file, spec.symbol, spec.symbol, spec.kind}, "\x00"),
			Ordinal:            index + 1,
			Name:               spec.symbol,
			TechnicalName:      spec.symbol,
			Kind:               spec.kind,
			Anchor:             anchor,
			EvidenceRefs:       []string{evidenceID},
		})
		evidence = append(evidence, semantic.SemanticEvidence{
			EvidenceID: evidenceID, Kind: "source", SourceAuthority: "code",
			ComputedBasisID: basisID, SnapshotID: snapshot.SnapshotID,
			DocumentRevisionID: "rev-" + spec.id, Anchor: anchor,
			ValidationStatus: "verified", RedactionStatus: "clean",
		})
		candidates = append(candidates, semantic.CandidateEntry{
			CandidateID: spec.id, EntrySymbolPath: spec.file + "#" + spec.symbol,
			Title: spec.title, SourceRoot: sourceRoot(spec.file), Module: spec.module, Package: spec.packageName,
		})
	}
	// The billing flow also has a separately anchored result claim.  It is not
	// an additional entry candidate, so the result Evidence remains a
	// canonical artifact without distorting the domain inventory.
	billingResultAnchor := fixtureAnchor("services/billing/charge.rb", "Billing#charge", snapshot, len(specs)+1)
	evidence = append(evidence, semantic.SemanticEvidence{
		EvidenceID: "ev-vs07-billing-result", Kind: "source", SourceAuthority: "code",
		ComputedBasisID: basisID, SnapshotID: snapshot.SnapshotID,
		DocumentRevisionID: "rev-billing-result", Anchor: billingResultAnchor,
		ValidationStatus: "verified", RedactionStatus: "clean",
	})
	for index := range candidates {
		if candidates[index].Module == "billing" {
			candidates[index].ResultEvidenceRefs = []string{"ev-vs07-billing-result"}
		}
	}

	// The dynamic router is part of the captured graph boundary but cannot be
	// assigned a confirmed domain.  It must remain visible as unmapped/unknown.
	unknownAnchor := fixtureAnchor("plugins/dynamic_router.go", "plugins.Dispatch", snapshot, len(specs))
	steps = append(steps, semantic.SemanticStep{
		StepID: "dynamic-dispatch", StructuralIdentity: strings.Join([]string{"plugins/dynamic_router.go", "plugins.Dispatch", "plugins.Dispatch", "boundary"}, "\x00"),
		Ordinal: len(specs) + 1, Name: "plugins.Dispatch", TechnicalName: "plugins.Dispatch",
		Kind: "boundary", Anchor: unknownAnchor, EvidenceRefs: []string{"ev-vs07-dynamic"},
	})
	evidence = append(evidence, semantic.SemanticEvidence{
		EvidenceID: "ev-vs07-dynamic", Kind: "source", SourceAuthority: "code",
		ComputedBasisID: basisID, SnapshotID: snapshot.SnapshotID,
		DocumentRevisionID: "rev-dynamic-dispatch", Anchor: unknownAnchor,
		ValidationStatus: "verified", RedactionStatus: "clean",
	})
	candidates = append(candidates, semantic.CandidateEntry{
		CandidateID: "dynamic-dispatch", EntrySymbolPath: "plugins/dynamic_router.go#plugins.Dispatch",
	})

	edges := make([]semantic.SemanticEdge, 0, len(steps)-1)
	for index := 0; index+1 < len(steps); index++ {
		edges = append(edges, semantic.SemanticEdge{
			FromStepID: steps[index].StepID, ToStepID: steps[index+1].StepID,
			ToSymbolPath: steps[index+1].StructuralIdentity, Kind: "observed_relation",
			ResolutionStatus: "verified",
		})
	}
	mapIR := &semantic.SemanticMapIR{
		SchemaID: semantic.SemanticMapSchemaID, SchemaVersion: semantic.SemanticSchemaVersion,
		MapID: "map-vs07-polyglot", GenerationID: generationID, ComputedBasisID: basisID,
		ValidatedAgainstSnapshotID: snapshot.SnapshotID, GenerationSequence: 1,
		PublicationKind: "checkpoint", Freshness: "historical", Settlement: "pending",
		EnrichmentStatus: "available", Authority: "historical",
		Quality: semantic.MapQuality{Stage: "Q2", UnresolvedCriticalCount: 1},
		Task:    semantic.MapTaskContext{TaskID: "task-vs07", IntentRevision: 1, Mode: "onboarding"},
		Basis: semantic.MapBasisContext{
			RepositoryID: repositoryID, ComputedWorkspaceSnapshotID: snapshot.SnapshotID,
			ComputedBasisID: basisID, SnapshotTreeID: snapshot.RootTreeID,
			WorkspaceEpoch: snapshot.WorkspaceEpoch, DependencyFingerprint: snapshot.DependencyFingerprint,
		},
		Summary: semantic.MapSummary{Requested: "explore evidence-backed domains", Current: "polyglot repository corpus"},
		Steps:   steps, Edges: edges, Evidence: evidence,
		Unknowns: []fusion.Unknown{{Subject: "plugins/dynamic_router.go#plugins.Dispatch", Reason: "dynamic dispatch has no verified domain mapping"}},
		Coverage: &semantic.CoverageBoundary{
			IncludedSourceRoots: []string{"cmd", "services", "packages", "src", "scripts", "plugins"},
			ExcludedReasons:     []string{"dynamic dispatch domain ownership is unresolved"},
		},
	}
	return &Fixture{Snapshot: snapshot, Map: mapIR, Candidates: candidates}, nil
}

func fixtureAnchor(file, symbol string, snapshot protocol.Snapshot, index int) slicing.Anchor {
	content := snapshot.Files[file]
	fileSum := sha256.Sum256([]byte(content))
	spanSum := sha256.Sum256([]byte(symbol + "\x00" + content))
	astSum := sha256.Sum256([]byte("ast:" + symbol))
	return slicing.Anchor{
		RepoRelativePath: file, ByteRange: [2]int{0, len([]byte(content))},
		FileHash: hex.EncodeToString(fileSum[:]), SpanHash: hex.EncodeToString(spanSum[:]),
		EnclosingSymbolPath: symbol, CanonicalAstFingerprint: hex.EncodeToString(astSum[:]),
		SymbolRange: func() *[2]int { r := [2]int{index, index + 1}; return &r }(),
	}
}

func requireFixture(t *testing.T, criterion string) *Fixture {
	t.Helper()
	fixture, err := NewFixture()
	if err != nil {
		t.Fatalf("%s fixture construction failed: %v", criterion, err)
	}
	if fixture.Map == nil || fixture.Map.ComputedBasisID != fixture.Snapshot.ComputedBasisID || fixture.Map.ValidatedAgainstSnapshotID != fixture.Snapshot.SnapshotID || fixture.Map.Basis.SnapshotTreeID != fixture.Snapshot.RootTreeID {
		t.Fatalf("%s fixture is not bound to one immutable basis: %+v", criterion, fixture.Map)
	}
	return fixture
}

func evidenceFor(criterion string, fixture *Fixture, refs ...string) Evidence {
	objectRefs := []string{
		"snapshot:" + fixture.Snapshot.SnapshotID,
		"tree:" + fixture.Snapshot.RootTreeID,
		"basis:" + fixture.Map.ComputedBasisID,
		"graph:" + fixture.Map.MapID,
		"generation:" + fixture.Map.GenerationID,
	}
	for _, item := range fixture.Map.Evidence {
		if item.EvidenceID != "" {
			objectRefs = append(objectRefs, "evidence:"+item.EvidenceID)
		}
	}
	objectRefs = append(objectRefs, refs...)
	return Evidence{
		Criterion: criterion, ImplementationTestID: implementationTestIDs[criterion],
		ImplementationPackage: implementationPackage, SnapshotID: fixture.Snapshot.SnapshotID,
		SnapshotTreeDigest: fixture.Snapshot.RootTreeID, ComputedBasisID: fixture.Map.ComputedBasisID,
		GenerationID: fixture.Map.GenerationID, ObjectRefs: objectRefs,
	}
}

func onboardingRequest(fixture *Fixture, candidates []semantic.CandidateEntry, level int) semantic.OnboardingRequestV2 {
	return semantic.OnboardingRequestV2{
		Query: semantic.OnboardingQueryV2{
			SchemaID: semantic.OnboardingQuerySchemaID, SchemaVersion: 2,
			RepositoryID: fixture.Map.Basis.RepositoryID, ComputedBasisID: fixture.Map.ComputedBasisID,
			GenerationID: fixture.Map.GenerationID, ValidatedAgainstSnapshotID: fixture.Map.ValidatedAgainstSnapshotID,
			Freshness: fixture.Map.Freshness, Level: level,
		},
		Map: fixture.Map, Candidates: candidates,
	}
}

func sourceRoot(file string) string {
	if index := strings.Index(file, "/"); index >= 0 {
		return file[:index]
	}
	return file
}

func overview(t *testing.T, fixture *Fixture, candidates []semantic.CandidateEntry) *semantic.DomainOverviewV2 {
	t.Helper()
	result, err := semantic.ExploreDomainsV2(onboardingRequest(fixture, candidates, 1))
	if err != nil {
		t.Fatalf("production onboarding overview failed: %v", err)
	}
	if result == nil || result.RepositoryID != repositoryID || result.ComputedBasisID != fixture.Map.ComputedBasisID || result.GenerationID != fixture.Map.GenerationID {
		t.Fatalf("onboarding overview lost canonical identity: %+v", result)
	}
	return result
}

func catalog(t *testing.T, fixture *Fixture, domain string, candidates []semantic.CandidateEntry) *semantic.RepresentativeFlowCatalogV2 {
	t.Helper()
	result, err := semantic.GetRepresentativeFlowCatalogV2(onboardingRequest(fixture, candidates, 2), domain)
	if err != nil {
		t.Fatalf("production onboarding catalog failed for %q: %v", domain, err)
	}
	if result == nil || result.ComputedBasisID != fixture.Map.ComputedBasisID || result.GenerationID != fixture.Map.GenerationID {
		t.Fatalf("catalog lost canonical identity: %+v", result)
	}
	return result
}

func marshalObject(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal onboarding result: %v", err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatalf("decode onboarding result: %v", err)
	}
	return object
}

func objectArray(t *testing.T, object map[string]any, key string) []map[string]any {
	t.Helper()
	values, ok := object[key].([]any)
	if !ok {
		t.Fatalf("onboarding result field %q is not an array: %#v", key, object[key])
	}
	result := make([]map[string]any, 0, len(values))
	for _, value := range values {
		item, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("onboarding result field %q contains non-object %#v", key, value)
		}
		result = append(result, item)
	}
	return result
}

func hasNonEmptyField(object map[string]any, keys ...string) bool {
	for _, key := range keys {
		value, ok := object[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return true
			}
		case []any:
			if len(typed) > 0 {
				return true
			}
		case map[string]any:
			if len(typed) > 0 {
				return true
			}
		default:
			return true
		}
	}
	return false
}

func field(object map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if value, ok := object[key]; ok {
			return value, true
		}
	}
	return nil, false
}

func reverseCandidates(in []semantic.CandidateEntry) []semantic.CandidateEntry {
	out := append([]semantic.CandidateEntry(nil), in...)
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return out
}

func sortedDomainNames(overview *semantic.DomainOverviewV2) []string {
	names := make([]string, 0, len(overview.Domains))
	for _, domain := range overview.Domains {
		names = append(names, domain.Name)
	}
	sort.Strings(names)
	return names
}

// RunA01 proves missing and unresolved repository identity fail closed.
func RunA01(t *testing.T) Evidence {
	t.Helper()
	fixture := requireFixture(t, "VS07-A1")
	request := onboardingRequest(fixture, fixture.Candidates, 1)
	request.Query.RepositoryID = ""
	if result, err := semantic.ExploreDomainsV2(request); err == nil || result != nil || !strings.Contains(err.Error(), "missing_precondition") {
		t.Fatalf("missing repository identity did not fail closed: result=%+v err=%v", result, err)
	}
	request = onboardingRequest(fixture, fixture.Candidates, 1)
	request.Query.RepositoryID = "repo-does-not-resolve"
	if result, err := semantic.ExploreDomainsV2(request); err == nil || result != nil || !strings.Contains(err.Error(), "missing_precondition") {
		t.Fatalf("unresolved repository identity did not fail closed: result=%+v err=%v", result, err)
	}
	return evidenceFor("VS07-A1", fixture, "precondition:repository-required", "precondition:repository-unresolved", "projection:none")
}

// RunA02 proves the projection accepts a nonstandard/polyglot corpus and does
// not manufacture the old Auth/Payment/Catalog example domains.
func RunA02(t *testing.T) Evidence {
	t.Helper()
	fixture := requireFixture(t, "VS07-A2")
	result := overview(t, fixture, fixture.Candidates)
	if len(result.Domains) < 4 {
		t.Fatalf("nonstandard corpus produced too few domains: %+v", result.Domains)
	}
	for _, expected := range []string{"Telemetry", "Billing", "Media", "Localization", "Maintenance"} {
		found := false
		for _, domain := range result.Domains {
			if domain.Name == expected {
				found = true
			}
		}
		if !found {
			t.Fatalf("graph-derived domain %q missing from overview: %+v", expected, result.Domains)
		}
	}
	for _, domain := range result.Domains {
		if domain.Name == "Auth" || domain.Name == "Payment" || domain.Name == "Catalog" {
			t.Fatalf("legacy fabricated domain escaped: %+v", domain)
		}
	}
	return evidenceFor("VS07-A2", fixture, "domains:polyglot", "signals:source-root", "signals:module-package", "signals:graph", "signals:evidence")
}

// RunA03 requires every emitted domain candidate to explain its label and
// expose Evidence, confidence/epistemic status and coverage boundary.
func RunA03(t *testing.T) Evidence {
	t.Helper()
	fixture := requireFixture(t, "VS07-A3")
	result := overview(t, fixture, fixture.Candidates)
	object := marshalObject(t, result)
	for index, domain := range objectArray(t, object, "domains") {
		if !hasNonEmptyField(domain, "domainId") || !hasNonEmptyField(domain, "name") {
			t.Fatalf("domain candidate %d has incomplete identity: %+v", index, domain)
		}
		if !hasNonEmptyField(domain, "rationale", "rationaleText", "selectionRationale") {
			t.Fatalf("domain candidate %d has no rationale: %+v", index, domain)
		}
		if !hasNonEmptyField(domain, "evidence", "evidenceRefs", "ownershipEvidence", "glossaryEvidence") {
			t.Fatalf("domain candidate %d has no Evidence refs: %+v", index, domain)
		}
		if !hasNonEmptyField(domain, "confidence", "confidenceScore", "epistemicState") {
			t.Fatalf("domain candidate %d has no confidence/epistemic state: %+v", index, domain)
		}
		if !hasNonEmptyField(domain, "coverageBoundary", "coverage") {
			t.Fatalf("domain candidate %d has no coverage boundary: %+v", index, domain)
		}
	}
	return evidenceFor("VS07-A3", fixture, "rationale:per-domain", "evidence:ownership", "evidence:glossary", "coverage:per-domain", "epistemic-state:explicit")
}

// RunA04 proves ranking and IDs are deterministic under input reorder, and
// each selected flow exposes a reason plus entry/result Evidence.
func RunA04(t *testing.T) Evidence {
	t.Helper()
	fixture := requireFixture(t, "VS07-A4")
	first := catalog(t, fixture, "Billing", fixture.Candidates)
	second := catalog(t, fixture, "Billing", reverseCandidates(fixture.Candidates))
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("representative flow ranking/identity changed after input reorder:\nfirst=%s\nsecond=%s", firstJSON, secondJSON)
	}
	if len(first.Flows) == 0 {
		t.Fatal("representative flow catalog is empty for a mapped domain")
	}
	object := marshalObject(t, first)
	for index, flow := range objectArray(t, object, "flows") {
		if !hasNonEmptyField(flow, "flowId") || !hasNonEmptyField(flow, "entrySymbol", "entrySymbolPath") {
			t.Fatalf("flow %d has incomplete identity: %+v", index, flow)
		}
		if !hasNonEmptyField(flow, "selectionReason", "rationale", "selectionRationale") {
			t.Fatalf("flow %d has no selection reason: %+v", index, flow)
		}
		if !hasNonEmptyField(flow, "entryEvidence", "entryEvidenceRefs") || !hasNonEmptyField(flow, "resultEvidence", "resultEvidenceRefs") {
			t.Fatalf("flow %d lacks entry/result Evidence: %+v", index, flow)
		}
	}
	return evidenceFor("VS07-A4", fixture, "ranking:deterministic", "identity:stable-under-reorder", "selection:reason", "entry-evidence:verified", "result-evidence:verified")
}

// RunA05 preserves an unmapped module and an incomplete coverage boundary.
func RunA05(t *testing.T) Evidence {
	t.Helper()
	fixture := requireFixture(t, "VS07-A5")
	result := overview(t, fixture, fixture.Candidates)
	if len(result.UnmappedModules) == 0 {
		t.Fatalf("unmapped dynamic module was silently omitted: %+v", result)
	}
	found := false
	for _, path := range result.UnmappedModules {
		if strings.Contains(path, "dynamic_router") || strings.Contains(path, "Dispatch") {
			found = true
		}
	}
	if !found {
		t.Fatalf("unmapped list does not identify the dynamic module: %+v", result.UnmappedModules)
	}
	object := marshalObject(t, result)
	if !hasNonEmptyField(object, "coverageBoundary", "coverage") {
		t.Fatalf("overview hid coverage boundary: %+v", object)
	}
	if !hasNonEmptyField(object, "recoveryGuidance", "recovery", "unknowns") {
		t.Fatalf("overview hid recovery guidance/unknown state: %+v", object)
	}
	return evidenceFor("VS07-A5", fixture, "unmapped:dynamic-router", "unknown:preserved", "coverage:incomplete", "recovery:guidance")
}

// RunA06 proves representative-flow selection retains the same canonical
// basis/generation and points back to the existing map rather than creating a
// second fact graph.
func RunA06(t *testing.T) Evidence {
	t.Helper()
	fixture := requireFixture(t, "VS07-A6")
	catalogResult := catalog(t, fixture, "Media", fixture.Candidates)
	if catalogResult.GenerationID != fixture.Map.GenerationID || catalogResult.ComputedBasisID != fixture.Map.ComputedBasisID {
		t.Fatalf("drilldown changed canonical generation/basis: %+v", catalogResult)
	}
	if len(catalogResult.Flows) == 0 {
		t.Fatal("selected domain did not yield a representative flow")
	}
	drilldown, err := semantic.DrilldownRepresentativeFlowV2(onboardingRequest(fixture, fixture.Candidates, 2), "Media", catalogResult.Flows[0].FlowID)
	if err != nil {
		t.Fatalf("canonical same-map drilldown failed: %v", err)
	}
	if drilldown.CanonicalMapID != fixture.Map.MapID || drilldown.ComputedBasisID != fixture.Map.ComputedBasisID || drilldown.GenerationID != fixture.Map.GenerationID || len(drilldown.StepRefs) == 0 {
		t.Fatalf("drilldown changed canonical identity or lost step refs: %+v", drilldown)
	}
	object := marshalObject(t, catalogResult)
	for index, flow := range objectArray(t, object, "flows") {
		grounded, ok := field(flow, "groundedMapId", "canonicalMapId", "flowMapId")
		if !ok || strings.TrimSpace(fmt.Sprint(grounded)) == "" {
			t.Fatalf("flow %d has no canonical map drilldown ref: %+v", index, flow)
		}
		if strings.TrimSpace(fmt.Sprint(grounded)) != fixture.Map.MapID {
			t.Fatalf("flow %d points at a fabricated or separate fact map %q, want canonical map %q", index, grounded, fixture.Map.MapID)
		}
		if generation, ok := field(flow, "generationId"); ok && strings.TrimSpace(fmt.Sprint(generation)) != fixture.Map.GenerationID {
			t.Fatalf("flow %d changed generation identity: %+v", index, flow)
		}
	}
	return evidenceFor("VS07-A6", fixture, "drilldown:same-generation", "drilldown:canonical-map", "fact-model:shared")
}

// RunA07 proves onboarding evidence is redacted before the value reaches the
// public JSON egress.  The secret is placed in a candidate title so this
// assertion covers a real projection field instead of a detached helper.
func RunA07(t *testing.T) Evidence {
	t.Helper()
	fixture := requireFixture(t, "VS07-A7")
	candidates := append([]semantic.CandidateEntry(nil), fixture.Candidates...)
	for index := range candidates {
		if candidates[index].Module == "billing" {
			candidates[index].Title = "billing token=vs07-secret-value"
		}
	}
	result := overview(t, fixture, candidates)
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "vs07-secret-value") {
		t.Fatalf("onboarding secret escaped public egress: %s", raw)
	}
	if !strings.Contains(strings.ToLower(string(raw)), "redact") {
		t.Fatalf("onboarding egress did not expose redaction status: %s", raw)
	}
	return evidenceFor("VS07-A7", fixture, "egress:redacted", "secret:removed", "boundary:mcp-flowview")
}

// ValidateEvidence rejects records that are not bound to the exact canonical
// snapshot and graph.  The contract harness separately checks execution
// identity and completed status, while this function prevents fabricated
// artifact references from entering the registry.
func ValidateEvidence(evidence Evidence) error {
	if evidence.Criterion == "" || implementationTestIDs[evidence.Criterion] == "" || evidence.ImplementationTestID != implementationTestIDs[evidence.Criterion] || evidence.ImplementationPackage != implementationPackage {
		return fmt.Errorf("VS07 evidence identity is incomplete or unexpected: %+v", evidence)
	}
	if evidence.SnapshotID == "" || evidence.SnapshotTreeDigest == "" || evidence.ComputedBasisID == "" || evidence.GenerationID == "" {
		return fmt.Errorf("VS07 evidence lacks immutable snapshot/basis/generation identity: %+v", evidence)
	}
	if len(evidence.ObjectRefs) < 5 {
		return fmt.Errorf("VS07 evidence has too few immutable refs: %+v", evidence.ObjectRefs)
	}
	seen := make(map[string]bool, len(evidence.ObjectRefs))
	hasSnapshot, hasTree, hasBasis, hasGraph, hasGeneration, hasEvidence, hasArtifact := false, false, false, false, false, false, false
	for _, ref := range evidence.ObjectRefs {
		parts := strings.SplitN(ref, ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" || seen[ref] {
			return fmt.Errorf("invalid or duplicate VS07 immutable ref %q", ref)
		}
		seen[ref] = true
		switch parts[0] {
		case "snapshot":
			if parts[1] != evidence.SnapshotID {
				return fmt.Errorf("snapshot ref %q does not match %q", ref, evidence.SnapshotID)
			}
			hasSnapshot = true
		case "tree":
			if parts[1] != evidence.SnapshotTreeDigest {
				return fmt.Errorf("tree ref %q does not match %q", ref, evidence.SnapshotTreeDigest)
			}
			hasTree = true
		case "basis":
			if parts[1] != evidence.ComputedBasisID {
				return fmt.Errorf("basis ref %q does not match %q", ref, evidence.ComputedBasisID)
			}
			hasBasis = true
		case "graph":
			hasGraph = true
		case "generation":
			if parts[1] != evidence.GenerationID {
				return fmt.Errorf("generation ref %q does not match %q", ref, evidence.GenerationID)
			}
			hasGeneration = true
		case "evidence":
			hasEvidence = true
		case "precondition", "projection", "domains", "signals", "rationale", "coverage", "epistemic-state", "ranking", "identity", "selection", "entry-evidence", "result-evidence", "unmapped", "unknown", "recovery", "drilldown", "fact-model", "egress", "secret", "boundary":
			hasArtifact = true
		default:
			return fmt.Errorf("unknown VS07 immutable ref kind %q", parts[0])
		}
	}
	if !hasSnapshot || !hasTree || !hasBasis || !hasGraph || !hasGeneration || !hasEvidence || !hasArtifact {
		return fmt.Errorf("VS07 evidence requires snapshot, tree, basis, graph, generation, Evidence and criterion refs: %+v", evidence.ObjectRefs)
	}
	return nil
}
