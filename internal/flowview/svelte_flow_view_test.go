package flowview

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFlowViewServesSvelteUI(t *testing.T) {
	if len(SvelteFlowViewHTML) == 0 {
		t.Fatal("SvelteFlowViewHTML is empty")
	}

	requiredMarkers := []string{
		"data-view=\"flowview\"",
		"MACRO CONTEXT STORYBOARD",
		"Blast Radius Radar",
		"id=\"macro-storyboard\"",
		"id=\"radar-card\"",
		"id=\"radar-svg\"",
		"id=\"query-input\"",
		"id=\"request-form\"",
	}

	for _, marker := range requiredMarkers {
		if !strings.Contains(SvelteFlowViewHTML, marker) {
			t.Fatalf("SvelteFlowViewHTML missing required marker %q", marker)
		}
	}

	srv := &Server{svelteUI: true}
	req := httptest.NewRequest("GET", "/?token=test", nil)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	if rec.Body.String() != SvelteFlowViewHTML {
		t.Fatal("expected SvelteFlowViewHTML to be served when svelteUI is enabled")
	}

	// Also verify /live serves SvelteFlowViewHTML when svelteUI is enabled
	reqLive := httptest.NewRequest("GET", "/live?token=test", nil)
	recLive := httptest.NewRecorder()
	srv.handleIndex(recLive, reqLive)

	if recLive.Code != 200 || recLive.Body.String() != SvelteFlowViewHTML {
		t.Fatal("expected SvelteFlowViewHTML to be served on /live when svelteUI is enabled")
	}

	// Verify docs/samples/live-semantic-map-prototype.html matches SvelteFlowViewHTML
	prototypeData, err := os.ReadFile(filepath.Join("..", "..", "docs", "samples", "live-semantic-map-prototype.html"))
	if err != nil {
		t.Fatalf("failed to read live-semantic-map-prototype.html: %v", err)
	}
	if string(prototypeData) != SvelteFlowViewHTML {
		t.Fatal("expected docs/samples/live-semantic-map-prototype.html to match SvelteFlowViewHTML")
	}
}
