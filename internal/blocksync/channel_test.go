package blocksync

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	"github.com/tendermint/tendermint/internal/p2p"
	"github.com/tendermint/tendermint/internal/p2p/mocks"
	tmrequire "github.com/tendermint/tendermint/internal/test/require"
	bcproto "github.com/tendermint/tendermint/proto/tendermint/blocksync"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/types"
)

type ChannelTestSuite struct {
	suite.Suite

	height           int64
	peerID           types.NodeID
	fakeClock        clockwork.FakeClock
	p2pChannel       *mocks.Channel
	channel          *Channel
	receivedEnvelope *p2p.Envelope
}

func TestChannelTestSuite(t *testing.T) {
	suite.Run(t, new(ChannelTestSuite))
}

func (suite *ChannelTestSuite) SetupTest() {
	suite.p2pChannel = mocks.NewChannel(suite.T())
	suite.height = 101
	suite.peerID = "peer id"
	suite.fakeClock = clockwork.NewFakeClock()
	suite.channel = NewChannel(suite.p2pChannel, ChannelWithClock(suite.fakeClock))
	suite.receivedEnvelope = &p2p.Envelope{
		From: suite.peerID,
		Message: &bcproto.BlockResponse{
			Commit: &tmproto.Commit{Height: suite.height},
		},
	}
}

func (suite *ChannelTestSuite) TearDownTest() {
	ctx := context.Background()
	// try to resolve again
	err := suite.channel.Resolve(ctx, suite.receivedEnvelope)
	tmrequire.Error(suite.T(), "cannot resolve a result", err)
}

func (suite *ChannelTestSuite) TestGetBlockSuccess() {
	ctx := context.Background()
	suite.p2pChannel.
		On("Send", mock.Anything, mock.Anything).
		Once().
		Return(nil)
	p, err := suite.channel.GetBlock(ctx, suite.height, suite.peerID)
	suite.Require().NoError(err)
	runtime.Gosched()
	err = suite.channel.Resolve(ctx, suite.receivedEnvelope)
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
	_, err = suite.channel.GetBlock(ctx, suite.height, suite.peerID)
	suite.Require().Error(err)
	tmrequire.Error(suite.T(), "failed send", err)
}

func (suite *ChannelTestSuite) TestGetBlockTimeout() {
	ctx := context.Background()
	suite.p2pChannel.
		On("Send", mock.Anything, mock.Anything).
		Once().
		Return(nil)
	suite.p2pChannel.
		On("SendError", mock.Anything, mock.Anything).
		Once().
		Return(nil)
	p, err := suite.channel.GetBlock(ctx, suite.height, suite.peerID)
	// need to wait for the goroutine is started
	time.Sleep(time.Millisecond)
	suite.fakeClock.Advance(peerTimeout)
	suite.Require().NoError(err)
	_, err = p.Await()
	tmrequire.Error(suite.T(), errPeerNotResponded.Error(), err)
	err = suite.channel.Resolve(ctx, suite.receivedEnvelope)
	tmrequire.Error(suite.T(), "cannot resolve a result", err)
}

func (suite *ChannelTestSuite) TestSend() {
	ctx := context.Background()
	errMsg := p2p.PeerError{}
	msg := p2p.Envelope{}
	suite.p2pChannel.
		On("Send", ctx, msg).
		Once().
		Return(nil)
	suite.p2pChannel.
		On("SendError", ctx, errMsg).
		Once().
		Return(nil)
	err := suite.channel.Send(ctx, msg)
	suite.Require().NoError(err)
	err = suite.channel.Send(ctx, errMsg)
	suite.Require().NoError(err)
}

func (suite *ChannelTestSuite) TestConsume() {
	ctx := context.Background()
	outCh := make(chan p2p.Envelope, 3)
	go func() {
		for i := 0; i < 3; i++ {
			outCh <- p2p.Envelope{}
		}
		close(outCh)
	}()
	suite.p2pChannel.
		On("Receive", ctx).
		Once().
		Return(func(ctx context.Context) *p2p.ChannelIterator {
			return p2p.NewChannelIterator(outCh)
		})
	consumer := newMockConsumer(suite.T())
	consumer.
		On("Handle", ctx, mock.Anything, mock.Anything).
		Times(3).
		Return(nil)
	suite.channel.Consume(ctx, consumer)
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

func (m *mockConsumer) Handle(ctx context.Context, channel *Channel, envelope *p2p.Envelope) error {
	ret := m.Called(ctx, channel, envelope)
	var r0 error
	if rf, ok := ret.Get(0).(func(ctx context.Context, channel *Channel, envelope *p2p.Envelope) error); ok {
		r0 = rf(ctx, channel, envelope)
	} else {
		r0 = ret.Error(0)
	}
	return r0
}
