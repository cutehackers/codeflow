// Command codeflow is the CodeFlow v2 core CLI (design-v2 §4.1): a single
// native binary orchestrating flow extraction, publishing, and serving.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"codeflow/internal/harvest"
	"codeflow/internal/initcmd"
)

// Populated at build time via -ldflags (Makefile build target).
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const usage = `codeflow — Business Flow First Engine

Usage:
  codeflow init [path]     prepare a repository: detect project, resolve
                           adapter pins, purge v1 remnants, create
                           .codeflow/workspace.json (default path: .)
  codeflow flows [path]    harvest flow candidates from the Dart project at
                           path (default .) and print them in automatic
                           score order (marker specificity x fan-in x
                           boundary reachability), honoring
                           codeflow.flows.yaml pin/exclude/rename.
                           Requires $CODEFLOW_ADAPTER_DART_BIN (absolute
                           adapter binary, or dartrun:<adapters/dart>).
                           Flag: --json (raw candidates JSON array)
  codeflow version         print version information

Planned (not implemented yet): show, publish, serve, mcp
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
	case "version":
		fmt.Printf("codeflow %s (commit=%s, built=%s)\n", version, commit, date)
	case "show", "publish", "serve", "mcp":
		fmt.Fprintf(os.Stderr, "codeflow %s: not implemented yet\n", command)
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", command, usage)
		os.Exit(1)
	}
}

func runFlows(args []string) {
	fs := flag.NewFlagSet("flows", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: codeflow flows [--json] [path]")
	}
	jsonOut := fs.Bool("json", false, "emit the raw candidates JSON array instead of a table")

	// The stdlib flag parser stops at the first positional argument, so
	// "flows <path> --json" would mis-parse. Hoist every flag-looking
	// argument to the front instead: flags may come before or after [path].
	var flagArgs, posArgs []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			flagArgs = append(flagArgs, a)
		} else {
			posArgs = append(posArgs, a)
		}
	}
	if err := fs.Parse(flagArgs); err != nil {
		os.Exit(2)
	}
	if len(posArgs) > 1 {
		fs.Usage()
		os.Exit(2)
	}
	target := "."
	if len(posArgs) == 1 {
		target = posArgs[0]
	}

	cfg, err := harvest.ResolveDartAdapter(os.Getenv(harvest.DartAdapterEnvVar))
	if err != nil {
		fatal(err)
	}
	runner := harvest.NewRunner(cfg, 1)
	defer runner.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	candidates, err := runner.Run(ctx, target)
	if err != nil {
		fatal(err)
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(candidates); err != nil {
			fatal(err)
		}
		return
	}
	printFlowTable(candidates)
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

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}

func runInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: codeflow init [path]")
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if fs.NArg() > 1 {
		fs.Usage()
		os.Exit(2)
	}
	target := "."
	if fs.NArg() == 1 {
		target = fs.Arg(0)
	}
	if _, err := initcmd.Run(target, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
