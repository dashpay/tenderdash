package p2p

import (
	"container/heap"
	"fmt"
	"net"
	"time"

	sync "github.com/sasha-s/go-deadlock"
)

const (
	// maxTrackedAddresses caps how many recent-connect timestamps are retained.
	// A full map of /16-style string keys is a few MB; reaching the cap forces a
	// sweep so growth can never exceed this bound across windows.
	maxTrackedAddresses = 65536
	// evictLowWater is the target size after a cap-triggered eviction; the
	// headroom below the cap keeps the costly oldest-first eviction rare under a
	// sustained distinct-address flood.
	evictLowWater = maxTrackedAddresses * 7 / 8
	// sweepEveryN is how many AddConn calls trigger an amortized sweep of
	// expired entries, keeping the per-call cost O(1) amortized.
	sweepEveryN = 1024
)

type connectionTracker interface {
	AddConn(net.IP) error
	RemoveConn(net.IP)
	Len() int
}

type connTrackerImpl struct {
	cache       map[string]uint
	lastConnect map[string]time.Time
	mutex       sync.RWMutex
	max         uint
	window      time.Duration
	addCount    uint
}

func newConnTracker(max uint, window time.Duration) connectionTracker {
	return &connTrackerImpl{
		cache:       make(map[string]uint),
		lastConnect: make(map[string]time.Time),
		max:         max,
		window:      window,
	}
}

func (rat *connTrackerImpl) Len() int {
	rat.mutex.RLock()
	defer rat.mutex.RUnlock()
	return len(rat.cache)
}

func (rat *connTrackerImpl) AddConn(addr net.IP) error {
	address := addr.String()
	rat.mutex.Lock()
	defer rat.mutex.Unlock()

	if num := rat.cache[address]; num >= rat.max {
		return fmt.Errorf("%q has %d connections [max=%d]", address, num, rat.max)
	} else if num == 0 {
		// if there is already at least one connection, check to
		// see if it was established before within the window,
		// and error if so.
		if last := rat.lastConnect[address]; time.Since(last) < rat.window {
			return fmt.Errorf("%q tried to connect within window of last %s", address, rat.window)
		}
	}

	rat.cache[address]++
	rat.lastConnect[address] = time.Now()

	rat.addCount++
	if rat.addCount%sweepEveryN == 0 || len(rat.lastConnect) > maxTrackedAddresses {
		rat.sweepExpired()
	}

	return nil
}

func (rat *connTrackerImpl) RemoveConn(addr net.IP) {
	address := addr.String()
	rat.mutex.Lock()
	defer rat.mutex.Unlock()

	if num := rat.cache[address]; num > 0 {
		rat.cache[address]--
	}
	if num := rat.cache[address]; num <= 0 {
		delete(rat.cache, address)
	}

	// Drop the recent-connect timestamp only once it is past the rate-limit
	// window; while still inside the window it must be retained so a reconnect
	// from the same address is rejected.
	if last, ok := rat.lastConnect[address]; ok && time.Since(last) >= rat.window {
		delete(rat.lastConnect, address)
	}
}

// sweepExpired bounds lastConnect. It first drops entries older than the
// rate-limit window, which no longer affect AddConn's window check, so removing
// them is pure bookkeeping cleanup. If a flood of distinct addresses within a
// single window still leaves the map above maxTrackedAddresses, the oldest
// entries are evicted down to evictLowWater; this trades early rate-limit
// expiry for the evicted (least recently seen) addresses against a hard memory
// bound. Evicting below the cap leaves headroom so the costly path runs rarely
// rather than on every subsequent insert. The caller must hold the write lock.
func (rat *connTrackerImpl) sweepExpired() {
	for address, last := range rat.lastConnect {
		if time.Since(last) >= rat.window {
			delete(rat.lastConnect, address)
		}
	}

	if len(rat.lastConnect) <= maxTrackedAddresses {
		return
	}

	// Partial selection: find the k oldest entries using a max-heap of size k.
	// O(N log k) time, O(k) space — the heap root is always the newest among
	// the k oldest candidates, so replacing it whenever a still-older entry is
	// found collects the exact k oldest entries without sorting all N.
	k := len(rat.lastConnect) - evictLowWater
	h := make(evictHeap, 0, k)
	for address, last := range rat.lastConnect {
		if h.Len() < k {
			heap.Push(&h, evictEntry{last: last, address: address})
		} else if last.Before(h[0].last) {
			h[0] = evictEntry{last: last, address: address}
			heap.Fix(&h, 0)
		}
	}
	for i := range h {
		delete(rat.lastConnect, h[i].address)
	}
}

// evictEntry pairs a connection-tracker key with its most-recent connect time,
// used by the cap-triggered eviction path in sweepExpired.
type evictEntry struct {
	last    time.Time
	address string
}

// evictHeap is a max-heap ordered by last time (newest timestamp at root).
// Maintaining the k oldest entries: when the heap is full, replacing the root
// (newest-of-oldest) with a still-older entry ensures the heap always holds
// the k globally oldest entries after a full scan.
type evictHeap []evictEntry

func (h evictHeap) Len() int            { return len(h) }
func (h evictHeap) Less(i, j int) bool  { return h[i].last.After(h[j].last) }
func (h evictHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *evictHeap) Push(x interface{}) { *h = append(*h, x.(evictEntry)) }
func (h *evictHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
