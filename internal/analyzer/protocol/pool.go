package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"
)

// DefaultIdleTTL is how long an idle pooled connection is trusted
// without a health probe before reuse.
const DefaultIdleTTL = 30 * time.Second

// defaultShutdownGrace bounds the graceful shutdown op during Pool.Close.
const defaultShutdownGrace = 500 * time.Millisecond

type poolEntry struct {
	conn       *Conn
	returnedAt time.Time
}

// Pool keeps persistent adapter subprocesses alive across requests and
// reuses them (design §5.2 영속 프로세스 풀). Get hands out healthy idle
// conns or spawns fresh ones; Call implements the crash policy of
// design §12: restart ONCE per request chain and retry the same request
// once; a second consecutive crash surfaces E_CRASHED to the caller.
type Pool struct {
	cfg     Config
	maxIdle int
	idleTTL time.Duration

	mu     sync.Mutex
	idle   []*poolEntry // newest last
	closed bool

	isolationMu       sync.Mutex
	isolationEvidence map[string]MountPermissionEvidence
}

// NewPool creates a pool spawning adapters with cfg and keeping at most
// maxIdle idle processes warm (maxIdle <= 0 means no pooling).
func NewPool(cfg Config, maxIdle int) *Pool {
	return &Pool{cfg: cfg.withDefaults(), maxIdle: maxIdle, idleTTL: DefaultIdleTTL, isolationEvidence: make(map[string]MountPermissionEvidence)}
}

func (p *Pool) recordIsolationEvidence(c *Conn) {
	if p == nil || c == nil {
		return
	}
	ev := c.MountPermissionEvidence()
	key := c.workDir
	if key == "" {
		return
	}
	p.isolationMu.Lock()
	p.isolationEvidence[key] = ev
	p.isolationMu.Unlock()
}

// MountPermissionEvidence returns aggregate evidence for every process this
// pool has spawned. It remains available after Close so registry callers can
// verify cleanup for crashed and replaced children.
func (p *Pool) MountPermissionEvidence() MountPermissionEvidence {
	if p == nil {
		return MountPermissionEvidence{}
	}
	p.isolationMu.Lock()
	defer p.isolationMu.Unlock()
	var aggregate MountPermissionEvidence
	first := true
	for _, ev := range p.isolationEvidence {
		if first {
			aggregate = ev
			aggregate.TerminalModes = append([]string(nil), ev.TerminalModes...)
			first = false
			continue
		}
		if aggregate.SourceDelivery == "" {
			aggregate.SourceDelivery = ev.SourceDelivery
		}
		if aggregate.SourceMount == "" {
			aggregate.SourceMount = ev.SourceMount
		}
		aggregate.ReadOnlySource = aggregate.ReadOnlySource && ev.ReadOnlySource
		aggregate.Disposable = aggregate.Disposable && ev.Disposable
		aggregate.RepositoryPathExposed = aggregate.RepositoryPathExposed || ev.RepositoryPathExposed
		aggregate.DependencyEnvironmentPreserved = aggregate.DependencyEnvironmentPreserved && ev.DependencyEnvironmentPreserved
		aggregate.CleanupVerified = aggregate.CleanupVerified && ev.CleanupVerified
		for _, mode := range ev.TerminalModes {
			found := false
			for _, existing := range aggregate.TerminalModes {
				if existing == mode {
					found = true
					break
				}
			}
			if !found {
				aggregate.TerminalModes = append(aggregate.TerminalModes, mode)
			}
		}
	}
	sort.Strings(aggregate.TerminalModes)
	return aggregate
}

// Get returns a healthy connection, preferring the newest idle one.
// Conns idle beyond the TTL are ping-probed before reuse; unhealthy or
// stale-broken entries are discarded. Otherwise a fresh subprocess is
// spawned (including its handshake).
func (p *Pool) Get(ctx context.Context) (*Conn, error) {
	for {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return nil, CrashedError("pool closed")
		}
		var e *poolEntry
		if n := len(p.idle); n > 0 {
			e = p.idle[n-1]
			p.idle = p.idle[:n-1]
		}
		p.mu.Unlock()

		if e == nil {
			return Spawn(ctx, p.cfg)
		}
		if err := e.conn.Broken(); err != nil {
			e.conn.Close()
			p.recordIsolationEvidence(e.conn)
			continue
		}
		if time.Since(e.returnedAt) > p.idleTTL {
			probeCtx, cancel := context.WithTimeout(context.Background(), defaultIdleProbeTimeout)
			_, err := e.conn.Ping(probeCtx)
			cancel()
			if err != nil {
				e.conn.Close()
				p.recordIsolationEvidence(e.conn)
				continue
			}
		}
		return e.conn, nil
	}
}

// Put returns a connection to the idle list. Broken connections and
// overflow beyond maxIdle are closed immediately.
func (p *Pool) Put(c *Conn) {
	if c == nil {
		return
	}
	p.recordIsolationEvidence(c)
	p.mu.Lock()
	if p.closed || p.maxIdle <= 0 || c.Broken() != nil {
		p.mu.Unlock()
		c.Close()
		p.recordIsolationEvidence(c)
		return
	}
	p.idle = append(p.idle, &poolEntry{conn: c, returnedAt: time.Now()})
	var evicted []*Conn
	for len(p.idle) > p.maxIdle {
		evicted = append(evicted, p.idle[0].conn)
		p.idle = p.idle[1:]
	}
	p.mu.Unlock()
	for _, old := range evicted {
		old.Close()
		p.recordIsolationEvidence(old)
	}
}

// Call runs one request against the pool with the §12 crash policy:
// if the conn crashes (E_CRASHED), it is discarded, a fresh process is
// spawned, and the identical request is retried exactly once. A second
// consecutive crash surfaces E_CRASHED to the caller. Any other outcome
// (success, timeout, cancellation, backpressure, bad request, adapter
// error) returns without retry, and the conn goes back to the pool when
// still healthy.
func (p *Pool) Call(ctx context.Context, op string, params any, result any) error {
	params, err := normalizeAnalysisParams(params, op)
	if err != nil {
		return err
	}
	conn, err := p.Get(ctx)
	if err != nil {
		return err
	}
	err = conn.Call(ctx, op, params, result)
	p.recordIsolationEvidence(conn)
	if !isCrash(err) {
		if conn.Broken() == nil {
			p.Put(conn)
		} else {
			conn.Close()
			p.recordIsolationEvidence(conn)
		}
		return err
	}

	// First crash: transparent restart once, resend the same request.
	conn.Close()
	p.recordIsolationEvidence(conn)
	retryConn, rerr := p.Get(ctx)
	if rerr != nil {
		return err // surface the original crash; spawn failure detail lost otherwise
	}
	err2 := retryConn.Call(ctx, op, params, result)
	p.recordIsolationEvidence(retryConn)
	if !isCrash(err2) && retryConn.Broken() == nil {
		p.Put(retryConn)
	} else {
		// A retry may fail without E_CRASHED after the connection has
		// already been marked broken. It is never safe to leave that
		// subprocess in the pool or running in the background.
		retryConn.Close()
		p.recordIsolationEvidence(retryConn)
	}
	return err2
}

// normalizeAnalysisParams preserves the pre-VS-02 Go seam for callers that
// still provide repoRoot, while ensuring the adapter receives only one
// immutable snapshot captured at the Core boundary. The compatibility field
// is removed before the request is serialized, so adapters never need to
// read the live worktree.
func normalizeAnalysisParams(params any, op string) (any, error) {
	if op != OpDetect && op != OpHarvestCandidates && op != OpSlice {
		return params, nil
	}
	request, ok := params.(map[string]any)
	if !ok {
		return params, nil
	}
	root, _ := request["repoRoot"].(string)
	if root == "" {
		return params, nil
	}

	snapshot, err := CaptureSnapshot(root, snapshotEpoch(request))
	if err != nil {
		return nil, err
	}
	normalized := snapshot.Params()
	for key, value := range request {
		if key != "repoRoot" {
			normalized[key] = value
		}
	}
	return normalized, nil
}

func snapshotEpoch(request map[string]any) int64 {
	switch value := request["workspaceEpoch"].(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	case json.Number:
		if epoch, err := value.Int64(); err == nil {
			return epoch
		}
	}
	return 0
}

func isCrash(err error) bool {
	var perr *Error
	return errors.As(err, &perr) && perr.Code == ECrashed
}

// Close drains every idle conn: best-effort graceful shutdown op, kill
// after grace. The pool rejects further Get/Put/Call.
func (p *Pool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	conns := make([]*Conn, 0, len(p.idle))
	for _, e := range p.idle {
		conns = append(conns, e.conn)
	}
	p.idle = nil
	p.mu.Unlock()

	for _, c := range conns {
		_ = c.Shutdown(defaultShutdownGrace)
		p.recordIsolationEvidence(c)
	}
}

// AdapterRegistry manages language-specific process pools.
type AdapterRegistry struct {
	mu                 sync.RWMutex
	pools              map[string]*Pool
	cfgs               map[string]Config
	maxIdle            int
	closed             bool
	capabilityRegistry *CapabilityRegistry
}

// NewAdapterRegistry initializes a multi-language pool manager.
func NewAdapterRegistry(maxIdle int) *AdapterRegistry {
	return &AdapterRegistry{
		pools:              make(map[string]*Pool),
		cfgs:               make(map[string]Config),
		maxIdle:            maxIdle,
		capabilityRegistry: NewCapabilityRegistry(DefaultCapabilityMeasurementTTL),
	}
}

// RegisterConfig registers or updates the adapter config for a given language.
func (r *AdapterRegistry) RegisterConfig(lang string, cfg Config) {
	r.mu.Lock()
	r.cfgs[lang] = cfg
	capabilities := r.capabilityRegistry
	r.mu.Unlock()
	if capabilities != nil {
		capabilities.Invalidate(lang)
	}
}

// GetPool returns or creates a Pool for the requested language.
func (r *AdapterRegistry) GetPool(lang string) (*Pool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, CrashedError("adapter registry closed")
	}
	if p, ok := r.pools[lang]; ok {
		return p, nil
	}
	cfg, ok := r.cfgs[lang]
	if !ok {
		return nil, errors.New("no adapter configuration registered for language: " + lang)
	}
	p := NewPool(cfg, r.maxIdle)
	r.pools[lang] = p
	return p, nil
}

// Call routes a request to the appropriate language pool.
func (r *AdapterRegistry) Call(ctx context.Context, lang string, op string, params any, result any) error {
	pool, err := r.GetPool(lang)
	if err != nil {
		return err
	}
	return pool.Call(ctx, op, params, result)
}

// Close drains and shuts down all language adapter pools.
func (r *AdapterRegistry) Close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	pools := make([]*Pool, 0, len(r.pools))
	for _, p := range r.pools {
		pools = append(pools, p)
	}
	r.pools = nil
	r.mu.Unlock()

	for _, p := range pools {
		p.Close()
	}
}
