package semantic

// markUnprovenInvocationFrames distinguishes verified source from verified
// execution. A callback body may have exact source evidence while the adapter
// has not proved that the requested flow invokes it.
func markUnprovenInvocationFrames(sequence *FlowSequence, model *SemanticMapIR) {
	if sequence == nil || model == nil || model.Authority != "candidate" || len(model.Steps) == 0 {
		return
	}
	root := model.Steps[0].InvocationID
	if root == "" {
		return
	}
	byID := make(map[string]SemanticStep, len(model.Steps))
	for _, step := range model.Steps {
		byID[step.StepID] = step
	}
	reachable := map[string]bool{root: true}
	for changed := true; changed; {
		changed = false
		for _, edge := range model.Edges {
			if edge.Kind != "resolved_cross_file" || edge.ResolutionStatus != "resolved" {
				continue
			}
			caller, callerExists := byID[edge.FromStepID]
			entry, entryExists := byID[edge.ToStepID]
			if !callerExists || !entryExists || !reachable[caller.InvocationID] || entry.InvocationID == "" || entry.CallerStepOrdinal == nil || *entry.CallerStepOrdinal != caller.Ordinal || reachable[entry.InvocationID] {
				continue
			}
			reachable[entry.InvocationID] = true
			changed = true
		}
	}
	var limited []string
	unprovenOnly := make(map[string]bool)
	for index := range sequence.Frames {
		frame := &sequence.Frames[index]
		allUnproven := len(frame.StepRefs) > 0
		anyUnproven := false
		for _, ref := range frame.StepRefs {
			step, exists := byID[ref]
			if !exists || step.InvocationID == "" || reachable[step.InvocationID] {
				allUnproven = false
				continue
			}
			anyUnproven = true
		}
		if anyUnproven {
			if frame.Status == "verified" {
				frame.Status = "partial"
			}
			limited = append(limited, frame.FrameID)
		}
		if allUnproven {
			unprovenOnly[frame.FrameID] = true
		}
	}
	if len(limited) > 0 {
		limitations := make([]FlowSummaryLimitation, 0, len(sequence.SummaryLimitations))
		for _, limitation := range sequence.SummaryLimitations {
			if limitation.Code == "grouping_evidence_missing" {
				refs := limitation.FrameRefs[:0]
				for _, ref := range limitation.FrameRefs {
					if !unprovenOnly[ref] {
						refs = append(refs, ref)
					}
				}
				limitation.FrameRefs = refs
				if len(refs) == 0 {
					continue
				}
			}
			limitations = append(limitations, limitation)
		}
		sequence.SummaryLimitations = limitations
		sequence.SummaryLimitations = append(sequence.SummaryLimitations, FlowSummaryLimitation{
			Code: "invocation_unproven", Message: "코드는 확인했지만 이 처리로 이어지는 실행 관계는 확인되지 않았습니다.", FrameRefs: limited,
		})
	}
}
