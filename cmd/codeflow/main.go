// Command codeflow is the CodeFlow v2 core CLI (design-v2 §4.1): a single
// native binary orchestrating flow extraction, publishing, and serving.
package main

import (
	"flag"
	"fmt"
	"os"

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
  codeflow version         print version information

Planned (not implemented yet): flows, show, publish, serve, mcp
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
	case "version":
		fmt.Printf("codeflow %s (commit=%s, built=%s)\n", version, commit, date)
	case "flows", "show", "publish", "serve", "mcp":
		fmt.Fprintf(os.Stderr, "codeflow %s: not implemented yet\n", command)
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", command, usage)
		os.Exit(1)
	}
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
