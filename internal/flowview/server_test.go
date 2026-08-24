package flowview_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"codeflow/internal/flowview"
	"codeflow/internal/storage"
)

func TestFlowViewServer(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codeflow-flowview-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	token := "test-secret-token-12345"
	srv, err := flowview.NewServer(flowview.Config{
		RepoRoot:  tmpDir,
		Port:      0, // random free port
		AuthToken: token,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	srv.Start()
	defer func() {
		_ = srv.Shutdown(context.Background())
	}()

	// 1. Publish a dummy flow in storage
	st := storage.New(tmpDir)
	sess, err := st.BeginGeneration("test-fingerprint")
	if err != nil {
		t.Fatalf("BeginGeneration failed: %v", err)
	}
	dummyFlow := []byte(`{
		"flowId": "flow-1234567890abcdef",
		"title": "Email Signup Flow",
		"basisSha": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"generatedAt": "2026-08-24T00:00:00Z",
		"steps": [
			{
				"ordinal": 1,
				"name": "Check email",
				"provenance": "derived",
				"freshness": "fresh",
				"confidence": 0.9,
				"basisSha": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				"anchor": {
					"repoRelativePath": "lib/main.dart",
					"byteRange": [0, 10],
					"fileHash": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
					"spanHash": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
					"enclosingSymbolPath": "EmailSignupNotifier.submit",
					"canonicalAstFingerprint": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
				}
			}
		],
		"unknowns": []
	}`)
	_ = sess.AddFlowSpec("flow-1234567890abcdef", dummyFlow, storage.FlowSummary{
		FlowID:          "flow-1234567890abcdef",
		Title:           "Email Signup Flow",
		EntrySymbolPath: "lib/main.dart#main",
		StepCount:       1,
	})
	_ = sess.Commit()

	client := &http.Client{Timeout: 5 * time.Second}
	baseURL := "http://" + srv.Addr()

	// 2. Test GET / (HTML shell)
	resp, err := client.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET / status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	// 3. Test GET /api/flows (with token via query param)
	authTok := srv.AuthToken()
	resp, err = client.Get(baseURL + "/api/flows?token=" + authTok)
	if err != nil {
		t.Fatalf("GET /api/flows failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /api/flows status = %d, want 200", resp.StatusCode)
	}
	var idx storage.GenerationIndex
	_ = json.NewDecoder(resp.Body).Decode(&idx)
	resp.Body.Close()
	if len(idx.Flows) != 1 {
		t.Errorf("expected 1 flow in index, got %d", len(idx.Flows))
	}

	// 4. Test GET /api/flow?id=... (with token via header)
	reqFlow, _ := http.NewRequest(http.MethodGet, baseURL+"/api/flow?id=flow-1234567890abcdef", nil)
	reqFlow.Header.Set("X-CodeFlow-Token", srv.AuthToken())
	resp, err = client.Do(reqFlow)
	if err != nil {
		t.Fatalf("GET /api/flow failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /api/flow status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	// 5. Test POST /api/approve with valid token
	approveBody, _ := json.Marshal(map[string]any{
		"flowId":     "flow-1234567890abcdef",
		"symbolPath": "EmailSignupNotifier.submit",
		"name":       "Approved Email Signup",
		"rules":      []string{"Must be valid format"},
	})
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/api/approve", bytes.NewReader(approveBody))
	req.Header.Set("X-CodeFlow-Token", token)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("POST /api/approve failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("POST /api/approve status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	// 6. Test POST /api/approve with missing/wrong token -> 401
	reqBad, _ := http.NewRequest(http.MethodPost, baseURL+"/api/approve", bytes.NewReader(approveBody))
	reqBad.Header.Set("X-CodeFlow-Token", "wrong-token")
	respBad, err := client.Do(reqBad)
	if err != nil {
		t.Fatalf("POST bad token failed: %v", err)
	}
	if respBad.StatusCode != http.StatusUnauthorized {
		t.Errorf("POST bad token status = %d, want 401", respBad.StatusCode)
	}
	respBad.Body.Close()
}
