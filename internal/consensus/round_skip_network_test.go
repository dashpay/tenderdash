package consensus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/internal/eventbus"
	"github.com/dashpay/tenderdash/internal/mempool"
	"github.com/dashpay/tenderdash/internal/test/factory"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// Validators connected through their reactors hand on the round of a proposer
// that goes silent while they wait for transactions. Each notices the silence
// on its own, their skip votes reach each other through the state channel, and
// every node, the silent proposer included, enters the next round on the
// certificate. That round waits for transactions in turn, under a proposer
// whose heartbeats arrive, and a transaction then gets the same block
// committed on every node.
func TestReactorHandsASilentProposersRoundOn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	const nodes = 4
	params := factory.ConsensusParams(func(c *types.ConsensusParams) {
		c.Version.ConsensusVersion = int32(tmproto.VersionParams_CONSENSUS_VERSION_2)
		c.Timeout.Vote = 50 * time.Millisecond
		c.Timeout.VoteDelta = 5 * time.Millisecond
		c.Timeout.ProposerHeartbeat = 50 * time.Millisecond
		c.Timeout.ProposerSilence = 300 * time.Millisecond
	})
	states := makeConsensusStateWithParams(ctx, t, nodes, "consensus_round_skip_network_test", newTickerFunc(), params,
		func(c *config.Config) { c.Consensus.CreateEmptyBlocks = false })
	// Only the proposer of a round signs heartbeats for it, so whichever
	// validator proposes round 0 of height 2 goes silent there.
	for _, state := range states {
		state.SetPrivValidator(ctx, &roundSilentSigner{PrivValidator: state.privValidator.PrivValidator, height: 2, round: 0})
	}
	rts := setup(ctx, t, nodes, states, 100)
	rts.switchToConsensus(ctx)

	// Height 1, the chain's first, does not wait for transactions.
	for _, sub := range rts.subs {
		require.Equal(t, int64(1), nextBlock(ctx, t, sub).Height)
	}

	require.Eventually(t, func() bool {
		for _, state := range states {
			stateData := state.GetStateData()
			if stateData.Height != 2 || stateData.RoundSkip == nil || stateData.Round != stateData.RoundSkip.Round+1 {
				return false
			}
		}
		return true
	}, 30*time.Second, 20*time.Millisecond, "the silent proposer's round was not handed on")
	for _, state := range states {
		assert.Equal(t, int32(0), state.GetStateData().RoundSkip.Round, "only the silent round is skipped")
	}

	tx := []byte{1, 2, 3}
	for _, state := range states {
		require.NoError(t, assertMempool(t, state.txNotifier).CheckTx(ctx, tx, nil, mempool.TxInfo{}))
	}
	var hash []byte
	for _, sub := range rts.subs {
		block := nextBlock(ctx, t, sub)
		require.Equal(t, int64(2), block.Height)
		if hash == nil {
			hash = block.Hash()
			continue
		}
		assert.Equal(t, hash, []byte(block.Hash()), "the nodes committed different blocks")
	}
}

// nextBlock returns the block of the next new-block event on sub.
func nextBlock(ctx context.Context, t *testing.T, sub eventbus.Subscription) *types.Block {
	t.Helper()
	msg, err := sub.Next(ctx)
	require.NoError(t, err)
	return msg.Data().(types.EventDataNewBlock).Block
}

// roundSilentSigner is a validator's signer that signs no proposer heartbeat
// for one round, as a proposer that goes silent in it would.
type roundSilentSigner struct {
	types.PrivValidator
	height int64
	round  int32
}

func (s *roundSilentSigner) SignProposerHeartbeat(
	ctx context.Context,
	chainID string,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
	heartbeat *tmproto.ProposerHeartbeat,
) error {
	if heartbeat.Height == s.height && heartbeat.Round == s.round {
		return errors.New("silent in this round")
	}
	return s.PrivValidator.SignProposerHeartbeat(ctx, chainID, quorumType, quorumHash, heartbeat)
}
