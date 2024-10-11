package p2p

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
)

func TestConnectionFiltering(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := log.NewNopLogger()

	filterByIPCount := 0
	router := &Router{
		logger:      logger,
		connTracker: newConnTracker(1, time.Second),
		options: RouterOptions{
			FilterPeerByIP: func(_ context.Context, _ net.IP, _ uint16) error {
				filterByIPCount++
				return errors.New("mock")
			},
		},
	}
	require.Equal(t, 0, filterByIPCount)
	router.openConnection(ctx, &MemoryConnection{logger: logger, closeFn: func() {}})
	require.Equal(t, 1, filterByIPCount)
}
