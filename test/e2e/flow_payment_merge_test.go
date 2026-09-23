package e2e_test

import (
	"os"
	"path/filepath"
	"testing"

	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/slicing"
	"codeflow/internal/curator/semantic"
)

func TestPaymentAlternativesPreserveSharedContinuation(t *testing.T) {
	root := t.TempDir()
	sources := map[string]string{
		"checkout.ts": `import { chargeCard } from './card';
import { transferFunds } from './bank';
import { receipt } from './receipt';
export function checkout(card: boolean, amount: number) {
 let payment;
 if (card) { payment = chargeCard(amount); }
 else { payment = transferFunds(amount); }
 return receipt(payment);
}`,
		"card.ts":    `export function chargeCard(amount: number) { return { method: 'card', amount }; }`,
		"bank.ts":    `export function transferFunds(amount: number) { return { method: 'bank', amount }; }`,
		"receipt.ts": `export function receipt(payment: unknown) { return { payment }; }`,
	}
	for file, source := range sources {
		if err := os.WriteFile(filepath.Join(root, file), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{"repoRoot": root, "candidateId": "cand-paymentmerge", "entrySymbolPath": "checkout.ts#checkout", "opts": map[string]any{"includeExecutionSemantics": true}}, &payload); err != nil {
		t.Fatal(err)
	}
	if err := slicing.ValidateExecutionReferences(payload.Steps, payload.Edges); err != nil {
		t.Fatal(err)
	}
	byText := map[string][]slicing.SliceStep{}
	byOrdinal := map[int]slicing.SliceStep{}
	for _, step := range payload.Steps {
		source := sources[step.Anchor.RepoRelativePath]
		span := step.Anchor.ByteRange
		if span[0] < 0 || span[1] > len(source) || span[0] >= span[1] {
			t.Fatalf("invalid source range: %+v", step)
		}
		text := source[span[0]:span[1]]
		byText[text] = append(byText[text], step)
		byOrdinal[step.Ordinal] = step
	}
	single := func(text string) slicing.SliceStep {
		t.Helper()
		if len(byText[text]) != 1 {
			t.Fatalf("expected one %q, got %d", text, len(byText[text]))
		}
		return byText[text][0]
	}
	common := single("receipt(payment)")
	card := single("chargeCard(amount)")
	bank := single("transferFunds(amount)")
	cardResult := single("payment = chargeCard(amount)")
	bankResult := single("payment = transferFunds(amount)")
	decisions := map[string]int{}
	merges := map[int]bool{}
	returns := map[int]bool{}
	for _, edge := range payload.Edges {
		if edge.StepOrdinal == nil || edge.TargetStepOrdinal == nil {
			continue
		}
		if len(edge.Conditions) > 0 {
			decisions[edge.Conditions[0].Outcome] = *edge.TargetStepOrdinal
		}
		if edge.Kind == "control_flow" && *edge.TargetStepOrdinal == common.Ordinal {
			merges[*edge.StepOrdinal] = true
		}
		if edge.Kind == "return" && (*edge.TargetStepOrdinal == cardResult.Ordinal || *edge.TargetStepOrdinal == bankResult.Ordinal) {
			origin := byOrdinal[*edge.StepOrdinal]
			if origin.CallerStepOrdinal == nil {
				t.Fatal("return has no caller")
			}
			expected := card.Ordinal
			if *edge.TargetStepOrdinal == bankResult.Ordinal {
				expected = bank.Ordinal
			}
			if *origin.CallerStepOrdinal != expected {
				t.Fatal("payment return crossed alternatives")
			}
			returns[*edge.TargetStepOrdinal] = true
		}
	}
	if decisions["truthy"] != card.Ordinal || decisions["falsy"] != bank.Ordinal {
		t.Fatalf("payment alternatives lost: %v", decisions)
	}
	if len(merges) != 2 || !merges[cardResult.Ordinal] || !merges[bankResult.Ordinal] {
		t.Fatalf("shared continuation lost: %v", merges)
	}
	if len(returns) != 2 {
		t.Fatalf("payment result resumption missing: %v", returns)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "payment-generation")
	sequence := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatal(err)
	}
	membership := map[string]int{}
	for _, frame := range sequence.Frames {
		for _, id := range frame.StepRefs {
			membership[id]++
		}
	}
	for _, step := range model.Steps {
		if membership[step.StepID] != 1 {
			t.Fatalf("step has %d parents", membership[step.StepID])
		}
	}
	if len(model.Edges) != len(payload.Edges) {
		t.Fatalf("relations lost in projection: %d -> %d", len(payload.Edges), len(model.Edges))
	}
}
