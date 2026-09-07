package mcp

import (
	"context"
	"errors"
	"sync"

	"codeflow/internal/protocol"
)

var errMCPModelHostServerClosed = errors.New("mcp: model-host requests are closed")

func closedLifecycleChannel() chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}

func joinLifecycleErrors(errs ...error) error {
	nonNil := make([]error, 0, len(errs))
	for _, err := range errs {
		if err != nil {
			nonNil = append(nonNil, err)
		}
	}
	switch len(nonNil) {
	case 0:
		return nil
	case 1:
		return nonNil[0]
	default:
		return errors.Join(nonNil...)
	}
}

// beginModelHostRequest admits one enrichment operation. A configured
// factory is wrapped with the request context and a shutdown-serialized
// admission check, so it cannot start a host after Close begins.
func (s *Server) beginModelHostRequest(parent context.Context) (context.Context, protocol.ModelHostFactory, func(), error) {
	if parent == nil {
		parent = context.Background()
	}
	s.modelHostMu.Lock()
	if s.modelHostClosed || s.modelHostClosing.Load() {
		s.modelHostMu.Unlock()
		return nil, nil, func() {}, errMCPModelHostServerClosed
	}
	if s.cfg.ModelHostFactory == nil {
		s.modelHostMu.Unlock()
		return parent, nil, func() {}, nil
	}
	requestCtx, cancel := context.WithCancel(parent)
	if s.modelHostActive == nil {
		s.modelHostActive = make(map[uint64]context.CancelFunc)
	}
	if len(s.modelHostActive) == 0 {
		s.modelHostDrainDone = make(chan struct{})
	}
	if s.modelHostDrainDone == nil {
		s.modelHostDrainDone = closedLifecycleChannel()
	}
	s.modelHostNextID++
	requestID := s.modelHostNextID
	s.modelHostActive[requestID] = cancel
	s.modelHostWG.Add(1)
	s.modelHostMu.Unlock()

	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			cancel()
			s.modelHostMu.Lock()
			delete(s.modelHostActive, requestID)
			if len(s.modelHostActive) == 0 && s.modelHostDrainDone != nil {
				close(s.modelHostDrainDone)
			}
			s.modelHostMu.Unlock()
			s.modelHostWG.Done()
		})
	}

	requestFactory := protocol.ModelHostFactory(func(factoryCtx context.Context) (*protocol.ModelHost, error) {
		if factoryCtx == nil {
			factoryCtx = requestCtx
		}
		// Closing is checked while holding the spawn-admission mutex and that
		// mutex remains held through the factory call. Close sets the
		// independent closing signal first, so a call waiting here cannot start
		// after shutdown has closed admission.
		return s.callModelHostFactory(factoryCtx, s.cfg.ModelHostFactory, false)
	})
	return requestCtx, requestFactory, release, nil
}

// callModelHostFactory is the MCP-owned spawn gate used by direct requests and
// by lazy FlowView coordinators. A coordinator cannot retain a raw factory
// that outlives MCP admission. tracked records coordinator calls so Close can
// cancel and join them just like direct enrichment requests.
func (s *Server) callModelHostFactory(ctx context.Context, factory protocol.ModelHostFactory, tracked bool) (*protocol.ModelHost, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if factory == nil {
		return nil, errors.New("mcp: model-host factory is unavailable")
	}
	s.modelHostSpawnMu.Lock()
	defer s.modelHostSpawnMu.Unlock()
	if s.modelHostClosing.Load() {
		return nil, errMCPModelHostServerClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var releaseTracked func()
	if tracked {
		trackedCtx, cancel := context.WithCancel(ctx)
		s.modelHostMu.Lock()
		if s.modelHostClosed || s.modelHostClosing.Load() {
			s.modelHostMu.Unlock()
			cancel()
			return nil, errMCPModelHostServerClosed
		}
		if s.modelHostActive == nil {
			s.modelHostActive = make(map[uint64]context.CancelFunc)
		}
		if len(s.modelHostActive) == 0 {
			s.modelHostDrainDone = make(chan struct{})
		}
		s.modelHostNextID++
		requestID := s.modelHostNextID
		s.modelHostActive[requestID] = cancel
		s.modelHostWG.Add(1)
		s.modelHostMu.Unlock()
		ctx = trackedCtx
		var releaseOnce sync.Once
		releaseTracked = func() {
			releaseOnce.Do(func() {
				cancel()
				s.modelHostMu.Lock()
				delete(s.modelHostActive, requestID)
				if len(s.modelHostActive) == 0 && s.modelHostDrainDone != nil {
					close(s.modelHostDrainDone)
				}
				s.modelHostMu.Unlock()
				s.modelHostWG.Done()
			})
		}
	}
	if releaseTracked != nil {
		defer releaseTracked()
	}

	s.modelHostMu.Lock()
	if s.modelHostClosed || s.modelHostClosing.Load() {
		s.modelHostMu.Unlock()
		return nil, errMCPModelHostServerClosed
	}
	if s.modelHostSpawning == 0 {
		s.modelHostSpawnDone = make(chan struct{})
	}
	s.modelHostSpawning++
	s.modelHostMu.Unlock()
	defer func() {
		s.modelHostMu.Lock()
		s.modelHostSpawning--
		if s.modelHostSpawning == 0 && s.modelHostSpawnDone != nil {
			close(s.modelHostSpawnDone)
		}
		s.modelHostMu.Unlock()
	}()
	return factory(ctx)
}

func (s *Server) coordinatorModelHostFactory() protocol.ModelHostFactory {
	if s == nil || s.cfg.ModelHostFactory == nil {
		return nil
	}
	return func(ctx context.Context) (*protocol.ModelHost, error) {
		return s.callModelHostFactory(ctx, s.cfg.ModelHostFactory, true)
	}
}

// stopModelHostRequests closes admission and cancels every accepted
// enrichment operation. It then synchronizes with any factory invocation
// already holding the spawn-admission mutex, bounded by ctx.
func (s *Server) stopModelHostRequests(ctx context.Context) error {
	s.modelHostClosing.Store(true)
	s.modelHostMu.Lock()
	if s.modelHostClosed {
		s.modelHostMu.Unlock()
		return nil
	}
	s.modelHostClosed = true
	cancels := make([]context.CancelFunc, 0, len(s.modelHostActive))
	for _, cancel := range s.modelHostActive {
		cancels = append(cancels, cancel)
	}
	s.modelHostMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	return s.waitModelHostSpawn(ctx)
}

func (s *Server) waitModelHostSpawn(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.modelHostMu.Lock()
	if s.modelHostSpawnDone == nil {
		s.modelHostSpawnDone = closedLifecycleChannel()
	}
	done := s.modelHostSpawnDone
	s.modelHostMu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) waitModelHostRequests(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.modelHostMu.Lock()
	if s.modelHostDrainDone == nil {
		s.modelHostDrainDone = closedLifecycleChannel()
	}
	done := s.modelHostDrainDone
	s.modelHostMu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
