package flowview

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLiveViewMatchesDesignatedTemplate(t *testing.T) {
	if IndexHTML != FlowViewHTML {
		t.Fatal("IndexHTML alias must match FlowViewHTML")
	}
	if LiveSemanticHTML != LiveViewHTML {
		t.Fatal("LiveSemanticHTML alias must match LiveViewHTML")
	}
}

func TestFlowViewAndLiveViewsAreSeparate(t *testing.T) {
	srv := &Server{}
	for _, tc := range []struct{ path, want string }{
		{"/", FlowViewHTML},
		{"/?token=test&request=checkout", FlowViewHTML},
		{"/?live=0", FlowViewHTML},
		{"/live", FlowViewHTML},
		{"/live?request=checkout", FlowViewHTML},
		{"/?live=1&request=checkout", FlowViewHTML},
	} {
		t.Run(tc.path, func(t *testing.T) {
			r := httptest.NewRecorder()
			srv.handleIndex(r, httptest.NewRequest("GET", tc.path, nil))
			if r.Code != 200 || r.Body.String() != tc.want {
				t.Fatalf("wrong view at %s: status %d", tc.path, r.Code)
			}
		})
	}
	r := httptest.NewRecorder()
	srv.handleIndex(r, httptest.NewRequest("GET", "/live/unknown", nil))
	if r.Code != 404 {
		t.Fatalf("unexpected live subroute status %d", r.Code)
	}
	initStart := strings.Index(FlowViewHTML, "async function init(){")
	if initStart < 0 {
		t.Fatal("FlowView initializer was not found")
	}
	initEnd := strings.Index(FlowViewHTML[initStart:], "async function loadFlow(")
	if initEnd < 0 {
		t.Fatal("FlowView initializer end was not found")
	}
	initializer := FlowViewHTML[initStart : initStart+initEnd]
	if !strings.Contains(initializer, "initLiveStream()") {
		t.Fatal("FlowView live mode must boot the workspace stream")
	}
	if !strings.Contains(initializer, "'/live'") && !strings.Contains(initializer, "liveParam") {
		t.Fatal("FlowView must gate the stream on live mode instead of always connecting")
	}
	if !strings.Contains(FlowViewHTML, "loadFlow(currentFlowId)") {
		t.Fatal("FlowView must reload the active flow on generation.published with an empty query")
	}
	if !strings.Contains(LiveViewHTML, "new EventSource('/api/workspace/stream") {
		t.Fatal("Live View lost its workspace stream subscription")
	}
}

func TestLiveViewContainsStoryboardAndRadar(t *testing.T) {
	requiredMarkers := []string{
		"id=\"macro-storyboard\"",
		"class=\"storyboard-track\"",
		"id=\"storyboard-status-pill\"",
		"id=\"radar-card\"",
		"id=\"radar-svg\"",
		"id=\"radar-pill\"",
		"function renderStoryboard(",
		"function renderRadar(",
		"function loadImpact(",
		"data-story-step=",
		"data-radar-symbol=",
	}
	for _, marker := range requiredMarkers {
		if !strings.Contains(LiveViewHTML, marker) {
			t.Fatalf("LiveViewHTML missing required Storyboard/Radar marker %q", marker)
		}
	}
}
