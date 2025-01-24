package client

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/gogo/protobuf/proto"
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
	suite.p2pChannel.
		On("SendError", mock.Anything, mock.Anything).
		Once().
		Return(nil)
	p, err := suite.client.GetBlock(ctx, suite.height, suite.peerID)
	// need to wait for the goroutine is started
	time.Sleep(time.Millisecond)
	suite.fakeClock.Advance(peerTimeout)
	suite.Require().NoError(err)
	_, err = p.Await()
	tmrequire.Error(suite.T(), ErrPeerNotResponded.Error(), err)
	err = suite.client.resolve(ctx, newEnvelope(reqID, suite.peerID, suite.response))
	tmrequire.Error(suite.T(), "cannot resolve a result", err)
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
