//go:build deadlock
// +build deadlock

package sync

import (
	"fmt"
	"os"
	"runtime/debug"
	"time"

	deadlock "github.com/sasha-s/go-deadlock"
)

const LockTimeout = 15 * time.Second

// A Mutex is a mutual exclusion lock.
type Mutex struct {
	deadlock.Mutex
	mutexTimeout
}

// mutexTimeout adds timeout support to mutex locks
type mutexTimeout struct {
	timerLock deadlock.Mutex
	timer     []*time.Timer
	stacks    []string
	startTime []time.Time
}

func (m *Mutex) Lock() {
	m.mutexTimeout.start("deadlock.Mutex.Lock()")
	m.Mutex.Lock()
}
func (m *Mutex) Unlock() {
	m.mutexTimeout.stop()
	m.Mutex.Unlock()
}

// An RWMutex is a reader/writer mutual exclusion lock.
type RWMutex struct {
	deadlock.RWMutex
	readMutexTimeout  mutexTimeout
	writeMutexTimeout mutexTimeout
}

func (m *RWMutex) Lock() {
	m.writeMutexTimeout.start("deadlock.RWMutex.Lock()")
	m.RWMutex.Lock()
}
func (m *RWMutex) Unlock() {
	// no need to lock timerLock, as we are already locked by RWMutex.Lock
	m.writeMutexTimeout.stop()
	m.RWMutex.Unlock()
}

func (m *RWMutex) RLock() {
	m.readMutexTimeout.start("deadlock.RWMutex.RLock()")
	m.RWMutex.RLock()
}

func (m *RWMutex) RUnlock() {
	m.readMutexTimeout.stop()
	m.RWMutex.RUnlock()
}

// lockTimeoutHandler is executed when lock times out
func (m *mutexTimeout) lockTimeoutHandler(lockName string, stack []byte) func() {
	return func() {
		m.timerLock.Lock()
		defer m.timerLock.Unlock()

		fmt.Fprintf(os.Stderr, "ERROR: LOCK TIMEOUT: %s\n-------------------\n%s\n-------------------\n", lockName, stack)
		fmt.Fprintln(os.Stderr, "ALL LOCKS:")
		for i, item := range m.stacks {
			fmt.Fprintf(os.Stderr, "LOCK %d:\n-------------------\n%s\n-------------------\n", i, item)
		}

		os.Exit(1) // we don't panic(), because panic is just ending current goroutine
	}
}

func (m *mutexTimeout) start(name string) {
	m.timerLock.Lock()
	defer m.timerLock.Unlock()

	stack := debug.Stack()
	m.timer = append(m.timer, time.AfterFunc(LockTimeout, m.lockTimeoutHandler(name, stack)))
	m.startTime = append(m.startTime, time.Now())
	m.stacks = append(m.stacks, string(stack))
}

func (m *mutexTimeout) stop() {
	m.timerLock.Lock()
	defer m.timerLock.Unlock()

	if m.timer != nil && len(m.timer) > 0 {
		m.timer[0].Stop()
		m.timer = m.timer[1:]

		m.stacks = m.stacks[1:]

		took := time.Since(m.startTime[0])
		m.startTime = m.startTime[1:]
		if took > 5*time.Second {
			fmt.Fprintf(os.Stderr, "WARNING: LOCK wait time: %s\n--------------\n%s\n---------\n", took.String(), debug.Stack())
		}
	}
}
