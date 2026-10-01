package flowview

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"codeflow/internal/curator/semantic"
)

func TestRequestedFlowOverviewSnapshotBlocksAndImmutableTransport(t *testing.T) {
	root := t.TempDir()
	for path, source := range map[string]string{
		"package.json": "{\"name\":\"checkout\",\"version\":\"1.0.0\"}",
		"checkout.ts":  "import { calculateFee } from './fee';\nexport function processCheckout(amount: number) {\n const subtotal = amount + 1;\n // password=example-sensitive-value\n" + strings.Repeat(" // gap context\n", 30) + " const fee = calculateFee(subtotal);\n return fee;\n}\n",
		"fee.ts":       "export function calculateFee(amount: number) {\n // password=example-sensitive-value\n if (amount < 0) {\n  return 0;\n }\n return amount * 2;\n}\n",
	} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	adapter, _ := filepath.Abs("../../../adapters/typescript")
	t.Setenv("CODEFLOW_ADAPTER_TYPESCRIPT_BIN", "noderun:"+adapter)
	srv, err := NewServer(Config{RepoRoot: root, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown(context.Background())
	response := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/view?entrySymbol=checkout.ts%23processCheckout&query=Calculate+checkout+fee&token="+srv.AuthToken(), nil))
	if response.Code != http.StatusOK {
		t.Fatalf("analysis: %d %s", response.Code, response.Body.String())
	}
	var analysis map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &analysis); err != nil {
		t.Fatal(err)
	}
	var model semantic.SemanticMapIR
	if err := decodeTaskViewPart(analysis["semanticMap"], &model); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	blocks := []semantic.RequestedFlowBlockSelection{}
	bySymbol := map[string]int{}
	for _, step := range model.Steps {
		ids = append(ids, step.StepID)
		symbol := step.Anchor.RepoRelativePath + "#" + step.Anchor.EnclosingSymbolPath
		index, exists := bySymbol[symbol]
		if !exists {
			index = len(blocks)
			bySymbol[symbol] = index
			blocks = append(blocks, semantic.RequestedFlowBlockSelection{StepIDs: []string{}})
		}
		blocks[index].StepIDs = append(blocks[index].StepIDs, step.StepID)
	}
	if len(ids) < 4 || len(blocks) != 2 {
		t.Fatalf("expected multi-statement, two-file analysis: %+v", model.Steps)
	}
	proposal := map[string]any{"viewId": analysis["viewId"], "segments": []semantic.RequestedFlowSegmentSelection{{SegmentID: "checkout-fee", Purpose: "Calculate checkout fee and reject negative amounts", InclusionReason: "The request asks for the fee, including its invalid-input result password=example-description-secret", StepIDs: ids, Blocks: blocks}}, "relations": []semantic.RequestedFlowRelationSelection{}}
	body, _ := json.Marshal(proposal)
	submit := func(token string, body []byte) *httptest.ResponseRecorder {
		result := httptest.NewRecorder()
		srv.httpServer.Handler.ServeHTTP(result, httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/view/overview?token="+token, bytes.NewReader(body)))
		return result
	}
	if result := submit("", body); result.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated overview accepted: %d", result.Code)
	}
	result := submit(srv.AuthToken(), body)
	if result.Code != http.StatusOK {
		t.Fatalf("overview: %d %s", result.Code, result.Body.String())
	}
	var view map[string]any
	if err := json.Unmarshal(result.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(result.Body.Bytes(), []byte("example-sensitive-value")) || bytes.Contains(result.Body.Bytes(), []byte("example-description-secret")) {
		t.Fatal("overview exposed a source or description secret")
	}
	id := view["viewId"].(string)
	if id == analysis["viewId"] {
		t.Fatal("overview changed original view")
	}
	var overview semantic.RequestedFlowOverview
	if err := decodeTaskViewPart(view["requestedFlowOverview"], &overview); err != nil {
		t.Fatal(err)
	}
	if len(overview.Segments[0].Blocks) != 2 || len(overview.Segments[0].Blocks[0].Highlights) < 2 {
		t.Fatalf("lost block membership: %+v", overview)
	}

	if overview.SchemaVersion != 1 || overview.Categories != nil {
		t.Fatal("legacy HTTP input did not retain version 1")
	}
	proposal["categories"] = []semantic.RequestedFlowCategory{{CategoryID: "checkout", Title: "Calculate checkout password=category-description-secret", SegmentIDs: []string{"checkout-fee"}}}
	categoryBody, _ := json.Marshal(proposal)
	categoryResponse := submit(srv.AuthToken(), categoryBody)
	if categoryResponse.Code != http.StatusOK {
		t.Fatalf("category proposal: %d %s", categoryResponse.Code, categoryResponse.Body.String())
	}
	var categoryView map[string]any
	if err := json.Unmarshal(categoryResponse.Body.Bytes(), &categoryView); err != nil {
		t.Fatal(err)
	}
	var categoryOverview semantic.RequestedFlowOverview
	if err := decodeTaskViewPart(categoryView["requestedFlowOverview"], &categoryOverview); err != nil {
		t.Fatal(err)
	}
	if categoryOverview.SchemaVersion != 2 || len(categoryOverview.Categories) != 1 || bytes.Contains(categoryResponse.Body.Bytes(), []byte("category-description-secret")) {
		t.Fatalf("invalid category output: %+v", categoryOverview)
	}
	categoryID := categoryView["viewId"].(string)
	categoryRestore, err := srv.RestoreTaskView(context.Background(), categoryID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(categoryRestore["requestedFlowOverview"], categoryView["requestedFlowOverview"]) {
		t.Fatalf("version 2 did not reopen: %v", categoryRestore["requestedFlowOverviewLimitation"])
	}
	for _, malformed := range []any{nil, []any{}, []any{map[string]any{"categoryId": "checkout", "title": "Checkout", "segmentIds": []string{"missing"}}}, []any{map[string]any{"categoryId": "checkout", "title": "Checkout", "segmentIds": []string{"checkout-fee"}, "frameRefs": []string{"invented"}}}} {
		proposal["categories"] = malformed
		invalidBody, _ := json.Marshal(proposal)
		if invalid := submit(srv.AuthToken(), invalidBody); invalid.Code != http.StatusBadRequest {
			t.Fatalf("malformed categories accepted: %v %d %s", malformed, invalid.Code, invalid.Body.String())
		}
	}
	var versionMismatch map[string]any
	_ = json.Unmarshal(categoryResponse.Body.Bytes(), &versionMismatch)
	versionMismatch["requestedFlowOverview"].(map[string]any)["schemaVersion"] = 1
	if _, err := srv.SaveTaskView(context.Background(), versionMismatch); err == nil {
		t.Fatal("version 1 accepted categories")
	}
	original, _ := srv.storage.ReadView(context.Background(), analysis["viewId"].(string))
	for _, test := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"snapshot", func(doc map[string]any) { doc["requestedFlowOverview"].(map[string]any)["snapshotId"] = "other" }},
		{"schema", func(doc map[string]any) { doc["requestedFlowOverview"].(map[string]any)["schemaVersion"] = 2 }},
		{"range", func(doc map[string]any) {
			doc["requestedFlowOverview"].(map[string]any)["segments"].([]any)[0].(map[string]any)["blocks"].([]any)[0].(map[string]any)["lineRange"] = []int{1, 999}
		}},
		{"source", func(doc map[string]any) {
			doc["sourceFiles"].(map[string]any)["checkout.ts"].([]any)[0].(map[string]any)["text"] = "tampered source"
		}},
		{"redacted-gap", func(doc map[string]any) {
			doc["sourceFiles"].(map[string]any)["checkout.ts"].([]any)[12].(map[string]any)["text"] = "tampered gap context"
		}},
		{"relation", func(doc map[string]any) {
			doc["requestedFlowOverview"].(map[string]any)["relations"].([]any)[0].(map[string]any)["edgeIndexes"] = []int{999}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var doc map[string]any
			_ = json.Unmarshal(result.Body.Bytes(), &doc)
			test.change(doc)
			if _, err := srv.SaveTaskView(context.Background(), doc); err == nil {
				t.Fatal("invalid overview saved")
			}
			FormatTaskViewResponse(doc, root)
			if doc["requestedFlowOverview"] != nil || doc["requestedFlowOverviewLimitation"] == nil {
				t.Fatal("invalid overview presented as verified")
			}
		})
	}
	after, _ := srv.storage.ReadView(context.Background(), analysis["viewId"].(string))
	if !bytes.Equal(original, after) {
		t.Fatal("original bytes changed")
	}
	if err := os.WriteFile(filepath.Join(root, "fee.ts"), []byte("export function calculateFee(amount: number) { return 999; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := srv.RestoreTaskView(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reopened["requestedFlowOverview"], view["requestedFlowOverview"]) {
		t.Fatalf("restoration lost overview: %v", reopened["requestedFlowOverviewLimitation"])
	}
	if !strings.Contains(string(result.Body.Bytes()), "return amount * 2") {
		t.Fatal("same-snapshot source absent")
	}
}
