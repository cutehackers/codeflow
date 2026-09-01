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

	ensureStarterLayersYaml(root, stdout)

	printSummary(stdout, res)
	return res, nil
}

func ensureStarterLayersYaml(root string, stdout io.Writer) {
	layersPath := filepath.Join(root, "codeflow.layers.yaml")
	if _, err := os.Stat(layersPath); err == nil {
		return // already exists, preserve developer configuration
	}

	pattern, _ := detect.DetectArchitecturePattern(root)
	starterContent := starterLayersYaml(pattern)

	if err := os.WriteFile(layersPath, []byte(starterContent), 0o644); err == nil {
		fmt.Fprintf(stdout, "  created starter codeflow.layers.yaml\n")
	}
}

func starterLayersYaml(pattern detect.ArchitecturePattern) string {
	switch pattern {
	case detect.PatternNextAppRouter:
		return `# CodeFlow Architecture Layers Configuration (Next.js App Router)
version: 1
strictOrder: true
allowUnknownLayer: false

layers:
  - name: presentation
    aliases: [ui, view, component, page, layout, screen, widget]
    pathPatterns: ["**/app/**/page.*", "**/app/**/layout.*", "**/app/**/template.*", "**/app/**/error.*", "**/app/**/loading.*", "**/components/**", "**/views/**", "**/ui/**"]

  - name: controller
    aliases: [controller, hook, context, store, slice, state, provider]
    pathPatterns: ["**/hooks/**", "**/contexts/**", "**/providers/**", "**/stores/**", "**/slices/**"]

  - name: usecase
    aliases: [usecase, service, action, handler, feature, route_handler]
    pathPatterns: ["**/actions/**", "**/app/api/**", "**/services/**", "**/lib/actions/**", "**/usecases/**"]

  - name: domain
    aliases: [domain, entity, model, schema, types]
    pathPatterns: ["**/types/**", "**/models/**", "**/schemas/**", "**/domain/**", "**/entities/**"]

  - name: data
    aliases: [data, repository, db, datasource, queries, dao]
    pathPatterns: ["**/db/**", "**/queries/**", "**/repositories/**", "**/data/**", "**/prisma/**", "**/drizzle/**"]

  - name: infra
    aliases: [infra, infrastructure, config, auth, middleware, platform]
    pathPatterns: ["**/middleware.*", "**/lib/auth.*", "**/config/**", "**/infra/**", "**/infrastructure/**"]

  - name: external
    aliases: [external, client, api, gateway, sdk, remote]
    pathPatterns: ["**/clients/**", "**/gateways/**", "**/lib/api.*", "**/services/external/**", "**/external/**", "**/api-client/**"]
`
	case detect.PatternFeatureSlicedDesign:
		return `# CodeFlow Architecture Layers Configuration (Feature-Sliced Design)
version: 1
strictOrder: true
allowUnknownLayer: false

layers:
  - name: presentation
    aliases: [presentation, page, widget, app, ui, view]
    pathPatterns: ["**/pages/**", "**/widgets/**", "**/app/**", "**/views/**", "**/features/**/ui/**", "**/entities/**/ui/**"]

  - name: controller
    aliases: [controller, feature, hook, store]
    pathPatterns: ["**/features/**/model/**", "**/entities/**/model/**", "**/shared/model/**", "**/features/**", "**/hooks/**", "**/stores/**"]

  - name: usecase
    aliases: [usecase, process, service, interactor]
    pathPatterns: ["**/features/**/api/**", "**/entities/**/api/**", "**/features/**/lib/**", "**/processes/**", "**/services/**", "**/usecases/**"]

  - name: domain
    aliases: [domain, entity, model, schema, types]
    pathPatterns: ["**/entities/**/types/**", "**/shared/types/**", "**/entities/**/model/types.*", "**/entities/**", "**/models/**", "**/types/**"]

  - name: data
    aliases: [data, repository, datasource, api_layer]
    pathPatterns: ["**/shared/api/**", "**/entities/**/api/**/repo.*", "**/repositories/**", "**/data/**"]

  - name: infra
    aliases: [infra, infrastructure, config, lib]
    pathPatterns: ["**/shared/config/**", "**/shared/lib/**", "**/infra/**"]

  - name: external
    aliases: [external, client, gateway, sdk]
    pathPatterns: ["**/shared/api/external/**", "**/shared/clients/**", "**/clients/**", "**/external/**"]
`
	case detect.PatternStandardReactSPA, detect.PatternGenericFrontend:
		return `# CodeFlow Architecture Layers Configuration (Standard React SPA)
version: 1
strictOrder: true
allowUnknownLayer: false

layers:
  - name: presentation
    aliases: [ui, view, component, page, screen]
    pathPatterns: ["**/components/**", "**/pages/**", "**/views/**", "**/screens/**"]

  - name: controller
    aliases: [controller, hook, context, store, slice, state]
    pathPatterns: ["**/hooks/**", "**/contexts/**", "**/stores/**", "**/slices/**", "**/reducers/**"]

  - name: usecase
    aliases: [usecase, service, interactor]
    pathPatterns: ["**/services/**", "**/usecases/**", "**/lib/services/**"]

  - name: domain
    aliases: [domain, entity, model, schema, types]
    pathPatterns: ["**/types/**", "**/models/**", "**/schemas/**", "**/entities/**"]

  - name: data
    aliases: [data, repository, query, datasource]
    pathPatterns: ["**/api/**", "**/repositories/**", "**/queries/**", "**/data/**"]

  - name: infra
    aliases: [infra, infrastructure, config, util, utils]
    pathPatterns: ["**/utils/**", "**/lib/utils/**", "**/config/**", "**/infra/**"]

  - name: external
    aliases: [external, client, gateway, sdk]
    pathPatterns: ["**/clients/**", "**/gateways/**", "**/external/**"]
`
	default:
		return `# CodeFlow Architecture Layers Configuration (§4.1.3)
version: 1
strictOrder: true
allowUnknownLayer: false

layers:
  - name: presentation
    aliases: [ui, view, widget, screen, page, component]
    pathPatterns: ["**/presentation/**", "**/ui/**", "**/views/**", "**/components/**", "**/screens/**"]

  - name: controller
    aliases: [controller, notifier, bloc, cubit, viewmodel, store, reducer]
    pathPatterns: ["**/controllers/**", "**/notifiers/**", "**/bloc/**", "**/stores/**", "**/viewmodels/**"]

  - name: usecase
    aliases: [usecase, use_case, service, interactor, command, query]
    pathPatterns: ["**/usecase/**", "**/usecases/**", "**/interactors/**", "**/domain/services/**"]

  - name: domain
    aliases: [domain, entity, model, aggregate, vo]
    pathPatterns: ["**/domain/**", "**/entities/**", "**/models/**"]

  - name: data
    aliases: [data, repository, datasource, data_source, dao]
    pathPatterns: ["**/data/**", "**/repositories/**", "**/datasources/**", "**/dao/**"]

  - name: infra
    aliases: [infra, infrastructure, platform, storage]
    pathPatterns: ["**/infra/**", "**/infrastructure/**", "**/storage/**", "**/platform/**"]

  - name: external
    aliases: [external, api, remote, client, gateway, network]
    pathPatterns: ["**/network/**", "**/api/**", "**/clients/**", "**/gateways/**"]
`
	}
}

func reportDetection(stdout io.Writer, root string, res *Result) {
	det := detect.Detect(root)
	res.Language = det.Language
	res.Confident = det.Confident
	res.ProjectName = det.ProjectName
	if res.ProjectName == "" {
		res.ProjectName = filepath.Base(root)
	}
	marker := detectMarker(root, det.Language)
	switch {
	case det.Confident && det.ProjectName != "":
		fmt.Fprintf(stdout, "detected %s project %q (%s)\n", det.Language, det.ProjectName, marker)
	case det.Confident:
		fmt.Fprintf(stdout, "detected %s project (%s without a name line)\n", det.Language, marker)
	default:
		fmt.Fprintf(stdout, "warn: no %s found; proceeding unconfident — language may be set later\n", marker)
	}
}

func detectMarker(root, lang string) string {
	switch lang {
	case "dart":
		return "pubspec.yaml"
	case "typescript":
		if _, err := os.Stat(filepath.Join(root, "package.json")); err == nil {
			return "package.json"
		}
		if _, err := os.Stat(filepath.Join(root, "tsconfig.json")); err == nil {
			return "tsconfig.json"
		}
		return "package.json"
	case "kotlin":
		if _, err := os.Stat(filepath.Join(root, "build.gradle.kts")); err == nil {
			return "build.gradle.kts"
		}
		if _, err := os.Stat(filepath.Join(root, "build.gradle")); err == nil {
			return "build.gradle"
		}
		return "pom.xml"
	case "swift":
		return "Package.swift"
	case "python":
		if _, err := os.Stat(filepath.Join(root, "pyproject.toml")); err == nil {
			return "pyproject.toml"
		}
		return "requirements.txt"
	case "go":
		return "go.mod"
	case "rust":
		return "Cargo.toml"
	default:
		return "project marker"
	}
}

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
