# CodeFlow Go Coding Standards

This document defines the Go (Golang) coding standards, design patterns, and engineering conventions for the **CodeFlow** project. All Go contributions to CodeFlow must adhere to these standards to maintain codebase consistency, readability, security, and architectural integrity.

---

## 1. Core Principles & Architectural Guardrails

### 1.1 Business Flow First & Code Comprehension First
CodeFlow exists to help developers understand end-to-end execution paths across architecture layers. Code written for CodeFlow must prioritize clarity and explicitness over micro-optimizations or complex metaprogramming.

### 1.2 Anti-Telemetry Guard
> **Rule:** Never leak internal compiler, benchmark, or engine telemetry (such as workspace epochs, analysis lag, or internal settlement flags) into the primary view or default agent outputs.

- **Primary Presentation:** FlowView, CLI flow inspection (`codeflow flows`, `codeflow query`, `codeflow show`), and core MCP tool outputs must present only business flow traversals, architectural layers, and verifiable source code.
- **Diagnostic Isolation:** Internal telemetry (epochs, pending revisions, lag metrics) is strictly confined to internal diagnostics, the explicit `codeflow status` command, or internal debug payloads.

### 1.3 Pure Go & Portability
- Maintain pure Go builds (`CGO_ENABLED=0`) for the core engine across macOS, Linux, and Windows.
- Pure Go SQLite driver (`modernc.org/sqlite`) is preferred for all portable state and catalog persistence.
- Platform-specific capabilities (e.g., Darwin process sandboxing and seatbelt profiles in `internal/protocol/model_host_sandbox_darwin.go`) must be isolated with Go build tags (`//go:build darwin`) with portable fallback stubs (`model_host_sandbox_unsupported.go`).

### 1.4 Single-Gate Secret Redaction
- All persistence, publishing, and MCP egress paths must route through `internal/secret` sanitization gates (`secret.Redact` / `secret.RedactSource`).
- Diagnostics, exception dumps, and preview snippets must never expose credentials, private keys, authorization headers, or environment secrets.

### 1.5 Absolute Path Sanitization
- Never commit or print raw user home directories (such as `/Users/<username>` or `/home/<username>`) in code, logs, diagnostics, or configuration.
- Use relative paths, canonical repository-relative paths, or `HOME/...` placeholders when referring to paths in documentation or user-visible messages.

---

## 2. Project Layout & Package Design

### 2.1 Directory Structure
```
codeflow/
├── cmd/
│   ├── codeflow/          # Main CLI binary entry point
│   └── flowmeter/         # Diagnostic / benchmark utility
├── internal/              # Core business and engine packages (unexported)
│   ├── detect/            # Workspace & language detection
│   ├── doctor/            # Environment & health checks
│   ├── flowview/          # Interactive web UI and Live View server
│   ├── fusion/            # Layer integration and architecture validation
│   ├── harvest/           # Flow candidate extraction & call-graph analysis
│   ├── mcp/               # Model Context Protocol server (stdio JSON-RPC)
│   ├── naming/            # Deterministic natural language step naming
│   ├── protocol/          # Adapter wire protocols, pools, and model hosts
│   ├── runtime/           # Execution supervision and verification
│   ├── secret/            # Secret scanning and redaction gate
│   ├── semantic/          # Semantic enrichment and task view queries
│   ├── slicing/           # Dynamic & static program slicing
│   ├── storage/           # Disk layout, generations, and publication transactions
│   └── workspace/         # VFS, snapshots, and change tracking
├── adapters/              # External language analyzers (Dart, TypeScript)
├── schemas/               # Canonical JSON Schema contracts
└── test/                  # End-to-end integration tests & fixtures
```

### 2.2 Package Responsibilities & Naming
- **Single-Word, Lowercase Names:** Package names must be short, lowercase, and singular (e.g., `detect`, `storage`, `fusion`, `harvest`, `slicing`, `naming`, `secret`).
- **No Stuttering:** Avoid repeating package names in exported types or functions:
  ```go
  // Preferred:
  storage.New(root)
  storage.Pointer{}

  // Avoid:
  storage.NewStorage(root)
  storage.StoragePointer{}
  ```
- **Internal by Default:** All application packages belong in `internal/`. Exported public packages (`pkg/`) should only be created if CodeFlow intentionally offers a public Go client library.
- **No Catch-All Packages:** Do not create `util`, `common`, `helpers`, or `misc` packages. Place functionality into domain-focused packages (e.g., `naming`, `secret`, `detect`).
- **Package Documentation:** Every package must start with a doc comment explaining its architectural role and cross-referencing design specifications or tickets:
  ```go
  // Package harvest discovers entry points, extracts flow candidates,
  // and traces call graphs across architecture boundaries (design §5.1).
  package harvest
  ```

---

## 3. Code Organization & Formatting

### 3.1 Formatting & Indentation
- Always format Go source code with `gofmt` (or `goimports`).
- Go files use **tabs** for indentation (as defined in `.editorconfig`).
- Configuration files (`.json`, `.yaml`), markdown files, and web assets default to **2 spaces**.
- All text files must use **LF** line endings.

### 3.2 Import Grouping
Organize imports into three distinct blocks separated by an empty line:
1. Standard library packages
2. External third-party packages
3. Internal project packages (`codeflow/internal/...`)

```go
package flowview

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"

	"codeflow/internal/detect"
	"codeflow/internal/fusion"
	"codeflow/internal/storage"
)
```

### 3.3 Declarations & Grouping
Group related constants and variables using grouped blocks:
```go
const (
	pubspecFileName     = "pubspec.yaml"
	packageJSONFileName = "package.json"
	goModFileName       = "go.mod"
)

var (
	ErrIncompatibleEpoch = errors.New("incompatible workspace epoch")
	ErrStorageLocked     = errors.New("storage lock acquired by another process")
)
```

---

## 4. Naming Conventions

Naming conventions are essential for codebase comprehension and maintainability. In CodeFlow, all Go code must strictly follow standard Go idioms and **Google's Go Style Guide** (specifically on naming, scope proportionality, and initialisms).

### 4.1 Core Naming Principles
- **Readability at Call Site:** Names must make sense in context when invoked by a caller. Because code is read far more often than written, prioritize clarity and semantic precision over abbreviations at package and type boundaries.
- **Proportional Scope Rule:** The length of an identifier must be proportional to its scope:
  - **Small local scopes (1–5 lines, loops, closures):** short, concise names (`i`, `n`, `b`, `buf`, `req`, `ctx`, `err`).
  - **Function/type scopes:** descriptive noun phrases (`activePointer`, `snapshotID`, `pendingRevisions`).
  - **Package/global scopes:** fully qualified, explicit names (`DefaultIdleProbeTimeout`, `ErrIncompatibleEpoch`).
- **Semantic Intent Over Mechanics:** Names must explain *what* a symbol represents or *why* it exists, not its underlying implementation type.

### 4.2 Initialisms & Acronyms (Google Standard)
Acronyms and initialisms (e.g., `ID`, `URL`, `HTTP`, `JSON`, `AST`, `MCP`, `RPC`, `SHA`, `UUID`, `API`, `VFS`) must maintain uniform casing throughout the identifier:
- **Exported Identifiers:** The entire initialism must be uppercase:
  - Preferred: `FlowID`, `GenerationID`, `HTTPRequest`, `ASTNode`, `MCPClient`, `JSONSchema`, `UUID`, `SHA256Checksum`, `VFS`.
  - Avoid: `FlowId`, `GenerationId`, `HttpRequest`, `AstNode`, `McpClient`, `JsonSchema`, `Uuid`, `Sha256Checksum`, `Vfs`.
- **Unexported Identifiers (at start of name):** The initialism is entirely lowercase:
  - Preferred: `flowID`, `httpRequest`, `astNode`, `mcpClient`, `jsonSchema`, `uuid`, `sha256Checksum`.
  - Avoid: `flowId`, `httpRequest`, `aSTNode`, `mCPClient`.
- **Unexported Identifiers (inside name):** The initialism is uppercase:
  - Preferred: `activeMCP`, `parseJSON`, `buildAST`, `formatUUID`, `currentURL`.

| Canonical Initialism | Exported (Good) | Unexported (Good) | Strictly Forbidden |
|---|---|---|---|
| ID | `FlowID`, `CatalogID` | `flowID`, `catalogID` | `FlowId`, `flowId` |
| URL | `TargetURL`, `URLString` | `targetURL`, `urlString` | `TargetUrl`, `targetUrl` |
| HTTP | `HTTPServer`, `HTTPClient` | `httpServer`, `httpClient` | `HttpServer`, `httpClient` |
| JSON | `JSONPayload`, `ToJSON` | `jsonPayload`, `toJSON` | `JsonPayload`, `toJson` |
| AST | `ASTVisitor`, `ASTTree` | `astVisitor`, `astTree` | `AstVisitor`, `astTree` |
| MCP | `MCPServer`, `MCPRequest` | `mcpServer`, `mcpRequest` | `McpServer`, `mcpRequest` |
| RPC | `RPCEnvelope`, `JSONRPC` | `rpcEnvelope`, `jsonRPC` | `RpcEnvelope`, `JsonRpc` |
| UUID | `UUIDGenerator` | `uuidGenerator` | `UuidGenerator`, `uuidGenerator` |

### 4.3 Package Naming & Stutter Prevention
- **Single-Word & Lowercase:** Package names must be a single, short, lowercase word without underscores, hyphens, or mixedCaps (`storage`, `detect`, `flowview`, `harvest`, `naming`, `secret`).
- **No Generic Anti-Pattern Names:** Packages named `util`, `common`, `helpers`, `base`, `shared`, `core`, or `models` are strictly prohibited. Give packages specific functional boundaries.
- **Eliminate Call-Site Stutter:** A package name qualifies all symbols inside it. Do not repeat the package name in exported types or functions:
  ```go
  // Preferred (clean call sites):
  storage.New(root)
  storage.Pointer
  detect.Detect(root)
  naming.Normalize(identifier)

  // Strictly Avoid (stuttering):
  storage.NewStorage(root)
  storage.StoragePointer
  detect.DetectProject(root)
  naming.NormalizeNaming(identifier)
  ```
- **Avoid Local Variable Collisions:** Do not choose package names that conflict with common variable names (`ctx`, `err`, `status`, `flow`).
- **Avoid Cryptic Internal Acronyms in Package & File Names:** Do not use obscure, concatenated internal acronyms (such as `rflsc` for *Live Semantic Compiler*, `rflscvs01`, `rflsc_runner`) in package names, directory paths, or filenames. Use clear, self-describing domain terms instead (e.g., `compiler`, `semantic`, `evidence`, `verification`). Follow the official terminology rules in [`AGENTS.md`](../../AGENTS.md).
- **Strictly Forbid Ticket & Vertical-Slice Identifiers (`vs01*`, `vs02*`, etc.) in Filenames:** Never embed planning tickets, issue numbers, or vertical slice identifiers (e.g., `vs01`, `vs02`, `vs05`, `vs01_a01`, `lpca_vs04`) into filenames, packages, commands, or test files. File and test names must clearly reflect their **business domain, UI context, or tested behavior** with descriptive prefix/suffix patterns (e.g., `snapshot_lease_test.go` instead of `vs01_a04_test.go`, `cmd/schema-check` instead of `cmd/vs05-schema-check`, `catalog_contract_registry.go` instead of `vs07_registry.go`). A developer reading the repository should understand what a file does solely from its name, without cross-referencing legacy tickets.
- **Changed-Code Enforcement:** Run `make check-naming` for every change. The check examines changed code paths and added or modified declarations for design-plan labels and internal acronyms. It intentionally does not reject labels in external registry metadata, acceptance IDs, schema IDs, protocol values, compatibility mappings, or documentation.
- **Naming Test Behavior:** Test names must describe the behavior or business rule under test. Keep acceptance IDs in the registry-to-test mapping rather than in the Go, Dart, or TypeScript test identifier.

### 4.4 Variable & Constant Naming
- **No Type Encoding (Hungarian Notation):** Never embed the Go type name into the variable:
  ```go
  // Preferred:
  users := []User{}
  flowCount := 42
  config := map[string]string{}

  // Avoid:
  userSlice := []User{}
  flowCountInt := 42
  configMap := map[string]string{}
  pStorage := &Storage{}
  ```
- **Standard Short Names for Tight Scopes:**
  - `i`, `j`, `k` for loop indices
  - `b`, `buf` for byte slices or buffers
  - `s`, `str` for strings; `r` for runes or readers
  - `w` for writers or HTTP response writers
  - `n` for byte counts or sizes
  - `ctx` for `context.Context`
  - `err` for `error`
  - `ok` for boolean map/channel lookup indicators
- **Constants Never Use Screaming Snake Case:**
  - In Go, constants follow camelCase (unexported) or PascalCase (exported).
  ```go
  // Preferred:
  const (
  	DefaultIdleTimeout = 30 * time.Second // exported
  	maxBufferBytes     = 4 * 1024 * 1024   // unexported
  	pubspecFileName    = "pubspec.yaml"    // unexported
  )

  // Strictly Avoid:
  const DEFAULT_IDLE_TIMEOUT = 30 * time.Second
  const MAX_BUFFER_BYTES = 4 * 1024 * 1024
  ```

### 4.5 Function & Method Naming
- **No "Get" Prefix on Standard Accessors (Getters):**
  In Go, getters are named directly after the property/noun without a `Get` prefix:
  ```go
  // Preferred:
  func (s *Storage) BaseDir() string
  func (e *SnapshotEngine) CurrentActivity() Activity
  func (p *Pool) Capacity() int

  // Strictly Avoid:
  func (s *Storage) GetBaseDir() string
  func (e *SnapshotEngine) GetCurrentActivity() Activity
  func (p *Pool) GetCapacity() int
  ```
  > **Note:** The `Get` prefix is only acceptable when performing an external network fetch (`client.Get(url)`), looking up a value in a key-value store / cache (`cache.Get(key)`), or when returning a value via a mutated pointer parameter.
- **Constructor Naming:**
  - Single primary type in package: use `New(...)` returning `*Type`:
    ```go
    // package storage
    func New(repoRoot string) *Storage
    ```
  - Multiple exported types in package: use `New<Type>(...)`:
    ```go
    // package workspace
    func NewSnapshotEngine(repoRoot string, epoch int64) (*SnapshotEngine, error)
    func NewVFS(root string) VFS
    ```
- **Action Verbs for Operations:**
  Methods performing side effects or distinct pipeline actions should start with active verbs:
  - `Publish()`, `Slice()`, `Harvest()`, `Redact()`, `Quarantine()`, `Supervise()`.
- **Conversion Methods:**
  - Use `To<Type>()` for transformations that create a new representation: `ToSlash()`, `ToJSON()`.
  - Use `As<Type>()` for type assertions or wrapper conversions: `AsMap()`, `AsString()`.

### 4.6 Receiver Naming
- **Short, 1–2 Character Abbreviations:** Reflect the type name directly (`s *Storage`, `e *SnapshotEngine`, `m *Server`, `r *AdapterRegistry`).
- **Absolute Consistency:** Every method belonging to the same type must use the exact same receiver name. Never mix `s` in one method and `st` in another.
- **Never Use Generic Names:** Do not use `this`, `self`, `me`, `ptr`, `obj`, or `v` as receiver names.

### 4.7 Interface Naming
- **Single-Method Interfaces:** Name after the method with an `-er` or `-able` suffix:
  - `io.Reader`, `io.Writer`, `io.Closer`, `fmt.Stringer`, `ThresholdDecisionResolver`.
- **Multi-Method Interfaces:** Name after the role or capability being abstracted:
  - `VFS`, `SnapshotLease`, `SourceAuditProvider`, `Persistence`.
- **No "I" Prefix:** Never prefix interface names with `I` (e.g., avoid `IStorage`, `IVFS`, `IReader`).

### 4.8 Error & Sentinel Naming
- **Sentinel Errors:** Package-level error variables must start with the prefix `Err`:
  ```go
  var (
  	ErrIncompatibleEpoch = errors.New("incompatible workspace epoch")
  	ErrSnapshotMismatch  = errors.New("runtime snapshot identity mismatch")
  	ErrConsentReplay     = errors.New("runtime consent replay")
  	ErrStorageLocked     = errors.New("storage lock acquired by another process")
  )
  ```
- **Custom Error Types:** Custom structs implementing the `error` interface must end with the suffix `Error`:
  ```go
  type BadRequestError struct {
  	Message string
  }

  type ResourceLimitError struct {
  	Limit  int64
  	Actual int64
  }
  ```

### 4.9 Boolean Variables & Functions
- **Predicate Phrasing:** Boolean names must read like assertions or questions:
  - `isConfident`, `hasStaleSteps`, `hasUnknownSteps`, `canProceed`, `shouldRetry`, `ok`.
- **Avoid Negative Names:** Never name booleans negatively, as negations create double-negatives:
  ```go
  // Preferred:
  if !isEnabled { ... }
  if !isFound { ... }

  // Avoid:
  if !isDisabled { ... }
  if !isNotFound { ... }
  ```

### 4.10 Test Naming Conventions
- **Test Functions:** `Test<Target>` or `Test<Target>_<Scenario>`:
  - `TestStorageAtomicPublishAndRecovery`
  - `TestDetect_DartProject`
  - `TestRedactSource_PreservesLineBreaks`
- **Assertion Log Standard:** Consistently format test failures using `got` and `want` (or `got` and `expected`):
  ```go
  if got != tt.want {
  	t.Errorf("Detect(%q) = %q, want %q", tt.marker, got, tt.want)
  }
  ```
- **Test Helpers:** Always invoke `t.Helper()` as the first line of any test assertion or fixture helper function.

---

## 5. Struct & Type Design

### 5.1 Zero-Value Safety & Constructors
- Design structs so their zero value is meaningful and safe whenever practical.
- If explicit initialization is necessary (allocating maps, setting defaults, compiling regexes), provide a `New...` constructor returning a pointer:
```go
func NewStorage(repoRoot string) *Storage {
	return &Storage{
		repoRoot: repoRoot,
		baseDir:  filepath.Join(repoRoot, ".codeflow"),
	}
}
```

### 5.2 JSON & Serialization Tags
- Use `camelCase` for JSON keys in API, storage, and MCP payloads.
- Use `omitempty` when zero/nil values should be omitted.
- Struct tags must align with canonical schemas under `schemas/`:
```go
type FlowSummary struct {
	FlowID          string `json:"flowId"`
	Title           string `json:"title"`
	Description     string `json:"description,omitempty"`
	EntrySymbolPath string `json:"entrySymbolPath"`
	StepCount       int    `json:"stepCount"`
	HasStaleSteps   bool   `json:"hasStaleSteps"`
	HasUnknownSteps bool   `json:"hasUnknownSteps"`
}
```

### 5.3 Defensive Copying & Immutability
- Never expose mutable internal slice or map fields directly from structs if concurrent access or outside mutation could corrupt internal state.
- Create defensive copies when accepting or returning slices/maps in shared storage or cached models:
```go
func (c *Catalog) Flows() []Flow {
	out := make([]Flow, len(c.flows))
	copy(out, c.flows)
	return out
}
```

---

## 6. Error Handling

### 6.1 Explicit Error Handling
- Never ignore errors with blank identifier `_` unless discarding write operations to an in-memory buffer (`bytes.Buffer`) or explicitly non-failing standard calls.
- Check errors immediately after the call that produced them.

### 6.2 Error Wrapping with `%w`
- Always add contextual information when bubbling errors up the call stack:
```go
// Preferred:
if err := os.MkdirAll(dir, 0o755); err != nil {
	return fmt.Errorf("create storage layout %s: %w", dir, err)
}
```
- Use `%w` to preserve the underlying error for `errors.Is()` and `errors.As()`.

### 6.3 Sentinel Errors & Error Types
- Define package-level sentinel errors using `errors.New(...)` prefixed with `Err`:
```go
var (
	ErrSnapshotMismatch  = errors.New("runtime snapshot identity mismatch")
	ErrConsentReplay     = errors.New("runtime consent replay")
	ErrIncompatibleEpoch = errors.New("incompatible workspace epoch")
)
```
- Compare sentinel errors using `errors.Is(err, ErrIncompatibleEpoch)` instead of string comparison.
- When errors require structured diagnostic attributes (e.g. JSON-RPC protocol error codes), define a custom error struct implementing `error` and `Unwrap() error`.

### 6.4 Panic Policy
- **No panics in production runtime:** Calling `panic()` in HTTP handlers, MCP server routines, CLI commands, or background workers is strictly prohibited.
- `panic()` is permissible only during package `init()` if an immutable static invariant fails (e.g., a hardcoded regular expression fails `regexp.MustCompile`).

---

## 7. Concurrency, Goroutines & Synchronization

### 7.1 Context Propagation
- Pass `context.Context` as the first parameter to functions performing I/O, network requests, subprocess execution, database operations, or long-running work.
- Never store `context.Context` inside a struct; pass it through method calls.
```go
func (s *Storage) PublishGeneration(ctx context.Context, gen Generation) error { ... }
```

### 7.2 Goroutine Lifecycles & Leak Prevention
- Every goroutine launched via `go func()` must have a deterministic lifecycle:
  1. Monitored by a `sync.WaitGroup` or managed by an errgroup/worker pool.
  2. Responsive to context cancellation (`ctx.Done()`) or an explicit stop channel.
- Never fire-and-forget goroutines that perform unbounded work or hold network connections without cancellation.

### 7.3 Timeouts & Deadlines
- External operations (adapter handshakes, subprocesses, model host inferences, HTTP requests) must enforce timeouts using `context.WithTimeout`:
```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

res, err := client.Analyze(ctx, req)
```

### 7.4 Mutex Hygiene
- Place the mutex directly above the struct fields it protects.
- Name mutexes clearly if multiple mutexes exist in the same struct (e.g. `mu sync.RWMutex`, `projectMu sync.RWMutex`).
- Immediately `defer mu.Unlock()` or `defer mu.RUnlock()` after acquiring locks in standard functions:
```go
type AdapterRegistry struct {
	mu    sync.RWMutex
	pools map[string]*protocol.Pool
}

func (r *AdapterRegistry) Get(key string) (*protocol.Pool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.pools[key]
	return p, ok
}
```

---

## 8. Subprocesses & OS Safety

### 8.1 Command Execution
- Always execute subprocesses using `exec.CommandContext(ctx, ...)`.
- Subprocesses must be assigned process groups to allow clean termination of child processes on timeout or cancellation (see `internal/protocol/process_group_*.go`).
- Supervise process stdout and stderr using bounded buffers or streaming scanners to prevent buffer deadlock.

### 8.2 Path Traversal & File Operations
- Always use `path/filepath` for filesystem paths; never manually concatenate OS paths with `"/"` or `"+"` strings.
- Validate paths against traversal attacks using `filepath.Clean` and verify that the target path remains within `repoRoot`:
```go
cleanPath := filepath.Clean(userPath)
if !strings.HasPrefix(cleanPath, repoRoot) {
	return fmt.Errorf("access denied: path outside workspace root")
}
```

---

## 9. Testing Standards

### 9.1 Package Testing Strategy
- **Black-Box Testing (`<pkg>_test`):** Default to `package <pkg>_test` to test the public package API as an external consumer.
- **White-Box Testing (`<pkg>`):** Use `package <pkg>` only when verifying unexported algorithmic internals or testing seams.

### 9.2 In-Memory CLI Testability
- Structure CLI commands by separating the `os.Exit` wrapper from the testable execution function:
```go
// cmd/codeflow/query.go
func runQuery(args []string) {
	code := executeQuery(args, os.Stdout, os.Stderr)
	if code != 0 {
		os.Exit(code)
	}
}

func executeQuery(args []string, stdout, stderr io.Writer) int {
	// Parses flags and executes logic against stdout/stderr writers
}
```
- This enables unit tests to verify CLI commands in memory with `bytes.Buffer` without spawning external processes or triggering `os.Exit`.

### 9.3 Standard Library Testing & Tables
- Rely primarily on the Go standard `testing` package.
- Write table-driven tests for functions with multiple scenarios, inputs, or edge cases:
```go
func TestDetection(t *testing.T) {
	tests := []struct {
		name         string
		markerFile   string
		wantLanguage string
	}{
		{
			name:         "Dart project",
			markerFile:   "pubspec.yaml",
			wantLanguage: "dart",
		},
		{
			name:         "TypeScript project",
			markerFile:   "package.json",
			wantLanguage: "typescript",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			// set up marker file...
			got := detect.Detect(dir)
			if got.Language != tt.wantLanguage {
				t.Errorf("Detect() language = %q, want %q", got.Language, tt.wantLanguage)
			}
		})
	}
}
```

### 9.4 Test Isolation & Utilities
- Use `t.TempDir()` for all filesystem tests (automatically cleaned up when tests finish).
- Use `t.Setenv(key, val)` for environment variables in tests.
- Use `t.Helper()` at the start of all test helper functions.
- Use `t.Parallel()` for tests that are thread-safe and do not mutate shared global state.
- Assert non-fatal checks with `t.Errorf(...)` to surface all failures in a single run; reserve `t.Fatalf(...)` for failed preconditions where subsequent execution is invalid.

---

## 10. Tooling & Verification

### 10.1 Standard Makefile Targets
Developers and automated agents must verify Go changes before proposing commits:
```bash
# Format code according to gofmt
make fmt

# Run Go static analysis
make vet

# Execute unit and integration tests
make test
```

### 10.2 Recommended Linters (`golangci-lint`)
When using `golangci-lint`, the following linters are strongly recommended for CodeFlow:
- `govet`: Reports suspicious constructs (printf errors, shadow variables).
- `errcheck`: Enforces that error return values are handled.
- `staticcheck`: Advanced static analysis and bug prevention.
- `unused`: Detects unused constants, variables, functions, and types.
- `gosec`: Inspects code for security vulnerabilities (e.g. file path traversal, command injection).
- `revive`: Fast, configurable drop-in replacement for `golint`.
- `misspell`: Catches typos in code and doc comments.

---

## 11. Checklist for Go Code Review

Before submitting or approving a Go PR, verify:
- [ ] Code is formatted with `go fmt` / `make fmt` and tabs are used for indentation.
- [ ] Naming conventions strictly follow Section 4 (Google Go Style: initialisms `FlowID`/`URL`/`MCP`, scope-proportional variable lengths, stutter-free APIs, getters without `Get`, and `Err*` sentinel errors).
- [ ] All errors are explicitly handled or wrapped with contextual information (`%w`).
- [ ] No internal telemetry (epochs, lag, settlement flags) is leaked into primary views (Anti-Telemetry Guard).
- [ ] No absolute user home directory paths (`/Users/...`) are introduced.
- [ ] Any public egress, MCP output, or persistence path routes through `internal/secret` redaction.
- [ ] Context cancellation and timeouts are respected across all I/O and subprocess calls.
- [ ] Struct tags match JSON schema contracts in `schemas/`.
- [ ] Unit tests are provided (using `t.TempDir()`, `t.Setenv()`, and table-driven style).
- [ ] Tests pass cleanly with `go test -race -count=1 ./...`.
