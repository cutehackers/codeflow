package flowview

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"codeflow/internal/analyzer/workspace"
)

// LiveCoordinatorRecord records the active logical coordinator's contact details.
type LiveCoordinatorRecord struct {
	URL       string    `json:"url"`
	Token     string    `json:"token"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"startedAt"`
	RepoRoot  string    `json:"repoRoot"`
}

func coordinatorFilePath(repoRoot string) string {
	return filepath.Join(repoRoot, workspace.DirName, "workspace", "coordinator.json")
}

// WriteLiveCoordinator records the active coordinator in the workspace directory.
func WriteLiveCoordinator(repoRoot string, record LiveCoordinatorRecord) error {
	path := coordinatorFilePath(repoRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create coordinator dir: %w", err)
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal coordinator record: %w", err)
	}
	tmpPath := fmt.Sprintf("%s.tmp.%d", path, time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("write coordinator temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("publish coordinator record: %w", err)
	}
	return nil
}

// ReadLiveCoordinator reads the coordinator record if present.
func ReadLiveCoordinator(repoRoot string) (*LiveCoordinatorRecord, error) {
	path := coordinatorFilePath(repoRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read coordinator record: %w", err)
	}
	var record LiveCoordinatorRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("unmarshal coordinator record: %w", err)
	}
	return &record, nil
}

// RemoveLiveCoordinator removes the coordinator record.
func RemoveLiveCoordinator(repoRoot string) error {
	path := coordinatorFilePath(repoRoot)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// RemoveLiveCoordinatorIfOwned removes the coordinator record only when it
// still points at the calling server. A shutting-down server must never
// delete a successor coordinator claimed by another process.
func RemoveLiveCoordinatorIfOwned(repoRoot, ownURL string, ownPID int) error {
	record, err := ReadLiveCoordinator(repoRoot)
	if err != nil || record == nil {
		return err
	}
	if record.URL != ownURL || (ownPID > 0 && record.PID != ownPID) {
		return nil
	}
	return RemoveLiveCoordinator(repoRoot)
}

// ClaimLiveCoordinator records the calling server only when no responsive
// coordinator exists. An embedded MCP coordinator must not evict an active
// CLI coordinator that browsers already discover.
func ClaimLiveCoordinator(repoRoot string, record LiveCoordinatorRecord) (claimed bool, existing *LiveCoordinatorRecord, err error) {
	if active, discoverErr := DiscoverLiveCoordinator(repoRoot); discoverErr == nil && active != nil {
		if active.URL == record.URL && active.PID == record.PID {
			return true, active, nil
		}
		return false, active, nil
	}
	if writeErr := WriteLiveCoordinator(repoRoot, record); writeErr != nil {
		return false, nil, writeErr
	}
	return true, nil, nil
}

// DiscoverLiveCoordinator checks if a coordinator is running and responsive.
// If the recorded process is dead or unresponsive, it cleans up the stale record.
func DiscoverLiveCoordinator(repoRoot string) (*LiveCoordinatorRecord, error) {
	record, err := ReadLiveCoordinator(repoRoot)
	if err != nil || record == nil {
		return nil, err
	}

	// Check if process is alive if on local machine
	if record.PID > 0 {
		process, findErr := os.FindProcess(record.PID)
		if findErr == nil && process != nil {
			if sigErr := process.Signal(syscall.Signal(0)); sigErr != nil {
				// Process is not running
				_ = RemoveLiveCoordinator(repoRoot)
				return nil, nil
			}
		}
	}

	// Ping the coordinator endpoint
	baseURL := record.URL
	if idx := strings.Index(baseURL, "?"); idx != -1 {
		baseURL = baseURL[:idx]
	}
	baseURL = strings.TrimRight(baseURL, "/")
	pingURL := baseURL + "/api/workspace/activity"
	if record.Token != "" {
		pingURL += "?token=" + url.QueryEscape(record.Token)
	}
	client := &http.Client{Timeout: 800 * time.Millisecond}
	resp, reqErr := client.Get(pingURL)
	if reqErr != nil {
		// Unresponsive coordinator
		_ = RemoveLiveCoordinator(repoRoot)
		return nil, nil
	}
	_ = resp.Body.Close()
	// 401/403 proves the endpoint is owned by an authenticated service, so
	// the record must not be treated as stale. Only unreachable endpoints
	// and unexpected statuses qualify for stale-record cleanup.
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return record, nil
	}
	if resp.StatusCode != http.StatusOK {
		_ = RemoveLiveCoordinator(repoRoot)
		return nil, nil
	}

	return record, nil
}

// AnalysisBasisRecord stores the durable snapshot identity of the comparison basis.
type AnalysisBasisRecord struct {
	BasisSnapshotID string `json:"basisSnapshotId"`
	WorkspaceEpoch  int64  `json:"workspaceEpoch"`
	RootTreeID      string `json:"rootTreeId,omitempty"`
}

func analysisBasisFilePath(repoRoot string) string {
	return filepath.Join(repoRoot, workspace.DirName, "workspace", "analysis-basis.json")
}

// ReadAnalysisBasis reads the recorded comparison basis.
func ReadAnalysisBasis(repoRoot string) (*AnalysisBasisRecord, error) {
	path := analysisBasisFilePath(repoRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var rec AnalysisBasisRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("corrupted analysis basis record: %w", err)
	}
	return &rec, nil
}

// WriteAnalysisBasis writes the recorded comparison basis.
func WriteAnalysisBasis(repoRoot string, rec AnalysisBasisRecord) error {
	path := analysisBasisFilePath(repoRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := fmt.Sprintf("%s.tmp.%d", path, time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
