//go:build deadlock
// +build deadlock

package sync

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"time"

	deadlock "github.com/sasha-s/go-deadlock"
)

const LockTimeout = 15 * time.Second

// muxHelper adds some debugging features, like timeout support, to mutex locks.
type muxHelper struct {
	timerLock  deadlock.Mutex
	timer      []*time.Timer
	stacks     []string
	lockHolder string
	startTime  []time.Time
}

func (m *muxHelper) print(dest io.Writer, header string, stack []byte) {
	fmt.Fprintln(dest, header)

	fmt.Fprintln(dest, "----------------")
	fmt.Fprintln(dest, "CALLED BY:")
	fmt.Fprintln(dest, string(stack))
	fmt.Fprintln(dest, "----------------")

	fmt.Fprintln(dest, "CURRENT LOCK HOLDER: "+m.lockHolder)
	fmt.Fprintln(dest, "WAIT QUEUE:")

	count := len(m.stacks)
	for i, item := range m.stacks {
		fmt.Fprintf(dest, "LOCK %d/%d:\n-------------------\n%s\n-------------------\n", i+1, count, item)
	}
}

// lockTimeoutHandler is executed when lock times out
func (m *muxHelper) lockTimeoutHandler(stack []byte) func() {
	return func() {
		m.timerLock.Lock()
		defer m.timerLock.Unlock()

		m.print(os.Stderr, "ERROR: LOCK TIMEOUT", stack)
		os.Exit(1) // we don't panic(), because panic is just ending current goroutine
	}
}

func (m *muxHelper) beforeLock() {
	m.timerLock.Lock()
	defer m.timerLock.Unlock()

	stack := debug.Stack()
	m.timer = append(m.timer, time.AfterFunc(LockTimeout, m.lockTimeoutHandler(stack)))
	m.startTime = append(m.startTime, time.Now())
	m.stacks = append(m.stacks, string(stack))
}

func (m *muxHelper) afterLock() {
	m.timerLock.Lock()
	defer m.timerLock.Unlock()
	stack := debug.Stack()
	if m.timer != nil && len(m.timer) > 0 {
		// we take first lock; this can be wrong for RWMutex or other edge cases, but it's "good enough" for now
		m.timer[0].Stop()
		m.timer = m.timer[1:]
		m.stacks = m.stacks[1:]
		took := time.Since(m.startTime[0])
		m.startTime = m.startTime[1:]
		if took > 5*time.Second {
			m.print(os.Stderr, fmt.Sprintf("WARNING: LOCK wait time is high: %s", took.String()), stack)
		}
	}

	m.lockHolder = string(stack)
}

func (m *muxHelper) beforeUnlock() {
	m.timerLock.Lock()
	defer m.timerLock.Unlock()

	m.lockHolder = ""
}

func (m *muxHelper) afterUnlock() {
	// m.timerLock.Lock()
	// defer m.timerLock.Unlock()
}
