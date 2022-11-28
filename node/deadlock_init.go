package node

import (
	"fmt"
	"time"

	sync "github.com/sasha-s/go-deadlock"
)

func init() {
	sync.Opts.OnPotentialDeadlock = func() {
		fmt.Println("===========================")
		fmt.Println("POTENTIAL DEADLOCK DETECTED")
		fmt.Println("===========================")
	}
	sync.Opts.DeadlockTimeout = 1 * time.Minute
}
