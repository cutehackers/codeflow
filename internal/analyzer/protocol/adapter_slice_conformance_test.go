package protocol_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codeflow/internal/analyzer/protocol"
	"codeflow/internal/collector/slicing"
)

// TestAdapterSliceEvidenceConformance verifies that Dart, Go, and TypeScript adapters
// produce slicing evidence meeting the identical baseline criteria (VS-05, A1-A6):
// valid byte ranges, correct SHA-256 hashes, valid execution references, expected
// statement types, and absence of prohibited transitions.
func TestAdapterSliceEvidenceConformance(t *testing.T) {
	root := protocol.RepoRootDir(t)

	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node runtime required for TypeScript adapter conformance: %v", err)
	}
	dartPath, err := exec.LookPath("dart")
	if err != nil {
		t.Fatalf("dart runtime required for Dart adapter conformance: %v", err)
	}
	goAdapterPath := protocol.ProvideGoAdapterBinary(t)

	adapterConfigs := map[string]protocol.Config{
		"Dart": {
			BinPath: dartPath,
			Args:    []string{"run", filepath.Join(root, "adapters", "dart", "bin", "codeflow_dart_adapter.dart")},
		},
		"Go": {
			BinPath: goAdapterPath,
		},
		"TypeScript": {
			BinPath: nodePath,
			Args:    []string{filepath.Join(root, "adapters", "typescript", "bin", "codeflow_ts_adapter.js")},
		},
	}

	t.Run("Dart", func(t *testing.T) {
		pool := protocol.NewPool(adapterConfigs["Dart"], 1)
		defer pool.Close()
		runner := slicing.NewRunner(pool)

		const dartSource = `class CheckoutService {
  int processCheckout(int amount) {
    if (amount <= 0) {
      return 0;
    }
    var fee = amount * 10;
    var total = amount + fee;
    return total;
  }

  int otherMethod() {
    return -1;
  }
}
`
		files := map[string]string{
			"pubspec.yaml":      "name: dart_conformance\nenvironment:\n  sdk: '>=3.0.0 <4.0.0'\n",
			"lib/checkout.dart": dartSource,
		}
		snapshot, err := protocol.NewSnapshot(1, files, "dart-conformance-basis")
		if err != nil {
			t.Fatalf("create snapshot: %v", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		payload, err := runner.SliceWithSnapshot(ctx, t.TempDir(), "cand-dartconf001", "lib/checkout.dart#CheckoutService.processCheckout", map[string]any{
			"includeExecutionSemantics": true,
		}, snapshot)
		if err != nil {
			t.Fatalf("Dart slice failed: %v", err)
		}

		verifyCommonSliceEvidence(t, payload, "lib/checkout.dart", dartSource, "CheckoutService.processCheckout")
		verifyFixtureBranchRelations(t, payload, dartSource)
	})

	t.Run("DartDirectArgument", func(t *testing.T) {
		pool := protocol.NewPool(adapterConfigs["Dart"], 1)
		defer pool.Close()
		runner := slicing.NewRunner(pool)
		const source = `class Rules {
  static List<int> getValidMoves(int seed) {
    final moves = <int>[];
    moves.addAll(getValidMovesFrom(seed));
    return moves;
  }
  static List<int> getValidMovesFrom(int seed) { return [seed]; }
}
`
		files := map[string]string{
			"pubspec.yaml":   "name: dart_argument_conformance\n",
			"lib/rules.dart": source,
		}
		snapshot, err := protocol.NewSnapshot(1, files, "dart-argument-basis")
		if err != nil {
			t.Fatalf("create snapshot: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		payload, err := runner.SliceWithSnapshot(ctx, t.TempDir(), "cand-dartarg001", "lib/rules.dart#Rules.getValidMoves", nil, snapshot)
		if err != nil {
			t.Fatalf("slice direct argument: %v", err)
		}
		var argument, collection *slicing.SliceStep
		for i := range payload.Steps {
			step := &payload.Steps[i]
			rangeInFile := step.Anchor.ByteRange
			if rangeInFile[0] < 0 || rangeInFile[1] > len(source) || rangeInFile[0] >= rangeInFile[1] {
				t.Fatalf("invalid source range: %+v", step)
			}
			code := source[rangeInFile[0]:rangeInFile[1]]
			switch code {
			case "getValidMovesFrom(seed)":
				argument = step
			case "moves.addAll(getValidMovesFrom(seed));":
				collection = step
			}
		}
		if argument == nil || collection == nil || argument.Kind != "call" || collection.Kind != "mutation" || argument.Ordinal >= collection.Ordinal {
			t.Fatalf("argument evaluation or collection mutation lost: argument=%+v collection=%+v", argument, collection)
		}
		if argument.FlowContext == nil || argument.FlowContext.Statement.ByteRange != argument.Anchor.ByteRange || collection.FlowContext == nil {
			t.Fatal("direct argument source proof missing")
		}
		resolved := false
		for _, edge := range payload.Edges {
			if strings.Contains(edge.ToSymbolPath, "unresolved_dynamic.addAll") {
				t.Fatalf("collection call invented a dynamic target: %+v", edge)
			}
			if edge.Kind == "resolved_cross_file" && edge.StepOrdinal != nil && *edge.StepOrdinal == argument.Ordinal && edge.ToSymbolPath == "lib/rules.dart#Rules.getValidMovesFrom" {
				resolved = true
			}
		}
		if !resolved {
			t.Fatal("direct argument did not enter its implementation")
		}
	})

	t.Run("DartEagerTransformation", func(t *testing.T) {
		pool := protocol.NewPool(adapterConfigs["Dart"], 1)
		defer pool.Close()
		const source = `class Rules {
  static int calculate(int seed) => seed * 2;
  static bool check() => true;
  static int run(int seed, bool enabled) {
    final adjusted = calculate(seed) + 1;
    final ready = enabled && check();
    return ready ? adjusted : 0;
  }
}
`
		snapshot, err := protocol.NewSnapshot(1, map[string]string{
			"pubspec.yaml":   "name: dart_eager_conformance\n",
			"lib/rules.dart": source,
		}, "dart-eager-basis")
		if err != nil {
			t.Fatalf("create snapshot: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		payload, err := slicing.NewRunner(pool).SliceWithSnapshot(ctx, t.TempDir(), "cand-darteager001", "lib/rules.dart#Rules.run", nil, snapshot)
		if err != nil {
			t.Fatalf("slice eager transformation: %v", err)
		}
		var call, assignment, condition, check, ready, checkResult *slicing.SliceStep
		for i := range payload.Steps {
			step := &payload.Steps[i]
			code := source[step.Anchor.ByteRange[0]:step.Anchor.ByteRange[1]]
			switch code {
			case "calculate(seed)":
				call = step
			case "final adjusted = calculate(seed) + 1;":
				assignment = step
			case "enabled":
				condition = step
			case "check()":
				check = step
			case "final ready = enabled && check();":
				ready = step
			case "true":
				if step.Anchor.EnclosingSymbolPath == "Rules.check" {
					checkResult = step
				}
			}
		}
		if call == nil || assignment == nil || condition == nil || check == nil || ready == nil || checkResult == nil || call.Ordinal >= assignment.Ordinal || call.FlowContext == nil || condition.FlowContext == nil || check.FlowContext == nil || checkResult.FlowContext == nil || assignment.AssignmentSourceOrdinal != nil {
			t.Fatalf("eager or conditional source proof invalid: call=%+v assignment=%+v condition=%+v check=%+v ready=%+v result=%+v", call, assignment, condition, check, ready, checkResult)
		}
		resolved, resolvedCheck, conditional, skipped := false, false, false, false
		for _, edge := range payload.Edges {
			if edge.Kind == "resolved_cross_file" && edge.StepOrdinal != nil && *edge.StepOrdinal == call.Ordinal && edge.ToSymbolPath == "lib/rules.dart#Rules.calculate" {
				resolved = true
			}
			if edge.Kind == "resolved_cross_file" && edge.StepOrdinal != nil && *edge.StepOrdinal == check.Ordinal && edge.TargetStepOrdinal != nil && *edge.TargetStepOrdinal == checkResult.Ordinal && edge.ToSymbolPath == "lib/rules.dart#Rules.check" {
				resolvedCheck = true
			}
			if edge.Kind == "control_flow" && edge.StepOrdinal != nil && edge.TargetStepOrdinal != nil && *edge.StepOrdinal == condition.Ordinal && len(edge.Conditions) == 1 {
				if *edge.TargetStepOrdinal == check.Ordinal && edge.Conditions[0].Outcome == "truthy" {
					conditional = true
				}
				if *edge.TargetStepOrdinal == ready.Ordinal && edge.Conditions[0].Outcome == "falsy" {
					skipped = true
				}
			}
		}
		if !resolved || !resolvedCheck || !conditional || !skipped {
			t.Fatalf("eager or conditional execution relation missing: resolved=%v resolvedCheck=%v conditional=%v skipped=%v", resolved, resolvedCheck, conditional, skipped)
		}
	})

	t.Run("Go", func(t *testing.T) {
		pool := protocol.NewPool(adapterConfigs["Go"], 1)
		defer pool.Close()
		runner := slicing.NewRunner(pool)

		const goSource = `package order

type OrderService struct{}

func (s *OrderService) ProcessOrder(amount int) int {
	if amount <= 0 {
		return 0
	}
	fee := amount * 10
	total := amount + fee
	return total
}

type OtherService struct{}

func (s *OtherService) ProcessOrder(amount int) int {
	return -1
}
`
		files := map[string]string{
			"go.mod":   "module conformance.order\n\ngo 1.22\n",
			"order.go": goSource,
		}
		snapshot, err := protocol.NewSnapshot(1, files, "go-conformance-basis")
		if err != nil {
			t.Fatalf("create snapshot: %v", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		payload, err := runner.SliceWithSnapshot(ctx, t.TempDir(), "cand-goconf00001", "order.go#OrderService.ProcessOrder", map[string]any{
			"includeExecutionSemantics": true,
		}, snapshot)
		if err != nil {
			t.Fatalf("Go slice failed: %v", err)
		}

		verifyCommonSliceEvidence(t, payload, "order.go", goSource, "OrderService.ProcessOrder")
		verifyFixtureBranchRelations(t, payload, goSource)
	})

	t.Run("TypeScript", func(t *testing.T) {
		pool := protocol.NewPool(adapterConfigs["TypeScript"], 1)
		defer pool.Close()
		runner := slicing.NewRunner(pool)

		const tsSource = `export class CheckoutService {
  total: number = 0;
  processCheckout(amount: number): number {
    if (amount <= 0) {
      return 0;
    }
    this.total = amount * 10;
    return this.total;
  }

  otherMethod(): number {
    return -1;
  }
}
`
		files := map[string]string{
			"package.json":    "{\"name\": \"ts-conformance\"}\n",
			"src/checkout.ts": tsSource,
		}
		snapshot, err := protocol.NewSnapshot(1, files, "ts-conformance-basis")
		if err != nil {
			t.Fatalf("create snapshot: %v", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		payload, err := runner.SliceWithSnapshot(ctx, t.TempDir(), "cand-tsconf000001", "src/checkout.ts#CheckoutService.processCheckout", map[string]any{
			"includeExecutionSemantics": true,
		}, snapshot)
		if err != nil {
			t.Fatalf("TypeScript slice failed: %v", err)
		}

		verifyCommonSliceEvidence(t, payload, "src/checkout.ts", tsSource, "CheckoutService.processCheckout")
		verifyFixtureBranchRelations(t, payload, tsSource)
	})

	t.Run("NegativeIntegrityChecks", func(t *testing.T) {
		// A3: Check that corrupted file hash or span hash fails verification
		const sampleSource = "void test() { return; }"
		fileHash := sha256Hex(sampleSource)

		validStep := slicing.SliceStep{
			Ordinal:    1,
			Kind:       "return",
			SymbolPath: "test",
			Anchor: slicing.Anchor{
				RepoRelativePath:    "test.dart",
				ByteRange:           [2]int{14, 22},
				FileHash:            fileHash,
				SpanHash:            sha256Hex(sampleSource[14:22]),
				EnclosingSymbolPath: "test",
			},
		}

		// 1. Corrupted FileHash must be rejected
		corruptFileHashStep := validStep
		corruptFileHashStep.Anchor.FileHash = "0000000000000000000000000000000000000000000000000000000000000000"
		if err := verifyStepIntegrity(corruptFileHashStep, "test.dart", sampleSource); err == nil {
			t.Errorf("expected error for corrupted FileHash, got nil")
		}

		// 2. Corrupted SpanHash must be rejected
		corruptSpanHashStep := validStep
		corruptSpanHashStep.Anchor.SpanHash = "1111111111111111111111111111111111111111111111111111111111111111"
		if err := verifyStepIntegrity(corruptSpanHashStep, "test.dart", sampleSource); err == nil {
			t.Errorf("expected error for corrupted SpanHash, got nil")
		}

		// 3. Whole-file fallback range must be rejected
		wholeFileStep := validStep
		wholeFileStep.Anchor.ByteRange = [2]int{0, len(sampleSource)}
		if err := verifyStepIntegrity(wholeFileStep, "test.dart", sampleSource); err == nil {
			t.Errorf("expected error for whole-file fallback byteRange, got nil")
		}

		// 4. Invalid execution references must be rejected by ValidateExecutionReferences
		badEdge := slicing.SliceEdge{
			Kind:              "control_flow",
			StepOrdinal:       intPtr(1),
			TargetStepOrdinal: intPtr(999), // non-existent target step
			ResolutionStatus:  "resolved",
			ToSymbolPath:      "test.dart#test",
		}
		if err := slicing.ValidateExecutionReferences([]slicing.SliceStep{validStep}, []slicing.SliceEdge{badEdge}); err == nil {
			t.Errorf("expected error from ValidateExecutionReferences for non-existent target step, got nil")
		}

		// 5. Prohibited normal edge exiting return statement must be rejected
		normalEdgeExitingReturn := slicing.SliceEdge{
			Kind:              "control_flow",
			StepOrdinal:       intPtr(1),
			TargetStepOrdinal: intPtr(2),
			ResolutionStatus:  "resolved",
			ToSymbolPath:      "test.dart#test",
		}
		step2 := validStep
		step2.Ordinal = 2
		step2.Kind = "call"
		if err := slicing.ValidateExecutionReferences([]slicing.SliceStep{validStep, step2}, []slicing.SliceEdge{normalEdgeExitingReturn}); err == nil {
			t.Errorf("expected error from ValidateExecutionReferences for normal flow exiting return step, got nil")
		}
	})
}

// TestTypeScriptEventAndAsyncSlicingConformance verifies that TypeScript/TSX event
// handler entry points and async error boundaries conform to exact source ranges
// and execution relations (VS-07, A1-A5).
func TestTypeScriptEventAndAsyncSlicingConformance(t *testing.T) {
	root := protocol.RepoRootDir(t)
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node runtime required: %v", err)
	}

	cfg := protocol.Config{
		BinPath: nodePath,
		Args:    []string{filepath.Join(root, "adapters", "typescript", "bin", "codeflow_ts_adapter.js")},
	}
	pool := protocol.NewPool(cfg, 1)
	defer pool.Close()
	runner := slicing.NewRunner(pool)

	const tsxSource = `import React, { useState } from 'react';

export const HomePage = () => {
  const [status, setStatus] = useState('idle');

  const onQuickCheckout = async (orderId: string) => {
    if (!orderId) {
      return;
    }
    try {
      await paymentService.charge(orderId);
      setStatus('paid');
    } catch (err) {
      setStatus('failed');
    }
  };

  const onOtherAction = () => {};

  return (
    <div>
      <button onClick={() => onQuickCheckout('123')}>Checkout</button>
    </div>
  );
};

export const OtherPage = () => {
  const onQuickCheckout = () => {
    return 'other';
  };
  return <div />;
};
`
	files := map[string]string{
		"package.json": "{\"name\": \"tsx-conformance\"}\n",
		"app/page.tsx": tsxSource,
	}
	snapshot, err := protocol.NewSnapshot(1, files, "tsx-conformance-basis")
	if err != nil {
		t.Fatalf("create snapshot: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	payload, err := runner.SliceWithSnapshot(ctx, t.TempDir(), "cand-tsxconf000001", "app/page.tsx#HomePage.onQuickCheckout", map[string]any{
		"includeExecutionSemantics": true,
	}, snapshot)
	if err != nil {
		t.Fatalf("TSX slice failed: %v", err)
	}

	if payload.CandidateID != "cand-tsxconf000001" {
		t.Errorf("CandidateID = %q, want cand-tsxconf000001", payload.CandidateID)
	}
	if payload.EntrySymbolPath != "app/page.tsx#HomePage.onQuickCheckout" {
		t.Errorf("EntrySymbolPath = %q, want app/page.tsx#HomePage.onQuickCheckout", payload.EntrySymbolPath)
	}

	expectedFileHash := sha256Hex(tsxSource)
	var hasGuard, hasAwait, hasCatchStep bool
	var awaitOrdinal, catchOrdinal int
	for _, step := range payload.Steps {
		if step.Anchor.RepoRelativePath != "app/page.tsx" {
			t.Errorf("step %d: RepoRelativePath = %q, want app/page.tsx", step.Ordinal, step.Anchor.RepoRelativePath)
		}
		if step.Anchor.ByteRange[0] == 0 && step.Anchor.ByteRange[1] >= len(tsxSource) {
			t.Errorf("step %d: whole-file fallback byteRange is prohibited", step.Ordinal)
		}
		if step.Anchor.FileHash != expectedFileHash {
			t.Errorf("step %d: FileHash mismatch", step.Ordinal)
		}
		spanBytes := tsxSource[step.Anchor.ByteRange[0]:step.Anchor.ByteRange[1]]
		if step.Anchor.SpanHash != sha256Hex(spanBytes) {
			t.Errorf("step %d: SpanHash mismatch", step.Ordinal)
		}

		// Disambiguation: must belong to HomePage, never OtherPage
		enclosing := step.Anchor.EnclosingSymbolPath
		if enclosing == "" {
			enclosing = step.SymbolPath
		}
		if strings.Contains(enclosing, "OtherPage") {
			t.Errorf("step %d: wrongly matched OtherPage symbol: %q", step.Ordinal, enclosing)
		}

		if (step.Kind == "guard" || step.Kind == "branch") && (strings.Contains(step.Description, "!orderId") || (step.GuardCondition != nil && strings.Contains(*step.GuardCondition, "!orderId"))) {
			hasGuard = true
		}
		if step.Kind == "await" && strings.Contains(step.Description, "paymentService.charge") {
			hasAwait = true
			awaitOrdinal = step.Ordinal
		}
		if strings.Contains(step.Description, "setStatus('failed')") {
			hasCatchStep = true
			catchOrdinal = step.Ordinal
		}
	}

	if !hasGuard {
		t.Errorf("missing guard step for !orderId")
	}
	if !hasAwait {
		t.Errorf("missing await step for paymentService.charge")
	}
	if !hasCatchStep {
		t.Errorf("missing catch handler step for setStatus('failed')")
	}

	// Validate execution references across all steps and edges
	if err := slicing.ValidateCollectedExecutionReferences(payload.Steps, payload.Edges); err != nil {
		t.Errorf("ValidateCollectedExecutionReferences failed: %v", err)
	}

	// A failure edge from another statement does not prove await recovery.
	var hasFailureEdge bool
	for _, edge := range payload.Edges {
		if edge.Kind == "failure" && edge.StepOrdinal != nil && *edge.StepOrdinal == awaitOrdinal &&
			edge.TargetStepOrdinal != nil && *edge.TargetStepOrdinal == catchOrdinal {
			hasFailureEdge = true
			break
		}
	}
	if !hasFailureEdge {
		t.Errorf("expected failure relation edge connecting rejected await to catch block")
	}
}

func verifyFixtureBranchRelations(t *testing.T, payload *slicing.SlicedPayload, source string) {
	t.Helper()
	find := func(fragment string) int {
		t.Helper()
		for _, step := range payload.Steps {
			span := step.Anchor.ByteRange
			if span[0] >= 0 && span[0] < span[1] && span[1] <= len(source) &&
				strings.Contains(source[span[0]:span[1]], fragment) {
				return step.Ordinal
			}
		}
		t.Fatalf("missing source statement %q", fragment)
		return 0
	}
	guard := find("amount <= 0")
	earlyReturn := find("return 0")
	mutation := find("amount * 10")
	for _, expectation := range []struct {
		from, to int
		outcome  string
	}{{guard, earlyReturn, "truthy"}, {guard, mutation, "falsy"}} {
		found := false
		for _, edge := range payload.Edges {
			if edge.Kind != "control_flow" || edge.StepOrdinal == nil || *edge.StepOrdinal != expectation.from ||
				edge.TargetStepOrdinal == nil || *edge.TargetStepOrdinal != expectation.to {
				continue
			}
			if len(edge.Conditions) == 1 && edge.Conditions[0].Outcome == expectation.outcome {
				found = true
			}
		}
		if !found {
			t.Errorf("missing proven branch %d -> %d (%s)", expectation.from, expectation.to, expectation.outcome)
		}
	}
	for _, edge := range payload.Edges {
		if edge.Kind != "control_flow" || edge.StepOrdinal == nil || *edge.StepOrdinal != guard ||
			edge.TargetStepOrdinal == nil {
			continue
		}
		if *edge.TargetStepOrdinal == earlyReturn && (len(edge.Conditions) != 1 || edge.Conditions[0].Outcome != "truthy") {
			t.Error("early return is incorrectly reachable without the true guard outcome")
		}
		if *edge.TargetStepOrdinal == mutation && (len(edge.Conditions) != 1 || edge.Conditions[0].Outcome != "falsy") {
			t.Error("mutation is incorrectly reachable without the false guard outcome")
		}
	}
}

func verifyCommonSliceEvidence(t *testing.T, payload *slicing.SlicedPayload, expectedRelPath, sourceContent, expectedSymbol string) {
	t.Helper()

	if len(payload.Steps) < 3 {
		t.Fatalf("expected at least 3 steps, got %d", len(payload.Steps))
	}

	expectedFileHash := sha256Hex(sourceContent)
	var hasBranch, hasMutation, hasReturn bool

	for _, step := range payload.Steps {
		// Verify step anchor
		if step.Anchor.RepoRelativePath != expectedRelPath {
			t.Errorf("step %d: RepoRelativePath = %q, want %q", step.Ordinal, step.Anchor.RepoRelativePath, expectedRelPath)
		}
		if step.Anchor.ByteRange[0] < 0 || step.Anchor.ByteRange[1] > len(sourceContent) || step.Anchor.ByteRange[0] >= step.Anchor.ByteRange[1] {
			t.Errorf("step %d: invalid ByteRange %v for source len %d", step.Ordinal, step.Anchor.ByteRange, len(sourceContent))
		}
		// Whole-file fallback check
		if step.Anchor.ByteRange[0] == 0 && step.Anchor.ByteRange[1] >= len(sourceContent) {
			t.Errorf("step %d: whole-file fallback byteRange prohibited %v", step.Ordinal, step.Anchor.ByteRange)
		}
		// SHA-256 hash checks
		if step.Anchor.FileHash != expectedFileHash {
			t.Errorf("step %d: FileHash = %q, want %q", step.Ordinal, step.Anchor.FileHash, expectedFileHash)
		}
		spanBytes := sourceContent[step.Anchor.ByteRange[0]:step.Anchor.ByteRange[1]]
		expectedSpanHash := sha256Hex(spanBytes)
		if step.Anchor.SpanHash != expectedSpanHash {
			t.Errorf("step %d: SpanHash = %q, want %q for %q", step.Ordinal, step.Anchor.SpanHash, expectedSpanHash, spanBytes)
		}

		// Disambiguation check: symbol path must match expected symbol
		enclosing := step.Anchor.EnclosingSymbolPath
		if enclosing == "" {
			enclosing = step.SymbolPath
		}
		if !strings.HasSuffix(enclosing, expectedSymbol) && !strings.Contains(expectedSymbol, enclosing) {
			t.Errorf("step %d: EnclosingSymbolPath = %q, want %q", step.Ordinal, enclosing, expectedSymbol)
		}

		// Categorize kinds
		switch step.Kind {
		case "guard", "branch":
			hasBranch = true
		case "mutation":
			hasMutation = true
		case "return":
			hasReturn = true
		}
	}

	if !hasBranch {
		t.Errorf("payload missing branch/guard step")
	}
	if !hasMutation {
		t.Errorf("payload missing mutation/assignment step")
	}
	if !hasReturn {
		t.Errorf("payload missing return step")
	}

	// Validate execution references across all steps and edges
	if err := slicing.ValidateCollectedExecutionReferences(payload.Steps, payload.Edges); err != nil {
		t.Errorf("ValidateCollectedExecutionReferences failed: %v", err)
	}

	// Verify prohibited transitions: no normal control_flow leaving a return or throw step
	returnOrdinals := make(map[int]bool)
	for _, step := range payload.Steps {
		if step.Kind == "return" || step.Kind == "throw" {
			returnOrdinals[step.Ordinal] = true
		}
	}
	for _, edge := range payload.Edges {
		if edge.StepOrdinal != nil && returnOrdinals[*edge.StepOrdinal] {
			if edge.Kind == "control_flow" {
				t.Errorf("prohibited control_flow edge leaving terminal step %d", *edge.StepOrdinal)
			}
		}
	}
}

func verifyStepIntegrity(step slicing.SliceStep, expectedRelPath, sourceContent string) error {
	if step.Anchor.RepoRelativePath != expectedRelPath {
		return &integrityError{msg: "path mismatch"}
	}
	if step.Anchor.ByteRange[0] == 0 && step.Anchor.ByteRange[1] >= len(sourceContent) {
		return &integrityError{msg: "whole-file fallback byteRange"}
	}
	expectedFileHash := sha256Hex(sourceContent)
	if step.Anchor.FileHash != expectedFileHash {
		return &integrityError{msg: "file hash mismatch"}
	}
	spanBytes := sourceContent[step.Anchor.ByteRange[0]:step.Anchor.ByteRange[1]]
	expectedSpanHash := sha256Hex(spanBytes)
	if step.Anchor.SpanHash != expectedSpanHash {
		return &integrityError{msg: "span hash mismatch"}
	}
	return nil
}

type integrityError struct {
	msg string
}

func (e *integrityError) Error() string {
	return e.msg
}

func sha256Hex(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

func intPtr(i int) *int {
	return &i
}
