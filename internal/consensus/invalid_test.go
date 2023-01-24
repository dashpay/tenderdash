package consensus

import (
	"context"
	"errors"
	"testing"
	"time"

	sync "github.com/sasha-s/go-deadlock"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tendermint/tendermint/internal/eventbus"
	"github.com/tendermint/tendermint/internal/p2p"
	"github.com/tendermint/tendermint/libs/bytes"
	"github.com/tendermint/tendermint/libs/log"
	tmrand "github.com/tendermint/tendermint/libs/rand"
	tmcons "github.com/tendermint/tendermint/proto/tendermint/consensus"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/types"
)

func TestReactorInvalidPrecommit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	config := configSetup(t)

	const n = 2
	states := makeConsensusState(ctx, t,
		config, n, "consensus_reactor_test",
		func() TimeoutTicker {
			return NewTimeoutTicker(log.NewNopLogger())
		})

	rts := setup(ctx, t, n, states, 100) // buffer must be large enough to not deadlock

	// this val sends a random precommit at each height
	node := rts.network.RandomNode()

	signal := make(chan struct{})
	// Update the doPrevote function to just send a valid precommit for a random
	// block and otherwise disable the priv validator.
	withInvalidPrevoter(t, rts, node.NodeID, signal)

	rts.switchToConsensus(ctx)

	// wait for a bunch of blocks
	//
	// TODO: Make this tighter by ensuring the halt happens by block 2.
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		for _, sub := range rts.subs {
			wg.Add(1)

			go func(s eventbus.Subscription) {
				defer wg.Done()
				_, err := s.Next(ctx)
				if ctx.Err() != nil {
					return
				}
				if !assert.NoError(t, err) {
					cancel() // cancel other subscribers on failure
				}
			}(sub)
		}
	}
	wait := make(chan struct{})
	go func() { defer close(wait); wg.Wait() }()

	select {
	case <-wait:
		if _, ok := <-signal; !ok {
			t.Fatal("test condition did not fire")
		}
	case <-ctx.Done():
		if _, ok := <-signal; !ok {
			t.Fatal("test condition did not fire after timeout")
			return
		}
	case <-signal:
		// test passed
	}
}

type invalidPrevoter struct {
	t        *testing.T
	stopCh   chan struct{}
	state    *State
	voteCh   p2p.Channel
	privVal  privValidator
	prevoter Prevoter
	peers    map[types.NodeID]*PeerState
}

func withInvalidPrevoter(t *testing.T, rts *reactorTestSuite, nodeID types.NodeID, stopCh chan struct{}) {
	reactor := rts.reactors[nodeID]
	bzState := rts.states[nodeID]
	proTxHash := rts.network.Nodes[nodeID].NodeInfo.ProTxHash
	privVal := bzState.privValidator
	privVal.ProTxHash = proTxHash

	voteCh := rts.voteChannels[nodeID]
	cmd := bzState.fsm.Get(EnterPrevoteType)
	enterPrevoteCmd := cmd.(*EnterPrevoteCommand)
	enterPrevoteCmd.prevoter = &invalidPrevoter{
		t:        t,
		stopCh:   stopCh,
		state:    bzState,
		voteCh:   voteCh,
		privVal:  privVal,
		peers:    reactor.peers,
		prevoter: enterPrevoteCmd.prevoter,
	}
}

func (p *invalidPrevoter) Do(ctx context.Context, stateData *StateData) error {
	// routine to:
	// - precommit for a random block
	// - send precommit to all peers
	// - disable privValidator (so we don't do normal precommits)
	defer func() {
		defer close(p.stopCh)
	}()
	valIndex, _ := stateData.Validators.GetByProTxHash(p.privVal.ProTxHash)
	// precommit a random block
	precommit := newFakePrecommit(stateData.Height, stateData.Round, p.privVal.ProTxHash, valIndex)
	protoVote := precommit.ToProto()
	err := p.privVal.SignVote(
		ctx,
		stateData.state.ChainID,
		stateData.Validators.QuorumType,
		stateData.Validators.QuorumHash,
		protoVote,
		log.NewNopLogger(),
	)
	require.NoError(p.t, err)

	precommit.BlockSignature = protoVote.BlockSignature
	p.privVal = privValidator{} // disable priv val so we don't do normal votes

	ids := make([]types.NodeID, 0, len(p.peers))
	for _, ps := range p.peers {
		ids = append(ids, ps.peerID)
	}

	count := 0
	for _, peerID := range ids {
		count++
		err = p.voteCh.Send(ctx, p2p.Envelope{
			To: peerID,
			Message: &tmcons.Vote{
				Vote: precommit.ToProto(),
			},
		})
		// we want to have sent some of these votes,
		// but if the test completes without erroring
		// or not sending any messages, then we should
		// error.
		if errors.Is(err, context.Canceled) && count > 0 {
			break
		}
		require.NoError(p.t, err)
	}
	return nil
}

func newFakePrecommit(height int64, round int32, proTxHash types.ProTxHash, valIndex int32) *types.Vote {
	blockHash := bytes.HexBytes(tmrand.Bytes(32))
	return &types.Vote{
		ValidatorProTxHash: proTxHash,
		ValidatorIndex:     valIndex,
		Height:             height,
		Round:              round,
		Type:               tmproto.PrecommitType,
		BlockID: types.BlockID{
			Hash:          blockHash,
			PartSetHeader: types.PartSetHeader{Total: 1, Hash: tmrand.Bytes(32)},
			StateID:       types.RandStateID().Hash(),
		},
	}
}
