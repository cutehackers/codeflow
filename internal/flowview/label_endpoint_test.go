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

func TestSemanticLabelerOptInPrinciple(t *testing.T) {
	tempDir := t.TempDir()
	srv := &Server{repoRoot: tempDir}

	// 1. By default, SLM is disabled. Endpoint returns a static fallback.
	reqBody, _ := json.Marshal(semanticLabelsHTTPRequest{
		FlowID:     "flow-1",
		SnapshotID: "snapshot-1",
		Frames: []curator.FlowSequenceFrame{
			{FrameID: "frame-1", Title: "주문 시작", Role: "entry"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/semantic/labels", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()
	srv.serveSemanticLabels(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", rec.Code)
	}
	var resp semanticLabelsHTTPResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.Status != "fallback" || len(resp.Labels) != 1 {
		t.Fatalf("expected fallback with one label by default, got: %+v", resp)
	}

	// 2. When explicitly configured with slm.enabled = true, labeler becomes available.
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": `[{"frameID":"frame-1","text":"고객 결제 요청 접수","status":"proposed"}]`,
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

	req2 := httptest.NewRequest(http.MethodPost, "/api/semantic/labels", bytes.NewReader(reqBody))
	rec2 := httptest.NewRecorder()
	srv.serveSemanticLabels(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", rec2.Code)
	}
	var resp2 semanticLabelsHTTPResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp2.Status != "proposed" || len(resp2.Labels) == 0 {
		t.Fatalf("expected status 'proposed' with labels, got status=%s labels=%+v", resp2.Status, resp2.Labels)
	}
	if resp2.Labels[0].Text != "고객 결제 요청 접수" {
		t.Fatalf("unexpected text text: %s", resp2.Labels[0].Text)
	}

	// 3. Environment variable override: CODEFLOW_SLM_ENABLED=0 overrides config file enabled:true
	t.Setenv("CODEFLOW_SLM_ENABLED", "0")
	req3 := httptest.NewRequest(http.MethodPost, "/api/semantic/labels", bytes.NewReader(reqBody))
	rec3 := httptest.NewRecorder()
	srv.serveSemanticLabels(rec3, req3)

	var resp3 semanticLabelsHTTPResponse
	if err := json.Unmarshal(rec3.Body.Bytes(), &resp3); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp3.Status != "fallback" {
		t.Fatalf("expected status 'fallback' when CODEFLOW_SLM_ENABLED=0 overrides config file, got: %s", resp3.Status)
	}
}

func TestSemanticLabelsMalformedModelResponseFallsBack(t *testing.T) {
	tempDir := t.TempDir()
	srv := &Server{repoRoot: tempDir}
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "not-json"}}},
		})
	}))
	defer mockServer.Close()

	config, err := json.Marshal(map[string]any{"slm": map[string]any{
		"enabled": true, "endpoint": mockServer.URL, "model": "test", "timeoutMs": 500,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "codeflow.config.json"), config, 0o644); err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(semanticLabelsHTTPRequest{
		FlowID: "flow-1", SnapshotID: "snapshot-1",
		Frames: []curator.FlowSequenceFrame{{FrameID: "frame-1", Title: "주문 시작", Role: "entry"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.serveSemanticLabels(rec, httptest.NewRequest(http.MethodPost, "/api/semantic/labels", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	var response semanticLabelsHTTPResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "fallback" || len(response.Labels) != 1 || response.Labels[0].Status != "fallback" {
		t.Fatalf("expected fallback response, got %+v", response)
	}
}
