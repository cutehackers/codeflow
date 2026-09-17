package flowview

import "testing"

func TestPreserveLogicalViewSelectionUsesStructuralIdentityAndAnchor(t *testing.T) {
	previous := LogicalViewSelection{
		SelectedStepID:             "step-old",
		SelectedStructuralIdentity: "checkout.submit|application|call",
		ScrollAnchor: &LogicalScrollAnchor{
			StepID:             "step-old",
			StructuralIdentity: "checkout.submit|application|call",
			OffsetPx:           37.5,
		},
	}
	next := []ViewStepIdentity{
		{StepID: "step-inserted", StructuralIdentity: "checkout.validate|domain|call"},
		{StepID: "step-new", StructuralIdentity: "checkout.submit|application|call"},
	}

	got := PreserveLogicalViewSelection(previous, next)
	if !got.Preserved || got.IdentityLoss || got.SelectedStepID != "step-new" || got.ScrollAnchor == nil || got.ScrollAnchor.StepID != "step-new" || got.ScrollAnchor.OffsetPx != 37.5 {
		t.Fatalf("compatible selection and logical anchor were not preserved: %+v", got)
	}
}

func TestPreserveLogicalViewSelectionReportsIdentityLossWithoutFallback(t *testing.T) {
	previous := LogicalViewSelection{
		SelectedStepID:             "step-old",
		SelectedStructuralIdentity: "checkout.submit|application|call",
		ScrollAnchor: &LogicalScrollAnchor{
			StepID:             "step-old",
			StructuralIdentity: "checkout.submit|application|call",
			OffsetPx:           12,
		},
	}

	for name, next := range map[string][]ViewStepIdentity{
		"removed": {{StepID: "step-other", StructuralIdentity: "checkout.validate|domain|call"}},
		"ambiguous": {
			{StepID: "step-a", StructuralIdentity: "checkout.submit|application|call"},
			{StepID: "step-b", StructuralIdentity: "checkout.submit|application|call"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := PreserveLogicalViewSelection(previous, next)
			if got.Preserved || !got.IdentityLoss || got.SelectedStepID != "" || got.ScrollAnchor != nil {
				t.Fatalf("identity loss must not use an ordinal or step-id fallback: %+v", got)
			}
		})
	}
}

func TestPreserveLogicalViewSelectionRequiresMatchingLogicalAnchor(t *testing.T) {
	previous := LogicalViewSelection{
		SelectedStructuralIdentity: "checkout.submit|application|call",
		ScrollAnchor: &LogicalScrollAnchor{
			StepID:             "step-old",
			StructuralIdentity: "checkout.other|application|call",
			OffsetPx:           12,
		},
	}
	got := PreserveLogicalViewSelection(previous, []ViewStepIdentity{{StepID: "step-new", StructuralIdentity: previous.SelectedStructuralIdentity}})
	if got.Preserved || got.IdentityLoss || got.SelectedStepID != "step-new" || got.ScrollAnchor != nil {
		t.Fatalf("selection with a mismatched anchor must not count as preserved: %+v", got)
	}
}
