package mockcoreserver

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	dashcore "github.com/dashpay/tenderdash/dash/core"
	"github.com/dashpay/tenderdash/libs/log"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	"github.com/dashpay/tenderdash/privval"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// countingCoreServer counts the `quorum info` calls it answers.
type countingCoreServer struct {
	*MockCoreServer
	quorumInfoCalls atomic.Int32
}

func (c *countingCoreServer) QuorumInfo(ctx context.Context, cmd btcjson.QuorumCmd) btcjson.QuorumInfoResult {
	c.quorumInfoCalls.Add(1)
	return c.MockCoreServer.QuorumInfo(ctx, cmd)
}

// The Dash Core signer is the one evonodes use, so its proposer liveness
// signatures are checked end to end: signed through `quorum platformsign` and
// verified with the validator's key share, which it looks up once per quorum.
func TestDashCoreSignerSignsProposerLivenessMessages(t *testing.T) {
	const (
		addr       = "localhost:19997"
		chainID    = "dash-core-signer-liveness"
		quorumType = btcjson.LLMQType_5_60
	)
	ctx := context.Background()
	quorumHash := crypto.RandQuorumHash()
	privKey := bls12381.GenPrivKey()
	filePV := privval.GenFilePV("", "")
	filePV.UpdatePrivateKey(ctx, privKey, quorumHash, privKey.PubKey(), 1000)
	proTxHash, err := filePV.GetProTxHash(ctx)
	require.NoError(t, err)

	cs := &countingCoreServer{MockCoreServer: &MockCoreServer{LLMQType: quorumType, FilePV: filePV}}
	srv := WithMethods(
		NewJRPCServer(addr, "/"),
		WithQuorumInfoMethod(cs, Endless),
		WithQuorumSignMethod(cs, Endless, quorumType),
		WithMasternodeMethod(cs, Endless),
		WithGetNetworkInfoMethod(cs, Endless),
	)
	go srv.Start()
	defer srv.Stop(ctx)

	logger := log.NewTestingLogger(t)
	rpcClient, err := dashcore.NewRPCClient(addr, "root", "root", logger)
	require.NoError(t, err)
	client, err := privval.NewDashCoreSignerClient(rpcClient, quorumType, logger)
	require.NoError(t, err)

	pbHeartbeat := &tmproto.ProposerHeartbeat{
		Height:            7,
		Round:             1,
		Sequence:          3,
		ProposerProTxHash: proTxHash,
		Timestamp:         tmtime.Now(),
	}
	require.NoError(t, client.SignProposerHeartbeat(ctx, chainID, quorumType, quorumHash, pbHeartbeat))
	heartbeat, err := types.ProposerHeartbeatFromProto(pbHeartbeat)
	require.NoError(t, err)
	assert.NoError(t, heartbeat.Verify(chainID, quorumType, quorumHash, privKey.PubKey()))

	pbVote := &tmproto.RoundSkipVote{Height: 7, Round: 1, ValidatorProTxHash: proTxHash}
	require.NoError(t, client.SignRoundSkipVote(ctx, chainID, quorumType, quorumHash, pbVote))
	vote, err := types.RoundSkipVoteFromProto(pbVote)
	require.NoError(t, err)
	assert.NoError(t, vote.Verify(chainID, quorumType, quorumHash, privKey.PubKey()))

	assert.Equal(t, int32(1), cs.quorumInfoCalls.Load(), "the key share is looked up once per quorum")
}
