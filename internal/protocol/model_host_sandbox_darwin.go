//go:build darwin

package protocol

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type modelHostIsolationSetup struct {
	command                       string
	args                          []string
	backend                       string
	enforcement                   string
	networkPolicy                 string
	policyDigest                  string
	runtimePolicyBinding          string
	runtimePolicySharedBaseDigest string
	probeScope                    string
	probePolicyDigest             string
	probeSharedBaseDigest         string
	runtimePolicyProfilePath      string
	runtimePolicyProfileIdentity  modelHostSandboxProfileIdentity
	trustedProbe                  ModelHostIsolationProbe
	runtimeRepositoryWriteAudit   *modelHostRuntimeRepositoryWriteAudit
}

type modelHostSandboxProfileIdentity struct {
	canonicalPath string
	dev           int32
	ino           uint64
	uid           uint32
	mode          os.FileMode
}

const maxModelHostSandboxProfileBytes = 1 << 20

// prepareModelHostIsolation creates the OS policy before the child is
// started. The repository is not a permitted path in this policy. Only the
// selected executable, system runtime files, and the disposable work
// directory are readable, and only the work directory is writable.
func prepareModelHostIsolation(cfg ModelHostConfig, workDir string) (modelHostIsolationSetup, error) {
	sandbox := "/usr/bin/sandbox-exec"
	var err error
	if _, statErr := os.Stat(sandbox); statErr != nil {
		return modelHostIsolationSetup{}, UnsupportedVersionError("model host filesystem sandbox backend is unavailable")
	}
	if sandbox, err = filepath.EvalSymlinks(sandbox); err != nil || sandbox != "/usr/bin/sandbox-exec" {
		return modelHostIsolationSetup{}, UnsupportedVersionError("model host filesystem sandbox backend is not trusted")
	}
	binary, err := exec.LookPath(cfg.BinPath)
	if err != nil {
		return modelHostIsolationSetup{}, UnsupportedVersionError("model host executable is unavailable")
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return modelHostIsolationSetup{}, UnsupportedVersionError("model host executable path is unavailable")
	}
	if binary, err = filepath.EvalSymlinks(binary); err != nil {
		return modelHostIsolationSetup{}, UnsupportedVersionError("model host executable cannot be canonicalized")
	}
	if modelHostRepositoryPath(binary) {
		return modelHostIsolationSetup{}, BadRequestError("model host executable resolves inside the repository")
	}
	repositoryRoot, err := os.Getwd()
	if err != nil || repositoryRoot == "" {
		return modelHostIsolationSetup{}, UnsupportedVersionError("repository boundary could not be established")
	}
	repositoryRoot, err = resolveModelHostRepositoryRoot(repositoryRoot)
	if err != nil {
		return modelHostIsolationSetup{}, UnsupportedVersionError("repository boundary could not be established")
	}
	if modelHostPathWithin(binary, repositoryRoot) {
		return modelHostIsolationSetup{}, BadRequestError("model host executable resolves inside the repository")
	}
	if err := validateModelHostArguments(cfg.Args, repositoryRoot); err != nil {
		return modelHostIsolationSetup{}, err
	}
	probeBinaries, err := trustedModelHostProbeBinaries()
	if err != nil {
		return modelHostIsolationSetup{}, UnsupportedVersionError("trusted model host isolation probe runtime is unavailable")
	}
	readPaths := []string{binary}
	for _, path := range cfg.AllowedReadPaths {
		if !filepath.IsAbs(path) {
			return modelHostIsolationSetup{}, BadRequestError("model host read path must be absolute")
		}
		clean := filepath.Clean(path)
		if modelHostCredentialPath(clean) || modelHostRepositoryPath(clean) || modelHostPathWithin(clean, repositoryRoot) {
			return modelHostIsolationSetup{}, BadRequestError("model host read path exposes a repository path")
		}
		resolved, resolveErr := filepath.EvalSymlinks(clean)
		if resolveErr != nil {
			return modelHostIsolationSetup{}, BadRequestError("model host read path cannot be canonicalized")
		}
		if modelHostCredentialPath(resolved) || modelHostRepositoryPath(resolved) || modelHostPathWithin(resolved, repositoryRoot) || modelHostPathWithin(repositoryRoot, resolved) {
			return modelHostIsolationSetup{}, BadRequestError("model host read path resolves inside a repository path")
		}
		if _, statErr := os.Stat(clean); statErr != nil {
			return modelHostIsolationSetup{}, BadRequestError("model host read path is unavailable")
		}
		readPaths = append(readPaths, resolved)
	}
	policyWorkDir, err := filepath.EvalSymlinks(workDir)
	if err != nil || policyWorkDir == "" {
		return modelHostIsolationSetup{}, UnsupportedVersionError("disposable working-directory boundary could not be established")
	}
	if modelHostPathWithin(policyWorkDir, repositoryRoot) || modelHostPathWithin(repositoryRoot, policyWorkDir) {
		return modelHostIsolationSetup{}, BadRequestError("disposable model host directory overlaps the repository")
	}
	profilePath := filepath.Join(policyWorkDir, "model-host.sb")
	probeProfilePath := filepath.Join(policyWorkDir, "model-host-probe.sb")
	deniedPaths := []string{profilePath, probeProfilePath}
	profile := modelHostSandboxProfileWithProbesAndDeniedPaths(binary, policyWorkDir, repositoryRoot, profilePath, readPaths, nil, deniedPaths)
	probeReadPaths := append([]string(nil), probeBinaries...)
	probeProfile := modelHostSandboxProfileWithProbesAndDeniedPaths(probeBinaries[0], policyWorkDir, repositoryRoot, probeProfilePath, probeReadPaths, probeBinaries, []string{probeProfilePath, profilePath})
	if err := validateModelHostSandboxProfileWithProbesAndDeniedPaths(profile, binary, policyWorkDir, repositoryRoot, profilePath, nil, deniedPaths, readPaths); err != nil {
		return modelHostIsolationSetup{}, UnsupportedVersionError("model host filesystem sandbox policy is incomplete")
	}
	if err := validateModelHostSandboxProfileWithProbesAndDeniedPaths(probeProfile, probeBinaries[0], policyWorkDir, repositoryRoot, probeProfilePath, probeBinaries, []string{probeProfilePath, profilePath}, probeReadPaths); err != nil {
		return modelHostIsolationSetup{}, UnsupportedVersionError("model host filesystem sandbox policy is incomplete")
	}
	runtimePolicySharedBaseDigest := modelHostSandboxSharedBaseDigest(profile)
	probeSharedBaseDigest := modelHostSandboxSharedBaseDigest(probeProfile)
	if runtimePolicySharedBaseDigest != probeSharedBaseDigest {
		return modelHostIsolationSetup{}, UnsupportedVersionError("model host isolation probe policy is not bound to the runtime policy")
	}
	policyDigest := modelHostSandboxPolicyDigest(profile)
	probePolicyDigest := modelHostSandboxPolicyDigest(probeProfile)
	if err := validateModelHostSandboxPolicyDigest(profile, policyDigest); err != nil {
		return modelHostIsolationSetup{}, UnsupportedVersionError("model host runtime sandbox policy digest is not bound to its exact profile")
	}
	if err := validateModelHostSandboxPolicyDigest(probeProfile, probePolicyDigest); err != nil {
		return modelHostIsolationSetup{}, UnsupportedVersionError("model host probe sandbox policy digest is not bound to its exact profile")
	}
	if err := os.WriteFile(profilePath, []byte(profile), 0o600); err != nil {
		return modelHostIsolationSetup{}, UnsupportedVersionError("model host filesystem sandbox policy could not be created")
	}
	if err := os.WriteFile(probeProfilePath, []byte(probeProfile), 0o600); err != nil {
		return modelHostIsolationSetup{}, UnsupportedVersionError("model host isolation probe policy could not be created")
	}
	runtimeProfileIdentity, err := captureModelHostSandboxProfileIdentity(profilePath, policyDigest)
	if err != nil {
		return modelHostIsolationSetup{}, UnsupportedVersionError("model host runtime sandbox policy identity could not be established")
	}
	trustedProbe, err := runTrustedModelHostIsolationProbe(sandbox, probeProfilePath, probeBinaries, repositoryRoot, policyWorkDir)
	if err != nil {
		return modelHostIsolationSetup{}, UnsupportedVersionError("trusted model host isolation probe failed")
	}
	args := make([]string, 0, len(cfg.Args)+3)
	args = append(args, "-f", profilePath, binary)
	args = append(args, cfg.Args...)
	if err := validateModelHostSandboxRuntimeBinding(sandbox, args, profilePath, binary); err != nil {
		return modelHostIsolationSetup{}, UnsupportedVersionError("model host runtime sandbox invocation is not bound to its exact profile")
	}
	runtimeWriteAudit, err := newModelHostRuntimeRepositoryWriteAudit(policyWorkDir, sentinelPathForModelHostRepository(repositoryRoot), trustedProbe.SentinelBeforeDigest, policyDigest)
	if err != nil {
		return modelHostIsolationSetup{}, UnsupportedVersionError("model host runtime repository-write audit could not be established")
	}
	return modelHostIsolationSetup{
		command:                       sandbox,
		args:                          args,
		backend:                       "sandbox-exec",
		enforcement:                   "enforced",
		networkPolicy:                 "deny_all",
		policyDigest:                  policyDigest,
		runtimePolicyBinding:          ModelHostRuntimePolicyBindingExact,
		runtimePolicySharedBaseDigest: runtimePolicySharedBaseDigest,
		probeScope:                    ModelHostProbeScopeSharedDenyBase,
		probePolicyDigest:             probePolicyDigest,
		probeSharedBaseDigest:         probeSharedBaseDigest,
		runtimePolicyProfilePath:      runtimeProfileIdentity.canonicalPath,
		runtimePolicyProfileIdentity:  runtimeProfileIdentity,
		trustedProbe:                  trustedProbe,
		runtimeRepositoryWriteAudit:   runtimeWriteAudit,
	}, nil
}

func sentinelPathForModelHostRepository(repositoryRoot string) string {
	sentinelPath := filepath.Join(repositoryRoot, ".git", "HEAD")
	if info, err := os.Stat(filepath.Join(repositoryRoot, ".git")); err == nil && !info.IsDir() {
		sentinelPath = filepath.Join(repositoryRoot, ".git")
	}
	return sentinelPath
}

func newModelHostRuntimeRepositoryWriteAudit(workDir, sentinelPath, beforeDigest, policyDigest string) (*modelHostRuntimeRepositoryWriteAudit, error) {
	if workDir == "" || sentinelPath == "" || beforeDigest == "" || policyDigest == "" {
		return nil, fmt.Errorf("runtime repository-write audit inputs are incomplete")
	}
	sentinelBefore, err := os.ReadFile(sentinelPath)
	if err != nil || len(sentinelBefore) == 0 {
		return nil, fmt.Errorf("read repository sentinel for runtime audit: %w", err)
	}
	digest := sha256.Sum256(sentinelBefore)
	actualBefore := "sha256:" + hex.EncodeToString(digest[:])
	if actualBefore != beforeDigest {
		return nil, fmt.Errorf("repository sentinel changed while preparing runtime audit")
	}
	targetPath := filepath.Join(workDir, modelHostRuntimeRepositoryWriteTargetName)
	if err := syscall.Mkfifo(targetPath, 0o600); err != nil {
		return nil, fmt.Errorf("create runtime repository-write audit target: %w", err)
	}
	// Keep one Core-owned write reference so a nonblocking read reports
	// EAGAIN while the child has not opened the endpoint, rather than EOF.
	targetReader, err := os.OpenFile(targetPath, os.O_RDWR|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open runtime repository-write audit target: %w", err)
	}
	challengeDigest := sha256.Sum256([]byte(policyDigest + "\x00" + workDir + "\x00" + sentinelPath))
	challenge := hex.EncodeToString(challengeDigest[:])
	challengePath := filepath.Join(workDir, modelHostRuntimeRepositoryWriteChallengeName)
	if err := os.WriteFile(challengePath, []byte(challenge), 0o600); err != nil {
		_ = targetReader.Close()
		return nil, fmt.Errorf("write runtime repository-write challenge: %w", err)
	}
	return &modelHostRuntimeRepositoryWriteAudit{
		targetPath: targetPath, targetReader: targetReader,
		challengePath: challengePath, sentinelPath: sentinelPath, beforeDigest: beforeDigest,
		challenge: challenge, policyDigest: policyDigest,
	}, nil
}

func validateModelHostSandboxProfile(profile, binary, workDir, repositoryRoot, profilePath string) error {
	return validateModelHostSandboxProfileWithProbesAndDeniedPaths(profile, binary, workDir, repositoryRoot, profilePath, nil, []string{profilePath})
}

func validateModelHostSandboxProfileWithProbe(profile, binary, workDir, repositoryRoot, profilePath, probeBinary string) error {
	if probeBinary == "" {
		return validateModelHostSandboxProfileWithProbesAndDeniedPaths(profile, binary, workDir, repositoryRoot, profilePath, nil, []string{profilePath})
	}
	return validateModelHostSandboxProfileWithProbesAndDeniedPaths(profile, binary, workDir, repositoryRoot, profilePath, []string{probeBinary}, []string{profilePath})
}

func validateModelHostSandboxProfileWithProbes(profile, binary, workDir, repositoryRoot, profilePath string, probeBinaries []string) error {
	return validateModelHostSandboxProfileWithProbesAndDeniedPaths(profile, binary, workDir, repositoryRoot, profilePath, probeBinaries, []string{profilePath})
}

func validateModelHostSandboxProfileWithProbesAndDeniedPaths(profile, binary, workDir, repositoryRoot, profilePath string, probeBinaries, deniedPaths []string, expectedReadPaths ...[]string) error {
	readPaths, err := modelHostSandboxExpectedReadPaths(binary, probeBinaries, expectedReadPaths)
	if err != nil {
		return err
	}

	expectedProfile := modelHostSandboxProfileWithProbesAndDeniedPaths(binary, workDir, repositoryRoot, profilePath, readPaths, probeBinaries, deniedPaths)
	expectedClauses, err := modelHostSandboxProfileClauses(expectedProfile)
	if err != nil {
		return fmt.Errorf("generated sandbox policy is not canonical: %w", err)
	}
	actualClauses, err := modelHostSandboxProfileClauses(profile)
	if err != nil {
		return err
	}
	for _, clause := range actualClauses {
		if clause == "(allow file-write*)" || clause == "(allow file-read*)" {
			return fmt.Errorf("sandbox policy contains unscoped filesystem access")
		}
	}

	// Keep these checks independent from the generator's byte output. The
	// shared-base digest intentionally omits these policy-specific clauses, so
	// an attacker must not be able to add an allow or remove a deny and then
	// recompute a digest that still passes validation.
	baseClauses := []string{
		"(version 1)",
		"(deny default)",
		"(deny network*)",
		"(deny process-fork)",
		"(allow signal (target self))",
		"(allow sysctl-read)",
		"(allow file-write* (subpath \"" + modelHostSandboxLiteral(workDir) + "\"))",
		"(allow file-read* (subpath \"" + modelHostSandboxLiteral(workDir) + "\"))",
		"(deny file-read* (subpath \"" + modelHostSandboxLiteral(repositoryRoot) + "\"))",
		"(deny file-read-metadata (subpath \"" + modelHostSandboxLiteral(repositoryRoot) + "\"))",
		"(allow file-write* (literal \"/dev/null\"))",
		"(allow file-read* (subpath \"/System\"))",
		"(allow file-read* (subpath \"/usr\"))",
	}
	literalReadClauses := []string{
		"(allow file-read* (literal \"/\"))",
		"(allow file-read* (literal \"/dev/null\"))",
		"(allow file-read* (literal \"/dev/urandom\"))",
		"(allow file-read* (literal \"/dev/random\"))",
	}
	for _, readPath := range readPaths {
		literalReadClauses = append(literalReadClauses, "(allow file-read* (literal \""+modelHostSandboxLiteral(readPath)+"\"))")
	}
	processExecClauses := []string{"(allow process-exec (literal \"" + modelHostSandboxLiteral(binary) + "\"))"}
	for _, probeBinary := range probeBinaries {
		if probeBinary == binary {
			continue
		}
		processExecClauses = append(processExecClauses, "(allow process-exec (literal \""+modelHostSandboxLiteral(probeBinary)+"\"))")
	}
	exactDenyClauses := make([]string, 0, len(deniedPaths)*2)
	for _, deniedPath := range deniedPaths {
		exactDenyClauses = append(exactDenyClauses,
			"(deny file-read* (literal \""+modelHostSandboxLiteral(deniedPath)+"\"))",
			"(deny file-write* (literal \""+modelHostSandboxLiteral(deniedPath)+"\"))")
	}
	if err := validateModelHostSandboxClauseGroup("literal-read", actualClauses, literalReadClauses); err != nil {
		return err
	}
	if err := validateModelHostSandboxClauseGroup("process-exec", actualClauses, processExecClauses); err != nil {
		return err
	}
	if err := validateModelHostSandboxClauseGroup("exact-deny", actualClauses, exactDenyClauses); err != nil {
		return err
	}
	if err := validateModelHostSandboxClauseGroup("base", actualClauses, baseClauses); err != nil {
		return err
	}

	if profile != expectedProfile {
		return fmt.Errorf("sandbox policy does not match canonical generated profile")
	}
	if len(actualClauses) != len(expectedClauses) {
		return fmt.Errorf("sandbox policy clause count does not match canonical profile")
	}
	for index := range expectedClauses {
		if actualClauses[index] != expectedClauses[index] {
			return fmt.Errorf("sandbox policy clause order is not canonical at index %d", index)
		}
	}
	return nil
}

func modelHostSandboxExpectedReadPaths(binary string, probeBinaries []string, expectedReadPaths [][]string) ([]string, error) {
	if len(expectedReadPaths) > 1 {
		return nil, fmt.Errorf("sandbox policy received more than one expected read-path set")
	}
	if len(expectedReadPaths) == 1 && expectedReadPaths[0] != nil {
		return append([]string(nil), expectedReadPaths[0]...), nil
	}
	readPaths := []string{binary}
	for _, probeBinary := range probeBinaries {
		if probeBinary != binary {
			readPaths = append(readPaths, probeBinary)
		}
	}
	return readPaths, nil
}

func modelHostSandboxProfileClauses(profile string) ([]string, error) {
	if profile == "" {
		return nil, fmt.Errorf("sandbox policy is empty")
	}
	rawClauses := strings.Split(profile, "\n")
	if len(rawClauses) > 0 && rawClauses[len(rawClauses)-1] == "" {
		rawClauses = rawClauses[:len(rawClauses)-1]
	}
	clauses := make([]string, 0, len(rawClauses))
	seen := make(map[string]struct{}, len(rawClauses))
	for _, rawClause := range rawClauses {
		clause := strings.TrimSpace(rawClause)
		if clause == "" {
			return nil, fmt.Errorf("sandbox policy contains an empty clause")
		}
		if _, exists := seen[clause]; exists {
			return nil, fmt.Errorf("sandbox policy contains duplicate clause: %s", clause)
		}
		seen[clause] = struct{}{}
		clauses = append(clauses, clause)
	}
	return clauses, nil
}

func validateModelHostSandboxClauseGroup(name string, actual, expected []string) error {
	actualCounts := make(map[string]int)
	for _, clause := range actual {
		actualCounts[clause]++
	}
	expectedCounts := make(map[string]int)
	for _, clause := range expected {
		canonical := strings.TrimSpace(clause)
		expectedCounts[canonical]++
	}
	for clause, count := range expectedCounts {
		if count != 1 {
			return fmt.Errorf("generated sandbox policy contains duplicate %s clause: %s", name, clause)
		}
		if actualCounts[clause] != count {
			if actualCounts[clause] == 0 {
				return fmt.Errorf("sandbox policy is missing expected %s clause: %s", name, clause)
			}
			return fmt.Errorf("sandbox policy has unexpected %s clause multiplicity: %s", name, clause)
		}
	}
	for clause := range actualCounts {
		if isModelHostSandboxClauseInGroup(name, clause) {
			if expectedCounts[clause] == 0 {
				return fmt.Errorf("sandbox policy has unexpected %s clause: %s", name, clause)
			}
		}
	}
	return nil
}

func isModelHostSandboxClauseInGroup(name, clause string) bool {
	switch name {
	case "literal-read":
		return strings.HasPrefix(clause, "(allow file-read* (literal ")
	case "process-exec":
		return strings.HasPrefix(clause, "(allow process-exec ")
	case "exact-deny":
		return strings.HasPrefix(clause, "(deny file-read* (literal ") || strings.HasPrefix(clause, "(deny file-write* (literal ")
	case "base":
		return !strings.HasPrefix(clause, "(allow file-read* (literal ") &&
			!strings.HasPrefix(clause, "(allow process-exec ") &&
			!strings.HasPrefix(clause, "(deny file-read* (literal ") &&
			!strings.HasPrefix(clause, "(deny file-write* (literal ")
	default:
		return false
	}
}

func modelHostSandboxProfile(binary, workDir, repositoryRoot, profilePath string, readPaths []string) string {
	return modelHostSandboxProfileWithProbe(binary, workDir, repositoryRoot, profilePath, readPaths, "")
}

func modelHostSandboxProfileWithProbe(binary, workDir, repositoryRoot, profilePath string, readPaths []string, probeBinary string) string {
	if probeBinary == "" {
		return modelHostSandboxProfileWithProbes(binary, workDir, repositoryRoot, profilePath, readPaths, nil)
	}
	return modelHostSandboxProfileWithProbes(binary, workDir, repositoryRoot, profilePath, readPaths, []string{probeBinary})
}

func modelHostSandboxProfileWithProbes(binary, workDir, repositoryRoot, profilePath string, readPaths, probeBinaries []string) string {
	return modelHostSandboxProfileWithProbesAndDeniedPaths(binary, workDir, repositoryRoot, profilePath, readPaths, probeBinaries, []string{profilePath})
}

func modelHostSandboxProfileWithProbesAndDeniedPaths(binary, workDir, repositoryRoot, profilePath string, readPaths, probeBinaries, deniedPaths []string) string {
	var profile strings.Builder
	profile.WriteString(fmt.Sprintf(`(version 1)
(deny default)
(deny network*)
(deny process-fork)
(allow signal (target self))
(allow sysctl-read)
(allow file-read* (literal "/"))
(allow file-read* (subpath "/System"))
(allow file-read* (subpath "/usr"))
(allow file-read* (literal "/dev/null"))
(allow file-read* (literal "/dev/urandom"))
(allow file-read* (literal "/dev/random"))
(allow file-write* (literal "/dev/null"))
(allow file-write* (subpath "%s"))
(allow file-read* (subpath "%s"))
	(deny file-read* (subpath "%s"))
	(deny file-read-metadata (subpath "%s"))
	`, modelHostSandboxLiteral(workDir), modelHostSandboxLiteral(workDir), modelHostSandboxLiteral(repositoryRoot), modelHostSandboxLiteral(repositoryRoot)))
	for _, deniedPath := range deniedPaths {
		profile.WriteString(fmt.Sprintf("(deny file-read* (literal \"%s\"))\n", modelHostSandboxLiteral(deniedPath)))
		profile.WriteString(fmt.Sprintf("(deny file-write* (literal \"%s\"))\n", modelHostSandboxLiteral(deniedPath)))
	}
	for _, path := range readPaths {
		profile.WriteString(fmt.Sprintf("(allow file-read* (literal \"%s\"))\n", modelHostSandboxLiteral(path)))
	}
	profile.WriteString(fmt.Sprintf("(allow process-exec (literal \"%s\"))\n", modelHostSandboxLiteral(binary)))
	for _, probeBinary := range probeBinaries {
		if probeBinary == binary {
			continue
		}
		profile.WriteString(fmt.Sprintf("(allow process-exec (literal \"%s\"))\n", modelHostSandboxLiteral(probeBinary)))
	}
	return profile.String()
}

// modelHostSandboxSharedBaseDigest hashes only the policy clauses that are
// intentionally shared by the runtime and trusted-probe profiles. Exact
// executable, literal-read, and exact denied-path clauses are profile-
// specific and must not be treated as probe evidence for the runtime.
func modelHostSandboxSharedBaseDigest(profile string) string {
	var base strings.Builder
	for _, line := range strings.SplitAfter(profile, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "(allow process-exec ") || strings.HasPrefix(trimmed, "(allow file-read* (literal ") || strings.HasPrefix(trimmed, "(deny file-read* (literal ") || strings.HasPrefix(trimmed, "(deny file-write* (literal ") {
			continue
		}
		base.WriteString(line)
	}
	digest := sha256.Sum256([]byte(base.String()))
	return "sha256:" + hex.EncodeToString(digest[:])
}

// modelHostSandboxBaseDigest is retained for package-local callers while its
// semantics are made explicit by modelHostSandboxSharedBaseDigest.
func modelHostSandboxBaseDigest(profile string) string {
	return modelHostSandboxSharedBaseDigest(profile)
}

// modelHostSandboxPolicyDigest identifies the exact bytes of one generated
// sandbox profile. It deliberately does not normalize or remove clauses.
func modelHostSandboxPolicyDigest(profile string) string {
	digest := sha256.Sum256([]byte(profile))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func validateModelHostSandboxPolicyDigest(profile, expected string) error {
	if !validModelHostPolicyDigest(expected) || modelHostSandboxPolicyDigest(profile) != expected {
		return fmt.Errorf("sandbox profile digest does not match exact profile bytes")
	}
	return nil
}

// validateModelHostSandboxRuntimeBinding is the Core-side binding between
// the exact runtime profile and the sandbox-exec invocation. The executable
// and profile path are checked as argv values, rather than inferred from a
// child declaration.
func validateModelHostSandboxRuntimeBinding(command string, args []string, profilePath, binary string) error {
	if command != "/usr/bin/sandbox-exec" || len(args) < 3 || args[0] != "-f" || args[1] != profilePath || args[2] != binary {
		return fmt.Errorf("sandbox runtime invocation is not bound to the exact profile and executable")
	}
	if !filepath.IsAbs(profilePath) || !filepath.IsAbs(binary) {
		return fmt.Errorf("sandbox runtime invocation paths must be absolute")
	}
	return nil
}

func captureModelHostSandboxProfileIdentity(profilePath, expectedDigest string) (modelHostSandboxProfileIdentity, error) {
	identity, profile, err := inspectModelHostSandboxProfileOnDisk(profilePath)
	if err != nil {
		return modelHostSandboxProfileIdentity{}, err
	}
	if modelHostSandboxPolicyDigest(string(profile)) != expectedDigest {
		return modelHostSandboxProfileIdentity{}, fmt.Errorf("sandbox profile digest does not match generated profile")
	}
	return identity, nil
}

func validateModelHostSandboxProfileOnDisk(expectedPath string, expectedIdentity modelHostSandboxProfileIdentity, expectedDigest string) error {
	identity, profile, err := inspectModelHostSandboxProfileOnDisk(expectedPath)
	if err != nil {
		return err
	}
	if identity.canonicalPath != expectedPath {
		return fmt.Errorf("sandbox profile canonical path changed before process start")
	}
	if identity.dev != expectedIdentity.dev || identity.ino != expectedIdentity.ino || identity.uid != expectedIdentity.uid || identity.mode != expectedIdentity.mode {
		return fmt.Errorf("sandbox profile file identity changed before process start")
	}
	if modelHostSandboxPolicyDigest(string(profile)) != expectedDigest {
		return fmt.Errorf("sandbox profile bytes changed before process start")
	}
	return nil
}

func inspectModelHostSandboxProfileOnDisk(profilePath string) (modelHostSandboxProfileIdentity, []byte, error) {
	if profilePath == "" || !filepath.IsAbs(profilePath) {
		return modelHostSandboxProfileIdentity{}, nil, fmt.Errorf("sandbox profile path is not absolute")
	}
	file, err := os.OpenFile(profilePath, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return modelHostSandboxProfileIdentity{}, nil, fmt.Errorf("open sandbox profile without following symlinks: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return modelHostSandboxProfileIdentity{}, nil, fmt.Errorf("stat sandbox profile: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return modelHostSandboxProfileIdentity{}, nil, fmt.Errorf("sandbox profile is not an owner-safe regular file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return modelHostSandboxProfileIdentity{}, nil, fmt.Errorf("sandbox profile identity is unavailable")
	}
	if uint32(os.Getuid()) != stat.Uid {
		return modelHostSandboxProfileIdentity{}, nil, fmt.Errorf("sandbox profile owner is not the Core user")
	}
	lstat, err := os.Lstat(profilePath)
	if err != nil {
		return modelHostSandboxProfileIdentity{}, nil, fmt.Errorf("lstat sandbox profile: %w", err)
	}
	if lstat.Mode()&os.ModeSymlink != 0 || !lstat.Mode().IsRegular() {
		return modelHostSandboxProfileIdentity{}, nil, fmt.Errorf("sandbox profile path is not a regular non-symlink")
	}
	lstatStat, ok := lstat.Sys().(*syscall.Stat_t)
	if !ok || lstatStat == nil || lstatStat.Dev != stat.Dev || lstatStat.Ino != stat.Ino || lstatStat.Uid != stat.Uid || lstat.Mode().Perm() != info.Mode().Perm() {
		return modelHostSandboxProfileIdentity{}, nil, fmt.Errorf("sandbox profile path identity changed while inspecting it")
	}
	canonicalPath, err := filepath.EvalSymlinks(profilePath)
	if err != nil || canonicalPath == "" {
		return modelHostSandboxProfileIdentity{}, nil, fmt.Errorf("sandbox profile canonical path is unavailable")
	}
	profile, err := io.ReadAll(io.LimitReader(file, maxModelHostSandboxProfileBytes+1))
	if err != nil {
		return modelHostSandboxProfileIdentity{}, nil, fmt.Errorf("read sandbox profile: %w", err)
	}
	if len(profile) > maxModelHostSandboxProfileBytes {
		return modelHostSandboxProfileIdentity{}, nil, fmt.Errorf("sandbox profile exceeds the bounded size")
	}
	return modelHostSandboxProfileIdentity{canonicalPath: canonicalPath, dev: stat.Dev, ino: stat.Ino, uid: stat.Uid, mode: info.Mode().Perm()}, profile, nil
}

func validateModelHostIsolationBeforeStart(isolation modelHostIsolationSetup) error {
	if isolation.runtimePolicyProfilePath == "" {
		return fmt.Errorf("runtime sandbox profile path is missing")
	}
	return validateModelHostSandboxProfileOnDisk(isolation.runtimePolicyProfilePath, isolation.runtimePolicyProfileIdentity, isolation.policyDigest)
}

func trustedModelHostProbeBinaries() ([]string, error) {
	// Keep probes to direct system utilities. A shell is intentionally not
	// needed here because invoking it would require a broader shell resolution
	// path and would weaken the probe profile's process-exec allowlist.
	paths := []string{"/usr/bin/head", "/usr/bin/touch", "/usr/bin/nc", "/bin/test"}
	resolved := make([]string, 0, len(paths))
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			return nil, err
		}
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil || canonical == "" || modelHostRepositoryPath(canonical) {
			return nil, fmt.Errorf("untrusted probe runtime")
		}
		resolved = append(resolved, canonical)
	}
	return resolved, nil
}

// The trusted probes are launched by Core under a probe profile with the same
// deny-base as the runtime profile. Repository and target paths are sent on
// stdin to fixed system utilities, never through the model host's argv/env or
// cwd. The target is a disposable file outside the repository and is never a
// source file.

func runTrustedModelHostIsolationProbe(sandbox, profilePath string, probeBinaries []string, repositoryRoot, workDir string) (ModelHostIsolationProbe, error) {
	if sandbox == "" || len(probeBinaries) < 4 {
		return ModelHostIsolationProbe{}, fmt.Errorf("trusted probe launcher is incomplete")
	}
	sentinelPath := filepath.Join(repositoryRoot, ".git", "HEAD")
	if info, err := os.Stat(filepath.Join(repositoryRoot, ".git")); err != nil {
		return ModelHostIsolationProbe{}, err
	} else if !info.IsDir() {
		sentinelPath = filepath.Join(repositoryRoot, ".git")
	}
	sentinelBefore, err := os.ReadFile(sentinelPath)
	if err != nil {
		return ModelHostIsolationProbe{}, fmt.Errorf("read repository sentinel: %w", err)
	}
	if len(sentinelBefore) == 0 {
		return ModelHostIsolationProbe{}, fmt.Errorf("repository sentinel is empty")
	}
	beforeDigest := sha256.Sum256(sentinelBefore)

	probeRoot, err := os.MkdirTemp("", "codeflow-model-host-denied-")
	if err != nil {
		return ModelHostIsolationProbe{}, err
	}
	probeRootResolved, resolveErr := filepath.EvalSymlinks(probeRoot)
	if resolveErr != nil {
		_ = os.RemoveAll(probeRoot)
		return ModelHostIsolationProbe{}, resolveErr
	}
	if modelHostPathWithin(probeRootResolved, repositoryRoot) || modelHostPathWithin(repositoryRoot, probeRootResolved) {
		_ = os.RemoveAll(probeRoot)
		return ModelHostIsolationProbe{}, fmt.Errorf("probe target overlaps repository")
	}
	defer os.RemoveAll(probeRoot)
	writePath := filepath.Join(probeRoot, "sentinel")
	writeMarker := []byte("codeflow-probe-target-v1")
	if err := os.WriteFile(writePath, writeMarker, 0o600); err != nil {
		return ModelHostIsolationProbe{}, err
	}
	readControlPath := filepath.Join(workDir, "probe-read-control")
	if err := os.WriteFile(readControlPath, []byte("codeflow-probe-read-control"), 0o600); err != nil {
		return ModelHostIsolationProbe{}, err
	}
	if outcome, controlErr := runTrustedModelHostCommand(sandbox, profilePath, probeBinaries[0], workDir, "read", readControlPath); controlErr != nil || outcome != "allowed" {
		return ModelHostIsolationProbe{}, fmt.Errorf("trusted probe read control failed: outcome=%s err=%v", outcome, controlErr)
	}
	writeControlPath := filepath.Join(workDir, "probe-write-control")
	if outcome, controlErr := runTrustedModelHostCommand(sandbox, profilePath, probeBinaries[1], workDir, "write", writeControlPath); controlErr != nil || outcome != "allowed" {
		return ModelHostIsolationProbe{}, fmt.Errorf("trusted probe write control failed: outcome=%s err=%v", outcome, controlErr)
	}
	if controlBytes, controlErr := os.ReadFile(writeControlPath); controlErr != nil || len(controlBytes) != 0 {
		return ModelHostIsolationProbe{}, fmt.Errorf("trusted probe write control was not observed")
	}
	readAttempt, err := runTrustedModelHostCommand(sandbox, profilePath, probeBinaries[0], workDir, "read", sentinelPath)

	disposableWriteAttempt, writeErr := runTrustedModelHostCommand(sandbox, profilePath, probeBinaries[1], workDir, "write", writePath)
	repositoryWriteAttempt, capabilityErr := runTrustedModelHostWriteCapabilityProbe(sandbox, profilePath, probeBinaries[3], workDir, sentinelPath)
	if err == nil {
		err = writeErr
	}
	if err == nil {
		err = capabilityErr
	}
	networkAttempt, networkErr := runTrustedModelHostNetworkProbe(sandbox, profilePath, probeBinaries[2], workDir)
	if err == nil {
		err = networkErr
	}
	if err != nil {
		return ModelHostIsolationProbe{}, err
	}
	sentinelAfter, err := os.ReadFile(sentinelPath)
	if err != nil {
		return ModelHostIsolationProbe{}, err
	}
	afterDigest := sha256.Sum256(sentinelAfter)
	targetAfter, err := os.ReadFile(writePath)
	if err != nil {
		return ModelHostIsolationProbe{}, err
	}
	if !bytes.Equal(targetAfter, writeMarker) {
		return ModelHostIsolationProbe{}, fmt.Errorf("trusted probe changed disposable denied target")
	}
	probe := ModelHostIsolationProbe{
		RepositoryReadAttempt:  readAttempt,
		RepositoryWriteAttempt: repositoryWriteAttempt,
		DisposableWriteAttempt: disposableWriteAttempt,
		SentinelBeforeDigest:   "sha256:" + hex.EncodeToString(beforeDigest[:]),
		SentinelAfterDigest:    "sha256:" + hex.EncodeToString(afterDigest[:]),
		SentinelUnchanged:      bytes.Equal(sentinelBefore, sentinelAfter),
		NetworkAttempt:         networkAttempt,
	}
	if err := ValidateModelHostIsolationProbe(probe); err != nil {
		return ModelHostIsolationProbe{}, err
	}
	if err := os.RemoveAll(probeRoot); err != nil {
		return ModelHostIsolationProbe{}, err
	}
	return probe, nil
}

// runTrustedModelHostWriteCapabilityProbe uses the non-mutating test(1) -w
// operation against the repository sentinel. A writable disposable control
// is required to pass first, so utility/profile failures cannot be mistaken
// for a denied repository capability. It never creates or modifies a source
// file.
func runTrustedModelHostWriteCapabilityProbe(sandbox, profilePath, executable, workDir, sentinelPath string) (string, error) {
	if executable == "" || sentinelPath == "" {
		return "", fmt.Errorf("invalid repository write-capability probe")
	}
	control, err := runTrustedModelHostAccessCommand(sandbox, profilePath, executable, workDir, workDir)
	if err != nil || control != "allowed" {
		return "", fmt.Errorf("trusted repository write-capability control failed: outcome=%s err=%v", control, err)
	}
	return runTrustedModelHostAccessCommand(sandbox, profilePath, executable, workDir, sentinelPath)
}

func runTrustedModelHostAccessCommand(sandbox, profilePath, executable, workDir, path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, sandbox, "-f", profilePath, executable, "-w", path)
	cmd.Dir = workDir
	cmd.Env = []string{"HOME=" + workDir, "TMPDIR=" + workDir, "PATH=/usr/bin:/bin"}
	if err := cmd.Run(); err == nil {
		return "allowed", nil
	} else if ctx.Err() != nil {
		return "", ctx.Err()
	} else if _, ok := err.(*exec.ExitError); ok {
		return "blocked", nil
	} else {
		return "", err
	}
}

func runTrustedModelHostCommand(sandbox, profilePath, executable, workDir, mode, path string) (string, error) {
	if executable == "" || (mode != "read" && mode != "write") || path == "" {
		return "", fmt.Errorf("invalid trusted probe command")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	commandArgs := []string{"-c", "1", path}
	if mode == "write" {
		commandArgs = []string{path}
	}
	cmd := exec.CommandContext(ctx, sandbox, append([]string{"-f", profilePath, executable}, commandArgs...)...)
	cmd.Dir = workDir
	cmd.Env = []string{"HOME=" + workDir, "TMPDIR=" + workDir, "PATH=/usr/bin:/bin"}
	if err := cmd.Run(); err == nil {
		return "allowed", nil
	} else if ctx.Err() != nil {
		return "", ctx.Err()
	} else if _, ok := err.(*exec.ExitError); ok {
		return "blocked", nil
	} else {
		return "", err
	}
}

func runTrustedModelHostNetworkProbe(sandbox, profilePath, executable, workDir string) (string, error) {
	inboundControl, err := runTrustedModelHostNetworkListener("", "", executable, workDir)
	if err != nil || !inboundControl {
		return "", fmt.Errorf("trusted network listener control failed")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer listener.Close()
	address := listener.Addr().String()
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", err
	}
	controlContext, controlCancel := context.WithTimeout(context.Background(), 2*time.Second)
	controlCommand := exec.CommandContext(controlContext, executable, "-z", "-w", "1", host, port)
	controlCommand.Dir = workDir
	controlCommand.Env = []string{"HOME=" + workDir, "TMPDIR=" + workDir, "PATH=/usr/bin:/bin"}
	if controlErr := controlCommand.Run(); controlErr != nil {
		controlCancel()
		return "", fmt.Errorf("trusted network probe control failed")
	}
	controlCancel()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, sandbox, "-f", profilePath, executable, "-z", "-w", "1", host, port)
	cmd.Dir = workDir
	cmd.Env = []string{"HOME=" + workDir, "TMPDIR=" + workDir, "PATH=/usr/bin:/bin"}
	outbound := "blocked"
	if runErr := cmd.Run(); runErr == nil {
		outbound = "allowed"
	} else if ctx.Err() != nil {
		return "", ctx.Err()
	} else if _, ok := runErr.(*exec.ExitError); !ok {
		return "", runErr
	}
	inbound, inboundErr := runTrustedModelHostNetworkListener(sandbox, profilePath, executable, workDir)
	if inboundErr != nil {
		return "", inboundErr
	}
	if outbound == "allowed" || inbound {
		return "allowed", nil
	}
	return "blocked", nil
}

// runTrustedModelHostNetworkListener performs an independent positive control
// or a sandboxed inbound-listen probe. It reports allowed only after a real
// local connection succeeds, so an nc startup/argument error is not treated as
// a blocked network operation.
func runTrustedModelHostNetworkListener(sandbox, profilePath, executable, workDir string) (bool, error) {
	reservation, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return false, err
	}
	host, port, err := net.SplitHostPort(reservation.Addr().String())
	_ = reservation.Close()
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	args := []string{"-l", host, port}
	var command *exec.Cmd
	if sandbox == "" {
		command = exec.CommandContext(ctx, executable, args...)
	} else {
		command = exec.CommandContext(ctx, sandbox, append([]string{"-f", profilePath, executable}, args...)...)
	}
	command.Dir = workDir
	command.Env = []string{"HOME=" + workDir, "TMPDIR=" + workDir, "PATH=/usr/bin:/bin"}
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return false, err
	}
	time.Sleep(30 * time.Millisecond)
	connection, dialErr := net.DialTimeout("tcp4", net.JoinHostPort(host, port), 100*time.Millisecond)
	connected := dialErr == nil
	if connection != nil {
		_ = connection.Close()
	}
	cancel()
	_ = command.Wait()
	return connected, nil
}

func resolveModelHostRepositoryRoot(start string) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	if current, err = filepath.EvalSymlinks(current); err != nil {
		return "", err
	}
	for {
		gitPath := filepath.Join(current, ".git")
		if _, statErr := os.Stat(gitPath); statErr == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("repository root not found")
		}
		current = parent
	}
}

func modelHostSandboxLiteral(value string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value)
}
