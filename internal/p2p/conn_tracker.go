package p2p

import (
	"fmt"
	"net"
	"sort"
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

	type entry struct {
		address string
		last    time.Time
	}
	entries := make([]entry, 0, len(rat.lastConnect))
	for address, last := range rat.lastConnect {
		entries = append(entries, entry{address: address, last: last})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].last.Before(entries[j].last)
	})
	for _, e := range entries[:len(entries)-evictLowWater] {
		delete(rat.lastConnect, e.address)
	}
}
