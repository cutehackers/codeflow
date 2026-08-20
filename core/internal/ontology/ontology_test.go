package ontology

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIngestFiltersUnauthorizedAndSecretEventsWithoutPersistingRawInput(t *testing.T) {
	raw := `{"class":"decision","text":"Name this checkout action Submit order"}
{"class":"tool_output","text":"not allowed"}
{"class":"intent","text":"api_key=super-secret"}
`
	items, err := Ingest(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Status != "inferred" || strings.Contains(items[0].Text, "secret") {
		t.Fatalf("items=%#v", items)
	}
	if entries, _ := os.ReadDir(t.TempDir()); len(entries) != 0 {
		t.Fatal("ingest must not persist raw transcripts")
	}
}
func TestApprovalPersistsOnlyConfirmedNormalizedKnowledge(t *testing.T) {
	repo := t.TempDir()
	candidate := Candidate{ID: "overlay:one", Text: "Submit order", Source: "decision", Status: "inferred"}
	confirmed, err := Approve(repo, candidate)
	if err != nil || confirmed.Text != candidate.Text {
		t.Fatalf("%#v %v", confirmed, err)
	}
	bytes, err := os.ReadFile(filepath.Join(repo, ".codeflow", "knowledge", "confirmed.json"))
	if err != nil || strings.Contains(string(bytes), "raw transcript") || !strings.Contains(string(bytes), "Submit order") {
		t.Fatalf("%s %v", bytes, err)
	}
}

func TestDomainLabelPersistsOnlyExplicitConfirmedTarget(t *testing.T) {
	repo := t.TempDir()
	label := DomainLabel{FlowID: "route:/join", ScenarioID: "sha256:scenario", StepID: "sha256:step", Title: " 이메일 인증을 요청합니다 "}
	stored, err := SaveDomainLabel(repo, label)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "confirmed" || stored.ID != DomainLabelID(label.FlowID, label.ScenarioID, label.StepID) || stored.Title != "이메일 인증을 요청합니다" {
		t.Fatalf("stored domain label lost its explicit approval boundary: %#v", stored)
	}
	loaded, err := LoadDomainLabels(repo)
	if err != nil || len(loaded) != 1 || loaded[0] != stored {
		t.Fatalf("domain label was not persisted deterministically: %#v %v", loaded, err)
	}
	if _, err := SaveDomainLabel(repo, DomainLabel{FlowID: "route:/join", ScenarioID: "sha256:scenario", Title: "password=do-not-store"}); err == nil {
		t.Fatal("secret-like domain wording was accepted")
	}
}
