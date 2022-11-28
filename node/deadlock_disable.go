//go:build !deadlock
// +build !deadlock

package node

import (
	sync "github.com/sasha-s/go-deadlock"
)

func init() {
	// Disable go-deadlock deadlock detection if we don't have deadlock flag set.
	sync.Opts.Disable = true
}
