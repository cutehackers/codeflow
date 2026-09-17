package flowview

import (
	"codeflow/internal/evidence"
	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const contextFixture = "class Settings {\n  void build() {\n    prepare(); // unrelated\n    menu(onTap: () {\n      logout();\n    });\n    cleanup();\n  }\n}\n"

func contextParams(source string) DeriveFlowContextParams {
	span := func(text string) [2]int {
		start := strings.Index(source, text)
		return [2]int{start, start + len(text)}
	}
	stmt, sig := span("logout();"), span("void build() ")
	call := [2]int{sig[0], strings.LastIndex(source, "  }") + 3}
	structural := span("onTap: () {\n      logout();\n    }")
	hash := sha256Hex([]byte(source))
	anchor := slicing.Anchor{RepoRelativePath: "lib/settings.dart", ByteRange: stmt, FileHash: hash, SpanHash: sha256Hex([]byte(source[stmt[0]:stmt[1]])), EnclosingSymbolPath: "Settings.build", CanonicalAstFingerprint: "statement"}
	step := semantic.SemanticStep{StepID: "step-logout", Ordinal: 1, Anchor: anchor, EvidenceRefs: []string{"ev-logout"}}
	meta := &slicing.FlowContextMetadata{SnapshotID: "snap-fixture", CanonicalPath: anchor.RepoRelativePath, SourceHash: hash, Statement: slicing.StatementNodeMetadata{NodeKind: "statement", ByteRange: stmt}, StructuralContext: slicing.StructuralContextMetadata{Status: "present", NodeKind: "callback", ByteRange: &structural}, Callable: slicing.CallableMetadata{SignatureByteRange: sig, ByteRange: call}}
	m := &semantic.SemanticMapIR{GenerationID: "gen-fixture", ValidatedAgainstSnapshotID: "snap-fixture", Steps: []semantic.SemanticStep{step}, Evidence: []semantic.SemanticEvidence{{EvidenceID: "ev-logout", SnapshotID: "snap-fixture", Anchor: anchor, ByteRange: stmt, ValidationStatus: "verified"}}}
	return DeriveFlowContextParams{Step: step, SemanticMap: m, SnapshotFiles: map[string][]byte{anchor.RepoRelativePath: []byte(source)}, SourceSnapshotID: "snap-fixture", AdapterHasFlowContext: true, Metadata: meta}
}

func observeContext(t *testing.T, id string, params DeriveFlowContextParams, p *FlowContextProjection) {
	t.Helper()
	evidence.ObserveFlowContext(t, "codeflow/internal/flowview", []string{id}, evidence.FlowContextRecord{SnapshotID: p.SnapshotID, SnapshotTreeDigest: sha256Hex(params.SnapshotFiles[params.Step.Anchor.RepoRelativePath]), Precision: string(p.Precision), Expansion: string(p.ExpansionScope), DisplayedLinesCount: len(p.DisplayedLines)}, map[string]any{"fixtureSource": params.SnapshotFiles[params.Step.Anchor.RepoRelativePath], "response": p})
}
func requireExact(t *testing.T, p *FlowContextProjection) {
	t.Helper()
	if p.Precision != PrecisionExact {
		t.Fatalf("precision=%s: %s", p.Precision, p.SourceLimitation)
	}
}

func TestRFLSCR2VS11_A01(t *testing.T) {
	params := contextParams(contextFixture)
	p := DeriveFlowContext(params)
	requireExact(t, p)
	if p.Statement.ByteRange != params.Step.Anchor.ByteRange || p.CallableSignature.Text != "void build()" || p.EvidenceStatus != "verified" {
		t.Fatalf("wrong source projection: %+v", p)
	}
	for _, line := range p.DisplayedLines {
		if line.IsHit && !strings.Contains(line.Text, "logout();") {
			t.Fatal("non-statement highlighted")
		}
	}
	observeContext(t, "VS11-A1", params, p)
}
func TestRFLSCR2VS11_A02(t *testing.T) {
	params := contextParams(contextFixture)
	p := DeriveFlowContext(params)
	requireExact(t, p)
	if p.StructuralContext.NodeKind != "callback" {
		t.Fatal("missing callback")
	}
	for _, line := range p.DisplayedLines {
		if strings.Contains(line.Text, "onTap:") && (!line.IsStruct || line.IsHit) {
			t.Fatal("structure selected as statement")
		}
	}
	observeContext(t, "VS11-A2", params, p)
}
func TestRFLSCR2VS11_A03(t *testing.T) {
	params := contextParams(contextFixture)
	if DeriveFlowContext(params).DirectRelation.HasRelation {
		t.Fatal("invented relation")
	}
	params.SemanticMap.Edges = []semantic.SemanticEdge{{FromStepID: params.Step.StepID, ToStepID: "next", ToSymbolPath: "Service.logout", Kind: "call"}}
	p := DeriveFlowContext(params)
	if !p.DirectRelation.HasRelation || p.DirectRelation.TargetSymbolPath != "Service.logout" {
		t.Fatal("lost direct edge")
	}
	params.SnapshotFiles = nil
	limited := DeriveFlowContext(params)
	if !limited.DirectRelation.HasRelation || limited.Precision != PrecisionUnavailable {
		t.Fatal("source failure lost relation")
	}
	observeContext(t, "VS11-A3", contextParams(contextFixture), p)
}
func TestRFLSCR2VS11_A04(t *testing.T) {
	params := contextParams(contextFixture)
	p := DeriveFlowContext(params)
	for _, line := range p.DisplayedLines {
		if strings.Contains(line.Text, "unrelated") || strings.Contains(line.Text, "cleanup") {
			t.Fatal("default expanded unrelated source")
		}
	}
	if p.Callable != nil {
		t.Fatal("default includes callable source")
	}
	params.Expansion = ExpansionCallable
	expanded := DeriveFlowContext(params)
	requireExact(t, expanded)
	if expanded.Callable == nil || len(expanded.DisplayedLines) <= len(p.DisplayedLines) {
		t.Fatal("callable did not expand")
	}
	params.Expansion = ExpansionFile
	file := DeriveFlowContext(params)
	requireExact(t, file)
	if file.DisplayedLines[0].Text != "class Settings {" || file.Statement.ByteRange != p.Statement.ByteRange {
		t.Fatal("file expansion changed evidence")
	}
	observeContext(t, "VS11-A4", params, file)
}
func TestRFLSCR2VS11_A05(t *testing.T) {
	cases := map[string]func(*DeriveFlowContextParams){
		"missing metadata":      func(p *DeriveFlowContextParams) { p.Metadata = nil },
		"missing capability":    func(p *DeriveFlowContextParams) { p.AdapterHasFlowContext = false },
		"missing snapshot":      func(p *DeriveFlowContextParams) { p.Metadata.SnapshotID = "" },
		"missing hash":          func(p *DeriveFlowContextParams) { p.Metadata.SourceHash = "" },
		"missing path":          func(p *DeriveFlowContextParams) { p.Metadata.CanonicalPath = "" },
		"wrong snapshot":        func(p *DeriveFlowContextParams) { p.Metadata.SnapshotID = "other" },
		"wrong source snapshot": func(p *DeriveFlowContextParams) { p.SourceSnapshotID = "other" },
		"wrong hash":            func(p *DeriveFlowContextParams) { p.Metadata.SourceHash = strings.Repeat("0", 64) },
		"wrong node":            func(p *DeriveFlowContextParams) { p.Metadata.Statement.NodeKind = "condition" },
		"broad anchor":          func(p *DeriveFlowContextParams) { p.Step.Anchor.ByteRange = p.Metadata.Callable.ByteRange },
		"equal structure": func(p *DeriveFlowContextParams) {
			r := p.Metadata.Statement.ByteRange
			p.Metadata.StructuralContext.ByteRange = &r
		},
		"wrong lines":         func(p *DeriveFlowContextParams) { p.Metadata.Statement.LineRange = [2]int{1, 2} },
		"missing callable":    func(p *DeriveFlowContextParams) { p.Metadata.Callable = slicing.CallableMetadata{} },
		"missing evidence":    func(p *DeriveFlowContextParams) { p.SemanticMap.Evidence = nil },
		"unverified evidence": func(p *DeriveFlowContextParams) { p.SemanticMap.Evidence[0].ValidationStatus = "unverified" },
		"wrong span hash":     func(p *DeriveFlowContextParams) { p.Step.Anchor.SpanHash = "wrong" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			params := contextParams(contextFixture)
			mutate(&params)
			p := DeriveFlowContext(params)
			if p.Precision == PrecisionExact || p.Statement != nil || p.SourceLimitation == "" {
				t.Fatalf("invalid proof accepted: %+v", p)
			}
			for _, line := range p.DisplayedLines {
				if line.IsHit {
					t.Fatal("invalid proof highlighted")
				}
			}
		})
	}
	params := contextParams(contextFixture)
	params.Metadata = nil
	observeContext(t, "VS11-A5", params, DeriveFlowContext(params))
}
func TestRFLSCR2VS11_A06(t *testing.T) {
	source := strings.Replace(contextFixture, "    prepare(); // unrelated", "    final apiKey =\n      'test-secret-placeholder';", 1)
	params := contextParams(source)
	params.Expansion = ExpansionFile
	p := DeriveFlowContext(params)
	requireExact(t, p)
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), "test-secret-placeholder") || !p.SourceRedacted {
		t.Fatal("multiline secret leaked")
	}
	if len(p.DisplayedLines) != len(strings.Split(source, "\n")) {
		t.Fatal("redaction changed snapshot line numbers")
	}
	params.RedactSourceError = true
	rejected := DeriveFlowContext(params)
	if len(rejected.DisplayedLines) != 0 || rejected.Precision != PrecisionUnavailable {
		t.Fatal("unsafe source shown")
	}
	observeContext(t, "VS11-A6", params, p)
}
func TestRFLSCR2VS11_A07(t *testing.T) {
	params, srv := contextServer(t)
	r := httptest.NewRecorder()
	srv.serveFlowContext(r, httptest.NewRequest("GET", "/api/flow/context?stepId=step-logout&generationId=gen-fixture", nil))
	var p FlowContextProjection
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &p) != nil {
		t.Fatal(r.Body.String())
	}
	requireExact(t, &p)
	for _, control := range []string{`id="flow-context-precision"`, `role="status"`, `id="btn-expand-callable"`, `id="btn-expand-file"`, `id="flow-source-limitation"`} {
		if !strings.Contains(FlowViewHTML, control) {
			t.Fatalf("missing accessible control %s", control)
		}
	}
	observeContext(t, "VS11-A7", params, &p)
}
func TestRFLSCR2VS11_A08(t *testing.T) {
	source := strings.Replace(contextFixture, "    menu(onTap: () {\n      logout();\n    });", "    logout();", 1)
	params := contextParams(source)
	params.Metadata.StructuralContext = slicing.StructuralContextMetadata{Status: "none"}
	params.Metadata.Callable.Signature = "invented callable description"
	p := DeriveFlowContext(params)
	requireExact(t, p)
	if p.StructuralContext.Status != "none" || p.CallableSignature.Text != "void build()" {
		t.Fatal("invented structure or source")
	}
	observeContext(t, "VS11-A8", params, p)
}

func TestFlowContextPrecisionDoesNotUpgradeUnknownEvidence(t *testing.T) {
	params := contextParams(contextFixture)
	params.SemanticMap.Evidence[0].ValidationStatus = "unknown"
	for _, scope := range []FlowExpansionScope{ExpansionFlowContext, ExpansionCallable, ExpansionFile} {
		params.Expansion = scope
		p := DeriveFlowContext(params)
		requireExact(t, p)
		if p.EvidenceStatus != "unknown" || p.Authority != "candidate" {
			t.Fatal("statement precision upgraded graph authority")
		}
	}
}

func TestFlowContextOneLineCallableSelectsOnlyStatementBytes(t *testing.T) {
	const source = "class Settings { void build() { logout(); } }"
	params := contextParams(contextFixture)
	stmt := [2]int{strings.Index(source, "logout();"), strings.Index(source, "logout();") + len("logout();")}
	sig := [2]int{strings.Index(source, "void build()"), strings.Index(source, "{ logout")}
	params.SnapshotFiles[params.Step.Anchor.RepoRelativePath] = []byte(source)
	params.Metadata.SourceHash = sha256Hex([]byte(source))
	params.Metadata.Statement.ByteRange = stmt
	params.Metadata.StructuralContext = slicing.StructuralContextMetadata{Status: "none"}
	params.Metadata.Callable = slicing.CallableMetadata{SignatureByteRange: sig, ByteRange: [2]int{sig[0], len(source) - 2}}
	params.Step.Anchor.ByteRange = stmt
	params.Step.Anchor.FileHash = params.Metadata.SourceHash
	params.Step.Anchor.SpanHash = sha256Hex([]byte("logout();"))
	params.SemanticMap.Evidence[0].Anchor = params.Step.Anchor
	params.SemanticMap.Evidence[0].ByteRange = stmt
	p := DeriveFlowContext(params)
	requireExact(t, p)
	if len(p.DisplayedLines) != 1 || p.DisplayedLines[0].Selection == nil || p.DisplayedLines[0].Selection.Text != "logout();" || strings.Join(p.Statement.Source, "") != "logout();" {
		t.Fatalf("whole callable selected: %+v", p)
	}
}

func contextServer(t *testing.T) (DeriveFlowContextParams, *Server) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "lib/settings.dart"), []byte(contextFixture), 0600); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(Config{RepoRoot: root, Port: 0, AuthToken: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	snapshot, _, release, err := srv.captureAnalysisSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	params := contextParams(contextFixture)
	params.SourceSnapshotID = snapshot.SnapshotID
	params.Metadata.SnapshotID = snapshot.SnapshotID
	params.SemanticMap.ValidatedAgainstSnapshotID = snapshot.SnapshotID
	params.SemanticMap.Evidence[0].SnapshotID = snapshot.SnapshotID
	srv.mapCache["gen-fixture"] = params.SemanticMap
	srv.rememberFlowContexts(params.SemanticMap, &slicing.SlicedPayload{SnapshotID: snapshot.SnapshotID, Steps: []slicing.SliceStep{{Ordinal: 1, Anchor: params.Step.Anchor, FlowContext: params.Metadata}}}, true)
	return params, srv
}
func TestFlowContextRetainsOriginalSnapshotAndRejectsWrongGeneration(t *testing.T) {
	params, srv := contextServer(t)
	os.WriteFile(filepath.Join(srv.repoRoot, "lib/settings.dart"), []byte(strings.ReplaceAll(contextFixture, "logout()", "newCall()")), 0600)
	_, _, release, err := srv.captureAnalysisSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()
	for _, scope := range []string{"flow_context", "callable", "file"} {
		r := httptest.NewRecorder()
		srv.serveFlowContext(r, httptest.NewRequest("GET", "/api/flow/context?stepId=step-logout&generationId=gen-fixture&expand="+scope, nil))
		var p FlowContextProjection
		json.Unmarshal(r.Body.Bytes(), &p)
		requireExact(t, &p)
		if p.SnapshotID != params.SourceSnapshotID || strings.Contains(r.Body.String(), "newCall") {
			t.Fatal("live source replaced generation source")
		}
	}
	for _, gen := range []string{"missing", ""} {
		r := httptest.NewRecorder()
		srv.serveFlowContext(r, httptest.NewRequest("GET", "/api/flow/context?stepId=step-logout&generationId="+gen, nil))
		if r.Code == http.StatusOK {
			t.Fatal("unknown generation silently replaced")
		}
	}
}
func TestFlowContextCapabilityAndProducerMutation(t *testing.T) {
	params, srv := contextServer(t)
	params.Metadata.Statement.NodeKind = "class"
	r := httptest.NewRecorder()
	srv.serveFlowContext(r, httptest.NewRequest("GET", "/api/flow/context?stepId=step-logout&generationId=gen-fixture", nil))
	var p FlowContextProjection
	json.Unmarshal(r.Body.Bytes(), &p)
	requireExact(t, &p)
	srv.rememberFlowContexts(params.SemanticMap, nil, false)
	r = httptest.NewRecorder()
	srv.serveFlowContext(r, httptest.NewRequest("GET", "/api/flow/context?stepId=step-logout&generationId=gen-fixture", nil))
	json.Unmarshal(r.Body.Bytes(), &p)
	if p.Precision != PrecisionUnavailable {
		t.Fatal("language inferred capability")
	}
}
