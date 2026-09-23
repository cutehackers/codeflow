package semantic_test

import (
	"codeflow/internal/curator/semantic"
	"testing"
)

func TestExecutionConditionDoesNotBecomeDecision(t *testing.T) {
	for _, tc := range []struct{ kind, invocation, role string }{
		{"guard", "run", "decision"}, {"branch", "run", "decision"}, {"decision", "run", "decision"},
		{"call", "run", "process"}, {"mutation", "run", "process"}, {"return", "run", "process"}, {"throw", "run", "process"},
		{"return", "", "result"}, {"external_effect", "run", "effect"}, {"", "", "decision"},
	} {
		t.Run(tc.kind+"/"+tc.invocation, func(t *testing.T) {
			condition := "ready"
			model := &semantic.SemanticMapIR{MapID: "map-condition", Steps: []semantic.SemanticStep{{StepID: "entry", Kind: "user_action"}, {StepID: "conditional", Kind: tc.kind, InvocationID: tc.invocation, Branch: &condition}}}
			seq := semantic.BuildFlowSequence(model)
			if len(seq.Frames) != 2 || seq.Frames[1].Role != tc.role {
				t.Fatalf("kind=%s role=%+v want=%s", tc.kind, seq.Frames, tc.role)
			}
			if seq.Frames[1].Condition == nil || *seq.Frames[1].Condition != condition {
				t.Fatal("execution condition discarded")
			}
			if model.Steps[1].Branch == nil || *model.Steps[1].Branch != condition {
				t.Fatal("source condition changed")
			}
		})
	}
}
