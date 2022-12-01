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

	// Set deadlock timeout to 5 minutes, to give enough time to state sync to execute and retry.
	sync.Opts.DeadlockTimeout = 5 * time.Minute
}
