package mcp

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"codeflow/internal/flowview"
	"codeflow/internal/semantic"
)

// onboardingRequestFromArgs is the MCP boundary parser.  It deliberately
// does not invent a repository, active basis, or historical generation.
func onboardingRequestFromArgs(args map[string]any) (flowview.OnboardingRequest, error) {
	request := flowview.OnboardingRequest{
		RepositoryID: strings.TrimSpace(stringArgument(args, "repositoryId")),
		Domain:       strings.TrimSpace(stringArgument(args, "domain")),
		Freshness:    strings.TrimSpace(stringArgument(args, "freshness")),
		ComputedBasisID: strings.TrimSpace(firstStringArgument(args,
			"computedBasisId", "basisId")),
		GenerationID: strings.TrimSpace(firstStringArgument(args,
			"generationId", "genId")),
		SnapshotID: strings.TrimSpace(firstStringArgument(args,
			"validatedAgainstSnapshotId", "snapshotId")),
	}
	if request.RepositoryID == "" {
		return flowview.OnboardingRequest{}, fmt.Errorf("missing_precondition: repositoryId is required")
	}
	if request.Freshness == "" {
		return flowview.OnboardingRequest{}, fmt.Errorf("missing_precondition: freshness is required and must be current or historical")
	}
	level, err := integerArgument(args, "level")
	if err != nil {
		return flowview.OnboardingRequest{}, fmt.Errorf("invalid_precondition: level must be an integer")
	}
	request.Level = level
	if request.Level == 0 {
		request.Level = 1
	}
	if request.Level < 0 {
		return flowview.OnboardingRequest{}, fmt.Errorf("invalid_precondition: level must be positive")
	}
	request.MaxVisibleCoreSteps, err = integerArgument(args, "maxVisibleCoreSteps")
	if err != nil {
		return flowview.OnboardingRequest{}, fmt.Errorf("invalid_precondition: maxVisibleCoreSteps must be an integer")
	}
	if _, present := args["maxVisibleCoreSteps"]; present {
		request.MaxVisibleCoreStepsProvided = true
		if request.MaxVisibleCoreSteps <= 0 {
			return flowview.OnboardingRequest{}, fmt.Errorf("invalid_precondition: maxVisibleCoreSteps must be a positive integer")
		}
	}
	if value, present := args["displayBudget"]; present {
		budget, err := parseMCPDisplayBudget(value)
		if err != nil {
			return flowview.OnboardingRequest{}, err
		}
		if request.MaxVisibleCoreStepsProvided && request.MaxVisibleCoreSteps != budget.TargetMax {
			return flowview.OnboardingRequest{}, fmt.Errorf("invalid_precondition: conflicting maxVisibleCoreSteps and displayBudget.targetMax")
		}
		request.DisplayBudget = &budget
		request.MaxVisibleCoreSteps = budget.TargetMax
	}
	return request, nil
}

func parseMCPDisplayBudget(value any) (semantic.DisplayBudget, error) {
	budgetMap, ok := value.(map[string]any)
	if !ok {
		return semantic.DisplayBudget{}, fmt.Errorf("invalid_precondition: displayBudget must be an object")
	}
	for key := range budgetMap {
		if key != "targetMin" && key != "targetMax" && key != "enforcement" {
			return semantic.DisplayBudget{}, fmt.Errorf("invalid_precondition: unsupported displayBudget field %q", key)
		}
	}
	min, minErr := integerArgument(budgetMap, "targetMin")
	max, maxErr := integerArgument(budgetMap, "targetMax")
	if minErr != nil || maxErr != nil || min <= 0 || max <= 0 {
		return semantic.DisplayBudget{}, fmt.Errorf("invalid_precondition: displayBudget targetMin and targetMax must be positive integers")
	}
	enforcement, ok := budgetMap["enforcement"].(string)
	if !ok || enforcement != "soft" {
		return semantic.DisplayBudget{}, fmt.Errorf("invalid_precondition: displayBudget.enforcement must be soft")
	}
	if min > max {
		return semantic.DisplayBudget{}, fmt.Errorf("invalid_precondition: displayBudget targetMin must not exceed targetMax")
	}
	return semantic.DisplayBudget{TargetMin: min, TargetMax: max, Enforcement: enforcement}, nil
}

func firstStringArgument(args map[string]any, names ...string) string {
	for _, name := range names {
		if value := stringArgument(args, name); value != "" {
			return value
		}
	}
	return ""
}

func integerArgument(args map[string]any, key string) (int, error) {
	raw, present := args[key]
	if !present || raw == nil {
		return 0, nil
	}
	switch value := raw.(type) {
	case int:
		return value, nil
	case int8:
		return int(value), nil
	case int16:
		return int(value), nil
	case int32:
		return int(value), nil
	case int64:
		return int(value), nil
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value {
			return 0, fmt.Errorf("fractional or non-finite number")
		}
		return int(value), nil
	case float32:
		f := float64(value)
		if math.IsNaN(f) || math.IsInf(f, 0) || math.Trunc(f) != f {
			return 0, fmt.Errorf("fractional or non-finite number")
		}
		return int(value), nil
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return 0, err
		}
		return n, nil
	default:
		return 0, fmt.Errorf("unsupported number type %T", raw)
	}
}
