// Command codeflow is the CodeFlow v2 core CLI (design-v2 §4.1): a single
// native binary orchestrating flow extraction, publishing, and serving.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"codeflow/internal/detect"
	"codeflow/internal/doctor"
	"codeflow/internal/flowview"
	"codeflow/internal/fusion"
	"codeflow/internal/harvest"
	"codeflow/internal/initcmd"
	"codeflow/internal/installation"
	"codeflow/internal/installstate"
	"codeflow/internal/mcp"
	"codeflow/internal/naming"
	"codeflow/internal/protocol"
	"codeflow/internal/slicing"
	"codeflow/internal/storage"
)

// Populated at build time via -ldflags (Makefile build target).
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const usage = `codeflow — Business Flow First Engine

Usage:
  codeflow init [path]        prepare a repository: detect project, resolve
                              adapter pins, create .codeflow/workspace.json
  codeflow flows [path]       harvest flow candidates in automatic score order.
                              Flags: --json
  codeflow query [path]       query task view and natural language feature flows.
                              Flags: --mode, --request, --entry, --flow, --domain, --json
  codeflow publish [path]     harvest, slice, fuse, and atomically publish flows.
                              Flags: --limit <N>
  codeflow show <id|entry>    display flow steps and business rules.
                              Flags: --json
  codeflow view [path]        start FlowView interactive web UI.
                              Flags: --port <port>
  codeflow serve [path]       alias for 'codeflow view'
  codeflow mcp [path]         start MCP stdio JSON-RPC server for AI agents.
  codeflow doctor [path]      check environment, adapter, and workspace integrity.
  codeflow uninstall          remove the CodeFlow MCP, skill, and owned files.
  codeflow version            print version information
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	command := os.Args[1]
	args := os.Args[2:]

	switch command {
	case "init":
		runInit(args)
	case "flows":
		runFlows(args)
	case "query":
		runQuery(args)
	case "publish":
		runPublish(args)
	case "show":
		runShow(args)
	case "view", "serve":
		runServe(args)
	case "mcp":
		runMCP(args)
	case "doctor":
		runDoctor(args)
	case "uninstall":
		runUninstall(args)
	case "install-record":
		runInstallRecord(args)
	case "version":
		fmt.Println(formatVersion(version, date))
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", command, usage)
		os.Exit(1)
	}
}

// formatVersion returns the formatted version string per CLI specification:
// `codeflow v*.*.*, date: {human readable date}`
func formatVersion(ver, rawDate string) string {
	ver = strings.TrimSpace(ver)
	if ver == "" {
		ver = "dev"
	}
	if !strings.HasPrefix(ver, "v") {
		ver = "v" + ver
	}
	humanDate := formatHumanDate(rawDate)
	return fmt.Sprintf("codeflow %s, date: %s", ver, humanDate)
}

func formatHumanDate(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "unknown" {
		return "unknown"
	}
	layouts := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05 MST",
		"2006-01-02 15:04:05",
		"2006-01-02",
		"2006/01/02",
		time.RFC1123,
		time.RFC1123Z,
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 {
				return t.Format("2006-01-02")
			}
			return t.Format("2006-01-02 15:04:05 MST")
		}
	}
	return raw
}

// reorderFlags moves flag tokens (and, for value-taking flags, their values)
// ahead of positional arguments so flags work in any position — e.g.
// `codeflow publish . -limit 5` — while keeping Go flag's parsing semantics.
func reorderFlags(fs *flag.FlagSet, args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			pos = append(pos, a)
			continue
		}
		flags = append(flags, a)
		if strings.Contains(a, "=") {
			continue
		}
		if f := fs.Lookup(strings.TrimLeft(a, "-")); f != nil {
			if _, isBool := f.Value.(interface{ IsBoolFlag() bool }); !isBool && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		}
	}
	return append(flags, pos...)
}

func runInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: codeflow init [path]")
	}
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		os.Exit(2)
	}
	posArgs := fs.Args()
	target := "."
	if len(posArgs) == 1 {
		target = posArgs[0]
	}

	res, err := initcmd.Run(target, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "codeflow init: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Initialized CodeFlow workspace in %s (language=%s)\n",
		res.RepoRoot, res.Language)
}

func runFlows(args []string) {
	fs := flag.NewFlagSet("flows", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: codeflow flows [--json] [path]")
	}
	jsonOut := fs.Bool("json", false, "emit the raw candidates JSON array instead of a table")

	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		os.Exit(2)
	}
	posArgs := fs.Args()
	target := "."
	if len(posArgs) == 1 {
		target = posArgs[0]
	}

	absTarget, err := filepath.Abs(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve path: %v\n", err)
		os.Exit(1)
	}

	det := detect.Detect(absTarget)
	lang := "dart"
	if det.Confident && det.Language != "" && det.Language != "unknown" {
		lang = det.Language
	}

	cfg, err := harvest.ResolveAdapter(lang, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "codeflow flows: %v\n", err)
		os.Exit(1)
	}

	runner := harvest.NewRunner(cfg, 1)
	defer runner.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	candidates, err := runner.Run(ctx, absTarget)
	if err != nil {
		fmt.Fprintf(os.Stderr, "codeflow flows: %v\n", err)
		os.Exit(1)
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(candidates); err != nil {
			fmt.Fprintf(os.Stderr, "encode output: %v\n", err)
			os.Exit(1)
		}
		return
	}

	printFlowTable(candidates)
}

func runPublish(args []string) {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	limitFlag := fs.Int("limit", 50, "maximum number of top-scored flows to slice and publish")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		os.Exit(2)
	}
	posArgs := fs.Args()
	target := "."
	if len(posArgs) == 1 {
		target = posArgs[0]
	}

	absTarget, err := filepath.Abs(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve path: %v\n", err)
		os.Exit(1)
	}

	det := detect.Detect(absTarget)
	lang := "dart"
	if det.Confident && det.Language != "" && det.Language != "unknown" {
		lang = det.Language
	}

	cfg, err := harvest.ResolveAdapter(lang, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "codeflow publish: %v\n", err)
		os.Exit(1)
	}

	pool := protocol.NewPool(cfg, 2)
	defer pool.Close()

	harvester := harvest.NewRunnerWithPool(pool)

	slicer := slicing.NewRunner(pool)
	st := storage.New(absTarget)
	eventLog := fusion.NewEventLog(absTarget)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	fmt.Println("Harvesting flow candidates...")
	candidates, err := harvester.Run(ctx, absTarget)
	if err != nil {
		fmt.Fprintf(os.Stderr, "harvest: %v\n", err)
		os.Exit(1)
	}

	// Business flows only: prioritize manifest pinned flows first, then root
	// user/system flows spanning layers. Deduped members (unless pinned) and
	// standalone internal use cases are excluded from top-level flow publishing.
	var pinned, unpinned []harvest.Candidate
	for _, c := range candidates {
		if c.ManifestOverride == "pinned" {
			pinned = append(pinned, c)
		} else if c.DedupedInto == nil && c.TriggerClass != "use_case_invocation" {
			unpinned = append(unpinned, c)
		}
	}

	toPublish := append(pinned, unpinned...)
	if len(toPublish) > *limitFlag {
		toPublish = toPublish[:*limitFlag]
	}

	approved, session, err := eventLog.MaterializeView()
	if err != nil {
		fmt.Fprintf(os.Stderr, "event log view: %v\n", err)
		os.Exit(1)
	}

	// Compute basisSha as hex64 worktree fingerprint over unique file parts of EntrySymbolPath.
	relMap := make(map[string]struct{})
	var relPaths []string
	for _, c := range toPublish {
		fp := c.EntrySymbolPath
		if idx := strings.Index(fp, "#"); idx >= 0 {
			fp = fp[:idx]
		}
		if fp == "" {
			continue
		}
		if _, ok := relMap[fp]; !ok {
			relMap[fp] = struct{}{}
			relPaths = append(relPaths, fp)
		}
	}
	basisSha, err := storage.ComputeWorktreeFingerprint(absTarget, relPaths)
	if err != nil {
		fmt.Fprintf(os.Stderr, "compute basisSha: %v\n", err)
		os.Exit(1)
	}

	sess, err := st.BeginGeneration(basisSha)
	if err != nil {
		fmt.Fprintf(os.Stderr, "begin generation: %v\n", err)
		os.Exit(1)
	}
	defer sess.Discard()

	fmt.Printf("Slicing and fusing %d flows...\n", len(toPublish))
	for _, c := range toPublish {
		cid := c.CandidateID
		sliced, err := slicer.Slice(ctx, absTarget, cid, c.EntrySymbolPath, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: slice failed for %s: %v\n", c.EntrySymbolPath, err)
			continue
		}

		title := c.IntentSignals.DerivedName
		if title == "" {
			title = naming.DeriveTitle(c.EntrySymbolPath)
		}
		desc := ""
		if c.IntentSignals.DocLine != nil {
			desc = *c.IntentSignals.DocLine
		}

		spec, err := fusion.Fuse(sliced, fusion.FuseOptions{
			CustomTitle:       title,
			CustomDescription: desc,
			RepoRoot:          absTarget,
			ApprovedLedger:    approved,
			SessionDrafts:     session,
			BasisSha:          basisSha,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: fuse failed for %s: %v\n", c.EntrySymbolPath, err)
			continue
		}

		specBytes, _ := json.Marshal(spec)
		_ = sess.AddFlowSpec(spec.FlowID, specBytes, storage.FlowSummary{
			FlowID:          spec.FlowID,
			Title:           spec.Title,
			Description:     spec.Description,
			EntrySymbolPath: c.EntrySymbolPath,
			StepCount:       len(spec.Steps),
		})
	}

	if err := sess.Commit(); err != nil {
		fmt.Fprintf(os.Stderr, "publish commit: %v\n", err)
		os.Exit(1)
	}

	ptr, _ := st.ReadPointer()
	fmt.Printf("✓ Successfully published generation %s (%d flows)\n", ptr.GenerationID, ptr.FlowCount)
}

func runShow(args []string) {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit flow as JSON")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		os.Exit(2)
	}
	posArgs := fs.Args()

	if len(posArgs) == 0 {
		fmt.Fprintln(os.Stderr, "usage: codeflow show <flowId|entrySymbolPath> [repoPath]")
		os.Exit(1)
	}

	query := posArgs[0]
	target := "."
	if len(posArgs) > 1 {
		target = posArgs[1]
	}

	absTarget, _ := filepath.Abs(target)
	st := storage.New(absTarget)

	flowID := query
	if strings.Contains(query, "#") || strings.Contains(query, "/") {
		flowID = fusion.ComputeFlowID(query)
	}

	raw, err := st.ReadActiveFlowSpec(flowID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "flow %s not found: %v\n", query, err)
		os.Exit(1)
	}

	if *jsonOut {
		fmt.Println(string(raw))
		return
	}

	var spec fusion.FlowSpec
	_ = json.Unmarshal(raw, &spec)

	fmt.Printf("\n=== %s ===\n", spec.Title)
	fmt.Printf("Flow ID:  %s\n", spec.FlowID)
	fmt.Printf("Basis:    %s\n", spec.BasisSha)
	fmt.Printf("Steps:    %d\n\n", len(spec.Steps))

	for _, s := range spec.Steps {
		provBadge := fmt.Sprintf("[%s]", s.Provenance)
		freshBadge := ""
		if s.Freshness == "stale" {
			freshBadge = " [STALE]"
		}
		fmt.Printf(" %2d. %-45s %s%s\n", s.Ordinal, s.Name, provBadge, freshBadge)
		if len(s.Rules) > 0 {
			fmt.Printf("     Rules: %s\n", strings.Join(s.Rules, ", "))
		}
		if s.StateDelta != nil {
			fmt.Printf("     State: %s -> %s\n", s.StateDelta.Before, s.StateDelta.After)
		}
		if s.SideEffect != nil {
			fmt.Printf("     Call:  %s\n", *s.SideEffect)
		}
		if s.Branch != nil {
			fmt.Printf("     Cond:  %s\n", *s.Branch)
		}
		fmt.Println()
	}
}

func runServe(args []string) {
	fs := flag.NewFlagSet("view", flag.ContinueOnError)
	portFlag := fs.Int("port", 4567, "loopback port for FlowView UI")
	tokenFlag := fs.String("token", "", "fixed auth token for testing or headless use")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		os.Exit(2)
	}
	posArgs := fs.Args()
	target := "."
	if len(posArgs) == 1 {
		target = posArgs[0]
	}

	absTarget, _ := filepath.Abs(target)
	srv, err := flowview.NewServer(flowview.Config{
		RepoRoot:  absTarget,
		Port:      *portFlag,
		AuthToken: *tokenFlag,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "start flowview: %v\n", err)
		os.Exit(1)
	}
	srv.Start()

	fmt.Printf("\n  CodeFlow View is live at:\n  %s\n\n  Press Ctrl+C to stop.\n", srv.URL())

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	_ = srv.Shutdown(context.Background())
}

func runMCP(args []string) {
	target := "."
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		target = args[0]
	}
	absTarget, _ := filepath.Abs(target)

	srv, err := mcp.NewServer(mcp.Config{
		RepoRoot:     absTarget,
		RequireToken: false,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "start mcp: %v\n", err)
		os.Exit(1)
	}
	defer srv.Close()

	if err := srv.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "mcp serve: %v\n", err)
		os.Exit(1)
	}
}

func runDoctor(args []string) {
	target := "."
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		target = args[0]
	}
	absTarget, _ := filepath.Abs(target)

	det := detect.Detect(absTarget)
	spec := ""
	if det.Language == "dart" || !det.Confident {
		spec = os.Getenv(harvest.DartAdapterEnvVar)
	}
	results := doctor.Diagnose(absTarget, spec)

	fmt.Printf("\nCodeFlow Environment Doctor: %s\n", absTarget)
	fmt.Println(strings.Repeat("=", 60))
	allPassed := true
	for _, r := range results {
		status := "✓"
		if !r.Passed {
			status = "✗"
			allPassed = false
		}
		fmt.Printf(" [%s] %-25s %s\n", status, r.Name, r.Message)
	}
	fmt.Println(strings.Repeat("=", 60))
	if !allPassed {
		os.Exit(1)
	}
}

func runInstallRecord(args []string) {
	fs := flag.NewFlagSet("install-record", flag.ContinueOnError)
	state := installstate.State{Version: 1}
	fs.StringVar(&state.Binary, "binary", "", "installed binary path")
	fs.StringVar(&state.SourceRoot, "source-root", "", "installer source path")
	fs.BoolVar(&state.OwnedSource, "owned-source", false, "whether the installer created source-root")
	fs.StringVar(&state.AdapterSpec, "adapter-spec", "", "adapter configuration")
	fs.StringVar(&state.SkillPath, "skill-path", "", "installed skill path")
	fs.StringVar(&state.SkillSHA256, "skill-sha256", "", "installed SKILL.md sha256")
	fs.StringVar(&state.MCPName, "mcp-name", "", "Codex MCP registration name")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		os.Exit(2)
	}
	if err := installstate.Save(state); err != nil {
		fmt.Fprintf(os.Stderr, "record installation: %v\n", err)
		os.Exit(1)
	}
}

func runUninstall(args []string) {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "usage: codeflow uninstall")
		os.Exit(2)
	}
	result, err := installation.Uninstall(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "codeflow uninstall: %v\n", err)
		os.Exit(1)
	}
	for _, item := range result.Removed {
		fmt.Printf("removed: %s\n", item)
	}
	for _, item := range result.Kept {
		fmt.Printf("kept: %s\n", item)
	}
}

// printFlowTable renders the ranked candidate list. RANK counts printed
// rows in final priority order; deduped/excluded/pinned rows carry a FLAGS
// tag so nothing harvested is silently hidden.
func printFlowTable(cs []harvest.Candidate) {
	if len(cs) == 0 {
		fmt.Println("no flow candidates found")
		return
	}
	fmt.Printf("%-4s  %-6s  %-18s  %-24s  %-52s  %s\n",
		"RANK", "SCORE", "CLASS", "DERIVED-NAME", "ENTRY", "FLAGS")
	for i, c := range cs {
		fmt.Printf("%-4d  %-6.3f  %-18s  %-24s  %-52s  %s\n",
			i+1,
			c.Score,
			truncateRunes(c.MarkerKind, 18),
			truncateRunes(c.IntentSignals.DerivedName, 24),
			truncateRunes(c.EntrySymbolPath, 52),
			flowFlags(c))
	}
}

func flowFlags(c harvest.Candidate) string {
	var flags []string
	switch c.ManifestOverride {
	case "pinned":
		flags = append(flags, "pinned")
	case "excluded":
		flags = append(flags, "excluded")
	}
	if c.DedupedInto != nil {
		flags = append(flags, "deduped")
	}
	if len(flags) == 0 {
		return "-"
	}
	return strings.Join(flags, "+")
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return string(r[:max-1]) + "…"
}
