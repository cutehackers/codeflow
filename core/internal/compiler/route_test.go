package compiler

import (
	"codeflow/core/internal/codegraph"
	"codeflow/core/internal/flowir"
	"codeflow/core/internal/manifest"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const routeSource = "final routes = [GoRoute(path: '/signup', builder: (context, state) => const SignupPage())];\nclass SignupPage {\n  void build() { ElevatedButton(onPressed: _submit); }\n  void _submit() { _navigate(); }\n  void _navigate() { context.go('/welcome'); }\n}\n"
const riverpodSource = "final signupProvider = NotifierProvider<SignupNotifier, String>(SignupNotifier.new);\nfinal asyncSignupProvider = AsyncNotifierProvider<AsyncSignupNotifier, String>(AsyncSignupNotifier.new);\nfinal routes = [GoRoute(path: '/signup', builder: (context, state) => const SignupPage())];\nclass SignupPage {\n  void build() { ElevatedButton(onPressed: _submit); }\n  void _submit() { ref.read(signupProvider.notifier).submit(); _navigate(); }\n  void _navigate() { context.go('/welcome'); }\n}\nclass SignupNotifier { void submit() { state = 'submitted'; } }\nclass AsyncSignupNotifier { Future<void> submit() async { state = AsyncLoading(); await load(); state = AsyncData('submitted'); } }\n"
const boundarySource = "final routes = [GoRoute(path: '/signup', builder: (context, state) => const SignupPage())];\nclass SignupPage {\n  void build() { ElevatedButton(onPressed: _submit); }\n  void _submit() { UserRepository.save(); PaymentsApi.charge(); _navigate(); }\n  void _navigate() { context.go('/welcome'); }\n}\n"
const dynamicBranchSource = "final routes = [GoRoute(path: '/signup', builder: (context, state) => const SignupPage())];\nclass SignupPage {\n  void build() { ElevatedButton(onPressed: _submit); }\n  void _submit() { _navigate(); }\n  void _navigate() {\n    if (approved) { context.go('/welcome'); } else { dynamic fallback; fallback.go('/retry'); }\n  }\n}\n"

func fixture(t *testing.T) (string, string) {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), []byte(routeSource), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init %s %v", out, err)
	}
	exec.Command("git", "-C", repo, "add", ".").Run()
	exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "fixture").Run()
	_, file, _, _ := runtime.Caller(0)
	return repo, "dart " + filepath.Join(filepath.Dir(file), "../../../adapters/dart/bin/codeflow-dart-adapter.dart")
}
func graph(t *testing.T, repo string, stale bool) *httptest.Server {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(repo, "lib/signup.dart"))
	hash := flowir.SHA256Bytes(b)
	if stale {
		hash = "sha256:stale"
	}
	head, _ := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	revision := string(head[:len(head)-1])
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/api/v1/tools":
			_, _ = w.Write([]byte(`{"tools":[{"name":"analyze_code_relationships"}]}`))
		case "/api/v1/tools/call":
			_, _ = fmt.Fprintf(w, `{"result":{"relationships":[{"kind":"call","from":{"path":"lib/signup.dart","symbol":"SignupPage._submit","byte_start":0,"byte_end":%d,"file_hash":"%s","revision":"%s"},"to":{"path":"lib/signup.dart","symbol":"SignupPage._navigate","byte_start":0,"byte_end":%d,"file_hash":"%s","revision":"%s"}}]}}`, len(b), hash, revision, len(b), hash, revision)
		default:
			w.WriteHeader(404)
		}
	}))
}
func TestCompileBuildsEvidenceBackedRouteAndRejectsStaleGraph(t *testing.T) {
	repo, adapter := fixture(t)
	s := graph(t, repo, false)
	defer s.Close()
	doc, problem, err := Compile(context.Background(), Options{Repo: repo, Selector: "signup", CodeGraphURL: s.URL, AdapterCommand: adapter})
	if err != nil || problem != nil {
		t.Fatalf("err=%v problem=%#v", err, problem)
	}
	if doc.Current.ID != "route:/signup" || len(doc.Current.Steps) != 2 || doc.Current.Steps[1].ResultFacts == nil || len(doc.Architecture.Components) < 3 {
		t.Fatalf("bad compiled flow %#v", doc)
	}
	if err := flowir.Validate(doc); err != nil {
		t.Fatal(err)
	}
	stale := graph(t, repo, true)
	defer stale.Close()
	_, problem, err = Compile(context.Background(), Options{Repo: repo, Selector: "signup", CodeGraphURL: stale.URL, AdapterCommand: adapter})
	if err != nil || problem == nil || problem.Code != "STALE_GRAPH" {
		t.Fatalf("stale err=%v problem=%#v", err, problem)
	}
}

func TestCompileRejectsTextualOrUnresolvedCallbackAsObserved(t *testing.T) {
	repo, adapter := fixture(t)
	spoof := "final routes = [GoRoute(path: '/signup', builder: (context, state) => const SignupPage())];\nclass SignupPage {\n  void build() { final note = 'onPressed: _submit'; ElevatedButton(onPressed: missingCallback); }\n  void _submit() { context.go('/wrong'); }\n}\n"
	if err := os.WriteFile(filepath.Join(repo, "lib/signup.dart"), []byte(spoof), 0644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", repo, "add", ".").Run()
	exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "unresolved callback").Run()
	s := graph(t, repo, false)
	defer s.Close()
	_, problem, err := Compile(context.Background(), Options{Repo: repo, Selector: "signup", CodeGraphURL: s.URL, AdapterCommand: adapter})
	if problem != nil || err == nil || !strings.Contains(err.Error(), "observed user action") {
		t.Fatalf("text and unresolved identifiers must not become observed actions: err=%v problem=%#v", err, problem)
	}
}

func TestCompileScopesResolvedActionToSelectedRouteOwner(t *testing.T) {
	repo, adapter := fixture(t)
	source := "final routes = [GoRoute(path: '/signup', builder: (context, state) => const SignupPage())];\nclass AUnrelatedPage { void build() { ElevatedButton(onPressed: _wrong); } void _wrong() { context.go('/wrong'); } }\nclass SignupPage { void build() { ElevatedButton(onPressed: _submit); } void _submit() { _navigate(); } void _navigate() { context.go('/welcome'); } }\n"
	if err := os.WriteFile(filepath.Join(repo, "lib/signup.dart"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", repo, "add", ".").Run()
	exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "route owner").Run()
	s := graph(t, repo, false)
	defer s.Close()
	doc, problem, err := Compile(context.Background(), Options{Repo: repo, Selector: "signup", CodeGraphURL: s.URL, AdapterCommand: adapter})
	if err != nil || problem != nil {
		t.Fatalf("err=%v problem=%#v", err, problem)
	}
	for _, fact := range doc.Facts {
		if fact.Kind == "user_action" {
			if fact.Proof != "resolved_ast" || fact.SymbolID == "" || !strings.Contains(fact.SymbolID, "SignupPage") || strings.Contains(fact.SymbolID, "AUnrelatedPage") {
				t.Fatalf("action lacks selected canonical resolved identity: %#v", fact)
			}
		}
		if fact.Object == "route:/wrong" {
			t.Fatalf("unrelated route owner leaked into flow: %#v", fact)
		}
	}
}

func TestCompileKeepsDynamicBranchUnknownWithoutGuessingTarget(t *testing.T) {
	repo, adapter := fixture(t)
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), []byte(dynamicBranchSource), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("git add: %s %v", out, err)
	}
	if out, err := exec.Command("git", "-C", repo, "commit", "-qm", "branch fixture").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s %v", out, err)
	}
	s := graph(t, repo, false)
	defer s.Close()
	doc, problem, err := Compile(context.Background(), Options{Repo: repo, Selector: "signup", CodeGraphURL: s.URL, AdapterCommand: adapter})
	if err != nil || problem != nil {
		t.Fatalf("err=%v problem=%#v", err, problem)
	}
	if doc.Current.Status != flowir.Mixed || len(doc.Current.Steps) != 4 || len(doc.Unknowns) != 1 || doc.Unknowns[0].Reason != "dynamic_dispatch" {
		t.Fatalf("expected observed and unknown branch outcomes, got %#v", doc)
	}
	branch := doc.Current.Steps[1].Branches[0]
	if branch.Status != flowir.Unknown || branch.ID != flowir.BranchID(branch.ConditionFact, []string{doc.Current.Steps[2].BehaviorKey, doc.Current.Steps[3].BehaviorKey}) {
		t.Fatalf("branch must have the deterministic condition/outcome identity: %#v", branch)
	}
	for _, fact := range doc.Facts {
		if fact.Kind == "route_transition" && fact.Object == "route:/retry" {
			t.Fatal("dynamic receiver must not be guessed as a route transition")
		}
	}
	if err := flowir.Validate(doc); err != nil {
		t.Fatal(err)
	}
}

func TestSuppliedJoinRouteStaticallyCompletesHomeStayAndCancelOutcomes(t *testing.T) {
	repo := filepath.Join(os.Getenv("HOME"), "workspace", "sgp-981-app")
	if _, err := os.Stat(repo); err != nil {
		t.Skip("supplied target unavailable")
	}
	_, file, _, _ := runtime.Caller(0)
	adapter := "dart " + filepath.Join(filepath.Dir(file), "../../../adapters/dart/bin/codeflow-dart-adapter.dart")
	doc, problem, err := Compile(context.Background(), Options{Repo: repo, Selector: "route:/join", AdapterCommand: adapter})
	if err != nil || problem != nil {
		t.Fatalf("join must publish a typed flow, err=%v problem=%#v", err, problem)
	}
	if doc.Current.ID != "route:/join" || doc.Current.Status != flowir.Observed || len(doc.Unknowns) != 0 {
		t.Fatalf("expected statically complete current join flow, got %#v", doc)
	}
	seenHome, seenAuth := false, false
	for _, fact := range doc.Facts {
		if fact.Kind == "visible_result" && fact.Object == "route:/home" && fact.Status == flowir.Observed {
			seenHome = true
		}
		if fact.Kind == "visible_result" && fact.Object == "route:/auth" && fact.Status == flowir.Observed {
			seenAuth = true
		}
	}
	if !seenHome || !seenAuth {
		t.Fatalf("expected observed /home seam and confirmed cancel /auth result, got %#v", doc.Facts)
	}
	for _, unknown := range doc.Unknowns {
		if unknown.Reason == "unsupported_riverpod_pattern" {
			t.Fatalf("a plain provider value read must not be presented as an unknown state mutation: %#v", unknown)
		}
	}
	var branches []flowir.Branch
	for i := range doc.Current.Steps {
		branches = append(branches, doc.Current.Steps[i].Branches...)
	}
	if len(branches) != 2 {
		t.Fatalf("expected completed-state and confirmation branches, got %#v", doc.Current.Steps)
	}
	for _, branch := range branches {
		if branch.Status != flowir.Observed || len(branch.OutcomeStepIDs) != 2 {
			t.Fatalf("every source-backed branch must be complete: %#v", branch)
		}
	}
	seenTerminal := false
	for _, fact := range doc.Facts {
		seenTerminal = seenTerminal || (fact.Kind == "terminal_result" && fact.Object == "result:no_navigation" && fact.Status == flowir.Observed)
	}
	if !seenTerminal {
		t.Fatalf("declining confirmation must be an explicit no-navigation result: %#v", doc.Facts)
	}
	chain := []string{"confirmation:", "event:event:JoinCancelEvent", "notifier_state:state:JoinState.isCanceled=true", "listener_condition:state.isCanceled", "route_transition:route:/auth"}
	for _, want := range chain {
		found := false
		for _, step := range doc.Current.Steps {
			found = found || strings.Contains(step.BehaviorKey, want)
		}
		if !found {
			t.Fatalf("missing confirmed cancellation step %q: %#v", want, doc.Current.Steps)
		}
	}
}

func TestCompileOwnedDartHomeDestinationSeamRequiresCompleteUniqueSlice(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  flowir.Status
	}{
		{name: "complete", files: homeDestinationSeamFiles(), want: flowir.Mixed},
		{name: "missing resolver", files: withoutHomeSeamFile("lib/route_destination_resolver.dart"), want: flowir.Unknown},
		{name: "ambiguous resolver", files: withAmbiguousResolver(), want: flowir.Unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, adapter := fixture(t)
			for name, contents := range tc.files {
				path := filepath.Join(repo, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if out, err := exec.Command("git", "-C", repo, "add", ".").CombinedOutput(); err != nil {
				t.Fatalf("add: %s %v", out, err)
			}
			if out, err := exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "home seam").CombinedOutput(); err != nil {
				t.Fatalf("commit: %s %v", out, err)
			}
			doc, problem, err := Compile(context.Background(), Options{Repo: repo, Selector: "route:/join", AdapterCommand: adapter})
			if err != nil || problem != nil {
				t.Fatalf("err=%v problem=%#v", err, problem)
			}
			if doc.Current.Status != tc.want {
				t.Fatalf("status=%s want=%s: %#v", doc.Current.Status, tc.want, doc)
			}
			seenHome := false
			for _, fact := range doc.Facts {
				seenHome = seenHome || fact.Kind == "visible_result" && fact.Object == "route:/home"
			}
			if seenHome != (tc.want == flowir.Mixed) {
				t.Fatalf("home result=%v status=%s", seenHome, doc.Current.Status)
			}
		})
	}
}

func TestOwnedDartSeamAnchorsRejectSourceChangedAfterCapture(t *testing.T) {
	repo, _ := fixture(t)
	for name, contents := range homeDestinationSeamFiles() {
		path := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	exec.Command("git", "-C", repo, "add", ".").Run()
	exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "home seam").Run()
	basis, err := manifest.Capture(repo)
	if err != nil {
		t.Fatal(err)
	}
	rels, err := codegraph.DartStructuralRelationships(repo, "route:/join")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "lib/app_router.dart"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := validateRelationships(basis, rels); err == nil {
		t.Fatal("changed seam source must invalidate its captured graph anchor")
	}
}

func homeDestinationSeamFiles() map[string]string {
	return map[string]string{
		"lib/join_routes.dart":                "const joinPath = '/join'; final routes = [GoRoute(path: joinPath, builder: (context, state) => const JoinPage())];\n",
		"lib/join_page.dart":                  "class JoinPage { void build() { ElevatedButton(onPressed: _requestExit); } void _requestExit() { if (completed) { ref.read(routeDestinationDispatcherProvider).go(const HomeDestination()); } } }\n",
		"lib/app_router.dart":                 "void go(destination) { _router.go(resolveDestination(destination)); }\n",
		"lib/route_destination_resolver.dart": "String resolveDestination(destination) => switch (destination) { HomeDestination() => homePath, };\n",
		"lib/content_routes.dart":             "const String homePath = '/home';\n",
	}
}
func withoutHomeSeamFile(remove string) map[string]string {
	files := homeDestinationSeamFiles()
	delete(files, remove)
	return files
}
func withAmbiguousResolver() map[string]string {
	files := homeDestinationSeamFiles()
	files["other/route_destination_resolver.dart"] = files["lib/route_destination_resolver.dart"]
	return files
}

func TestCompilePreservesSynchronousRiverpodStateChangeAndSourceTrust(t *testing.T) {
	repo, adapter := fixture(t)
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), []byte(riverpodSource), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("add: %s %v", out, err)
	}
	if out, err := exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "riverpod").CombinedOutput(); err != nil {
		t.Fatalf("commit: %s %v", out, err)
	}
	s := graph(t, repo, false)
	defer s.Close()
	doc, problem, err := Compile(context.Background(), Options{Repo: repo, Selector: "signup", CodeGraphURL: s.URL, AdapterCommand: adapter})
	if err != nil || problem != nil {
		t.Fatalf("err=%v problem=%#v", err, problem)
	}
	var transition *flowir.Fact
	for i := range doc.Facts {
		if doc.Facts[i].Kind == "state_transition" {
			transition = &doc.Facts[i]
		}
	}
	if transition == nil || transition.Status != flowir.Observed || len(transition.Evidence) != 1 || transition.Evidence[0].Kind == "session" {
		t.Fatalf("state fact was not current code evidence: %#v", transition)
	}
	if len(doc.Current.Steps) != 3 || doc.Current.Steps[1].ResultFacts[0] != transition.ID || doc.Current.Steps[1].Status != flowir.Observed {
		t.Fatalf("state transition missing from timeline: %#v", doc.Current.Steps)
	}
	if err := flowir.Validate(doc); err != nil {
		t.Fatal(err)
	}
}

func TestCompileOrdersDirectAsyncNotifierTransitions(t *testing.T) {
	repo, adapter := fixture(t)
	async := strings.Replace(riverpodSource, "signupProvider.notifier).submit", "asyncSignupProvider.notifier).submit", 1)
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), []byte(async), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("add: %s %v", out, err)
	}
	if out, err := exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "async riverpod").CombinedOutput(); err != nil {
		t.Fatalf("commit: %s %v", out, err)
	}
	s := graph(t, repo, false)
	defer s.Close()
	doc, problem, err := Compile(context.Background(), Options{Repo: repo, Selector: "signup", CodeGraphURL: s.URL, AdapterCommand: adapter})
	if err != nil || problem != nil {
		t.Fatalf("err=%v problem=%#v", err, problem)
	}
	count := 0
	for _, fact := range doc.Facts {
		if fact.Kind == "state_transition" {
			count++
		}
	}
	if count != 2 || len(doc.Current.Steps) != 3 {
		t.Fatalf("expected direct async loading/data facts and one causal step: facts=%#v steps=%#v", doc.Facts, doc.Current.Steps)
	}
}

func TestSessionTextCannotCreateOrUpgradeStateTransition(t *testing.T) {
	basis := flowir.Basis{Repository: "fixture", WorktreeFingerprint: "sha256:fixture", Manifest: []flowir.ManifestEntry{{Path: "lib/x.dart", Type: "file", Mode: "0644", FileHash: "sha256:code", GitState: "clean"}}}
	anchor := flowir.Anchor{Kind: "code", Path: "lib/x.dart", Symbol: "code", FileHash: "sha256:code", Revision: "current"}
	entry := flowir.Fact{ID: "entry", Kind: "entry_point", Subject: "route:/x", Status: flowir.Observed, Evidence: []flowir.Anchor{anchor}}
	state := flowir.Fact{ID: "state", Kind: "state_transition", Subject: "Notifier.submit", Object: "state:done", Status: flowir.Observed, Evidence: []flowir.Anchor{{Kind: "session", Path: "chat.json", FileHash: "session-text"}}}
	doc := flowir.Document{SchemaVersion: flowir.SchemaVersion, Basis: basis, Facts: []flowir.Fact{entry, state}, Current: flowir.Flow{ID: "route:/x", FlowKey: "route:/x", EntryPointFact: entry.ID, Steps: []flowir.Step{{ID: "s", BehaviorKey: "x", Order: 1, Actor: "system", TriggerFact: entry.ID, ResultFacts: []string{state.ID}, PrimaryEvidence: []flowir.Anchor{anchor}, Status: flowir.Observed}}}}
	if err := flowir.Validate(doc); err == nil {
		t.Fatal("session text must not create an observed state-transition fact")
	}
}

func TestUnsupportedRiverpodPatternRemainsTimelineUnknown(t *testing.T) {
	repo, adapter := fixture(t)
	unsupported := strings.Replace(riverpodSource, "signupProvider.notifier", "missingProvider.notifier", 1)
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), []byte(unsupported), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("add: %s %v", out, err)
	}
	if out, err := exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "unsupported riverpod").CombinedOutput(); err != nil {
		t.Fatalf("commit: %s %v", out, err)
	}
	s := graph(t, repo, false)
	defer s.Close()
	doc, problem, err := Compile(context.Background(), Options{Repo: repo, Selector: "signup", CodeGraphURL: s.URL, AdapterCommand: adapter})
	if err != nil || problem != nil {
		t.Fatalf("err=%v problem=%#v", err, problem)
	}
	if doc.Current.Status != flowir.Mixed || len(doc.Unknowns) == 0 || len(doc.Current.Steps) != 3 || doc.Current.Steps[1].Status != flowir.Unknown {
		t.Fatalf("unsupported Riverpod was not preserved as a timeline unknown: %#v", doc)
	}
}

func TestPlainProviderValueReadIsDependencyNotUnknownMutation(t *testing.T) {
	repo, adapter := fixture(t)
	plainRead := strings.Replace(riverpodSource, "ref.read(signupProvider.notifier).submit();", "ref.read(signupProvider);", 1)
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), []byte(plainRead), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("add: %s %v", out, err)
	}
	if out, err := exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "plain provider read").CombinedOutput(); err != nil {
		t.Fatalf("commit: %s %v", out, err)
	}
	s := graph(t, repo, false)
	defer s.Close()
	doc, problem, err := Compile(context.Background(), Options{Repo: repo, Selector: "signup", CodeGraphURL: s.URL, AdapterCommand: adapter})
	if err != nil || problem != nil {
		t.Fatalf("err=%v problem=%#v", err, problem)
	}
	for _, unknown := range doc.Unknowns {
		if unknown.Reason == "unsupported_riverpod_pattern" {
			t.Fatalf("plain provider read became a fake mutation debt: %#v", doc)
		}
	}
}

func TestCompileTracksRepositoryAndContractBackedExternalBoundary(t *testing.T) {
	repo, adapter := fixture(t)
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), []byte(boundarySource), 0644); err != nil {
		t.Fatal(err)
	}
	contract := []byte(`{"version":"1","external":{"PaymentsApi.charge":{"result":"charge accepted"}}}`)
	if err := os.WriteFile(filepath.Join(repo, "codeflow.external-contracts.json"), contract, 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("add: %s %v", out, err)
	}
	if out, err := exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "boundaries").CombinedOutput(); err != nil {
		t.Fatalf("commit: %s %v", out, err)
	}
	s := graph(t, repo, false)
	defer s.Close()
	doc, problem, err := Compile(context.Background(), Options{Repo: repo, Selector: "signup", CodeGraphURL: s.URL, AdapterCommand: adapter})
	if err != nil || problem != nil {
		t.Fatalf("err=%v problem=%#v", err, problem)
	}
	seen := map[string]*flowir.Fact{}
	for i := range doc.Facts {
		seen[doc.Facts[i].Kind] = &doc.Facts[i]
	}
	if seen["repository_access"] == nil || seen["external_call"] == nil || seen["external_result"] == nil || seen["external_result"].Evidence[0].Path != "codeflow.external-contracts.json" {
		t.Fatalf("missing current boundary facts: %#v", doc.Facts)
	}
	if doc.Current.Status != flowir.Observed || len(doc.Current.Steps) != 4 || doc.Current.Steps[1].BehaviorFacts[0] != seen["repository_access"].ID || doc.Current.Steps[2].ResultFacts[0] != seen["external_result"].ID {
		t.Fatalf("boundary timeline is not causal: %#v", doc.Current.Steps)
	}
	if !contains(doc.Architecture.Boundaries, "data") || !contains(doc.Architecture.Boundaries, "external") || !contains(doc.Architecture.Relations, "external_contract_result") {
		t.Fatalf("architecture omits boundaries: %#v", doc.Architecture)
	}
}

func TestCompileStopsAtMissingExternalContract(t *testing.T) {
	repo, adapter := fixture(t)
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), []byte(boundarySource), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("add: %s %v", out, err)
	}
	if out, err := exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "missing contract").CombinedOutput(); err != nil {
		t.Fatalf("commit: %s %v", out, err)
	}
	s := graph(t, repo, false)
	defer s.Close()
	doc, problem, err := Compile(context.Background(), Options{Repo: repo, Selector: "signup", CodeGraphURL: s.URL, AdapterCommand: adapter})
	if err != nil || problem != nil {
		t.Fatalf("err=%v problem=%#v", err, problem)
	}
	if doc.Current.Status != flowir.Mixed || len(doc.Unknowns) != 1 || doc.Unknowns[0].Reason != "EXTERNAL_BOUNDARY_UNKNOWN" || doc.Current.Steps[2].Status != flowir.Unknown {
		t.Fatalf("missing contract must stop at explicit unknown: %#v", doc)
	}
}

func TestCompileProjectsExplicitCausalEdgesAndActionableDebt(t *testing.T) {
	repo, adapter := fixture(t)
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), []byte(dynamicBranchSource), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("add: %s %v", out, err)
	}
	if out, err := exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "causal debt").CombinedOutput(); err != nil {
		t.Fatalf("commit: %s %v", out, err)
	}
	s := graph(t, repo, false)
	defer s.Close()
	doc, problem, err := Compile(context.Background(), Options{Repo: repo, Selector: "signup", CodeGraphURL: s.URL, AdapterCommand: adapter})
	if err != nil || problem != nil {
		t.Fatalf("compile=%v problem=%#v", err, problem)
	}
	if len(doc.CausalEdges) == 0 {
		t.Fatal("timeline must project evidence-backed causal edges")
	}
	if len(doc.Unknowns) != 1 || doc.Unknowns[0].DebtState != "open" || len(doc.Unknowns[0].ResolutionCriteria) == 0 || len(doc.Unknowns[0].RelatedEdges) == 0 {
		t.Fatalf("unknown must become actionable cognitive debt: %#v", doc.Unknowns)
	}
	if err := flowir.Validate(doc); err != nil {
		t.Fatalf("causal document invalid: %v", err)
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
