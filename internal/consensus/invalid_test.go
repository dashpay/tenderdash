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
	byzState := rts.states[node.NodeID]
	byzReactor := rts.reactors[node.NodeID]

	signal := make(chan struct{})
	// Update the doPrevote function to just send a valid precommit for a random
	// block and otherwise disable the priv validator.
	privVal := byzState.privValidator
	doPrevoteCmd := newMockAction(func(ctx context.Context, stateEvent StateEvent) error {
		stateData := stateEvent.StateData
		defer close(signal)
		invalidDoPrevoteFunc(ctx, t, stateData, byzState, byzReactor, rts.voteChannels[node.NodeID], privVal)
		return nil
	})
	byzState.ctrl.Register(DoPrevoteType, doPrevoteCmd)

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

func invalidDoPrevoteFunc(
	ctx context.Context,
	t *testing.T,
	stateData *StateData,
	cs *State,
	r *Reactor,
	voteCh p2p.Channel,
	pv types.PrivValidator,
) {
	// routine to:
	// - precommit for a random block
	// - send precommit to all peers
	// - disable privValidator (so we don't do normal precommits)

	var err error
	cs.privValidator.PrivValidator = pv
	err = cs.privValidator.init(ctx)
	require.NoError(t, err)

	valIndex, _ := stateData.Validators.GetByProTxHash(cs.privValidator.ProTxHash)

	// precommit a random block
	blockHash := bytes.HexBytes(tmrand.Bytes(32))
	precommit := &types.Vote{
		ValidatorProTxHash: cs.privValidator.ProTxHash,
		ValidatorIndex:     valIndex,
		Height:             stateData.Height,
		Round:              stateData.Round,
		Type:               tmproto.PrecommitType,
		BlockID: types.BlockID{
			Hash:          blockHash,
			PartSetHeader: types.PartSetHeader{Total: 1, Hash: tmrand.Bytes(32)},
			StateID:       types.RandStateID().Hash(),
		},
	}

	p := precommit.ToProto()
	err = cs.privValidator.SignVote(
		ctx,
		stateData.state.ChainID,
		stateData.Validators.QuorumType,
		stateData.Validators.QuorumHash,
		p,
		log.NewNopLogger(),
	)
	require.NoError(t, err)

	precommit.BlockSignature = p.BlockSignature
	cs.privValidator = privValidator{} // disable priv val so we don't do normal votes

	ids := make([]types.NodeID, 0, len(r.peers))
	for _, ps := range r.peers {
		ids = append(ids, ps.peerID)
	}

	count := 0
	for _, peerID := range ids {
		count++
		err := voteCh.Send(ctx, p2p.Envelope{
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
		require.NoError(t, err)
	}
}
