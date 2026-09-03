package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"codeflow/internal/workspace"
)

func runStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit workspace status as JSON")
	if err := fs.Parse(reorderFlags(fs, args)); err != nil {
		os.Exit(2)
	}

	target := "."
	posArgs := fs.Args()
	if len(posArgs) > 0 {
		target = posArgs[0]
	}

	absTarget, err := filepath.Abs(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve path: %v\n", err)
		os.Exit(1)
	}

	engine, err := workspace.NewSnapshotEngine(absTarget, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "snapshot engine: %v\n", err)
		os.Exit(1)
	}

	act := engine.CurrentActivity()

	if *jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(act)
		return
	}

	fmt.Printf("\n=== CodeFlow Workspace Status ===\n")
	fmt.Printf("Workspace Epoch:     %s\n", act.WorkspaceEpoch)
	fmt.Printf("Activity:            %s\n", act.Activity)
	if act.CurrentSnapshotID != "" {
		fmt.Printf("Current Snapshot:    %s\n", act.CurrentSnapshotID)
	} else {
		fmt.Printf("Current Snapshot:    (none)\n")
	}
	fmt.Printf("Pending Revisions:   %d\n", act.PendingRevisions)
	fmt.Printf("Analysis Lag:        %d ms\n", act.AnalysisLagMs)
	fmt.Println()
}
