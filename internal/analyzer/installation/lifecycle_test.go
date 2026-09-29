// End-to-end guarantee for the one-shot installer and uninstaller:
// scripts/install.sh must produce a working owned installation inside an
// isolated HOME, and `codeflow uninstall` must remove exactly what it owns.
package installation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const stubCodex = `#!/usr/bin/env bash
set -u
STATE_FILE="${CODEFLOW_STUB_STATE:?}"
touch "$STATE_FILE"
case "$1 $2" in
"mcp get")
  name="$3"
  line="$(grep -F "$name"$(printf '\t') "$STATE_FILE" | head -1)"
  if [ -z "$line" ]; then
    echo "error: no mcp server named $name" >&2
    exit 1
  fi
  spec="${line#*$(printf '\t')}"
  printf '{"name":"%s","command":"%s"}\n' "$name" "$spec"
  ;;
"mcp add")
  name="$3"; shift 3
  envspec=""
  while [ "${1:-}" = "--env" ]; do envspec="$envspec$2 "; shift 2; done
  [ "${1:-}" = "--" ] && shift
  printf '%s\t%s%s\n' "$name" "$envspec" "$*" >> "$STATE_FILE"
  ;;
"mcp remove")
  name="$3"
  grep -Fv "$name"$(printf '\t') "$STATE_FILE" > "$STATE_FILE.tmp" || true
  mv "$STATE_FILE.tmp" "$STATE_FILE"
  ;;
*)
  echo "stub: unsupported: $*" >&2
  exit 64
  ;;
esac
`

func requireLifecycleTools(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("bash-based installer lifecycle test")
	}
	for _, tool := range []string{"go", "dart", "node", "make", "shasum", "cp", "tar"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is required for the installer lifecycle test", tool)
		}
	}
}

var (
	prebuiltPreparationOnce   sync.Once
	prebuiltCodeflowBinary    string
	prebuiltDartAdapterBinary string
	prebuiltPreparationError  error
)

func ensurePrebuiltTestBinaries(t *testing.T) (string, string) {
	t.Helper()
	prebuiltPreparationOnce.Do(func() {
		repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
		if err != nil {
			prebuiltPreparationError = err
			return
		}
		tempDir, err := os.MkdirTemp("", "codeflow-lifecycle-prebuilt-*")
		if err != nil {
			prebuiltPreparationError = err
			return
		}

		codeflowBin := filepath.Join(tempDir, "codeflow")
		buildDate := time.Now().UTC().Format(time.RFC3339)
		cmd := exec.Command("go", "build", "-ldflags", "-X main.version=v0.4.0 -X main.date="+buildDate, "-o", codeflowBin, "./cmd/codeflow")
		cmd.Dir = repoRoot
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			prebuiltPreparationError = fmt.Errorf("prebuild codeflow: %w\n%s", err, out)
			return
		}

		dartBin := filepath.Join(tempDir, "dart-adapter")
		dartSrc := filepath.Join(repoRoot, "adapters", "dart")
		dartCmd := exec.Command("dart", "compile", "exe", "bin/codeflow_dart_adapter.dart", "-o", dartBin)
		dartCmd.Dir = dartSrc
		if out, err := dartCmd.CombinedOutput(); err != nil {
			prebuiltPreparationError = fmt.Errorf("prebuild dart adapter: %w\n%s", err, out)
			return
		}

		prebuiltCodeflowBinary = codeflowBin
		prebuiltDartAdapterBinary = dartBin
	})

	if prebuiltPreparationError != nil {
		t.Fatalf("ensure prebuilt test binaries failed: %v", prebuiltPreparationError)
	}
	return prebuiltCodeflowBinary, prebuiltDartAdapterBinary
}

type sandbox struct {
	root       string
	home       string
	installDir string
	skillDest  string
	binary     string
	mcpState   string
	env        []string
}

func newSandbox(t *testing.T) *sandbox {
	t.Helper()
	requireLifecycleTools(t)
	cfBin, dtBin := ensurePrebuiltTestBinaries(t)
	// The installer runs `go build`, whose module/build caches contain
	// read-only directories that would break t.TempDir() cleanup if placed
	// inside the sandbox. Keep them in the developer's real cache locations.
	origHome := os.Getenv("HOME")
	root := t.TempDir()
	s := &sandbox{
		root:       root,
		home:       filepath.Join(root, "home"),
		installDir: filepath.Join(root, "install"),
		mcpState:   filepath.Join(root, "mcp-state.txt"),
	}
	s.binary = filepath.Join(s.installDir, "codeflow")
	s.skillDest = filepath.Join(s.home, ".codex", "skills", "codeflow")
	if err := os.MkdirAll(s.home, 0o755); err != nil {
		t.Fatal(err)
	}

	stubDir := filepath.Join(root, "stub")
	if err := os.MkdirAll(stubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stub := filepath.Join(stubDir, "codex")
	if err := os.WriteFile(stub, []byte(stubCodex), 0o755); err != nil {
		t.Fatal(err)
	}

	path := stubDir + string(os.PathListSeparator) + os.Getenv("PATH")
	s.env = append(os.Environ(),
		"HOME="+s.home,
		"CODEX_HOME="+filepath.Join(s.home, ".codex"),
		"INSTALL_DIR="+s.installDir,
		"CODEFLOW_STUB_STATE="+s.mcpState,
		"CODEFLOW_TEST_CODEFLOW_BIN="+cfBin,
		"CODEFLOW_TEST_DART_BIN="+dtBin,
		"PATH="+path,
		"GOPATH="+filepath.Join(origHome, "go"),
		"GOMODCACHE="+filepath.Join(origHome, "go", "pkg", "mod"),
		"GOCACHE="+filepath.Join(origHome, ".cache", "go-build"),
	)
	return s
}

func (s *sandbox) run(t *testing.T, name string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Env = s.env
	cmd.Dir = s.home
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (s *sandbox) mcpLine() string {
	b, err := os.ReadFile(s.mcpState)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "codeflow\t") {
			return line
		}
	}
	return ""
}

// installFromCheckout runs the real one-shot installer against this checkout.
func (s *sandbox) installFromCheckout(t *testing.T) {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("..", "..", "..", "scripts", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.run(t, "bash", script)
	if err != nil {
		t.Fatalf("install.sh failed: %v\n%s", err, out)
	}
}

func assertInstalled(t *testing.T, s *sandbox) {
	t.Helper()
	if _, err := os.Stat(s.binary); err != nil {
		t.Fatalf("installed binary missing: %v", err)
	}
	goAdapter := filepath.Join(s.installDir, "codeflow_go_adapter")
	if _, err := os.Stat(goAdapter); err != nil {
		t.Fatalf("installed Go adapter missing: %v", err)
	}
	if output, err := s.run(t, s.binary, "install-check-adapter", "--language", "go", "--adapter-spec", goAdapter); err != nil {
		t.Fatalf("installed Go adapter protocol failed: %v\n%s", err, output)
	}
	typeScriptAdapter := filepath.Join(s.installDir, "codeflow_ts_adapter")
	if output, err := s.run(t, s.binary, "install-check-adapter", "--language", "typescript", "--adapter-spec", typeScriptAdapter); err != nil {
		t.Fatalf("installed TypeScript adapter protocol failed: %v\n%s", err, output)
	}
	versionOutput, err := s.run(t, s.binary, "version")
	if err != nil {
		t.Fatalf("installed binary version failed: %v\n%s", err, versionOutput)
	}
	if !strings.Contains(versionOutput, "v0.4.0") {
		t.Fatalf("installed binary has wrong version: %s", versionOutput)
	}
	if _, err := os.Stat(filepath.Join(s.skillDest, "SKILL.md")); err != nil {
		t.Fatalf("installed skill missing: %v", err)
	}
	line := s.mcpLine()
	if !strings.Contains(line, s.binary) {
		t.Fatalf("MCP registration not recorded with installed binary: %q", line)
	}
	record := filepath.Join(s.home, ".codeflow", "install-state.json")
	b, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("installation ownership record missing: %v", err)
	}
	for _, want := range []string{s.binary, s.skillDest} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("ownership record does not reference %s:\n%s", want, b)
		}
	}
	assertInstalledCodeReadingTools(t, s)
}

func assertInstalledCodeReadingTools(t *testing.T, s *sandbox) {
	t.Helper()
	toolDir := filepath.Join(s.home, ".gemini", "antigravity-cli", "mcp", "codeflow")
	entries, err := os.ReadDir(toolDir)
	if err != nil {
		t.Fatalf("installed MCP tool schemas missing: %v", err)
	}
	var toolCount int
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".json" {
			toolCount++
		}
	}
	if toolCount != 9 {
		t.Fatalf("installed MCP tool schema count = %d, want 9", toolCount)
	}
	for _, tc := range []struct {
		name       string
		properties []string
	}{
		{name: "analyze_flow", properties: []string{"request", "candidateId"}},
		{name: "get_flow_payload", properties: []string{"viewId", "compact"}},
		{name: "open_review", properties: []string{"viewId", "focusSymbol", "focusReason"}},
		{name: "curate_code_reading_path", properties: []string{"viewId", "selections", "relations"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(toolDir, tc.name+".json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("installed tool schema missing: %v", err)
			}
			var schema struct {
				Name       string `json:"name"`
				Parameters struct {
					Properties map[string]json.RawMessage `json:"properties"`
				} `json:"parameters"`
			}
			if err := json.Unmarshal(data, &schema); err != nil {
				t.Fatalf("invalid installed tool schema: %v", err)
			}
			if schema.Name != tc.name {
				t.Fatalf("installed tool name = %q, want %q", schema.Name, tc.name)
			}
			for _, property := range tc.properties {
				if _, ok := schema.Parameters.Properties[property]; !ok {
					t.Errorf("installed tool %s has no %s parameter", tc.name, property)
				}
			}
		})
	}
}

func TestInstallThenUninstallLifecycle(t *testing.T) {
	s := newSandbox(t)
	s.installFromCheckout(t)
	assertInstalled(t, s)

	out, err := s.run(t, s.binary, "uninstall")
	if err != nil {
		t.Fatalf("uninstall failed: %v\n%s", err, out)
	}
	if _, serr := os.Stat(s.binary); !os.IsNotExist(serr) {
		t.Fatalf("binary survived uninstall: %v", serr)
	}
	if _, serr := os.Stat(filepath.Join(s.installDir, "codeflow_go_adapter")); !os.IsNotExist(serr) {
		t.Fatalf("Go adapter survived uninstall: %v", serr)
	}
	if _, serr := os.Stat(s.skillDest); !os.IsNotExist(serr) {
		t.Fatalf("skill survived uninstall: %v", serr)
	}
	if line := s.mcpLine(); line != "" {
		t.Fatalf("MCP registration survived uninstall: %q", line)
	}
	if _, serr := os.Stat(filepath.Join(s.home, ".codeflow", "install-state.json")); !os.IsNotExist(serr) {
		t.Fatalf("ownership record survived uninstall: %v", serr)
	}

	// Verify Multi-Agent cleanups (Cursor, Claude, Antigravity)
	antigravityConfig := filepath.Join(s.home, ".gemini", "config", "mcp_config.json")
	if b, err := os.ReadFile(antigravityConfig); err == nil {
		if strings.Contains(string(b), `"codeflow"`) {
			t.Fatalf("Antigravity MCP entry survived uninstall:\n%s", string(b))
		}
	}
	cursorConfig := filepath.Join(s.home, ".cursor", "mcp.json")
	if b, err := os.ReadFile(cursorConfig); err == nil {
		if strings.Contains(string(b), `"codeflow"`) {
			t.Fatalf("Cursor MCP entry survived uninstall:\n%s", string(b))
		}
	}
	if _, serr := os.Stat(filepath.Join(s.home, ".cursor", "skills", "codeflow")); !os.IsNotExist(serr) {
		t.Fatalf("Cursor skill survived uninstall: %v", serr)
	}
	if _, serr := os.Stat(filepath.Join(s.home, ".claude", "skills", "codeflow")); !os.IsNotExist(serr) {
		t.Fatalf("Claude Code skill survived uninstall: %v", serr)
	}
	if _, serr := os.Stat(filepath.Join(s.home, ".gemini", "config", "skills", "codeflow")); !os.IsNotExist(serr) {
		t.Fatalf("Gemini config skill survived uninstall: %v", serr)
	}
	if _, serr := os.Stat(filepath.Join(s.home, ".gemini", "antigravity-cli", "skills", "codeflow")); !os.IsNotExist(serr) {
		t.Fatalf("Antigravity skill survived uninstall: %v", serr)
	}
	if _, serr := os.Stat(filepath.Join(s.home, ".gemini", "antigravity-cli", "mcp", "codeflow")); !os.IsNotExist(serr) {
		t.Fatalf("Antigravity MCP tool schemas survived uninstall: %v", serr)
	}
}

func TestInstallRejectsTypeScriptAdapterProtocolFailure(t *testing.T) {
	s := newSandbox(t)
	nodeStub := filepath.Join(s.root, "stub", "node")
	if err := os.WriteFile(nodeStub, []byte("#!/usr/bin/env bash\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "..", "scripts", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.run(t, "bash", script)
	if err == nil || !strings.Contains(out, "installed TypeScript adapter protocol") {
		t.Fatalf("installer must reject a failing TypeScript adapter before registration: %v\n%s", err, out)
	}
	if line := s.mcpLine(); line != "" {
		t.Fatalf("installer registered MCP after TypeScript adapter failure: %q", line)
	}
	if _, err := os.Stat(filepath.Join(s.home, ".codeflow", "install-state.json")); !os.IsNotExist(err) {
		t.Fatalf("installer recorded success after TypeScript adapter failure: %v", err)
	}
}

func TestInstallFromReleaseArchive(t *testing.T) {
	s := newSandbox(t)
	installerPath := prepareReleaseArchiveInstallation(t, s, true)
	if out, err := s.run(t, "bash", installerPath); err != nil {
		t.Fatalf("release archive installation failed: %v\n%s", err, out)
	}
	assertInstalled(t, s)
}

func TestInstallRejectsReleaseWithoutDartAdapter(t *testing.T) {
	s := newSandbox(t)
	installerPath := prepareReleaseArchiveInstallation(t, s, false)
	out, err := s.run(t, "bash", installerPath)
	if err == nil || !strings.Contains(out, "Release archive is missing the Dart adapter") {
		t.Fatalf("installer must reject a release without its Dart adapter: %v\n%s", err, out)
	}
	if line := s.mcpLine(); line != "" {
		t.Fatalf("installer registered MCP after missing Dart adapter: %q", line)
	}
	if _, err := os.Stat(filepath.Join(s.home, ".codeflow", "install-state.json")); !os.IsNotExist(err) {
		t.Fatalf("installer recorded success without Dart adapter: %v", err)
	}
	for _, name := range []string{"codeflow", "codeflow_go_adapter"} {
		if _, err := os.Stat(filepath.Join(s.installDir, name)); !os.IsNotExist(err) {
			t.Fatalf("installer copied %s before validating the release archive: %v", name, err)
		}
	}
}

func TestInstallRejectsIncompatibleReleaseAdapterBeforeCopy(t *testing.T) {
	s := newSandbox(t)
	installerPath := prepareReleaseArchiveInstallation(t, s, true)
	adapterPath := filepath.Join(s.root, "release", "bin", "dart-adapter")
	if err := os.WriteFile(adapterPath, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(s.root, "codeflow-release.tar.gz")
	if out, err := exec.Command("tar", "-czf", archive, "-C", filepath.Join(s.root, "release"), "bin", "skills", "adapters").CombinedOutput(); err != nil {
		t.Fatalf("package incompatible release archive: %v\n%s", err, out)
	}
	out, err := s.run(t, "bash", installerPath)
	if err == nil || !strings.Contains(out, "Dart adapter") {
		t.Fatalf("installer must reject an incompatible Dart adapter: %v\n%s", err, out)
	}
	if _, err := os.Stat(s.binary); !os.IsNotExist(err) {
		t.Fatalf("installer copied CodeFlow before checking adapter compatibility: %v", err)
	}
	if line := s.mcpLine(); line != "" {
		t.Fatalf("installer registered MCP after incompatible adapter: %q", line)
	}
}

func prepareReleaseArchiveInstallation(t *testing.T, s *sandbox, includeDart bool) string {
	t.Helper()
	codeflowBin, dartBin := ensurePrebuiltTestBinaries(t)
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	archiveDir := filepath.Join(s.root, "release")
	for _, dir := range []string{"bin", "skills", "adapters"} {
		if err := os.MkdirAll(filepath.Join(archiveDir, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	binaries := [][2]string{{codeflowBin, filepath.Join(archiveDir, "bin", "codeflow")}}
	if includeDart {
		binaries = append(binaries, [2]string{dartBin, filepath.Join(archiveDir, "bin", "dart-adapter")})
	}
	for _, item := range binaries {
		if out, err := exec.Command("cp", item[0], item[1]).CombinedOutput(); err != nil {
			t.Fatalf("copy release binary: %v\n%s", err, out)
		}
	}
	goAdapter := filepath.Join(archiveDir, "bin", "codeflow_go_adapter")
	build := exec.Command("go", "build", "-o", goAdapter, "./adapters/go")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build release Go adapter: %v\n%s", err, out)
	}
	for _, item := range [][2]string{
		{filepath.Join(repoRoot, "skills", "codeflow"), filepath.Join(archiveDir, "skills", "codeflow")},
		{filepath.Join(repoRoot, "adapters", "typescript"), filepath.Join(archiveDir, "adapters", "typescript")},
	} {
		if out, err := exec.Command("cp", "-R", item[0], item[1]).CombinedOutput(); err != nil {
			t.Fatalf("copy release asset: %v\n%s", err, out)
		}
	}
	archive := filepath.Join(s.root, "codeflow-release.tar.gz")
	if out, err := exec.Command("tar", "-czf", archive, "-C", archiveDir, "bin", "skills", "adapters").CombinedOutput(); err != nil {
		t.Fatalf("package release archive: %v\n%s", err, out)
	}
	installer, err := os.ReadFile(filepath.Join(repoRoot, "scripts", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	installerPath := filepath.Join(s.root, "install.sh")
	if err := os.WriteFile(installerPath, installer, 0o755); err != nil {
		t.Fatal(err)
	}
	curlStub := filepath.Join(s.root, "stub", "curl")
	if err := os.WriteFile(curlStub, []byte("#!/usr/bin/env bash\nwhile [ \"$#\" -gt 0 ]; do\n  if [ \"$1\" = \"-o\" ]; then cp \"$CODEFLOW_TEST_RELEASE_ARCHIVE\" \"$2\"; exit $?; fi\n  shift\ndone\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s.env = append(s.env, "CODEFLOW_TEST_RELEASE_ARCHIVE="+archive)
	return installerPath
}

func TestReinstallUpdatesPreviouslyOwnedSkill(t *testing.T) {
	s := newSandbox(t)
	s.installFromCheckout(t)

	skillFile := filepath.Join(s.skillDest, "SKILL.md")
	oldSkill := []byte("previous installer-owned skill\n")
	if err := os.WriteFile(skillFile, oldSkill, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(oldSkill)
	statePath := filepath.Join(s.home, ".codeflow", "install-state.json")
	stateBytes, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(stateBytes, &state); err != nil {
		t.Fatal(err)
	}
	state["skillSHA256"] = hex.EncodeToString(sum[:])
	stateBytes, err = json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, append(stateBytes, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	s.installFromCheckout(t)
	sourceSkill, err := os.ReadFile(filepath.Join("..", "..", "..", "skills", "codeflow", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	installedSkill, err := os.ReadFile(skillFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(installedSkill) != string(sourceSkill) {
		t.Fatal("reinstall did not update the previously installer-owned skill")
	}
}

func TestReinstallRejectsUserModifiedSkill(t *testing.T) {
	s := newSandbox(t)
	s.installFromCheckout(t)

	skillFile := filepath.Join(s.skillDest, "SKILL.md")
	f, err := os.OpenFile(skillFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\nuser edit\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	script, err := filepath.Abs(filepath.Join("..", "..", "..", "scripts", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.run(t, "bash", script)
	if err == nil {
		t.Fatalf("reinstall should reject a user-modified skill:\n%s", out)
	}
	if !strings.Contains(out, "was changed; refusing to overwrite") {
		t.Fatalf("reinstall did not explain the skill conflict:\n%s", out)
	}
}

func TestUninstallKeepsModifiedSkill(t *testing.T) {
	s := newSandbox(t)
	s.installFromCheckout(t)
	assertInstalled(t, s)

	skillFile := filepath.Join(s.skillDest, "SKILL.md")
	f, err := os.OpenFile(skillFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\nuser edit\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	out, err := s.run(t, s.binary, "uninstall")
	if err != nil {
		t.Fatalf("uninstall failed: %v\n%s", err, out)
	}
	if _, serr := os.Stat(skillFile); serr != nil {
		t.Fatalf("modified skill was removed: %v", serr)
	}
	if !strings.Contains(out, "changed") {
		t.Fatalf("uninstall did not explain the retained skill:\n%s", out)
	}
}

func TestUninstallRefusesForeignMCPRegistration(t *testing.T) {
	s := newSandbox(t)
	s.installFromCheckout(t)
	assertInstalled(t, s)

	line := s.mcpLine()
	foreign := strings.Replace(line, s.binary, "/usr/bin/true", 1)
	if err := os.WriteFile(s.mcpState, []byte(foreign+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := s.run(t, s.binary, "uninstall")
	if err == nil {
		t.Fatalf("uninstall removed a registration it no longer owns:\n%s", out)
	}
	if got := s.mcpLine(); !strings.Contains(got, "/usr/bin/true") {
		t.Fatalf("foreign registration was altered: %q", got)
	}
	if _, serr := os.Stat(s.binary); serr != nil {
		t.Fatal("binary was removed despite refusing the MCP removal")
	}
}
