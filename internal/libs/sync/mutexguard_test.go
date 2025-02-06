package sync_test

import (
	"sync"
	"testing"
	"time"

	deadlock "github.com/sasha-s/go-deadlock"
	"github.com/stretchr/testify/assert"

	tmsync "github.com/dashpay/tenderdash/internal/libs/sync"
)

// TestLockGuardMultipleUnlocks checks that the LockGuard function correctly handles multiple unlock calls.
func TestLockGuardMultipleUnlocks(t *testing.T) {
	// Disable deadlock detection logic for this test
	deadlockDisabled := deadlock.Opts.Disable
	deadlock.Opts.Disable = true
	defer func() {
		deadlock.Opts.Disable = deadlockDisabled
	}()
	var mtx deadlock.Mutex

	unlock := tmsync.LockGuard(&mtx)
	// deferred unlock() will do nothing because we unlock inside the test, but we still want to check this
	defer func() { assert.False(t, unlock()) }()
	// locking should not be possible
	assert.False(t, mtx.TryLock())

	assert.True(t, unlock())
	//  here we can lock
	mtx.Lock()
	// unlock should do nothing
	assert.False(t, unlock())
	// locking again should not be possible
	assert.False(t, mtx.TryLock())
	// unlock should do nothing
	assert.False(t, unlock())
	// but this unlock should work
	mtx.Unlock()
	assert.True(t, mtx.TryLock())
}

// TestLockGuard checks that the LockGuard function correctly increments a counter using multiple goroutines.
func TestLockGuard(t *testing.T) {
	var mtx deadlock.Mutex
	var counter int
	var wg sync.WaitGroup

	increment := func() {
		defer wg.Done()
		unlock := tmsync.LockGuard(&mtx)
		defer unlock()
		counter++
	}

	// Start multiple goroutines to increment the counter
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go increment()
	}

	wg.Wait()
	assert.Equal(t, 100, counter, "Counter should be incremented to 100")
}

// TestRLockGuard checks that the RLockGuard function allows multiple read locks
// and correctly increments a counter using write locks.
func TestRLockGuard(t *testing.T) {
	var mtx deadlock.RWMutex
	var counter int
	var wg sync.WaitGroup

	read := func() {
		defer wg.Done()
		unlock := tmsync.RLockGuard(&mtx)
		defer unlock()
		_ = counter // Just read the counter
	}

	write := func() {
		defer wg.Done()
		unlock := tmsync.LockGuard(&mtx)
		defer unlock()
		counter++
	}

	// Start multiple goroutines to read the counter
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go read()
	}

	// Start multiple goroutines to write to the counter
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go write()
	}

	wg.Wait()
	assert.Equal(t, 10, counter, "Counter should be incremented to 10")
}

// TestMixedLocks checks the behavior of mixed read and write locks,
// ensuring that the counter is correctly incremented while allowing concurrent reads.
func TestMixedLocks(t *testing.T) {
	var mtx deadlock.RWMutex
	var counter int
	var wg sync.WaitGroup

	read := func() {
		defer wg.Done()
		unlock := tmsync.RLockGuard(&mtx)
		defer unlock()
		time.Sleep(10 * time.Millisecond) // Simulate read delay
		_ = counter                       // Just read the counter
	}

	write := func() {
		defer wg.Done()
		unlock := tmsync.LockGuard(&mtx)
		defer unlock()
		counter++
		time.Sleep(10 * time.Millisecond) // Simulate write delay
	}

	// Start multiple goroutines to read the counter
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go read()
	}

	// Start multiple goroutines to write to the counter
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go write()
	}

	wg.Wait()
	assert.Equal(t, 5, counter, "Counter should be incremented to 5")
}
