//go:build darwin && cgo

package protocol

/*
#include <errno.h>
#include <fcntl.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <libproc.h>
#include <mach/mach_time.h>
#include <sys/resource.h>
#include <sys/wait.h>
#include <sys/types.h>
#include <signal.h>
#include <time.h>
#include <unistd.h>

#ifndef RLIMIT_NPROC
#define RLIMIT_NPROC 7
#endif

static void codeflow_model_host_child_failed(int status_fd, unsigned char stage, int failure_errno) {
	unsigned char marker = stage;
	unsigned char detail = (failure_errno > 0 && failure_errno < 255) ? (unsigned char)failure_errno : 255;
	(void)write(status_fd, &marker, sizeof(marker));
	(void)write(status_fd, &detail, sizeof(detail));
	_exit(125);
}

static int codeflow_current_physical_memory_usage(uint64_t *usage_bytes) {
	if (usage_bytes == NULL) {
		return EINVAL;
	}
	struct proc_taskinfo info;
	int result = proc_pidinfo(getpid(), PROC_PIDTASKINFO, 0, &info, sizeof(info));
	if (result != (int)sizeof(info)) {
		return result < 0 ? errno : ESRCH;
	}
	*usage_bytes = info.pti_resident_size;
	return 0;
}

// proc_pidinfo is called only by the already-running Core process. It is not
// invoked from a child after fork, where the Go runtime may have other
// threads. The metric matches the direct child probe.
static int codeflow_process_physical_memory_usage(pid_t pid, uint64_t *usage_bytes) {
	if (pid <= 0 || usage_bytes == NULL) {
		return EINVAL;
	}
	struct proc_taskinfo info;
	int result = proc_pidinfo(pid, PROC_PIDTASKINFO, 0, &info, sizeof(info));
	if (result != (int)sizeof(info)) {
		return result < 0 ? errno : ESRCH;
	}
	*usage_bytes = info.pti_resident_size;
	return 0;
}

static int codeflow_process_cpu_time(pid_t pid, uint64_t *cpu_time_ns) {
	if (pid <= 0 || cpu_time_ns == NULL) {
		return EINVAL;
	}
	struct proc_taskinfo info;
	int result = proc_pidinfo(pid, PROC_PIDTASKINFO, 0, &info, sizeof(info));
	if (result != (int)sizeof(info)) {
		return result < 0 ? errno : ESRCH;
	}
	mach_timebase_info_data_t timebase = {0, 0};
	if (mach_timebase_info(&timebase) != KERN_SUCCESS || timebase.denom == 0) {
		return EINVAL;
	}
	uint64_t ticks = info.pti_total_user + info.pti_total_system;
	__uint128_t nanos = ((__uint128_t)ticks * timebase.numer) / timebase.denom;
	if (nanos > UINT64_MAX) {
		return ERANGE;
	}
	*cpu_time_ns = (uint64_t)nanos;
	return 0;
}

static void codeflow_close_if_not_standard(int fd) {
	if (fd > STDERR_FILENO) {
		(void)close(fd);
	}
}

static void codeflow_close_child_descriptors(int stdin_fd, int stdout_fd, int stderr_fd) {
	codeflow_close_if_not_standard(stdin_fd);
	if (stdout_fd != stdin_fd) {
		codeflow_close_if_not_standard(stdout_fd);
	}
	if (stderr_fd != stdin_fd && stderr_fd != stdout_fd) {
		codeflow_close_if_not_standard(stderr_fd);
	}
}

static int codeflow_clone_vector(char *const source[], char ***copy_out) {
	if (source == NULL || copy_out == NULL) {
		return EINVAL;
	}
	size_t count = 0;
	while (source[count] != NULL) {
		count++;
	}
	char **copy = calloc(count + 1, sizeof(*copy));
	if (copy == NULL) {
		return ENOMEM;
	}
	for (size_t i = 0; i < count; i++) {
		copy[i] = strdup(source[i]);
		if (copy[i] == NULL) {
			for (size_t j = 0; j < i; j++) {
				free(copy[j]);
			}
			free(copy);
			return ENOMEM;
		}
	}
	*copy_out = copy;
	return 0;
}

static void codeflow_free_vector(char **vector) {
	if (vector == NULL) {
		return;
	}
	for (size_t i = 0; vector[i] != NULL; i++) {
		free(vector[i]);
	}
	free(vector);
}

static int codeflow_reap_child(pid_t child) {
	int status = 0;
	pid_t result;
	do {
		result = waitpid(child, &status, 0);
	} while (result < 0 && errno == EINTR);
	if (result == child) {
		return 0;
	}
	return result < 0 ? errno : ECHILD;
}

static void codeflow_reset_sigxcpu_default(void) {
	struct sigaction action;
	memset(&action, 0, sizeof(action));
	action.sa_handler = SIG_DFL;
	sigemptyset(&action.sa_mask);
	(void)sigaction(SIGXCPU, &action, NULL);
}

static int codeflow_apply_child_limit(int status_fd, int resource, uint64_t value, unsigned char apply_stage) {
	struct rlimit requested = {(rlim_t)value, (rlim_t)value};
	if (setrlimit(resource, &requested) != 0) {
		codeflow_model_host_child_failed(status_fd, apply_stage, errno);
	}
	struct rlimit applied;
	if (getrlimit(resource, &applied) != 0) {
		codeflow_model_host_child_failed(status_fd, (unsigned char)(apply_stage + 1), errno);
	}
	if (applied.rlim_cur != requested.rlim_cur || applied.rlim_max != requested.rlim_max) {
		codeflow_model_host_child_failed(status_fd, (unsigned char)(apply_stage + 1), EINVAL);
	}
	return 0;
}

static int codeflow_install_child_limits(int status_fd, uint64_t cpu_seconds, uint64_t process_count) {
	int flags = fcntl(status_fd, F_GETFD);
	if (flags < 0 || fcntl(status_fd, F_SETFD, flags | FD_CLOEXEC) < 0) {
		codeflow_model_host_child_failed(status_fd, 1, errno);
	}
	(void)codeflow_apply_child_limit(status_fd, RLIMIT_CPU, cpu_seconds, 2);
	(void)codeflow_apply_child_limit(status_fd, RLIMIT_NPROC, process_count, 6);
	return 0;
}

// codeflow_spawn_model_host forks a target that applies CPU and the per-UID
// process-count defense limits and acknowledges them before it can execve.
// Memory is monitored by the already-running Core process after startup, and
// the sandbox's process-fork denial establishes the host-tree bound. The
// child path only uses async-signal-safe operations after fork.
static int codeflow_spawn_model_host(
	const char *path,
	char *const argv[],
	char *const envp[],
	const char *work_dir,
	int stdin_fd,
	int stdout_fd,
	int stderr_fd,
	int status_fd,
	uint64_t cpu_seconds,
	uint64_t memory_bytes,
	uint64_t process_count,
	int *pid_out) {
	if (path == NULL || argv == NULL || envp == NULL || work_dir == NULL ||
		stdin_fd < 0 || stdout_fd < 0 || stderr_fd < 0 || status_fd < 0 || pid_out == NULL) {
		return EINVAL;
	}
	(void)memory_bytes;
	char *owned_path = strdup(path);
	char *owned_work_dir = strdup(work_dir);
	if (owned_path == NULL || owned_work_dir == NULL) {
		free(owned_path);
		free(owned_work_dir);
		return ENOMEM;
	}
	char **owned_argv = NULL;
	char **owned_envp = NULL;
	int clone_result = codeflow_clone_vector(argv, &owned_argv);
	if (clone_result != 0) {
		free(owned_path);
		free(owned_work_dir);
		return clone_result;
	}
	clone_result = codeflow_clone_vector(envp, &owned_envp);
	if (clone_result != 0) {
		codeflow_free_vector(owned_argv);
		free(owned_path);
		free(owned_work_dir);
		return clone_result;
	}
	int ready_pipe[2];
	if (pipe(ready_pipe) != 0) {
		codeflow_free_vector(owned_argv);
		codeflow_free_vector(owned_envp);
		free(owned_path);
		free(owned_work_dir);
		return errno;
	}
	pid_t target = fork();
	if (target < 0) {
		int fork_errno = errno;
		(void)close(ready_pipe[0]);
		(void)close(ready_pipe[1]);
		codeflow_free_vector(owned_argv);
		codeflow_free_vector(owned_envp);
		free(owned_path);
		free(owned_work_dir);
		return fork_errno;
	}
	if (target == 0) {
		(void)close(ready_pipe[0]);
		if (setpgid(0, 0) != 0) {
			codeflow_model_host_child_failed(status_fd, 5, errno);
		}
		(void)codeflow_install_child_limits(status_fd, cpu_seconds, process_count);
		unsigned char ready_marker = 1;
		if (write(ready_pipe[1], &ready_marker, sizeof(ready_marker)) != (ssize_t)sizeof(ready_marker)) {
			codeflow_model_host_child_failed(status_fd, 12, errno);
		}
		(void)close(ready_pipe[1]);
		if (stdin_fd != STDIN_FILENO && dup2(stdin_fd, STDIN_FILENO) < 0) {
			codeflow_model_host_child_failed(status_fd, 13, errno);
		}
		if (stdout_fd != STDOUT_FILENO && dup2(stdout_fd, STDOUT_FILENO) < 0) {
			codeflow_model_host_child_failed(status_fd, 14, errno);
		}
		if (stderr_fd != STDERR_FILENO && dup2(stderr_fd, STDERR_FILENO) < 0) {
			codeflow_model_host_child_failed(status_fd, 15, errno);
		}
		codeflow_close_child_descriptors(stdin_fd, stdout_fd, stderr_fd);
		if (chdir(owned_work_dir) != 0) {
			codeflow_model_host_child_failed(status_fd, 16, errno);
		}
		execve(owned_path, owned_argv, owned_envp);
		codeflow_model_host_child_failed(status_fd, 17, errno);
	}
	(void)close(ready_pipe[1]);
	unsigned char ready_marker = 0;
	ssize_t ready_result;
	do {
		ready_result = read(ready_pipe[0], &ready_marker, sizeof(ready_marker));
	} while (ready_result < 0 && errno == EINTR);
	int ready_errno = errno;
	(void)close(ready_pipe[0]);
	if (ready_result != (ssize_t)sizeof(ready_marker) || ready_marker != 1) {
		(void)kill(target, SIGKILL);
		int reap_errno = codeflow_reap_child(target);
		codeflow_free_vector(owned_argv);
		codeflow_free_vector(owned_envp);
		free(owned_path);
		free(owned_work_dir);
		if (reap_errno != 0) {
			return reap_errno;
		}
		return ready_result < 0 ? ready_errno : ECHILD;
	}
	codeflow_free_vector(owned_argv);
	codeflow_free_vector(owned_envp);
	free(owned_path);
	free(owned_work_dir);
	*pid_out = (int)target;
	return 0;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"
)

func startModelHostProcess(command string, args, env []string, workDir string, limits ModelHostResourceLimits) (*exec.Cmd, io.WriteCloser, io.ReadCloser, io.ReadCloser, error) {
	if err := ValidateModelHostResourceLimits(limits); err != nil {
		return nil, nil, nil, nil, err
	}
	childStdin, parentStdin, err := os.Pipe()
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("model host stdin pipe: %w", err)
	}
	parentStdout, childStdout, err := os.Pipe()
	if err != nil {
		_ = childStdin.Close()
		_ = parentStdin.Close()
		return nil, nil, nil, nil, fmt.Errorf("model host stdout pipe: %w", err)
	}
	parentStderr, childStderr, err := os.Pipe()
	if err != nil {
		_ = childStdin.Close()
		_ = parentStdout.Close()
		_ = parentStdin.Close()
		_ = childStdout.Close()
		return nil, nil, nil, nil, fmt.Errorf("model host stderr pipe: %w", err)
	}
	statusRead, statusWrite, err := os.Pipe()
	if err != nil {
		_ = childStdin.Close()
		_ = parentStdout.Close()
		_ = parentStderr.Close()
		_ = parentStdin.Close()
		_ = childStdout.Close()
		_ = childStderr.Close()
		return nil, nil, nil, nil, fmt.Errorf("model host supervisor status pipe: %w", err)
	}

	cCommand := C.CString(command)
	defer C.free(unsafe.Pointer(cCommand))
	cWorkDir := C.CString(workDir)
	defer C.free(unsafe.Pointer(cWorkDir))
	cArgsMemory := C.calloc(C.size_t(len(args)+1), C.size_t(unsafe.Sizeof(uintptr(0))))
	if cArgsMemory == nil {
		_ = statusRead.Close()
		_ = childStdin.Close()
		_ = parentStdin.Close()
		_ = parentStdout.Close()
		_ = childStdout.Close()
		_ = parentStderr.Close()
		_ = childStderr.Close()
		return nil, nil, nil, nil, fmt.Errorf("model host argument vector allocation failed")
	}
	defer C.free(cArgsMemory)
	cArgs := (*[1 << 30]*C.char)(cArgsMemory)[: len(args)+1 : len(args)+1]
	cArgs[0] = cCommand
	for i, arg := range args {
		cArgs[i+1] = C.CString(arg)
		defer C.free(unsafe.Pointer(cArgs[i+1]))
	}
	cEnvMemory := C.calloc(C.size_t(len(env)+1), C.size_t(unsafe.Sizeof(uintptr(0))))
	if cEnvMemory == nil {
		_ = statusRead.Close()
		_ = childStdin.Close()
		_ = parentStdin.Close()
		_ = parentStdout.Close()
		_ = childStdout.Close()
		_ = parentStderr.Close()
		_ = childStderr.Close()
		return nil, nil, nil, nil, fmt.Errorf("model host environment vector allocation failed")
	}
	defer C.free(cEnvMemory)
	cEnv := (*[1 << 30]*C.char)(cEnvMemory)[: len(env)+1 : len(env)+1]
	for i, value := range env {
		cEnv[i] = C.CString(value)
		defer C.free(unsafe.Pointer(cEnv[i]))
	}
	var pid C.int
	spawnErr := C.codeflow_spawn_model_host(
		cCommand,
		(**C.char)(unsafe.Pointer(&cArgs[0])),
		(**C.char)(unsafe.Pointer(&cEnv[0])),
		cWorkDir,
		C.int(childStdin.Fd()), C.int(childStdout.Fd()), C.int(childStderr.Fd()), C.int(statusWrite.Fd()),
		C.uint64_t(limits.CPUTimeSeconds), C.uint64_t(limits.MemoryBytes), C.uint64_t(limits.ProcessCount), &pid,
	)
	_ = childStdin.Close()
	_ = childStdout.Close()
	_ = childStderr.Close()
	_ = statusWrite.Close()
	if spawnErr != 0 {
		_ = statusRead.Close()
		_ = parentStdin.Close()
		_ = parentStdout.Close()
		_ = parentStderr.Close()
		return nil, nil, nil, nil, fmt.Errorf("model host supervisor could not start: %w", errnoError(int(spawnErr)))
	}
	status, readErr := io.ReadAll(statusRead)
	_ = statusRead.Close()
	if readErr != nil {
		process, findErr := os.FindProcess(int(pid))
		if findErr == nil && process != nil {
			_ = process.Kill()
		}
		reapErr := reapModelHostProcessPID(int(pid), process)
		_ = parentStdin.Close()
		_ = parentStdout.Close()
		_ = parentStderr.Close()
		if reapErr != nil {
			return nil, nil, nil, nil, errors.Join(fmt.Errorf("model host supervisor status could not be observed: %w", readErr), fmt.Errorf("reap model host process: %w", reapErr))
		}
		return nil, nil, nil, nil, fmt.Errorf("model host supervisor status could not be observed: %w", readErr)
	}
	if len(status) != 0 {
		process, _ := os.FindProcess(int(pid))
		reapErr := reapModelHostProcessPID(int(pid), process)
		_ = parentStdin.Close()
		_ = parentStdout.Close()
		_ = parentStderr.Close()
		if reapErr != nil {
			return nil, nil, nil, nil, fmt.Errorf("model host resource-limit failure child could not be reaped: %w", reapErr)
		}
		detail := 0
		if len(status) > 1 {
			detail = int(status[1])
		}
		return nil, nil, nil, nil, UnsupportedVersionError(fmt.Sprintf("model host resource limits could not be applied (stage %d code %d)", status[0], detail))
	}
	process, err := os.FindProcess(int(pid))
	if err != nil || process == nil {
		reapErr := reapModelHostProcessPID(int(pid), process)
		_ = parentStdin.Close()
		_ = parentStdout.Close()
		_ = parentStderr.Close()
		if reapErr != nil {
			return nil, nil, nil, nil, fmt.Errorf("model host supervisor process could not be observed or reaped: %w", reapErr)
		}
		return nil, nil, nil, nil, UnsupportedVersionError("model host supervisor process could not be observed")
	}
	cmdArgs := append([]string{command}, args...)
	cmd := &exec.Cmd{Path: command, Args: cmdArgs, Env: append([]string(nil), env...), Dir: workDir, Process: process}
	return cmd, parentStdin, parentStdout, parentStderr, nil
}

func currentModelHostPhysicalMemoryUsage() (uint64, error) {
	var usage C.uint64_t
	if result := C.codeflow_current_physical_memory_usage(&usage); result != 0 {
		return 0, fmt.Errorf("model host physical memory usage observation failed (code %d)", int(result))
	}
	return uint64(usage), nil
}

func modelHostPhysicalMemoryUsage(pid int) (uint64, error) {
	var usage C.uint64_t
	if result := C.codeflow_process_physical_memory_usage(C.pid_t(pid), &usage); result != 0 {
		return 0, fmt.Errorf("model host physical memory usage observation failed (code %d)", int(result))
	}
	return uint64(usage), nil
}

func modelHostCPUTimeUsage(pid int) (time.Duration, error) {
	var usage C.uint64_t
	if result := C.codeflow_process_cpu_time(C.pid_t(pid), &usage); result != 0 {
		return 0, fmt.Errorf("model host CPU time observation failed (code %d)", int(result))
	}
	return time.Duration(uint64(usage)), nil
}

func modelHostResetCPUTimeLimitSignal() {
	C.codeflow_reset_sigxcpu_default()
}

func modelHostProcessExitedDueToCPUResourceLimit(state *os.ProcessState, limit uint64) bool {
	if state == nil || limit == 0 {
		return false
	}
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return false
	}
	if status.Signal() == syscall.SIGXCPU {
		return true
	}
	if status.Signal() != syscall.SIGKILL {
		return false
	}
	usage, ok := state.SysUsage().(*syscall.Rusage)
	if !ok || usage == nil {
		return false
	}
	return modelHostRusageCPUAtLeast(usage, limit)
}

func modelHostRusageCPUAtLeast(usage *syscall.Rusage, limit uint64) bool {
	if usage == nil || limit == 0 {
		return false
	}
	seconds := usage.Utime.Sec + usage.Stime.Sec
	microseconds := usage.Utime.Usec + usage.Stime.Usec
	if seconds < 0 || microseconds < 0 {
		return false
	}
	seconds += int64(microseconds) / 1_000_000
	return uint64(seconds) >= limit
}

func modelHostResourceLimitBackend() string {
	return ModelHostResourceBackendDarwinHostTree
}

func errnoError(value int) error {
	if value <= 0 {
		return fmt.Errorf("unknown errno")
	}
	return syscall.Errno(value)
}

func reapModelHostProcessPID(pid int, process *os.Process) error {
	if process != nil {
		_, err := process.Wait()
		return err
	}
	if pid <= 0 {
		return errors.New("model host process id is invalid")
	}
	var status syscall.WaitStatus
	waited, err := syscall.Wait4(pid, &status, 0, nil)
	if err != nil {
		return err
	}
	if waited != pid {
		return fmt.Errorf("waited for process %d, got %d", pid, waited)
	}
	return nil
}
