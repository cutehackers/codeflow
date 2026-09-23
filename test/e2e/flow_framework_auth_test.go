package e2e_test

import (
	"os"
	"path/filepath"
	"testing"

	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/slicing"
	"codeflow/internal/curator/semantic"
)

func TestNextRouteAuthenticationDenialPreservesGateway(t *testing.T) {
	root := t.TempDir()
	source := `type RouteRequest = { headers: { get(name: string): string | null } };

export async function POST(request: RouteRequest) {
  const credential = request.headers.get('authorization');
  if (!credential) return Response.json({ error: 'unauthorized' }, { status: 401 });
  return createOrder(credential);
}

function createOrder(credential: string) {
  return { accepted: true, credential };
}
`
	if err := os.MkdirAll(filepath.Join(root, "app", "api", "checkout"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app", "api", "checkout", "route.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{
		"repoRoot": root, "candidateId": "cand-nextrouteauth", "entrySymbolPath": "app/api/checkout/route.ts#POST",
		"opts": map[string]any{"includeExecutionSemantics": true},
	}, &payload); err != nil {
		t.Fatal(err)
	}
	if err := slicing.ValidateExecutionReferences(payload.Steps, payload.Edges); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "next-route-auth-generation")
	sequence := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatal(err)
	}

	stepByID := make(map[string]semantic.SemanticStep, len(model.Steps))
	parentByStep := make(map[string]semantic.FlowSequenceFrame, len(model.Steps))
	for _, step := range model.Steps {
		stepByID[step.StepID] = step
	}
	for _, frame := range sequence.Frames {
		for _, stepID := range frame.StepRefs {
			parentByStep[stepID] = frame
		}
	}
	var authenticationGate semantic.SemanticStep
	for _, step := range model.Steps {
		if step.Kind == "guard" && step.Branch != nil && *step.Branch == "!credential" {
			authenticationGate = step
			break
		}
	}
	if authenticationGate.StepID == "" {
		t.Fatalf("route authentication condition missing: %+v", model.Steps)
	}
	if parent := parentByStep[authenticationGate.StepID]; parent.Role != "decision" || parent.PrimaryStepRef != authenticationGate.StepID {
		t.Fatalf("authentication gateway hidden: %+v", parent)
	}
	outcomes := make(map[string]semantic.SemanticStep)
	for _, edge := range model.Edges {
		if edge.FromStepID != authenticationGate.StepID || len(edge.Conditions) != 1 || edge.Conditions[0].StepID != authenticationGate.StepID {
			continue
		}
		outcomes[edge.Conditions[0].Outcome] = stepByID[edge.ToStepID]
	}
	denied, proceeds := outcomes["truthy"], outcomes["falsy"]
	if denied.Kind != "call" || denied.Name != "Response.json({ error: 'unauthorized' }, { status: 401 })" || parentByStep[denied.StepID].Role != "boundary" || proceeds.StepID == "" || parentByStep[denied.StepID].FrameID == parentByStep[authenticationGate.StepID].FrameID {
		t.Fatalf("authentication outcomes were not independently preserved: denied=%+v proceeds=%+v", denied, proceeds)
	}
	var denialReturn semantic.SemanticStep
	for _, edge := range model.Edges {
		if edge.FromStepID == denied.StepID && edge.Kind == "control_flow" && edge.ResolutionStatus == "resolved" {
			denialReturn = stepByID[edge.ToStepID]
			break
		}
	}
	if denialReturn.Kind != "return" || denialReturn.Anchor.RepoRelativePath != "app/api/checkout/route.ts" || denialReturn.Anchor.EnclosingSymbolPath != "POST" {
		t.Fatalf("denial source lost: %+v", denied.Anchor)
	}
}
