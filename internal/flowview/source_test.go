package flowview_test

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"codeflow/internal/flowview"
)

func TestSourceEndpointSymbolScopedViews(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codeflow-source-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// 300-line file: line 1 marker, lines 2..299 filler, line 300 marker.
	var b strings.Builder
	b.WriteString("// first line\n")
	for i := 2; i < 300; i++ {
		b.WriteString("// filler\n")
	}
	b.WriteString("// last line\n")
	if err := os.WriteFile(tmpDir+"/lib.dart", []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	token := "source-test-token"
	srv, err := flowview.NewServer(flowview.Config{RepoRoot: tmpDir, Port: 0, AuthToken: token})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	srv.Start()
	defer func() { _ = srv.Shutdown(context.Background()) }()

	client := &http.Client{Timeout: 5 * time.Second}
	baseURL := "http://" + srv.Addr()
	get := func(query string) string {
		resp, err := client.Get(baseURL + "/api/source?token=" + token + "&" + query)
		if err != nil {
			t.Fatalf("GET /api/source?%s failed: %v", query, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/source?%s status = %d, want 200", query, resp.StatusCode)
		}
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			sb.Write(buf[:n])
			if err != nil {
				break
			}
		}
		return sb.String()
	}
	countLines := func(s string) int { return strings.Count(s, "\n") }

	// Symbol-scoped view (e.g. codeLens.viewStartLine..viewEndLine) is served in full.
	// Joined lines have no trailing newline, so N lines contain N-1 "\n".
	if got := get("path=lib.dart&startLine=10&endLine=50"); countLines(got) != 40 {
		t.Errorf("41-line view served %d newlines, want 40", countLines(got))
	}

	// Ranges beyond the old 20-line cap are honored up to the default cap (160).
	if got := get("path=lib.dart&startLine=2&endLine=200"); countLines(got) != 159 {
		t.Errorf("200-line request capped at %d newlines, want 159", countLines(got))
	}

	// maxLines raises the cap up to the hard limit.
	if got := get("path=lib.dart&startLine=2&endLine=300&maxLines=300"); countLines(got) != 298 {
		t.Errorf("maxLines=300 served %d newlines, want 298", countLines(got))
	}

	// mode=file returns the whole file (raw bytes, trailing newline included).
	if got := get("path=lib.dart&mode=file"); countLines(got) != 300 {
		t.Errorf("mode=file served %d newlines, want 300", countLines(got))
	}

	// Focus markers survive slicing.
	if got := get("path=lib.dart&startLine=1&endLine=3"); !strings.Contains(got, "// first line") {
		t.Error("range view missing first line marker")
	}
	if got := get("path=lib.dart&mode=file"); !strings.Contains(got, "// last line") {
		t.Error("file view missing last line marker")
	}
}
