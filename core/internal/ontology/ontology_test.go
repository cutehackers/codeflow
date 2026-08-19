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
