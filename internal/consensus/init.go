package consensus

import (
	"time"

	sync "github.com/sasha-s/go-deadlock"
)

func init() {
	sync.Opts.DeadlockTimeout = 3 * time.Second
}
