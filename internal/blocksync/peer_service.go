package blocksync

import (
	"time"

	"github.com/jonboulle/clockwork"
	sync "github.com/sasha-s/go-deadlock"
)

const (
	// These are soft service allowances, not a measurement of wire bandwidth.
	serviceBlockAllowance = time.Second
	serviceBytesPerSecond = 128 * 1024
	serviceGrace          = 10 * time.Second
	serviceCooldown       = 30 * time.Second
	serviceRecoveryProbes = 2
)

// peerService counts active waiting once, even with overlapping requests. Local
// result publication/application and intervals without requests consume no budget.
// Each peer instance owns its state, so late results cannot poison a reconnect.
type peerService struct {
	mtx           sync.Mutex
	clock         clockwork.Clock
	active        int
	validating    int
	last          time.Time
	debt          time.Duration
	probation     bool
	probeReserved bool
	recoveries    int
	retryAt       time.Time
}

func newPeerService(clock clockwork.Clock) *peerService {
	return &peerService{clock: clock}
}

func (s *peerService) setClock(clock clockwork.Clock) {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	s.clock = clock
}

func (s *peerService) advance() time.Time {
	now := s.clock.Now()
	if s.active > 0 {
		s.debt += now.Sub(s.last)
	}
	s.last = now
	return now
}

func (s *peerService) classify(now time.Time) {
	// Validation runs outside the client dispatcher. Its scheduling delay must not
	// be mistaken for a lack of completed blocks.
	if s.validating == 0 && s.debt > serviceGrace && !s.probation {
		s.probation = true
		s.retryAt = now.Add(serviceCooldown)
		s.recoveries = 0
	}
}

// priority returns 2 for a due probe, 1 for ordinary work, and 0 while cooling.
func (s *peerService) priority(pending int32, reserve bool) int {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	now := s.advance()
	s.classify(now)
	if !s.probation {
		return 1
	}
	if pending != 0 || s.active != 0 || s.validating != 0 || s.probeReserved || now.Before(s.retryAt) {
		return 0
	}
	if reserve {
		s.probeReserved = true
	}
	return 2
}

func (s *peerService) request() *serviceRequest {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	return &serviceRequest{service: s, probe: s.probeReserved}
}

// serviceRequest is also the client observer. complete is called by the worker
// only after its existing validation, before returning to the result queue.
type serviceRequest struct {
	service  *peerService
	probe    bool
	finished bool
	start    time.Time
	elapsed  time.Duration
}

func (r *serviceRequest) Started() {
	s := r.service
	s.mtx.Lock()
	defer s.mtx.Unlock()
	r.start = s.advance()
	s.active++
}

func (r *serviceRequest) Finished() {
	s := r.service
	s.mtx.Lock()
	defer s.mtx.Unlock()
	now := s.advance()
	r.elapsed = now.Sub(r.start)
	r.finished = true
	s.active--
	s.validating++
}

func serviceAllowance(size int) time.Duration {
	return serviceBlockAllowance + time.Duration(size)*time.Second/serviceBytesPerSecond
}

func (r *serviceRequest) complete(size int) {
	s := r.service
	s.mtx.Lock()
	defer s.mtx.Unlock()
	now := s.advance()
	if r.finished {
		s.validating--
		if size > 0 {
			s.debt -= serviceAllowance(size)
			if s.debt < -serviceGrace {
				s.debt = -serviceGrace
			}
		}
	}
	// Bound retained history without discarding a large response's allowance
	// before accounting for its entire observed transfer interval.
	if s.debt > time.Minute {
		s.debt = time.Minute
	}
	if r.probe {
		s.probeReserved = false
		s.retryAt = now.Add(serviceCooldown)
		if r.finished && size > 0 && r.elapsed <= serviceAllowance(size) {
			s.recoveries++
		} else {
			s.recoveries = 0
		}
		if s.recoveries >= serviceRecoveryProbes {
			s.probation = false
			s.debt = 0
		}
	} else if s.active == 0 && s.validating == 0 {
		if s.debt <= serviceGrace {
			// Validated bytes can correct a provisional large-response penalty.
			s.probation = false
		} else if s.probation {
			s.retryAt = now.Add(serviceCooldown)
		}
	}
	s.classify(now)
}
