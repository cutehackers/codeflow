package flowview

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codeflow/internal/evidence"
)

func TestApprovalPublicPersistenceFaultMatrix(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	args := []string{"test", "-overlay", evidence.FaultOverlay(t, root), "./internal/flowview", "-run", "^TestApprovalPublicPersistenceFaultsOverlay$", "-count=1", "-timeout=120s", "-v"}
	if evidence.RaceEnabled {
		args = append(args, "-race")
	}
	command := exec.CommandContext(ctx, "go", args...)
	command.Dir = root
	command.Env = append(os.Environ(), "CODEFLOW_VS09_CRITERION=", "CODEFLOW_VS09_CHALLENGE=")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("public fault matrix: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "--- PASS: TestApprovalPublicPersistenceFaultsOverlay") || strings.Contains(string(output), "--- SKIP:") {
		t.Fatalf("public fault matrix did not execute: %s", output)
	}
	t.Log(string(output))
}
