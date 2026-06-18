package p2p

import (
	"net"

	sync "github.com/sasha-s/go-deadlock"
)

// limitListener is modeled on golang.org/x/net/netutil.LimitListener, but
// instead of parking (blocking) connections that exceed the limit until a slot
// frees, it closes them immediately. This keeps over-limit inbound connections
// from pinning kernel sockets and file descriptors.

// newLimitListener returns a net.Listener that accepts at most n simultaneous
// connections. Connections beyond n are closed right after they are accepted
// rather than held open, and the slot is released when the connection is closed.
// n <= 0 means unlimited: the original listener is returned unchanged.
func newLimitListener(l net.Listener, n int) net.Listener {
	if n <= 0 {
		return l
	}
	return &limitListener{
		Listener: l,
		sem:      make(chan struct{}, n),
	}
}

type limitListener struct {
	net.Listener
	sem chan struct{}
}

// tryAcquire takes a slot without blocking, returning false if none is free.
func (l *limitListener) tryAcquire() bool {
	select {
	case l.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (l *limitListener) release() { <-l.sem }

// Accept accepts the next connection. When the simultaneous-connection limit is
// reached, the accepted connection is closed immediately and Accept moves on to
// the next one, so callers never observe an over-limit connection.
func (l *limitListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if !l.tryAcquire() {
			_ = c.Close()
			continue
		}
		return &limitListenerConn{Conn: c, release: l.release}, nil
	}
}

type limitListenerConn struct {
	net.Conn
	releaseOnce sync.Once
	release     func()
}

func (l *limitListenerConn) Close() error {
	err := l.Conn.Close()
	l.releaseOnce.Do(l.release)
	return err
}
