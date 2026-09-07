package mcp

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestExploreProjectDomainsRejectsExplicitZeroBudget(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codeflow-mcp-onboarding-budget-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	srv, err := NewServer(Config{RepoRoot: tmpDir, RequireToken: false})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer srv.Close()

	_, err = srv.executeTool(context.Background(), "explore_project_domains", map[string]any{
		"target": tmpDir, "repositoryId": "repo-budget", "freshness": "historical",
		"computedBasisId": "basis-budget", "generationId": "generation-budget",
		"validatedAgainstSnapshotId": "snapshot-budget", "maxVisibleCoreSteps": float64(0),
	})
	if err == nil || !strings.Contains(err.Error(), "maxVisibleCoreSteps must be a positive integer") {
		t.Fatalf("expected explicit zero budget rejection, got %v", err)
	}
}

func TestOnboardingTaskViewRejectsStrictBudgetAndPreservesTargetMin(t *testing.T) {
	strict := map[string]any{
		"repositoryId": "repo-budget", "freshness": "historical",
		"computedBasisId": "basis-budget", "generationId": "generation-budget",
		"validatedAgainstSnapshotId": "snapshot-budget",
		"displayBudget":              map[string]any{"targetMin": float64(3), "targetMax": float64(5), "enforcement": "strict"},
	}
	if _, err := onboardingRequestFromArgs(strict); err == nil || !strings.Contains(err.Error(), "enforcement") {
		t.Fatalf("expected strict display budget rejection, got %v", err)
	}
	soft := map[string]any{
		"repositoryId": "repo-budget", "freshness": "historical",
		"computedBasisId": "basis-budget", "generationId": "generation-budget",
		"validatedAgainstSnapshotId": "snapshot-budget",
		"displayBudget":              map[string]any{"targetMin": float64(3), "targetMax": float64(5), "enforcement": "soft"},
	}
	request, err := onboardingRequestFromArgs(soft)
	if err != nil {
		t.Fatalf("parse complete soft display budget: %v", err)
	}
	if request.DisplayBudget == nil || request.DisplayBudget.TargetMin != 3 || request.DisplayBudget.TargetMax != 5 || request.DisplayBudget.Enforcement != "soft" {
		t.Fatalf("display budget targetMin/targetMax was not preserved: %+v", request.DisplayBudget)
	}
}
