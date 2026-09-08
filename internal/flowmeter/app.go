// Package flowmeter coordinates repeatable CodeFlow release measurements.
package flowmeter

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const helpText = `flowmeter는 고정된 CodeFlow benchmark corpus로 성능을 측정하고 비교합니다.

가장 쉬운 사용법:
  flowmeter run              현재 CodeFlow 빌드의 성능을 측정합니다.
  flowmeter compare          같은 corpus와 환경의 최근 두 결과를 비교합니다.

v0.4.0 release 근거 수집:
  flowmeter release collect --target-version v0.4.0

VS-10 release evidence 고급 사용법:
  flowmeter prepare --dir <경로>          12개 시나리오의 측정 계획을 준비합니다.
  flowmeter run --dir <경로>              계획의 수집 명령을 실행합니다.
  flowmeter approve --dir <경로>          측정값의 품질 기준을 승인합니다.
  flowmeter finalize --dir <경로>         전체 근거의 최종 상태를 판정합니다.
  flowmeter compare latest --root <경로>  최근 두 release 결과를 비교합니다.

각 명령은 완료 후 다음에 실행할 명령을 알려줍니다.
`

// Run executes the standalone flowmeter command and returns its process exit code.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		_, _ = fmt.Fprint(stdout, helpText)
		return 0
	}
	if args[0] == "prepare" {
		return runPrepare(ctx, args[1:], stdin, stdout, stderr)
	}
	if args[0] == "release" {
		return runRelease(ctx, args[1:], stdin, stdout, stderr)
	}
	if args[0] == "run" {
		if len(args) == 1 || args[1] != "--dir" {
			return runBenchmark(ctx, args[1:], stdout, stderr)
		}
		return runCollectors(ctx, args[1:], stdout, stderr)
	}
	if args[0] == "approve" {
		return runApprove(args[1:], stdin, stdout, stderr)
	}
	if args[0] == "finalize" {
		return runFinalize(args[1:], stdout, stderr)
	}
	if args[0] == "compare" {
		return runCompare(args[1:], stdout, stderr)
	}
	if args[0] == "." && len(args) == 1 {
		return runBenchmark(ctx, nil, stdout, stderr)
	}
	_, _ = fmt.Fprintf(stderr, "알 수 없는 명령: %s\n\n%s", args[0], helpText)
	return 2
}

func runCollectors(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dir := flags.String("dir", "", "measurement run directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *dir == "" || flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "사용법: flowmeter run --dir <실행 디렉터리>")
		return 2
	}
	plan, err := loadPlan(filepath.Join(*dir, "flowmeter-plan.json"))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "측정 계획 읽기 실패: %v\n", err)
		return 1
	}
	if len(plan.Commands) == 0 {
		_, _ = fmt.Fprintln(stderr, "실행할 수집 명령이 없습니다.")
		return 1
	}
	for _, command := range plan.Commands {
		if strings.TrimSpace(command.ID) == "" || strings.TrimSpace(command.Command) == "" {
			_, _ = fmt.Fprintf(stderr, "%q 수집 명령이 비어 있습니다. flowmeter-plan.json에 실제 명령을 입력하세요.\n", command.ID)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "[%s] %s\n", command.ID, command.Description)
		process := shellCommand(ctx, command.Command)
		process.Dir = *dir
		process.Stdout = stdout
		process.Stderr = stderr
		if err := process.Run(); err != nil {
			_, _ = fmt.Fprintf(stderr, "[%s] 실행 실패: %v\n", command.ID, err)
			return 1
		}
	}
	for _, name := range plan.RequiredArtifacts {
		path, err := containedArtifactPath(*dir, name)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "잘못된 required artifact %q: %v\n", name, err)
			return 1
		}
		info, err := os.Lstat(path)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "필수 실행 근거가 없습니다: %s\n", name)
			return 1
		}
		if !info.Mode().IsRegular() {
			_, _ = fmt.Fprintf(stderr, "필수 실행 근거가 일반 파일이 아닙니다: %s\n", name)
			return 1
		}
	}
	_, _ = fmt.Fprintf(stdout, "필수 실행 근거 파일을 모두 확인했습니다. 다음: flowmeter approve --dir %s\n", *dir)
	return 0
}

func shellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd", "/C", command)
	}
	return exec.CommandContext(ctx, "sh", "-c", command)
}

func loadPlan(path string) (Plan, error) {
	file, err := os.Open(path)
	if err != nil {
		return Plan{}, err
	}
	defer file.Close()
	var plan Plan
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return Plan{}, err
	}
	if plan.SchemaVersion != 1 || strings.TrimSpace(plan.TargetVersion) == "" {
		return Plan{}, fmt.Errorf("지원하지 않거나 불완전한 측정 계획")
	}
	artifacts := make(map[string]bool, len(plan.RequiredArtifacts))
	for _, artifact := range plan.RequiredArtifacts {
		if artifacts[artifact] {
			return Plan{}, fmt.Errorf("중복 required artifact: %s", artifact)
		}
		artifacts[artifact] = true
	}
	for _, artifact := range requiredMeasurementArtifacts {
		if !artifacts[artifact] {
			return Plan{}, fmt.Errorf("필수 required artifact가 빠졌습니다: %s", artifact)
		}
	}
	if len(artifacts) != len(requiredMeasurementArtifacts) {
		return Plan{}, fmt.Errorf("알 수 없는 required artifact가 있습니다")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Plan{}, fmt.Errorf("측정 계획에는 JSON 객체 하나만 있어야 합니다")
	}
	return plan, nil
}

func containedArtifactPath(dir, name string) (string, error) {
	if name == "" || filepath.IsAbs(name) {
		return "", fmt.Errorf("실행 디렉터리 내부의 상대 경로여야 합니다")
	}
	clean := filepath.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("실행 디렉터리 밖을 가리킬 수 없습니다")
	}
	return filepath.Join(dir, clean), nil
}

type Plan struct {
	SchemaVersion     int           `json:"schemaVersion"`
	TargetVersion     string        `json:"targetVersion"`
	Commands          []PlanCommand `json:"commands"`
	RequiredArtifacts []string      `json:"requiredArtifacts"`
}

type PlanCommand struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Command     string `json:"command"`
}

var requiredMeasurementArtifacts = []string{
	"release-profile.json",
	"scenario-manifest.json",
	"execution-reports.json",
	"child-evidence.json",
}

func runPrepare(_ context.Context, args []string, _ io.Reader, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("prepare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dir := flags.String("dir", "", "measurement run directory")
	targetVersion := flags.String("target-version", "", "release target version")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *dir == "" || *targetVersion == "" || flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "사용법: flowmeter prepare --dir <실행 디렉터리> --target-version <버전>")
		return 2
	}
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		_, _ = fmt.Fprintf(stderr, "실행 디렉터리 생성 실패: %v\n", err)
		return 1
	}
	plan := Plan{
		SchemaVersion: 1,
		TargetVersion: *targetVersion,
		Commands: []PlanCommand{
			{ID: "scenarios", Description: "12개 실제 시나리오와 9개 측정값을 수집합니다."},
			{ID: "invariants", Description: "10개 hard invariant를 실제로 검사합니다."},
			{ID: "child-evidence", Description: "VS01부터 VS09까지 실행 근거를 수집합니다."},
		},
		RequiredArtifacts: append([]string(nil), requiredMeasurementArtifacts...),
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "측정 계획 생성 실패: %v\n", err)
		return 1
	}
	data = append(data, '\n')
	path := filepath.Join(*dir, "flowmeter-plan.json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			_, _ = fmt.Fprintf(stderr, "기존 측정 계획을 덮어쓰지 않았습니다: %s\n", path)
		} else {
			_, _ = fmt.Fprintf(stderr, "측정 계획 저장 실패: %v\n", err)
		}
		return 1
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_, _ = fmt.Fprintf(stderr, "측정 계획 저장 실패: %v\n", err)
		return 1
	}
	if err := file.Close(); err != nil {
		_, _ = fmt.Fprintf(stderr, "측정 계획 저장 실패: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "측정 계획을 만들었습니다: %s\n각 command를 실제 실행 명령으로 채운 뒤 flowmeter run --dir %s 를 실행하세요.\n", path, *dir)
	return 0
}
