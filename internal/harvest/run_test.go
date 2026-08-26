package harvest

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"codeflow/internal/contractharness"
)

// moduleRoot locates the repository root relative to this source file,
// independent of the test process working directory.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine source location")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func dartOrSkip(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("dart"); err != nil {
		t.Skipf("dart SDK not found in PATH: %v", err)
	}
}

func adapterSpec(t *testing.T) string {
	t.Helper()
	spec := "dartrun:" + filepath.Join(moduleRoot(t), "adapters", "dart")
	if _, err := ResolveDartAdapter(spec); err != nil {
		t.Skipf("dart adapter not usable: %v", err)
	}
	return spec
}

func newIntegrationRunner(t *testing.T) *Runner {
	t.Helper()
	cfg, err := ResolveDartAdapter(adapterSpec(t))
	if err != nil {
		t.Fatalf("ResolveDartAdapter: %v", err)
	}
	cfg.DefaultTimeout = 120 * time.Second
	r := NewRunner(cfg, 1)
	t.Cleanup(r.Close)
	return r
}

func runCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

// copyExampleApp copies testdata/example_app into a fresh temp dir and
// drops the manifest fixture in (pin+rename submit, exclude toggleDarkMode).
func copyExampleApp(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "shop_app")
	src := filepath.Join(moduleRoot(t), "testdata", "example_app")
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy example_app: %v", err)
	}

	const manifest = `flows:
  - entry: lib/features/auth/email_signup_notifier.dart#EmailSignupNotifier.submit
    name: 회원가입
excluded:
  - lib/features/settings/settings_notifier.dart#SettingsNotifier.toggleDarkMode
`
	if err := os.WriteFile(filepath.Join(dst, ManifestFileName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dst
}

func byEntry(cs []Candidate) map[string]Candidate {
	m := make(map[string]Candidate, len(cs))
	for _, c := range cs {
		m[c.EntrySymbolPath] = c
	}
	return m
}

func TestResolveDartAdapterForms(t *testing.T) {
	t.Run("missing spec is an actionable error", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		_, err := ResolveDartAdapter("")
		if err == nil || !strings.Contains(err.Error(), DartAdapterEnvVar) {
			t.Fatalf("err = %v, want mention of %s", err, DartAdapterEnvVar)
		}
	})
	t.Run("absolute executable path", func(t *testing.T) {
		bin := filepath.Join(t.TempDir(), "fake_adapter.sh")
		if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		cfg, err := ResolveDartAdapter(bin)
		if err != nil {
			t.Fatalf("ResolveDartAdapter: %v", err)
		}
		if cfg.BinPath != bin || len(cfg.Args) != 0 {
			t.Errorf("cfg = %+v, want direct BinPath=%q no args", cfg, bin)
		}
	})
	t.Run("dartrun form resolves dart and the adapter entrypoint", func(t *testing.T) {
		dir := filepath.Join(moduleRoot(t), "adapters", "dart")
		cfg, err := ResolveDartAdapter("dartrun:" + dir)
		if err != nil {
			t.Fatalf("ResolveDartAdapter: %v", err)
		}
		dartBin, _ := exec.LookPath("dart")
		if cfg.BinPath != dartBin {
			t.Errorf("BinPath = %q, want %q", cfg.BinPath, dartBin)
		}
		wantArgs := []string{"run", filepath.Join(dir, "bin", "codeflow_dart_adapter.dart")}
		if len(cfg.Args) != 2 || cfg.Args[0] != wantArgs[0] || cfg.Args[1] != wantArgs[1] {
			t.Errorf("Args = %v, want %v", cfg.Args, wantArgs)
		}
	})
	t.Run("relative paths are rejected", func(t *testing.T) {
		for _, bad := range []string{"adapters/dart", "./x", "somefile"} {
			if _, err := ResolveDartAdapter(bad); err == nil {
				t.Errorf("ResolveDartAdapter(%q) succeeded, want error", bad)
			}
		}
	})
	t.Run("dartrun with missing package dir is rejected", func(t *testing.T) {
		_, err := ResolveDartAdapter("dartrun:/nonexistent/pkg/dir")
		if err == nil || !strings.Contains(err.Error(), "entrypoint") {
			t.Fatalf("err = %v, want missing-entrypoint message", err)
		}
	})
}

func TestRunnerExampleAppEndToEnd(t *testing.T) {
	dartOrSkip(t)
	app := copyExampleApp(t)
	r := newIntegrationRunner(t)

	got, err := r.Run(runCtx(t), app)
	if err != nil {
		t.Fatalf("Run: %v (stderr tail via pool diagnostics unavailable)", err)
	}
	t.Logf("example_app harvest produced %d candidates", len(got))
	for i, c := range got {
		t.Logf("%2d  score=%.3f  fanIn=%d  boundary=%-5v  %-18s %-12s %s  flags=%s/%v",
			i+1, c.Score, c.FanIn, c.BoundaryReachable, c.MarkerKind, c.TriggerClass,
			c.EntrySymbolPath, c.ManifestOverride, deref(c.DedupedInto))
	}
	if len(got) < 8 {
		t.Fatalf("only %d candidates; the fixture must yield at least the 10 known markers minus drift", len(got))
	}
	entries := byEntry(got)

	// 1. EmailSignupNotifier.submit: user_action/notifier_method, scored,
	//    pinned and renamed by the manifest, restored from dedup.
	submitPath := "lib/features/auth/email_signup_notifier.dart#EmailSignupNotifier.submit"
	submit, ok := entries[submitPath]
	if !ok {
		t.Fatalf("%s missing from harvest: %v", submitPath, keysOf(entries))
	}
	if submit.TriggerClass != "user_action" || submit.MarkerKind != "notifier_method" {
		t.Errorf("submit classification = %s/%s, want user_action/notifier_method", submit.TriggerClass, submit.MarkerKind)
	}
	if submit.Score <= 0 || submit.Score > 1 {
		t.Errorf("submit score = %v, want in (0,1]", submit.Score)
	}
	if submit.ManifestOverride != "pinned" {
		t.Errorf("submit manifestOverride = %q, want pinned", submit.ManifestOverride)
	}
	if submit.IntentSignals.DerivedName != "회원가입" {
		t.Errorf("submit derivedName = %q, want manifest name 회원가입", submit.IntentSignals.DerivedName)
	}
	if submit.DedupedInto != nil {
		t.Errorf("submit dedupedInto = %v; pinning forces inclusion over dedup", deref(submit.DedupedInto))
	}

	// 2. Generated files never leak into candidates.
	for _, c := range got {
		if strings.Contains(c.EntrySymbolPath, ".g.dart") || strings.Contains(c.EntrySymbolPath, ".freezed.dart") {
			t.Errorf("generated file leaked into candidates: %s", c.EntrySymbolPath)
		}
		if c.IntentSignals.ClassName == "CartGenMapper" {
			t.Errorf("symbol defined only in cart.g.dart leaked: %+v", c)
		}
	}

	// 3. PlaceOrderUseCase.call is usecase_call — the most specific marker
	//    in the payload; nothing outranks it on specificity.
	ucPath := "lib/features/orders/place_order_usecase.dart#PlaceOrderUseCase.call"
	uc, ok := entries[ucPath]
	if !ok {
		t.Fatalf("%s missing from harvest", ucPath)
	}
	if uc.TriggerClass != "use_case_invocation" || uc.MarkerKind != "usecase_call" {
		t.Errorf("PlaceOrderUseCase.call classification = %s/%s, want use_case_invocation/usecase_call", uc.TriggerClass, uc.MarkerKind)
	}
	for _, c := range got {
		if markerRank(c.MarkerKind) > markerRank(uc.MarkerKind) {
			t.Errorf("%s (%s) outranks usecase_call in specificity", c.EntrySymbolPath, c.MarkerKind)
		}
	}
	if !uc.BoundaryReachable {
		t.Error("place_order_usecase.dart declares OrderRepository; boundaryReachable = false")
	}

	// 4. toggleDarkMode excluded per the manifest fixture.
	togglePath := "lib/features/settings/settings_notifier.dart#SettingsNotifier.toggleDarkMode"
	toggle, ok := entries[togglePath]
	if !ok {
		t.Fatalf("%s missing from harvest", togglePath)
	}
	if toggle.ManifestOverride != "excluded" {
		t.Errorf("toggleDarkMode manifestOverride = %q, want excluded", toggle.ManifestOverride)
	}
	if toggle.MarkerKind != "state_mutation" {
		t.Errorf("toggleDarkMode markerKind = %q, want state_mutation (standalone state transition)", toggle.MarkerKind)
	}

	// 5. Dedup bookkeeping is coherent across the whole payload.
	reps := map[string]bool{}
	for _, c := range got {
		if c.DedupedInto == nil {
			reps[c.CandidateID] = true
		}
	}
	for _, c := range got {
		if c.DedupedInto != nil && !reps[deref(c.DedupedInto)] {
			t.Errorf("%s dedupedInto unknown candidate %v", c.EntrySymbolPath, deref(c.DedupedInto))
		}
	}
	if got[0].DedupedInto != nil {
		t.Errorf("top-ranked row %s is itself deduped; ordering broken", got[0].EntrySymbolPath)
	}

	// 6. Every returned candidate validates against the contract.
	for _, c := range got {
		b, merr := json.Marshal(c)
		if merr != nil {
			t.Fatal(merr)
		}
		if verr := contractharness.Validate(contractharness.BaseURL+"candidate.schema.json", b); verr != nil {
			t.Errorf("candidate %s violates candidate.schema.json: %v", c.EntrySymbolPath, verr)
		}
	}
}

func TestRunnerDeterministicOutputBytes(t *testing.T) {
	dartOrSkip(t)
	app := copyExampleApp(t)
	r := newIntegrationRunner(t)
	ctx := runCtx(t)

	first, err := r.Run(ctx, app)
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	second, err := r.Run(ctx, app) // reuses the pooled warm adapter process
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	b1, err1 := json.Marshal(first)
	b2, err2 := json.Marshal(second)
	if err1 != nil || err2 != nil {
		t.Fatalf("marshal: %v %v", err1, err2)
	}
	if !bytes.Equal(b1, b2) {
		t.Fatalf("two runs differ:\n%s\n--- vs ---\n%s", b1, b2)
	}
	if len(first) != len(second) {
		t.Fatalf("run lengths differ: %d vs %d", len(first), len(second))
	}
}

func buildCodeflowBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "codeflow")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/codeflow")
	cmd.Dir = moduleRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

func TestCLIFlowsCommand(t *testing.T) {
	dartOrSkip(t)
	bin := buildCodeflowBinary(t)
	app := copyExampleApp(t)

	env := append(os.Environ(), "CODEFLOW_ADAPTER_DART_BIN="+adapterSpec(t))

	t.Run("--json emits a schema-valid candidates array", func(t *testing.T) {
		cmd := exec.Command(bin, "flows", "--json", app)
		cmd.Env = env
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("codeflow flows --json: %v\nstdout:\n%s", err, out)
		}
		var cs []Candidate
		if jerr := json.Unmarshal(out, &cs); jerr != nil {
			t.Fatalf("stdout is not a JSON array: %v\n%.400s", jerr, out)
		}
		if len(cs) == 0 {
			t.Fatal("empty candidates array")
		}
		for _, c := range cs {
			b, _ := json.Marshal(c)
			if verr := contractharness.Validate(contractharness.BaseURL+"candidate.schema.json", b); verr != nil {
				t.Errorf("CLI candidate %s violates the contract: %v", c.EntrySymbolPath, verr)
			}
		}
	})

	t.Run("table mode renders ranked rows", func(t *testing.T) {
		cmd := exec.Command(bin, "flows", app)
		cmd.Env = env
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("codeflow flows: %v\nstdout:\n%s", err, out)
		}
		text := string(out)
		for _, want := range []string{"RANK", "SCORE", "CLASS", "DERIVED-NAME", "ENTRY", "FLAGS",
			"email_signup_notifier.dart", "pinned", "deduped", "excluded", "회원가입"} {
			if !strings.Contains(text, want) {
				t.Errorf("table output missing %q:\n%s", want, text)
			}
		}
	})

	t.Run("missing adapter config exits 1 with an actionable message", func(t *testing.T) {
		cmd := exec.Command(bin, "flows", app)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
		out, err := cmd.CombinedOutput()
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 1 {
			t.Fatalf("exit = %v, want 1", err)
		}
		if !strings.Contains(string(out), DartAdapterEnvVar) {
			t.Fatalf("stderr %q does not mention %s", out, DartAdapterEnvVar)
		}
	})
}

func keysOf(m map[string]Candidate) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
