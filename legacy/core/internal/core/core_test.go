package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	compare "codeflow/core/internal/comparison"
	"codeflow/core/internal/delta"
	"codeflow/core/internal/flowir"
	"codeflow/core/internal/manifest"
	"codeflow/core/internal/ontology"
	"codeflow/core/internal/store"
	"golang.org/x/net/html"
)

func TestFlowViewTemplateV1IsFlowAgnosticAndKeepsStableRegions(t *testing.T) {
	lower := strings.ToLower(flowViewSource)
	for _, productWord := range []string{"signup", "joinpage", "회원가입"} {
		if strings.Contains(lower, strings.ToLower(productWord)) {
			t.Fatalf("FlowView template contains product-specific word %q", productWord)
		}
	}
	if !strings.Contains(flowViewSource, `data-flowview-version="1"`) {
		t.Fatal("FlowView template version is missing")
	}
	for _, liveContract := range []string{`data-publication="{{.Publication}}"`, `fetch('/_codeflow/publication'`, `window.location.reload()`} {
		if !strings.Contains(flowViewSource, liveContract) {
			t.Fatalf("FlowView live publication contract is missing: %s", liveContract)
		}
	}
	previous := -1
	for _, region := range []string{"snapshot", "business-journeys", "workspace-map", "domain-scenarios", "timeline-navigation", "timeline", "step-detail", "impact-chain", "code-lens", "architecture", "cognitive-debt"} {
		position := strings.Index(flowViewSource, `data-region="`+region+`"`)
		if position < 0 || position <= previous {
			t.Fatalf("missing or reordered stable region %q", region)
		}
		previous = position
	}
	for _, contract := range []string{`data-break-after=`, `data-alternative=`, `data-jump-step=`, `class="branch-jump"`} {
		if !strings.Contains(flowViewSource, contract) {
			t.Fatalf("FlowView branch-navigation contract %q is missing", contract)
		}
	}
	if !strings.Contains(flowViewSource, "function render(index, shouldScroll = true)") || !strings.Contains(flowViewSource, "render(0, false)") || strings.Contains(flowViewSource, "index !== 0") {
		t.Fatal("FlowView must scroll both maps when a user returns to the first step, while keeping initial render still")
	}
	for _, interaction := range []string{
		`buttons.forEach((button, i) => button.addEventListener('click', () => render(i)))`,
		`branchJumps.forEach((jump) => jump.addEventListener('click', () => render(Number(jump.dataset.jumpStep))))`,
		`if (architectureMap.open) window.requestAnimationFrame(() => render(selected))`,
		`workbench?.scrollIntoView({ block: 'start', behavior: 'smooth' })`,
	} {
		if !strings.Contains(flowViewSource, interaction) {
			t.Fatalf("FlowView bidirectional selection contract is missing: %s", interaction)
		}
	}
	for _, responsive := range []string{
		`code, .mono { overflow-wrap: anywhere; }`,
		`.flow-workbench { display: grid; min-width: 0;`,
		`.flow-workbench, .detail-grid, .delta-grid { grid-template-columns: minmax(0, 1fr); }`,
	} {
		if !strings.Contains(flowViewSource, responsive) {
			t.Fatalf("FlowView narrow-screen overflow guard is missing: %s", responsive)
		}
	}
	for _, codeLensContract := range []string{
		`class="source-toolbar"`,
		`class="source-code" tabindex="0"`,
		`class="code-line" data-selected=`,
		`.code-line-number { position: sticky;`,
		`white-space: nowrap; overflow-wrap: normal;`,
		`.code-line-text { min-width: 0; padding: 0 10px; white-space: pre; overflow-wrap: normal;`,
	} {
		if !strings.Contains(flowViewSource, codeLensContract) {
			t.Fatalf("FlowView readable code-lens contract is missing: %s", codeLensContract)
		}
	}
	if strings.Contains(flowViewSource, "aria-selected") || !strings.Contains(flowViewSource, `aria-pressed=`) {
		t.Fatal("FlowView buttons must expose native pressed state rather than invalid aria-selected semantics")
	}
}

func TestStaticExportLeavesWorkspaceCardsNonNavigable(t *testing.T) {
	model := flowViewModel{Export: true, Journeys: journeyNavigation{Journeys: []journeyNavigationItem{{ID: "complete-signup", Segments: []journeySegmentNavigation{{FlowID: "route:/join", ScenarioID: "scenario:one"}}}}}, Workspace: workspaceNavigation{Flows: []workspaceNavigationFlow{{ID: "route:/join", Selected: true}, {ID: "route:/sign-in"}}}}
	var exported bytes.Buffer
	if err := exportPage.Execute(&exported, model); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(exported.String(), `href="/?flow=`) {
		t.Fatal("static export retained workspace navigation links")
	}
	if strings.Contains(exported.String(), `href="/?journey=`) || strings.Contains(exported.String(), `data-business-journey="complete-signup" href=`) {
		t.Fatal("static export retained business journey navigation links")
	}
}

func TestRuntimeTokenComparisonRejectsEmptyAndNearMatches(t *testing.T) {
	if secureToken("", "") || secureToken("secret", "secreu") || secureToken("secret-extra", "secret") || !secureToken("secret", "secret") {
		t.Fatal("runtime token comparison accepted an empty or non-exact credential")
	}
}

func TestCloseCancelsPendingReconcileBeforeOwnedResources(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "lib/signup.dart"), []byte("void signup() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	instance, err := StartFixture(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	captured := make(chan struct{}, 1)
	instance.capture = func(string) (flowir.Basis, error) {
		captured <- struct{}{}
		return flowir.Basis{}, nil
	}
	instance.ScheduleReconcile()
	if err := instance.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	select {
	case <-captured:
		t.Fatal("reconcile ran after Core released its analyzer and storage")
	default:
	}
}

func TestTimelineSeparatesAdjacentAlternativeBranchOutcomes(t *testing.T) {
	condition := flowir.Fact{ID: "condition", Kind: "condition", Object: "state?.isCompleted ?? false", Status: flowir.Observed}
	home := flowir.Fact{ID: "home", Kind: "visible_result", Object: "route:/home", Status: flowir.Observed}
	stop := flowir.Fact{ID: "stop", Kind: "visible_result", Object: "terminal:no_navigation", Status: flowir.Observed}
	document := flowir.Document{
		Facts: []flowir.Fact{condition, home, stop},
		Current: flowir.Flow{Steps: []flowir.Step{
			{ID: "condition-step", Order: 1, BehaviorFacts: []string{condition.ID}, Status: flowir.Observed, Branches: []flowir.Branch{{ID: "branch", ConditionFact: condition.ID, OutcomeStepIDs: []string{"home-step", "stop-step"}, Status: flowir.Observed}}},
			{ID: "home-step", Order: 2, ResultFacts: []string{home.ID}, Status: flowir.Observed},
			{ID: "stop-step", Order: 3, ResultFacts: []string{stop.ID}, Status: flowir.Observed},
		}},
	}

	items := timeline(document, nil)
	if len(items) != 3 || len(items[0].Branches) != 1 {
		t.Fatalf("unexpected timeline projection: %#v", items)
	}
	branch := items[0].Branches[0]
	if branch.Condition != "이전 작업이 완료되었는지 확인합니다" {
		t.Fatalf("condition was not expressed as reader-facing domain language: %q", branch.Condition)
	}
	if len(branch.Outcomes) != 2 || branch.Outcomes[0].StepIndex != 1 || branch.Outcomes[1].StepIndex != 2 {
		t.Fatalf("branch outcomes do not target their timeline steps: %#v", branch.Outcomes)
	}
	if !items[1].BreakAfter || !items[2].Alternative || items[1].BranchPath != "분기 1 · 경로 A" || items[2].BranchPath != "분기 1 · 경로 B" {
		t.Fatalf("adjacent alternatives still look sequential: %#v %#v", items[1], items[2])
	}
}

func TestDisplayFactUsesReaderLanguageWithoutLosingCodeIdentity(t *testing.T) {
	action := flowir.Fact{Kind: "user_action", Subject: "package:account/join.dart::class:_JoinPageState::method:_requestExit"}
	condition := flowir.Fact{Kind: "confirmation_condition", Object: "confirmed == true"}
	if got := displayFact(action); got != "사용자 동작 · 나가기 요청" {
		t.Fatalf("private symbol leaked into primary action label: %q", got)
	}
	if got := displayFact(condition); got != "계속 진행해도 되는지 확인합니다" {
		t.Fatalf("condition was not expressed as reader-facing domain language: %q", got)
	}
	if shortSymbol(action.Subject) != "_JoinPageState._requestExit" {
		t.Fatal("reader label must not destroy the underlying code identity")
	}
	provider := flowir.Fact{Kind: "provider_dependency", Object: "provider:routeDestinationDispatcherProvider"}
	if got := displayFact(provider); got != "이 단계에 필요한 정보를 확인합니다" {
		t.Fatalf("provider jargon leaked into reader label: %q", got)
	}
	event := flowir.Fact{Kind: "event_dispatch", Object: "event:JoinCancelEvent"}
	if got := displayFact(event); got != "다음 처리를 요청합니다" {
		t.Fatalf("event jargon leaked into reader label: %q", got)
	}
	visibleAction := flowir.Fact{Kind: "user_action", Subject: "Page.submit", Object: "이메일로 가입"}
	if got := displayFact(visibleAction); got != "“이메일로 가입”을 선택합니다" {
		t.Fatalf("source-backed action copy was not used as the scenario label: %q", got)
	}
}

func TestScenarioSelectionScopesTimelineToOneUserJourney(t *testing.T) {
	anchor := flowir.Anchor{Kind: "code", Path: "lib/join.dart", FileHash: "source"}
	entry := flowir.Fact{ID: "entry", Kind: "entry_point", Subject: "route:/join", Status: flowir.Observed, Evidence: []flowir.Anchor{anchor}}
	email := flowir.Fact{ID: "email", Kind: "user_action", Subject: "Join.email", Object: "이메일로 가입", Status: flowir.Observed, Evidence: []flowir.Anchor{anchor}}
	phone := flowir.Fact{ID: "phone", Kind: "user_action", Subject: "Join.phone", Object: "전화번호로 가입", Status: flowir.Observed, Evidence: []flowir.Anchor{anchor}}
	emailResult := flowir.Fact{ID: "email-result", Kind: "visible_result", Subject: "Join.email", Object: "route:/verify-email", Status: flowir.Observed, Evidence: []flowir.Anchor{{Kind: "code", Path: anchor.Path, Symbol: "route", FileHash: anchor.FileHash}}}
	phoneResult := flowir.Fact{ID: "phone-result", Kind: "visible_result", Subject: "Join.phone", Object: "route:/verify-phone", Status: flowir.Observed, Evidence: []flowir.Anchor{{Kind: "code", Path: anchor.Path, Symbol: "route", FileHash: anchor.FileHash}}}
	document := flowir.Document{Facts: []flowir.Fact{entry, email, phone, emailResult, phoneResult}, Current: flowir.Flow{ID: "route:/join", EntryPointFact: entry.ID, Steps: []flowir.Step{
		{ID: "email-action", BehaviorKey: "email-action", Order: 1, Actor: "user", TriggerFact: entry.ID, BehaviorFacts: []string{email.ID}, Status: flowir.Observed, PrimaryEvidence: []flowir.Anchor{anchor}},
		{ID: "email-result-step", BehaviorKey: "email-result", Order: 2, Actor: "system", TriggerFact: email.ID, ResultFacts: []string{emailResult.ID}, Status: flowir.Observed, PrimaryEvidence: []flowir.Anchor{anchor}},
		{ID: "phone-action", BehaviorKey: "phone-action", Order: 3, Actor: "user", TriggerFact: entry.ID, BehaviorFacts: []string{phone.ID}, Status: flowir.Observed, PrimaryEvidence: []flowir.Anchor{anchor}},
		{ID: "phone-result-step", BehaviorKey: "phone-result", Order: 4, Actor: "system", TriggerFact: phone.ID, ResultFacts: []string{phoneResult.ID}, Status: flowir.Observed, PrimaryEvidence: []flowir.Anchor{anchor}},
	}}}
	flowir.DeriveScenarios(&document)
	if len(document.Scenarios) != 2 {
		t.Fatalf("scenario projection=%#v", document.Scenarios)
	}
	scoped, scenario := scopeScenario(document, document.Scenarios[0].ID)
	if scenario == nil || len(scoped.Current.Steps) != 2 || scoped.Current.Steps[0].ID != "email-action" {
		t.Fatalf("scenario scope retained another action: %#v", scoped.Current.Steps)
	}
	items := timeline(scoped, nil)
	if len(items) != 2 || strings.Contains(items[0].Title+items[1].Title, "전화번호") {
		t.Fatalf("timeline mixed independent sign-up methods: %#v", items)
	}
	navigation := buildScenarioNavigation(document, scenario.ID, nil, nil)
	if navigation.Selected == nil || navigation.Selected.Title != "이메일로 가입" {
		t.Fatalf("scenario navigation lost source-backed user label: %#v", navigation)
	}
}

func TestArchitectureFlowOmitsEmptyLanes(t *testing.T) {
	action := flowir.Fact{ID: "action", Kind: "user_action", Subject: "Page.submit", Status: flowir.Observed}
	state := flowir.Fact{ID: "state", Kind: "state_transition", Subject: "Notifier.submit", Object: "state:done", Status: flowir.Observed}
	document := flowir.Document{Facts: []flowir.Fact{action, state}}
	items := []timelineItem{
		{Step: flowir.Step{ID: "action-step", Actor: "user", BehaviorFacts: []string{action.ID}, Status: flowir.Observed}, Title: "사용자 동작"},
		{Step: flowir.Step{ID: "state-step", Actor: "system", ResultFacts: []string{state.ID}, Status: flowir.Observed}, Title: "상태 변경"},
	}
	view := architectureFlowView(document, items)
	if len(view.Lanes) != 2 || view.Lanes[0].ID != "interface" || view.Lanes[1].ID != "state" {
		t.Fatalf("sparse architecture map retained empty lanes: %#v", view.Lanes)
	}
}

func TestAffectedFlowIDsRecompileOnlyProvenImpactSet(t *testing.T) {
	base := flowir.Basis{HeadRevision: "head", Manifest: []flowir.ManifestEntry{
		{Path: "lib/a.dart", Type: "file", Mode: "0644", FileHash: "a1", GitState: "clean"},
		{Path: "lib/b.dart", Type: "file", Mode: "0644", FileHash: "b1", GitState: "clean"},
		{Path: "README.md", Type: "file", Mode: "0644", FileHash: "r1", GitState: "clean"},
		{Path: "config/route.map", Type: "file", Mode: "0644", FileHash: "c1", GitState: "clean"},
	}}
	documents := []flowir.Document{
		{Basis: base, Current: flowir.Flow{ID: "route:/a"}, Facts: []flowir.Fact{{Evidence: []flowir.Anchor{{Kind: "code", Path: "lib/a.dart", FileHash: "a1"}}}}, CausalEdges: []flowir.CausalEdge{{Evidence: []flowir.Anchor{{Kind: "config", Path: "config/route.map", FileHash: "c1"}}}}},
		{Basis: base, Current: flowir.Flow{ID: "route:/b"}, Facts: []flowir.Fact{{Evidence: []flowir.Anchor{{Kind: "code", Path: "lib/b.dart", FileHash: "b1"}}}}},
	}
	changedA := base
	changedA.Manifest = append([]flowir.ManifestEntry(nil), base.Manifest...)
	changedA.Manifest[0].FileHash = "a2"
	if got := affectedFlowIDs(documents, changedA); len(got) != 1 || got[0] != "route:/a" {
		t.Fatalf("known source impact was not scoped to its flow: %#v", got)
	}
	readme := base
	readme.Manifest = append([]flowir.ManifestEntry(nil), base.Manifest...)
	readme.Manifest[2].FileHash = "r2"
	if got := affectedFlowIDs(documents, readme); len(got) != 0 {
		t.Fatalf("unreferenced documentation forced analysis: %#v", got)
	}
	causalEvidence := base
	causalEvidence.Manifest = append([]flowir.ManifestEntry(nil), base.Manifest...)
	causalEvidence.Manifest[3].FileHash = "c2"
	if got := affectedFlowIDs(documents, causalEvidence); len(got) != 1 || got[0] != "route:/a" {
		t.Fatalf("causal-edge evidence was omitted from impact analysis: %#v", got)
	}
	if err := anchorsMatchBasis(documents[0], base); err != nil {
		t.Fatalf("current anchors were rejected: %v", err)
	}
	if err := anchorsMatchBasis(documents[0], changedA); err == nil {
		t.Fatal("stale evidence hash was allowed onto a new basis")
	}
	newDart := base
	newDart.Manifest = append(append([]flowir.ManifestEntry(nil), base.Manifest...), flowir.ManifestEntry{Path: "lib/new.dart", Type: "file", Mode: "0644", FileHash: "new", GitState: "untracked"})
	if got := affectedFlowIDs(documents, newDart); len(got) != 2 {
		t.Fatalf("new Dart input did not conservatively affect all flows: %#v", got)
	}
	newHead := base
	newHead.HeadRevision = "next"
	if got := affectedFlowIDs(documents, newHead); len(got) != 2 {
		t.Fatalf("revision change did not invalidate every anchor: %#v", got)
	}
}

func TestWorkspaceEdgesRequireObservedExactSameBasisTargets(t *testing.T) {
	basis := flowir.Basis{Repository: "/repo", HeadRevision: "revision", WorktreeFingerprint: "fingerprint", Manifest: []flowir.ManifestEntry{}}
	join := flowir.Document{Basis: basis, Current: flowir.Flow{ID: "route:/join"}, Facts: []flowir.Fact{
		{Kind: "visible_result", Object: "route:/home", Status: flowir.Observed},
		{Kind: "route_transition", Object: "route:/auth", Status: flowir.Unknown},
		{Kind: "route_transition", Object: "route:/missing", Status: flowir.Observed},
		{Kind: "route_transition", Object: "route:/join", Status: flowir.Observed},
	}}
	home := flowir.Document{Basis: basis, Current: flowir.Flow{ID: "route:/home"}}
	auth := flowir.Document{Basis: basis, Current: flowir.Flow{ID: "route:/auth"}}
	workspace, err := buildWorkspace([]flowir.Document{join, home, auth})
	if err != nil || len(workspace.Edges) != 1 || workspace.Edges[0].From != "route:/join" || workspace.Edges[0].To != "route:/home" {
		t.Fatalf("workspace admitted an inferred, missing, or self edge: %#v err=%v", workspace.Edges, err)
	}
	mixed := auth
	mixed.Basis.WorktreeFingerprint = "different"
	if _, err := buildWorkspace([]flowir.Document{join, home, mixed}); err == nil {
		t.Fatal("cross-revision workspace was accepted")
	}
}

func TestCognitiveDebtReviewIsAuthenticatedAndDoesNotRewriteFlowIR(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), []byte("void signup() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	c, err := StartFixture(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	document, err := c.Document(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	anchor := document.Current.Steps[0].PrimaryEvidence[0]
	debtID := flowir.Hash("debt", "signup-result")
	document.Unknowns = []flowir.UnknownDetail{{
		ID:                 debtID,
		Question:           "어떤 사용자 결과가 이어지는가?",
		Reason:             "missing_relation",
		RelatedSteps:       []string{document.Current.Steps[0].ID},
		Evidence:           []flowir.Anchor{anchor},
		DebtState:          "open",
		ResolutionCriteria: []string{"현재 코드 또는 테스트가 결과를 증명한다."},
		SuggestedEvidence:  []string{"route assertion"},
	}}
	if err := c.store.Publish(context.Background(), document, "with-debt", "ready"); err != nil {
		t.Fatal(err)
	}
	before, _ := flowir.CanonicalJSON(document)

	unauthorized, _ := http.Post(c.URL+"/api/v1/debt/review", "application/json", strings.NewReader(`{"id":"`+debtID+`","state":"accepted"}`))
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized review status=%d", unauthorized.StatusCode)
	}
	unauthorized.Body.Close()

	request, _ := http.NewRequest(http.MethodPost, c.URL+"/api/v1/debt/review", strings.NewReader(`{"id":"`+debtID+`","state":"accepted"}`))
	request.Header.Set("X-CodeFlow-Token", c.Token)
	response, err := http.DefaultClient.Do(request)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("review err=%v status=%v", err, response)
	}
	response.Body.Close()
	after, _, _, err := c.store.Get(context.Background(), document.Current.ID)
	if err != nil {
		t.Fatal(err)
	}
	afterBytes, _ := flowir.CanonicalJSON(after)
	if !bytes.Equal(before, afterBytes) {
		t.Fatal("review status must remain separate from deterministic FlowIR")
	}

	view, err := http.Get(c.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	html, _ := io.ReadAll(view.Body)
	view.Body.Close()
	if !strings.Contains(string(html), `data-debt-state="accepted"`) || !strings.Contains(string(html), "현재 코드에서 확인된 내용과 빠진 연결") || !strings.Contains(string(html), "코드에서 확인할 것:") || strings.Contains(string(html), "완료 조건:") || strings.Contains(string(html), "필요 증거:") {
		t.Fatalf("accepted actionable debt missing from FlowView: %s", html)
	}
}

func TestComparisonAPIAndFlowViewPresentBaselineDelta(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "lib/signup.dart"), []byte("void signup() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	c, err := StartFixture(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	doc, err := c.Document(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c.comparison = &compare.Result{Current: doc, Baseline: doc, Delta: delta.Delta{BaselineRevision: "abc123", CurrentRevision: doc.Basis.HeadRevision, AddedSteps: []string{"new"}}}
	req, _ := http.NewRequest(http.MethodGet, c.URL+"/api/v1/compare", nil)
	req.Header.Set("X-CodeFlow-Token", c.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Data compare.Result `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || body.Data.Delta.BaselineRevision != "abc123" {
		t.Fatalf("comparison API %#v status=%d", body, resp.StatusCode)
	}
	page, err := http.Get(c.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	html, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if !strings.Contains(string(html), `aria-label="기준선과 달라진 동작"`) || !strings.Contains(string(html), `data-baseline="abc123"`) {
		t.Fatalf("missing FlowView delta: %s", html)
	}
}

func TestSemanticOverlayNeverChangesFlowIRAndOnlyApprovalPersistsKnowledge(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), []byte("void signup() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	c, err := StartFixture(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	before, err := c.Document(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	beforeBytes, _ := flowir.CanonicalJSON(before)
	raw := `{"class":"decision","text":"This action submits the order"}
{"class":"intent","text":"api_key=must-not-store"}
{"class":"tool_output","text":"unapproved event"}`
	request, _ := http.NewRequest(http.MethodPost, c.URL+"/api/v1/overlay/import", strings.NewReader(raw))
	request.Header.Set("X-CodeFlow-Token", c.Token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var imported []struct{ ID, Text, Status string }
	if err := json.NewDecoder(response.Body).Decode(&struct {
		Data *[]struct{ ID, Text, Status string } `json:"data"`
	}{Data: &imported}); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(imported) != 1 || imported[0].Status != "inferred" || strings.Contains(imported[0].Text, "key") {
		t.Fatalf("secret or disallowed overlay survived: %#v", imported)
	}
	after, err := c.Document(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	afterBytes, _ := flowir.CanonicalJSON(after)
	if string(beforeBytes) != string(afterBytes) {
		t.Fatal("overlay must not modify deterministic FlowIR")
	}
	page, err := http.Get(c.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	html, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if !strings.Contains(string(html), `aria-label="승인 전 추론한 의미"`) || !strings.Contains(string(html), `data-overlay-status="inferred"`) {
		t.Fatalf("FlowView does not separate inferred overlay: %s", html)
	}
	approve, _ := http.NewRequest(http.MethodPost, c.URL+"/api/v1/overlay/approve", strings.NewReader(`{"id":"`+imported[0].ID+`"}`))
	approve.Header.Set("X-CodeFlow-Token", c.Token)
	response, err = http.DefaultClient.Do(approve)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("approval=%v status=%v", err, response)
	}
	response.Body.Close()
	knowledge, err := os.ReadFile(filepath.Join(repo, ".codeflow", "knowledge", "confirmed.json"))
	if err != nil || !strings.Contains(string(knowledge), "submits the order") || strings.Contains(string(knowledge), "must-not-store") {
		t.Fatalf("knowledge=%s err=%v", knowledge, err)
	}
	clear, _ := http.NewRequest(http.MethodDelete, c.URL+"/api/v1/overlay", nil)
	clear.Header.Set("X-CodeFlow-Token", c.Token)
	response, err = http.DefaultClient.Do(clear)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("clear=%v status=%v", err, response)
	}
	response.Body.Close()
	final, _ := c.Document(context.Background())
	finalBytes, _ := flowir.CanonicalJSON(final)
	if string(beforeBytes) != string(finalBytes) {
		t.Fatal("approval must not modify deterministic FlowIR")
	}
}

func TestDomainLabelAndStaticExportKeepFlowIRImmutable(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), []byte("void signup() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	c, err := StartFixture(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	before, err := c.Document(context.Background())
	if err != nil || len(before.Scenarios) != 1 || len(before.Scenarios[0].StepIDs) == 0 {
		t.Fatalf("fixture needs an action-rooted scenario: %#v %v", before.Scenarios, err)
	}
	beforeBytes, _ := flowir.CanonicalJSON(before)
	label := fmt.Sprintf(`{"flow_id":%q,"scenario_id":%q,"step_id":%q,"title":"가입 정보를 확인합니다"}`, before.Current.ID, before.Scenarios[0].ID, before.Scenarios[0].StepIDs[0])
	request, _ := http.NewRequest(http.MethodPut, c.URL+"/api/v1/domain-labels", strings.NewReader(label))
	request.Header.Set("X-CodeFlow-Token", c.Token)
	response, err := http.DefaultClient.Do(request)
	if err != nil || response.StatusCode != http.StatusOK {
		if response != nil {
			response.Body.Close()
		}
		t.Fatalf("domain label approval=%v", err)
	}
	response.Body.Close()
	after, err := c.Document(context.Background())
	afterBytes, _ := flowir.CanonicalJSON(after)
	if err != nil || string(beforeBytes) != string(afterBytes) {
		t.Fatal("approved domain wording must not alter deterministic FlowIR")
	}
	view, err := http.Get(c.URL + "/?scenario=" + before.Scenarios[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(view.Body)
	view.Body.Close()
	if !strings.Contains(string(body), "가입 정보를 확인합니다") || !strings.Contains(string(body), `data-region="domain-scenarios"`) {
		t.Fatalf("FlowView did not render the selected domain scenario: %s", body)
	}
	exported, err := c.ExportHTML(context.Background(), before.Current.ID, before.Scenarios[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"fetch('/_codeflow/publication'", "vscode://"} {
		if strings.Contains(string(exported), forbidden) {
			t.Fatalf("static report retained local-only content %q", forbidden)
		}
	}
	if !strings.Contains(string(exported), "가입 정보를 확인합니다") || !strings.Contains(string(exported), `data-region="domain-scenarios"`) {
		t.Fatalf("static report did not preserve reviewed domain flow: %s", exported)
	}
	if _, err := c.ExportHTML(context.Background(), before.Current.ID, "missing-scenario"); err == nil {
		t.Fatal("export accepted a scenario that is not in the current observed flow")
	}
	exportRequest, _ := http.NewRequest(http.MethodGet, c.URL+"/api/v1/export?flow_id=route:%2Fsignup&scenario="+before.Scenarios[0].ID, nil)
	exportRequest.Header.Set("X-CodeFlow-Token", c.Token)
	exportResponse, err := http.DefaultClient.Do(exportRequest)
	if err != nil {
		t.Fatal(err)
	}
	exportBody, _ := io.ReadAll(exportResponse.Body)
	exportResponse.Body.Close()
	if exportResponse.StatusCode != http.StatusOK || !strings.Contains(exportResponse.Header.Get("Content-Disposition"), "attachment") || strings.Contains(string(exportBody), "vscode://") {
		t.Fatalf("export endpoint did not serve a safe attachment: status=%d headers=%v", exportResponse.StatusCode, exportResponse.Header)
	}
}

func TestBusinessJourneyRequiresCurrentScenarioAndRendersBeforeScreenNavigation(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), []byte("void signup() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	c, err := StartFixture(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	document, err := c.Document(context.Background())
	if err != nil || len(document.Scenarios) != 1 {
		t.Fatalf("fixture scenario unavailable: %#v %v", document.Scenarios, err)
	}
	journey := fmt.Sprintf(`{"id":"complete-signup","title":"가입을 완료합니다","outcome":"가입 완료 상태가 준비됩니다","segments":[{"flow_id":%q,"scenario_id":%q}]}`, document.Current.ID, document.Scenarios[0].ID)
	request, _ := http.NewRequest(http.MethodPut, c.URL+"/api/v1/business-journeys", strings.NewReader(journey))
	request.Header.Set("X-CodeFlow-Token", c.Token)
	response, err := http.DefaultClient.Do(request)
	if err != nil || response.StatusCode != http.StatusOK {
		if response != nil {
			response.Body.Close()
		}
		t.Fatalf("business journey approval=%v", err)
	}
	response.Body.Close()
	workspaceRequest, _ := http.NewRequest(http.MethodGet, c.URL+"/api/v2/workspace", nil)
	workspaceRequest.Header.Set("X-CodeFlow-Token", c.Token)
	workspaceResponse, err := http.DefaultClient.Do(workspaceRequest)
	if err != nil {
		t.Fatal(err)
	}
	workspaceBody, _ := io.ReadAll(workspaceResponse.Body)
	workspaceResponse.Body.Close()
	if workspaceResponse.StatusCode != http.StatusOK || !strings.Contains(string(workspaceBody), `"business_journeys"`) || !strings.Contains(string(workspaceBody), "complete-signup") {
		t.Fatalf("workspace API did not expose approved business journey: status=%d body=%s", workspaceResponse.StatusCode, workspaceBody)
	}
	view, err := http.Get(c.URL + "/?journey=complete-signup")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(view.Body)
	view.Body.Close()
	text := string(body)
	journeyAt, screenAt := strings.Index(text, `data-region="business-journeys"`), strings.Index(text, `data-region="domain-scenarios"`)
	if view.StatusCode != http.StatusOK || journeyAt < 0 || screenAt < 0 || journeyAt >= screenAt || !strings.Contains(text, "가입을 완료합니다") {
		t.Fatalf("FlowView did not put current business journey before screen details: status=%d html=%s", view.StatusCode, body)
	}
	invalid := `{"id":"stale-flow","title":"잘못된 경로","segments":[{"flow_id":"route:/missing","scenario_id":"missing"}]}`
	request, _ = http.NewRequest(http.MethodPut, c.URL+"/api/v1/business-journeys", strings.NewReader(invalid))
	request.Header.Set("X-CodeFlow-Token", c.Token)
	response, err = http.DefaultClient.Do(request)
	if err != nil || response.StatusCode != http.StatusBadRequest {
		if response != nil {
			response.Body.Close()
		}
		t.Fatalf("invalid journey target was accepted: %v", err)
	}
	response.Body.Close()
}

func TestBusinessJourneyRejectsIndependentScenariosFromTheSameFlow(t *testing.T) {
	flow := flowir.Document{
		Current: flowir.Flow{ID: "route:/signup"},
		Scenarios: []flowir.Scenario{
			{ID: "email", Status: flowir.Observed},
			{ID: "phone", Status: flowir.Observed},
		},
	}
	workspace := workspaceDocument{Flows: []flowir.Document{flow}}
	journey := ontology.BusinessJourney{
		ID:       "complete-signup",
		Title:    "가입을 완료합니다",
		Segments: []ontology.JourneySegment{{FlowID: flow.Current.ID, ScenarioID: "email"}, {FlowID: flow.Current.ID, ScenarioID: "phone"}},
	}
	if businessJourneyTargetExists(workspace, journey) {
		t.Fatal("independent scenarios in the same flow were accepted as a sequential business journey")
	}
	navigation := buildJourneyNavigation(workspace, nil, []ontology.BusinessJourney{journey}, journey.ID)
	if navigation.Selected == nil || !navigation.Selected.Stale || navigation.Selected.Status != flowir.Unknown {
		t.Fatalf("same-flow journey was presented as a supported path: %#v", navigation)
	}
}

func TestJourneyNavigationPreservesOnlyVerifiedJourneySegments(t *testing.T) {
	flow := flowir.Document{
		Current: flowir.Flow{ID: "route:/signup"},
		Scenarios: []flowir.Scenario{
			{ID: "email", Status: flowir.Observed},
			{ID: "phone", Status: flowir.Observed},
		},
	}
	workspace := workspaceDocument{Flows: []flowir.Document{flow, flowir.Document{Current: flowir.Flow{ID: "route:/home"}}}}
	journey := &journeyNavigationItem{
		ID: "complete-signup",
		Segments: []journeySegmentNavigation{{
			FlowID:     flow.Current.ID,
			ScenarioID: "email",
			URL:        "/?journey=complete-signup&flow=route%3A%2Fsignup&scenario=email",
		}},
	}
	mapNavigation := buildWorkspaceNavigation(workspace, flow.Current.ID, journey)
	if mapNavigation.Flows[0].URL != journey.Segments[0].URL || mapNavigation.Flows[1].URL != "/?flow=route%3A%2Fhome" {
		t.Fatalf("workspace map did not preserve journey only for its verified segment: %#v", mapNavigation.Flows)
	}
	scenarios := buildScenarioNavigation(flow, "email", nil, journey)
	if scenarios.Flows[0].URL != journey.Segments[0].URL || scenarios.Flows[1].URL != "/?flow=route%3A%2Fsignup&scenario=phone" {
		t.Fatalf("scenario navigation did not preserve journey only for its verified segment: %#v", scenarios.Flows)
	}
}

func TestFixtureCorePublishesAuthenticatedAPIAndSemanticFlowView(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	source := []byte("void signup() {\n  go('/welcome');\n}\n")
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), source, 0644); err != nil {
		t.Fatal(err)
	}
	c, err := StartFixture(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	if !strings.HasPrefix(c.URL, "http://127.0.0.1:") {
		t.Fatalf("must bind literal loopback: %s", c.URL)
	}
	endpoint := c.URL + "/api/v1/flows/ignored?id=route:%2Fsignup"
	response, err := http.Get(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing token status=%d", response.StatusCode)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if strings.Contains(string(body), c.Token) {
		t.Fatal("token leaked in unauthorized response")
	}
	request, _ := http.NewRequest(http.MethodGet, endpoint, nil)
	request.Header.Set("X-CodeFlow-Token", c.Token)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatalf("api status %d", response.StatusCode)
	}
	var api struct {
		Basis struct {
			Repository          string `json:"repository"`
			WorktreeFingerprint string `json:"worktree_fingerprint"`
		} `json:"basis"`
		Status string `json:"status"`
		Data   struct {
			Facts []struct {
				Evidence []struct {
					FileHash  string `json:"file_hash"`
					ByteRange []int  `json:"byte_range"`
				} `json:"evidence"`
			} `json:"facts"`
		} `json:"data"`
		Unknowns []any  `json:"unknowns"`
		ViewURL  string `json:"view_url"`
	}
	if err := json.NewDecoder(response.Body).Decode(&api); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if api.Status != "ready" || api.Basis.Repository != repo || api.Basis.WorktreeFingerprint == "" || api.ViewURL != c.URL+"/" || api.Unknowns == nil {
		t.Fatalf("missing/inconsistent CodeFlowResponse envelope: %#v", api)
	}
	if len(api.Data.Facts) == 0 || api.Data.Facts[0].Evidence[0].FileHash != flowir.SHA256Bytes(source) || api.Data.Facts[0].Evidence[0].ByteRange[1] != len(source) {
		t.Fatal("API did not return stored snapshot evidence")
	}
	response, err = http.Get(c.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := html.Parse(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !hasSemanticTimeline(tree) || !hasText(tree, "lib/signup.dart:1-4") || !hasArchitectureAndEditorNavigation(tree) {
		t.Fatal("FlowView did not render linked timeline, architecture flow, current anchor, and editor navigation")
	}
}

func TestResolverAPIAndFlowViewExposeSameTypedResolution(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	source := []byte("final route = GoRoute(path: '/signup', builder: (c, s) => const SizedBox());\n")
	if err := os.WriteFile(filepath.Join(repo, "lib", "routes.dart"), source, 0644); err != nil {
		t.Fatal(err)
	}
	// The existing G02 fixture remains intentionally anchored at this path;
	// resolver discovery independently proves the real route declaration above.
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), []byte("void signup() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, here, _, _ := runtime.Caller(0)
	adapter := "dart " + filepath.Join(filepath.Dir(here), "../../../adapters/dart/bin/codeflow-dart-adapter.dart")
	c, err := StartFixtureWithAdapter(context.Background(), repo, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	request, _ := http.NewRequest(http.MethodGet, c.URL+"/api/v1/entry-points/resolve?selector=signup", nil)
	request.Header.Set("X-CodeFlow-Token", c.Token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body struct {
		Status string `json:"status"`
		Data   struct {
			EntryPoint struct {
				FlowID string        `json:"flow_id"`
				Anchor flowir.Anchor `json:"anchor"`
			} `json:"entry_point"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || body.Status != "ready" || body.Data.EntryPoint.FlowID != "route:/signup" || body.Data.EntryPoint.Anchor.FileHash != flowir.SHA256Bytes(source) {
		t.Fatalf("%d %#v", response.StatusCode, body)
	}
	view, err := http.Get(c.URL + "/?selector=signup")
	if err != nil {
		t.Fatal(err)
	}
	html, _ := io.ReadAll(view.Body)
	view.Body.Close()
	if !strings.Contains(string(html), "route:/signup — lib/routes.dart:1") || !strings.Contains(string(html), `data-resolution-state="ready"`) {
		t.Fatalf("view %s", html)
	}
	request, _ = http.NewRequest(http.MethodGet, c.URL+"/api/v1/entry-points/resolve?selector=missing", nil)
	request.Header.Set("X-CodeFlow-Token", c.Token)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("missing should be an honest unknown, got %d", response.StatusCode)
	}
}

func TestReconcileKeepsLastConsistentSnapshotWhenObservationCannotStabilize(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), []byte("void signup() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	c, err := StartFixture(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	before, _, _, err := c.store.Get(context.Background(), "route:/signup")
	if err != nil {
		t.Fatal(err)
	}
	c.capture = func(string) (flowir.Basis, error) { return flowir.Basis{}, manifest.ErrChanging }
	if err := c.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, _, status, err := c.store.Get(context.Background(), "route:/signup")
	if err != nil {
		t.Fatal(err)
	}
	if status != "analyzing" || after.Basis.WorktreeFingerprint != before.Basis.WorktreeFingerprint {
		t.Fatalf("status=%s before=%s after=%s", status, before.Basis.WorktreeFingerprint, after.Basis.WorktreeFingerprint)
	}
	request, _ := http.NewRequest(http.MethodGet, c.URL+"/api/v1/flows/ignored?id=route:%2Fsignup", nil)
	request.Header.Set("X-CodeFlow-Token", c.Token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var api struct {
		Basis  flowir.Basis    `json:"basis"`
		Status string          `json:"status"`
		Data   flowir.Document `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&api); err != nil {
		t.Fatal(err)
	}
	if api.Status != "analyzing" || api.Basis.WorktreeFingerprint != api.Data.Basis.WorktreeFingerprint {
		t.Fatal("API basis and stored FlowIR basis diverged")
	}
	pageResponse, err := http.Get(c.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	pageBody, _ := io.ReadAll(pageResponse.Body)
	pageResponse.Body.Close()
	if !strings.Contains(string(pageBody), "Analysis status</dt><dd>analyzing") || !strings.Contains(string(pageBody), before.Basis.WorktreeFingerprint) {
		t.Fatal("FlowView did not render the same stored basis and runtime status")
	}
}

func TestDeletingCodeFlowCacheReconstructsSameBasis(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), []byte("void signup() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	first, err := StartFixture(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	firstDoc, _, _, err := first.store.Get(context.Background(), "route:/signup")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(repo, ".codeflow")); err != nil {
		t.Fatal(err)
	}
	second, err := StartFixture(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close(context.Background())
	secondDoc, _, _, err := second.store.Get(context.Background(), "route:/signup")
	if err != nil {
		t.Fatal(err)
	}
	if firstDoc.Basis.WorktreeFingerprint != secondDoc.Basis.WorktreeFingerprint || len(firstDoc.Basis.Manifest) != len(secondDoc.Basis.Manifest) {
		t.Fatal("cache deletion changed reconstructed deterministic FlowBasis")
	}
}

func TestResolvedDebtLeavesCurrentFlowWhenUnknownDisappears(t *testing.T) {
	review := store.DebtReview{DebtID: "old-boundary", State: "resolved", Question: "internal analyzer question"}
	if got := resolvedDebt([]store.DebtReview{review}, flowir.Document{}); len(got) != 0 {
		t.Fatalf("obsolete resolved history must not add cognitive debt to current FlowView: %#v", got)
	}
	document := flowir.Document{Unknowns: []flowir.UnknownDetail{{ID: "old-boundary"}}}
	if got := resolvedDebt([]store.DebtReview{review}, document); len(got) != 1 {
		t.Fatalf("a review for a still-current boundary remains available: %#v", got)
	}
}

func TestActionableDebtUsesReaderLanguageInsteadOfAnalyzerJargon(t *testing.T) {
	document := flowir.Document{Current: flowir.Flow{ID: "route:/home"}, Unknowns: []flowir.UnknownDetail{{ID: "u", Reason: "supported_user_action_missing"}}}
	items := actionableDebt(document)
	if len(items) != 1 {
		t.Fatalf("debt items=%#v", items)
	}
	text := items[0].Cause + " " + items[0].Confirmed + " " + items[0].Missing + " " + items[0].NextAction
	for _, jargon := range []string{"resolved callback", "route 선언", "정적 코드 흐름"} {
		if strings.Contains(text, jargon) {
			t.Fatalf("FlowView debt guidance leaked analyzer jargon %q: %s", jargon, text)
		}
	}
	for _, readerTerm := range []string{"버튼·탭", "화면 경로", "실행하는 메서드"} {
		if !strings.Contains(text, readerTerm) {
			t.Fatalf("FlowView debt guidance is missing reader term %q: %s", readerTerm, text)
		}
	}
}

func hasSemanticTimeline(node *html.Node) bool {
	if node.Type == html.ElementNode && node.Data == "ol" {
		for _, a := range node.Attr {
			if a.Key == "aria-label" && a.Val == "코드 흐름 타임라인" {
				return true
			}
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if hasSemanticTimeline(child) {
			return true
		}
	}
	return false
}

func hasArchitectureAndEditorNavigation(node *html.Node) bool {
	architecture, editor := false, false
	var visit func(*html.Node)
	visit = func(current *html.Node) {
		if current.Type == html.ElementNode {
			attributes := map[string]string{}
			for _, attr := range current.Attr {
				attributes[attr.Key] = attr.Val
			}
			if current.Data == "button" && attributes["data-architecture-step"] != "" {
				architecture = true
			}
			if current.Data == "a" && strings.HasPrefix(attributes["href"], "vscode://file/") && strings.Contains(attributes["aria-label"], "VS Code") {
				editor = true
			}
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(node)
	return architecture && editor
}
func hasText(node *html.Node, wanted string) bool {
	if node.Type == html.TextNode && strings.Contains(node.Data, wanted) {
		return true
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if hasText(child, wanted) {
			return true
		}
	}
	return false
}
