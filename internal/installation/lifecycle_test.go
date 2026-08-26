// End-to-end guarantee for the one-shot installer and uninstaller:
// scripts/install.sh must produce a working owned installation inside an
// isolated HOME, and `codeflow uninstall` must remove exactly what it owns.
package installation

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
	for _, tool := range []string{"go", "dart", "make", "shasum"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is required for the installer lifecycle test", tool)
		}
	}
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
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "install.sh"))
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
	if _, serr := os.Stat(s.skillDest); !os.IsNotExist(serr) {
		t.Fatalf("skill survived uninstall: %v", serr)
	}
	if line := s.mcpLine(); line != "" {
		t.Fatalf("MCP registration survived uninstall: %q", line)
	}
	if _, serr := os.Stat(filepath.Join(s.home, ".codeflow", "install-state.json")); !os.IsNotExist(serr) {
		t.Fatalf("ownership record survived uninstall: %v", serr)
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
