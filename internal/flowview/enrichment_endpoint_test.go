package flowview

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"codeflow/internal/curator"
)

func TestSemanticEnrichmentOptInPrinciple(t *testing.T) {
	tempDir := t.TempDir()
	srv := &Server{repoRoot: tempDir}

	// 1. By default, SLM is disabled (opt-in principle). Endpoint returns "unavailable".
	reqBody, _ := json.Marshal(semanticEnrichmentHTTPRequest{
		Frames: []curator.FlowFrame{
			{FrameID: "frame-1", Title: "주문 시작", Role: "entry"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/semantic/enrich", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()
	srv.serveSemanticEnrichment(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", rec.Code)
	}
	var resp semanticEnrichmentHTTPResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.Status != "unavailable" {
		t.Fatalf("expected status 'unavailable' by default, got: %s", resp.Status)
	}

	// 2. When explicitly configured with slm.enabled = true, enricher becomes available.
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": `[{"frameId":"frame-1","narrative":"고객 결제 요청 접수","status":"enriched"}]`,
					},
				},
			},
		})
	}))
	defer mockServer.Close()

	cfgContent := map[string]any{
		"slm": map[string]any{
			"enabled":   true,
			"endpoint":  mockServer.URL,
			"model":     "qwen2.5-coder:1.5b",
			"timeoutMs": 1000,
		},
	}
	cfgBytes, _ := json.Marshal(cfgContent)
	if err := os.WriteFile(filepath.Join(tempDir, "codeflow.config.json"), cfgBytes, 0644); err != nil {
		t.Fatal(err)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/semantic/enrich", bytes.NewReader(reqBody))
	rec2 := httptest.NewRecorder()
	srv.serveSemanticEnrichment(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", rec2.Code)
	}
	var resp2 semanticEnrichmentHTTPResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp2.Status != "enriched" || len(resp2.Narratives) == 0 {
		t.Fatalf("expected status 'enriched' with narratives, got status=%s narratives=%+v", resp2.Status, resp2.Narratives)
	}
	if resp2.Narratives[0].Narrative != "고객 결제 요청 접수" {
		t.Fatalf("unexpected narrative text: %s", resp2.Narratives[0].Narrative)
	}

	// 3. Environment variable override: CODEFLOW_SLM_ENABLED=0 overrides config file enabled:true
	t.Setenv("CODEFLOW_SLM_ENABLED", "0")
	req3 := httptest.NewRequest(http.MethodPost, "/api/semantic/enrich", bytes.NewReader(reqBody))
	rec3 := httptest.NewRecorder()
	srv.serveSemanticEnrichment(rec3, req3)

	var resp3 semanticEnrichmentHTTPResponse
	if err := json.Unmarshal(rec3.Body.Bytes(), &resp3); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp3.Status != "unavailable" {
		t.Fatalf("expected status 'unavailable' when CODEFLOW_SLM_ENABLED=0 overrides config file, got: %s", resp3.Status)
	}
}
