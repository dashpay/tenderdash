package evidence

import (
	"reflect"
	"time"

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

// PeerRoutineToken returns the address of the identity token for peerID's
// current sync routine, or 0 if none. It exposes the token's IDENTITY (its
// address), not its value, so callers can assert that two sequentially-created
// goroutines for the same peer received distinct tokens. With the pre-fix
// zero-size (*struct{}) token every allocation shared runtime.zerobase, so this
// returned the same address for every goroutine — this accessor makes that
// regression observable. Test-only accessor for the unexported peerRoutines map.
func (r *Reactor) PeerRoutineToken(peerID types.NodeID) uintptr {
	r.mtx.Lock()
	defer r.mtx.Unlock()
	entry, ok := r.peerRoutines[peerID]
	if !ok {
		return 0
	}
	return reflect.ValueOf(entry.id).Pointer()
}
