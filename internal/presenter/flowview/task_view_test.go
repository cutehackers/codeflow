package flowview

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"codeflow/internal/analyzer/protocol"
	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/slicing"
	"codeflow/internal/collector/storage"
	"codeflow/internal/curator/semantic"
)

func TestFlowViewTaskViewEndpoint(t *testing.T) {
	root := copyFixtureWithoutCodeflow(t, "nextjs-app-fixture")
	if _, err := os.Stat(filepath.Join(root, ".codeflow")); !os.IsNotExist(err) {
		t.Fatalf("fixture copy is not pristine: .codeflow stat error=%v", err)
	}

	moduleRoot, _ := filepath.Abs("../../..")
	t.Setenv("CODEFLOW_ADAPTER_TYPESCRIPT_BIN", "noderun:"+filepath.Join(moduleRoot, "adapters", "typescript"))

	srv, err := NewServer(Config{
		RepoRoot: root,
		Port:     0,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// 1. Missing precondition
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/view?token="+srv.AuthToken(), nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing precondition, got %d", rec.Code)
	}
	var errDoc map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &errDoc)
	if errDoc["code"] != "missing_precondition" {
		t.Errorf("expected code missing_precondition, got %v", errDoc["code"])
	}

	// 2. Ambiguous target
	reqAmb := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/view?token="+srv.AuthToken()+"&query=checkout", nil)
	recAmb := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recAmb, reqAmb)

	if recAmb.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for ambiguous target, got %d", recAmb.Code)
	}
	var ambDoc map[string]any
	_ = json.Unmarshal(recAmb.Body.Bytes(), &ambDoc)
	if ambDoc["code"] != "ambiguous_target" {
		t.Errorf("expected code ambiguous_target, got %v", ambDoc["code"])
	}
	candidates, _ := ambDoc["candidateTargets"].([]any)
	if len(candidates) < 2 {
		t.Errorf("expected at least 2 candidates in ambiguous response, got %v", candidates)
	}

	// 3. Unambiguous query
	reqValid := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/view?token="+srv.AuthToken()+"&query=빠른+결제&entrySymbol=app/page.tsx%23HomePage.onQuickCheckout", nil)
	recValid := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recValid, reqValid)

	if recValid.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid query, got %d: %s", recValid.Code, recValid.Body.String())
	}
	var resDoc map[string]any
	if err := json.Unmarshal(recValid.Body.Bytes(), &resDoc); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if _, ok := resDoc["candidateAnswer"]; !ok {
		t.Error("missing candidateAnswer in response")
	}
	if _, ok := resDoc["semanticMap"]; !ok {
		t.Error("missing semanticMap in response")
	}
	if _, ok := resDoc["projection"]; !ok {
		t.Error("missing projection in response")
	}
	resolution, ok := resDoc["flowResolution"].(map[string]any)
	if !ok {
		t.Fatal("missing flowResolution for a natural-language request")
	}
	if resolution["rawRequest"] != "빠른 결제" || resolution["status"] != "resolved" {
		t.Fatalf("unexpected flow resolution: %v", resolution)
	}
	if resolution["entrySymbolPath"] != "app/page.tsx#HomePage.onQuickCheckout" {
		t.Fatalf("resolution entry differs from the selected candidate: %v", resolution)
	}
	evidence, ok := resolution["evidence"].([]any)
	if !ok || len(evidence) != 1 {
		t.Fatalf("expected one verified selection evidence record, got %v", resolution["evidence"])
	}
	selectionEvidence := evidence[0].(map[string]any)
	selectionAnchor, ok := selectionEvidence["sourceAnchor"].(map[string]any)
	if !ok {
		t.Fatal("resolved request lacks the selected entry source anchor")
	}
	semanticMap := resDoc["semanticMap"].(map[string]any)
	entryStep := semanticMap["steps"].([]any)[0].(map[string]any)
	if !reflect.DeepEqual(selectionAnchor, entryStep["anchor"]) {
		t.Fatalf("selection source differs from the first verified entry step: %v", selectionAnchor)
	}
	sbDoc, ok := resDoc["flowSequence"].(map[string]any)
	if !ok || sbDoc == nil {
		t.Fatal("missing or invalid flowSequence in response")
	}
	if sbDoc["schemaId"] != semantic.FlowSequenceSchemaID {
		t.Errorf("expected flowSequence schemaId %s, got %v", semantic.FlowSequenceSchemaID, sbDoc["schemaId"])
	}
	frames, ok := sbDoc["frames"].([]any)
	if !ok || len(frames) == 0 {
		t.Errorf("expected at least 1 frame in flowSequence, got %v", sbDoc["frames"])
	}
	for _, fRaw := range frames {
		if fMap, ok := fRaw.(map[string]any); ok {
			if arch, hasArch := fMap["architecture"]; hasArch && arch != "" {
				t.Errorf("flowSequence frame must not contain deprecated architecture field: %v", fMap)
			}
		}
	}
	baseline, _ := resDoc["baseline"].(map[string]any)
	if baseline == nil || baseline["status"] != "none" {
		t.Errorf("expected explicit baseline none marker, got %v", resDoc["baseline"])
	}
}

func TestDirectEntryHasCurrentSourceSelectionRecord(t *testing.T) {
	root := copyFixtureWithoutCodeflow(t, "nextjs-app-fixture")
	moduleRoot, _ := filepath.Abs("../../..")
	t.Setenv("CODEFLOW_ADAPTER_TYPESCRIPT_BIN", "noderun:"+filepath.Join(moduleRoot, "adapters", "typescript"))
	srv, err := NewServer(Config{RepoRoot: root, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown(context.Background())
	entry := "services/orderService.ts#processOrder"
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/view?entrySymbol=services/orderService.ts%23processOrder&token="+srv.AuthToken(), nil)
	response := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("direct entry analysis failed: status %d", response.Code)
	}
	var view struct {
		FlowResolutionMissing bool `json:"flowResolutionMissing"`
		FlowResolution        struct {
			Status          string                `json:"status"`
			RawRequest      string                `json:"rawRequest"`
			EntrySymbolPath string                `json:"entrySymbolPath"`
			Evidence        []fusion.FlowEvidence `json:"evidence"`
		} `json:"flowResolution"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.FlowResolutionMissing || view.FlowResolution.Status != "resolved" ||
		view.FlowResolution.RawRequest != entry || view.FlowResolution.EntrySymbolPath != entry ||
		len(view.FlowResolution.Evidence) != 1 || view.FlowResolution.Evidence[0].SourceAnchor == nil {
		t.Fatalf("new direct analysis lost its verified selection: %+v", view)
	}
}

func TestGoDirectMethodWithoutHarvestMarkerHasSelectionRecord(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/checkout\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := "package checkout\n\ntype Service struct{}\n\nfunc (s *Service) Checkout(amount int) int {\n\tfee := calculateFee(amount)\n\treturn fee\n}\n\nfunc (s *Service) ProcessCheckout(amount int) int { return amount + 1 }\n\nfunc calculateFee(amount int) int { return amount * 2 }\n"
	if err := os.WriteFile(filepath.Join(root, "checkout.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"mixed-checkout","version":"1.0.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "page.tsx"), []byte("export function Page() { return <main>Checkout</main> }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	adapter, err := filepath.Abs("../../../adapters/go")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEFLOW_ADAPTER_GO_BIN", "gorun:"+adapter)
	srv, err := NewServer(Config{RepoRoot: root, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown(context.Background())
	snapshot, _, releaseSnapshot, err := srv.captureAnalysisSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshot.Files["checkout.go"]; !ok {
		t.Fatalf("mixed-language snapshot omitted Go source: %v", snapshot.Files)
	}
	releaseSnapshot()
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/view?entrySymbol=checkout.go%23Service.Checkout&token="+srv.AuthToken(), nil)
	response := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("Go direct method analysis failed: status %d: %s", response.Code, response.Body.String())
	}
	var view struct {
		FlowResolution fusion.FlowResolution            `json:"flowResolution"`
		SemanticMap    semantic.SemanticMapIR           `json:"semanticMap"`
		FlowContexts   map[string]FlowContextProjection `json:"flowContexts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.FlowResolution.RawRequest != "checkout.go#Service.Checkout" ||
		view.FlowResolution.EntrySymbolPath != "checkout.go#Service.Checkout" ||
		len(view.FlowResolution.Evidence) != 1 || view.FlowResolution.Evidence[0].SourceAnchor == nil {
		t.Fatalf("Go direct method lacks same-snapshot selection evidence: %+v", view.FlowResolution)
	}
	var calleeSourceVisible bool
	for _, step := range view.SemanticMap.Steps {
		if step.Anchor.EnclosingSymbolPath != "calculateFee" {
			continue
		}
		context := view.FlowContexts[step.StepID]
		if context.SourceValidationStatus != "verified" || context.Precision != PrecisionExact {
			t.Fatalf("Go callee source is not exact: %+v", context)
		}
		for _, line := range context.DisplayedLines {
			if line.IsHit && line.Selection != nil && strings.Contains(line.Selection.Text, "return amount * 2") {
				calleeSourceVisible = true
			}
		}
	}
	if !calleeSourceVisible {
		t.Fatalf("Go call result did not open the callee source: %+v", view.SemanticMap.Steps)
	}
	focusReason := "Checkout fee is calculated by this direct call"
	focusURL := "http://127.0.0.1/api/task/view?entrySymbol=checkout.go%23Service.Checkout&focusSymbol=checkout.go%23calculateFee&focusReason=" + url.QueryEscape(focusReason) + "&token=" + srv.AuthToken()
	focusResponse := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(focusResponse, httptest.NewRequest(http.MethodGet, focusURL, nil))
	if focusResponse.Code != http.StatusOK {
		t.Fatalf("verified focus analysis failed: status %d: %s", focusResponse.Code, focusResponse.Body.String())
	}
	var focused struct {
		ViewID         string                      `json:"viewId"`
		RequestedFocus semantic.RequestedFlowFocus `json:"requestedFocus"`
		Request        semantic.FeatureQueryParams `json:"request"`
		SemanticMap    semantic.SemanticMapIR      `json:"semanticMap"`
	}
	if err := json.Unmarshal(focusResponse.Body.Bytes(), &focused); err != nil {
		t.Fatal(err)
	}
	if focused.ViewID == "" || focused.RequestedFocus.SymbolPath != "checkout.go#calculateFee" ||
		focused.RequestedFocus.CallStepID == "" || focused.RequestedFocus.EntryStepID == "" ||
		focused.RequestedFocus.Reason != focusReason || focused.Request.FocusSymbol != focused.RequestedFocus.SymbolPath {
		t.Fatalf("verified focus was not saved with the request: %+v", focused)
	}
	restored, err := srv.RestoreTaskView(context.Background(), focused.ViewID)
	if err != nil {
		t.Fatal(err)
	}
	if restoredFocus, ok := restored["requestedFocus"].(map[string]any); !ok || restoredFocus["entryStepId"] != focused.RequestedFocus.EntryStepID {
		t.Fatalf("restored view lost the verified focus: %v", restored["requestedFocus"])
	}
	var tampered map[string]any
	if err := json.Unmarshal(focusResponse.Body.Bytes(), &tampered); err != nil {
		t.Fatal(err)
	}
	tampered["requestedFocus"].(map[string]any)["entryStepId"] = "unrelated-step"
	if _, err := srv.SaveTaskView(context.Background(), tampered); err == nil {
		t.Fatal("persistence accepted a requested focus pointing to unrelated code")
	}
	pathTo := func(from, to string) []int {
		type visit struct {
			stepID string
			path   []int
		}
		queue := []visit{{stepID: from}}
		seen := map[string]bool{from: true}
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			if current.stepID == to {
				return current.path
			}
			for index, edge := range focused.SemanticMap.Edges {
				if edge.FromStepID != current.stepID || edge.ResolutionStatus != "resolved" || edge.Kind == "callback_argument" || seen[edge.ToStepID] {
					continue
				}
				seen[edge.ToStepID] = true
				queue = append(queue, visit{stepID: edge.ToStepID, path: append(append([]int(nil), current.path...), index)})
			}
		}
		return nil
	}
	rootStepID := focused.SemanticMap.Steps[0].StepID
	selections := []semantic.CodeReadingSelection{{StepID: rootStepID, Role: "entry", RequestPart: "Checkout", InclusionReason: "This starts the requested checkout"}}
	relations := []semantic.CodeReadingRelationSelection{}
	if rootStepID != focused.RequestedFocus.CallStepID {
		selections = append(selections, semantic.CodeReadingSelection{StepID: focused.RequestedFocus.CallStepID, Role: "handoff", RequestPart: "Checkout", InclusionReason: "This calls the fee calculation"})
		relations = append(relations, semantic.CodeReadingRelationSelection{FromStepID: rootStepID, ToStepID: focused.RequestedFocus.CallStepID, EdgeIndexes: pathTo(rootStepID, focused.RequestedFocus.CallStepID)})
	}
	selections = append(selections, semantic.CodeReadingSelection{StepID: focused.RequestedFocus.EntryStepID, Role: "outcome", RequestPart: "Checkout", InclusionReason: "This produces the fee"})
	relations = append(relations, semantic.CodeReadingRelationSelection{FromStepID: focused.RequestedFocus.CallStepID, ToStepID: focused.RequestedFocus.EntryStepID, EdgeIndexes: pathTo(focused.RequestedFocus.CallStepID, focused.RequestedFocus.EntryStepID)})
	readingRequest, err := json.Marshal(map[string]any{"viewId": focused.ViewID, "selections": selections, "relations": relations})
	if err != nil {
		t.Fatal(err)
	}
	readingResponse := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(readingResponse, httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/view/reading-path?token="+srv.AuthToken(), strings.NewReader(string(readingRequest))))
	if readingResponse.Code != http.StatusOK {
		t.Fatalf("source-backed code reading path failed: %d %s", readingResponse.Code, readingResponse.Body.String())
	}
	var readingView struct {
		ViewID          string                   `json:"viewId"`
		CodeReadingPath semantic.CodeReadingPath `json:"codeReadingPath"`
	}
	if err := json.Unmarshal(readingResponse.Body.Bytes(), &readingView); err != nil {
		t.Fatal(err)
	}
	if readingView.ViewID == focused.ViewID || len(readingView.CodeReadingPath.Nodes) != len(selections) || !readingView.CodeReadingPath.Nodes[len(selections)-1].CodeViewAvailable {
		t.Fatalf("reading path was not saved as a new exact-code view: %+v", readingView)
	}
	reopenedReading, err := srv.RestoreTaskView(context.Background(), readingView.ViewID)
	if err != nil || reopenedReading["codeReadingPath"] == nil || reopenedReading["codeReadingPathLimitation"] != nil {
		t.Fatalf("valid reading path did not survive restoration: %v %+v", err, reopenedReading)
	}
	contexts := reopenedReading["flowContexts"].(map[string]any)
	entryContext := contexts[readingView.CodeReadingPath.Nodes[0].StepID].(map[string]any)
	for _, rawLine := range entryContext["displayedLines"].([]any) {
		line := rawLine.(map[string]any)
		if line["isHit"] == true {
			delete(line, "selection")
			break
		}
	}
	FormatTaskViewResponse(reopenedReading, root)
	if reopenedReading["codeReadingPath"] != nil || reopenedReading["codeReadingPathLimitation"] == nil {
		t.Fatal("restored reading path retained a CodeView entry with no exact highlight")
	}
	invalidFocusURL := "http://127.0.0.1/api/task/view?entrySymbol=checkout.go%23Service.Checkout&focusSymbol=checkout.go%23Service.ProcessCheckout&focusReason=wrong+target&token=" + srv.AuthToken()
	invalidFocusResponse := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(invalidFocusResponse, httptest.NewRequest(http.MethodGet, invalidFocusURL, nil))
	if invalidFocusResponse.Code != http.StatusBadRequest {
		t.Fatalf("unrelated focus was accepted: %d %s", invalidFocusResponse.Code, invalidFocusResponse.Body.String())
	}
	naturalRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/view?query=ProcessCheckout&token="+srv.AuthToken(), nil)
	naturalResponse := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(naturalResponse, naturalRequest)
	if naturalResponse.Code != http.StatusBadRequest {
		t.Fatalf("Go natural request should request candidate selection: %d %s", naturalResponse.Code, naturalResponse.Body.String())
	}
	var choices struct {
		Code             string   `json:"code"`
		CandidateTargets []string `json:"candidateTargets"`
	}
	if err := json.Unmarshal(naturalResponse.Body.Bytes(), &choices); err != nil {
		t.Fatal(err)
	}
	if choices.Code != "unresolved_target" || !slices.Contains(choices.CandidateTargets, "checkout.go#Service.ProcessCheckout") {
		t.Fatalf("mixed-language discovery omitted the Go candidate: %+v", choices)
	}
	previewRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/candidate/preview?entrySymbol=checkout.go%23Service.ProcessCheckout&token="+srv.AuthToken(), nil)
	previewResponse := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(previewResponse, previewRequest)
	if previewResponse.Code != http.StatusOK {
		t.Fatalf("Go candidate preview in mixed repository failed: %d %s", previewResponse.Code, previewResponse.Body.String())
	}
	selectedRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/view?query=ProcessCheckout&entrySymbol=checkout.go%23Service.ProcessCheckout&token="+srv.AuthToken(), nil)
	selectedResponse := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(selectedResponse, selectedRequest)
	if selectedResponse.Code != http.StatusOK {
		t.Fatalf("Go selected request in mixed repository failed: %d %s", selectedResponse.Code, selectedResponse.Body.String())
	}
	var naturalView struct {
		FlowResolution fusion.FlowResolution `json:"flowResolution"`
	}
	if err := json.Unmarshal(selectedResponse.Body.Bytes(), &naturalView); err != nil {
		t.Fatal(err)
	}
	if naturalView.FlowResolution.RawRequest != "ProcessCheckout" ||
		naturalView.FlowResolution.EntrySymbolPath != "checkout.go#Service.ProcessCheckout" ||
		len(naturalView.FlowResolution.Evidence) != 1 || naturalView.FlowResolution.Evidence[0].SourceAnchor == nil {
		t.Fatalf("Go natural request selected the wrong source: %+v", naturalView.FlowResolution)
	}
}

func TestCandidatePreviewBindsSourceSelectionToSnapshot(t *testing.T) {
	root := copyFixtureWithoutCodeflow(t, "nextjs-app-fixture")
	moduleRoot, _ := filepath.Abs("../../..")
	t.Setenv("CODEFLOW_ADAPTER_TYPESCRIPT_BIN", "noderun:"+filepath.Join(moduleRoot, "adapters", "typescript"))
	srv, err := NewServer(Config{RepoRoot: root, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown(context.Background())
	entry := "app/page.tsx%23HomePage.onQuickCheckout"
	previewRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/candidate/preview?entrySymbol="+entry+"&token="+srv.AuthToken(), nil)
	previewResponse := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(previewResponse, previewRequest)
	if previewResponse.Code != http.StatusOK {
		t.Fatalf("candidate preview failed: %d %s", previewResponse.Code, previewResponse.Body.String())
	}
	var preview struct {
		SnapshotID string `json:"snapshotId"`
		Entry      string `json:"entrySymbolPath"`
		Source     struct {
			Path string `json:"path"`
			Code string `json:"code"`
		} `json:"source"`
	}
	if err := json.Unmarshal(previewResponse.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.SnapshotID == "" || preview.Entry != "app/page.tsx#HomePage.onQuickCheckout" || preview.Source.Path != "app/page.tsx" || !strings.Contains(preview.Source.Code, "onQuickCheckout") {
		t.Fatalf("preview did not show the selected source: %+v", preview)
	}
	staleRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/view?query=quick+checkout&entrySymbol="+entry+"&expectedSnapshotId=stale&token="+srv.AuthToken(), nil)
	staleResponse := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(staleResponse, staleRequest)
	if staleResponse.Code != http.StatusConflict {
		t.Fatalf("stale preview was analyzed: %d %s", staleResponse.Code, staleResponse.Body.String())
	}
	views, err := srv.storage.ListViews(context.Background())
	if err != nil || len(views) != 0 {
		t.Fatalf("stale preview saved a view: %v, %v", views, err)
	}
}

func TestExplicitSourceEntryKeepsNaturalRequestWithoutHarvestCandidate(t *testing.T) {
	const entry = "internal/presenter/flowview/flow_context.go#DeriveFlowContext"
	target := &semantic.ResolvedTarget{EntrySymbolPath: entry, CandidateID: "cand-0123456789abcdef"}
	snapshot := protocol.Snapshot{SnapshotID: "snapshot-current", ComputedBasisID: "basis-current"}
	request := "How does CodeFlow derive source context for a flow step?"
	resolution, err := resolveFlowRecord(request, entry, target, nil, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.RawRequest != request || resolution.EntrySymbolPath != entry || resolution.Status != "resolved" ||
		len(resolution.Evidence) != 1 || resolution.Evidence[0].SnapshotID != snapshot.SnapshotID ||
		resolution.Evidence[0].Description != "분석 요청에 명시된 진입 심볼" {
		t.Fatalf("explicit source selection was not preserved: %+v", resolution)
	}
	if _, err := resolveFlowRecord(request, "other.go#Run", target, nil, snapshot); err == nil {
		t.Fatal("a different source selection was accepted")
	}
}

func TestFailedCodeValidationDoesNotPublishAnalysis(t *testing.T) {
	root := copyFixtureWithoutCodeflow(t, "nextjs-app-fixture")
	sourcePath := filepath.Join(root, "app", "page.tsx")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(source), "setStatus('processing');", "setStatus({ apiKey: 'sk-1234567890abcdef' });", 1)
	if changed == string(source) {
		t.Fatal("fixture entry statement was not found")
	}
	if err := os.WriteFile(sourcePath, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	moduleRoot, _ := filepath.Abs("../../..")
	t.Setenv("CODEFLOW_ADAPTER_TYPESCRIPT_BIN", "noderun:"+filepath.Join(moduleRoot, "adapters", "typescript"))
	srv, err := NewServer(Config{RepoRoot: root, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown(context.Background())

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/view?query=quick+checkout&entrySymbol=app/page.tsx%23HomePage.onQuickCheckout&token="+srv.AuthToken(), nil)
	response := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unreadable entry was accepted: status %d", response.Code)
	}
	var failure struct {
		Code       string `json:"code"`
		Target     string `json:"target"`
		Reason     string `json:"reason"`
		NextAction string `json:"nextAction"`
		Resolution struct {
			Status     string `json:"status"`
			RawRequest string `json:"rawRequest"`
		} `json:"flowResolution"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Code != "analysis_failed" || failure.Target != "app/page.tsx#HomePage.onQuickCheckout" ||
		failure.Reason == "" || failure.NextAction == "" || failure.Resolution.Status != "analysis_failed" || failure.Resolution.RawRequest != "quick checkout" {
		t.Fatalf("analysis failure lacks the affected target or recovery action: %+v", failure)
	}
	if strings.Contains(response.Body.String(), "sk-1234567890abcdef") {
		t.Fatal("analysis failure leaked masked source")
	}
	views, err := srv.storage.ListViews(context.Background())
	if err != nil || len(views) != 0 {
		t.Fatalf("failed analysis saved a View: %v, %v", views, err)
	}
	srv.mu.Lock()
	cacheCount := len(srv.mapCache)
	contextCount := len(srv.flowContextMetadata)
	srv.mu.Unlock()
	if cacheCount != 0 || contextCount != 0 {
		t.Fatalf("failed analysis published %d maps and %d source contexts", cacheCount, contextCount)
	}
}

func TestUnreadableDetailKeepsVerifiedCodeAndMarksBoundary(t *testing.T) {
	root := copyFixtureWithoutCodeflow(t, "nextjs-app-fixture")
	sourcePath := filepath.Join(root, "hooks", "useCart.ts")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(source),
		"sum + item.price * item.quantity, 0",
		"({ apiKey: sum + item.price * item.quantity }).apiKey, 0", 1)
	if changed == string(source) {
		t.Fatal("fixture detail was not found")
	}
	if err := os.WriteFile(sourcePath, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	moduleRoot, _ := filepath.Abs("../../..")
	t.Setenv("CODEFLOW_ADAPTER_TYPESCRIPT_BIN", "noderun:"+filepath.Join(moduleRoot, "adapters", "typescript"))
	srv, err := NewServer(Config{RepoRoot: root, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown(context.Background())

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/view?query=quick+checkout&entrySymbol=app/page.tsx%23HomePage.onQuickCheckout&token="+srv.AuthToken(), nil)
	response := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("verified flow should remain readable: status %d", response.Code)
	}
	var view struct {
		ViewID      string `json:"viewId"`
		SemanticMap struct {
			Steps []struct {
				StepID string `json:"stepId"`
				Name   string `json:"name"`
			} `json:"steps"`
		} `json:"semanticMap"`
		FlowContexts map[string]FlowContextProjection `json:"flowContexts"`
		FlowSequence semantic.FlowSequence            `json:"flowSequence"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	var detailID, predecessorID string
	for _, step := range view.SemanticMap.Steps {
		if strings.HasPrefix(step.Name, "cart.reduce(") {
			detailID = step.StepID
		}
		if step.Name == "calculateTotal()" {
			predecessorID = step.StepID
		}
	}
	if detailID == "" || predecessorID == "" {
		t.Fatal("expected detail or verified predecessor is missing from the analysis")
	}
	detail := view.FlowContexts[detailID]
	if detail.SourceLimitation == "" {
		t.Fatal("unreadable detail has no source limitation")
	}
	for _, line := range detail.DisplayedLines {
		if line.Selection != nil {
			t.Fatal("redacted detail was incorrectly highlighted")
		}
	}
	var marked bool
	for _, limitation := range view.FlowSequence.SummaryLimitations {
		if limitation.Code == "source_code_unavailable" && len(limitation.FrameRefs) > 0 {
			marked = true
			if strings.Contains(limitation.Message, "apiKey") {
				t.Fatal("source limitation exposed masked code")
			}
		}
	}
	if !marked {
		t.Fatal("unreadable detail has no explicit reading boundary")
	}
	var predecessorBoundary bool
	for _, frame := range view.FlowSequence.Frames {
		if slices.Contains(frame.StepRefs, predecessorID) && slices.Contains(frame.Outcomes, "boundary") && frame.Status != "verified" {
			predecessorBoundary = true
		}
	}
	if !predecessorBoundary {
		t.Fatal("last verified source step has no reading boundary")
	}
	restored, err := srv.storage.ReadView(context.Background(), view.ViewID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(restored), `"source_code_unavailable"`) {
		t.Fatal("saved View lost the source reading boundary")
	}
}

func TestFlowViewTaskViewRequestIdentity(t *testing.T) {
	root := copyFixtureWithoutCodeflow(t, "nextjs-app-fixture")
	moduleRoot, _ := filepath.Abs("../../..")
	t.Setenv("CODEFLOW_ADAPTER_TYPESCRIPT_BIN", "noderun:"+filepath.Join(moduleRoot, "adapters", "typescript"))

	srv, err := NewServer(Config{
		RepoRoot: root,
		Port:     0,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	entry := "app/page.tsx%23HomePage.onQuickCheckout"
	firstURL := "http://127.0.0.1/api/task/view?token=" + srv.AuthToken() + "&entrySymbol=" + entry + "&requestId=req-identity-1"

	get := func(url string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		rec := httptest.NewRecorder()
		srv.httpServer.Handler.ServeHTTP(rec, req)
		return rec
	}

	first := get(firstURL)
	if first.Code != http.StatusOK {
		t.Fatalf("expected 200 for first request, got %d: %s", first.Code, first.Body.String())
	}
	var firstDoc map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &firstDoc); err != nil {
		t.Fatalf("failed to decode first response: %v", err)
	}
	if firstDoc["requestId"] != "req-identity-1" {
		t.Errorf("expected requestId echo, got %v", firstDoc["requestId"])
	}

	second := get(firstURL)
	if second.Code != http.StatusOK {
		t.Fatalf("expected 200 for repeated request, got %d: %s", second.Code, second.Body.String())
	}
	if first.Body.String() != second.Body.String() {
		t.Error("expected identical payload reuse for same request ID and input")
	}

	conflict := get("http://127.0.0.1/api/task/view?token=" + srv.AuthToken() + "&query=checkout&requestId=req-identity-1")
	if conflict.Code != http.StatusConflict {
		t.Fatalf("expected 409 for reused request ID with different input, got %d: %s", conflict.Code, conflict.Body.String())
	}
	var conflictDoc map[string]any
	_ = json.Unmarshal(conflict.Body.Bytes(), &conflictDoc)
	if conflictDoc["code"] != "conflict" {
		t.Errorf("expected code conflict, got %v", conflictDoc["code"])
	}

	anonymous := get("http://127.0.0.1/api/task/view?token=" + srv.AuthToken() + "&entrySymbol=" + entry)
	if anonymous.Code != http.StatusOK {
		t.Fatalf("expected 200 without request ID, got %d: %s", anonymous.Code, anonymous.Body.String())
	}
	var anonymousDoc map[string]any
	_ = json.Unmarshal(anonymous.Body.Bytes(), &anonymousDoc)
	rid, _ := anonymousDoc["requestId"].(string)
	if rid == "" {
		t.Error("expected server-assigned requestId when the caller omits it")
	}
}

func TestSavedTaskViewSurvivesRestartWithoutAnalysis(t *testing.T) {
	root := copyFixtureWithoutCodeflow(t, "nextjs-app-fixture")
	moduleRoot, _ := filepath.Abs("../../..")
	t.Setenv("CODEFLOW_ADAPTER_TYPESCRIPT_BIN", "noderun:"+filepath.Join(moduleRoot, "adapters", "typescript"))
	srv, err := NewServer(Config{RepoRoot: root, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1/api/task/view?token="+srv.AuthToken()+"&entrySymbol=app/page.tsx%23HomePage.onQuickCheckout", nil))
	if rec.Code != 200 {
		t.Fatalf("analysis: %d %s", rec.Code, rec.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	id, _ := result["viewId"].(string)
	if id == "" {
		t.Fatal("missing saved view")
	}
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The adapter and current source are unavailable. Reading must still succeed.
	t.Setenv("CODEFLOW_ADAPTER_TYPESCRIPT_BIN", "missing-adapter")
	if err := os.Remove(filepath.Join(root, "app", "page.tsx")); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewServer(Config{RepoRoot: root, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Shutdown(context.Background())
	restored := httptest.NewRecorder()
	reopened.httpServer.Handler.ServeHTTP(restored, httptest.NewRequest("GET", "http://127.0.0.1/api/view?token="+reopened.AuthToken()+"&viewId="+id, nil))
	if restored.Code != 200 {
		t.Fatalf("restore: %d %s", restored.Code, restored.Body.String())
	}
	var actual map[string]any
	if err := json.Unmarshal(restored.Body.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result, actual) {
		t.Fatal("restored result differs from analysis")
	}
	denied := httptest.NewRecorder()
	reopened.httpServer.Handler.ServeHTTP(denied, httptest.NewRequest("GET", "http://127.0.0.1/api/view?viewId="+id, nil))
	if denied.Code == 200 {
		t.Fatal("unauthenticated read accepted")
	}
}

func TestRestoreTaskViewSourceContextMissingTriggersReanalysisFlag(t *testing.T) {
	store := storage.New(t.TempDir())
	savedPayload := map[string]any{
		"flowId":       "flow-missing-context",
		"title":        "결제 승인",
		"flowSequence": testFlowSequence("flow-missing-context"),
		"flowContexts": map[string]any{},
		"sourceFiles":  map[string]any{},
		"semanticMap": map[string]any{
			"steps": []any{
				map[string]any{"stepId": "step-1", "name": "승인 요청"},
			},
		},
	}
	savedBytes, err := json.Marshal(savedPayload)
	if err != nil {
		t.Fatal(err)
	}
	viewID, _, err := store.SaveView(context.Background(), savedBytes)
	if err != nil {
		t.Fatal(err)
	}

	srv := &Server{storage: store}
	restored, err := srv.RestoreTaskView(context.Background(), viewID)
	if err != nil {
		t.Fatalf("failed to restore view: %v", err)
	}

	if restored["sourceContextMissing"] != true {
		t.Fatalf("expected sourceContextMissing to be true, got: %v", restored["sourceContextMissing"])
	}
	if restored["needsReanalysis"] != true {
		t.Fatalf("expected needsReanalysis to be true, got: %v", restored["needsReanalysis"])
	}
	if notice, ok := restored["sourceNotice"].(string); !ok || notice == "" {
		t.Fatalf("expected non-empty sourceNotice, got: %v", restored["sourceNotice"])
	}
}

func TestRestoreTaskViewKeepsInvalidHistoricalReadingPathOutOfCurrentResponse(t *testing.T) {
	store := storage.New(t.TempDir())
	savedPayload := map[string]any{
		"flowSequence": testFlowSequence("flow-historical-reading"),
		"semanticMap":  map[string]any{"steps": []any{}},
		"codeReadingPath": map[string]any{
			"schemaId": semantic.CodeReadingPathSchemaID, "schemaVersion": 2,
			"nodes": []any{}, "relations": []any{},
		},
	}
	savedBytes, err := json.Marshal(savedPayload)
	if err != nil {
		t.Fatal(err)
	}
	viewID, _, err := store.SaveView(context.Background(), savedBytes)
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{storage: store}
	restored, err := srv.RestoreTaskView(context.Background(), viewID)
	if err != nil {
		t.Fatal(err)
	}
	if restored["codeReadingPath"] != nil || restored["codeReadingPathLimitation"] == "" {
		t.Fatalf("invalid saved reading path was presented as current: %+v", restored)
	}
	raw, err := store.ReadView(context.Background(), viewID)
	if err != nil || !strings.Contains(string(raw), `"codeReadingPath"`) {
		t.Fatalf("historical view was rewritten: %v", err)
	}
}

func TestRestoreTaskViewRejectsMissingFlowSequence(t *testing.T) {
	store := storage.New(t.TempDir())
	savedBytes, err := json.Marshal(map[string]any{
		"flowId":      "flow-old",
		"semanticMap": map[string]any{"steps": []any{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	viewID, _, err := store.SaveView(context.Background(), savedBytes)
	if err != nil {
		t.Fatal(err)
	}

	srv := &Server{storage: store}
	if _, err := srv.RestoreTaskView(context.Background(), viewID); !errors.Is(err, errUnsupportedFlowSequenceSchema) {
		t.Fatalf("expected unsupported FlowSequence schema error, got: %v", err)
	}
	rec := httptest.NewRecorder()
	srv.serveTaskViewDetail(rec, httptest.NewRequest(http.MethodGet, "/api/view?viewId="+viewID, nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected HTTP 400 for unsupported FlowSequence schema, got %d", rec.Code)
	}
	var errorDoc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &errorDoc); err != nil {
		t.Fatal(err)
	}
	if errorDoc["code"] != errUnsupportedFlowSequenceSchema.Error() {
		t.Fatalf("expected error code %q, got %v", errUnsupportedFlowSequenceSchema, errorDoc["code"])
	}
}

func TestRestoreTaskViewSourceDeletedOnDiskTriggersReanalysis(t *testing.T) {
	tempRepo := t.TempDir()
	store := storage.New(tempRepo)

	// A saved view pointing to a file "deleted_module.go" that does NOT exist in tempRepo
	savedPayload := map[string]any{
		"viewId":       "view-deleted-disk",
		"flowId":       "flow-deleted-disk",
		"title":        "삭제된 모듈 흐름",
		"flowSequence": testFlowSequence("flow-deleted-disk"),
		"flowContexts": map[string]any{
			"step-1": map[string]any{
				"canonicalPath": "deleted_module.go",
				"filePath":      "deleted_module.go",
			},
		},
		"sourceFiles": map[string]any{
			"deleted_module.go": []any{"line 1", "line 2"},
		},
		"semanticMap": map[string]any{
			"steps": []any{
				map[string]any{"stepId": "step-1", "name": "모듈 호출"},
			},
		},
	}
	savedBytes, err := json.Marshal(savedPayload)
	if err != nil {
		t.Fatal(err)
	}
	viewID, _, err := store.SaveView(context.Background(), savedBytes)
	if err != nil {
		t.Fatal(err)
	}

	srv := &Server{storage: store, repoRoot: tempRepo}
	restored, err := srv.RestoreTaskView(context.Background(), viewID)
	if err != nil {
		t.Fatalf("failed to restore view: %v", err)
	}

	if restored["sourceContextMissing"] != true {
		t.Fatalf("expected sourceContextMissing=true when source files are deleted from disk, got: %v", restored["sourceContextMissing"])
	}
	if restored["needsReanalysis"] != true {
		t.Fatalf("expected needsReanalysis=true when source files are deleted from disk, got: %v", restored["needsReanalysis"])
	}
	notice, _ := restored["sourceNotice"].(string)
	if !strings.Contains(notice, "삭제") && !strings.Contains(notice, "재분석") {
		t.Fatalf("expected informative notice mentioning deletion/reanalysis, got: %q", notice)
	}
}

func testFlowSequence(flowID string) map[string]any {
	return map[string]any{
		"schemaId":        semantic.FlowSequenceSchemaID,
		"schemaVersion":   semantic.FlowSequenceSchemaVersion,
		"flowID":          flowID,
		"generationId":    "generation-test",
		"computedBasisId": "basis-test",
		"snapshotID":      "snapshot-test",
		"frames":          []any{},
	}
}

func TestRestoreTaskViewStripsDeprecatedArchitecture(t *testing.T) {
	store := storage.New(t.TempDir())
	legacyPayload := map[string]any{
		"flowId":       "flow-legacy-arch",
		"architecture": "presentation",
		"flowSequence": map[string]any{
			"schemaId":        semantic.FlowSequenceSchemaID,
			"schemaVersion":   semantic.FlowSequenceSchemaVersion,
			"flowID":          "flow-legacy-arch",
			"generationId":    "generation-legacy",
			"computedBasisId": "basis-legacy",
			"snapshotID":      "snapshot-legacy",
			"frames": []any{
				map[string]any{
					"frameID":        "frame-1",
					"ordinal":        1,
					"role":           "entry",
					"title":          "Legacy Entry",
					"stepRefs":       []any{"step-1"},
					"primaryStepRef": "step-1",
					"status":         "verified",
					"frameMatchKey":  "entry|test",
					"architecture":   "controller",
				},
			},
		},
		"semanticMap": map[string]any{
			"architecture": "domain",
			"steps": []any{
				map[string]any{"stepId": "step-1", "name": "Entry"},
			},
		},
	}
	savedBytes, err := json.Marshal(legacyPayload)
	if err != nil {
		t.Fatal(err)
	}
	viewID, _, err := store.SaveView(context.Background(), savedBytes)
	if err != nil {
		t.Fatal(err)
	}

	srv := &Server{storage: store}
	restored, err := srv.RestoreTaskView(context.Background(), viewID)
	if err != nil {
		t.Fatalf("restore legacy view failed: %v", err)
	}

	if _, hasArch := restored["architecture"]; hasArch {
		t.Errorf("restored root still contains deprecated architecture")
	}
	fs, ok := restored["flowSequence"].(map[string]any)
	if !ok {
		t.Fatalf("missing flowSequence in restored")
	}
	frames, _ := fs["frames"].([]any)
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(frames))
	}
	f0 := frames[0].(map[string]any)
	if _, hasArch := f0["architecture"]; hasArch {
		t.Errorf("restored frame still contains deprecated architecture")
	}
	if restored["flowResolutionMissing"] != true {
		t.Errorf("expected flowResolutionMissing to be true for legacy view")
	}
	if notice, ok := restored["resolutionLimitation"].(string); !ok || notice == "" {
		t.Errorf("expected non-empty resolutionLimitation for legacy view")
	}
}

func TestValidateRepresentativeCodeInvariants(t *testing.T) {
	fileContent := []byte("package test\n\ntype TestRunner struct{}\n\nfunc (r *TestRunner) Run() {\n\tprintln(\"hello\")\n}\n")
	statementStart := strings.Index(string(fileContent), "println(")
	statementEnd := statementStart + len("println(\"hello\")")
	functionStart := strings.Index(string(fileContent), "func (r *TestRunner)")
	signatureEnd := strings.Index(string(fileContent), "{\n\tprintln(") + 1
	statementRange := [2]int{statementStart, statementEnd}
	anchor := slicing.Anchor{
		RepoRelativePath: "test.go", EnclosingSymbolPath: "TestRunner.Run",
		ByteRange: statementRange, FileHash: sha256Hex(fileContent),
		SpanHash: sha256Hex(fileContent[statementStart:statementEnd]),
	}
	snap := protocol.Snapshot{
		SnapshotID: "snap-1", ComputedBasisID: "basis-1",
		Files: map[string]string{
			"test.go": string(fileContent),
		},
	}
	resolved := &semantic.ResolvedTarget{
		CandidateID:     "cand-1",
		EntrySymbolPath: "test.go#TestRunner.Run",
	}

	mapIR := &semantic.SemanticMapIR{
		ComputedBasisID: "basis-1", ValidatedAgainstSnapshotID: "snap-1",
		Steps: []semantic.SemanticStep{{StepID: "step-1", Anchor: anchor, EvidenceRefs: []string{"evidence-1"}}},
		Evidence: []semantic.SemanticEvidence{{
			EvidenceID: "evidence-1", SnapshotID: "snap-1", Anchor: anchor,
			ByteRange: statementRange, ValidationStatus: "verified", SourceValidationStatus: "verified",
		}},
	}
	meta := &slicing.FlowContextMetadata{
		SnapshotID: "snap-1", CanonicalPath: "test.go", SourceHash: sha256Hex(fileContent),
		Statement:         slicing.StatementNodeMetadata{NodeKind: "statement", ByteRange: statementRange},
		StructuralContext: slicing.StructuralContextMetadata{Status: "none"},
		Callable: slicing.CallableMetadata{
			SignatureByteRange: [2]int{functionStart, signatureEnd},
			ByteRange:          [2]int{functionStart, len(fileContent) - 1},
		},
	}
	contextFor := func(metadata *slicing.FlowContextMetadata) *FlowContextProjection {
		return DeriveFlowContext(DeriveFlowContextParams{
			Step: mapIR.Steps[0], SemanticMap: mapIR,
			SnapshotFiles:    map[string][]byte{"test.go": fileContent},
			SourceSnapshotID: "snap-1", Metadata: metadata,
			AdapterHasFlowContext: true, Expansion: ExpansionFlowContext,
		})
	}
	flowContexts := map[string]*FlowContextProjection{"step-1": contextFor(meta)}
	sequence := &semantic.FlowSequence{Frames: []semantic.FlowSequenceFrame{{PrimaryStepRef: "step-1"}}}
	if err := validateRepresentativeCode(resolved, mapIR, sequence, flowContexts, snap); err != nil {
		t.Fatalf("expected exact representative code to pass: %v", err)
	}

	wholeFile := *mapIR
	wholeFile.Steps = append([]semantic.SemanticStep(nil), mapIR.Steps...)
	wholeFile.Steps[0].Anchor.ByteRange = [2]int{0, len(fileContent)}
	err := validateRepresentativeCode(resolved, &wholeFile, sequence, flowContexts, snap)
	if err == nil || !strings.Contains(err.Error(), "whole-file fallback range") {
		t.Fatalf("expected whole-file fallback error, got: %v", err)
	}

	symbolMismatch := *mapIR
	symbolMismatch.Steps = append([]semantic.SemanticStep(nil), mapIR.Steps...)
	symbolMismatch.Steps[0].Anchor.EnclosingSymbolPath = "OtherService.Run"
	err = validateRepresentativeCode(resolved, &symbolMismatch, sequence, flowContexts, snap)
	if err == nil || !strings.Contains(err.Error(), "symbol mismatch") {
		t.Fatalf("expected symbol mismatch error, got: %v", err)
	}

	missingFile := *mapIR
	missingFile.Steps = append([]semantic.SemanticStep(nil), mapIR.Steps...)
	missingFile.Steps[0].Anchor.RepoRelativePath = "missing.go"
	err = validateRepresentativeCode(resolved, &missingFile, sequence, flowContexts, snap)
	if err == nil || !strings.Contains(err.Error(), "not in snapshot") {
		t.Fatalf("expected file missing error, got: %v", err)
	}

	flowContexts["step-1"] = contextFor(nil)
	if err := validateRepresentativeCode(resolved, mapIR, sequence, flowContexts, snap); err == nil || !strings.Contains(err.Error(), "exact CodeView") {
		t.Fatalf("expected fallback context to be rejected, got: %v", err)
	}
	flowContexts["step-1"] = contextFor(meta)
	for i := range flowContexts["step-1"].DisplayedLines {
		flowContexts["step-1"].DisplayedLines[i].IsHit = false
		flowContexts["step-1"].DisplayedLines[i].Selection = nil
	}
	if err := validateRepresentativeCode(resolved, mapIR, sequence, flowContexts, snap); err == nil || !strings.Contains(err.Error(), "not highlighted") {
		t.Fatalf("expected missing CodeView highlight to be rejected, got: %v", err)
	}
}

func TestValidateRepresentativeCodeChecksEveryGateway(t *testing.T) {
	content := []byte("package test\nfunc run() {\nprintln(1)\nprintln(2)\n}\n")
	firstStart := strings.Index(string(content), "println(1)")
	secondStart := strings.Index(string(content), "println(2)")
	anchorFor := func(start int) slicing.Anchor {
		return slicing.Anchor{
			RepoRelativePath: "test.go", EnclosingSymbolPath: "run",
			ByteRange: [2]int{start, start + len("println(1)")},
			FileHash:  sha256Hex(content), SpanHash: sha256Hex(content[start : start+len("println(1)")]),
		}
	}
	model := &semantic.SemanticMapIR{
		ComputedBasisID: "basis-1", ValidatedAgainstSnapshotID: "snap-1",
		Steps: []semantic.SemanticStep{
			{StepID: "first", Anchor: anchorFor(firstStart)},
			{StepID: "second", Anchor: anchorFor(secondStart)},
		},
	}
	contexts := map[string]*FlowContextProjection{
		"first": {
			StepID: "first", SnapshotID: "snap-1", CanonicalPath: "test.go", Precision: PrecisionExact,
			SourceValidationStatus: "verified", Statement: &StatementProjection{ByteRange: model.Steps[0].Anchor.ByteRange},
			DisplayedLines: []FlowContextLine{{Text: "println(1)", IsHit: true, Selection: &FlowContextSelection{Text: "println(1)"}}},
		},
	}
	sequence := &semantic.FlowSequence{Frames: []semantic.FlowSequenceFrame{
		{PrimaryStepRef: "first"}, {PrimaryStepRef: "second"},
	}}
	snapshot := protocol.Snapshot{SnapshotID: "snap-1", ComputedBasisID: "basis-1", Files: map[string]string{"test.go": string(content)}}
	resolved := &semantic.ResolvedTarget{EntrySymbolPath: "test.go#run"}
	if err := validateRepresentativeCode(resolved, model, sequence, contexts, snapshot); err == nil || !strings.Contains(err.Error(), "second") {
		t.Fatalf("second gateway without exact CodeView source must fail: %v", err)
	}
}

func TestCodeReadingPathDisclosesUnselectedVerifiedBranches(t *testing.T) {
	model := &semantic.SemanticMapIR{
		Steps: []semantic.SemanticStep{{StepID: "decision", Kind: "guard"}},
		Edges: []semantic.SemanticEdge{
			{FromStepID: "decision", ToStepID: "success", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []semantic.SemanticBranchCondition{{StepID: "decision", Outcome: "truthy"}}},
			{FromStepID: "decision", ToStepID: "failure", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []semantic.SemanticBranchCondition{{StepID: "decision", Outcome: "falsy"}}},
			{FromStepID: "decision", ToStepID: "unknown", Kind: "control_flow", ResolutionStatus: "unresolved", Conditions: []semantic.SemanticBranchCondition{{StepID: "decision", Outcome: "unknown"}}},
		},
	}
	path := &semantic.CodeReadingPath{
		Nodes:     []semantic.CodeReadingNode{{StepID: "decision"}, {StepID: "success"}},
		Relations: []semantic.CodeReadingRelation{{FromNodeID: "decision", ToNodeID: "success", EdgeIndexes: []int{0}}},
	}
	if got := codeReadingPathBranchLimitation(model, path); !strings.Contains(got, "1개") {
		t.Fatalf("unselected confirmed outcome was hidden: %q", got)
	}
	path.Relations = append(path.Relations, semantic.CodeReadingRelation{FromNodeID: "decision", ToNodeID: "failure", EdgeIndexes: []int{1}})
	if got := codeReadingPathBranchLimitation(model, path); got != "" {
		t.Fatalf("complete confirmed outcomes were reported as missing: %q", got)
	}
	model.Steps = append(model.Steps, semantic.SemanticStep{StepID: "inner", Kind: "guard"})
	model.Edges = append(model.Edges,
		semantic.SemanticEdge{FromStepID: "success", ToStepID: "inner", Kind: "control_flow", ResolutionStatus: "resolved"},
		semantic.SemanticEdge{FromStepID: "inner", ToStepID: "result", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []semantic.SemanticBranchCondition{{StepID: "inner", Outcome: "truthy"}}},
		semantic.SemanticEdge{FromStepID: "inner", ToStepID: "other", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []semantic.SemanticBranchCondition{{StepID: "inner", Outcome: "falsy"}}},
	)
	path.Relations = append(path.Relations, semantic.CodeReadingRelation{FromNodeID: "success", ToNodeID: "result", EdgeIndexes: []int{3, 4}})
	if got := codeReadingPathBranchLimitation(model, path); !strings.Contains(got, "1개") {
		t.Fatalf("unselected outcome of a summarized decision was hidden: %q", got)
	}
}
