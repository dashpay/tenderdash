package p2p

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// connStaysOpen dials addr and reports whether the connection remains open.
// An over-limit connection is closed by the listener right after Accept, so a
// read returns a non-timeout error (EOF/reset) quickly; a connection within the
// limit has no data written to it, so the read blocks until the short deadline.
func connStaysOpen(t *testing.T, addr net.Addr) (net.Conn, bool) {
	t.Helper()
	c, err := net.Dial(addr.Network(), addr.String())
	require.NoError(t, err)

	require.NoError(t, c.SetReadDeadline(time.Now().Add(150*time.Millisecond)))
	_, err = c.Read(make([]byte, 1))

	if nerr, ok := err.(net.Error); ok && nerr.Timeout() {
		_ = c.SetReadDeadline(time.Time{})
		return c, true
	}
	return c, false
}

func TestLimitListener(t *testing.T) {
	testCases := []struct {
		name string
		max  int
	}{
		{name: "limit one", max: 1},
		{name: "limit two", max: 2},
	}

	for _, tc := range testCases {
		tc := tc //nolint:scopelint
		t.Run(tc.name, func(t *testing.T) {
			base, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			ln := newLimitListener(base, tc.max)
			defer ln.Close()

			acceptCh := make(chan net.Conn, tc.max+2)
			go func() {
				for {
					c, err := ln.Accept()
					if err != nil {
						return
					}
					acceptCh <- c
				}
			}()

			// Fill every slot; these connections stay open and are handed up.
			// Track both client and accepted (server-side) ends — the slot is
			// released by closing the accepted end.
			clients := make([]net.Conn, 0, tc.max)
			accepted := make([]net.Conn, 0, tc.max)
			for i := 0; i < tc.max; i++ {
				c, ok := connStaysOpen(t, base.Addr())
				require.True(t, ok, "connection %d within the limit must stay open", i)
				clients = append(clients, c)
				accepted = append(accepted, <-acceptCh)
			}

			// One more connection is over the limit: it must be closed promptly,
			// not parked, and never handed up.
			over, ok := connStaysOpen(t, base.Addr())
			require.False(t, ok, "over-limit connection must be closed, not parked")
			over.Close()
			select {
			case <-acceptCh:
				require.Fail(t, "over-limit connection must not be handed up")
			case <-time.After(150 * time.Millisecond):
			}

			// Releasing a slot (closing an accepted conn) makes it reusable.
			require.NoError(t, accepted[0].Close())
			clients[0].Close()

			var reused net.Conn
			require.Eventually(t, func() bool {
				c, ok := connStaysOpen(t, base.Addr())
				if !ok {
					c.Close()
					return false
				}
				reused = c
				return true
			}, 2*time.Second, 50*time.Millisecond, "a released slot must be reusable")
			<-acceptCh
			reused.Close()

			for _, c := range clients[1:] {
				c.Close()
			}
			for _, c := range accepted[1:] {
				c.Close()
			}
		})
	}
}

// TestLimitListenerReleaseOnce verifies that closing an accepted connection more
// than once releases its slot exactly once, so the slot accounting cannot drift
// and create a phantom extra slot.
func TestLimitListenerReleaseOnce(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ln := newLimitListener(base, 1)
	defer ln.Close()

	acceptCh := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			acceptCh <- c
		}
	}()

	c1, ok := connStaysOpen(t, base.Addr())
	require.True(t, ok)
	c1.Close()
	accepted := <-acceptCh

	// Double close: the second Close returns an error from the underlying conn,
	// but the slot must be released exactly once.
	require.NoError(t, accepted.Close())
	_ = accepted.Close()

	// Exactly one slot exists, so after the release one new connection may
	// occupy it; a second concurrent one is over the limit.
	var c2 net.Conn
	require.Eventually(t, func() bool {
		c, ok := connStaysOpen(t, base.Addr())
		if !ok {
			c.Close()
			return false
		}
		c2 = c
		return true
	}, 2*time.Second, 50*time.Millisecond, "the single released slot must accept one connection")
	defer c2.Close()
	<-acceptCh

	c3, ok := connStaysOpen(t, base.Addr())
	require.False(t, ok, "a double release must not create a phantom extra slot")
	c3.Close()
}
