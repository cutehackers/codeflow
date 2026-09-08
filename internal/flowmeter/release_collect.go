package flowmeter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var releaseVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

type ReleaseCollection struct {
	SchemaVersion     int                      `json:"schemaVersion"`
	TargetVersion     string                   `json:"targetVersion"`
	SourceRevision    string                   `json:"sourceRevision"`
	SourceDirty       bool                     `json:"sourceDirty"`
	StartedAt         string                   `json:"startedAt"`
	CompletedAt       string                   `json:"completedAt"`
	BenchmarkRef      string                   `json:"benchmarkRef"`
	Checks            []ReleaseCollectionCheck `json:"checks"`
	Status            string                   `json:"status"`
	ReleaseReady      bool                     `json:"releaseReady"`
	RemainingEvidence []string                 `json:"remainingEvidence"`
}

type ReleaseCollectionCheck struct {
	ID          string `json:"id"`
	Command     string `json:"command"`
	ExitCode    int    `json:"exitCode"`
	StartedAt   string `json:"startedAt"`
	CompletedAt string `json:"completedAt"`
	LogPath     string `json:"logPath"`
	LogRef      string `json:"logRef"`
	Result      string `json:"result"`
}

type releaseCommand struct {
	id   string
	args []string
	dir  string
}

func runRelease(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		_, _ = fmt.Fprintln(stdout, "사용법: flowmeter release collect --target-version vX.Y.Z [--dir <경로>]")
		_, _ = fmt.Fprintln(stdout, "수집 후 승인과 최종 판정은 flowmeter release approve/finalize --dir <경로>를 사용합니다.")
		return 0
	}
	switch args[0] {
	case "collect":
		return runReleaseCollect(ctx, args[1:], stdout, stderr)
	case "approve":
		return runApprove(args[1:], stdin, stdout, stderr)
	case "finalize":
		return runFinalize(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "알 수 없는 release 명령: %s\n", args[0])
		return 2
	}
}

func runReleaseCollect(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("release collect", flag.ContinueOnError)
	flags.SetOutput(stderr)
	targetVersion := flags.String("target-version", "", "release target version")
	dir := flags.String("dir", "", "release collection directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if !releaseVersionPattern.MatchString(*targetVersion) || flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "사용법: flowmeter release collect --target-version vX.Y.Z [--dir <경로>]")
		return 2
	}
	repoRoot, err := gitOutput(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "CodeFlow Git 저장소 확인 실패: %v\n", err)
		return 1
	}
	if *dir == "" {
		*dir = filepath.Join(repoRoot, ".codeflow", "flowmeter", "releases", safeBenchmarkPathPart(*targetVersion), time.Now().UTC().Format("20060102T150405.000000000Z"))
	} else if !filepath.IsAbs(*dir) {
		*dir = filepath.Join(repoRoot, *dir)
	}
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		_, _ = fmt.Fprintf(stderr, "release 수집 디렉터리 생성 실패: %v\n", err)
		return 1
	}
	manifestPath := filepath.Join(*dir, "release-collection.json")
	if _, err := os.Stat(manifestPath); err == nil {
		_, _ = fmt.Fprintf(stderr, "기존 수집 결과를 덮어쓰지 않았습니다: %s\n", manifestPath)
		return 1
	}
	started := time.Now().UTC()
	revision, err := gitOutputAt(ctx, repoRoot, "rev-parse", "HEAD")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "source revision 확인 실패: %v\n", err)
		return 1
	}
	dirtyOutput, err := gitOutputAt(ctx, repoRoot, "status", "--porcelain")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "작업 트리 상태 확인 실패: %v\n", err)
		return 1
	}
	collection := ReleaseCollection{
		SchemaVersion: 1, TargetVersion: *targetVersion, SourceRevision: revision,
		SourceDirty: strings.TrimSpace(dirtyOutput) != "", StartedAt: started.Format(time.RFC3339Nano),
		ReleaseReady: false,
		RemainingEvidence: []string{
			"12개 production scenario 결과를 evaluator execution-report 형식으로 연결",
			"고정 정답 corpus에서 precision, recall, semantic_delta, alignment_validity, unknown_coverage 측정",
			"실제 comprehension study 결과와 peak process memory 측정",
			"registry 로그를 VS01~VS09 child-evidence 형식으로 연결",
			"10개 hard invariant 결과를 scenario trace와 연결",
		},
	}
	_, _ = fmt.Fprintf(stdout, "release %s 근거 수집을 시작합니다: %s\n", *targetVersion, *dir)
	benchmark, err := measureFixedBenchmark(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "고정 성능 benchmark 실패: %v\n", err)
		return 1
	}
	benchmarkPath := filepath.Join(*dir, "benchmark-result.json")
	if err := writeJSONExclusive(benchmarkPath, benchmark); err != nil {
		_, _ = fmt.Fprintf(stderr, "benchmark 저장 실패: %v\n", err)
		return 1
	}
	collection.BenchmarkRef, err = fileRef(benchmarkPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "benchmark digest 실패: %v\n", err)
		return 1
	}
	commands := releaseVerificationCommands(repoRoot)
	failed := benchmark.Status != "passed"
	for _, command := range commands {
		_, _ = fmt.Fprintf(stdout, "[%s] 실행 중\n", command.id)
		check := executeReleaseCommand(ctx, *dir, command)
		collection.Checks = append(collection.Checks, check)
		if check.Result != "pass" {
			failed = true
			_, _ = fmt.Fprintf(stderr, "[%s] 실패했습니다. 로그: %s\n", command.id, check.LogPath)
			break
		}
	}
	collection.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	collection.Status = "incomplete"
	if failed {
		collection.Status = "failed"
	} else if collection.SourceDirty {
		collection.RemainingEvidence = append([]string{"릴리즈 후보를 clean Git revision으로 다시 수집"}, collection.RemainingEvidence...)
	}
	if err := writeJSONExclusive(manifestPath, collection); err != nil {
		_, _ = fmt.Fprintf(stderr, "수집 manifest 저장 실패: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "수집 결과: %s\n상태: %s\n", manifestPath, collection.Status)
	if failed {
		return 1
	}
	_, _ = fmt.Fprintln(stdout, "자동 수집은 완료됐지만 releaseReady는 아닙니다. manifest의 remainingEvidence를 준비한 뒤 승인해야 합니다.")
	return 0
}

func releaseVerificationCommands(repoRoot string) []releaseCommand {
	return []releaseCommand{
		{id: "vs01-vs09-registries", dir: repoRoot, args: []string{"go", "test", "-v", "./internal/contractharness", "-run", `^TestRFLSCR2VS0[1-9]_EvidenceRegistry$`, "-count=1"}},
		{id: "go-regression", dir: repoRoot, args: []string{"go", "test", "-count=1", "./..."}},
		{id: "dart-regression", dir: filepath.Join(repoRoot, "adapters", "dart"), args: []string{"dart", "test"}},
		{id: "typescript-regression", dir: repoRoot, args: []string{"node", "adapters/typescript/test/index.test.js"}},
	}
}

func executeReleaseCommand(ctx context.Context, outputDir string, command releaseCommand) ReleaseCollectionCheck {
	started := time.Now().UTC()
	check := ReleaseCollectionCheck{ID: command.id, Command: strings.Join(command.args, " "), StartedAt: started.Format(time.RFC3339Nano), ExitCode: 0, Result: "pass"}
	process := exec.CommandContext(ctx, command.args[0], command.args[1:]...)
	process.Dir = command.dir
	output, err := process.CombinedOutput()
	if err != nil {
		output = append(output, []byte("\ncommand error: "+err.Error()+"\n")...)
		check.Result = "fail"
		check.ExitCode = 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			check.ExitCode = exitErr.ExitCode()
		}
	}
	check.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	check.LogPath = filepath.Join("logs", command.id+".log")
	logPath := filepath.Join(outputDir, check.LogPath)
	_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
	if writeErr := os.WriteFile(logPath, output, 0o600); writeErr != nil {
		check.Result = "fail"
		check.ExitCode = 1
		return check
	}
	digest := sha256.Sum256(output)
	check.LogRef = "sha256:" + hex.EncodeToString(digest[:])
	return check
}

func gitOutput(ctx context.Context, args ...string) (string, error) {
	return gitOutputAt(ctx, "", args...)
}

func gitOutputAt(ctx context.Context, dir string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = dir
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func fileRef(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}
