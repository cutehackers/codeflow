package flowview

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"codeflow/internal/workspace"
)

// Test-only network fixture for Playwright. The model host is the external
// controlled boundary. Proposal persistence, approval HTTP, history and SSE
// are production collaborators, not synthetic successful responses.
func TestApprovalBrowserFixture(t *testing.T) {
	if os.Getenv("CODEFLOW_APPROVAL_BROWSER_FIXTURE") != "1" {
		t.Skip("started explicitly by the Playwright approval fixture")
	}
	srv, root, enrichment := newFlowViewApprovalProposalFixture(t)
	srv.Start()
	defer func() { _ = srv.httpServer.Close(); _ = srv.Shutdown(context.Background()) }()
	_, portText, err := net.SplitHostPort(srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"semanticMap": srv.cachedSemanticMap(enrichment.Proposal.GenerationID, ""), "enrichment": enrichment, "workspaceId": srv.approvalWorkspaceID}
	stop := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != http.MethodPost || r.Header.Get("X-CodeFlow-Token") != srv.AuthToken() {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/stop":
			once.Do(func() { close(stop) })
		case "/restart":
			// Close real live connections to exercise EventSource reconnection.
			_ = srv.httpServer.Close()
			if err := srv.Shutdown(r.Context()); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			restarted, err := NewServer(Config{RepoRoot: root, Port: port, AuthToken: srv.AuthToken()})
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			srv = restarted
			srv.Start()
		case "/advance":
			_, _, err := srv.engine.ApplyVersionedEdit(r.Context(), workspace.EditRequest{Path: "main.go", Content: []byte("package main\nfunc Changed() {}\n"), DocumentVersion: 2, Source: workspace.SourceIDEVersioned})
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		default:
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer control.Close()
	data, err := json.Marshal(map[string]any{"url": srv.URL(), "controlUrl": control.URL, "token": srv.AuthToken(), "payload": payload})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("APPROVAL_BROWSER_FIXTURE_JSON:%s\n", data)
	select {
	case <-stop:
	case <-time.After(2 * time.Minute):
		t.Fatal("approval browser fixture was not stopped")
	}
}
