//go:build deadlock
// +build deadlock

package sync

import (
	deadlock "github.com/sasha-s/go-deadlock"
)

// A Mutex is a mutual exclusion lock.
type Mutex struct {
	deadlock.Mutex
	muxHelper
}

func (m *Mutex) Lock() {
	m.muxHelper.beforeLock()
	m.Mutex.Lock()
	m.muxHelper.afterLock()
}
func (m *Mutex) Unlock() {
	m.muxHelper.beforeUnlock()
	m.Mutex.Unlock()
	m.muxHelper.afterUnlock()
}

// An RWMutex is a reader/writer mutual exclusion lock.
type RWMutex struct {
	deadlock.RWMutex
	readMuxHelper  muxHelper
	writeMuxHelper muxHelper
}

func (m *RWMutex) Lock() {
	m.writeMuxHelper.beforeLock()
	m.RWMutex.Lock()
	m.writeMuxHelper.afterLock()

}
func (m *RWMutex) Unlock() {
	// no need to lock timerLock, as we are already locked by RWMutex.Lock
	m.writeMuxHelper.beforeUnlock()
	m.RWMutex.Unlock()
	m.writeMuxHelper.afterUnlock()
}

func (m *RWMutex) RLock() {
	m.readMuxHelper.beforeLock()
	m.RWMutex.RLock()
	m.readMuxHelper.afterLock()
}

func (m *RWMutex) RUnlock() {
	m.readMuxHelper.beforeUnlock()
	m.RWMutex.RUnlock()
	m.readMuxHelper.afterUnlock()
}
