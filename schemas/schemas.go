// Package schemas provides embedded access to the CodeFlow JSON Schema contracts
// and golden fixtures.
package schemas

import (
	"embed"
)

// FS contains all CodeFlow contract JSON schemas (*.schema.json).
//
//go:embed *.schema.json
var FS embed.FS

// FixturesFS contains the golden test fixtures for contract validation.
//
//go:embed fixtures/*
var FixturesFS embed.FS
