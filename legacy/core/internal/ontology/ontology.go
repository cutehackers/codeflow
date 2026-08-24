// Package ontology keeps optional human meaning outside deterministic FlowIR.
package ontology

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Candidate struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Source string `json:"source"`
	Status string `json:"status"`
}
type Confirmed struct{ ID, Text, Source string }

var secret = regexp.MustCompile(`(?i)(secret|password|api[_ -]?key|authorization|bearer\s+|sk-[a-z0-9]|akia[0-9a-z]{16})`)
var allowed = map[string]bool{"decision": true, "intent": true, "rename": true, "user_message": true}

// Ingest reads an explicitly supplied JSONL export. It accepts only the
// documented semantic event classes, discards secret-bearing events before
// normalization, and never writes the raw export to disk.
func Ingest(reader io.Reader) ([]Candidate, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	seen := map[string]bool{}
	var out []Candidate
	for scanner.Scan() {
		var event struct {
			Class   string `json:"class"`
			Type    string `json:"type"`
			Text    string `json:"text"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, fmt.Errorf("invalid authorized JSONL event: %w", err)
		}
		class := event.Class
		if class == "" {
			class = event.Type
		}
		text := strings.TrimSpace(event.Text)
		if text == "" {
			text = strings.TrimSpace(event.Content)
		}
		if !allowed[class] || text == "" || secret.MatchString(text) {
			continue
		}
		if len(text) > 280 {
			text = text[:280]
		}
		id := hash(class, text)
		if !seen[id] {
			out = append(out, Candidate{ID: id, Text: text, Source: class, Status: "inferred"})
			seen[id] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func Approve(repo string, candidate Candidate) (Confirmed, error) {
	if candidate.ID == "" || candidate.Status != "inferred" || candidate.Text == "" || secret.MatchString(candidate.Text) {
		return Confirmed{}, fmt.Errorf("candidate is not eligible for approval")
	}
	dir := filepath.Join(repo, ".codeflow", "knowledge")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return Confirmed{}, err
	}
	path := filepath.Join(dir, "confirmed.json")
	var existing []Confirmed
	if bytes, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(bytes, &existing); err != nil {
			return Confirmed{}, fmt.Errorf("read confirmed knowledge: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return Confirmed{}, err
	}
	confirmed := Confirmed{ID: candidate.ID, Text: candidate.Text, Source: candidate.Source}
	for _, value := range existing {
		if value.ID == confirmed.ID {
			return value, nil
		}
	}
	existing = append(existing, confirmed)
	bytes, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return Confirmed{}, err
	}
	if err := os.WriteFile(path, append(bytes, '\n'), 0644); err != nil {
		return Confirmed{}, err
	}
	return confirmed, nil
}
func hash(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return "overlay:" + hex.EncodeToString(h.Sum(nil))
}
