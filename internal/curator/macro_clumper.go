package curator

import (
	"fmt"
	"strings"

	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/slicing"
)

// CandidateFrame represents an unranked gateway scene produced by the macro-clumping stage.
type CandidateFrame struct {
	Role            string // entry | decision | process | effect | result | boundary
	Title           string
	PrimaryStepRef  string
	StepRefs        []string
	Anchor          slicing.Anchor
	Condition       *string
	CollapsedDetail *CollapsedDetail
	IsRecursion     bool
	Score           int
	SymbolPath      string
	Status          string
}

func getStepID(step fusion.FlowStep, idx int) string {
	if step.StepID != nil && *step.StepID != "" {
		return *step.StepID
	}
	return fmt.Sprintf("step-%03d", idx+1)
}

func computeFusionStepStatus(step fusion.FlowStep) string {
	if step.Kind == "boundary" || step.Provenance == "unknown" || step.Freshness == "orphaned" {
		return "unknown"
	}
	for _, r := range step.Rules {
		if strings.HasPrefix(r, "boundary:") || strings.Contains(strings.ToLower(r), "boundary") {
			return "unknown"
		}
	}
	if step.Anchor.RepoRelativePath == "" || step.Freshness == "stale" || step.Provenance == "derived" || (step.Confidence > 0 && step.Confidence < 0.5) {
		return "partial"
	}
	return "verified"
}

// MacroClumper combines micro-steps into cohesive macro gateway blocks and collapses recursion cycles.
type MacroClumper struct{}

// NewMacroClumper creates a MacroClumper instance.
func NewMacroClumper() *MacroClumper {
	return &MacroClumper{}
}

// Clump performs the first pass of curation:
// 1. Detects cycles (direct and indirect A->B->C->A) and collapses them into a single recursive frame.
// 2. Combines consecutive guard statements into a single decision frame.
// 3. Combines consecutive mutation statements into a single process frame.
// 4. Absorbs continuous non-business steps into the preceding frame.
func (mc *MacroClumper) Clump(steps []fusion.FlowStep, edges []fusion.FlowEdge) []CandidateFrame {
	if len(steps) == 0 {
		return nil
	}

	// 1. Check for cycles/recursion (direct A->A or indirect A->B->C->A)
	// We map symbol paths and look for repeat occurrences that form a cycle.
	cycleStartIdx := -1
	cycleEndIdx := -1
	symbolFirstSeen := make(map[string]int)

	for i, step := range steps {
		sym := step.Anchor.EnclosingSymbolPath
		if sym == "" {
			sym = step.Name
		}
		if firstIdx, seen := symbolFirstSeen[sym]; seen {
			// Only consider it a cycle if execution left sym in between (visited at least one different symbol)
			hasLeft := false
			for k := firstIdx + 1; k < i; k++ {
				ksym := steps[k].Anchor.EnclosingSymbolPath
				if ksym == "" {
					ksym = steps[k].Name
				}
				if ksym != sym {
					hasLeft = true
					break
				}
			}
			if hasLeft {
				cycleStartIdx = firstIdx
				cycleEndIdx = i
				break
			}
		} else {
			symbolFirstSeen[sym] = i
		}
	}

	// If edge explicitly declares a cycle
	if cycleStartIdx == -1 {
		for _, e := range edges {
			if e.Kind == "cycle" || e.ResolutionStatus == "cycle" {
				// Find matching step indices
				for i, s := range steps {
					if s.Anchor.EnclosingSymbolPath == e.ToSymbolPath && cycleStartIdx == -1 {
						cycleStartIdx = i
					}
					if s.Anchor.EnclosingSymbolPath == e.ToSymbolPath && i > cycleStartIdx && cycleStartIdx != -1 {
						cycleEndIdx = i
						break
					}
				}
				if cycleStartIdx != -1 && cycleEndIdx == -1 {
					cycleEndIdx = len(steps) - 1
				}
				break
			}
		}
	}

	var candidates []CandidateFrame

	for i := 0; i < len(steps); i++ {
		step := steps[i]
		sym := step.Anchor.EnclosingSymbolPath
		if sym == "" {
			sym = step.Name
		}

		// Handle Cycle / Recursion Clumping
		if cycleStartIdx != -1 && i == cycleStartIdx {
			// Absorb all steps from cycleStartIdx through cycleEndIdx into this single frame
			var cycleRefs []string
			var cycleNames []string
			cycleStatus := "verified"
			for ci := cycleStartIdx; ci <= cycleEndIdx && ci < len(steps); ci++ {
				cycleRefs = append(cycleRefs, getStepID(steps[ci], ci))
				csym := steps[ci].Anchor.EnclosingSymbolPath
				if csym == "" {
					csym = steps[ci].Name
				}
				if len(cycleNames) == 0 || cycleNames[len(cycleNames)-1] != csym {
					cycleNames = append(cycleNames, csym)
				}
				cs := computeFusionStepStatus(steps[ci])
				if (cs == "unknown" || cs == "partial") && cycleStatus == "verified" {
					cycleStatus = "partial"
				}
			}
			if len(cycleNames) > 0 && cycleNames[len(cycleNames)-1] != cycleNames[0] {
				cycleNames = append(cycleNames, cycleNames[0])
			}

			cycleReason := fmt.Sprintf("재귀/순환 실행 경로 접힘 (사이클: %s)", strings.Join(cycleNames, " -> "))
			collapsedCount := len(cycleRefs) - 1
			if collapsedCount < 1 {
				collapsedCount = 1
			}

			candidates = append(candidates, CandidateFrame{
				Role:           "process",
				Title:          step.Name,
				PrimaryStepRef: getStepID(step, i),
				StepRefs:       cycleRefs,
				Anchor:         step.Anchor,
				Condition:      step.Branch,
				IsRecursion:    true,
				CollapsedDetail: &CollapsedDetail{
					Count:  collapsedCount,
					Reason: cycleReason,
				},
				Score:      85,
				SymbolPath: sym,
				Status:     cycleStatus,
			})

			i = cycleEndIdx
			continue
		}

		// Determine role of step
		role := "process"
		if i == 0 {
			role = "entry"
		} else if i == len(steps)-1 && (step.Kind == "return" || step.Kind == "result") {
			role = "result"
		} else if step.Kind == "guard" || step.Kind == "branch" || step.Kind == "decision" || step.Branch != nil {
			role = "decision"
		} else if step.Kind == "mutation" {
			role = "process"
		} else if step.Kind == "external_effect" || step.Kind == "effect" {
			role = "effect"
		} else if step.Kind == "boundary" {
			role = "boundary"
		} else {
			// Check if this step can be merged into the previous candidate
			if len(candidates) > 0 {
				last := &candidates[len(candidates)-1]
				// Continuous internal calculation or utility call -> absorb into previous frame
				last.StepRefs = append(last.StepRefs, getStepID(step, i))
				st := computeFusionStepStatus(step)
				if (st == "unknown" || st == "partial") && last.Status == "verified" {
					last.Status = "partial"
				}
				if last.CollapsedDetail == nil {
					last.CollapsedDetail = &CollapsedDetail{
						Count:  0,
						Reason: "연속 내부 처리 단계 접힘",
					}
				}
				last.CollapsedDetail.Count++
				continue
			}
		}

		// Consecutive guard merging within same enclosing symbol:
		if role == "decision" && len(candidates) > 0 && candidates[len(candidates)-1].Role == "decision" && candidates[len(candidates)-1].SymbolPath == sym {
			last := &candidates[len(candidates)-1]
			last.StepRefs = append(last.StepRefs, getStepID(step, i))
			last.Title = "사전 유효성 검증"
			st := computeFusionStepStatus(step)
			if (st == "unknown" || st == "partial") && last.Status == "verified" {
				last.Status = "partial"
			}
			if last.CollapsedDetail == nil {
				last.CollapsedDetail = &CollapsedDetail{
					Count:  0,
					Reason: "연속 유효성 검증 가드 병합",
				}
			}
			last.CollapsedDetail.Count++
			continue
		}

		// Consecutive mutation merging within same enclosing symbol:
		if role == "process" && len(candidates) > 0 && candidates[len(candidates)-1].Role == "process" && candidates[len(candidates)-1].SymbolPath == sym && !candidates[len(candidates)-1].IsRecursion {
			last := &candidates[len(candidates)-1]
			last.StepRefs = append(last.StepRefs, getStepID(step, i))
			st := computeFusionStepStatus(step)
			if (st == "unknown" || st == "partial") && last.Status == "verified" {
				last.Status = "partial"
			}
			if last.CollapsedDetail == nil {
				last.CollapsedDetail = &CollapsedDetail{
					Count:  0,
					Reason: "연속 상태 변경 처리 병합",
				}
			}
			last.CollapsedDetail.Count++
			continue
		}

		initialStatus := computeFusionStepStatus(step)
		if role == "boundary" {
			initialStatus = "unknown"
		}

		// Create new CandidateFrame
		candidates = append(candidates, CandidateFrame{
			Role:           role,
			Title:          step.Name,
			PrimaryStepRef: getStepID(step, i),
			StepRefs:       []string{getStepID(step, i)},
			Anchor:         step.Anchor,
			Condition:      step.Branch,
			Score:          roleScore(role),
			SymbolPath:     sym,
			Status:         initialStatus,
		})
	}

	return candidates
}

func roleScore(role string) int {
	switch role {
	case "entry":
		return 100
	case "effect":
		return 90
	case "result":
		return 85
	case "process":
		return 85
	case "decision":
		return 80
	case "boundary":
		return 70
	default:
		return 50
	}
}
