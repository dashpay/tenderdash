package client

import (
	"context"
	"errors"
	"math"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/conn"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

// TestRecvRateLimitHandler tests the rate limit middleware when receiving messages from peers.
// It tests that the rate limit is applied per peer.
//
// GIVEN 5 peers named 1..5 and rate limit of 2/s and burst 4,
// WHEN we send 1, 2, 3, 4 and 5 msgs per second respectively for 3 seconds,
// THEN:
// * peer 1 and 2 receive all messages,
// * other peers receive 2 messages per second plus 4 burst messages.
//
// Reuses testRateLimit from client_test.go
func TestRecvRateLimitHandler(t *testing.T) {
	// don't run this if we are in short mode
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	const (
		Limit           = 2.0
		Burst           = 4
		Peers           = 5
		TestTimeSeconds = 3
	)

	sent := make([]atomic.Uint32, Peers)

	fakeHandler := newMockConsumer(t)
	fakeHandler.On("Handle", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).
		Run(func(args mock.Arguments) {
			peerID := args.Get(2).(*p2p.Envelope).From
			peerNum, err := strconv.Atoi(string(peerID))
			require.NoError(t, err)
			sent[peerNum-1].Add(1)
		})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := log.NewTestingLogger(t)
	client := &Client{}

	mw := WithRecvRateLimitPerPeerHandler(ctx,
		Limit,
		func(*p2p.Envelope) uint { return 1 },
		false,
		logger,
	)(fakeHandler).(*recvRateLimitPerPeerHandler)

	mw.burst = Burst

	sendFn := func(peerID types.NodeID) error {
		envelope := p2p.Envelope{
			From:      peerID,
			ChannelID: testChannelID,
		}
		return mw.Handle(ctx, client, &envelope)
	}

	parallelSendWithLimit(t, ctx, sendFn, Peers, TestTimeSeconds)
	assertRateLimits(t, sent, Limit, Burst, TestTimeSeconds)
}

// TestSendRateLimit tests the rate limit for sending messages using p2p.client.
//
// Each peer should have his own, independent rate limit.
//
// GIVEN 5 peers named 1..5 and rate limit of 2/s and burst 4,
// WHEN we send 1, 2, 3, 4 and 5 msgs per second respectively for 3 seconds,
// THEN:
// * peer 1 and 2 receive all messages,
// * other peers receive 2 messages per second plus 4 burst messages.
func (suite *ChannelTestSuite) TestSendRateLimit() {
	if testing.Short() {
		suite.T().Skip("skipping test in short mode.")
	}

	const (
		Limit           = 2.0
		Burst           = 4
		Peers           = 5
		TestTimeSeconds = 3
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := suite.client

	limiter := NewRateLimit(ctx, Limit, false, suite.client.logger)
	limiter.burst = Burst
	suite.client.rateLimit = map[conn.ChannelID]*RateLimit{
		testChannelID: limiter,
	}

	sendFn := func(peerID types.NodeID) error {
		envelope := p2p.Envelope{
			To:        peerID,
			ChannelID: testChannelID,
		}
		return client.Send(ctx, envelope)

	}
	sent := make([]atomic.Uint32, Peers)

	suite.p2pChannel.On("Send", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			peerID := args.Get(1).(p2p.Envelope).To
			peerNum, err := strconv.Atoi(string(peerID))
			suite.NoError(err)
			sent[peerNum-1].Add(1)
		}).
		Return(nil)

	parallelSendWithLimit(suite.T(), ctx, sendFn, Peers, TestTimeSeconds)
	assertRateLimits(suite.T(), sent, Limit, Burst, TestTimeSeconds)
}

// parallelSendWithLimit sends messages to peers in parallel with a rate limit.
//
// The function sends messages to peers. Each peer gets its number, starting from 1.
// Rate limit is equal to the peer number, eg. peer 1 sends 1 msg/s, peeer 2 sends 2 msg/s etc.
func parallelSendWithLimit(t *testing.T, ctx context.Context, sendFn func(peerID types.NodeID) error,
	peers int, testTimeSeconds int) {
	t.Helper()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// all goroutines will wait for the start signal
	start := sync.RWMutex{}
	start.Lock()

	for peer := 1; peer <= peers; peer++ {
		peerID := types.NodeID(strconv.Itoa(peer))
		// peer number is the rate limit
		msgsPerSec := peer

		go func(peerID types.NodeID, rate int) {
			start.RLock()
			defer start.RUnlock()

			for s := 0; s < testTimeSeconds; s++ {
				until := time.NewTimer(time.Second)
				defer until.Stop()

				for i := 0; i < rate; i++ {
					select {
					case <-ctx.Done():
						return
					default:
					}

					if err := sendFn(peerID); !errors.Is(err, context.Canceled) {
						require.NoError(t, err)
					}
				}

				select {
				case <-until.C:
					// noop, we just sleep until the end of the second
				case <-ctx.Done():
					return
				}
			}

		}(peerID, msgsPerSec)
	}

	// start the test
	startTime := time.Now()
	start.Unlock()
	runtime.Gosched()
	time.Sleep(time.Duration(testTimeSeconds) * time.Second)
	cancel()
	// wait for all goroutines to finish, that is - drop RLocks
	start.Lock()
	defer start.Unlock()

	// check if test ran for the expected time
	// note we ignore up to 99 ms to account for any processing time
	elapsed := math.Floor(time.Since(startTime).Seconds()*10) / 10
	assert.Equal(t, float64(testTimeSeconds), elapsed, "test should run for %d seconds", testTimeSeconds)
}

// assertRateLimits checks if the rate limits were applied correctly
// We assume that index of each item in `sent` is the peer number, as described in parallelSendWithLimit.
func assertRateLimits(t *testing.T, sent []atomic.Uint32, limit float64, burst int, seconds int) {
	for peer := 1; peer <= len(sent); peer++ {
		expected := int(limit)*seconds + burst
		if expected > peer*seconds {
			expected = peer * seconds
		}

		assert.Equal(t, expected, int(sent[peer-1].Load()), "peer %d should receive %d messages", peer, expected)
	}
}
