package consensus

import (
	"time"

	"github.com/sasha-s/go-deadlock"
)

func init() {
	deadlock.Opts.DeadlockTimeout = 5 * time.Second
}
