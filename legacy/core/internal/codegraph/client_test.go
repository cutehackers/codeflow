package codegraph

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"codeflow/core/internal/flowir"
)

func TestRelationshipsUsesPublicToolBridgeAndUnwrapsResult(t *testing.T) {
	var called bool
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/api/v1/tools":
			_, _ = w.Write([]byte(`{"tools":[{"name":"analyze_code_relationships"}]}`))
		case "/api/v1/tools/call":
			called = true
			var req struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if json.NewDecoder(r.Body).Decode(&req) != nil || req.Name != "analyze_code_relationships" || req.Arguments["entry_point"] != "route:/signup" {
				t.Errorf("bad tool request: %#v", req)
				w.WriteHeader(400)
				return
			}
			_, _ = w.Write([]byte(`{"result":{"relationships":[{"kind":"call","from":{"path":"lib/a.dart","symbol":"A.a","byte_start":1,"byte_end":2,"file_hash":"sha256:x","revision":"abc"},"to":{"path":"lib/a.dart","symbol":"A.b","byte_start":3,"byte_end":4,"file_hash":"sha256:x","revision":"abc"}}]}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer s.Close()
	rels, err := New(s.URL).Relationships(context.Background(), "/repo", "route:/signup")
	if err != nil || !called || len(rels) != 1 || rels[0].To.Symbol != "A.b" {
		t.Fatalf("rels=%#v err=%v called=%v", rels, err, called)
	}
}

func TestCodeGraphContextSchemaUsesQueryTargetAndReanchorsCurrentSource(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	source := []byte("void submit() { navigate(); }\n")
	if err := os.WriteFile(filepath.Join(repo, "lib/a.dart"), source, 0644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "init", "-q", repo).Run()
	exec.Command("git", "-C", repo, "add", ".").Run()
	exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "fixture").Run()
	called := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.Write([]byte(`{"status":"ok"}`))
		case "/api/v1/tools":
			w.Write([]byte(`{"tools":[{"name":"analyze_code_relationships","inputSchema":{"properties":{"query_type":{"type":"string"},"target":{"type":"string"}}}}]}`))
		case "/api/v1/tools/call":
			var req struct {
				Arguments map[string]any `json:"arguments"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			called = req.Arguments["query_type"] == "find_all_callees" && req.Arguments["target"] == "/signup"
			fmt.Fprintf(w, `{"result":{"relationships":[{"kind":"call","from":{"path":"lib/a.dart","symbol":"submit","byte_start":0,"byte_end":%d},"to":{"path":"lib/a.dart","symbol":"navigate","byte_start":0,"byte_end":%d}}]}}`, len(source), len(source))
		default:
			w.WriteHeader(404)
		}
	}))
	defer s.Close()
	rels, err := New(s.URL).Relationships(context.Background(), repo, "route:/signup")
	if err != nil || !called || len(rels) != 1 || rels[0].From.FileHash != flowir.SHA256Bytes(source) || rels[0].From.Revision == "" {
		t.Fatalf("rels=%#v err=%v called=%v", rels, err, called)
	}
}
func TestCodeGraphContextRejectsUnanchoredResult(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.Write([]byte(`{}`))
		case "/api/v1/tools":
			w.Write([]byte(`{"tools":[{"name":"analyze_code_relationships","inputSchema":{"properties":{"query_type":{},"target":{}}}}]}`))
		case "/api/v1/tools/call":
			w.Write([]byte(`{"relationships":[{"kind":"call","from":{"symbol":"x"},"to":{"symbol":"y"}}]}`))
		}
	}))
	defer s.Close()
	_, err := New(s.URL).Relationships(context.Background(), t.TempDir(), "route:/signup")
	if f, ok := err.(*Failure); !ok || f.Code != "CODEGRAPH_UNANCHORED" {
		t.Fatalf("%T %v", err, err)
	}
}
func TestCodeGraphContextIndexesAndPollsWhenPublicJobToolsAreAvailable(t *testing.T) {
	var calls []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.Write([]byte(`{}`))
		case "/api/v1/tools":
			w.Write([]byte(`{"tools":[{"name":"analyze_code_relationships","inputSchema":{"properties":{"query_type":{},"target":{}}}},{"name":"add_code_to_graph"},{"name":"check_job_status"},{"name":"list_indexed_repositories"}]}`))
		case "/api/v1/tools/call":
			var req struct {
				Name string `json:"name"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			calls = append(calls, req.Name)
			switch req.Name {
			case "list_indexed_repositories":
				w.Write([]byte(`{"result":[]}`))
			case "add_code_to_graph":
				w.Write([]byte(`{"result":{"job_id":"j"}}`))
			case "check_job_status":
				w.Write([]byte(`{"result":{"status":"completed"}}`))
			case "analyze_code_relationships":
				w.Write([]byte(`{"relationships":[]}`))
			}
		}
	}))
	defer s.Close()
	_, err := New(s.URL).Relationships(context.Background(), t.TempDir(), "route:/signup")
	if f, ok := err.(*Failure); !ok || f.Code != "CODEGRAPH_UNKNOWN" {
		t.Fatalf("%T %v", err, err)
	}
	if strings.Join(calls, ",") != "list_indexed_repositories,add_code_to_graph,check_job_status,analyze_code_relationships" {
		t.Fatalf("calls=%v", calls)
	}
}
func TestDartStructuralBridgeResolvesConstRouteAndRejectsDynamic(t *testing.T) {
	repo := t.TempDir()
	os.MkdirAll(filepath.Join(repo, "lib"), 0755)
	source := []byte("const String joinPath = '/join';\nfinal routes=[GoRoute(path: joinPath, builder: (c,s) => const JoinPage())];\nclass JoinPage {}\n")
	os.WriteFile(filepath.Join(repo, "lib/routes.dart"), source, 0644)
	exec.Command("git", "init", "-q", repo).Run()
	exec.Command("git", "-C", repo, "add", ".").Run()
	exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "fixture").Run()
	rels, err := DartStructuralRelationships(repo, "route:/join")
	if err != nil || len(rels) != 1 || rels[0].From.FileHash != flowir.SHA256Bytes(source) || rels[0].To.ByteEnd == 0 {
		t.Fatalf("rels=%#v err=%v", rels, err)
	}
	if _, err := DartStructuralRelationships(repo, "route:/computed"); err == nil {
		t.Fatal("unknown route must not be invented")
	}
}

func TestDartStructuralBridgeSupportsTypedAndPageBuilderRoutes(t *testing.T) {
	for _, test := range []struct {
		name   string
		flowID string
		source string
		page   string
	}{
		{
			name:   "typed route",
			flowID: "route:/home",
			source: "const homePath = '/home';\n@TypedGoRoute<HomeRoute>(path: homePath)\nclass HomeRoute extends GoRouteData { const HomeRoute(); Widget build(BuildContext c, GoRouterState s) => const HomePage(); }\n",
			page:   "class HomePage { const HomePage(); }\n",
		},
		{
			name:   "page builder child",
			flowID: "route:/auth",
			source: "final route = GoRoute(path: '/auth', pageBuilder: (c, s) => NoTransitionPage(child: const AuthEntryPage()));\n",
			page:   "class AuthEntryPage { const AuthEntryPage(); }\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(repo, "lib", "routes.dart"), []byte(test.source), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(repo, "lib", "page.dart"), []byte(test.page), 0644); err != nil {
				t.Fatal(err)
			}
			_ = exec.Command("git", "init", "-q", repo).Run()
			_ = exec.Command("git", "-C", repo, "add", ".").Run()
			_ = exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "fixture").Run()
			rels, err := DartStructuralRelationships(repo, test.flowID)
			if err != nil || len(rels) != 1 || rels[0].To.Path != "lib/page.dart" || rels[0].From.Revision == "" {
				t.Fatalf("relationships=%#v err=%v", rels, err)
			}
		})
	}
}

func TestDirectEventControllerRequiresOneMatchingProviderAndEventCase(t *testing.T) {
	page := []byte("void x() { ref.dispatch(accountMachine, const AbortRegistration()); }")
	valid := []byte("final accountMachine = x; void handle() { case final AbortRegistration e:; }")
	if path, _ := directEventController(map[string][]byte{"page.dart": page, "controller.dart": valid}, page); path != "controller.dart" {
		t.Fatalf("unique direct controller=%q", path)
	}
	if path, _ := directEventController(map[string][]byte{"page.dart": page, "a.dart": valid, "b.dart": valid}, page); path != "" {
		t.Fatalf("ambiguous controller must fail closed: %q", path)
	}
	if path, _ := directEventController(map[string][]byte{"page.dart": page, "controller.dart": []byte("final accountMachine = x;")}, page); path != "" {
		t.Fatalf("missing event case must fail closed: %q", path)
	}
}

func TestSuppliedTargetHasCurrentConstJoinRouteSlice(t *testing.T) {
	target := filepath.Join(os.Getenv("HOME"), "workspace", "sgp-981-app")
	if _, err := os.Stat(target); err != nil {
		t.Skip("supplied target unavailable")
	}
	rels, err := DartStructuralRelationships(target, "route:/join")
	if err != nil || len(rels) != 5 || rels[0].From.FileHash == "" || rels[0].From.Revision == "" {
		t.Fatalf("target route slice rels=%#v err=%v", rels, err)
	}
	for _, rel := range rels[1:] {
		if rel.To.FileHash == "" || rel.To.Revision != rels[0].From.Revision {
			t.Fatalf("seam relationship lacks current source anchor: %#v", rel)
		}
	}
}
func TestRelationshipsRejectsMissingTool(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_, _ = w.Write([]byte(`{"tools":[]}`))
	}))
	defer s.Close()
	_, err := New(s.URL).Relationships(context.Background(), "/repo", "route:/x")
	f, ok := err.(*Failure)
	if !ok || f.Code != "CODEGRAPH_INCOMPATIBLE" {
		t.Fatalf("%T %v", err, err)
	}
}
