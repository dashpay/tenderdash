package consensus

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/eventemitter"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// startWaitingValidator starts a one-validator chain that waits for transactions
// before every block after the first, and returns its state, the heartbeats it
// sends and its new blocks.
func startWaitingValidator(
	ctx context.Context,
	t *testing.T,
	consensusVersion tmproto.VersionParams_ConsensusVersion,
) (*State, <-chan *types.ProposerHeartbeat, <-chan *types.Block) {
	t.Helper()
	cs, heartbeats, blocks := newWaitingValidator(ctx, t, consensusVersion)
	stateData := cs.GetStateData()
	startTestRound(ctx, cs, stateData.Height, stateData.Round)
	return cs, heartbeats, blocks
}

// newWaitingValidator builds what startWaitingValidator starts.
func newWaitingValidator(
	ctx context.Context,
	t *testing.T,
	consensusVersion tmproto.VersionParams_ConsensusVersion,
) (*State, <-chan *types.ProposerHeartbeat, <-chan *types.Block) {
	t.Helper()
	baseConfig := configSetup(t)
	config, err := ResetConfig(t, "consensus_proposer_liveness_test")
	require.NoError(t, err)
	config.Consensus.CreateEmptyBlocks = false

	params := factory.ConsensusParams(func(c *types.ConsensusParams) {
		c.Version.ConsensusVersion = int32(consensusVersion)
		c.Timeout.ProposerHeartbeat = 50 * time.Millisecond
		c.Timeout.ProposerSilence = 250 * time.Millisecond
	})
	state, privVals := makeGenesisState(ctx, t, baseConfig, genesisStateArgs{
		Validators: 1,
		Power:      types.DefaultDashVotingPower,
		Params:     params,
	})
	cs := newStateWithConfig(ctx, t, log.NewNopLogger(), config, state, privVals[0], NewCounterApplication())
	assertMempool(t, cs.txNotifier).EnableTxsAvailable()

	heartbeats := make(chan *types.ProposerHeartbeat, 1000)
	cs.emitter.AddListener(proposerHeartbeatEventName, func(data eventemitter.EventData) error {
		heartbeats <- data.(*types.ProposerHeartbeat)
		return nil
	})
	blocks := make(chan *types.Block, 10)
	blockSub := subscribe(ctx, t, cs.eventBus, types.EventQueryNewBlock)
	go func() {
		for {
			select {
			case msg := <-blockSub:
				blocks <- msg.Data().(types.EventDataNewBlock).Block
			case <-ctx.Done():
				return
			}
		}
	}()

	return cs, heartbeats, blocks
}

func awaitBlock(t *testing.T, blocks <-chan *types.Block) *types.Block {
	t.Helper()
	select {
	case block := <-blocks:
		return block
	case <-time.After(10 * time.Second):
		t.Fatal("no block committed")
		return nil
	}
}

func TestStateProposerHeartbeats(t *testing.T) {
	t.Run("should announce itself while waiting for transactions and stop to propose", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cs, heartbeats, blocks := startWaitingValidator(ctx, t, tmproto.VersionParams_CONSENSUS_VERSION_2)

		first := awaitBlock(t, blocks)
		require.Equal(t, int64(1), first.Height)

		stateData := cs.GetStateData()
		proposer := stateData.Validators.Validators[0]
		var last uint64
		for i := 0; i < 3; i++ {
			select {
			case hb := <-heartbeats:
				assert.Equal(t, int64(2), hb.Height)
				assert.Equal(t, int32(0), hb.Round)
				assert.Greater(t, hb.Sequence, last)
				last = hb.Sequence
				assert.NoError(t, hb.Verify(stateData.state.ChainID, stateData.Validators.QuorumType,
					stateData.Validators.QuorumHash, proposer.PubKey))
			case <-time.After(5 * time.Second):
				t.Fatalf("no heartbeat %d while waiting at height 2", i+1)
			}
		}

		checkTxsRange(ctx, t, cs, 0, 1)
		second := awaitBlock(t, blocks)
		require.Equal(t, int64(2), second.Height)

		// Heartbeats now come for height 3, where the node waits again; none is
		// for height 2 once its block is in.
		deadline := time.After(5 * time.Second)
		for {
			select {
			case hb := <-heartbeats:
				if hb.Height == 3 {
					return
				}
				assert.Equal(t, int64(2), hb.Height)
			case <-deadline:
				t.Fatal("no heartbeat while waiting at height 3")
			}
		}
	})

	t.Run("should announce itself as soon as it starts in a waiting round", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cs, heartbeats, _ := newWaitingValidator(ctx, t, tmproto.VersionParams_CONSENSUS_VERSION_2)

		// As after a restart, the node is in a waiting round before its receive
		// routine starts, and nothing else will happen until a transaction
		// arrives.
		stateData := cs.GetStateData()
		stateData.updateRoundStep(stateData.Round, cstypes.RoundStepNewRound)
		require.NoError(t, stateData.Save())
		require.True(t, waitingRound(&stateData))
		startConsensusState(ctx, cs, 0)

		select {
		case hb := <-heartbeats:
			assert.Equal(t, stateData.Height, hb.Height)
		case <-time.After(5 * time.Second):
			t.Fatal("no heartbeat from a node that started in a waiting round")
		}
	})

	t.Run("should send no heartbeats before consensus version 2", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_, heartbeats, blocks := startWaitingValidator(ctx, t, tmproto.VersionParams_CONSENSUS_VERSION_1)

		awaitBlock(t, blocks)
		select {
		case hb := <-heartbeats:
			t.Fatalf("heartbeat %v sent on a version 1 chain", hb)
		case <-time.After(500 * time.Millisecond):
		}
	})
}
