package consensus

import (
	"time"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	tmtime "github.com/dashpay/tenderdash/libs/time"
)

type roundScheduler struct {
	// state changes may be triggered by: msgs from peers,
	// msgs from ourself, or by timeouts
	timeoutTicker TimeoutTicker
}

// ScheduleRound0 enterNewRoundCommand(height, 0) at StartTime
func (b *roundScheduler) ScheduleRound0(rs cstypes.RoundState) {
	sleepDuration := rs.StartTime.Sub(tmtime.Now())
	b.ScheduleTimeout(sleepDuration, rs.Height, 0, cstypes.RoundStepNewHeight)
}

// ScheduleTimeout attempts to schedule a timeout (by sending timeoutInfo on the tickChan)
func (b *roundScheduler) ScheduleTimeout(duration time.Duration, height int64, round int32, step cstypes.RoundStepType) {
	b.timeoutTicker.ScheduleTimeout(timeoutInfo{duration, height, round, step})
}
