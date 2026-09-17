// Package analyzer provides project detection, adapter management, and CodeGraph integration.
package analyzer

import (
	"codeflow/internal/analyzer/detect"
	"codeflow/internal/analyzer/protocol"
)

// ProjectDetector encapsulates detection of repository languages and architectures.
type ProjectDetector struct{}

// NewProjectDetector creates a new ProjectDetector.
func NewProjectDetector() *ProjectDetector {
	return &ProjectDetector{}
}

// Detect detects the language in a local directory.
func (d *ProjectDetector) Detect(absRoot string) detect.Detection {
	return detect.Detect(absRoot)
}

// DetectSnapshot detects language from an in-memory snapshot file map.
func (d *ProjectDetector) DetectSnapshot(files map[string]string) detect.Detection {
	return detect.DetectSnapshot(files)
}

// AdapterPoolManager manages the lifecycle of protocol adapters.
type AdapterPoolManager struct {
	pool *protocol.Pool
}

// NewAdapterPoolManager constructs a pool of protocol adapters.
func NewAdapterPoolManager(cfg protocol.Config, size int) *AdapterPoolManager {
	return &AdapterPoolManager{
		pool: protocol.NewPool(cfg, size),
	}
}

// Pool returns the underlying protocol pool.
func (m *AdapterPoolManager) Pool() *protocol.Pool {
	return m.pool
}

// Close gracefully releases adapter processes.
func (m *AdapterPoolManager) Close() {
	if m.pool != nil {
		m.pool.Close()
	}
}

// NewCodeGraphClient returns a client for the given repository.
func NewCodeGraphClient(repoRoot string) CodeGraphClient {
	return NewDefaultCodeGraphClient(repoRoot)
}
