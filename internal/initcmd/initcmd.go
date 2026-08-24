// Package initcmd orchestrates `codeflow init`: resolve the target repo,
// detect the project, purge v1 remnants (fresh start, decision #16),
// load-or-create the workspace manifest with resolved adapter pins, and
// print a summary. Running it twice on the same repo is idempotent.
package initcmd

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"codeflow/internal/detect"
	"codeflow/internal/freshstart"
	"codeflow/internal/pin"
	"codeflow/internal/workspace"
)

// Result reports what a single init run observed and changed.
type Result struct {
	RepoRoot    string
	ProjectName string
	Language    string
	Confident   bool
	Pins        map[string]string
	UpdatedPins []string
	PurgedCount int
	Created     bool // true when workspace.json did not exist before this run
}

// Run executes the full init flow against repoRoot, writing progress and a
// summary to stdout (io.Discard when nil).
func Run(repoRoot string, stdout io.Writer) (*Result, error) {
	if stdout == nil {
		stdout = io.Discard
	}
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("init: resolve path: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("init: target repository: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("init: not a directory: %s", root)
	}

	res := &Result{RepoRoot: root}
	reportDetection(stdout, root, res)

	purged, err := scanAndPurge(stdout, root)
	if err != nil {
		return nil, err
	}
	res.PurgedCount = purged

	ws, created, err := loadOrCreate(root)
	if err != nil {
		return nil, err
	}
	res.Created = created

	registry, err := pin.Default()
	if err != nil {
		return nil, err
	}
	for _, adapter := range registry.Names() {
		version, err := registry.Resolve(adapter)
		if err != nil {
			return nil, err
		}
		if existing, ok := ws.AdapterPins[adapter]; !ok || existing != version {
			ws.SetPin(adapter, version)
			res.UpdatedPins = append(res.UpdatedPins, adapter)
		}
	}
	res.Pins = maps.Clone(ws.AdapterPins)

	if err := ws.Validate(); err != nil {
		return nil, fmt.Errorf("init: %w", err)
	}
	if err := ws.Save(); err != nil {
		return nil, fmt.Errorf("init: %w", err)
	}

	printSummary(stdout, res)
	return res, nil
}

func reportDetection(stdout io.Writer, root string, res *Result) {
	det := detect.Detect(root)
	res.Language = det.Language
	res.Confident = det.Confident
	res.ProjectName = det.ProjectName
	if res.ProjectName == "" {
		res.ProjectName = filepath.Base(root)
	}
	switch {
	case det.Confident && det.ProjectName != "":
		fmt.Fprintf(stdout, "detected %s project %q (%s)\n", det.Language, det.ProjectName, pubspecHint)
	case det.Confident:
		fmt.Fprintf(stdout, "detected %s project (%s without a name line)\n", det.Language, pubspecHint)
	default:
		fmt.Fprintf(stdout, "warn: no pubspec.yaml found; proceeding unconfident — language may be set later\n")
	}
}

const pubspecHint = "pubspec.yaml"

func scanAndPurge(stdout io.Writer, root string) (int, error) {
	remnants, err := freshstart.ScanV1Remnants(root)
	if err != nil {
		return 0, fmt.Errorf("init: %w", err)
	}
	if len(remnants) == 0 {
		return 0, nil
	}
	fmt.Fprintf(stdout, "found %d v1 remnant(s); removing (fresh start):\n", len(remnants))
	for _, remnant := range remnants {
		rel, relErr := filepath.Rel(root, remnant)
		if relErr != nil {
			rel = remnant
		}
		fmt.Fprintf(stdout, "  removed %s\n", filepath.ToSlash(rel))
	}
	if err := freshstart.Purge(root, remnants); err != nil {
		return 0, fmt.Errorf("init: %w", err)
	}
	return len(remnants), nil
}

func loadOrCreate(root string) (*workspace.Workspace, bool, error) {
	if workspace.Exists(root) {
		ws, err := workspace.Load(root)
		if err != nil {
			return nil, false, fmt.Errorf("init: %w", err)
		}
		return ws, false, nil
	}
	return workspace.New(root), true, nil
}

func printSummary(stdout io.Writer, res *Result) {
	state := "updated"
	if res.Created {
		state = "created"
	}
	fmt.Fprintln(stdout, "codeflow init complete")
	fmt.Fprintf(stdout, "  project : %s\n", res.ProjectName)
	fmt.Fprintf(stdout, "  language: %s\n", languageLabel(res))
	for _, adapter := range slices.Sorted(maps.Keys(res.Pins)) {
		suffix := ""
		if !res.Created && slices.Contains(res.UpdatedPins, adapter) {
			suffix = " (pin updated)"
		}
		fmt.Fprintf(stdout, "  pins    : %s@%s%s\n", adapter, res.Pins[adapter], suffix)
	}
	fmt.Fprintf(stdout, "  purged  : %d v1 remnant(s)\n", res.PurgedCount)
	fmt.Fprintf(stdout, "  storage : %s/%s (%s)\n", workspace.DirName, workspace.FileName, state)
	fmt.Fprintf(stdout, "  next    : run 'codeflow flows' to list discovered flows (not implemented yet)\n")
}

func languageLabel(res *Result) string {
	if res.Confident {
		return res.Language
	}
	return res.Language + " (unconfident)"
}
