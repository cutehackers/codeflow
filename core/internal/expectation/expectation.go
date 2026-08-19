// Package expectation verifies repository-owned cognitive-debt guardrails
// against evidence-backed FlowIR. Expectations can require existing facts;
// they can never promote an unknown into observed behavior.
package expectation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"codeflow/core/internal/flowir"
)

const Filename = "codeflow.flow-expectations.json"

type File struct {
	Version string          `json:"version"`
	Flows   map[string]Flow `json:"flows"`
}

type Flow struct {
	RequiredResults     []string `json:"required_results,omitempty"`
	RequiredCausalKinds []string `json:"required_causal_kinds,omitempty"`
	AllowedDebtReasons  []string `json:"allowed_debt_reasons,omitempty"`
	MaxOpenDebt         *int     `json:"max_open_debt,omitempty"`
}

type Failure struct {
	Code, Message string
}

func (f Failure) Error() string { return f.Code + ": " + f.Message }

type Report struct {
	FlowID   string    `json:"flow_id"`
	Ready    bool      `json:"ready"`
	Failures []Failure `json:"failures"`
}

func Load(repo string) (File, error) {
	return LoadPath(filepath.Join(repo, Filename))
}

func LoadPath(path string) (File, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return File{}, Failure{"EXPECTATIONS_UNCONFIGURED", "add " + path + " to guard an important flow"}
	}
	if err != nil {
		return File{}, err
	}
	var file File
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return File{}, Failure{"EXPECTATIONS_INVALID", err.Error()}
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return File{}, Failure{"EXPECTATIONS_INVALID", "multiple JSON values are not allowed"}
	}
	if file.Version != "1" || len(file.Flows) == 0 {
		return File{}, Failure{"EXPECTATIONS_INVALID", "version 1 and at least one flow are required"}
	}
	return file, nil
}

func Verify(file File, document flowir.Document) Report {
	report := Report{FlowID: document.Current.ID}
	expected, ok := file.Flows[document.Current.ID]
	if !ok {
		report.Failures = append(report.Failures, Failure{"FLOW_EXPECTATION_MISSING", "no expectation exists for " + document.Current.ID})
		return report
	}
	results := map[string]bool{}
	for _, fact := range document.Facts {
		if fact.Kind == "visible_result" && fact.Status == flowir.Observed {
			results[fact.Object] = true
		}
	}
	for _, result := range expected.RequiredResults {
		if !results[result] {
			report.Failures = append(report.Failures, Failure{"REQUIRED_RESULT_MISSING", fmt.Sprintf("observed result %s is missing", result)})
		}
	}
	kinds := map[string]bool{}
	for _, edge := range document.CausalEdges {
		if edge.Status == flowir.Observed {
			kinds[edge.Kind] = true
		}
	}
	for _, kind := range expected.RequiredCausalKinds {
		if !kinds[kind] {
			report.Failures = append(report.Failures, Failure{"REQUIRED_CAUSAL_RELATION_MISSING", fmt.Sprintf("observed causal kind %s is missing", kind)})
		}
	}
	if expected.MaxOpenDebt != nil && len(document.Unknowns) > *expected.MaxOpenDebt {
		report.Failures = append(report.Failures, Failure{"OPEN_DEBT_BUDGET_EXCEEDED", fmt.Sprintf("open debt %d exceeds %d", len(document.Unknowns), *expected.MaxOpenDebt)})
	}
	allowed := map[string]bool{}
	for _, reason := range expected.AllowedDebtReasons {
		allowed[reason] = true
	}
	for _, debt := range document.Unknowns {
		if !allowed[debt.Reason] {
			report.Failures = append(report.Failures, Failure{"UNAPPROVED_CAUSAL_DEBT", "unapproved debt reason: " + debt.Reason})
		}
	}
	report.Ready = len(report.Failures) == 0
	return report
}
