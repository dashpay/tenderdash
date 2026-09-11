package consensus

import (
	"time"

	"github.com/jonboulle/clockwork"
	sync "github.com/sasha-s/go-deadlock"
)

// A peer's unverified height may delay proposals only for a bounded interval.
const maxCatchupSuppression = 10 * time.Minute

// catchupTracker delays proposals after block sync until the handover target is
// applied, or its unverified height claim expires. Voting remains enabled.
type catchupTracker struct {
	mtx          sync.Mutex
	targetHeight int64 // committed block height, not consensus proposal height
	expiresAt    time.Time
	clock        clockwork.Clock
}

func (t *catchupTracker) arm(targetHeight int64, clock clockwork.Clock) {
	if t == nil {
		return
	}
	t.mtx.Lock()
	defer t.mtx.Unlock()
	t.targetHeight = targetHeight
	t.clock = clock
	t.expiresAt = clock.Now().Add(maxCatchupSuppression)
}

// mayPropose receives the next local consensus height. Lower peer claims and
// peer removal cannot erase the target; only another handover can re-arm it.
func (t *catchupTracker) mayPropose(height int64) bool {
	if t == nil {
		return true
	}
	t.mtx.Lock()
	defer t.mtx.Unlock()
	if t.targetHeight == 0 {
		return true
	}
	if height <= t.targetHeight && t.clock.Now().Before(t.expiresAt) {
		return false
	}
	t.targetHeight = 0
	return true
}
