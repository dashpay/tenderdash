package client

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/p2p"
	tmrequire "github.com/dashpay/tenderdash/internal/test/require"
	"github.com/dashpay/tenderdash/libs/log"
	bcproto "github.com/dashpay/tenderdash/proto/tendermint/blocksync"
	"github.com/dashpay/tenderdash/types"
)

func TestErrorLoggerP2PMessageHandler(t *testing.T) {
	ctx := context.Background()
	reqID := uuid.NewString()
	fakeHandler := newMockConsumer(t)
	logger := log.NewTestingLogger(t)
	testCases := []struct {
		mockFn  func(hd *mockConsumer, logger *log.TestingLogger)
		wantErr string
	}{
		{
			mockFn: func(hd *mockConsumer, logger *log.TestingLogger) {
				logger.AssertMatch(regexp.MustCompile("failed to handle a message from a p2p client"))
				hd.On("Handle", mock.Anything, mock.Anything, mock.Anything).
					Once().
					Return(errors.New("error"))
			},
			wantErr: "error",
		},
		{
			mockFn: func(hd *mockConsumer, _logger *log.TestingLogger) {
				hd.On("Handle", mock.Anything, mock.Anything, mock.Anything).
					Once().
					Return(nil)
			},
		},
	}
	client := &Client{}
	envelope := &p2p.Envelope{
		Attributes: map[string]string{RequestIDAttribute: reqID},
	}
	for i, tc := range testCases {
		mw := &errorLoggerP2PMessageHandler{logger: logger, next: fakeHandler}
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			tc.mockFn(fakeHandler, logger)
			err := mw.Handle(ctx, client, envelope)
			tmrequire.Error(t, tc.wantErr, err)
		})
	}
}

func TestRecoveryP2PMessageHandler(t *testing.T) {
	ctx := context.Background()
	fakeHandler := newMockConsumer(t)
	testCases := []struct {
		mockFn  func(fakeHandler *mockConsumer)
		wantErr string
	}{
		{
			mockFn: func(fakeHandler *mockConsumer) {
				fakeHandler.
					On("Handle", mock.Anything, mock.Anything, mock.Anything).
					Once().
					Panic("panic")
			},
			wantErr: "panic in processing message",
		},
		{
			mockFn: func(fakeHandler *mockConsumer) {
				fakeHandler.
					On("Handle", mock.Anything, mock.Anything, mock.Anything).
					Once().
					Return(nil)
			},
		},
	}
	logger := log.NewTestingLogger(t)
	mw := recoveryP2PMessageHandler{logger: logger, next: fakeHandler}
	client := &Client{}
	envelope := &p2p.Envelope{}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			tc.mockFn(fakeHandler)
			err := mw.Handle(ctx, client, envelope)
			tmrequire.Error(t, tc.wantErr, err)
		})
	}
}

func TestValidateMessageHandler(t *testing.T) {
	ctx := context.Background()
	testCases := []struct {
		envelope p2p.Envelope
		wantErr  string
	}{
		{
			envelope: p2p.Envelope{ChannelID: 0},
			wantErr:  "unknown channel ID",
		},
		{
			envelope: p2p.Envelope{ChannelID: 1},
			wantErr:  ErrRequestIDAttributeRequired.Error(),
		},
		{
			envelope: p2p.Envelope{
				ChannelID: 1,
				Attributes: map[string]string{
					RequestIDAttribute: uuid.NewString(),
				},
				Message: &bcproto.BlockResponse{},
			},
			wantErr: ErrResponseIDAttributeRequired.Error(),
		},
		{
			envelope: p2p.Envelope{
				ChannelID: 1,
				Attributes: map[string]string{
					RequestIDAttribute: uuid.NewString(),
				},
			},
		},
	}
	fakeHandler := newMockConsumer(t)
	fakeHandler.
		On("Handle", mock.Anything, mock.Anything, mock.Anything).
		Maybe().
		Return(nil)
	mw := validateMessageHandler{allowedChannelIDs: map[p2p.ChannelID]struct{}{
		0x1: {},
	}, next: fakeHandler}
	client := &Client{}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			err := mw.Handle(ctx, client, &tc.envelope)
			tmrequire.Error(t, tc.wantErr, err)
		})
	}
}

// TestRateLimitHandler tests the rate limit middleware.
//
// GIVEN 5 peers named 1..5 and rate limit of 2/s and burst 4,
// WHEN we send 1, 2, 3, 4 and 5 msgs per second respectively for 3 seconds,
// THEN:
// * peer 1 and 2 receive all messages,
// * other peers receive 2 messages per second plus 4 burst messages.
func TestRateLimitHandler(t *testing.T) {
	const (
		Peers           = 5
		RateLimit       = 2.0
		Burst           = 4
		TestTimeSeconds = 3
	)

	// don't run this if we are in short mode
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	fakeHandler := newMockConsumer(t)

	// we cancel manually to control race conditions
	ctx, cancel := context.WithCancel(context.Background())

	logger := log.NewTestingLogger(t)
	client := &Client{}

	mw := WithRecvRateLimitPerPeerHandler(RateLimit, func(*p2p.Envelope) uint { return 1 }, false, logger)(fakeHandler).(*recvRateLimitPerPeerHandler)
	mw.burst = Burst

	start := sync.RWMutex{}
	start.Lock()

	sent := make([]atomic.Uint32, Peers)

	for peer := 1; peer <= Peers; peer++ {
		counter := &sent[peer-1]
		peerID := types.NodeID(strconv.Itoa(peer))
		fakeHandler.On("Handle", mock.Anything, mock.Anything, mock.MatchedBy(
			func(e *p2p.Envelope) bool {
				return e.From == peerID
			},
		)).Return(nil).Run(func(_args mock.Arguments) {
			counter.Add(1)
		})

		go func(peerID types.NodeID, rate int) {
			start.RLock()
			defer start.RUnlock()

			for {
				until := time.NewTimer(time.Second)
				defer until.Stop()

				for i := 0; i < rate; i++ {
					select {
					case <-ctx.Done():
						return
					default:
					}

					envelope := &p2p.Envelope{
						From: peerID,
					}

					err := mw.Handle(ctx, client, envelope)
					require.NoError(t, err)
				}

				select {
				case <-until.C:
					// noop, we just sleep
				case <-ctx.Done():
					return
				}
			}
		}(peerID, peer)
	}

	// start the test
	startTime := time.Now()
	start.Unlock()
	time.Sleep(TestTimeSeconds * time.Second)
	cancel()
	// wait for all goroutines to finish, that is - drop RLocks
	start.Lock()

	// Check assertions

	// we floor with 1 decimal place, as elapsed will be slightly more than TestTimeSeconds
	elapsed := math.Floor(time.Since(startTime).Seconds()*10) / 10
	require.Equal(t, float64(TestTimeSeconds), elapsed, "test should run for %d seconds", TestTimeSeconds)

	for peer := 1; peer <= Peers; peer++ {
		expected := int(RateLimit)*TestTimeSeconds + Burst
		if expected > peer*TestTimeSeconds {
			expected = peer * TestTimeSeconds
		}

		assert.Equal(t, expected, int(sent[peer-1].Load()), "peer %d should receive %d messages", peer, expected)
	}
	// require.Equal(t, uint32(1*TestTimeSeconds), sent[0].Load(), "peer 0 should receive 1 message per second")
	// require.Equal(t, uint32(2*TestTimeSeconds), sent[1].Load(), "peer 1 should receive 2 messages per second")
	// require.Equal(t, uint32(2*TestTimeSeconds+Burst), sent[2].Load(), "peer 2 should receive 2 messages per second")
}
