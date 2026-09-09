package flowview

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestLiveViewMatchesDesignatedTemplate(t *testing.T) {
	template, err := os.ReadFile("../../docs/samples/" + LiveViewTemplate)
	if err != nil {
		t.Fatal(err)
	}
	const marker = "// SAMPLE_BOOTSTRAP\n"
	if strings.Count(string(template), marker) != 1 {
		t.Fatal("template must have exactly one sample bootstrap")
	}
	renderer, _, _ := strings.Cut(string(template), marker)
	want := renderer + "// LIVE_BOOTSTRAP\nboot();\n</script>\n</body>\n</html>\n"
	if LiveViewHTML != want {
		t.Fatal("Live view differs from the designated template: run go generate ./internal/flowview")
	}
	if LiveSemanticHTML != LiveViewHTML {
		t.Fatal("LiveSemanticHTML alias must match LiveViewHTML")
	}
	if IndexHTML != FlowViewHTML {
		t.Fatal("IndexHTML alias must match FlowViewHTML")
	}
}

func TestFlowViewAndLiveViewsAreSeparate(t *testing.T) {
	srv := &Server{}
	for _, tc := range []struct{ path, want string }{
		{"/", FlowViewHTML},
		{"/?token=test&request=checkout", FlowViewHTML},
		{"/?live=0", FlowViewHTML},
		{"/live?request=checkout", LiveViewHTML},
		{"/?live=1&request=checkout", LiveViewHTML},
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
	if strings.Contains(initializer, "initLiveStream()") || strings.Contains(initializer, "loadWorkspaceActivity()") || strings.Contains(FlowViewHTML, `id="workspace-activity-badge"`) {
		t.Fatal("FlowView still starts or displays workspace change state")
	}
	if !strings.Contains(LiveViewHTML, "new EventSource('/api/workspace/stream") {
		t.Fatal("Live View lost its workspace stream subscription")
	}
}
