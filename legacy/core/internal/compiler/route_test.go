package compiler

import (
	"codeflow/core/internal/codegraph"
	"codeflow/core/internal/entrypoint"
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
const multiRouteSource = "final routes = [\n  GoRoute(path: '/signup', builder: (context, state) => const SignupPage()),\n  GoRoute(path: '/settings', builder: (context, state) => const SettingsPage()),\n];\nclass SignupPage {\n  const SignupPage();\n  void build() { ElevatedButton(onPressed: _submit); }\n  void _submit() { context.go('/welcome'); }\n}\nclass SettingsPage {\n  const SettingsPage();\n  void build() { ElevatedButton(onPressed: _save); }\n  void _save() { context.go('/done'); }\n}\n"

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

func TestCompileStartsPushTokenRegistrationAtVerifiedSystemEvent(t *testing.T) {
	repo, adapter := fixture(t)
	source := "class PushRegistration { void start() { FirebaseMessaging.instance.onTokenRefresh.listen(_registerToken); } void _registerToken(String token) { _persistToken(token); } void _persistToken(String token) { DeviceRepository.register(token); } }\n"
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("git add %s: %v", out, err)
	}
	if out, err := exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "system entry").CombinedOutput(); err != nil {
		t.Fatalf("git commit %s: %v", out, err)
	}
	selector := "system:push-token:lib/signup.dart:PushRegistration:_registerToken"
	doc, problem, err := Compile(context.Background(), Options{Repo: repo, Selector: selector, AdapterCommand: adapter})
	if err != nil || problem != nil {
		t.Fatalf("system entry compilation failed: err=%v problem=%#v", err, problem)
	}
	if doc.Current.ID != selector || len(doc.Current.Steps) != 1 || doc.Current.Steps[0].Actor != "system" || len(doc.Scenarios) != 1 {
		t.Fatalf("push registration was not modeled as a system-rooted scenario: %#v", doc)
	}
	if len(doc.Unknowns) != 1 || doc.Unknowns[0].Reason != "missing_relation" {
		t.Fatalf("unsupported token result must remain explicit rather than guessed: %#v", doc.Unknowns)
	}
	foundHelperBoundary := false
	for _, fact := range doc.Facts {
		if fact.Kind == "repository_access" && strings.Contains(fact.Object, "DeviceRepository.register") {
			foundHelperBoundary = true
		}
	}
	if !foundHelperBoundary {
		t.Fatalf("system entry did not analyze the direct helper body: %#v", doc.Facts)
	}
}

func TestAssembleNeverBorrowsAnUnrelatedRouteTransition(t *testing.T) {
	basis, entry, anchor := assembleFixture()
	routeAnchor := anchor
	routeAnchor.Symbol = "route"
	doc, err := assemble(basis, entry, []semanticFact{
		{kind: "user_action", subject: "SignupPage.submit", proof: "resolved_ast", symbolID: "SignupPage.submit", anchor: anchor, status: flowir.Observed},
		{kind: "route_transition", subject: "OtherPage.close", object: "route:/wrong", proof: "framework_rule_v1", anchor: routeAnchor, status: flowir.Observed},
	}, "owned_dart_structural")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Current.Status != flowir.Unknown || len(doc.Unknowns) != 1 || doc.Unknowns[0].Reason != "missing_relation" {
		t.Fatalf("unrelated transition must stop at an explicit unknown: %#v", doc)
	}
	for _, fact := range doc.Facts {
		if fact.Kind == "visible_result" {
			t.Fatalf("unrelated transition became a visible result: %#v", fact)
		}
	}
}

func TestAssembleUsesOnlyResolvedCallPathToRouteTransition(t *testing.T) {
	basis, entry, anchor := assembleFixture()
	routeAnchor := anchor
	routeAnchor.Symbol = "route"
	doc, err := assemble(basis, entry, []semanticFact{
		{kind: "user_action", subject: "SignupPage.submit", proof: "resolved_ast", symbolID: "SignupPage.submit", anchor: anchor, status: flowir.Observed},
		{kind: "call", subject: "SignupPage.submit", object: "SignupPage.navigate", proof: "resolved_ast", symbolID: "SignupPage.navigate", anchor: anchor, status: flowir.Observed},
		{kind: "route_transition", subject: "OtherPage.close", object: "route:/wrong", proof: "framework_rule_v1", anchor: routeAnchor, status: flowir.Observed},
		{kind: "route_transition", subject: "SignupPage.navigate", object: "route:/welcome", proof: "framework_rule_v1", anchor: routeAnchor, status: flowir.Observed},
	}, "owned_dart_structural")
	if err != nil {
		t.Fatal(err)
	}
	visible := ""
	for _, fact := range doc.Facts {
		if fact.Kind == "visible_result" {
			visible = fact.Object
		}
	}
	if doc.Current.Status != flowir.Observed || visible != "route:/welcome" || len(doc.Current.Steps) != 2 {
		t.Fatalf("resolved call path was not preserved: %#v", doc)
	}
}

func TestAssemblePreservesMultipleActionsWithoutInventingOutcomes(t *testing.T) {
	basis, entry, anchor := assembleFixture()
	second := anchor
	second.ByteRange = []int{20, 30}
	second.LineRange = []int{2, 2}
	second.Fingerprint = "action-b"
	routeAnchor := anchor
	routeAnchor.Symbol = "route"
	secondRouteAnchor := second
	secondRouteAnchor.Symbol = "route"
	doc, err := assemble(basis, entry, []semanticFact{
		{kind: "user_action", subject: "Page.continueAction", proof: "resolved_ast", symbolID: "Page.continueAction", anchor: anchor, status: flowir.Observed},
		{kind: "route_transition", subject: "Page.continueAction", object: "route:/next", proof: "framework_rule_v1", anchor: routeAnchor, status: flowir.Observed},
		{kind: "user_action", subject: "Page.helpAction", proof: "resolved_ast", symbolID: "Page.helpAction", anchor: second, status: flowir.Observed},
		{kind: "route_transition", subject: "UnrelatedPage.close", object: "route:/wrong", proof: "framework_rule_v1", anchor: secondRouteAnchor, status: flowir.Observed},
	}, "owned_dart_structural")
	if err != nil {
		t.Fatal(err)
	}
	visible := map[string]bool{}
	actions := 0
	for _, fact := range doc.Facts {
		if fact.Kind == "visible_result" {
			visible[fact.Object] = true
		}
		if fact.Kind == "user_action" {
			actions++
		}
	}
	if doc.Current.Status != flowir.Mixed || actions != 2 || !visible["route:/next"] || visible["route:/wrong"] || len(doc.Unknowns) != 1 {
		t.Fatalf("multiple actions were dropped or cross-wired: %#v", doc)
	}
}

func assembleFixture() (flowir.Basis, entrypoint.EntryPoint, flowir.Anchor) {
	anchor := flowir.Anchor{Kind: "code", Path: "lib/page.dart", Symbol: "code", LineRange: []int{1, 1}, ByteRange: []int{0, 10}, FileHash: "sha256:page", SpanHash: "sha256:span", Fingerprint: "action-a", Revision: "revision"}
	basis := flowir.Basis{Repository: "fixture", HeadRevision: "revision", WorktreeFingerprint: "sha256:fixture", Manifest: []flowir.ManifestEntry{{Path: "lib/page.dart", Type: "file", Mode: "0644", FileHash: "sha256:page", GitState: "clean"}}}
	entryAnchor := anchor
	entryAnchor.Symbol = "route"
	entryAnchor.Fingerprint = "entry"
	return basis, entrypoint.EntryPoint{FlowID: "route:/page", Anchor: entryAnchor}, anchor
}

func TestCompileKeepsTextualOrUnresolvedCallbackExplicitlyUnknown(t *testing.T) {
	repo, adapter := fixture(t)
	spoof := "final routes = [GoRoute(path: '/signup', builder: (context, state) => const SignupPage())];\nclass SignupPage {\n  void build() { final note = 'onPressed: _submit'; ElevatedButton(onPressed: missingCallback); }\n  void _submit() { context.go('/wrong'); }\n}\n"
	if err := os.WriteFile(filepath.Join(repo, "lib/signup.dart"), []byte(spoof), 0644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", repo, "add", ".").Run()
	exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "unresolved callback").Run()
	s := graph(t, repo, false)
	defer s.Close()
	doc, problem, err := Compile(context.Background(), Options{Repo: repo, Selector: "signup", CodeGraphURL: s.URL, AdapterCommand: adapter})
	if problem != nil || err != nil {
		t.Fatalf("unknown flow must remain reviewable: err=%v problem=%#v", err, problem)
	}
	if doc.Current.Status != flowir.Unknown || len(doc.Unknowns) != 1 || doc.Unknowns[0].Reason != "supported_user_action_missing" {
		t.Fatalf("unresolved callback did not remain explicit unknown: %#v", doc.Current)
	}
	for _, fact := range doc.Facts {
		if fact.Kind == "user_action" && fact.Status == flowir.Observed {
			t.Fatalf("text or unresolved identifier became an observed action: %#v", fact)
		}
	}
}

func TestCompileUsesASTNavigationAndIgnoresRouteText(t *testing.T) {
	repo, adapter := fixture(t)
	source := "final routes = [GoRoute(path: '/signup', builder: (context, state) => const SignupPage())];\nclass SignupPage { void build() { ElevatedButton(onPressed: _submit); } void _submit() { final example = \"context.go('/wrong')\"; /* context.go('/also-wrong'); */ context.go('/right'); } }\n"
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", repo, "add", ".").Run()
	exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "ast navigation").Run()
	s := graph(t, repo, false)
	defer s.Close()
	doc, problem, err := Compile(context.Background(), Options{Repo: repo, Selector: "signup", CodeGraphURL: s.URL, AdapterCommand: adapter})
	if err != nil || problem != nil {
		t.Fatalf("err=%v problem=%#v", err, problem)
	}
	for _, fact := range doc.Facts {
		if fact.Kind == "visible_result" && fact.Object != "route:/right" {
			t.Fatalf("route-looking text became navigation evidence: %#v", fact)
		}
	}
}

func TestCompileRecognizesResolvedTapCallback(t *testing.T) {
	repo, adapter := fixture(t)
	source := "final routes = [GoRoute(path: '/signup', builder: (context, state) => const SignupPage())];\nclass SignupPage { void build() { GestureDetector(onTap: _openHelp); } void _openHelp() { context.push('/help'); } }\n"
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", repo, "add", ".").Run()
	exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "tap action").Run()
	s := graph(t, repo, false)
	defer s.Close()
	doc, problem, err := Compile(context.Background(), Options{Repo: repo, Selector: "signup", CodeGraphURL: s.URL, AdapterCommand: adapter})
	if err != nil || problem != nil {
		t.Fatalf("err=%v problem=%#v", err, problem)
	}
	seenTap, seenHelp := false, false
	for _, fact := range doc.Facts {
		seenTap = seenTap || (fact.Kind == "user_action" && len(fact.Evidence) == 1 && strings.Contains(fact.Evidence[0].Fingerprint, "onTap"))
		seenHelp = seenHelp || (fact.Kind == "visible_result" && fact.Object == "route:/help")
	}
	if !seenTap || !seenHelp || doc.Current.Status != flowir.Observed {
		t.Fatalf("resolved tap callback was not compiled: %#v", doc)
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

func TestCompileResolvedCausalityDoesNotDependOnProductNames(t *testing.T) {
	repo, adapter := fixture(t)
	files := map[string]string{
		"lib/routes.dart":               "import 'registration_page.dart';\nconst registrationPath = '/register';\nfinal routes = [GoRoute(path: registrationPath, builder: (context, state) => const RegistrationPage())];\n",
		"lib/registration_page.dart":    "import 'registration_machine.dart';\nimport 'navigation.dart';\nimport 'screen_routes.dart';\nfinal ref = Ref();\nfinal router = Router();\nfinal dispatcher = NavigationDispatcher();\nclass Ref { void dispatch(Object provider, Object event) {} void listen(Object provider, void Function(RegistrationState?, RegistrationState) callback) {} }\nclass Router { void go(String route) {} }\nclass NavigationDispatcher { void go(Object destination) {} }\nclass RegistrationPage { const RegistrationPage(); void build() { Button(onPressed: _abortRegistration); ref.listen(accountMachine, _observe); } void _abortRegistration() { if (alreadyDone) { dispatcher.go(const DashboardTarget()); return; } if (!confirmed) return; ref.dispatch(accountMachine, const AbortRegistration()); } void _observe(RegistrationState? previous, RegistrationState state) { if (state.aborted) router.go(loginRoute); } }\n",
		"lib/registration_machine.dart": "final accountMachine = Object();\nfinal class AbortRegistration { const AbortRegistration(); }\nfinal class RegistrationState { final bool aborted; const RegistrationState({this.aborted = false}); }\nclass RegistrationMachine { RegistrationState state = const RegistrationState(); void handle(Object event) { switch (event) { case final AbortRegistration event: _applyAbort(event); default: break; } } void _applyAbort(AbortRegistration event) { state = const RegistrationState(aborted: true); } }\n",
		"lib/navigation.dart":           "final class DashboardTarget { const DashboardTarget(); }\n",
		"lib/destination_map.dart":      "import 'navigation.dart';\nimport 'screen_routes.dart';\nString destinationPath(Object destination) => switch (destination) { DashboardTarget() => dashboardRoute, _ => '/fallback', };\n",
		"lib/screen_routes.dart":        "const String dashboardRoute = '/dashboard';\nconst String loginRoute = '/login';\n",
	}
	for name, contents := range files {
		path := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(repo, "lib", "signup.dart")); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "add", "-A").CombinedOutput(); err != nil {
		t.Fatalf("add: %s %v", out, err)
	}
	if out, err := exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "generic causality").CombinedOutput(); err != nil {
		t.Fatalf("commit: %s %v", out, err)
	}
	doc, problem, err := Compile(context.Background(), Options{Repo: repo, Selector: "route:/register", AdapterCommand: adapter})
	if err != nil || problem != nil {
		t.Fatalf("err=%v problem=%#v", err, problem)
	}
	if doc.Current.Status != flowir.Observed || len(doc.Unknowns) != 0 {
		t.Fatalf("generic resolved chain must be complete: status=%s unknowns=%#v", doc.Current.Status, doc.Unknowns)
	}
	want := map[string]bool{
		"event:AbortRegistration":              false,
		"state:RegistrationState.aborted=true": false,
		"route:/dashboard":                     false,
		"route:/login":                         false,
	}
	for _, fact := range doc.Facts {
		if _, ok := want[fact.Object]; ok {
			want[fact.Object] = fact.Status == flowir.Observed && fact.Proof == "resolved_ast"
		}
		if strings.Contains(fact.Subject+fact.Object+fact.SymbolID, "JoinCancel") || strings.Contains(fact.Subject+fact.Object+fact.SymbolID, "HomeDestination") {
			t.Fatalf("product-specific semantic identity leaked: %#v", fact)
		}
	}
	for object, seen := range want {
		if !seen {
			t.Fatalf("missing generic observed fact %s: %#v", object, doc.Facts)
		}
	}
	eventOnly := strings.Replace(files["lib/registration_page.dart"], "if (alreadyDone) { dispatcher.go(const DashboardTarget()); return; } ", "", 1)
	if err := os.WriteFile(filepath.Join(repo, "lib", "registration_page.dart"), []byte(eventOnly), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "add", "-A").CombinedOutput(); err != nil {
		t.Fatalf("add event-only: %s %v", out, err)
	}
	if out, err := exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "event listener only").CombinedOutput(); err != nil {
		t.Fatalf("commit event-only: %s %v", out, err)
	}
	eventDoc, problem, err := Compile(context.Background(), Options{Repo: repo, Selector: "route:/register", AdapterCommand: adapter})
	if err != nil || problem != nil {
		t.Fatalf("event-only err=%v problem=%#v", err, problem)
	}
	if eventDoc.Current.Status != flowir.Observed || len(eventDoc.Unknowns) != 0 || len(eventDoc.Current.Steps) != 7 {
		t.Fatalf("listener-owned navigation timeline is incomplete: status=%s steps=%d unknowns=%#v", eventDoc.Current.Status, len(eventDoc.Current.Steps), eventDoc.Unknowns)
	}
	last := eventDoc.Current.Steps[len(eventDoc.Current.Steps)-1]
	if !strings.Contains(last.BehaviorKey, "route:/login") {
		t.Fatalf("listener-owned route is not the final causal step: %#v", eventDoc.Current.Steps)
	}
}

func TestCompileManySharesExactBasisAndKeepsFlowsIndependent(t *testing.T) {
	repo, adapter := fixture(t)
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), []byte(multiRouteSource), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("add: %s %v", out, err)
	}
	if out, err := exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "multi routes").CombinedOutput(); err != nil {
		t.Fatalf("commit: %s %v", out, err)
	}
	starts := filepath.Join(t.TempDir(), "adapter-starts")
	wrapper := filepath.Join(t.TempDir(), "adapter-wrapper")
	script := fmt.Sprintf("#!/bin/sh\nprintf x >> %q\nexec dart %q \"$@\"\n", starts, strings.TrimPrefix(adapter, "dart "))
	if err := os.WriteFile(wrapper, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	documents, problem, err := CompileMany(context.Background(), Options{Repo: repo, AdapterCommand: wrapper}, []string{"route:/signup", "route:/settings"})
	if err != nil || problem != nil || len(documents) != 2 {
		t.Fatalf("multi compile documents=%d problem=%#v err=%v", len(documents), problem, err)
	}
	if documents[0].Current.ID != "route:/signup" || documents[1].Current.ID != "route:/settings" {
		t.Fatalf("selector order changed: %s %s", documents[0].Current.ID, documents[1].Current.ID)
	}
	if documents[0].Basis.WorktreeFingerprint != documents[1].Basis.WorktreeFingerprint || documents[0].Basis.HeadRevision != documents[1].Basis.HeadRevision {
		t.Fatal("multi-flow documents do not share one exact basis")
	}
	if documents[0].Current.Steps[1].BehaviorKey == documents[1].Current.Steps[1].BehaviorKey {
		t.Fatal("independent flows were flattened into one behavior")
	}
	started, err := os.ReadFile(starts)
	if err != nil || string(started) != "x" {
		t.Fatalf("multi-flow must reuse one adapter process: starts=%q err=%v", started, err)
	}
	if _, problem, _ := CompileMany(context.Background(), Options{}, []string{"route:/signup", "route:/signup"}); problem == nil || problem.Code != "DUPLICATE_SELECTOR" {
		t.Fatalf("duplicate selector problem=%#v", problem)
	}
}

func TestCompileOwnedDartHomeDestinationSeamRequiresCompleteUniqueSlice(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  flowir.Status
	}{
		{name: "complete", files: homeDestinationSeamFiles(), want: flowir.Mixed},
		{name: "missing resolver", files: withoutHomeSeamFile("lib/destination_resolver.dart"), want: flowir.Unknown},
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
	if err := os.WriteFile(filepath.Join(repo, "lib/destination_resolver.dart"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := validateRelationships(basis, rels); err == nil {
		t.Fatal("changed seam source must invalidate its captured graph anchor")
	}
}

func homeDestinationSeamFiles() map[string]string {
	return map[string]string{
		"lib/join_routes.dart":          "import 'join_page.dart';\nconst joinPath = '/join'; final routes = [GoRoute(path: joinPath, builder: (context, state) => const JoinPage())];\n",
		"lib/join_page.dart":            "import 'navigation.dart';\nfinal dispatcher = NavigationDispatcher();\nclass NavigationDispatcher { void go(Object destination) {} }\nclass JoinPage { const JoinPage(); void build() { ElevatedButton(onPressed: _requestExit); } void _requestExit() { if (completed) { dispatcher.go(const DashboardTarget()); } } }\n",
		"lib/navigation.dart":           "final class DashboardTarget { const DashboardTarget(); }\n",
		"lib/destination_resolver.dart": "import 'navigation.dart';\nimport 'screen_routes.dart';\nString resolveDestination(Object destination) => switch (destination) { DashboardTarget() => dashboardPath, _ => '/fallback', };\n",
		"lib/screen_routes.dart":        "const String dashboardPath = '/home';\n",
	}
}
func withoutHomeSeamFile(remove string) map[string]string {
	files := homeDestinationSeamFiles()
	delete(files, remove)
	return files
}
func withAmbiguousResolver() map[string]string {
	files := homeDestinationSeamFiles()
	files["other/destination_resolver.dart"] = files["lib/destination_resolver.dart"]
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
