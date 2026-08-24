package ontology

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const domainLabelsFile = "domain-labels.v1.json"

// DomainLabel is reviewed reader-facing copy attached to a deterministic
// scenario or one of its observed steps. It deliberately references stable
// FlowIR IDs rather than source locations: a changed interaction cannot keep
// an old business explanation by accident.
type DomainLabel struct {
	ID         string `json:"id"`
	FlowID     string `json:"flow_id"`
	ScenarioID string `json:"scenario_id"`
	StepID     string `json:"step_id,omitempty"`
	Title      string `json:"title"`
	Status     string `json:"status"`
}

type domainLabelFile struct {
	Version string        `json:"version"`
	Labels  []DomainLabel `json:"labels"`
}

func DomainLabelID(flowID, scenarioID, stepID string) string {
	h := sha256.New()
	for _, value := range []string{"domain_label_v1", flowID, scenarioID, stepID} {
		h.Write([]byte(value))
		h.Write([]byte{0})
	}
	return "domain-label:" + hex.EncodeToString(h.Sum(nil))
}

func LoadDomainLabels(repo string) ([]DomainLabel, error) {
	path := filepath.Join(repo, ".codeflow", "knowledge", domainLabelsFile)
	bytes, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []DomainLabel{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read domain labels: %w", err)
	}
	var stored domainLabelFile
	if err := json.Unmarshal(bytes, &stored); err != nil {
		return nil, fmt.Errorf("read domain labels: %w", err)
	}
	if stored.Version != "1" {
		return nil, fmt.Errorf("unsupported domain labels version %q", stored.Version)
	}
	for _, label := range stored.Labels {
		if err := validateDomainLabel(label); err != nil {
			return nil, err
		}
	}
	sort.Slice(stored.Labels, func(i, j int) bool { return stored.Labels[i].ID < stored.Labels[j].ID })
	return stored.Labels, nil
}

// SaveDomainLabel is the explicit approval boundary for domain copy. The
// caller must already have authenticated the local reviewer and validated the
// target against the current FlowIR document.
func SaveDomainLabel(repo string, label DomainLabel) (DomainLabel, error) {
	label.Title = strings.Join(strings.Fields(strings.TrimSpace(label.Title)), " ")
	label.Status = "confirmed"
	label.ID = DomainLabelID(label.FlowID, label.ScenarioID, label.StepID)
	if err := validateDomainLabel(label); err != nil {
		return DomainLabel{}, err
	}
	labels, err := LoadDomainLabels(repo)
	if err != nil {
		return DomainLabel{}, err
	}
	found := false
	for i := range labels {
		if labels[i].ID == label.ID {
			labels[i] = label
			found = true
		}
	}
	if !found {
		labels = append(labels, label)
	}
	sort.Slice(labels, func(i, j int) bool { return labels[i].ID < labels[j].ID })
	dir := filepath.Join(repo, ".codeflow", "knowledge")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return DomainLabel{}, err
	}
	bytes, err := json.MarshalIndent(domainLabelFile{Version: "1", Labels: labels}, "", "  ")
	if err != nil {
		return DomainLabel{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, domainLabelsFile), append(bytes, '\n'), 0644); err != nil {
		return DomainLabel{}, err
	}
	return label, nil
}

func validateDomainLabel(label DomainLabel) error {
	if label.FlowID == "" || label.ScenarioID == "" || label.Title == "" || label.Status != "confirmed" {
		return fmt.Errorf("domain label requires a flow, scenario, title, and confirmed status")
	}
	if label.ID != DomainLabelID(label.FlowID, label.ScenarioID, label.StepID) {
		return fmt.Errorf("domain label has an invalid identity")
	}
	if len([]rune(label.Title)) > 140 || secret.MatchString(label.Title) {
		return fmt.Errorf("domain label is too long or contains secret-like material")
	}
	return nil
}
