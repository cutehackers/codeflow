package protocol

import (
	"context"
	"errors"
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
}

// NewPool creates a pool spawning adapters with cfg and keeping at most
// maxIdle idle processes warm (maxIdle <= 0 means no pooling).
func NewPool(cfg Config, maxIdle int) *Pool {
	return &Pool{cfg: cfg.withDefaults(), maxIdle: maxIdle, idleTTL: DefaultIdleTTL}
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
			continue
		}
		if time.Since(e.returnedAt) > p.idleTTL {
			probeCtx, cancel := context.WithTimeout(context.Background(), defaultIdleProbeTimeout)
			_, err := e.conn.Ping(probeCtx)
			cancel()
			if err != nil {
				e.conn.Close()
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
	p.mu.Lock()
	if p.closed || p.maxIdle <= 0 || c.Broken() != nil {
		p.mu.Unlock()
		c.Close()
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
	conn, err := p.Get(ctx)
	if err != nil {
		return err
	}
	err = conn.Call(ctx, op, params, result)
	if !isCrash(err) {
		if conn.Broken() == nil {
			p.Put(conn)
		} else {
			conn.Close()
		}
		return err
	}

	// First crash: transparent restart once, resend the same request.
	conn.Close()
	retryConn, rerr := p.Get(ctx)
	if rerr != nil {
		return err // surface the original crash; spawn failure detail lost otherwise
	}
	err2 := retryConn.Call(ctx, op, params, result)
	if !isCrash(err2) && retryConn.Broken() == nil {
		p.Put(retryConn)
	} else if isCrash(err2) {
		retryConn.Close()
	}
	return err2
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
	}
}
