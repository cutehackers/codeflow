package flowview

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"

	"codeflow/internal/analyzer/detect"
	"codeflow/internal/analyzer/protocol"
	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/harvest"
	"codeflow/internal/collector/secret"
	"codeflow/internal/collector/slicing"
)

// serveCandidatePreview shows the entry code before a natural-language request
// is bound to one candidate. The preview and the subsequent analysis each
// validate their own immutable snapshot.
func (s *Server) serveCandidatePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	entry := strings.TrimSpace(r.URL.Query().Get("entrySymbol"))
	if entry == "" {
		http.Error(w, "entrySymbol is required", http.StatusBadRequest)
		return
	}
	snapshot, _, releaseSnapshot, err := s.captureAnalysisSnapshot(r.Context())
	if err != nil {
		http.Error(w, "source snapshot is unavailable", http.StatusServiceUnavailable)
		return
	}
	defer releaseSnapshot()
	language := detect.RequestedEntryLanguage(entry, snapshot.Files)
	cwd, err := filepath.Abs(".")
	if err != nil {
		http.Error(w, "adapter location is unavailable", http.StatusServiceUnavailable)
		return
	}
	adapter, err := harvest.ResolveAdapterForRepo(s.repoRoot, cwd, language, "")
	if err != nil {
		http.Error(w, "candidate adapter is unavailable", http.StatusServiceUnavailable)
		return
	}
	pool := protocol.NewPool(adapter, 2)
	defer pool.Close()
	candidates, err := harvest.NewRunnerWithPool(pool).RunWithSnapshot(r.Context(), s.repoRoot, snapshot)
	if err != nil {
		http.Error(w, "candidate discovery failed", http.StatusServiceUnavailable)
		return
	}
	var selected *harvest.Candidate
	for i := range candidates {
		if candidates[i].EntrySymbolPath == entry && candidates[i].ManifestOverride != "excluded" {
			selected = &candidates[i]
			break
		}
	}
	if selected == nil {
		http.Error(w, "candidate is not in the current snapshot", http.StatusNotFound)
		return
	}
	payload, err := slicing.NewRunner(pool).SliceWithSnapshot(r.Context(), s.repoRoot, selected.CandidateID, entry, nil, snapshot)
	if err != nil || payload == nil || len(payload.Steps) == 0 {
		http.Error(w, "candidate entry code could not be verified", http.StatusUnprocessableEntity)
		return
	}
	anchor := payload.Steps[0].Anchor
	resolution := &fusion.FlowResolution{EntrySymbolPath: entry, Evidence: []fusion.FlowEvidence{{}}}
	if err := fusion.AttachFlowResolutionSourceAnchor(resolution, anchor, snapshot.Files); err != nil {
		http.Error(w, "candidate entry source differs from the snapshot", http.StatusUnprocessableEntity)
		return
	}
	content := snapshot.Files[anchor.RepoRelativePath]
	if anchor.ByteRange[0] == 0 && anchor.ByteRange[1] == len(content) && (len(content) > 50 || strings.Contains(content, "\n")) {
		http.Error(w, "candidate entry is not a precise code statement", http.StatusUnprocessableEntity)
		return
	}
	startLine := strings.Count(content[:anchor.ByteRange[0]], "\n") + 1
	endLine := strings.Count(content[:anchor.ByteRange[1]-1], "\n") + 1
	if endLine-startLine > 80 {
		http.Error(w, "candidate entry code is too broad to preview", http.StatusUnprocessableEntity)
		return
	}
	lines := strings.Split(secret.RedactSource(content).Text, "\n")
	viewStart := startLine - 4
	if viewStart < 1 {
		viewStart = 1
	}
	viewEnd := endLine + 8
	if viewEnd > len(lines) {
		viewEnd = len(lines)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"entrySymbolPath": entry,
		"candidateId":     selected.CandidateID,
		"snapshotId":      snapshot.SnapshotID,
		"source": map[string]any{
			"path":      anchor.RepoRelativePath,
			"startLine": viewStart,
			"endLine":   viewEnd,
			"code":      strings.Join(lines[viewStart-1:viewEnd], "\n"),
		},
	})
}
