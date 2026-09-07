package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"codeflow/internal/contractharness"
	"codeflow/internal/detect"
	"codeflow/internal/harvest"
	"codeflow/internal/protocol"
	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
	"codeflow/internal/workspace"
)

func runQuery(args []string) {
	code := executeQuery(args, os.Stdout, os.Stderr)
	if code != 0 {
		os.Exit(code)
	}
}

func executeQuery(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("query", flag.ContinueOnError)
	fs.SetOutput(stderr)

	modeFlag := fs.String("mode", "feature", "Query mode (feature, review, impact, debug)")
	reqFlag := fs.String("request", "", "Natural language user request")
	entryFlag := fs.String("entry", "", "Entry symbol path")
	flowFlag := fs.String("flow", "", "Flow ID")
	domainFlag := fs.String("domain", "", "Domain filter")
	jsonFlag := fs.Bool("json", false, "Emit structured JSON output")

	reordered := reorderFlags(fs, args)
	if err := fs.Parse(reordered); err != nil {
		return 1
	}

	repoPath := "."
	if fs.NArg() > 0 {
		repoPath = fs.Arg(0)
	}
	absRoot, err := filepath.Abs(repoPath)
	if err != nil {
		fmt.Fprintf(stderr, "resolve path %s: %v\n", repoPath, err)
		return 1
	}

	query := &semantic.TaskViewQuery{
		SchemaID:      "https://codeflow.local/schemas/task-view-query.schema.json",
		SchemaVersion: 1,
		Mode:          *modeFlag,
		Feature: &semantic.FeatureQueryParams{
			Request:     *reqFlag,
			EntrySymbol: *entryFlag,
			FlowID:      *flowFlag,
			Domain:      *domainFlag,
		},
	}

	qBytes, _ := json.Marshal(query)
	if err := contractharness.ValidateTaskViewQuery(qBytes); err != nil {
		fmt.Fprintf(stderr, "error [%s]: %v\n", semantic.ErrCodeMissingPrecondition, err)
		return 1
	}

	ctx := context.Background()
	engine, err := workspace.NewSnapshotEngine(absRoot, 0)
	if err != nil {
		fmt.Fprintf(stderr, "initialize workspace snapshot: %v\n", err)
		return 1
	}
	head, err := engine.Reconcile(ctx, nil)
	if err != nil {
		fmt.Fprintf(stderr, "capture workspace snapshot: %v\n", err)
		return 1
	}
	lease, err := engine.SnapshotVFS(head.SnapshotID)
	if err != nil {
		fmt.Fprintf(stderr, "retain workspace snapshot: %v\n", err)
		return 1
	}
	defer lease.Close()
	snapshot, err := protocol.SnapshotFromLease(lease)
	if err != nil {
		fmt.Fprintf(stderr, "convert workspace snapshot: %v\n", err)
		return 1
	}

	det := detect.DetectSnapshot(snapshot.Files)
	lang := det.Language
	if lang == "" || lang == "unknown" {
		lang = "typescript"
	}

	adapterCfg, err := harvest.ResolveAdapter(lang, "")
	if err != nil {
		fmt.Fprintf(stderr, "resolve adapter: %v\n", err)
		return 1
	}

	pool := protocol.NewPool(adapterCfg, 2)
	defer pool.Close()

	harvester := harvest.NewRunnerWithPool(pool)
	candidates, err := harvester.RunWithSnapshot(ctx, absRoot, snapshot)
	if err != nil {
		fmt.Fprintf(stderr, "harvest candidates: %v\n", err)
		return 1
	}

	target, err := semantic.ResolveFeatureQueryTarget(query, candidates)
	if err != nil {
		var qErr *semantic.QueryError
		if errors.As(err, &qErr) {
			fmt.Fprintf(stderr, "error [%s]: %s\n", qErr.Code, qErr.Message)
			if len(qErr.CandidateTargets) > 0 {
				fmt.Fprintf(stderr, "candidate targets:\n")
				for _, c := range qErr.CandidateTargets {
					fmt.Fprintf(stderr, "  - %s\n", c)
				}
			}
			return 1
		}
		fmt.Fprintf(stderr, "resolve query target: %v\n", err)
		return 1
	}

	slicer := slicing.NewRunner(pool)
	slicePayload, err := slicer.SliceWithSnapshot(ctx, absRoot, target.CandidateID, target.EntrySymbolPath, nil, snapshot)
	if err != nil {
		fmt.Fprintf(stderr, "slice failed: %v\n", err)
		return 1
	}

	reqText := *reqFlag
	if reqText == "" {
		reqText = target.Title
	}

	intent, err := semantic.NormalizeTaskIntent(reqText, semantic.IntentOptions{
		Mode: *modeFlag,
	})
	if err != nil {
		fmt.Fprintf(stderr, "normalize intent: %v\n", err)
		return 1
	}
	snapshotInput, err := snapshot.AnalyzerInput()
	if err != nil {
		fmt.Fprintf(stderr, "validated snapshot input: %v\n", err)
		return 1
	}

	mapIR, proj, err := semantic.CompileDeterministicFeatureMap(target, intent, slicePayload, semantic.CompileOptions{
		ComputedBasisID: snapshot.ComputedBasisID, WorkspaceEpoch: snapshot.WorkspaceEpoch,
		ValidatedAgainstSnapshotID: snapshot.SnapshotID, SnapshotID: snapshot.SnapshotID, SnapshotTreeID: snapshot.RootTreeID,
		RepositoryID: absRoot, DependencyFingerprint: snapshot.DependencyFingerprint, ConfigurationFingerprint: snapshot.ConfigurationFingerprint,
		AdapterVersion: slicePayload.AdapterVersion, AnalyzerRevision: slicePayload.AnalyzerVersion,
		AnalysisReadSetID: semantic.MetadataString(slicePayload.AnalysisReadSet, "readSetId"), CausalObservationClosureID: semantic.MetadataString(slicePayload.CausalObservationClosure, "closureId"),
		SnapshotFiles: snapshot.Files, SnapshotInput: &snapshotInput,
	})
	if err != nil {
		fmt.Fprintf(stderr, "compile map: %v\n", err)
		return 1
	}
	mapBytes, err := json.Marshal(mapIR)
	if err != nil {
		fmt.Fprintf(stderr, "marshal map: %v\n", err)
		return 1
	}
	if err := contractharness.ValidateSemanticMapIR(mapBytes); err != nil {
		fmt.Fprintf(stderr, "semantic map contract: %v\n", err)
		return 1
	}
	projectionBytes, err := json.Marshal(proj)
	if err != nil {
		fmt.Fprintf(stderr, "marshal projection: %v\n", err)
		return 1
	}
	if err := contractharness.ValidateFlowViewProjection(projectionBytes); err != nil {
		fmt.Fprintf(stderr, "projection contract: %v\n", err)
		return 1
	}

	evidenceRecords, _ := semantic.ExtractAndRedactEvidenceFromProtocolSnapshot(target, slicePayload, snapshot)

	if *jsonFlag {
		output := map[string]any{
			"candidateAnswer": map[string]string{
				"requested":  mapIR.Summary.Requested,
				"candidate":  mapIR.Summary.Current,
				"authority":  mapIR.Authority,
				"freshness":  mapIR.Freshness,
				"settlement": mapIR.Settlement,
			},
			"taskIntent":          intent,
			"agentReportedStatus": nil,
			"semanticMap":         mapIR,
			"projection":          proj,
			"evidence":            evidenceRecords,
			"unknowns":            mapIR.Unknowns,
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(output)
		return 0
	}

	// Human readable output format: Answer -> Flow Rail -> Evidence Dock -> Unknowns
	fmt.Fprintf(stdout, "=== Candidate Answer ===\n")
	fmt.Fprintf(stdout, "Requested: %s\n", mapIR.Summary.Requested)
	fmt.Fprintf(stdout, "Candidate: %s\n", mapIR.Summary.Current)
	fmt.Fprintf(stdout, "Quality:   %s (basis: %s, epoch: %d)\n\n", mapIR.Quality.Stage, mapIR.ComputedBasisID, mapIR.Basis.WorkspaceEpoch)

	fmt.Fprintf(stdout, "=== Semantic Flow Rail ===\n")
	stepLookup := make(map[string]semantic.SemanticStep)
	for _, s := range mapIR.Steps {
		stepLookup[s.StepID] = s
	}

	for _, stepRef := range proj.VisibleStepRefs {
		if s, ok := stepLookup[stepRef]; ok {
			layerStr := ""
			if s.Layer != "" {
				layerStr = fmt.Sprintf("[%s] ", s.Layer)
			}
			kindStr := ""
			if s.Kind != "" {
				kindStr = fmt.Sprintf(" (%s)", s.Kind)
			}
			fmt.Fprintf(stdout, "  %2d. %s%s - %s%s\n", s.Ordinal, layerStr, s.Name, s.TechnicalName, kindStr)
		}
	}
	for _, f := range proj.FoldedSubflows {
		fmt.Fprintf(stdout, "      ... [folded %d subflow steps between %s -> %s]\n", f.HiddenCount, f.EntryStepRef, f.ExitStepRef)
	}
	fmt.Fprintln(stdout)

	fmt.Fprintf(stdout, "=== Evidence Dock ===\n")
	if len(evidenceRecords) == 0 {
		fmt.Fprintf(stdout, "  (no direct source evidence anchors)\n")
	} else {
		for _, ev := range evidenceRecords {
			cl := ev.CodeLens
			if cl != nil {
				fmt.Fprintf(stdout, "  [Anchor] %s:%d-%d (view: %d-%d) [%s]\n",
					cl.Path, cl.StartLine, cl.EndLine, cl.ViewStartLine, cl.ViewEndLine, ev.RedactionStatus)
			} else {
				fmt.Fprintf(stdout, "  [Anchor] %s [%s]\n", ev.Anchor.RepoRelativePath, ev.RedactionStatus)
			}
		}
	}
	fmt.Fprintln(stdout)

	fmt.Fprintf(stdout, "=== Unknowns ===\n")
	if len(mapIR.Unknowns) == 0 {
		fmt.Fprintf(stdout, "  None (fully verified)\n")
	} else {
		for _, u := range mapIR.Unknowns {
			fmt.Fprintf(stdout, "  - %s: %s\n", u.Subject, u.Reason)
		}
	}

	return 0
}
