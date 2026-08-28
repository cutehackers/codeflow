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

func TestArchitectureMapEndpoints(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codeflow-map-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	token := "map-test-token-67890"
	srv, err := flowview.NewServer(flowview.Config{RepoRoot: tmpDir, Port: 0, AuthToken: token})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	srv.Start()
	defer func() { _ = srv.Shutdown(context.Background()) }()

	st := storage.New(tmpDir)
	sess, err := st.BeginGeneration("map-fingerprint")
	if err != nil {
		t.Fatalf("BeginGeneration failed: %v", err)
	}
	flowA := []byte(`{
		"flowId": "flow-aaaaaaaaaaaaaaaa",
		"title": "panel journey",
		"steps": [
			{"ordinal": 1, "name": "open", "provenance": "derived", "freshness": "fresh", "confidence": 0.9,
			 "anchor": {"repoRelativePath": "lib/screens/a_panel.dart", "enclosingSymbolPath": "Panel.show"}},
			{"ordinal": 2, "name": "run", "provenance": "derived", "freshness": "fresh", "confidence": 0.9,
			 "anchor": {"repoRelativePath": "lib/core/dispatcher.dart", "enclosingSymbolPath": "Dispatcher.run"}},
			{"ordinal": 3, "name": "store", "provenance": "derived", "freshness": "fresh", "confidence": 0.9,
			 "anchor": {"repoRelativePath": "lib/persist/vault.dart", "enclosingSymbolPath": "Vault.put"}},
			{"ordinal": 4, "name": "track", "provenance": "derived", "freshness": "fresh", "confidence": 0.9,
			 "anchor": {"repoRelativePath": "lib/shared/keeper.dart", "enclosingSymbolPath": "Keeper.watch"},
			 "stateDelta": {"before": "idle", "after": "watching"}}
		],
		"edges": [
			{"kind": "resolved_cross_file", "toSymbolPath": "Dispatcher.run", "resolutionStatus": "resolved", "stepOrdinal": 1},
			{"kind": "resolved_cross_file", "toSymbolPath": "Vault.put", "resolutionStatus": "resolved", "stepOrdinal": 2},
			{"kind": "resolved_cross_file", "toSymbolPath": "Keeper.watch", "resolutionStatus": "resolved", "stepOrdinal": 2}
		]
	}`)
	flowB := []byte(`{
		"flowId": "flow-bbbbbbbbbbbbbbbb",
		"title": "admin journey",
		"steps": [
			{"ordinal": 1, "name": "sheet", "provenance": "approved", "freshness": "fresh", "confidence": 1.0,
			 "anchor": {"repoRelativePath": "lib/screens/b_sheet.dart", "enclosingSymbolPath": "AdminSheet.show"}},
			{"ordinal": 2, "name": "run", "provenance": "derived", "freshness": "fresh", "confidence": 0.9,
			 "anchor": {"repoRelativePath": "lib/core/dispatcher.dart", "enclosingSymbolPath": "Dispatcher.run"}},
			{"ordinal": 3, "name": "store", "provenance": "derived", "freshness": "fresh", "confidence": 0.9,
			 "anchor": {"repoRelativePath": "lib/persist/vault.dart", "enclosingSymbolPath": "Vault.put"}}
		],
		"edges": [
			{"kind": "resolved_cross_file", "toSymbolPath": "Dispatcher.run", "resolutionStatus": "resolved", "stepOrdinal": 1},
			{"kind": "resolved_cross_file", "toSymbolPath": "Vault.put", "resolutionStatus": "resolved", "stepOrdinal": 2}
		]
	}`)
	if err := sess.AddFlowSpec("flow-aaaaaaaaaaaaaaaa", flowA, storage.FlowSummary{FlowID: "flow-aaaaaaaaaaaaaaaa", Title: "panel journey", EntrySymbolPath: "lib/screens/a_panel.dart#Panel.show", StepCount: 2}); err != nil {
		t.Fatalf("AddFlowSpec A: %v", err)
	}
	if err := sess.AddFlowSpec("flow-bbbbbbbbbbbbbbbb", flowB, storage.FlowSummary{FlowID: "flow-bbbbbbbbbbbbbbbb", Title: "admin journey", EntrySymbolPath: "lib/screens/b_sheet.dart#AdminSheet.show", StepCount: 2}); err != nil {
		t.Fatalf("AddFlowSpec B: %v", err)
	}
	if err := sess.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	baseURL := "http://" + srv.Addr()

	// Auth required.
	resp, err := client.Get(baseURL + "/api/map")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /api/map without token = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	// Empty map shape when no generation exists — but we have one; first
	// verify against a fresh server? No: this repo HAS a generation now.
	req, _ := http.NewRequest(http.MethodGet, baseURL+"/api/map?token="+srv.AuthToken(), nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/map = %d, want 200", resp.StatusCode)
	}
	var amap flowview.ArchitectureMap
	if err := json.NewDecoder(resp.Body).Decode(&amap); err != nil {
		t.Fatalf("decode map: %v", err)
	}
	resp.Body.Close()

	if amap.GenerationID == "" {
		t.Errorf("generationId empty")
	}
	if len(amap.Lanes) == 0 || len(amap.Components) != 5 {
		t.Fatalf("lanes=%v components=%d, want 5 unique symbols", amap.Lanes, len(amap.Components))
	}
	foundDisp := false
	for _, c := range amap.Components {
		if c.SymbolPath == "Dispatcher.run" {
			foundDisp = true
			if c.Layer != flowview.LayerUsecase {
				t.Errorf("Dispatcher.run layer = %q, want usecase", c.Layer)
			}
			if c.Confidence <= 0 || c.Confidence > 1 {
				t.Errorf("Dispatcher.run confidence out of range: %v", c.Confidence)
			}
			if len(c.Flows) != 2 {
				t.Errorf("Dispatcher.run flows = %v, want both", c.Flows)
			}
		}
	}
	if !foundDisp {
		t.Fatal("Dispatcher.run component missing")
	}

	// Manual override endpoint: valid lane lands in the manifest and shows
	// up on the next map render with confidence 1.0.
	ovBody, _ := json.Marshal(map[string]string{"symbol": "Dispatcher.run", "lane": "data"})
	post, _ := http.NewRequest(http.MethodPost, baseURL+"/api/map/override?token="+srv.AuthToken(), bytes.NewReader(ovBody))
	resp, err = client.Do(post)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST override = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	req2, _ := http.NewRequest(http.MethodGet, baseURL+"/api/map?token="+srv.AuthToken(), nil)
	resp, err = client.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	var remapped flowview.ArchitectureMap
	_ = json.NewDecoder(resp.Body).Decode(&remapped)
	resp.Body.Close()
	for _, c := range remapped.Components {
		if c.SymbolPath == "Dispatcher.run" && (c.Layer != flowview.LayerData || c.Confidence != 1.0) {
			t.Errorf("override not applied on re-render: %+v", c)
		}
	}

	// Invalid lane rejected.
	badBody, _ := json.Marshal(map[string]string{"symbol": "X.y", "lane": "Not-A-Lane"})
	bad, _ := http.NewRequest(http.MethodPost, baseURL+"/api/map/override?token="+srv.AuthToken(), bytes.NewReader(badBody))
	resp, err = client.Do(bad)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("POST invalid lane = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}
