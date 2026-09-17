package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"codeflow/internal/secret"
)

// TaskViewSummary identifies an immutable, self-contained FlowView result.
type TaskViewSummary struct {
	ViewID       string `json:"viewId"`
	SavedAt      string `json:"savedAt"`
	Title        string `json:"title"`
	GenerationID string `json:"generationId"`
}

func validViewID(id string) bool {
	if len(id) != 64 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil && id == strings.ToLower(id)
}

// SaveView stores the redacted result including its source excerpts. No snapshot
// cache or mutable active pointer is needed to restore this result.
func (s *Storage) SaveView(ctx context.Context, payload []byte) (string, []byte, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(payload, &doc); err != nil {
		return "", nil, fmt.Errorf("decode view: %w", err)
	}
	for _, key := range []string{"viewId", "flowView", "token"} {
		delete(doc, key)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return "", nil, err
	}
	clean, _, err := secret.RedactJSON(raw)
	if err != nil {
		return "", nil, fmt.Errorf("redact view: %w", err)
	}
	digest := sha256.Sum256(clean)
	id := hex.EncodeToString(digest[:])
	root, err := os.OpenRoot(s.repoRoot)
	if err != nil {
		return "", nil, err
	}
	defer root.Close()
	dir := filepath.Join(".codeflow", "semantics", "views")
	if err := root.MkdirAll(dir, 0o700); err != nil {
		return "", nil, err
	}
	// Exclusive temporary files avoid sharing partially written data across servers.
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", nil, err
	}
	temp := filepath.Join(dir, ".pending-"+hex.EncodeToString(nonce[:]))
	file, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", nil, err
	}
	defer root.Remove(temp)
	if _, err := file.Write(clean); err != nil {
		file.Close()
		return "", nil, err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return "", nil, err
	}
	if err := file.Close(); err != nil {
		return "", nil, err
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if err := root.Rename(temp, filepath.Join(dir, id+".json")); err != nil {
		return "", nil, err
	}
	return id, clean, nil
}

// ReadView rejects foreign paths and damaged content instead of reanalyzing.
func (s *Storage) ReadView(ctx context.Context, id string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validViewID(id) {
		return nil, errors.New("invalid view ID")
	}
	root, err := os.OpenRoot(s.repoRoot)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	data, err := root.ReadFile(filepath.Join(".codeflow", "semantics", "views", id+".json"))
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != id {
		return nil, errors.New("saved view integrity mismatch")
	}
	return data, nil
}

func (s *Storage) ListViews(ctx context.Context) ([]TaskViewSummary, error) {
	result := []TaskViewSummary{}
	root, err := os.OpenRoot(s.repoRoot)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	dir, err := root.Open(filepath.Join(".codeflow", "semantics", "views"))
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !validViewID(id) || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := s.ReadView(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("read task view: %w", err)
		}
		var doc struct {
			SemanticMap struct {
				GenerationID string `json:"generationId"`
				Summary      struct {
					Requested string `json:"requested"`
				} `json:"summary"`
			} `json:"semanticMap"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil, err
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		result = append(result, TaskViewSummary{SavedAt: info.ModTime().UTC().Format(time.RFC3339Nano), ViewID: id, Title: doc.SemanticMap.Summary.Requested, GenerationID: doc.SemanticMap.GenerationID})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].SavedAt > result[j].SavedAt })
	return result, nil
}
