package flowview

import (
	"encoding/json"
	"testing"
)

func TestInferLayerDeterministic(t *testing.T) {
	cases := []struct {
		name       string
		path       string
		sym        string
		stateDelta bool
		sideEffect bool
		want       string
	}{
		{"boundary call is external", "packages/feature_account/lib/src/features/join/data/join_repository.dart", "JoinRepository.cancel", false, true, LayerExternal},
		{"presentation page is ui", "packages/feature_account/lib/src/features/join/presentation/join_page.dart", "JoinPageState._requestExit", false, false, LayerUI},
		{"dialog symbol is ui", "lib/src/features/home/feed_screen.dart", "FeedDialog.show", false, false, LayerUI},
		{"repository is data", "packages/feature_account/lib/src/features/join/data/join_repository.dart", "JoinRepository.cancel", false, false, LayerData},
		{"state mutation is state", "packages/feature_account/lib/src/features/join/presentation/join_controller.dart", "JoinController._onJoinCancel", true, false, LayerState},
		{"controller logic is application", "packages/feature_account/lib/src/features/join/presentation/join_controller.dart", "JoinController.submit", false, false, LayerApplication},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, convention := InferLayer(tc.path, tc.sym, tc.stateDelta, tc.sideEffect)
			if got != tc.want {
				t.Errorf("InferLayer(%q, %q, %v, %v) = %q, want %q", tc.path, tc.sym, tc.stateDelta, tc.sideEffect, got, tc.want)
			}
			if convention == "" {
				t.Errorf("InferLayer(%q, %q) returned empty convention", tc.path, tc.sym)
			}
			// Determinism: same input, same output.
			if again, _ := InferLayer(tc.path, tc.sym, tc.stateDelta, tc.sideEffect); again != got {
				t.Errorf("InferLayer not deterministic: %q then %q", got, again)
			}
		})
	}
}

func TestApplyLayersAdaptiveLaneLabels(t *testing.T) {
	spec := []byte(`{
		"flowId": "flow-1234567890abcdef",
		"steps": [
			{"ordinal": 1, "anchor": {"repoRelativePath": "lib/presentation/join_page.dart", "enclosingSymbolPath": "JoinPage.exit"}},
			{"ordinal": 2, "anchor": {"repoRelativePath": "lib/presentation/join_controller.dart", "enclosingSymbolPath": "JoinController.cancel"}, "stateDelta": {"before": "a", "after": "b"}},
			{"ordinal": 3, "anchor": {"repoRelativePath": "lib/data/join_repository.dart", "enclosingSymbolPath": "JoinRepository.cancel"}, "sideEffect": "cancel"}
		],
		"unknowns": []
	}`)
	out := applyLayers(spec)
	var doc struct {
		Lanes []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"lanes"`
		Steps []struct {
			Layer string `json:"layer"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("decorated spec invalid JSON: %v", err)
	}
	// Lane labels use the project's own vocabulary, not fixed generic names.
	// The state step lives in JoinController, so its lane reads "상태 변경" —
	// distinct from the application lane that shares the same controller.
	want := map[string]string{
		"ui":       "화면(Presentation)",
		"state":    "상태 변경(Controller)",
		"external": "외부 연동(External)",
	}
	if len(doc.Lanes) != 3 {
		t.Fatalf("lanes = %d, want 3 (state lane has no steps)", len(doc.Lanes))
	}
	for _, lane := range doc.Lanes {
		if want[lane.ID] != lane.Label {
			t.Errorf("lane %q label = %q, want %q", lane.ID, lane.Label, want[lane.ID])
		}
	}
}

func TestApplyLayersDecoratesSteps(t *testing.T) {
	spec := []byte(`{
		"flowId": "flow-1234567890abcdef",
		"steps": [
			{"ordinal": 1, "anchor": {"repoRelativePath": "lib/ui/join_page.dart", "enclosingSymbolPath": "JoinPage.exit"}},
			{"ordinal": 2, "anchor": {"repoRelativePath": "lib/logic/join_controller.dart", "enclosingSymbolPath": "JoinController.cancel"}, "stateDelta": {"before": "a", "after": "b"}}
		],
		"unknowns": []
	}`)
	out := applyLayers(spec)
	var doc struct {
		Steps []struct {
			Layer string `json:"layer"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("decorated spec invalid JSON: %v", err)
	}
	if len(doc.Steps) != 2 {
		t.Fatalf("steps lost during decoration: %d", len(doc.Steps))
	}
	if doc.Steps[0].Layer != LayerUI {
		t.Errorf("step 1 layer = %q, want %q", doc.Steps[0].Layer, LayerUI)
	}
	if doc.Steps[1].Layer != LayerState {
		t.Errorf("step 2 layer = %q, want %q", doc.Steps[1].Layer, LayerState)
	}
}

func TestApplyLayersToleratesMalformedSpec(t *testing.T) {
	bad := []byte(`{"steps": "not-an-array"}`)
	if out := string(applyLayers(bad)); out != string(bad) {
		t.Errorf("malformed spec must pass through unchanged, got %q", out)
	}
	broken := []byte(`{not json`)
	if out := string(applyLayers(broken)); out != string(broken) {
		t.Errorf("broken JSON must pass through unchanged, got %q", out)
	}
}
