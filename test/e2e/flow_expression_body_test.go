package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/slicing"
	"codeflow/internal/curator/semantic"
)

func TestExpressionBodyCallsPreserveReturnAndConditions(t *testing.T) {
	root := t.TempDir()
	source := `export const price = (value: number) => value * 2;
export const choose = (ready: boolean) => ready ? price(2) : price(3);
export function start() { return choose(true); }
`
	if err := os.WriteFile(filepath.Join(root, "flow.ts"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{"repoRoot": root, "candidateId": "cand-expression", "entrySymbolPath": "flow.ts#start", "opts": map[string]any{"includeExecutionSemantics": true}}, &payload); err != nil {
		t.Fatal(err)
	}
	if err := slicing.ValidateExecutionReferences(payload.Steps, payload.Edges); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, step := range payload.Steps {
		kinds[step.SymbolPath+":"+step.Kind]++
		r := step.Anchor.ByteRange
		if r[0] < 0 || r[1] > len(source) || r[0] >= r[1] {
			t.Fatalf("invalid source range: %+v", step.Anchor)
		}
	}
	if kinds["start:call"] != 1 || kinds["start:return"] != 1 || kinds["choose:branch"] != 1 || kinds["choose:call"] != 2 || kinds["choose:return"] != 1 || kinds["price:return"] != 2 {
		t.Fatalf("expression execution missing: %v", kinds)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "expression-generation")
	seq := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, seq); err != nil {
		t.Fatal(err)
	}
	outcomes := map[string]int{}
	for _, edge := range model.Edges {
		for _, condition := range edge.Conditions {
			outcomes[condition.Outcome]++
		}
	}
	if outcomes["truthy"] != 1 || outcomes["falsy"] != 1 {
		t.Fatalf("conditional expression paths lost: %v", outcomes)
	}
	parents := map[string]string{}
	for _, frame := range seq.Frames {
		for _, id := range frame.StepRefs {
			if parents[id] != "" {
				t.Fatal("duplicate expression step")
			}
			parents[id] = frame.FrameID
		}
	}
	roles := make(map[string]string)
	for _, frame := range seq.Frames {
		roles[frame.FrameID] = frame.Role
	}
	for _, step := range model.Steps {
		if step.Anchor.EnclosingSymbolPath == "choose" {
			expected := "process"
			if step.Kind == "branch" {
				expected = "decision"
			}
			if roles[parents[step.StepID]] != expected {
				t.Fatalf("conditional execution misclassified: %s -> %s", step.Name, roles[parents[step.StepID]])
			}
		}
	}
	if len(parents) != 8 {
		t.Fatalf("got %d execution steps", len(parents))
	}
}

func TestAwaitRetryLoopSurvivesProjection(t *testing.T) {
	root := t.TempDir()
	source := `async function retryRequest() { return true; }
export async function retryUntilDone(done: boolean) {
  while (!done) { await retryRequest(); }
  finish();
}
function finish() {}`
	if err := os.WriteFile(filepath.Join(root, "retry.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{"repoRoot": root, "candidateId": "cand-awaitretry", "entrySymbolPath": "retry.ts#retryUntilDone", "opts": map[string]any{"includeExecutionSemantics": true}}, &payload); err != nil {
		t.Fatal(err)
	}
	if err := slicing.ValidateExecutionReferences(payload.Steps, payload.Edges); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "await-retry-generation")
	sequence := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, step := range model.Steps {
		ids[step.Name] = step.StepID
	}
	for _, relation := range [][3]string{{"await_loop_back", "await retryRequest()", "while (!done)"}, {"control_flow", "while (!done)", "finish()"}} {
		found := false
		for _, edge := range model.Edges {
			found = found || (edge.Kind == relation[0] && edge.ResolutionStatus == "resolved" && edge.FromStepID == ids[relation[1]] && edge.ToStepID == ids[relation[2]])
		}
		if ids[relation[1]] == "" || ids[relation[2]] == "" || !found {
			t.Fatalf("missing relation %v: %+v", relation, model.Edges)
		}
	}
}

func TestRecoveredAwaitRetryLoopSurvivesProjection(t *testing.T) {
	root := t.TempDir()
	source := `async function retryRequest() { return true; }
export async function retryUntilDone(done: boolean) {
  while (!done) {
    try { await retryRequest(); } catch { recover(); }
  }
  finish();
}
function recover() {} function finish() {}`
	if err := os.WriteFile(filepath.Join(root, "retry.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{"repoRoot": root, "candidateId": "cand-recoveredawaitretry", "entrySymbolPath": "retry.ts#retryUntilDone", "opts": map[string]any{"includeExecutionSemantics": true}}, &payload); err != nil {
		t.Fatal(err)
	}
	if err := slicing.ValidateExecutionReferences(payload.Steps, payload.Edges); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "recovered-await-retry-generation")
	sequence := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, step := range model.Steps {
		ids[step.Name] = step.StepID
	}
	for _, relation := range [][3]string{
		{"await_loop_back", "await retryRequest()", "while (!done)"},
		{"failure", "await retryRequest()", "recover()"},
		{"loop_back", "recover()", "while (!done)"},
		{"control_flow", "while (!done)", "finish()"},
	} {
		found := false
		for _, edge := range model.Edges {
			found = found || (edge.Kind == relation[0] && edge.ResolutionStatus == "resolved" && edge.FromStepID == ids[relation[1]] && edge.ToStepID == ids[relation[2]])
		}
		if ids[relation[1]] == "" || ids[relation[2]] == "" || !found {
			t.Fatalf("missing relation %v: %+v", relation, model.Edges)
		}
	}
}

func TestCompoundGuardKeepsIndependentDecisions(t *testing.T) {
	root := t.TempDir()
	source := `export function checkout(authenticated: boolean, authorized: boolean, limit: number) {
 if (!authenticated || !authorized) return "denied";
 if (limit < 1) return "limit";
 return "accepted";
 }`
	if err := os.WriteFile(filepath.Join(root, "flow.ts"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{"repoRoot": root, "candidateId": "cand-guardcondition", "entrySymbolPath": "flow.ts#checkout", "opts": map[string]any{"includeExecutionSemantics": true}}, &payload); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "guard-generation")
	seq := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, seq); err != nil {
		t.Fatal(err)
	}
	if len(model.Steps) != 6 || len(seq.Frames) != 5 {
		t.Fatalf("steps=%+v frames=%+v", model.Steps, seq.Frames)
	}
	first := seq.Frames[0]
	if len(first.StepRefs) != 2 || first.Role != "entry" {
		t.Fatalf("guard evaluation not grouped: %+v", first)
	}
	for _, frame := range seq.Frames[1:] {
		if len(frame.StepRefs) != 1 {
			t.Fatalf("independent handling absorbed: %+v", frame)
		}
	}
	outcomes := map[string]int{}
	for _, edge := range model.Edges {
		for _, condition := range edge.Conditions {
			outcomes[condition.Outcome]++
		}
	}
	if outcomes["truthy"] != 3 || outcomes["falsy"] != 3 {
		t.Fatalf("conditions lost: %v", outcomes)
	}
}

func TestWhileConditionRelationsSurviveProjection(t *testing.T) {
	root := t.TempDir()
	source := `function check(count: number) { return count < 2; }
export function retry(count: number) { while(check(count)) { count++; if(count === 1) return count; } return -1; }`
	if err := os.WriteFile(filepath.Join(root, "retry.ts"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{"repoRoot": root, "candidateId": "cand-whileflow", "entrySymbolPath": "retry.ts#retry", "opts": map[string]any{"includeExecutionSemantics": true}}, &payload); err != nil {
		t.Fatal(err)
	}
	if err := slicing.ValidateExecutionReferences(payload.Steps, payload.Edges); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "while-generation")
	sequence := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatal(err)
	}
	byID := map[string]semantic.SemanticStep{}
	for _, step := range model.Steps {
		byID[step.StepID] = step
	}
	back := 0
	for _, edge := range model.Edges {
		if edge.Kind != "loop_back" {
			continue
		}
		back++
		if edge.ResolutionStatus != "resolved" || byID[edge.ToStepID].Name != "check(count)" || byID[edge.FromStepID].Kind != "guard" || len(edge.Conditions) != 1 || edge.Conditions[0].Outcome != "falsy" {
			t.Fatalf("incorrect loop relation: %+v", edge)
		}
	}
	if back != 1 || len(model.Edges) != len(payload.Edges) {
		t.Fatalf("loop facts lost: back=%d", back)
	}
}

func TestLoopControlTransfersSurviveProjection(t *testing.T) {
	root := t.TempDir()
	source := `export function retry(count: number) { while(count < 3) { count++; if(count === 1) continue; break; } return count; }`
	if err := os.WriteFile(filepath.Join(root, "retry.ts"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{"repoRoot": root, "candidateId": "cand-loopcontrol", "entrySymbolPath": "retry.ts#retry", "opts": map[string]any{"includeExecutionSemantics": true}}, &payload); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "loop-control-generation")
	if err := semantic.ValidateFlowSequenceReferences(model, semantic.BuildFlowSequence(model)); err != nil {
		t.Fatal(err)
	}
	steps := map[string]semantic.SemanticStep{}
	for _, step := range model.Steps {
		steps[step.StepID] = step
	}
	kinds := map[string]int{}
	for _, edge := range model.Edges {
		if edge.Kind == "loop_back" {
			if steps[edge.FromStepID].Kind != "continue" || steps[edge.ToStepID].Name != "while (count < 3)" {
				t.Fatalf("incorrect continue: %+v", edge)
			}
		}
		if edge.Kind == "loop_exit" {
			if steps[edge.FromStepID].Kind != "break" || steps[edge.ToStepID].Name != "return count;" {
				t.Fatalf("incorrect break: %+v", edge)
			}
		}
		kinds[edge.Kind]++
	}
	if kinds["loop_back"] != 1 || kinds["loop_exit"] != 1 || len(model.Edges) != len(payload.Edges) {
		t.Fatalf("control transfers lost: %v", kinds)
	}
}

func TestNestedWhileBreakResumesOuterCondition(t *testing.T) {
	root := t.TempDir()
	source := `export function retry(outer: boolean, inner: boolean) { while(outer) { while(inner) { break; } } return 1; }`
	if err := os.WriteFile(filepath.Join(root, "retry.ts"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{"repoRoot": root, "candidateId": "cand-nestedwhile", "entrySymbolPath": "retry.ts#retry", "opts": map[string]any{"includeExecutionSemantics": true}}, &payload); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "nested-while-generation")
	if err := semantic.ValidateFlowSequenceReferences(model, semantic.BuildFlowSequence(model)); err != nil {
		t.Fatal(err)
	}
	steps := map[string]semantic.SemanticStep{}
	for _, step := range model.Steps {
		steps[step.StepID] = step
	}
	resumed := 0
	for _, edge := range model.Edges {
		if edge.Kind != "loop_exit_back" {
			continue
		}
		resumed++
		if edge.ResolutionStatus != "resolved" || steps[edge.FromStepID].Kind != "break" || steps[edge.ToStepID].Name != "while (outer)" {
			t.Fatalf("wrong enclosing loop: %+v", edge)
		}
	}
	if resumed != 1 || len(model.Steps) != 4 || len(model.Edges) != len(payload.Edges) {
		t.Fatalf("nested execution lost: resumption=%d steps=%d", resumed, len(model.Steps))
	}
}

func TestIterableLoopRelationsSurviveProjection(t *testing.T) {
	root := t.TempDir()
	source := `export function processItems(items: string[]) {
  for (const item of items) { apply(item); }
  finish();
}
function apply(item: string) {} function finish() {}`
	if err := os.WriteFile(filepath.Join(root, "flow.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{"repoRoot": root, "candidateId": "cand-iterableloop", "entrySymbolPath": "flow.ts#processItems", "opts": map[string]any{"includeExecutionSemantics": true}}, &payload); err != nil {
		t.Fatal(err)
	}
	if err := slicing.ValidateExecutionReferences(payload.Steps, payload.Edges); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "iterable-loop-generation")
	sequence := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatalf("%v: %+v", err, model.Edges)
	}
	ids := map[string]string{}
	for _, step := range model.Steps {
		ids[step.Name] = step.StepID
	}
	for _, relation := range []struct {
		kind, from, to, outcome string
	}{
		{kind: "control_flow", from: "for (const item of items)", to: "apply(item)", outcome: "truthy"},
		{kind: "control_flow", from: "for (const item of items)", to: "finish()", outcome: "falsy"},
		{kind: "loop_back", from: "apply(item)", to: "for (const item of items)"},
	} {
		found := false
		for _, edge := range model.Edges {
			if edge.Kind != relation.kind || edge.ResolutionStatus != "resolved" || edge.FromStepID != ids[relation.from] || edge.ToStepID != ids[relation.to] {
				continue
			}
			if relation.outcome == "" || (len(edge.Conditions) == 1 && edge.Conditions[0].Outcome == relation.outcome) {
				found = true
			}
		}
		if ids[relation.from] == "" || ids[relation.to] == "" || !found {
			t.Fatalf("missing relation %+v: %+v", relation, model.Edges)
		}
	}
}

func TestAsyncIterableLoopRelationsSurviveProjection(t *testing.T) {
	root := t.TempDir()
	source := `export async function processItems(items: AsyncIterable<string>) {
  for await (const item of items) { apply(item); }
  finish();
}
function apply(item: string) {} function finish() {}`
	if err := os.WriteFile(filepath.Join(root, "flow.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{"repoRoot": root, "candidateId": "cand-asynciterableloop", "entrySymbolPath": "flow.ts#processItems", "opts": map[string]any{"includeExecutionSemantics": true}}, &payload); err != nil {
		t.Fatal(err)
	}
	if err := slicing.ValidateExecutionReferences(payload.Steps, payload.Edges); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "async-iterable-loop-generation")
	sequence := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatalf("%v: %+v", err, model.Edges)
	}
	ids := map[string]string{}
	for _, step := range model.Steps {
		if step.Name == "for await (const item of items)" {
			if step.Kind == "await" {
				ids["wait"] = step.StepID
			}
			if step.Kind == "branch" {
				ids["decision"] = step.StepID
			}
			continue
		}
		ids[step.Name] = step.StepID
	}
	for _, relation := range []struct {
		kind, from, to, outcome string
	}{
		{kind: "await_resume", from: "wait", to: "decision"},
		{kind: "control_flow", from: "decision", to: "apply(item)", outcome: "truthy"},
		{kind: "control_flow", from: "decision", to: "finish()", outcome: "falsy"},
		{kind: "loop_back", from: "apply(item)", to: "wait"},
	} {
		found := false
		for _, edge := range model.Edges {
			if edge.Kind != relation.kind || edge.ResolutionStatus != "resolved" || edge.FromStepID != ids[relation.from] || edge.ToStepID != ids[relation.to] {
				continue
			}
			if relation.outcome == "" || (len(edge.Conditions) == 1 && edge.Conditions[0].Outcome == relation.outcome) {
				found = true
			}
		}
		if ids[relation.from] == "" || ids[relation.to] == "" || !found {
			t.Fatalf("missing relation %+v: %+v", relation, model.Edges)
		}
	}
	for _, edge := range model.Edges {
		if edge.Kind == "unknown_edge" && edge.FromStepID == ids["wait"] {
			return
		}
	}
	t.Fatalf("async iterator limitation missing: %+v", model.Unknowns)
}

func TestRecoveredAsyncIterableLoopRelationsSurviveProjection(t *testing.T) {
	root := t.TempDir()
	source := `export async function processItems(items: AsyncIterable<string>) {
  for await (const item of items) { try { await apply(item); } catch { recover(item); } }
  finish();
}
async function apply(item: string) {} function recover(item: string) {} function finish() {}`
	if err := os.WriteFile(filepath.Join(root, "flow.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{"repoRoot": root, "candidateId": "cand-recoveredasynciterable", "entrySymbolPath": "flow.ts#processItems", "opts": map[string]any{"includeExecutionSemantics": true}}, &payload); err != nil {
		t.Fatal(err)
	}
	if err := slicing.ValidateExecutionReferences(payload.Steps, payload.Edges); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "recovered-async-iterable-generation")
	sequence := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatalf("%v: %+v", err, model.Edges)
	}
	ids := map[string]string{}
	for _, step := range model.Steps {
		if step.Name == "for await (const item of items)" {
			if step.Kind == "await" {
				ids["wait"] = step.StepID
			}
			continue
		}
		ids[step.Name] = step.StepID
	}
	for _, relation := range []struct{ kind, from, to string }{
		{kind: "await_loop_back", from: "await apply(item)", to: "wait"},
		{kind: "failure", from: "await apply(item)", to: "recover(item)"},
		{kind: "loop_back", from: "recover(item)", to: "wait"},
	} {
		found := false
		for _, edge := range model.Edges {
			found = found || (edge.Kind == relation.kind && edge.ResolutionStatus == "resolved" && edge.FromStepID == ids[relation.from] && edge.ToStepID == ids[relation.to])
		}
		if ids[relation.from] == "" || ids[relation.to] == "" || !found {
			t.Fatalf("missing relation %+v: %+v", relation, model.Edges)
		}
	}
}

func TestFinalizingAwaitRetryLoopSurvivesProjection(t *testing.T) {
	root := t.TempDir()
	source := `export async function retryUntilDone(done: boolean) {
  while (!done) { try { await retry(); } finally { cleanup(); } }
  finish();
}
async function retry() {} function cleanup() {} function finish() {}`
	if err := os.WriteFile(filepath.Join(root, "flow.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{"repoRoot": root, "candidateId": "cand-finalizingretry", "entrySymbolPath": "flow.ts#retryUntilDone", "opts": map[string]any{"includeExecutionSemantics": true}}, &payload); err != nil {
		t.Fatal(err)
	}
	if err := slicing.ValidateExecutionReferences(payload.Steps, payload.Edges); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "finalizing-await-retry-generation")
	sequence := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatalf("%v: %+v", err, model.Edges)
	}
	ids := map[string]string{}
	for _, step := range model.Steps {
		ids[step.Name] = step.StepID
	}
	for _, relation := range []struct{ kind, from, to, outcome string }{
		{kind: "await_resume", from: "await retry()", to: "cleanup()"},
		{kind: "loop_back", from: "cleanup()", to: "while (!done)"},
		{kind: "control_flow", from: "while (!done)", to: "finish()", outcome: "falsy"},
	} {
		found := false
		for _, edge := range model.Edges {
			if edge.Kind != relation.kind || edge.ResolutionStatus != "resolved" || edge.FromStepID != ids[relation.from] || edge.ToStepID != ids[relation.to] {
				continue
			}
			if relation.outcome == "" || (len(edge.Conditions) == 1 && edge.Conditions[0].Outcome == relation.outcome) {
				found = true
			}
		}
		if ids[relation.from] == "" || ids[relation.to] == "" || !found {
			t.Fatalf("missing relation %+v: %+v", relation, model.Edges)
		}
	}
	for _, unknown := range model.Unknowns {
		if strings.Contains(unknown.Reason, "대기 실패") {
			return
		}
	}
	t.Fatalf("unhandled retry rejection was not retained: %+v", model.Unknowns)
}

func TestRecoveredFinalizingAwaitRetryLoopSurvivesProjection(t *testing.T) {
	root := t.TempDir()
	source := `export async function retryUntilDone(done: boolean) {
  while (!done) { try { await retry(); } catch { recover(); } finally { cleanup(); } }
  finish();
}
async function retry() {} function recover() {} function cleanup() {} function finish() {}`
	if err := os.WriteFile(filepath.Join(root, "flow.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{"repoRoot": root, "candidateId": "cand-recoveredfinalizingretry", "entrySymbolPath": "flow.ts#retryUntilDone", "opts": map[string]any{"includeExecutionSemantics": true}}, &payload); err != nil {
		t.Fatal(err)
	}
	if err := slicing.ValidateExecutionReferences(payload.Steps, payload.Edges); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "recovered-finalizing-await-retry-generation")
	sequence := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatalf("%v: %+v", err, model.Edges)
	}
	ids := map[string]string{}
	for _, step := range model.Steps {
		ids[step.Name] = step.StepID
	}
	for _, relation := range []struct{ kind, from, to string }{
		{kind: "failure", from: "await retry()", to: "recover()"},
		{kind: "await_resume", from: "await retry()", to: "cleanup()"},
		{kind: "control_flow", from: "recover()", to: "cleanup()"},
		{kind: "loop_back", from: "cleanup()", to: "while (!done)"},
	} {
		found := false
		for _, edge := range model.Edges {
			found = found || (edge.Kind == relation.kind && edge.ResolutionStatus == "resolved" && edge.FromStepID == ids[relation.from] && edge.ToStepID == ids[relation.to])
		}
		if ids[relation.from] == "" || ids[relation.to] == "" || !found {
			t.Fatalf("missing relation %+v: %+v", relation, model.Edges)
		}
	}
}

func TestCountedForLoopRelationsSurviveProjection(t *testing.T) {
	root := t.TempDir()
	source := `export function processItems(limit: number) {
  for (let index = 0; index < limit; index++) { apply(index); }
  finish();
}
function apply(index: number) {} function finish() {}`
	if err := os.WriteFile(filepath.Join(root, "flow.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{"repoRoot": root, "candidateId": "cand-countedloop", "entrySymbolPath": "flow.ts#processItems", "opts": map[string]any{"includeExecutionSemantics": true}}, &payload); err != nil {
		t.Fatal(err)
	}
	if err := slicing.ValidateExecutionReferences(payload.Steps, payload.Edges); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "counted-loop-generation")
	sequence := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatalf("%v: %+v", err, model.Edges)
	}
	ids := map[string]string{}
	for _, step := range model.Steps {
		ids[step.Name] = step.StepID
	}
	for _, relation := range []struct {
		kind, from, to, outcome string
	}{
		{kind: "control_flow", from: "for (let index = 0; index < limit; index++)", to: "apply(index)", outcome: "truthy"},
		{kind: "control_flow", from: "for (let index = 0; index < limit; index++)", to: "finish()", outcome: "falsy"},
		{kind: "control_flow", from: "apply(index)", to: "index++"},
		{kind: "loop_back", from: "index++", to: "for (let index = 0; index < limit; index++)"},
	} {
		found := false
		for _, edge := range model.Edges {
			if edge.Kind != relation.kind || edge.ResolutionStatus != "resolved" || edge.FromStepID != ids[relation.from] || edge.ToStepID != ids[relation.to] {
				continue
			}
			if relation.outcome == "" || (len(edge.Conditions) == 1 && edge.Conditions[0].Outcome == relation.outcome) {
				found = true
			}
		}
		if ids[relation.from] == "" || ids[relation.to] == "" || !found {
			t.Fatalf("missing relation %+v: %+v", relation, model.Edges)
		}
	}
}

func TestDoLoopRelationsSurviveProjection(t *testing.T) {
	root := t.TempDir()
	source := `export function processUntilDone(again: boolean) {
  do { apply(); } while (again);
  finish();
}
function apply() {} function finish() {}`
	if err := os.WriteFile(filepath.Join(root, "flow.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{"repoRoot": root, "candidateId": "cand-doloop01", "entrySymbolPath": "flow.ts#processUntilDone", "opts": map[string]any{"includeExecutionSemantics": true}}, &payload); err != nil {
		t.Fatal(err)
	}
	if err := slicing.ValidateExecutionReferences(payload.Steps, payload.Edges); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "do-loop-generation")
	sequence := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatalf("%v: %+v", err, model.Edges)
	}
	ids := map[string]string{}
	for _, step := range model.Steps {
		ids[step.Name] = step.StepID
	}
	for _, relation := range []struct {
		kind, from, to, outcome string
	}{
		{kind: "control_flow", from: "apply()", to: "while (again)"},
		{kind: "loop_reentry", from: "while (again)", to: "apply()", outcome: "truthy"},
		{kind: "control_flow", from: "while (again)", to: "finish()", outcome: "falsy"},
	} {
		found := false
		for _, edge := range model.Edges {
			if edge.Kind != relation.kind || edge.ResolutionStatus != "resolved" || edge.FromStepID != ids[relation.from] || edge.ToStepID != ids[relation.to] {
				continue
			}
			if relation.outcome == "" || (len(edge.Conditions) == 1 && edge.Conditions[0].Outcome == relation.outcome) {
				found = true
			}
		}
		if ids[relation.from] == "" || ids[relation.to] == "" || !found {
			t.Fatalf("missing relation %+v: %+v", relation, model.Edges)
		}
	}
}

func TestSwitchRelationsSurviveProjection(t *testing.T) {
	root := t.TempDir()
	source := `export function selectAction(action: string) {
  switch (action) {
    case "approve": approve(); break;
    case "reject": reject(); break;
    default: review();
  }
  finish();
}
function approve() {} function reject() {} function review() {} function finish() {}`
	if err := os.WriteFile(filepath.Join(root, "flow.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{"repoRoot": root, "candidateId": "cand-switchflow", "entrySymbolPath": "flow.ts#selectAction", "opts": map[string]any{"includeExecutionSemantics": true}}, &payload); err != nil {
		t.Fatal(err)
	}
	if err := slicing.ValidateExecutionReferences(payload.Steps, payload.Edges); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "switch-generation")
	sequence := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatalf("%v: %+v", err, model.Edges)
	}
	ids := map[string]string{}
	for _, step := range model.Steps {
		ids[step.Name] = step.StepID
	}
	for _, relation := range [][3]string{{"control_flow", "switch (action)", `case "approve":`}, {"control_flow", `case "approve":`, "approve()"}, {"switch_exit", "break;", "finish()"}} {
		found := false
		for _, edge := range model.Edges {
			found = found || (edge.Kind == relation[0] && edge.ResolutionStatus == "resolved" && edge.FromStepID == ids[relation[1]] && edge.ToStepID == ids[relation[2]])
		}
		if ids[relation[1]] == "" || ids[relation[2]] == "" || !found {
			t.Fatalf("missing relation %v: %+v", relation, model.Edges)
		}
	}
}
