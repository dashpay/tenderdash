package blocksync

import (
	"os"
	"time"

	sm "github.com/dashpay/tenderdash/internal/state"
)

// bsPerfOn mirrors the state package's TD_BLOCK_PERF switch so the two stages of
// verify can be attributed without threading a logger through the applier.
var bsPerfOn = os.Getenv("TD_BLOCK_PERF") == "1"

type bsLap struct{ t time.Time }

func startLapBS() bsLap { return bsLap{t: time.Now()} }

func (l *bsLap) done(name string) {
	if !bsPerfOn {
		return
	}
	now := time.Now()
	sm.RecordPerf(name, now.Sub(l.t))
	l.t = now
}
