package semantic

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"codeflow/internal/collector/evidence"
	"codeflow/internal/collector/slicing"
)

func TestVS04CompilerRequiresSnapshotAndUsesStructuralEvidence(t *testing.T) {
	content := []byte("package demo\n\nfunc Submit() {\n\treturn\n}\n")
	start := bytes.Index(content, []byte("func Submit"))
	end := len(content)
	fileHash := sha256.Sum256(content)
	spanHash := sha256.Sum256(content[start:end])
	step := slicing.SliceStep{
		Ordinal: 1, Kind: "mutation", Description: "submit", SymbolPath: "demo.Submit",
		Anchor: slicing.Anchor{
			RepoRelativePath: "service.go", ByteRange: [2]int{start, end},
			FileHash: hex.EncodeToString(fileHash[:]), SpanHash: hex.EncodeToString(spanHash[:]),
			EnclosingSymbolPath: "demo.Submit", CanonicalAstFingerprint: "ast-submit-v1",
		},
	}
	target := &ResolvedTarget{FlowID: "flow-demo", CandidateID: "cand-demo", EntrySymbolPath: "service.go#demo.Submit", Title: "submit"}
	sliced := &slicing.SlicedPayload{CandidateID: target.CandidateID, EntrySymbolPath: target.EntrySymbolPath, Steps: []slicing.SliceStep{step}}
	intent, err := NormalizeTaskIntent("submit 흐름", IntentOptions{TaskID: "task-demo"})
	if err != nil {
		t.Fatal(err)
	}
	validatedSnapshot, err := evidence.SnapshotInputFromContent("snapshot-demo", "basis-demo", "tree-demo", "", "deps-demo", 4, map[string]string{"service.go": string(content)})
	if err != nil {
		t.Fatal(err)
	}
	result := attachValidatedVS02Result(t, sliced, validatedSnapshot, []string{"."})

	if _, _, err := CompileDeterministicFeatureMap(target, intent, sliced, CompileOptions{ComputedBasisID: "basis-demo", ValidatedAgainstSnapshotID: "snapshot-demo"}); err == nil {
		t.Fatal("compiler accepted a slice without immutable snapshot bytes")
	}

	mapIR, _, err := CompileDeterministicFeatureMap(target, intent, sliced, CompileOptions{
		ComputedBasisID: "basis-demo", WorkspaceEpoch: 4, GenerationID: "generation-demo",
		ValidatedAgainstSnapshotID: "snapshot-demo", SnapshotID: "snapshot-demo", SnapshotTreeID: "tree-demo",
		RepositoryID: "repo-demo", DependencyFingerprint: "deps-demo", AdapterVersion: result.AdapterVersion, AnalyzerRevision: result.AnalyzerRevision,
		AnalysisReadSetID: result.ReadSet.ReadSetID, CausalObservationClosureID: result.Closure.ClosureID,
		SnapshotFiles: map[string]string{"service.go": string(content)}, SnapshotInput: &validatedSnapshot,
	})
	if err != nil {
		t.Fatalf("strict compiler failed: %v", err)
	}
	if mapIR.Authority != "candidate" || mapIR.Freshness != "historical" || mapIR.Settlement == "passed" {
		t.Fatalf("compiler fabricated publication authority: authority=%q freshness=%q settlement=%q", mapIR.Authority, mapIR.Freshness, mapIR.Settlement)
	}
	if len(mapIR.Evidence) != 1 || len(mapIR.Steps[0].EvidenceRefs) != 1 || mapIR.Evidence[0].EvidenceID != mapIR.Steps[0].EvidenceRefs[0] {
		t.Fatalf("compiler did not bind evidence refs to canonical evidence: %+v", mapIR)
	}
	if mapIR.Steps[0].CodeLens.StartLine != 3 || mapIR.Steps[0].CodeLens.EndLine != 5 {
		t.Fatalf("CodeLens was not derived from snapshot newlines: %+v", mapIR.Steps[0].CodeLens)
	}
	if mapIR.Steps[0].StepID == "" || mapIR.Steps[0].StructuralIdentity == "" {
		t.Fatal("strict compiler omitted structural identity")
	}

	shifted := []byte("package demo\n\n\nfunc Submit() {\n\treturn\n}\n")
	shiftedStart := bytes.Index(shifted, []byte("func Submit"))
	shiftedFileHash := sha256.Sum256(shifted)
	shiftedSpanHash := sha256.Sum256(shifted[shiftedStart:])
	shiftedStep := step
	shiftedStep.Anchor.ByteRange = [2]int{shiftedStart, len(shifted)}
	shiftedStep.Anchor.FileHash = hex.EncodeToString(shiftedFileHash[:])
	shiftedStep.Anchor.SpanHash = hex.EncodeToString(shiftedSpanHash[:])
	shiftedStep.Anchor.CanonicalAstFingerprint = "ast-submit-v2"
	shiftedPayload := &slicing.SlicedPayload{CandidateID: target.CandidateID, EntrySymbolPath: target.EntrySymbolPath, Steps: []slicing.SliceStep{shiftedStep}}
	shiftedInput := func() *evidence.SnapshotInput {
		s, _ := evidence.SnapshotInputFromContent("snapshot-demo-2", "basis-demo-2", "tree-demo-2", "", "deps-demo-2", 5, map[string]string{"service.go": string(shifted)})
		return &s
	}()
	shiftedResult := attachValidatedVS02Result(t, shiftedPayload, *shiftedInput, []string{"."})
	shiftedMap, _, err := CompileDeterministicFeatureMap(target, intent, shiftedPayload, CompileOptions{
		ComputedBasisID: "basis-demo-2", WorkspaceEpoch: 5, GenerationID: "generation-demo-2", ValidatedAgainstSnapshotID: "snapshot-demo-2", SnapshotID: "snapshot-demo-2", SnapshotTreeID: "tree-demo-2", RepositoryID: "repo-demo", DependencyFingerprint: "deps-demo-2", SnapshotFiles: map[string]string{"service.go": string(shifted)}, SnapshotInput: func() *evidence.SnapshotInput {
			s, _ := evidence.SnapshotInputFromContent("snapshot-demo-2", "basis-demo-2", "tree-demo-2", "", "deps-demo-2", 5, map[string]string{"service.go": string(shifted)})
			return &s
		}(), AdapterVersion: shiftedResult.AdapterVersion, AnalyzerRevision: shiftedResult.AnalyzerRevision, AnalysisReadSetID: shiftedResult.ReadSet.ReadSetID, CausalObservationClosureID: shiftedResult.Closure.ClosureID,
	})
	if err != nil {
		t.Fatalf("shifted strict compiler failed: %v", err)
	}
	if shiftedMap.Steps[0].StepID != mapIR.Steps[0].StepID {
		t.Fatalf("line move changed structural step identity: before=%s after=%s", mapIR.Steps[0].StepID, shiftedMap.Steps[0].StepID)
	}
}

func TestVS04CompilerRequiresValidatedVS02SliceMetadata(t *testing.T) {
	content := []byte("package demo\n\nfunc Submit() {\n\treturn\n}\n")
	start := bytes.Index(content, []byte("func Submit"))
	fileHash := sha256.Sum256(content)
	spanHash := sha256.Sum256(content[start:])
	step := slicing.SliceStep{
		Ordinal: 1, Kind: "mutation", Description: "submit", SymbolPath: "demo.Submit",
		Anchor: slicing.Anchor{
			RepoRelativePath: "service.go", ByteRange: [2]int{start, len(content)},
			FileHash: hex.EncodeToString(fileHash[:]), SpanHash: hex.EncodeToString(spanHash[:]),
			EnclosingSymbolPath: "demo.Submit", CanonicalAstFingerprint: "ast-submit-v1",
		},
	}
	target := &ResolvedTarget{FlowID: "flow-metadata", CandidateID: "cand-metadata", EntrySymbolPath: "service.go#demo.Submit", Title: "submit"}
	sliced := &slicing.SlicedPayload{CandidateID: target.CandidateID, EntrySymbolPath: target.EntrySymbolPath, Steps: []slicing.SliceStep{step}}
	input, err := evidence.SnapshotInputFromContent("snapshot-metadata", "basis-metadata", "tree-metadata", "", "deps-metadata", 7, map[string]string{"service.go": string(content)})
	if err != nil {
		t.Fatal(err)
	}
	opts := CompileOptions{
		ComputedBasisID: "basis-metadata", WorkspaceEpoch: 7,
		ValidatedAgainstSnapshotID: input.SnapshotID, SnapshotID: input.SnapshotID, SnapshotTreeID: input.RootTreeID,
		RepositoryID: "repo-metadata", DependencyFingerprint: input.DependencyFingerprint,
		SnapshotFiles: map[string]string{"service.go": string(content)}, SnapshotInput: &input,
	}
	_, _, err = CompileDeterministicFeatureMap(target, nil, sliced, opts)
	if err == nil || !strings.Contains(err.Error(), "analysis read-set") {
		t.Fatalf("compiler accepted unvalidated VS02 slice metadata: %v", err)
	}
}

func TestVS04CompilerRejectsUnconfirmedIntent(t *testing.T) {
	content := []byte("package demo\n\nfunc Submit() { return }\n")
	start := bytes.Index(content, []byte("func Submit"))
	fileHash := sha256.Sum256(content)
	spanHash := sha256.Sum256(content[start:])
	payload := &slicing.SlicedPayload{CandidateID: "cand-confirm", EntrySymbolPath: "demo.Submit", Steps: []slicing.SliceStep{{
		Ordinal: 1, Kind: "mutation", Description: "submit", SymbolPath: "demo.Submit",
		Anchor: slicing.Anchor{RepoRelativePath: "service.go", ByteRange: [2]int{start, len(content)}, FileHash: hex.EncodeToString(fileHash[:]), SpanHash: hex.EncodeToString(spanHash[:]), EnclosingSymbolPath: "demo.Submit", CanonicalAstFingerprint: "ast-submit"},
	}}}
	input, err := evidence.SnapshotInputFromContent("snapshot-confirm", "basis-confirm", "tree-confirm", "", "deps-confirm", 1, map[string]string{"service.go": string(content)})
	if err != nil {
		t.Fatal(err)
	}
	result := attachValidatedVS02Result(t, payload, input, []string{"."})
	intent, err := NormalizeTaskIntent("submit인지 preview인지", IntentOptions{Mode: "feature", TaskID: "task-confirm"})
	if err != nil {
		t.Fatal(err)
	}
	target := &ResolvedTarget{FlowID: "flow-confirm", CandidateID: "cand-confirm", EntrySymbolPath: "demo.Submit", Title: "submit"}
	_, _, err = CompileDeterministicFeatureMap(target, intent, payload, CompileOptions{ComputedBasisID: input.ComputedBasisID, WorkspaceEpoch: input.WorkspaceEpoch, GenerationID: "generation-confirm", ValidatedAgainstSnapshotID: input.SnapshotID, SnapshotID: input.SnapshotID, SnapshotTreeID: input.RootTreeID, RepositoryID: "repo-confirm", DependencyFingerprint: input.DependencyFingerprint, AdapterVersion: result.AdapterVersion, AnalyzerRevision: result.AnalyzerRevision, AnalysisReadSetID: result.ReadSet.ReadSetID, CausalObservationClosureID: result.Closure.ClosureID, SnapshotFiles: map[string]string{"service.go": string(content)}, SnapshotInput: &input})
	if err == nil || !strings.Contains(err.Error(), ErrCodeAmbiguousTarget) {
		t.Fatalf("compiler accepted needs_confirmation intent: %v", err)
	}
}

func TestVS04CompilerKeepsAmbiguousRelationAsUnknownBoundary(t *testing.T) {
	target := &ResolvedTarget{FlowID: "flow-relation-ambiguous", CandidateID: "cand-relation01", EntrySymbolPath: "lib/service.dart#Service.start", Title: "relation"}
	first := makeStep(1, "first", "call")
	second := makeStep(2, "second", "call")
	first.SymbolPath, second.SymbolPath = "Shared.handle", "Shared.handle"
	first.Anchor.EnclosingSymbolPath, second.Anchor.EnclosingSymbolPath = "Shared.First", "Shared.Second"
	toStep := 1
	payload := &slicing.SlicedPayload{CandidateID: target.CandidateID, EntrySymbolPath: target.EntrySymbolPath, Steps: []slicing.SliceStep{first, second}, Edges: []slicing.SliceEdge{{Kind: "resolved_cross_file", ToSymbolPath: "lib/service.dart#Shared.handle", ResolutionStatus: "resolved", Depth: 1, StepOrdinal: &toStep}}}
	mapIR, _, err := CompileDeterministicFeatureMap(target, nil, payload, strictCompileOptions(t, payload, "basis-relation-ambiguous", 1))
	if err != nil {
		t.Fatalf("ambiguous relation should not fail the whole feature query: %v", err)
	}
	if len(mapIR.Edges) != 0 || len(mapIR.BoundaryTargets) != 1 || len(mapIR.Unknowns) == 0 {
		t.Fatalf("ambiguous relation was not retained as an unknown boundary: %+v", mapIR)
	}
}
