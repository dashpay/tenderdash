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
