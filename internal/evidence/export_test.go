package evidence

import (
	"context"
	"time"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/types"
)

// SetEvidenceSyncIntervalForTesting overrides evidenceSyncInterval for the
// duration of a test. Call the returned function (typically via defer) to
// restore the original value.
//
// This is an internal-package test export compiled only during `go test`.
// It is not part of the package's public API.
func SetEvidenceSyncIntervalForTesting(d time.Duration) func() {
	old := evidenceSyncInterval
	evidenceSyncInterval = d
	return func() { evidenceSyncInterval = old }
}

// PeerRoutineCount returns the number of active per-peer sync-routine entries.
// Test-only accessor for the unexported peerRoutines map.
func (r *Reactor) PeerRoutineCount() int {
	r.mtx.Lock()
	defer r.mtx.Unlock()
	return len(r.peerRoutines)
}

// PeerRoutineActive reports whether a sync-routine map entry exists for peerID.
// Test-only accessor for the unexported peerRoutines map.
func (r *Reactor) PeerRoutineActive(peerID types.NodeID) bool {
	r.mtx.Lock()
	defer r.mtx.Unlock()
	_, ok := r.peerRoutines[peerID]
	return ok
}

// PeerRoutineID returns the monotonic identity assigned to peerID's current sync
// routine, or 0 if none (ids start at 1, so 0 unambiguously means "absent").
// Callers assert that two sequentially-created goroutines for the same peer
// received distinct ids — the property the Down→Up flap guard relies on.
// Test-only accessor for the unexported peerRoutines map.
func (r *Reactor) PeerRoutineID(peerID types.NodeID) uint64 {
	r.mtx.Lock()
	defer r.mtx.Unlock()
	entry, ok := r.peerRoutines[peerID]
	if !ok {
		return 0
	}
	return entry.id
}

// HandleEvidenceMessageForTest exposes the unexported handleEvidenceMessage so
// external (evidence_test) tests can drive a single inbound envelope directly.
// Test-only.
func (r *Reactor) HandleEvidenceMessageForTest(ctx context.Context, envelope *p2p.Envelope) error {
	return r.handleEvidenceMessage(ctx, envelope)
}
