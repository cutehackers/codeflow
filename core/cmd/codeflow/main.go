package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"codeflow/core/internal/baseline"
	"codeflow/core/internal/comparison"
	"codeflow/core/internal/compiler"
	flowcore "codeflow/core/internal/core"
	"codeflow/core/internal/doctor"
	"codeflow/core/internal/entrypoint"
	"codeflow/core/internal/expectation"
	"codeflow/core/internal/manifest"
	"codeflow/core/internal/mcp"
	flowruntime "codeflow/core/internal/runtime"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: codeflow doctor|fixture-open|basis|resolve|analyze|verify|serve|open|refresh|compare|cache|mcp")
		return 2
	}
	if args[0] == "fixture-open" {
		return fixtureOpen(args[1:], stdout, stderr)
	}
	if args[0] == "basis" {
		return basis(args[1:], stdout, stderr)
	}
	if args[0] == "resolve" {
		return resolve(args[1:], stdout, stderr)
	}
	if args[0] == "analyze" {
		return analyze(args[1:], stdout, stderr)
	}
	if args[0] == "verify" {
		return verify(args[1:], stdout, stderr)
	}
	if args[0] == "serve" {
		return serve(args[1:], stdout, stderr)
	}
	if args[0] == "open" {
		return openFlow(args[1:], stdout, stderr)
	}
	if args[0] == "refresh" {
		return analyze(args[1:], stdout, stderr)
	}
	if args[0] == "compare" {
		return compare(args[1:], stdout, stderr)
	}
	if args[0] == "mcp" {
		return mcpServe(args[1:], stdout, stderr)
	}
	if args[0] == "cache" {
		return cacheCommand(args[1:], stdout, stderr)
	}
	if args[0] != "doctor" {
		fmt.Fprintln(stderr, "usage: codeflow doctor [--repo DIR] [--format human|json] [--codegraph-url URL] [--adapter PATH]")
		return 2
	}
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	repo := flags.String("repo", ".", "repository to inspect")
	format := flags.String("format", "human", "output format: human or json")
	graphURL := flags.String("codegraph-url", "", "CodeGraph HTTP service URL")
	adapter := flags.String("adapter", "", "Dart adapter executable path")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if *format != "human" && *format != "json" {
		fmt.Fprintln(stderr, "--format must be human or json")
		return 2
	}
	report := doctor.Diagnose(context.Background(), doctor.Options{Repo: *repo, CodeGraphURL: *graphURL, AdapterPath: *adapter})
	if *format == "json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(report)
	} else {
		writeHuman(stdout, report)
	}
	if report.Ready {
		return 0
	}
	for _, check := range report.Checks {
		if check.State == doctor.Incompatible {
			return 2
		}
	}
	return 1
}

func cacheCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "status" && args[0] != "clean") {
		fmt.Fprintln(stderr, "usage: codeflow cache status|clean [--repo DIR] [--format human|json]")
		return 2
	}
	action := args[0]
	flags := flag.NewFlagSet("cache "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	repo := flags.String("repo", ".", "repository cache to inspect")
	format := flags.String("format", "human", "output format: human or json")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || (*format != "human" && *format != "json") {
		fmt.Fprintln(stderr, "usage: codeflow cache status|clean [--repo DIR] [--format human|json]")
		return 2
	}
	if action == "clean" {
		if err := baseline.CleanCache(*repo); err != nil {
			fmt.Fprintln(stderr, "cache clean:", err)
			return 1
		}
	}
	report, err := baseline.InspectCache(*repo)
	if err != nil {
		fmt.Fprintln(stderr, "cache:", err)
		return 1
	}
	if *format == "json" {
		_ = json.NewEncoder(stdout).Encode(report)
		return 0
	}
	if action == "clean" {
		fmt.Fprintln(stdout, "CodeFlow baseline cache cleaned (reconstructable data only).")
	}
	fmt.Fprintf(stdout, "Baseline cache: %d mirror(s), %s · automatic retention: %d\n", len(report.Baselines), humanBytes(report.TotalBytes), report.RetentionLimit)
	fmt.Fprintf(stdout, "Path: %s\n", report.Root)
	return 0
}

func humanBytes(bytes int64) string {
	const kib, mib = int64(1024), int64(1024 * 1024)
	if bytes >= mib {
		return fmt.Sprintf("%.1f MiB", float64(bytes)/float64(mib))
	}
	if bytes >= kib {
		return fmt.Sprintf("%.1f KiB", float64(bytes)/float64(kib))
	}
	return fmt.Sprintf("%d B", bytes)
}

func verify(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	repo := flags.String("repo", ".", "repository to inspect")
	graph := flags.String("codegraph-url", "", "CodeGraph HTTP URL")
	adapter := flags.String("adapter", "", "installed Dart adapter command")
	expectations := flags.String("expectations", "", "flow expectation JSON (defaults to repository contract)")
	timeout := flags.Duration("timeout", 15*time.Second, "analysis deadline")
	if err := flags.Parse(args); err != nil || flags.NArg() > 1 {
		fmt.Fprintln(stderr, "usage: codeflow verify [--repo DIR] [--codegraph-url URL] [--adapter COMMAND] [SELECTOR]")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	document, problem, err := compiler.Compile(ctx, compiler.Options{Repo: *repo, Selector: flags.Arg(0), CodeGraphURL: *graph, AdapterCommand: resolvedAdapter(*adapter)})
	if err != nil {
		fmt.Fprintln(stderr, "verify:", err)
		return 2
	}
	if problem != nil {
		_ = json.NewEncoder(stdout).Encode(problem)
		return 1
	}
	var file expectation.File
	if *expectations == "" {
		file, err = expectation.Load(document.Basis.Repository)
	} else {
		file, err = expectation.LoadPath(*expectations)
	}
	if err != nil {
		_ = json.NewEncoder(stdout).Encode(map[string]any{"flow_id": document.Current.ID, "ready": false, "failures": []string{err.Error()}})
		return 1
	}
	report := expectation.Verify(file, document)
	_ = json.NewEncoder(stdout).Encode(report)
	if !report.Ready {
		return 1
	}
	return 0
}

func mcpServe(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("mcp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	repo := flags.String("repo", ".", "repository Core to attach")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: codeflow mcp [--repo DIR]")
		return 2
	}
	if err := (mcp.Server{Repo: *repo}).Serve(context.Background(), os.Stdin, stdout); err != nil {
		fmt.Fprintln(stderr, "mcp:", err)
		return 1
	}
	return 0
}

func compare(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("compare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	repo := flags.String("repo", ".", "repository to inspect")
	baseline := flags.String("baseline", "", "local Git revision")
	graph := flags.String("codegraph-url", "", "CodeGraph HTTP URL")
	adapter := flags.String("adapter", "", "installed Dart adapter command")
	if err := flags.Parse(args); err != nil || flags.NArg() > 1 || *baseline == "" {
		fmt.Fprintln(stderr, "usage: codeflow compare [--repo DIR] --baseline REV [--codegraph-url URL] [--adapter COMMAND] [SELECTOR]")
		return 2
	}
	result, problem, err := comparison.Build(context.Background(), comparison.Options{Repo: *repo, Revision: *baseline, Selector: flags.Arg(0), CodeGraphURL: *graph, AdapterCommand: resolvedAdapter(*adapter)})
	if err != nil {
		fmt.Fprintln(stderr, "compare:", err)
		return 2
	}
	if problem != nil {
		_ = json.NewEncoder(stdout).Encode(problem)
		return 1
	}
	_ = json.NewEncoder(stdout).Encode(result)
	return 0
}

// analyze is the non-browser real-analysis path. The identical document is
// persisted and available through Core's authenticated API/FlowView while it
// runs; CLI output is its deterministic FlowIR body.
func analyze(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("analyze", flag.ContinueOnError)
	flags.SetOutput(stderr)
	repo := flags.String("repo", ".", "repository to inspect")
	graph := flags.String("codegraph-url", "", "CodeGraph HTTP URL")
	adapter := flags.String("adapter", "", "installed Dart adapter command")
	timeout := flags.Duration("timeout", 45*time.Second, "analysis deadline")
	if err := flags.Parse(args); err != nil || flags.NArg() > 1 {
		fmt.Fprintln(stderr, "usage: codeflow analyze [--repo DIR] [--codegraph-url URL] [--adapter COMMAND] [SELECTOR]")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	instance, problem, err := flowcore.StartAnalysis(ctx, *repo, flowcore.AnalysisOptions{Selector: flags.Arg(0), CodeGraphURL: *graph, AdapterCommand: resolvedAdapter(*adapter)})
	if err != nil {
		fmt.Fprintln(stderr, "analysis:", err)
		return 2
	}
	if problem != nil {
		_ = json.NewEncoder(stdout).Encode(problem)
		return 1
	}
	defer instance.Close(context.Background())
	doc, err := instance.Document(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "analysis read:", err)
		return 2
	}
	_ = json.NewEncoder(stdout).Encode(doc)
	return 0
}

// serve keeps one already-published analysis available for local review. It
// deliberately does not open a browser: packaging that journey belongs to a
// later ticket. The URL is printed only after StartAnalysis has committed the
// document and Core has acquired its authenticated loopback runtime.
func serve(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serveContext(ctx, args, stdout, stderr)
}

func serveContext(wait context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	repo := flags.String("repo", ".", "repository to inspect")
	graph := flags.String("codegraph-url", "", "CodeGraph HTTP URL")
	adapter := flags.String("adapter", "", "installed Dart adapter command")
	timeout := flags.Duration("timeout", 45*time.Second, "analysis deadline")
	if err := flags.Parse(args); err != nil || flags.NArg() > 1 {
		fmt.Fprintln(stderr, "usage: codeflow serve [--repo DIR] [--codegraph-url URL] [--adapter COMMAND] [SELECTOR]")
		return 2
	}
	started := time.Now()
	compile, cancel := context.WithTimeout(wait, *timeout)
	instance, problem, err := flowcore.StartAnalysis(compile, *repo, flowcore.AnalysisOptions{Selector: flags.Arg(0), CodeGraphURL: *graph, AdapterCommand: resolvedAdapter(*adapter)})
	cancel()
	if err != nil {
		fmt.Fprintln(stderr, "serve:", err)
		return 2
	}
	if problem != nil {
		_ = json.NewEncoder(stdout).Encode(problem)
		return 1
	}
	// Publication happens inside StartAnalysis before the Core is returned.
	fmt.Fprintf(stdout, "CodeFlow review URL: %s/\n", instance.URL)
	selected := flags.Arg(0)
	if selected == "" {
		selected = "auto-selected unique route"
	}
	fmt.Fprintf(stdout, "Ready in %.1fs · %s\n", time.Since(started).Seconds(), selected)
	<-wait.Done()
	shutdown, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = instance.Close(shutdown)
	shutdownCancel()
	if err != nil {
		fmt.Fprintln(stderr, "serve shutdown:", err)
		return 2
	}
	return 0
}

// open is the installed macOS convenience journey. It intentionally owns the
// foreground runtime; a later daemon manager may reuse it across processes.
func openFlow(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("open", flag.ContinueOnError)
	flags.SetOutput(stderr)
	repo := flags.String("repo", ".", "repository to inspect")
	graph := flags.String("codegraph-url", "", "CodeGraph HTTP URL")
	adapter := flags.String("adapter", "", "installed Dart adapter command")
	if err := flags.Parse(args); err != nil || flags.NArg() > 1 {
		fmt.Fprintln(stderr, "usage: codeflow open [--repo DIR] [--codegraph-url URL] [--adapter COMMAND] [SELECTOR]")
		return 2
	}
	if reused, url, err := reuseRuntime(*repo); err != nil {
		fmt.Fprintln(stderr, "open:", err)
		return 2
	} else if reused {
		if runtime.GOOS != "darwin" {
			fmt.Fprintln(stderr, "open: MACOS_REQUIRED: compatible Core refreshed; use `codeflow serve` to review on this platform")
			return 2
		}
		if err := exec.Command("/usr/bin/open", url).Run(); err != nil {
			fmt.Fprintln(stderr, "open:", err)
			return 2
		}
		fmt.Fprintf(stdout, "CodeFlow review URL: %s\n", url)
		return 0
	}
	if runtime.GOOS != "darwin" {
		fmt.Fprintln(stderr, "open: MACOS_REQUIRED: use `codeflow serve` on this platform")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	compile, cancel := context.WithTimeout(ctx, 45*time.Second)
	instance, problem, err := flowcore.StartAnalysis(compile, *repo, flowcore.AnalysisOptions{Selector: flags.Arg(0), CodeGraphURL: *graph, AdapterCommand: resolvedAdapter(*adapter)})
	cancel()
	if err != nil {
		fmt.Fprintln(stderr, "open:", err)
		return 2
	}
	if problem != nil {
		_ = json.NewEncoder(stdout).Encode(problem)
		return 1
	}
	url := instance.URL + "/"
	if err := exec.Command("/usr/bin/open", url).Run(); err != nil {
		_ = instance.Close(context.Background())
		fmt.Fprintln(stderr, "open:", err)
		return 2
	}
	fmt.Fprintf(stdout, "CodeFlow review URL: %s\n", url)
	<-ctx.Done()
	shutdown, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	if err := instance.Close(shutdown); err != nil {
		return 2
	}
	return 0
}

func reuseRuntime(repo string) (bool, string, error) {
	state, err := flowruntime.ReadState(repo)
	if err != nil {
		return false, "", nil
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/", state.Port)
	req, _ := http.NewRequest(http.MethodPost, url+"api/v1/refresh", nil)
	req.Header.Set("X-CodeFlow-Token", state.AuthToken)
	response, err := http.DefaultClient.Do(req)
	if err != nil || response.StatusCode != http.StatusOK {
		if response != nil {
			response.Body.Close()
		}
		return false, "", fmt.Errorf("CORE_INCOMPATIBLE_OR_UNAVAILABLE: existing runtime could not refresh safely")
	}
	response.Body.Close()
	return true, url, nil
}

func resolvedAdapter(value string) string {
	if value != "" {
		return value
	}
	if executable, err := os.Executable(); err == nil {
		dir := filepath.Dir(executable)
		candidates := []string{
			filepath.Join(dir, "..", "libexec", "codeflow-dart-adapter"),
			filepath.Join(dir, "adapters", "dart", "bin", "codeflow-dart-adapter.dart"),
			filepath.Join(dir, "..", "adapters", "dart", "bin", "codeflow-dart-adapter.dart"),
		}
		for _, candidate := range candidates {
			if _, err := os.Stat(candidate); err != nil {
				continue
			}
			if strings.HasSuffix(candidate, ".dart") {
				return "dart " + candidate
			}
			return candidate
		}
	}
	// Local source checkout: Go retains this file's build path unless the
	// binary was deliberately built with trimpath. This keeps `go run` and test
	// binaries as easy to use as ./codeflow without target-repo adapter flags.
	if _, sourceFile, _, ok := runtime.Caller(0); ok {
		candidate := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "../../..", "adapters", "dart", "bin", "codeflow-dart-adapter.dart"))
		if _, err := os.Stat(candidate); err == nil {
			return "dart " + candidate
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		candidate := filepath.Join(cwd, "adapters", "dart", "bin", "codeflow-dart-adapter.dart")
		if _, err := os.Stat(candidate); err == nil {
			return "dart " + candidate
		}
	}
	return ""
}

// resolve is the non-browser public path for CF-G04. It returns exactly the
// same typed selector result as the authenticated Core API and FlowView.
func resolve(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("resolve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	repo := flags.String("repo", ".", "repository to inspect")
	adapter := flags.String("adapter", "", "installed Dart adapter command")
	timeout := flags.Duration("timeout", 30*time.Second, "adapter deadline")
	if err := flags.Parse(args); err != nil || flags.NArg() > 1 {
		fmt.Fprintln(stderr, "usage: codeflow resolve [--repo DIR] [--adapter COMMAND] [SELECTOR]")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	result := entrypoint.Resolve(ctx, *repo, flags.Arg(0), resolvedAdapter(*adapter))
	_ = json.NewEncoder(stdout).Encode(result)
	if result.State == entrypoint.Ready {
		return 0
	}
	if result.State == entrypoint.Unavailable {
		return 2
	}
	return 1
}

// basis is the non-mutating CLI representation of the exact same FlowBasis
// that Core stores and returns from its API.
func basis(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("basis", flag.ContinueOnError)
	flags.SetOutput(stderr)
	repo := flags.String("repo", ".", "repository to inspect")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	result, err := manifest.Capture(*repo)
	if err != nil {
		fmt.Fprintln(stderr, "capture basis:", err)
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(struct {
		Basis  any    `json:"basis"`
		Status string `json:"status"`
	}{result, "ready"}); err != nil {
		return 1
	}
	return 0
}

// fixture-open is intentionally explicit: it demonstrates the G02 vertical slice and is not analysis.
func fixtureOpen(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("fixture-open", flag.ContinueOnError)
	flags.SetOutput(stderr)
	repo := flags.String("repo", ".", "repository containing controlled lib/signup.dart fixture")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	instance, err := flowcore.StartFixture(context.Background(), *repo)
	if err != nil {
		fmt.Fprintln(stderr, "fixture Core:", err)
		return 1
	}
	defer instance.Close(context.Background())
	fmt.Fprintf(stdout, "CodeFlow fixture FlowView: %s/\n", instance.URL)
	fmt.Fprintln(stdout, "Use Ctrl-C to stop the fixture Core.")
	for {
		time.Sleep(time.Second)
	}
}

func writeHuman(writer io.Writer, report doctor.Report) {
	fmt.Fprintf(writer, "CodeFlow doctor: %s\n", report.Repository)
	for _, check := range report.Checks {
		fmt.Fprintf(writer, "%-14s %-13s %s\n", check.Name, strings.ToUpper(string(check.State)), check.Message)
		if check.Remediation != "" {
			fmt.Fprintf(writer, "  fix: %s\n", check.Remediation)
		}
	}
	for _, warning := range report.Warnings {
		fmt.Fprintf(writer, "warning: %s\n", warning)
	}
	if report.Ready {
		fmt.Fprintln(writer, "Result: ready for CodeFlow analysis")
	} else {
		fmt.Fprintln(writer, "Result: not ready; resolve the checks above")
	}
}
