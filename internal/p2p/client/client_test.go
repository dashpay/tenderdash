package client

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/cosmos/gogoproto/proto"
	"github.com/google/uuid"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/mocks"
	tmrequire "github.com/dashpay/tenderdash/internal/test/require"
	bcproto "github.com/dashpay/tenderdash/proto/tendermint/blocksync"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

const testChannelID = 0x1

type ChannelTestSuite struct {
	suite.Suite

	height      int64
	peerID      types.NodeID
	fakeClock   *clockwork.FakeClock
	p2pChannel  *mocks.Channel
	client      *Client
	response    *bcproto.BlockResponse
	descriptors map[p2p.ChannelID]*p2p.ChannelDescriptor
}

func TestChannelTestSuite(t *testing.T) {
	suite.Run(t, new(ChannelTestSuite))
}

func (suite *ChannelTestSuite) SetupSuite() {
	suite.descriptors = map[p2p.ChannelID]*p2p.ChannelDescriptor{
		p2p.ErrorChannel: {ID: p2p.ErrorChannel, Name: "error"},
		testChannelID:    {ID: testChannelID, Name: "test"},
	}
}

func (suite *ChannelTestSuite) SetupTest() {
	suite.p2pChannel = mocks.NewChannel(suite.T())
	suite.height = 101
	suite.peerID = "peer id"
	suite.fakeClock = clockwork.NewFakeClock()
	suite.client = New(
		suite.descriptors,
		func(_ctx context.Context, _descriptor *p2p.ChannelDescriptor) (p2p.Channel, error) {
			return suite.p2pChannel, nil
		},
		WithClock(suite.fakeClock),
		WithChanIDResolver(func(_msg proto.Message) p2p.ChannelID {
			return testChannelID
		}),
	)
	suite.response = &bcproto.BlockResponse{
		Commit: &tmproto.Commit{Height: suite.height},
	}
}

func (suite *ChannelTestSuite) TestGetBlockSuccess() {
	ctx := context.Background()
	var reqID string
	envelopeArg := func(envelope p2p.Envelope) bool {
		var ok bool
		reqID, ok = envelope.Attributes[RequestIDAttribute]
		return ok
	}
	suite.p2pChannel.
		On("Send", mock.Anything, mock.MatchedBy(envelopeArg)).
		Once().
		Return(nil)
	p, err := suite.client.GetBlock(ctx, suite.height, suite.peerID)
	suite.Require().NoError(err)
	// this call should start a goroutine that was created in a promise that a result of GetBlock method
	runtime.Gosched()
	envelope := newEnvelope(uuid.NewString(), suite.peerID, suite.response)
	envelope.AddAttribute(ResponseIDAttribute, reqID)
	err = suite.client.resolve(ctx, envelope)
	suite.Require().NoError(err)
	resp, err := p.Await()
	suite.Require().NoError(err)
	suite.Require().Equal(suite.height, resp.Commit.Height)
}

func (suite *ChannelTestSuite) TestGetBlockFailedSend() {
	ctx := context.Background()
	err := errors.New("failed send")
	suite.p2pChannel.
		On("Send", mock.Anything, mock.Anything).
		Once().
		Return(err)
	suite.p2pChannel.
		On("SendError", mock.Anything, p2p.PeerError{NodeID: suite.peerID, Err: err}).
		Once().
		Return(err)
	_, err = suite.client.GetBlock(ctx, suite.height, suite.peerID)
	suite.Require().Error(err)
	tmrequire.Error(suite.T(), "failed send", err)
}

func (suite *ChannelTestSuite) TestGetBlockTimeout() {
	ctx := context.Background()
	var reqID string
	envelopeArg := func(envelope p2p.Envelope) bool {
		var ok bool
		reqID, ok = envelope.Attributes[RequestIDAttribute]
		return ok
	}
	suite.p2pChannel.
		On("Send", mock.Anything, mock.MatchedBy(envelopeArg)).
		Once().
		Return(nil)
	p, err := suite.client.GetBlock(ctx, suite.height, suite.peerID)
	// need to wait for the goroutine is started
	time.Sleep(time.Millisecond)
	suite.fakeClock.Advance(peerTimeout)
	suite.Require().NoError(err)
	_, err = p.Await()
	// A timeout rejects the promise and leaves the peer alone; deciding whether
	// the peer is at fault belongs to the caller. The channel mock has no
	// SendError expectation, so reporting the peer here would fail this test.
	tmrequire.Error(suite.T(), ErrPeerNotResponded.Error(), err)
	err = suite.client.resolve(ctx, newEnvelope(reqID, suite.peerID, suite.response))
	tmrequire.Error(suite.T(), "cannot resolve a result", err)
}

// A resolver loads a pending channel from the map and may be descheduled before
// it sends. If retiring the request closed that channel, the resolver's send
// would panic and take the whole process down, because the resolve path runs
// outside the consumer's panic recovery. This reproduces that window with the
// deschedule made explicit.
func (suite *ChannelTestSuite) TestRemovePendingKeepsChannelSendable() {
	reqID := uuid.NewString()
	respCh := suite.client.addPending(reqID)
	suite.client.removePending(reqID)
	suite.Require().NotPanics(func() {
		respCh <- result{Value: suite.response}
	})
}

// A peer that answers a request we have already given up on is slow, not
// dishonest. resolve must report no error for it, because iter turns any error
// from resolve into a PeerError that can evict the peer outright - bypassing the
// caller's own failure-counting policy.
func (suite *ChannelTestSuite) TestLateResponseAfterTimeoutIsNotAnError() {
	ctx := context.Background()
	var reqID string
	envelopeArg := func(envelope p2p.Envelope) bool {
		var ok bool
		reqID, ok = envelope.Attributes[RequestIDAttribute]
		return ok
	}
	suite.p2pChannel.
		On("Send", mock.Anything, mock.MatchedBy(envelopeArg)).
		Once().
		Return(nil)
	p, err := suite.client.GetBlock(ctx, suite.height, suite.peerID)
	suite.Require().NoError(err)
	// wait for the promise goroutine to arm its timeout before moving the clock
	suite.Require().NoError(suite.fakeClock.BlockUntilContext(ctx, 1))
	suite.fakeClock.Advance(peerTimeout)
	_, err = p.Await()
	tmrequire.Error(suite.T(), ErrPeerNotResponded.Error(), err)

	envelope := newEnvelope(uuid.NewString(), suite.peerID, suite.response)
	envelope.AddAttribute(ResponseIDAttribute, reqID)
	suite.Require().NoError(suite.client.resolve(ctx, envelope))
}

// Silencing late responses must not silence fabricated ones: a response quoting
// an ID we never issued is unsolicited traffic and stays attributable.
func (suite *ChannelTestSuite) TestUnsolicitedResponseIDIsAnError() {
	ctx := context.Background()
	neverIssued := uuid.NewString()
	envelope := newEnvelope(uuid.NewString(), suite.peerID, suite.response)
	envelope.AddAttribute(ResponseIDAttribute, neverIssued)
	err := suite.client.resolve(ctx, envelope)
	suite.Require().Error(err)
	suite.Require().Contains(err.Error(), neverIssued)
}

// Retired IDs are remembered so late responses can be recognized, but the memory
// has to be a window rather than a log: retention must track how many requests
// are in flight, not how many have ever been issued.
func (suite *ChannelTestSuite) TestRetiredRequestIDsAreBounded() {
	const requests = 100
	for i := 0; i < requests; i++ {
		reqID := uuid.NewString()
		suite.client.addPending(reqID)
		suite.client.removePending(reqID)
	}
	suite.Require().Equal(requests, suite.pendingLen())

	suite.fakeClock.Advance(2*peerTimeout + time.Second)
	suite.client.addPending(uuid.NewString())
	suite.Require().Equal(1, suite.pendingLen())
	// The expiry index must drain with the map, or it becomes the leak instead.
	suite.Require().Empty(suite.client.tombstones)
}

// A second response for a request whose promise has already settled must not
// park the resolver. The one-slot buffer still holds the first response and
// nothing will ever drain it, so a blocking send never returns: the consumer
// goroutine that carries every blocksync message would stop dead until the node
// shuts down.
func (suite *ChannelTestSuite) TestDuplicateResponseDoesNotBlockResolver() {
	ctx := context.Background()
	reqID := uuid.NewString()
	suite.client.addPending(reqID)
	// Fill the buffer. With no promise waiting, this response is never read.
	suite.Require().NoError(suite.client.resolveMessage(ctx, reqID, result{Value: suite.response}))

	done := make(chan error, 1)
	go func() {
		done <- suite.client.resolveMessage(ctx, reqID, result{Value: suite.response})
	}()
	select {
	case err := <-done:
		suite.Require().NoError(err)
	case <-time.After(2 * time.Second):
		suite.Require().Fail("resolveMessage blocked on a response buffer nobody reads")
	}
}

func (suite *ChannelTestSuite) TestGetSyncStatus() {
	ctx := context.Background()
	envelopeArg := func(envelope p2p.Envelope) bool {
		_, ok := envelope.Attributes[RequestIDAttribute]
		_, isStatusRequest := envelope.Message.(*bcproto.StatusRequest)
		return ok && isStatusRequest && envelope.Broadcast
	}
	suite.p2pChannel.
		On("Send", mock.Anything, mock.MatchedBy(envelopeArg)).
		Once().
		Return(nil)
	err := suite.client.GetSyncStatus(ctx)
	suite.Require().NoError(err)
}

func (suite *ChannelTestSuite) TestSend() {
	ctx := context.Background()
	errMsg := p2p.PeerError{}
	envelope := p2p.Envelope{}
	envelopeArg := func(envelope p2p.Envelope) bool {
		var ok bool
		_, ok = envelope.Attributes[RequestIDAttribute]
		return ok
	}
	suite.p2pChannel.
		On("Send", mock.Anything, mock.MatchedBy(envelopeArg)).
		Once().
		Return(nil)
	suite.p2pChannel.
		On("SendError", mock.Anything, errMsg).
		Once().
		Return(nil)
	err := suite.client.Send(ctx, envelope)
	suite.Require().NoError(err)
	err = suite.client.Send(ctx, errMsg)
	suite.Require().NoError(err)
}

func (suite *ChannelTestSuite) TestConsumeHandle() {
	ctx := context.Background()
	outCh := make(chan p2p.Envelope)
	go func() {
		for i := 0; i < 3; i++ {
			outCh <- p2p.Envelope{}
		}
		close(outCh)
	}()
	suite.p2pChannel.
		On("Receive", ctx).
		Once().
		Return(func(_ctx context.Context) p2p.ChannelIterator {
			return p2p.NewChannelIterator(outCh)
		})
	consumer := newMockConsumer(suite.T())
	consumer.
		On("Handle", ctx, mock.Anything, mock.Anything).
		Times(3).
		Return(nil)
	err := suite.client.Consume(ctx, ConsumerParams{
		ReadChannels: []p2p.ChannelID{testChannelID},
		Handler:      consumer,
	})
	suite.Require().NoError(err)
}

func (suite *ChannelTestSuite) TestConsumeResolve() {
	ctx := context.Background()
	reqID := uuid.NewString()
	testCases := []struct {
		resp proto.Message
	}{
		{
			resp: &bcproto.BlockResponse{},
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("%d", i), func() {
			outCh := make(chan p2p.Envelope)
			go func() {
				defer close(outCh)
				outCh <- p2p.Envelope{
					Attributes: map[string]string{
						ResponseIDAttribute: reqID,
					},
					Message: &bcproto.BlockResponse{},
				}
			}()
			consumer := newMockConsumer(suite.T())
			suite.p2pChannel.
				On("Receive", ctx).
				Once().
				Return(func(_ctx context.Context) p2p.ChannelIterator {
					return p2p.NewChannelIterator(outCh)
				})
			resCh := suite.client.addPending(reqID)
			err := suite.client.Consume(ctx, ConsumerParams{
				ReadChannels: []p2p.ChannelID{testChannelID},
				Handler:      consumer,
			})
			suite.Require().NoError(err)
			res := <-resCh
			resp := res.Value.(*bcproto.BlockResponse)
			suite.Require().Equal(tc.resp, resp)
		})
	}
}

func (suite *ChannelTestSuite) TestConsumeError() {
	ctx := context.Background()
	msg := p2p.Envelope{
		From: "peer",
	}
	handlerErr := errors.New("consumer handler error")
	testCases := []struct {
		mockFn func()
		retErr error
	}{
		{
			retErr: context.Canceled,
		},
		{
			retErr: context.DeadlineExceeded,
		},
		{
			retErr: errors.New("consumer handler error"),
			mockFn: func() {
				suite.p2pChannel.
					On("SendError", ctx, p2p.PeerError{NodeID: msg.From, Err: handlerErr}).
					Once().
					Return(nil)
			},
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("%d", i), func() {
			if tc.mockFn != nil {
				tc.mockFn()
			}
			outCh := make(chan p2p.Envelope, 1)
			outCh <- msg
			suite.p2pChannel.
				On("Receive", ctx).
				Once().
				Return(func(_ctx context.Context) p2p.ChannelIterator {
					return p2p.NewChannelIterator(outCh)
				})
			consumer := newMockConsumer(suite.T())
			consumer.
				On("Handle", ctx, mock.Anything, mock.Anything).
				Once().
				Return(func(_ context.Context, _ *Client, _ *p2p.Envelope) error {
					close(outCh)
					return tc.retErr
				})
			err := suite.client.Consume(ctx, ConsumerParams{
				ReadChannels: []p2p.ChannelID{testChannelID},
				Handler:      consumer,
			})
			suite.Require().NoError(err)
		})
	}
}

type mockConsumer struct {
	mock.Mock
}

func newMockConsumer(t *testing.T) *mockConsumer {
	m := &mockConsumer{}
	m.Mock.Test(t)
	t.Cleanup(func() { m.AssertExpectations(t) })
	return m
}

func (m *mockConsumer) Handle(ctx context.Context, client *Client, envelope *p2p.Envelope) error {
	ret := m.Called(ctx, client, envelope)
	var r0 error
	if rf, ok := ret.Get(0).(func(ctx context.Context, channel *Client, envelope *p2p.Envelope) error); ok {
		r0 = rf(ctx, client, envelope)
	} else {
		r0 = ret.Error(0)
	}
	return r0
}

func newEnvelope(reqID string, peerID types.NodeID, resp *bcproto.BlockResponse) *p2p.Envelope {
	return &p2p.Envelope{
		Attributes: map[string]string{
			RequestIDAttribute: reqID,
		},
		From:    peerID,
		Message: resp,
	}
}

func (suite *ChannelTestSuite) pendingLen() int {
	n := 0
	suite.client.pending.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
}
