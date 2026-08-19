// Package compiler converts validated graph and Dart evidence into the small
// causal route flow supported by CF-G05.
package compiler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"codeflow/core/internal/codegraph"
	"codeflow/core/internal/dartadapter"
	"codeflow/core/internal/entrypoint"
	"codeflow/core/internal/flowir"
	"codeflow/core/internal/manifest"
)

type Options struct {
	Repo, Selector, CodeGraphURL, AdapterCommand string
	// Basis is supplied only by the immutable baseline mirror. Its files are
	// still re-hashed below before graph evidence can enter FlowIR.
	Basis *flowir.Basis
}
type Problem struct{ Code, Message string }

// Compile is fail-closed: graph relations and semantic anchors must describe
// the exact captured worktree. No source-derived relationship is accepted
// without a current graph slice.
func Compile(ctx context.Context, opt Options) (flowir.Document, *Problem, error) {
	basis := flowir.Basis{}
	var err error
	if opt.Basis != nil {
		basis = *opt.Basis
	} else if basis, err = manifest.Capture(opt.Repo); err != nil {
		return flowir.Document{}, nil, err
	}
	resolved := entrypoint.Resolve(ctx, opt.Repo, opt.Selector, opt.AdapterCommand)
	if resolved.State != entrypoint.Ready {
		if resolved.Unknown != nil {
			return flowir.Document{}, &Problem{resolved.Unknown.Code, resolved.Unknown.Message}, nil
		}
		return flowir.Document{}, &Problem{"ENTRY_POINT_UNKNOWN", "entry point is unavailable"}, nil
	}
	graph := codegraph.New(opt.CodeGraphURL)
	rels, err := graph.Relationships(ctx, basis.Repository, resolved.EntryPoint.FlowID)
	if err != nil {
		f := asGraph(err)
		return flowir.Document{}, &Problem{f.Code, f.Message}, nil
	}
	paths, err := validateRelationships(basis, rels)
	if err != nil {
		return flowir.Document{}, &Problem{"STALE_GRAPH", err.Error()}, nil
	}
	semantic, err := dartadapter.RefineRouteFlow(ctx, opt.AdapterCommand, basis.Repository, resolved.EntryPoint.FlowID, paths)
	if err != nil {
		f := dartadapter.AsFailure(err)
		return flowir.Document{}, &Problem{f.Code, f.Message}, nil
	}
	facts, err := validateSemantic(basis, semantic)
	if err != nil {
		return flowir.Document{}, &Problem{"STALE_GRAPH", err.Error()}, nil
	}
	doc, err := assemble(basis, *resolved.EntryPoint, facts, graph.Backend)
	if err != nil {
		return flowir.Document{}, nil, err
	}
	return doc, nil, nil
}
func asGraph(err error) *codegraph.Failure {
	if f, ok := err.(*codegraph.Failure); ok {
		return f
	}
	return &codegraph.Failure{Code: "CODEGRAPH_UNAVAILABLE", Message: err.Error()}
}
func validateRelationships(basis flowir.Basis, rels []codegraph.Relationship) ([]string, error) {
	m := map[string]flowir.ManifestEntry{}
	for _, e := range basis.Manifest {
		m[e.Path] = e
	}
	paths := map[string]bool{}
	for _, r := range rels {
		if r.Kind != "call" {
			continue
		}
		for _, a := range []codegraph.Anchor{r.From, r.To} {
			e, ok := m[a.Path]
			if !ok || e.Type != "file" || a.Symbol == "" || a.FileHash == "" || a.FileHash != e.FileHash || a.Revision == "" || a.Revision != basis.HeadRevision || a.ByteStart < 0 || a.ByteEnd <= a.ByteStart {
				return nil, fmt.Errorf("graph relationship anchor %s is stale or incomplete", a.Path)
			}
			b, err := os.ReadFile(filepath.Join(basis.Repository, filepath.FromSlash(a.Path)))
			if err != nil || a.ByteEnd > len(b) || flowir.SHA256Bytes(b) != a.FileHash {
				return nil, fmt.Errorf("graph relationship anchor %s no longer matches source", a.Path)
			}
			paths[a.Path] = true
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("graph slice contains no validated call relationship")
	}
	out := make([]string, 0, len(paths))
	for p := range paths {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

type semanticFact struct {
	kind, subject, object string
	proof, symbolID       string
	anchor                flowir.Anchor
	status                flowir.Status
}

func validateSemantic(basis flowir.Basis, values []dartadapter.SemanticFact) ([]semanticFact, error) {
	m := map[string]flowir.ManifestEntry{}
	for _, e := range basis.Manifest {
		m[e.Path] = e
	}
	out := make([]semanticFact, 0, len(values))
	for _, v := range values {
		e, ok := m[v.Anchor.Path]
		if !ok || e.Type != "file" || v.Anchor.ByteStart < 0 || v.Anchor.ByteEnd <= v.Anchor.ByteStart {
			return nil, fmt.Errorf("semantic anchor %s is invalid", v.Anchor.Path)
		}
		b, err := os.ReadFile(filepath.Join(basis.Repository, filepath.FromSlash(v.Anchor.Path)))
		if err != nil || v.Anchor.ByteEnd > len(b) || flowir.SHA256Bytes(b) != e.FileHash {
			return nil, fmt.Errorf("semantic anchor %s is stale", v.Anchor.Path)
		}
		lineEnd := v.Anchor.LineEnd
		if lineEnd < v.Anchor.LineStart {
			lineEnd = v.Anchor.LineStart
		}
		a := flowir.Anchor{Kind: "code", Path: v.Anchor.Path, Symbol: symbolFor(v.Kind), LineRange: []int{v.Anchor.LineStart, lineEnd}, ByteRange: []int{v.Anchor.ByteStart, v.Anchor.ByteEnd}, FileHash: e.FileHash, SpanHash: flowir.SHA256Bytes(b[v.Anchor.ByteStart:v.Anchor.ByteEnd]), Fingerprint: v.Anchor.Fingerprint, Revision: basis.HeadRevision}
		status := flowir.Observed
		if v.Kind == "dynamic_dispatch" || v.Kind == "unknown_state" || v.Kind == "external_boundary_unknown" {
			status = flowir.Unknown
		}
		out = append(out, semanticFact{kind: v.Kind, subject: v.Subject, object: v.Object, proof: v.Proof, symbolID: v.SymbolID, anchor: a, status: status})
	}
	return out, nil
}
func symbolFor(kind string) string {
	if kind == "route_transition" {
		return "route"
	}
	if kind == "state_transition" || kind == "unknown_state" {
		return "ui_state"
	}
	return "code"
}
func insertTimelineStep(steps []flowir.Step, next flowir.Step) []flowir.Step {
	for i := range steps {
		if steps[i].Order >= next.Order {
			steps[i].Order++
		}
	}
	steps = append(steps, next)
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].Order < steps[j].Order })
	return steps
}
func assemble(basis flowir.Basis, entry entrypoint.EntryPoint, values []semanticFact, backend string) (flowir.Document, error) {
	entryFact := flowir.Fact{ID: flowir.Hash(flowir.SchemaVersion, "entry_point", entry.FlowID, "", entry.Anchor.Fingerprint), Kind: "entry_point", Subject: entry.FlowID, Proof: "framework_rule_v1", Evidence: []flowir.Anchor{entry.Anchor}, Status: flowir.Observed}
	facts := []flowir.Fact{entryFact}
	var action, call, transition, condition, dynamic, dependency, operation, state, unknownState, repository, external, externalResult, externalUnknown, confirmation, terminalResult, eventDispatch, notifierState, listenerCondition, listenerRoute *flowir.Fact
	transitions := []*flowir.Fact{}
	for _, v := range values {
		kind := v.kind
		if kind != "user_action" && kind != "call" && kind != "route_transition" && kind != "condition" && kind != "dynamic_dispatch" && kind != "provider_dependency" && kind != "notifier_operation" && kind != "state_transition" && kind != "unknown_state" && kind != "repository_access" && kind != "external_call" && kind != "external_result" && kind != "external_boundary_unknown" && kind != "confirmation_condition" && kind != "terminal_result" && kind != "event_dispatch" && kind != "notifier_state_transition" && kind != "listener_condition" {
			continue
		}
		f := flowir.Fact{ID: flowir.Hash(flowir.SchemaVersion, kind, v.subject, v.object, v.anchor.Fingerprint), Kind: kind, Subject: v.subject, Object: v.object, Proof: v.proof, SymbolID: v.symbolID, Evidence: []flowir.Anchor{v.anchor}, Status: v.status}
		facts = append(facts, f)
		switch kind {
		case "user_action":
			if action == nil {
				action = &facts[len(facts)-1]
			}
		case "call":
			if call == nil {
				call = &facts[len(facts)-1]
			}
		case "route_transition":
			transitions = append(transitions, &facts[len(facts)-1])
			if v.object == "route:/auth" {
				listenerRoute = &facts[len(facts)-1]
			}
		case "condition":
			if condition == nil {
				condition = &facts[len(facts)-1]
			}
		case "dynamic_dispatch":
			if dynamic == nil {
				dynamic = &facts[len(facts)-1]
			}
		case "provider_dependency":
			if dependency == nil {
				dependency = &facts[len(facts)-1]
			}
		case "notifier_operation":
			if operation == nil {
				operation = &facts[len(facts)-1]
			}
		case "state_transition":
			if state == nil {
				state = &facts[len(facts)-1]
			}
		case "unknown_state":
			if unknownState == nil {
				unknownState = &facts[len(facts)-1]
			}
		case "repository_access":
			if repository == nil {
				repository = &facts[len(facts)-1]
			}
		case "external_call":
			if external == nil {
				external = &facts[len(facts)-1]
			}
		case "external_result":
			if externalResult == nil {
				externalResult = &facts[len(facts)-1]
			}
		case "external_boundary_unknown":
			if externalUnknown == nil {
				externalUnknown = &facts[len(facts)-1]
			}
		case "confirmation_condition":
			if confirmation == nil {
				confirmation = &facts[len(facts)-1]
			}
		case "terminal_result":
			if terminalResult == nil {
				terminalResult = &facts[len(facts)-1]
			}
		case "event_dispatch":
			if eventDispatch == nil {
				eventDispatch = &facts[len(facts)-1]
			}
		case "notifier_state_transition":
			if notifierState == nil {
				notifierState = &facts[len(facts)-1]
			}
		case "listener_condition":
			if listenerCondition == nil {
				listenerCondition = &facts[len(facts)-1]
			}
		}
	}
	if action == nil {
		return flowir.Document{}, fmt.Errorf("supported route requires an observed user action")
	}
	for _, candidate := range transitions {
		if candidate.Subject == action.Subject {
			transition = candidate
			break
		}
	}
	if transition == nil && len(transitions) > 0 {
		transition = transitions[0]
	}
	if transition == nil {
		step := flowir.Step{ID: flowir.Hash(entry.FlowID, entryFact.ID, action.ID), BehaviorKey: entry.FlowID + ":user:" + action.Subject, Order: 1, Actor: "user", TriggerFact: entryFact.ID, BehaviorFacts: []string{action.ID}, PrimaryEvidence: []flowir.Anchor{action.Evidence[0]}, Status: flowir.Unknown}
		doc := flowir.Document{SchemaVersion: flowir.SchemaVersion, Basis: basis, Facts: facts, Architecture: flowir.ArchitectureSlice{EntryPoints: []string{entry.FlowID}, Boundaries: []string{"ui", "application", "graph:" + backend}, Components: []string{entry.Anchor.Path, action.Subject}, Relations: []string{"call"}}, Current: flowir.Flow{ID: entry.FlowID, FlowKey: entry.FlowID, EntryPointFact: entryFact.ID, Steps: []flowir.Step{step}, Status: flowir.Unknown}, Unknowns: []flowir.UnknownDetail{{ID: flowir.Hash("unknown", "missing_route_transition", action.ID), Question: "Which visible route or UI result follows this action?", Reason: "missing_relation", RelatedSteps: []string{step.ID}, Evidence: []flowir.Anchor{action.Evidence[0]}}}}
		doc.CausalEdges = causalEdges(doc)
		attachDebtGuidance(&doc)
		return doc, flowir.Validate(doc)
	}
	visible := flowir.Fact{ID: flowir.Hash(flowir.SchemaVersion, "visible_result", transition.Subject, transition.Object, transition.Evidence[0].Fingerprint), Kind: "visible_result", Subject: transition.Subject, Object: transition.Object, Proof: transition.Proof, SymbolID: transition.SymbolID, Evidence: transition.Evidence, Status: flowir.Observed}
	facts = append(facts, visible)
	step1 := flowir.Step{ID: flowir.Hash(entry.FlowID, entryFact.ID, action.ID), BehaviorKey: entry.FlowID + ":user:" + action.Subject, Order: 1, Actor: "user", TriggerFact: entryFact.ID, BehaviorFacts: []string{action.ID}, PrimaryEvidence: []flowir.Anchor{action.Evidence[0]}, Status: flowir.Observed}
	behavior := []string{transition.ID}
	anchors := []flowir.Anchor{transition.Evidence[0]}
	if call != nil {
		behavior = []string{call.ID, transition.ID}
		anchors = []flowir.Anchor{call.Evidence[0], transition.Evidence[0]}
	}
	step2 := flowir.Step{ID: flowir.Hash(entry.FlowID, action.ID, transition.ID), BehaviorKey: entry.FlowID + ":system:route_transition:" + transition.Object, Order: 2, Actor: "system", TriggerFact: action.ID, BehaviorFacts: behavior, ResultFacts: []string{visible.ID}, PrimaryEvidence: anchors, Status: flowir.Observed}
	steps := []flowir.Step{step1, step2}
	status := flowir.Observed
	unknowns := []flowir.UnknownDetail(nil)
	// A branch is emitted only when the route result is directly tied to the
	// same source body as the condition. The literal result remains observed on
	// that branch; the other conditional outcome is explicitly unknown unless
	// the adapter independently proves it. Never present a conditional route as
	// an unconditional result for the initiating user action.
	if condition != nil && dynamic != nil {
		conditionStep := flowir.Step{ID: flowir.Hash(entry.FlowID, action.ID, condition.ID), BehaviorKey: entry.FlowID + ":system:condition:" + condition.ID, Order: 2, Actor: "system", TriggerFact: action.ID, BehaviorFacts: []string{condition.ID}, PrimaryEvidence: []flowir.Anchor{condition.Evidence[0]}, Status: flowir.Mixed}
		observed := step2
		observed.Order = 3
		unknownStep := flowir.Step{ID: flowir.Hash(entry.FlowID, condition.ID, dynamic.ID), BehaviorKey: entry.FlowID + ":system:dynamic_dispatch:" + dynamic.Subject, Order: 4, Actor: "system", TriggerFact: condition.ID, BehaviorFacts: []string{dynamic.ID}, PrimaryEvidence: []flowir.Anchor{dynamic.Evidence[0]}, Status: flowir.Unknown}
		conditionStep.Branches = []flowir.Branch{{ID: flowir.BranchID(condition.ID, []string{observed.BehaviorKey, unknownStep.BehaviorKey}), ConditionFact: condition.ID, OutcomeStepIDs: []string{observed.ID, unknownStep.ID}, Evidence: []flowir.Anchor{condition.Evidence[0]}, Status: flowir.Unknown}}
		steps = []flowir.Step{step1, conditionStep, observed, unknownStep}
		status = flowir.Mixed
		unknowns = []flowir.UnknownDetail{{ID: flowir.Hash("unknown", "dynamic_dispatch", dynamic.ID), Question: "Which target does this dynamic dispatch reach?", Reason: "dynamic_dispatch", RelatedSteps: []string{unknownStep.ID}, Evidence: []flowir.Anchor{dynamic.Evidence[0]}}}
	} else if condition != nil && transition.Subject == condition.Subject && confirmation != nil && terminalResult != nil && terminalResult.Subject == confirmation.Subject && eventDispatch != nil && notifierState != nil && listenerCondition != nil && listenerRoute != nil && listenerRoute.Subject == listenerCondition.Subject {
		// Both outcomes are already present in current source. A completed join
		// moves home; otherwise execution continues to the confirmation guard.
		// Do not manufacture an unknown merely because the choice is made at
		// runtime—the set of possible outcomes is statically complete.
		conditionStep := flowir.Step{ID: flowir.Hash(entry.FlowID, action.ID, condition.ID), BehaviorKey: entry.FlowID + ":system:condition:" + condition.ID, Order: 2, Actor: "system", TriggerFact: action.ID, BehaviorFacts: []string{condition.ID}, PrimaryEvidence: []flowir.Anchor{condition.Evidence[0]}, Status: flowir.Observed}
		observed := step2
		observed.Order = 3
		confirmationStep := flowir.Step{ID: flowir.Hash(entry.FlowID, action.ID, confirmation.ID), BehaviorKey: entry.FlowID + ":system:confirmation:" + confirmation.ID, Order: 4, Actor: "system", TriggerFact: condition.ID, BehaviorFacts: []string{confirmation.ID}, PrimaryEvidence: []flowir.Anchor{confirmation.Evidence[0]}, Status: flowir.Observed}
		conditionStep.Branches = []flowir.Branch{{ID: flowir.BranchID(condition.ID, []string{observed.BehaviorKey, confirmationStep.BehaviorKey}), ConditionFact: condition.ID, OutcomeStepIDs: []string{observed.ID, confirmationStep.ID}, Evidence: []flowir.Anchor{condition.Evidence[0]}, Status: flowir.Observed}}
		steps = []flowir.Step{step1, conditionStep, observed, confirmationStep}
		status = flowir.Observed
		unknowns = nil
	} else if condition != nil && transition.Subject == condition.Subject {
		conditionStep := flowir.Step{ID: flowir.Hash(entry.FlowID, action.ID, condition.ID), BehaviorKey: entry.FlowID + ":system:condition:" + condition.ID, Order: 2, Actor: "system", TriggerFact: action.ID, BehaviorFacts: []string{condition.ID}, PrimaryEvidence: []flowir.Anchor{condition.Evidence[0]}, Status: flowir.Mixed}
		observed := step2
		observed.Order = 3
		unknownStep := flowir.Step{ID: flowir.Hash(entry.FlowID, condition.ID, "unproven_conditional_outcome"), BehaviorKey: entry.FlowID + ":system:conditional_outcome:unknown", Order: 4, Actor: "system", TriggerFact: condition.ID, PrimaryEvidence: []flowir.Anchor{condition.Evidence[0]}, Status: flowir.Unknown}
		conditionStep.Branches = []flowir.Branch{{ID: flowir.BranchID(condition.ID, []string{observed.BehaviorKey, unknownStep.BehaviorKey}), ConditionFact: condition.ID, OutcomeStepIDs: []string{observed.ID, unknownStep.ID}, Evidence: []flowir.Anchor{condition.Evidence[0]}, Status: flowir.Unknown}}
		steps = []flowir.Step{step1, conditionStep, observed, unknownStep}
		status = flowir.Mixed
		unknowns = []flowir.UnknownDetail{{ID: flowir.Hash("unknown", "conditional_route_alternative", condition.ID), Question: "What happens when this route condition does not take the observed /home outcome?", Reason: "conditional_route_alternative", RelatedSteps: []string{unknownStep.ID}, Evidence: []flowir.Anchor{condition.Evidence[0]}}}
	}
	// Keep provider evidence as its own causal step. This is deliberately a
	// source-backed transition, not a claim about business intent. An unsupported
	// Riverpod shape stays unknown at this exact timeline position.
	if state != nil || unknownState != nil {
		stateFact := state
		if stateFact == nil {
			stateFact = unknownState
		}
		behavior := []string{}
		anchors := []flowir.Anchor{}
		if dependency != nil {
			behavior = append(behavior, dependency.ID)
			anchors = append(anchors, dependency.Evidence[0])
		}
		if operation != nil {
			behavior = append(behavior, operation.ID)
			anchors = append(anchors, operation.Evidence[0])
		}
		if len(anchors) == 0 {
			anchors = append(anchors, stateFact.Evidence[0])
		}
		for i := range steps {
			if steps[i].Order >= 2 {
				steps[i].Order++
			}
		}
		stateStep := flowir.Step{ID: flowir.Hash(entry.FlowID, action.ID, stateFact.ID), BehaviorKey: entry.FlowID + ":system:state_change:" + stateFact.Object, Order: 2, Actor: "system", TriggerFact: action.ID, BehaviorFacts: behavior, ResultFacts: []string{stateFact.ID}, PrimaryEvidence: anchors, Status: stateFact.Status}
		steps = append([]flowir.Step{steps[0], stateStep}, steps[1:]...)
		if stateFact.Status == flowir.Unknown {
			status = flowir.Mixed
			unknowns = append(unknowns, flowir.UnknownDetail{ID: flowir.Hash("unknown", "riverpod", stateFact.ID), Question: "Which Riverpod state transition occurs here?", Reason: "unsupported_riverpod_pattern", RelatedSteps: []string{stateStep.ID}, Evidence: []flowir.Anchor{stateFact.Evidence[0]}})
		}
	}
	if repository != nil {
		repositoryStep := flowir.Step{ID: flowir.Hash(entry.FlowID, action.ID, repository.ID), BehaviorKey: entry.FlowID + ":system:repository:" + repository.Object, Order: 2, Actor: "system", TriggerFact: action.ID, BehaviorFacts: []string{repository.ID}, ResultFacts: []string{repository.ID}, PrimaryEvidence: []flowir.Anchor{repository.Evidence[0]}, Status: flowir.Observed}
		steps = insertTimelineStep(steps, repositoryStep)
	}
	if external != nil {
		trigger := action.ID
		if repository != nil {
			trigger = repository.ID
		}
		result := externalResult
		if result == nil {
			result = externalUnknown
		}
		if result != nil {
			order := 2
			if repository != nil {
				order = 3
			}
			boundaryStep := flowir.Step{ID: flowir.Hash(entry.FlowID, trigger, external.ID), BehaviorKey: entry.FlowID + ":system:external:" + external.Object, Order: order, Actor: "system", TriggerFact: trigger, BehaviorFacts: []string{external.ID}, ResultFacts: []string{result.ID}, PrimaryEvidence: []flowir.Anchor{external.Evidence[0], result.Evidence[0]}, Status: result.Status}
			steps = insertTimelineStep(steps, boundaryStep)
			if result.Status == flowir.Unknown {
				status = flowir.Mixed
				unknowns = append(unknowns, flowir.UnknownDetail{ID: flowir.Hash("unknown", "external_boundary", result.ID), Question: "What result does this external boundary produce?", Reason: "EXTERNAL_BOUNDARY_UNKNOWN", RelatedSteps: []string{boundaryStep.ID}, Evidence: []flowir.Anchor{result.Evidence[0]}})
			}
		}
	}
	// CF-G13E admits one deliberately narrow causal chain. All five facts are
	// independently anchored by the adapter, and the listener route must belong
	// to the same listener body; otherwise no cancellation result is published.
	if confirmation != nil && terminalResult != nil && eventDispatch != nil && notifierState != nil && listenerCondition != nil && listenerRoute != nil && listenerRoute.Subject == listenerCondition.Subject {
		authVisible := flowir.Fact{ID: flowir.Hash(flowir.SchemaVersion, "visible_result", listenerRoute.Subject, listenerRoute.Object, listenerRoute.Evidence[0].Fingerprint), Kind: "visible_result", Subject: listenerRoute.Subject, Object: listenerRoute.Object, Proof: listenerRoute.Proof, SymbolID: listenerRoute.SymbolID, Evidence: listenerRoute.Evidence, Status: flowir.Observed}
		facts = append(facts, authVisible)
		nextOrder := 1
		confirmationIndex := -1
		for i := range steps {
			if steps[i].Order >= nextOrder {
				nextOrder = steps[i].Order + 1
			}
			if len(steps[i].BehaviorFacts) == 1 && steps[i].BehaviorFacts[0] == confirmation.ID {
				confirmationIndex = i
			}
		}
		if confirmationIndex < 0 {
			steps = append(steps, flowir.Step{ID: flowir.Hash(entry.FlowID, action.ID, confirmation.ID), BehaviorKey: entry.FlowID + ":system:confirmation:" + confirmation.ID, Order: nextOrder, Actor: "system", TriggerFact: action.ID, BehaviorFacts: []string{confirmation.ID}, PrimaryEvidence: []flowir.Anchor{confirmation.Evidence[0]}, Status: flowir.Observed})
			confirmationIndex = len(steps) - 1
			nextOrder++
		}
		terminalStep := flowir.Step{ID: flowir.Hash(entry.FlowID, confirmation.ID, terminalResult.ID), BehaviorKey: entry.FlowID + ":system:terminal:" + terminalResult.Object, Order: nextOrder, Actor: "system", TriggerFact: confirmation.ID, BehaviorFacts: []string{terminalResult.ID}, ResultFacts: []string{terminalResult.ID}, PrimaryEvidence: []flowir.Anchor{terminalResult.Evidence[0]}, Status: flowir.Observed}
		eventStep := flowir.Step{ID: flowir.Hash(entry.FlowID, confirmation.ID, eventDispatch.ID), BehaviorKey: entry.FlowID + ":system:event:" + eventDispatch.Object, Order: nextOrder + 1, Actor: "system", TriggerFact: confirmation.ID, BehaviorFacts: []string{eventDispatch.ID}, PrimaryEvidence: []flowir.Anchor{eventDispatch.Evidence[0]}, Status: flowir.Observed}
		stateStep := flowir.Step{ID: flowir.Hash(entry.FlowID, eventDispatch.ID, notifierState.ID), BehaviorKey: entry.FlowID + ":system:notifier_state:" + notifierState.Object, Order: nextOrder + 2, Actor: "system", TriggerFact: eventDispatch.ID, BehaviorFacts: []string{notifierState.ID}, ResultFacts: []string{notifierState.ID}, PrimaryEvidence: []flowir.Anchor{notifierState.Evidence[0]}, Status: flowir.Observed}
		listenerStep := flowir.Step{ID: flowir.Hash(entry.FlowID, notifierState.ID, listenerCondition.ID), BehaviorKey: entry.FlowID + ":system:listener_condition:" + listenerCondition.Object, Order: nextOrder + 3, Actor: "system", TriggerFact: notifierState.ID, BehaviorFacts: []string{listenerCondition.ID}, PrimaryEvidence: []flowir.Anchor{listenerCondition.Evidence[0]}, Status: flowir.Observed}
		authStep := flowir.Step{ID: flowir.Hash(entry.FlowID, listenerCondition.ID, listenerRoute.ID), BehaviorKey: entry.FlowID + ":system:route_transition:" + listenerRoute.Object, Order: nextOrder + 4, Actor: "system", TriggerFact: listenerCondition.ID, BehaviorFacts: []string{listenerRoute.ID}, ResultFacts: []string{authVisible.ID}, PrimaryEvidence: []flowir.Anchor{listenerRoute.Evidence[0]}, Status: flowir.Observed}
		steps[confirmationIndex].Branches = []flowir.Branch{{ID: flowir.BranchID(confirmation.ID, []string{terminalStep.BehaviorKey, eventStep.BehaviorKey}), ConditionFact: confirmation.ID, OutcomeStepIDs: []string{terminalStep.ID, eventStep.ID}, Evidence: []flowir.Anchor{confirmation.Evidence[0]}, Status: flowir.Observed}}
		steps = append(steps, terminalStep, eventStep, stateStep, listenerStep, authStep)
	}
	boundaries := []string{"ui", "application"}
	if backend != "" {
		boundaries = append(boundaries, "graph:"+backend)
	}
	components := []string{entry.Anchor.Path, action.Subject, transition.Subject}
	relations := []string{"call", "transition"}
	if dependency != nil {
		boundaries = append(boundaries, "state")
		components = append(components, dependency.Object)
		relations = append(relations, "provider_dependency")
	}
	if operation != nil {
		components = append(components, operation.Object)
		relations = append(relations, "notifier_operation")
	}
	if state != nil {
		components = append(components, state.Object)
		relations = append(relations, "state_transition")
	}
	if unknownState != nil {
		relations = append(relations, "unknown_state")
	}
	if repository != nil {
		boundaries = append(boundaries, "data")
		components = append(components, repository.Object)
		relations = append(relations, "repository_access")
	}
	if external != nil {
		boundaries = append(boundaries, "external")
		components = append(components, external.Object)
		relations = append(relations, "external_call")
	}
	if externalResult != nil {
		relations = append(relations, "external_contract_result")
	}
	if externalUnknown != nil {
		relations = append(relations, "EXTERNAL_BOUNDARY_UNKNOWN")
	}
	doc := flowir.Document{SchemaVersion: flowir.SchemaVersion, Basis: basis, Facts: facts, Architecture: flowir.ArchitectureSlice{EntryPoints: []string{entry.FlowID}, Boundaries: boundaries, Components: components, Relations: relations}, Current: flowir.Flow{ID: entry.FlowID, FlowKey: entry.FlowID, EntryPointFact: entryFact.ID, Steps: steps, Status: status}, Unknowns: unknowns}
	doc.CausalEdges = causalEdges(doc)
	attachDebtGuidance(&doc)
	return doc, flowir.Validate(doc)
}

// causalEdges makes every timeline assertion navigable in both directions.
// The compiler only projects explicit trigger/behavior/result references; it
// never guesses a relation from proximity or matching prose.
func causalEdges(doc flowir.Document) []flowir.CausalEdge {
	seen := map[string]bool{}
	var edges []flowir.CausalEdge
	add := func(from, to, kind string, conditions []string, evidence []flowir.Anchor, status flowir.Status) {
		if from == "" || to == "" || from == to || len(evidence) == 0 {
			return
		}
		id := flowir.CausalEdgeID(from, to, kind, conditions)
		if seen[id] {
			return
		}
		seen[id] = true
		edges = append(edges, flowir.CausalEdge{ID: id, FromFact: from, ToFact: to, Kind: kind, Conditions: conditions, Evidence: evidence, Status: status})
	}
	byKind := map[string][]flowir.Fact{}
	for _, fact := range doc.Facts {
		byKind[fact.Kind] = append(byKind[fact.Kind], fact)
	}
	link := func(from flowir.Fact, targetKinds []string, subject, kind string, conditions []string) {
		for _, targetKind := range targetKinds {
			for _, to := range byKind[targetKind] {
				if to.Subject != subject {
					continue
				}
				status := flowir.Observed
				if from.Status == flowir.Unknown || to.Status == flowir.Unknown {
					status = flowir.Unknown
				}
				add(from.ID, to.ID, kind, conditions, to.Evidence, status)
			}
		}
	}
	for _, entry := range byKind["entry_point"] {
		for _, action := range byKind["user_action"] {
			add(entry.ID, action.ID, "enters", nil, action.Evidence, action.Status)
		}
	}
	for _, action := range byKind["user_action"] {
		link(action, []string{"call"}, action.Subject, "invokes", nil)
		link(action, []string{"condition", "provider_dependency", "repository_access", "external_call", "confirmation_condition", "route_transition"}, action.Subject, "causes", nil)
	}
	for _, call := range byKind["call"] {
		link(call, []string{"call", "condition", "provider_dependency", "repository_access", "external_call", "route_transition"}, call.Object, "invokes", nil)
	}
	for _, dependency := range byKind["provider_dependency"] {
		link(dependency, []string{"notifier_operation"}, dependency.Object, "resolves_to", nil)
	}
	for _, operation := range byKind["notifier_operation"] {
		link(operation, []string{"state_transition", "unknown_state"}, operation.Object, "changes_state", nil)
	}
	for _, condition := range byKind["condition"] {
		link(condition, []string{"route_transition", "dynamic_dispatch"}, condition.Subject, "guards", []string{condition.ID})
	}
	for _, external := range byKind["external_call"] {
		link(external, []string{"external_result", "external_boundary_unknown"}, external.Object, "produces", nil)
	}
	for _, confirmation := range byKind["confirmation_condition"] {
		link(confirmation, []string{"event_dispatch", "terminal_result"}, confirmation.Subject, "permits", []string{confirmation.ID})
	}
	// Branch outcome edges are derived from the explicit FlowIR references, not
	// source proximity. This keeps the causal map navigable even when a bounded
	// adapter proves a continuation across separate callback/listener bodies.
	steps := map[string]flowir.Step{}
	for _, step := range doc.Current.Steps {
		steps[step.ID] = step
	}
	for _, step := range doc.Current.Steps {
		for _, branch := range step.Branches {
			for _, outcomeID := range branch.OutcomeStepIDs {
				outcome := steps[outcomeID]
				ids := append(append([]string{}, outcome.BehaviorFacts...), outcome.ResultFacts...)
				if len(ids) == 0 {
					continue
				}
				to := ids[0]
				if fact, ok := func() (flowir.Fact, bool) {
					for _, candidate := range doc.Facts {
						if candidate.ID == to {
							return candidate, true
						}
					}
					return flowir.Fact{}, false
				}(); ok {
					add(branch.ConditionFact, to, "permits", []string{branch.ConditionFact}, fact.Evidence, fact.Status)
				}
			}
		}
	}
	// These joins are admitted only when the bounded adapter has already
	// produced one unique, independently anchored cancellation chain.
	if len(byKind["event_dispatch"]) == 1 && len(byKind["notifier_state_transition"]) == 1 {
		add(byKind["event_dispatch"][0].ID, byKind["notifier_state_transition"][0].ID, "changes_state", nil, byKind["notifier_state_transition"][0].Evidence, flowir.Observed)
	}
	if len(byKind["notifier_state_transition"]) == 1 && len(byKind["listener_condition"]) == 1 {
		add(byKind["notifier_state_transition"][0].ID, byKind["listener_condition"][0].ID, "observed_by", nil, byKind["listener_condition"][0].Evidence, flowir.Observed)
	}
	for _, listener := range byKind["listener_condition"] {
		link(listener, []string{"route_transition"}, listener.Subject, "guards", nil)
	}
	for _, transition := range byKind["route_transition"] {
		for _, visible := range byKind["visible_result"] {
			if visible.Subject == transition.Subject && visible.Object == transition.Object {
				add(transition.ID, visible.ID, "produces", nil, visible.Evidence, visible.Status)
			}
		}
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	return edges
}

// attachDebtGuidance turns an unknown boundary into an actionable cognitive
// debt record without promoting it to observed behavior.
func attachDebtGuidance(doc *flowir.Document) {
	byStep := map[string][]string{}
	for _, edge := range doc.CausalEdges {
		for _, step := range doc.Current.Steps {
			for _, id := range append(append([]string{step.TriggerFact}, step.BehaviorFacts...), step.ResultFacts...) {
				if edge.FromFact == id || edge.ToFact == id {
					byStep[step.ID] = append(byStep[step.ID], edge.ID)
					break
				}
			}
		}
	}
	for i := range doc.Unknowns {
		u := &doc.Unknowns[i]
		u.DebtState = "open"
		for _, step := range u.RelatedSteps {
			u.RelatedEdges = append(u.RelatedEdges, byStep[step]...)
		}
		sort.Strings(u.RelatedEdges)
		u.RelatedEdges = uniqueStrings(u.RelatedEdges)
		switch u.Reason {
		case "unsupported_riverpod_pattern":
			u.ResolutionCriteria = []string{"A unique provider operation and its state assignment are source-anchored.", "The state consumer or visible result is source-anchored."}
			u.SuggestedEvidence = []string{"Dart Analyzer-resolved provider operation", "State-listener or widget test"}
		case "conditional_route_alternative", "dynamic_dispatch":
			u.ResolutionCriteria = []string{"Each condition outcome has a source-anchored target or explicit terminal result."}
			u.SuggestedEvidence = []string{"Resolved conditional target", "Focused route test"}
		case "EXTERNAL_BOUNDARY_UNKNOWN":
			u.ResolutionCriteria = []string{"A versioned external contract identifies the result and failure behavior."}
			u.SuggestedEvidence = []string{"Repository-local external contract"}
		default:
			u.ResolutionCriteria = []string{"A current source, test, or contract anchor proves the missing causal relation."}
			u.SuggestedEvidence = []string{"Current source anchor", "Focused behavior test or contract"}
		}
		u.Impact = []string{doc.Current.ID}
	}
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := values[:0]
	for _, value := range values {
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	return out
}
