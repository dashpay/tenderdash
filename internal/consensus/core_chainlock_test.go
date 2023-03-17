package consensus

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"testing"
	"time"

	sync "github.com/sasha-s/go-deadlock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tendermint/tendermint/abci/example/counter"
	abci "github.com/tendermint/tendermint/abci/types"
	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	"github.com/tendermint/tendermint/internal/eventbus"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/types"
)

func TestValidProposalChainLocks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const nVals = 4
	var initChainLockHeight uint32 = 2
	states := genConsStates(ctx, t, nVals, initChainLockHeight, 1)
	rts := setupReactor(ctx, t, nVals, states, 100)

	for i := 0; i < 3; i++ {
		timeoutWaitGroup(t, rts.subs, states, func(sub eventbus.Subscription) {
			msg, err := sub.Next(ctx)
			require.NoError(t, err)
			block := msg.Data().(types.EventDataNewBlock).Block
			// this is true just because of this test where each new height has a new chain lock that is incremented by 1
			state := states[0].GetStateData().state
			assert.EqualValues(t, initChainLockHeight+uint32(i), block.Header.CoreChainLockedHeight) //nolint:scopelint
			assert.EqualValues(t, state.InitialHeight+int64(i), block.Header.Height)                 //nolint:scopelint
		})
	}
}

// one byz val sends a proposal for a height 1 less than it should, but then sends the correct block after it
func TestReactorInvalidProposalHeightForChainLocks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const nVals = 4
	var initChainLockHeight uint32 = 2
	states := genConsStates(ctx, t, nVals, initChainLockHeight, 1)

	// this proposer sends a chain lock at each height
	byzProposerID := 0
	byzProposer := states[byzProposerID]

	// update the decide proposal to propose the incorrect height
	enterProposeWithInvalidProposalDecider(byzProposer)

	rts := setupReactor(ctx, t, nVals, states, 100)

	for i := 0; i < 3; i++ {
		timeoutWaitGroup(t, rts.subs, states, func(sub eventbus.Subscription) {
			msg, err := sub.Next(ctx)
			require.NoError(t, err)
			block := msg.Data().(types.EventDataNewBlock).Block
			// this is true just because of this test where each new height has a new chain lock that is incremented by 1
			state := states[0].GetStateData().state
			assert.EqualValues(t, initChainLockHeight+uint32(i), block.Header.CoreChainLockedHeight) //nolint:scopelint
			assert.EqualValues(t, state.InitialHeight+int64(i), block.Header.Height)                 //nolint:scopelint
		})
	}
}

func TestReactorInvalidBlockChainLock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const nVals = 4
	states := genConsStates(ctx, t, nVals, 100, -1)
	rts := setupReactor(ctx, t, nVals, states, 100)

	for i := 0; i < 10; i++ {
		timeoutWaitGroup(t, rts.subs, states, func(sub eventbus.Subscription) {
			msg, err := sub.Next(ctx)
			require.NoError(t, err)
			block := msg.Data().(types.EventDataNewBlock).Block
			expected := 100

			// We started at 1 then 99, then try 98, 97, 96...
			// The chain lock should stay on 99
			assert.EqualValues(t, expected, block.Header.CoreChainLockedHeight)
		})
	}
}

func newCounterWithCoreChainLocks(initCoreChainHeight uint32, step int32) func(logger log.Logger, _ string) abci.Application {
	return func(logger log.Logger, _ string) abci.Application {
		counterApp := counter.NewApplication(true)
		counterApp.InitCoreChainLock(initCoreChainHeight, step)
		return counterApp
	}
}

func setupReactor(ctx context.Context, t *testing.T, n int, states []*State, size int) *reactorTestSuite {
	t.Helper()
	rts := setup(ctx, t, n, states, size)
	rts.switchToConsensus(ctx)
	return rts
}

func enterProposeWithInvalidProposalDecider(state *State) {
	action := state.ctrl.Get(EnterProposeType)
	enterProposeAction := action.(*EnterProposeAction)
	propler := enterProposeAction.proposalCreator.(*Proposaler)
	invalidDecider := &invalidProposalDecider{Proposaler: propler}
	enterProposeAction.proposalCreator = invalidDecider
}

type invalidProposalDecider struct {
	*Proposaler
}

func (p *invalidProposalDecider) Create(ctx context.Context, height int64, round int32, rs *cstypes.RoundState) error {
	// routine to:
	// - precommit for a random block
	// - send precommit to all peers
	// - disable privValidator (so we don't do normal precommits)

	// Create on block
	block, blockParts := rs.ValidBlock, rs.ValidBlockParts
	if block == nil {
		var err error
		// Create a new proposal block from state/txs from the mempool.
		block, blockParts, err = p.createProposalBlock(ctx, round, rs)
		if err != nil {
			return err
		}
	}
	// Make proposal
	propBlockID := types.BlockID{Hash: block.Hash(), PartSetHeader: blockParts.Header()}
	// It is byzantine because it is not updating the LastCoreChainLockedBlockHeight
	proposal := types.NewProposal(height, p.committedState.LastCoreChainLockedBlockHeight, round, rs.ValidRound, propBlockID, block.Header.Time)
	err := p.signProposal(ctx, height, proposal)
	if err != nil {
		return err
	}
	// send proposal and block parts on internal msg queue
	p.sendMessages(ctx, &ProposalMessage{proposal})
	p.sendMessages(ctx, blockPartsToMessages(rs.Height, rs.Round, blockParts)...)
	return nil
}

func timeoutWaitGroup(
	t *testing.T,
	subs map[types.NodeID]eventbus.Subscription,
	states []*State,
	f func(eventbus.Subscription),
) {
	var wg sync.WaitGroup
	wg.Add(len(subs))

	for _, sub := range subs {
		go func(sub eventbus.Subscription) {
			f(sub)
			wg.Done()
		}(sub)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	// we're running many nodes in-process, possibly in a virtual machine,
	// and spewing debug messages - making a block could take a while,
	timeout := time.Second * 20

	select {
	case <-done:
	case <-time.After(timeout):
		for i, state := range states {
			t.Log("#################")
			t.Log("Validator", i)
			t.Log(state.GetRoundState())
			t.Log("")
		}
		os.Stdout.Write([]byte("pprof.Lookup('goroutine'):\n"))
		err := pprof.Lookup("goroutine").WriteTo(os.Stdout, 1)
		require.NoError(t, err)
		capture()
		t.Fatal("Timed out waiting for all validators to commit a block")
	}
}

func capture() {
	trace := make([]byte, 10240000)
	count := runtime.Stack(trace, true)
	fmt.Printf("Stack of %d bytes: %s\n", count, trace)
}

func genConsStates(ctx context.Context, t *testing.T, nVals int, initCoreChainHeight uint32, step int32) []*State {
	conf := configSetup(t)
	gen := consensusNetGen{
		cfg:     conf,
		nVals:   nVals,
		appFunc: newCounterWithCoreChainLocks(initCoreChainHeight, step),
	}
	states, _, _, _ := gen.generate(ctx, t)
	return states
}
