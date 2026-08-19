// Package doctor implements the read-only environment diagnosis behind `codeflow doctor`.
package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"codeflow/core/internal/codegraph"
	"codeflow/core/internal/config"
)

type State string

const (
	Ready        State = "ready"
	Unconfigured State = "unconfigured"
	Unavailable  State = "unavailable"
	Incompatible State = "incompatible"
	Invalid      State = "invalid"
)

type Check struct {
	Name        string            `json:"name"`
	State       State             `json:"state"`
	Message     string            `json:"message"`
	Remediation string            `json:"remediation,omitempty"`
	Details     map[string]string `json:"details,omitempty"`
}

type Report struct {
	Repository string   `json:"repository"`
	Ready      bool     `json:"ready"`
	Checks     []Check  `json:"checks"`
	Warnings   []string `json:"warnings,omitempty"`
}

type Options struct {
	Repo         string
	CodeGraphURL string
	AdapterPath  string
	LookPath     func(string) (string, error)
	Run          func(context.Context, string, ...string) ([]byte, error)
	HTTPClient   *http.Client
}

func Diagnose(ctx context.Context, options Options) Report {
	if options.LookPath == nil {
		options.LookPath = exec.LookPath
	}
	if options.Run == nil {
		options.Run = run
	}
	if options.HTTPClient == nil {
		options.HTTPClient = &http.Client{Timeout: 1200 * time.Millisecond}
	}
	if options.Repo == "" {
		options.Repo = "."
	}
	repo, err := filepath.Abs(options.Repo)
	if err != nil {
		repo = options.Repo
	}
	report := Report{Repository: repo}

	gitCheck, gitRoot := checkGit(ctx, repo, options)
	report.Checks = append(report.Checks, gitCheck)
	configRoot := repo
	if gitRoot != "" {
		configRoot = gitRoot
		report.Repository = gitRoot
	}
	configCheck, warnings := checkConfig(configRoot)
	report.Checks = append(report.Checks, configCheck)
	report.Warnings = append(report.Warnings, warnings...)
	report.Checks = append(report.Checks, checkCodeGraph(ctx, options))
	report.Checks = append(report.Checks, checkSDK(ctx, configRoot, options))
	report.Checks = append(report.Checks, checkAdapter(ctx, options))
	report.Ready = true
	for _, check := range report.Checks {
		if check.State != Ready && check.State != Unconfigured {
			report.Ready = false
			break
		}
	}
	return report
}

func checkGit(ctx context.Context, repo string, options Options) (Check, string) {
	if _, err := options.LookPath("git"); err != nil {
		return Check{Name: "git", State: Unavailable, Message: "Git executable was not found", Remediation: "Install Git and run doctor again."}, ""
	}
	out, err := options.Run(ctx, "git", "-C", repo, "rev-parse", "--show-toplevel")
	if err != nil {
		return Check{Name: "git", State: Unavailable, Message: "The selected directory is not a Git worktree", Remediation: "Run codeflow doctor from a Git repository."}, ""
	}
	head := "present"
	if _, err := options.Run(ctx, "git", "-C", repo, "rev-parse", "--verify", "HEAD"); err != nil {
		head = "unborn"
	}
	return Check{Name: "git", State: Ready, Message: "Git worktree detected", Details: map[string]string{"root": strings.TrimSpace(string(out)), "head": head}}, strings.TrimSpace(string(out))
}

func checkConfig(repo string) (Check, []string) {
	result, err := config.Load(repo)
	if err != nil {
		state := Invalid
		if strings.Contains(err.Error(), "unsupported schema_version") {
			state = Incompatible
		}
		return Check{Name: "configuration", State: state, Message: err.Error(), Remediation: "Update codeflow.yaml to the supported v1 configuration contract."}, nil
	}
	if !result.Present {
		return Check{Name: "configuration", State: Unconfigured, Message: "No codeflow.yaml; unique-only feature discovery remains available"}, []string{"codeflow.yaml is not present; unique-only feature discovery will be used"}
	}
	return Check{Name: "configuration", State: Ready, Message: "codeflow.yaml v1 is valid", Details: map[string]string{"repository_id": result.Config.Repository.ID}}, result.Warnings
}

func checkCodeGraph(ctx context.Context, options Options) Check {
	url := options.CodeGraphURL
	if url == "" {
		url = os.Getenv("CODEFLOW_CODEGRAPH_URL")
	}
	if url == "" {
		url = "http://127.0.0.1:8000"
	}
	baseURL := strings.TrimRight(url, "/")
	for _, endpoint := range []string{"/health", "/api/v1/status", "/api/v1/tools"} {
		response, err := get(ctx, options.HTTPClient, baseURL+endpoint)
		if err != nil {
			return Check{Name: "codegraph", State: Unavailable, Message: "CodeGraph HTTP service is unreachable; owned Dart structural fallback is available for supported routes", Remediation: "Start a compatible CodeGraph service for external graph coverage, or analyze a supported Dart route with the owned structural backend.", Details: map[string]string{"url": baseURL, "owned_dart_structural": "available"}}
		}
		if response.StatusCode < 200 || response.StatusCode > 299 {
			response.Body.Close()
			return Check{Name: "codegraph", State: Incompatible, Message: fmt.Sprintf("CodeGraph %s returned HTTP %d", endpoint, response.StatusCode), Remediation: "Use a CodeGraph release exposing the v1 health, status, and tools contracts.", Details: map[string]string{"url": baseURL}}
		}
		if endpoint == "/api/v1/status" {
			var status json.RawMessage
			err = json.NewDecoder(response.Body).Decode(&status)
			response.Body.Close()
			if err != nil {
				return Check{Name: "codegraph", State: Incompatible, Message: "CodeGraph status response is not valid JSON", Remediation: "Use a CodeGraph release exposing the documented status endpoint."}
			}
			continue
		}
		if endpoint == "/api/v1/tools" {
			var document any
			err = json.NewDecoder(response.Body).Decode(&document)
			response.Body.Close()
			if err != nil {
				return Check{Name: "codegraph", State: Incompatible, Message: "CodeGraph tools response is not valid JSON", Remediation: "Use a CodeGraph release exposing registered tool schemas."}
			}
			if !codegraph.CompatibleTools(document) {
				return Check{Name: "codegraph", State: Incompatible, Message: "CodeGraph relationship tool schema is incompatible", Remediation: "Use a CodeGraphContext release whose analyze_code_relationships schema accepts query_type and target, or the legacy repository and entry_point bridge schema.", Details: map[string]string{"required_schema": "query_type,target | repository,entry_point"}}
			}
			continue
		}
		response.Body.Close()
	}
	return Check{Name: "codegraph", State: Ready, Message: "CodeGraph HTTP discovery profile is compatible", Details: map[string]string{"profile": "codeflow.codegraph-discovery.v1"}}
}

var requiredCodeGraphTools = []string{
	"add_code_to_graph",
	"analyze_code_relationships",
	"find_code",
	"list_indexed_repositories",
}

func discoveredToolNames(document any) map[string]bool {
	names := make(map[string]bool)
	var visit func(any)
	visit = func(value any) {
		switch node := value.(type) {
		case []any:
			for _, item := range node {
				visit(item)
			}
		case map[string]any:
			if name, ok := node["name"].(string); ok {
				names[name] = true
			}
			for _, key := range []string{"tools", "data", "result"} {
				if child, ok := node[key]; ok {
					visit(child)
				}
			}
		}
	}
	visit(document)
	return names
}

func missingTools(names map[string]bool) []string {
	missing := make([]string, 0, len(requiredCodeGraphTools))
	for _, tool := range requiredCodeGraphTools {
		if !names[tool] {
			missing = append(missing, tool)
		}
	}
	return missing
}

func get(ctx context.Context, client *http.Client, url string) (*http.Response, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	return client.Do(req)
}

func checkSDK(ctx context.Context, repo string, options Options) Check {
	flutter := filepath.Join(repo, ".fvm", "flutter_sdk", "bin", "flutter")
	if info, err := os.Stat(flutter); err == nil && !info.IsDir() {
		out, err := options.Run(ctx, flutter, "--version", "--machine")
		if err != nil {
			return Check{Name: "dart_flutter", State: Unavailable, Message: "Project Flutter SDK could not report its version", Remediation: "Repair the repository-local Flutter SDK."}
		}
		var version struct {
			FlutterVersion string `json:"flutterVersion"`
			DartSdkVersion string `json:"dartSdkVersion"`
		}
		if json.Unmarshal(out, &version) != nil || !atLeastDart310(version.DartSdkVersion) {
			return Check{Name: "dart_flutter", State: Incompatible, Message: "Project Flutter SDK does not provide Dart 3.10 or later", Remediation: "Upgrade the repository-local Flutter SDK."}
		}
		return Check{Name: "dart_flutter", State: Ready, Message: "Project Flutter SDK detected", Details: map[string]string{"version": version.DartSdkVersion, "flutter_version": version.FlutterVersion, "source": ".fvm/flutter_sdk"}}
	}
	path, err := options.LookPath("dart")
	if err != nil {
		return Check{Name: "dart_flutter", State: Unavailable, Message: "Dart SDK was not found", Remediation: "Install Dart 3.10 or later, or configure a repository-local Flutter SDK."}
	}
	out, err := options.Run(ctx, path, "--version")
	if err != nil {
		return Check{Name: "dart_flutter", State: Unavailable, Message: "Dart SDK could not report its version", Remediation: "Repair the Dart SDK installation."}
	}
	version := dartVersion(string(out))
	if !atLeastDart310(version) {
		return Check{Name: "dart_flutter", State: Incompatible, Message: "Dart SDK is older than 3.10", Remediation: "Install Dart 3.10 or later.", Details: map[string]string{"version": version}}
	}
	return Check{Name: "dart_flutter", State: Ready, Message: "Dart SDK detected", Details: map[string]string{"version": version, "path": path, "source": "PATH dart"}}
}

func checkAdapter(ctx context.Context, options Options) Check {
	path := options.AdapterPath
	if path == "" {
		path = os.Getenv("CODEFLOW_DART_ADAPTER")
	}
	if path == "" {
		var err error
		path, err = options.LookPath("codeflow-dart-adapter")
		if err != nil {
			return Check{Name: "dart_adapter", State: Unavailable, Message: "CodeFlow Dart adapter was not found", Remediation: "Install the matching CodeFlow Dart adapter or set CODEFLOW_DART_ADAPTER."}
		}
	}
	var out []byte
	var err error
	if strings.HasSuffix(path, ".dart") {
		dart, dartErr := options.LookPath("dart")
		if dartErr != nil {
			return Check{Name: "dart_adapter", State: Unavailable, Message: "Dart is required to run the owned Dart adapter", Remediation: "Install Dart 3.10 or later."}
		}
		out, err = options.Run(ctx, dart, path, "--probe")
	} else {
		out, err = options.Run(ctx, path, "--probe")
	}
	if err != nil {
		return Check{Name: "dart_adapter", State: Unavailable, Message: "Dart adapter could not complete its probe", Remediation: "Install a runnable CodeFlow Dart adapter compatible with protocol v1."}
	}
	var probe struct {
		ProtocolVersion string `json:"protocol_version"`
		Status          string `json:"status"`
	}
	if err := json.Unmarshal(out, &probe); err != nil || probe.ProtocolVersion != "1" || probe.Status != "ready" {
		return Check{Name: "dart_adapter", State: Incompatible, Message: "Dart adapter probe is incompatible", Remediation: "Install a CodeFlow Dart adapter compatible with protocol v1."}
	}
	return Check{Name: "dart_adapter", State: Ready, Message: "Dart adapter probe is compatible", Details: map[string]string{"protocol": "1", "path": path}}
}

func dartVersion(output string) string {
	for _, field := range strings.Fields(output) {
		field = strings.TrimPrefix(field, "v")
		if strings.Count(field, ".") >= 1 && field[0] >= '0' && field[0] <= '9' {
			return field
		}
	}
	return ""
}

func atLeastDart310(version string) bool {
	var major, minor int
	if _, err := fmt.Sscanf(version, "%d.%d", &major, &minor); err != nil {
		return false
	}
	return major > 3 || major == 3 && minor >= 10
}

func run(ctx context.Context, command string, args ...string) ([]byte, error) {
	// SDK probes can contend with concurrent integration tests on developer laptops;
	// this is a readiness check, not an interactive request path.
	commandContext, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(commandContext, command, args...)
	out, err := cmd.CombinedOutput()
	if errors.Is(commandContext.Err(), context.DeadlineExceeded) {
		return out, fmt.Errorf("command timed out: %w", err)
	}
	return out, err
}
