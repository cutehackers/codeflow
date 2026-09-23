package e2e_test

import (
	"os"
	"path/filepath"
	"testing"

	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/slicing"
	"codeflow/internal/curator/semantic"
)

func TestCaughtAwaitPreservesRecoveryAndFinally(t *testing.T) {
	root := t.TempDir()
	source := `export async function checkout() {
  try { await reserve(); commit(); } catch { rollback(); } finally { release(); } complete();
}
async function reserve() {} function commit() {} function rollback() {} function release() {} function complete() {}`
	if err := os.WriteFile(filepath.Join(root, "checkout.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{"repoRoot": root, "candidateId": "cand-catchflow", "entrySymbolPath": "checkout.ts#checkout", "opts": map[string]any{"includeExecutionSemantics": true}}, &payload); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "caught-await-generation")
	sequence := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatalf("%v: %+v", err, model.Edges)
	}
	ids := map[string]string{}
	for _, step := range model.Steps {
		ids[step.Name] = step.StepID
	}
	has := func(kind, from, to string) bool {
		for _, edge := range model.Edges {
			if edge.Kind == kind && edge.ResolutionStatus == "resolved" && edge.FromStepID == ids[from] && edge.ToStepID == ids[to] {
				return true
			}
		}
		return false
	}
	for _, relation := range [][3]string{{"failure", "await reserve()", "rollback()"}, {"await_resume", "await reserve()", "commit()"}, {"control_flow", "commit()", "release()"}, {"control_flow", "rollback()", "release()"}, {"control_flow", "release()", "complete()"}} {
		if ids[relation[1]] == "" || ids[relation[2]] == "" || !has(relation[0], relation[1], relation[2]) {
			t.Fatalf("missing relation %v: %+v", relation, model.Edges)
		}
	}
}

func TestTerminalCompletionEntersFinallyWithoutNormalContinuation(t *testing.T) {
	for _, tc := range []struct {
		name, completion string
	}{
		{name: "return", completion: "return result;"},
		{name: "throw", completion: "throw reason;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			source := `export function finish() { try { ` + tc.completion + ` } finally { cleanup(); } after(); }
function cleanup() {} function after() {}`
			if err := os.WriteFile(filepath.Join(root, "finish.ts"), []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			pool, ctx, cancel := tsAdapterPool(t)
			defer cancel()
			defer pool.Close()
			var payload slicing.SlicedPayload
			if err := pool.Call(ctx, "slice", map[string]any{"repoRoot": root, "candidateId": "cand-terminalfinally", "entrySymbolPath": "finish.ts#finish", "opts": map[string]any{"includeExecutionSemantics": true}}, &payload); err != nil {
				t.Fatal(err)
			}
			spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
			if err != nil {
				t.Fatal(err)
			}
			model := semantic.ProjectFlowSpec(spec, "terminal-finally-generation")
			sequence := semantic.BuildFlowSequence(model)
			if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
				t.Fatalf("%v: %+v", err, model.Edges)
			}
			completion, cleanup, after := "", "", ""
			for _, step := range model.Steps {
				switch step.Name {
				case tc.completion:
					completion = step.StepID
				case "cleanup()":
					cleanup = step.StepID
				case "after()":
					after = step.StepID
				}
			}
			if completion == "" || cleanup == "" {
				t.Fatalf("terminal completion or cleanup omitted: %+v", model.Steps)
			}
			for _, edge := range model.Edges {
				if edge.Kind == "finally" && edge.ResolutionStatus == "resolved" && edge.FromStepID == completion && edge.ToStepID == cleanup {
					return
				}
				if after != "" && edge.Kind == "control_flow" && edge.FromStepID == cleanup && edge.ToStepID == after {
					t.Fatal("cleanup gained a normal successor after terminal completion")
				}
			}
			t.Fatalf("missing terminal finally relation: %+v", model.Edges)
		})
	}
}

func TestExplicitThrowPreservesCatchFinallyAndContinuation(t *testing.T) {
	root := t.TempDir()
	source := `export function checkout() {
  try { before(); throw reason; } catch { rollback(); } finally { release(); } complete();
}
function before() {} function rollback() {} function release() {} function complete() {}`
	if err := os.WriteFile(filepath.Join(root, "checkout.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{"repoRoot": root, "candidateId": "cand-throwcatch", "entrySymbolPath": "checkout.ts#checkout", "opts": map[string]any{"includeExecutionSemantics": true}}, &payload); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "throw-catch-generation")
	sequence := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatalf("%v: %+v", err, model.Edges)
	}
	ids := map[string]string{}
	for _, step := range model.Steps {
		ids[step.Name] = step.StepID
	}
	for _, relation := range [][3]string{{"failure", "throw reason;", "rollback()"}, {"control_flow", "rollback()", "release()"}, {"control_flow", "release()", "complete()"}} {
		found := false
		for _, edge := range model.Edges {
			found = found || (edge.Kind == relation[0] && edge.ResolutionStatus == "resolved" && edge.FromStepID == ids[relation[1]] && edge.ToStepID == ids[relation[2]])
		}
		if ids[relation[1]] == "" || ids[relation[2]] == "" || !found {
			t.Fatalf("missing relation %v: %+v", relation, model.Edges)
		}
	}
}

func TestCalledThrowPreservesCallerRecovery(t *testing.T) {
	root := t.TempDir()
	source := `export function checkout() {
  try { charge(); complete(); } catch { recover(); } after();
}
function charge() { throw failure; }
function complete() {}
function recover() {}
function after() {}`
	if err := os.WriteFile(filepath.Join(root, "checkout.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{
		"repoRoot": root, "candidateId": "cand-calledthrow", "entrySymbolPath": "checkout.ts#checkout",
		"opts": map[string]any{"includeExecutionSemantics": true},
	}, &payload); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "called-throw-generation")
	sequence := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatalf("called throw relation failed validation: %v", err)
	}
	ids := map[string]string{}
	for _, step := range model.Steps {
		ids[step.Name] = step.StepID
	}
	for _, edge := range model.Edges {
		if edge.Kind == "failure" && edge.ResolutionStatus == "resolved" && edge.FromStepID == ids["throw failure;"] && edge.ToStepID == ids["recover()"] {
			return
		}
	}
	t.Fatalf("called throw did not reach caller recovery: %+v", model.Edges)
}

func TestAsyncCallerCatchesDirectSynchronousThrow(t *testing.T) {
	root := t.TempDir()
	source := `export async function checkout() {
  try { charge(); complete(); } catch { recover(); } after();
}
function charge() { throw failure; }
function complete() {}
function recover() {}
function after() {}`
	if err := os.WriteFile(filepath.Join(root, "checkout.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{
		"repoRoot": root, "candidateId": "cand-asynccalledthrow", "entrySymbolPath": "checkout.ts#checkout",
		"opts": map[string]any{"includeExecutionSemantics": true},
	}, &payload); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "async-called-throw-generation")
	sequence := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatalf("async caller throw relation failed validation: %v", err)
	}
	ids := map[string]string{}
	for _, step := range model.Steps {
		if step.Name == "throw failure;" && step.Anchor.EnclosingSymbolPath == "charge" {
			ids["throw"] = step.StepID
		}
		if step.Name == "recover()" {
			ids["recover"] = step.StepID
		}
	}
	for _, edge := range model.Edges {
		if edge.Kind == "failure" && edge.ResolutionStatus == "resolved" && edge.FromStepID == ids["throw"] && edge.ToStepID == ids["recover"] {
			return
		}
	}
	t.Fatalf("direct synchronous throw did not reach async caller recovery: %+v", model.Edges)
}

func TestAsyncCallerPreservesSynchronousReturn(t *testing.T) {
	root := t.TempDir()
	source := `export async function checkout() {
  const amount = price(true);
  record(amount);
}
function price(flag: boolean) { if (flag) return 1; return 2; }
function record(value: number) { return value; }`
	if err := os.WriteFile(filepath.Join(root, "checkout.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{
		"repoRoot": root, "candidateId": "cand-asyncreturn", "entrySymbolPath": "checkout.ts#checkout",
		"opts": map[string]any{"includeExecutionSemantics": true},
	}, &payload); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "async-direct-return-generation")
	sequence := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatalf("async caller return relation failed validation: %v", err)
	}
	ids := map[string]string{}
	for _, step := range model.Steps {
		if step.Name == "return 1;" || step.Name == "return 2;" {
			ids[step.Name] = step.StepID
		}
		if step.Name == "amount = price(true)" {
			ids["assignment"] = step.StepID
		}
	}
	if ids["return 1;"] == "" || ids["return 2;"] == "" || ids["assignment"] == "" {
		t.Fatalf("return or async caller assignment omitted: %+v", model.Steps)
	}
	returns := map[string]bool{}
	for _, edge := range model.Edges {
		if edge.Kind == "return" && edge.ResolutionStatus == "resolved" && edge.ToStepID == ids["assignment"] {
			returns[edge.FromStepID] = true
		}
	}
	if !returns[ids["return 1;"]] || !returns[ids["return 2;"]] {
		t.Fatalf("direct synchronous return did not resume async caller: %+v", model.Edges)
	}
}

func TestAsyncCallerPreservesSynchronousReturnBeforeLaterAwait(t *testing.T) {
	root := t.TempDir()
	source := `export async function checkout() {
  const amount = price(true);
  await signal();
  record(amount);
}
function price(flag: boolean) { if (flag) return 1; return 2; }
async function signal() {} function record(value: number) {}`
	if err := os.WriteFile(filepath.Join(root, "checkout.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{
		"repoRoot": root, "candidateId": "cand-asyncreturnlaterawait", "entrySymbolPath": "checkout.ts#checkout",
		"opts": map[string]any{"includeExecutionSemantics": true},
	}, &payload); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "async-return-before-later-await-generation")
	sequence := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatalf("async caller return relation failed validation: %v", err)
	}
	ids := map[string]string{}
	for _, step := range model.Steps {
		if step.Name == "return 1;" || step.Name == "return 2;" {
			ids[step.Name] = step.StepID
		}
		if step.Name == "amount = price(true)" {
			ids["assignment"] = step.StepID
		}
	}
	for _, returnName := range []string{"return 1;", "return 2;"} {
		found := false
		for _, edge := range model.Edges {
			found = found || (edge.Kind == "return" && edge.ResolutionStatus == "resolved" && edge.FromStepID == ids[returnName] && edge.ToStepID == ids["assignment"])
		}
		if ids[returnName] == "" || ids["assignment"] == "" || !found {
			t.Fatalf("normal return before later await missing for %s: %+v", returnName, model.Edges)
		}
	}
}

func TestFinalizerThrowPreservesCallerRecovery(t *testing.T) {
	root := t.TempDir()
	source := `export function checkout() {
  try { charge(); complete(); } catch { recover(); } after();
}
function charge() { try { prepare(); } finally { throw failure; } }
function prepare() {}
function complete() {}
function recover() {}
function after() {}`
	if err := os.WriteFile(filepath.Join(root, "checkout.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{
		"repoRoot": root, "candidateId": "cand-finalizerthrow", "entrySymbolPath": "checkout.ts#checkout",
		"opts": map[string]any{"includeExecutionSemantics": true},
	}, &payload); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "finalizer-throw-generation")
	sequence := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatalf("finalizer throw relation failed validation: %v", err)
	}
	ids := map[string]string{}
	for _, step := range model.Steps {
		if step.Name == "throw failure;" && step.Anchor.EnclosingSymbolPath == "charge" {
			ids["throw"] = step.StepID
		}
		if step.Name == "recover()" {
			ids["recover"] = step.StepID
		}
	}
	for _, edge := range model.Edges {
		if edge.Kind == "failure" && edge.ResolutionStatus == "resolved" && edge.FromStepID == ids["throw"] && edge.ToStepID == ids["recover"] {
			return
		}
	}
	t.Fatalf("finalizer throw did not reach caller recovery: %+v", model.Edges)
}

func TestAwaitedAsyncThrowPreservesCallerRecovery(t *testing.T) {
	root := t.TempDir()
	source := `export async function checkout() {
  try { await charge(); complete(); } catch { recover(); } after();
}
async function charge() { throw failure; }
function complete() {}
function recover() {}
function after() {}`
	if err := os.WriteFile(filepath.Join(root, "checkout.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{
		"repoRoot": root, "candidateId": "cand-awaitedthrow", "entrySymbolPath": "checkout.ts#checkout",
		"opts": map[string]any{"includeExecutionSemantics": true},
	}, &payload); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "awaited-throw-generation")
	sequence := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatalf("awaited async throw relation failed validation: %v", err)
	}
	ids := map[string]string{}
	for _, step := range model.Steps {
		if step.Name == "throw failure;" && step.Anchor.EnclosingSymbolPath == "charge" {
			ids["throw"] = step.StepID
		}
		if step.Name == "recover()" {
			ids["recover"] = step.StepID
		}
	}
	for _, edge := range model.Edges {
		if edge.Kind == "failure" && edge.ResolutionStatus == "resolved" && edge.FromStepID == ids["throw"] && edge.ToStepID == ids["recover"] {
			return
		}
	}
	t.Fatalf("awaited async throw did not reach caller recovery: %+v", model.Edges)
}

func TestFinallyReturnResumesCallerWithOnlyTheFinalResult(t *testing.T) {
	root := t.TempDir()
	source := `export function checkout() {
  let result;
  result = finish();
  record(result);
}
function finish() { try { return original(); } finally { return replacement(); } }
function original() { return 1; }
function replacement() { return 2; }
function record(value: unknown) { return value; }`
	if err := os.WriteFile(filepath.Join(root, "checkout.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{
		"repoRoot": root, "candidateId": "cand-finallyreturn", "entrySymbolPath": "checkout.ts#checkout",
		"opts": map[string]any{"includeExecutionSemantics": true},
	}, &payload); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "finally-return-generation")
	sequence := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatalf("final return relation failed validation: %v", err)
	}
	ids := map[string]string{}
	for _, step := range model.Steps {
		ids[step.Name] = step.StepID
	}
	finalReturn, assignment := ids["return replacement();"], ids["result = finish()"]
	if finalReturn == "" || assignment == "" {
		t.Fatalf("final return or assignment omitted: %+v", model.Steps)
	}
	for _, edge := range model.Edges {
		if edge.Kind == "return" && edge.ResolutionStatus == "resolved" && edge.ToStepID == assignment && edge.FromStepID != finalReturn {
			t.Fatalf("non-final return resumed caller: %+v", edge)
		}
		if edge.Kind == "return" && edge.ResolutionStatus == "resolved" && edge.FromStepID == finalReturn && edge.ToStepID == assignment {
			return
		}
	}
	t.Fatalf("final return did not resume caller: %+v", model.Edges)
}

func TestStorageFailureImpactsTransactionAndCache(t *testing.T) {
	root := t.TempDir()
	source := `export async function processOrder() {
  beginTransaction();
  try {
    const saved = await persist();
    updateCache(saved);
    commitTransaction();
    return saved;
  } catch (err) {
    rollbackTransaction();
    invalidateCache();
    return null;
  } finally {
    releaseConnection();
  }
}
async function persist() { return 1; }
function beginTransaction() {}
function updateCache(val: any) {}
function commitTransaction() {}
function rollbackTransaction() {}
function invalidateCache() {}
function releaseConnection() {}`
	if err := os.WriteFile(filepath.Join(root, "storage_failure.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{
		"repoRoot":        root,
		"candidateId":     "cand-storagefail",
		"entrySymbolPath": "storage_failure.ts#processOrder",
		"opts":            map[string]any{"includeExecutionSemantics": true},
	}, &payload); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "storage-failure-generation")
	sequence := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatalf("hierarchy references invalid: %v", err)
	}

	ids := map[string]string{}
	for _, step := range model.Steps {
		ids[step.Name] = step.StepID
	}

	hasRelation := func(kind, from, to string) bool {
		for _, edge := range model.Edges {
			if edge.Kind == kind && edge.ResolutionStatus == "resolved" && edge.FromStepID == ids[from] && edge.ToStepID == ids[to] {
				return true
			}
		}
		return false
	}

	// 1. Verify storage failure triggers rollbackTransaction
	if !hasRelation("failure", "await persist()", "rollbackTransaction()") {
		t.Fatalf("expected failure edge from await persist() to rollbackTransaction(), got edges: %+v", model.Edges)
	}

	// 2. Verify rollbackTransaction leads to invalidateCache
	if !hasRelation("control_flow", "rollbackTransaction()", "invalidateCache()") {
		t.Fatalf("expected control_flow edge from rollbackTransaction() to invalidateCache(), got edges: %+v", model.Edges)
	}

	// 3. Verify normal await success leads to commitTransaction
	foundSuccessContinuation := false
	for _, edge := range model.Edges {
		if edge.Kind == "await_resume" && edge.ResolutionStatus == "resolved" && edge.FromStepID == ids["await persist()"] {
			foundSuccessContinuation = true
			break
		}
	}
	if !foundSuccessContinuation {
		t.Fatalf("expected await_resume edge from await persist(), got edges: %+v", model.Edges)
	}

	// 4. Verify releaseConnection is reached as finalizer
	foundFinalizerEntry := false
	for _, edge := range model.Edges {
		if edge.ToStepID == ids["releaseConnection()"] && edge.ResolutionStatus == "resolved" {
			foundFinalizerEntry = true
			break
		}
	}
	if !foundFinalizerEntry {
		t.Fatalf("expected edge entering finalizer releaseConnection(), got edges: %+v", model.Edges)
	}

	// 5. Verify FlowSequence frames preserve transaction and cache handling steps
	foundRollbackFrame := false
	foundInvalidateFrame := false
	for _, frame := range sequence.Frames {
		for _, sRef := range frame.StepRefs {
			if sRef == ids["rollbackTransaction()"] {
				foundRollbackFrame = true
			}
			if sRef == ids["invalidateCache()"] {
				foundInvalidateFrame = true
			}
		}
	}
	if !foundRollbackFrame || !foundInvalidateFrame {
		t.Fatalf("storage failure impact steps omitted from FlowSequence frames: rollback=%v invalidate=%v", foundRollbackFrame, foundInvalidateFrame)
	}
}
