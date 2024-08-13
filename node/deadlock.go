package node

import (
	"fmt"

	"github.com/dashpay/tenderdash/config"
	sync "github.com/sasha-s/go-deadlock"
)

func SetupDeadlockDetection(cfg *config.BaseConfig) {
	if cfg.DeadlockDetection == 0 {
		sync.Opts.Disable = true
	} else {
		sync.Opts.Disable = false
		sync.Opts.OnPotentialDeadlock = func() {
			fmt.Println("===========================")
			fmt.Println("POTENTIAL DEADLOCK DETECTED")
			fmt.Println("===========================")
		}

		// Set deadlock timeout to 5 minutes, to give enough time to state sync to execute and retry.
		sync.Opts.DeadlockTimeout = cfg.DeadlockDetection
	}
}
