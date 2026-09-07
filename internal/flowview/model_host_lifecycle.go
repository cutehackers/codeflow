package flowview

import (
	"context"
	"errors"
	"sync"

	"codeflow/internal/protocol"
)

var errFlowViewModelHostServerClosed = errors.New("flowview: model-host requests are closed")

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

// beginModelHostRequest admits one enrichment operation. The returned factory
// is request-scoped and serializes its admission check with Shutdown so a host
// cannot be spawned after shutdown begins. A nil configured factory is a
// supported unavailable configuration and does not create lifecycle state.
func (s *Server) beginModelHostRequest(parent context.Context) (context.Context, protocol.ModelHostFactory, func(), error) {
	if parent == nil {
		parent = context.Background()
	}
	s.modelHostMu.Lock()
	if s.modelHostClosed || s.modelHostClosing.Load() {
		s.modelHostMu.Unlock()
		return nil, nil, func() {}, errFlowViewModelHostServerClosed
	}
	if s.modelHostFactory == nil {
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
		// mutex remains held through the factory call. Shutdown sets the
		// independent closing signal first, so a call waiting here cannot start
		// after shutdown has closed admission.
		return s.callModelHostFactory(factoryCtx, s.modelHostFactory, false)
	})
	return requestCtx, requestFactory, release, nil
}

// callModelHostFactory is the single Core-owned spawn gate. The gate is held
// through the configured factory invocation, while the independent closing
// signal lets Shutdown cancel and reject calls that have not entered it yet.
// tracked is used by a coordinator-owned wrapper that did not go through the
// normal HTTP enrichment admission path.
func (s *Server) callModelHostFactory(ctx context.Context, factory protocol.ModelHostFactory, tracked bool) (*protocol.ModelHost, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if factory == nil {
		return nil, errors.New("flowview: model-host factory is unavailable")
	}
	s.modelHostSpawnMu.Lock()
	defer s.modelHostSpawnMu.Unlock()
	if s.modelHostClosing.Load() {
		return nil, errFlowViewModelHostServerClosed
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
			return nil, errFlowViewModelHostServerClosed
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
		return nil, errFlowViewModelHostServerClosed
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

func (s *Server) waitLiveConsumer(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.liveMu.Lock()
	if s.liveDone == nil {
		s.liveDone = closedLifecycleChannel()
	}
	done := s.liveDone
	s.liveMu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
