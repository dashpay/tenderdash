package p2p

import (
	"math"
	"math/rand"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func randByte() byte {
	return byte(rand.Intn(math.MaxUint8))
}

func randLocalIPv4() net.IP {
	return net.IPv4(127, randByte(), randByte(), randByte())
}

// seqIPv4 returns a deterministic, distinct IPv4 address for the given index.
func seqIPv4(i int) net.IP {
	return net.IPv4(10, byte(i>>16), byte(i>>8), byte(i))
}

func TestConnTracker(t *testing.T) {
	for name, factory := range map[string]func() connectionTracker{
		"BaseSmall": func() connectionTracker {
			return newConnTracker(10, time.Second)
		},
		"BaseLarge": func() connectionTracker {
			return newConnTracker(100, time.Hour)
		},
	} {
		t.Run(name, func(t *testing.T) {
			factory := factory //nolint:scopelint
			t.Run("Initialized", func(t *testing.T) {
				ct := factory()
				require.Equal(t, 0, ct.Len())
			})
			t.Run("RepeatedAdding", func(t *testing.T) {
				ct := factory()
				ip := randLocalIPv4()
				require.NoError(t, ct.AddConn(ip))
				for i := 0; i < 100; i++ {
					_ = ct.AddConn(ip)
				}
				require.Equal(t, 1, ct.Len())
			})
			t.Run("AddingMany", func(t *testing.T) {
				ct := factory()
				for i := 0; i < 100; i++ {
					_ = ct.AddConn(randLocalIPv4())
				}
				require.Equal(t, 100, ct.Len())
			})
			t.Run("Cycle", func(t *testing.T) {
				ct := factory()
				for i := 0; i < 100; i++ {
					ip := randLocalIPv4()
					require.NoError(t, ct.AddConn(ip))
					ct.RemoveConn(ip)
				}
				require.Equal(t, 0, ct.Len())
			})
		})
	}
	t.Run("VeryShort", func(t *testing.T) {
		ct := newConnTracker(10, time.Microsecond)
		for i := 0; i < 10; i++ {
			ip := randLocalIPv4()
			require.NoError(t, ct.AddConn(ip))
			time.Sleep(2 * time.Microsecond)
			require.NoError(t, ct.AddConn(ip))
		}
		require.Equal(t, 10, ct.Len())
	})
	t.Run("Window", func(t *testing.T) {
		const window = 100 * time.Millisecond
		ct := newConnTracker(10, window)
		ip := randLocalIPv4()
		require.NoError(t, ct.AddConn(ip))
		ct.RemoveConn(ip)
		require.Error(t, ct.AddConn(ip))
		time.Sleep(window)
		require.NoError(t, ct.AddConn(ip))
	})

}

// lastConnectLen returns the number of tracked last-connect timestamps. It is a
// white-box helper: Len() reports active connections, not the recent-connect
// bookkeeping that this fix bounds.
func lastConnectLen(ct connectionTracker) int {
	impl := ct.(*connTrackerImpl)
	impl.mutex.RLock()
	defer impl.mutex.RUnlock()
	return len(impl.lastConnect)
}

func TestConnTrackerBounded(t *testing.T) {
	testCases := []struct {
		name string
		// churn opens then immediately closes each connection (short-lived),
		// which is the case that previously leaked one entry per distinct IP.
		churn bool
	}{
		{name: "churn of distinct IPs", churn: true},
		{name: "concurrent distinct IPs", churn: false},
	}

	for _, tc := range testCases {
		tc := tc //nolint:scopelint
		t.Run(tc.name, func(t *testing.T) {
			// A window far longer than the test guarantees no entry ages out on
			// time alone, so any bound comes from the activity-driven sweep.
			ct := newConnTracker(1, time.Hour)

			const total = maxTrackedAddresses + 5000
			for i := 0; i < total; i++ {
				ip := seqIPv4(i)
				require.NoError(t, ct.AddConn(ip))
				if tc.churn {
					ct.RemoveConn(ip)
				}
			}

			require.LessOrEqual(t, lastConnectLen(ct), maxTrackedAddresses,
				"lastConnect must stay within the size cap regardless of distinct-IP churn")
		})
	}
}

// TestConnKey verifies the connKey bucketing helper for IPv4 and IPv6.
func TestConnKey(t *testing.T) {
	// IPv4: keyed by full /32, same as ip.String().
	ip4a := net.ParseIP("10.0.0.1")
	ip4b := net.ParseIP("10.0.0.2")
	require.Equal(t, ip4a.String(), connKey(ip4a, 64), "IPv4 key must equal ip.String()")
	require.NotEqual(t, connKey(ip4a, 64), connKey(ip4b, 64), "distinct IPv4 addrs must have distinct keys")

	// IPv6: two /128 in the same /64 share a key.
	ip6a := net.ParseIP("2001:db8::1")
	ip6b := net.ParseIP("2001:db8::2")
	require.Equal(t, connKey(ip6a, 64), connKey(ip6b, 64),
		"/128 addrs in the same /64 must share a key")
	require.Equal(t, "2001:db8::/64", connKey(ip6a, 64),
		"IPv6 /64 key must be the masked prefix")

	// IPv6: two /64s produce distinct keys.
	ip6c := net.ParseIP("2001:db8:1::1")
	require.NotEqual(t, connKey(ip6a, 64), connKey(ip6c, 64),
		"addresses in different /64 subnets must not collide")

	// nil returns the sentinel.
	require.Equal(t, "<nil>", connKey(nil, 64))
}

// TestConnTrackerIPv6Bucketing verifies that the tracker collapses all /128
// addresses within the same /64 into one entry so an attacker cannot evade the
// per-address limit by rotating through fresh IPv6 addresses in their /64.
func TestConnTrackerIPv6Bucketing(t *testing.T) {
	// Same /64 — both addresses map to bucket "2001:db8::/64".
	ip6a := net.ParseIP("2001:db8::1")
	ip6b := net.ParseIP("2001:db8::2")

	t.Run("SameSlotSameMax", func(t *testing.T) {
		// max=1: the first /128 fills the single slot; the second /128 in the
		// same /64 must be rejected because the bucket is already at capacity.
		ct := newConnTracker(1, time.Hour)
		require.NoError(t, ct.AddConn(ip6a), "first /128 in /64 must be accepted")
		require.Error(t, ct.AddConn(ip6b),
			"second /128 in the same /64 must be rejected (bucket at max=1)")
		// Confirm only one key exists in the tracker.
		require.Equal(t, 1, ct.Len())
	})

	t.Run("SameRateLimitWindow", func(t *testing.T) {
		// Verify the rate-limit window is shared: adding ip6a, removing it, then
		// adding ip6b (a different /128, same /64) within the window is rejected.
		ct := newConnTracker(10, time.Hour)
		require.NoError(t, ct.AddConn(ip6a), "first add must succeed")
		ct.RemoveConn(ip6a)
		require.Error(t, ct.AddConn(ip6b),
			"reconnect from same /64 within window must be rejected even with a fresh /128")
	})

	t.Run("DifferentBucketsAreIndependent", func(t *testing.T) {
		// Two /128 addresses in DIFFERENT /64 subnets must not interfere.
		ip6c := net.ParseIP("2001:db8:1::1")
		ct := newConnTracker(1, time.Hour)
		require.NoError(t, ct.AddConn(ip6a), "first /64 bucket accepted")
		require.NoError(t, ct.AddConn(ip6c),
			"address in a distinct /64 must be accepted independently")
		require.Equal(t, 2, ct.Len())
	})

	t.Run("IPv4StillKeyedPerAddress", func(t *testing.T) {
		// IPv4 keys by full /32: two distinct IPv4 addresses are always independent.
		ip4a := net.ParseIP("192.168.1.1")
		ip4b := net.ParseIP("192.168.1.2")
		ct := newConnTracker(1, time.Hour)
		require.NoError(t, ct.AddConn(ip4a))
		require.NoError(t, ct.AddConn(ip4b),
			"distinct IPv4 /32 addresses must not share a bucket")
		require.Equal(t, 2, ct.Len())
	})
}

// trackedAddresses returns the set of addresses currently in lastConnect.
func trackedAddresses(ct connectionTracker) map[string]struct{} {
	impl := ct.(*connTrackerImpl)
	impl.mutex.RLock()
	defer impl.mutex.RUnlock()
	out := make(map[string]struct{}, len(impl.lastConnect))
	for addr := range impl.lastConnect {
		out[addr] = struct{}{}
	}
	return out
}

func TestConnTrackerSweepEvictsExpired(t *testing.T) {
	const window = 20 * time.Millisecond
	ct := newConnTracker(1, window)

	// Seed entries that will become stale.
	const stale = 200
	staleIPs := make([]net.IP, 0, stale)
	for i := 0; i < stale; i++ {
		ip := seqIPv4(i)
		staleIPs = append(staleIPs, ip)
		require.NoError(t, ct.AddConn(ip))
		ct.RemoveConn(ip)
	}
	require.Equal(t, stale, lastConnectLen(ct))

	time.Sleep(2 * window)

	// Drive a full sweep interval of AddConn calls so the amortized sweep runs.
	for i := stale; i < stale+sweepEveryN; i++ {
		require.NoError(t, ct.AddConn(seqIPv4(i)))
	}

	// Every stale entry (older than the window) must have been evicted.
	remaining := trackedAddresses(ct)
	for _, ip := range staleIPs {
		require.NotContains(t, remaining, ip.String(),
			"stale entry %s older than the window must be swept away", ip)
	}
}
