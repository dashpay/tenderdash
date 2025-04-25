package sync

// UnlockFn is a function that unlocks a mutex.
// It returns true if the mutex was unlocked, false if it was already unlocked.
type UnlockFn func() bool

// Mtx is a mutex interface.
// Implemented by sync.Mutex and deadlock.Mutex.
type Mtx interface {
	Lock()
	Unlock()
}

// RMtx is a mutex that can be locked for read.
//
// Implemented by sync.RwMutex and deadlock.RwMutex.
type RMtx interface {
	RLock()
	RUnlock()
}

// LockGuard locks the mutex and returns a function that unlocks it.
// The returned function must be called to release the lock.
// The returned function may be called multiple times - only the first call will unlock the mutex, others will be no-ops.
func LockGuard(mtx Mtx) UnlockFn {
	mtx.Lock()
	locked := true

	return func() bool {
		if locked {
			locked = false
			mtx.Unlock()
			return true
		}
		return false
	}
}

// RLockGuard locks the read-write mutex for reading and returns a function that unlocks it.
// The returned function must be called to release the lock.
// The returned function may be called multiple times - only the first call will unlock the mutex, others will be no-ops.
func RLockGuard(mtx RMtx) UnlockFn {
	mtx.RLock()
	locked := true

	return func() bool {
		if locked {
			locked = false
			mtx.RUnlock()
			return true
		}
		return false
	}
}
