package blocksync

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"github.com/benbjohnson/clock"
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
	fakeClock        *clock.Mock
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
	suite.fakeClock = clock.NewMock()
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
	runtime.Gosched()
	suite.fakeClock.Add(peerTimeout)
	suite.Require().NoError(err)
	_, err = p.Await()
	tmrequire.Error(suite.T(), errPeerNotResponded.Error(), err)
	err = suite.channel.Resolve(ctx, suite.receivedEnvelope)
	tmrequire.Error(suite.T(), "cannot resolve a result", err)
}

func (suite *ChannelTestSuite) TestSend() {

}
