package watch

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"time"
)

// CaptureResult holds the outcome of a stat-before/read/stat-after capture (Raw §7.1, VS03-A4).
type CaptureResult struct {
	Path      string
	Content   []byte
	ContentID string
	ModTime   time.Time
	Conflict  bool
	Deleted   bool
	Error     error
}

// CaptureFileWithStatCheck implements the Raw §7.1 / §10.4 capture contract:
// stat-before -> read -> stat-after + content hash.
// If the file changes during read, it retries up to maxRetries before reporting conflict.
func CaptureFileWithStatCheck(filePath string, maxRetries int) CaptureResult {
	if maxRetries <= 0 {
		maxRetries = 1
	}

	for attempt := 0; attempt < maxRetries; attempt++ {
		fiBefore, err := os.Stat(filePath)
		if err != nil {
			if os.IsNotExist(err) {
				return CaptureResult{Path: filePath, Deleted: true}
			}
			return CaptureResult{Path: filePath, Error: err}
		}

		data, err := os.ReadFile(filePath)
		if err != nil {
			if os.IsNotExist(err) {
				return CaptureResult{Path: filePath, Deleted: true}
			}
			return CaptureResult{Path: filePath, Error: err}
		}

		fiAfter, err := os.Stat(filePath)
		if err != nil {
			if os.IsNotExist(err) {
				return CaptureResult{Path: filePath, Deleted: true}
			}
			return CaptureResult{Path: filePath, Error: err}
		}

		// Stat check: modTime and size must match exactly
		if fiBefore.ModTime().Equal(fiAfter.ModTime()) && fiBefore.Size() == fiAfter.Size() {
			hash := sha256.Sum256(data)
			return CaptureResult{
				Path:      filePath,
				Content:   data,
				ContentID: hex.EncodeToString(hash[:]),
				ModTime:   fiAfter.ModTime(),
				Conflict:  false,
				Deleted:   false,
			}
		}

		// File modified during read -> retry with small backoff
		time.Sleep(10 * time.Millisecond)
	}

	return CaptureResult{
		Path:     filePath,
		Conflict: true,
		Error:    fmt.Errorf("capture conflict: file %s changed concurrently during read", filePath),
	}
}
