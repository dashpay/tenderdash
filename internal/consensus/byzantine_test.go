package consensus

import (
	"context"
	"fmt"
	"path"
	"testing"
	"time"

	sync "github.com/sasha-s/go-deadlock"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	"github.com/dashpay/tenderdash/abci/example/kvstore"
	abci "github.com/dashpay/tenderdash/abci/types"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/internal/eventbus"
	"github.com/dashpay/tenderdash/internal/evidence"
	"github.com/dashpay/tenderdash/internal/mempool"
	"github.com/dashpay/tenderdash/internal/p2p"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/store"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/log"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// Byzantine node sends two different prevotes (nil and blockID) to the same
// validator.
func TestByzantinePrevoteEquivocation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	// empirically, this test either passes in <1s or hits some
	// kind of deadlock and hit the larger timeout. This timeout
	// can be extended a bunch if needed, but it's good to avoid
	// falling back to a much coarser timeout
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	nValidators := 4
	prevoteHeight := int64(2)
	testName := "consensus_byzantine_test"
	tickerFunc := newMockTickerFunc(true)

	genDoc, privVals := factory.RandGenesisDoc(nValidators, factory.ConsensusParams())
	states := make([]*State, nValidators)

	for i := 0; i < nValidators; i++ {
		func() {
			logger := consensusLogger(t).With("test", "byzantine", "validator", i)
			stateDB := dbm.NewMemDB() // each state needs its own db
			stateStore := sm.NewStore(stateDB)
			state, err := sm.MakeGenesisState(genDoc)
			require.NoError(t, err)
			require.NoError(t, stateStore.Save(state))

			thisConfig, err := ResetConfig(t, fmt.Sprintf("%s_%d", testName, i))
			require.NoError(t, err)

			ensureDir(t, path.Dir(thisConfig.Consensus.WalFile()), 0700) // dir for wal
			app, err := kvstore.NewMemoryApp()
			require.NoError(t, err)
			vals := types.TM2PB.ValidatorUpdates(state.Validators)
			_, err = app.InitChain(ctx, &abci.RequestInitChain{ValidatorSet: &vals})
			require.NoError(t, err)

			blockDB := dbm.NewMemDB()
			blockStore := store.NewBlockStore(blockDB)

			// one for mempool, one for consensus
			proxyAppConnMem := abciclient.NewLocalClient(logger, app)
			proxyAppConnCon := abciclient.NewLocalClient(logger, app)

			// Make Mempool
			mp := mempool.NewTxMempool(
				log.NewNopLogger().With("module", "mempool"),
				thisConfig.Mempool,
				proxyAppConnMem,
			)
			if thisConfig.Consensus.WaitForTxs() {
				mp.EnableTxsAvailable()
			}

			eventBus := eventbus.NewDefault(log.NewNopLogger().With("module", "events"))
			require.NoError(t, eventBus.Start(ctx))

			// Make a full instance of the evidence pool
			evidenceDB := dbm.NewMemDB()
			evpool := evidence.NewPool(logger.With("module", "evidence"), evidenceDB, stateStore, blockStore, evidence.NopMetrics(), eventBus)

			// Make State
			blockExec := sm.NewBlockExecutor(stateStore, proxyAppConnCon, mp, evpool, blockStore, eventBus)
			cs, err := NewState(logger, thisConfig.Consensus, stateStore, blockExec, blockStore, mp, evpool, eventBus, WithTimeoutTicker(tickerFunc()))
			require.NoError(t, err)
			// set private validator
			pv := privVals[i]
			cs.SetPrivValidator(ctx, pv)

			states[i] = cs
		}()
	}

	rts := setup(ctx, t, nValidators, states, 512) // buffer must be large enough to not deadlock

	var bzNodeID types.NodeID

	// Set the first state's reactor as the dedicated byzantine reactor and grab
	// the NodeID that corresponds to the state so we can reference the reactor.
	bzNodeState := states[0]
	for nID, s := range rts.states {
		if s == bzNodeState {
			bzNodeID = nID
			break
		}
	}

	withBzPrevoter(t, rts, bzNodeID, prevoteHeight)
	// alter prevote so that the byzantine node double votes when height is 2

	// Introducing a lazy proposer means that the time of the block committed is
	// different to the timestamp that the other nodes have. This tests to ensure
	// that the evidence that finally gets proposed will have a valid timestamp.
	// lazyProposer := states[1]
	lazyNodeState := states[1]

	enterProposeWithBzProposalDecider(lazyNodeState)

	rts.switchToConsensus(ctx)

	// Evidence should be submitted and committed at the third height but
	// we will check the first six just in case
	evidenceFromEachValidator := make([]types.Evidence, nValidators)

	var wg sync.WaitGroup
	i := 0
	subctx, subcancel := context.WithCancel(ctx)
	defer subcancel()
	for _, sub := range rts.subs {
		wg.Add(1)

		go func(j int, s eventbus.Subscription) {
			defer wg.Done()
			for {
				if subctx.Err() != nil {
					return
				}

				msg, err := s.Next(subctx)
				if subctx.Err() != nil {
					return
				}

				if err != nil {
					t.Errorf("waiting for subscription: %v", err)
					subcancel()
					return
				}

				require.NotNil(t, msg)
				block := msg.Data().(types.EventDataNewBlock).Block
				if len(block.Evidence) != 0 {
					evidenceFromEachValidator[j] = block.Evidence[0]
					return
				}
			}
		}(i, sub)
		i++
	}

	wg.Wait()

	proTxHash, err := bzNodeState.privValidator.GetProTxHash(ctx)
	require.NoError(t, err)

	// don't run more assertions if we've encountered a timeout
	select {
	case <-subctx.Done():
		t.Fatal("encountered timeout")
	default:
	}

	for idx, ev := range evidenceFromEachValidator {
		require.NotNil(t, ev, idx)
		ev, ok := ev.(*types.DuplicateVoteEvidence)
		require.True(t, ok)
		assert.Equal(t, proTxHash, ev.VoteA.ValidatorProTxHash)
		assert.Equal(t, prevoteHeight, ev.Height())
	}
}

type byzantinePrevoter struct {
	t             *testing.T
	logger        log.Logger
	voteSigner    *voteSigner
	blockExec     *sm.BlockExecutor
	prevoteHeight int64
	voteCh        p2p.Channel
	peers         map[types.NodeID]*PeerState
	origin        Prevoter
}

func withBzPrevoter(t *testing.T, rts *reactorTestSuite, bzNodeID types.NodeID, height int64) {
	reactor := rts.reactors[bzNodeID]
	bzState := rts.states[bzNodeID]
	voteCh := rts.voteChannels[bzNodeID]
	cmd := bzState.ctrl.Get(EnterPrevoteType)
	enterPrevoteCmd := cmd.(*EnterPrevoteAction)
	enterPrevoteCmd.prevoter = &byzantinePrevoter{
		t:             t,
		logger:        bzState.logger,
		voteSigner:    bzState.voteSigner,
		blockExec:     bzState.blockExec,
		prevoteHeight: height,
		voteCh:        voteCh,
		peers:         reactor.peers,
		origin:        enterPrevoteCmd.prevoter,
	}
}

func (p *byzantinePrevoter) Do(ctx context.Context, stateData *StateData) error {
	// allow first height to happen normally so that byzantine validator is no longer proposer
	uncommittedState, err := p.blockExec.ProcessProposal(
		ctx,
		stateData.ProposalBlock,
		stateData.Round,
		stateData.state,
		true,
	)
	require.NoError(p.t, err)
	assert.NotZero(p.t, uncommittedState)
	stateData.CurrentRoundState = uncommittedState
	if stateData.Height != p.prevoteHeight {
		return p.origin.Do(ctx, stateData)
	}
	prevote1, err := p.voteSigner.signVote(ctx, stateData, tmproto.PrevoteType, stateData.BlockID())
	require.NoError(p.t, err)
	prevote2, err := p.voteSigner.signVote(ctx, stateData, tmproto.PrevoteType, types.BlockID{})
	require.NoError(p.t, err)
	// send two votes to all peers (1st to one half, 2nd to another half)
	i := 0
	for _, ps := range p.peers {
		var msg p2p.Envelope
		if i < len(p.peers)/2 {
			msg = newP2PMessage(ps.peerID, prevote1)
		} else {
			msg = newP2PMessage(ps.peerID, prevote2)
		}
		require.NoError(p.t, p.voteCh.Send(ctx, msg))
		i++
	}
	return nil
}

func newP2PMessage(peerID types.NodeID, obj any) p2p.Envelope {
	envelope := p2p.Envelope{
		To: peerID,
	}
	switch t := obj.(type) {
	case *types.Vote:
		envelope.Message = &tmcons.Vote{
			Vote: t.ToProto(),
		}
	}
	return envelope
}

type bzProposalDecider struct {
	*Proposaler
}

func enterProposeWithBzProposalDecider(state *State) {
	action := state.ctrl.Get(EnterProposeType)
	enterProposeAction := action.(*EnterProposeAction)
	propler := enterProposeAction.proposalCreator.(*Proposaler)
	invalidDecider := &bzProposalDecider{Proposaler: propler}
	enterProposeAction.proposalCreator = invalidDecider
}

func (p *bzProposalDecider) Create(ctx context.Context, height int64, round int32, rs *cstypes.RoundState) error {
	block, blockParts, err := p.createProposalBlock(ctx, round, rs)
	if err != nil {
		return err
	}

	// Make proposal
	propBlockID := block.BlockID(blockParts)
	proposal := types.NewProposal(
		height,
		p.committedState.LastCoreChainLockedBlockHeight,
		round,
		rs.ValidRound,
		propBlockID,
		block.Header.Time,
	)
	err = p.signProposal(ctx, height, proposal)
	if err != nil {
		p.logger.Error("enterPropose: Error signing proposal",
			"height", height,
			"round", round,
			"error", err)
		return err
	}
	p.sendMessages(ctx, &ProposalMessage{proposal})
	p.sendMessages(ctx, blockPartsToMessages(height, round, blockParts)...)
	return nil
}
