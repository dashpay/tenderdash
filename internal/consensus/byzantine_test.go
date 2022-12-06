package consensus

import (
	"context"
	"fmt"
	"path"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	dbm "github.com/tendermint/tm-db"

	abciclient "github.com/tendermint/tendermint/abci/client"
	"github.com/tendermint/tendermint/abci/example/kvstore"
	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/internal/eventbus"
	"github.com/tendermint/tendermint/internal/evidence"
	"github.com/tendermint/tendermint/internal/mempool"
	"github.com/tendermint/tendermint/internal/p2p"
	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/internal/store"
	"github.com/tendermint/tendermint/internal/test/factory"
	"github.com/tendermint/tendermint/libs/log"
	tmcons "github.com/tendermint/tendermint/proto/tendermint/consensus"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/types"
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

	config := configSetup(t)

	nValidators := 4
	prevoteHeight := int64(2)
	testName := "consensus_byzantine_test"
	tickerFunc := newMockTickerFunc(true)

	genDoc, privVals := factory.RandGenesisDoc(config, nValidators, 1, factory.ConsensusParams())
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
			cs, err := NewState(logger, thisConfig.Consensus, stateStore, blockExec, blockStore, mp, evpool, eventBus)
			require.NoError(t, err)
			// set private validator
			pv := privVals[i]
			cs.SetPrivValidator(ctx, pv)

			cs.SetTimeoutTicker(tickerFunc())

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

	bzReactor := rts.reactors[bzNodeID]
	doPrevoteOrigin := bzNodeState.behaviour.commander.commands[DoPrevoteType]
	// alter prevote so that the byzantine node double votes when height is 2
	doPrevoteCmd := newMockCommand(func(ctx context.Context, behaviour *Behaviour, stateEvent StateEvent) (any, error) {
		appState := stateEvent.AppState
		event := stateEvent.Data.(DoPrevoteEvent)
		height := event.Height
		round := event.Round
		// allow first height to happen normally so that byzantine validator is no longer proposer
		uncommittedState, err := bzNodeState.blockExec.ProcessProposal(ctx, appState.ProposalBlock, round, appState.state, true)
		assert.NoError(t, err)
		assert.NotZero(t, uncommittedState)
		appState.CurrentRoundState = uncommittedState

		if height != prevoteHeight {
			return doPrevoteOrigin.Execute(ctx, behaviour, stateEvent)
		}
		prevote1, err := bzNodeState.voteSigner.signVote(ctx, appState, tmproto.PrevoteType, appState.BlockID())
		require.NoError(t, err)

		prevote2, err := bzNodeState.voteSigner.signVote(ctx, appState, tmproto.PrevoteType, types.BlockID{})
		require.NoError(t, err)

		// send two votes to all peers (1st to one half, 2nd to another half)
		i := 0
		for _, ps := range bzReactor.peers {
			voteCh := rts.voteChannels[bzNodeID]
			if i < len(bzReactor.peers)/2 {

				require.NoError(t, voteCh.Send(ctx,
					p2p.Envelope{
						To: ps.peerID,
						Message: &tmcons.Vote{
							Vote: prevote1.ToProto(),
						},
					}))
			} else {
				require.NoError(t, voteCh.Send(ctx,
					p2p.Envelope{
						To: ps.peerID,
						Message: &tmcons.Vote{
							Vote: prevote2.ToProto(),
						},
					}))
			}

			i++
		}
		return nil, nil
	})

	bzNodeState.behaviour.RegisterCommand(DoPrevoteType, doPrevoteCmd)

	// Introducing a lazy proposer means that the time of the block committed is
	// different to the timestamp that the other nodes have. This tests to ensure
	// that the evidence that finally gets proposed will have a valid timestamp.
	// lazyProposer := states[1]
	lazyNodeState := states[1]

	decideProposalCmd := newMockCommand(func(ctx context.Context, behaviour *Behaviour, stateEvent StateEvent) (any, error) {
		appState := stateEvent.AppState
		event := stateEvent.Data.(DecideProposalEvent)
		height := event.Height
		round := event.Round
		require.False(t, lazyNodeState.privValidator.IsZero())

		var commit *types.Commit
		switch {
		case appState.Height == appState.state.InitialHeight:
			// We're creating a proposal for the first block.
			// The commit is empty, but not nil.
			commit = types.NewCommit(0, 0, types.BlockID{}, nil)
		case appState.LastCommit != nil:
			// Make the commit from LastCommit
			commit = appState.LastCommit
		default: // This shouldn't happen.
			lazyNodeState.logger.Error("enterPropose: Cannot propose anything: No commit for the previous block")
			return nil, nil
		}

		if lazyNodeState.privValidator.IsZero() {
			// If this node is a validator & proposer in the current round, it will
			// miss the opportunity to create a block.
			lazyNodeState.logger.Error("enterPropose", "err", ErrPrivValidatorNotSet)
			return nil, nil
		}

		block, uncommittedState, err := lazyNodeState.blockExec.CreateProposalBlock(
			ctx,
			appState.Height,
			round,
			appState.state,
			commit,
			lazyNodeState.privValidator.ProTxHash,
			0,
		)
		require.NoError(t, err)
		assert.NotZero(t, uncommittedState)
		blockParts, err := block.MakePartSet(types.BlockPartSizeBytes)
		require.NoError(t, err)

		// Flush the WAL. Otherwise, we may not recompute the same proposal to sign,
		// and the privValidator will refuse to sign anything.
		if err := lazyNodeState.wal.FlushAndSync(); err != nil {
			lazyNodeState.logger.Error("error flushing to disk")
		}

		// Make proposal
		propBlockID := block.BlockID(blockParts)
		assert.NoError(t, err)

		proposal := types.NewProposal(
			height,
			appState.state.LastCoreChainLockedBlockHeight,
			round,
			appState.ValidRound,
			propBlockID,
			block.Header.Time,
		)
		p := proposal.ToProto()
		if _, err := lazyNodeState.privValidator.SignProposal(
			ctx,
			appState.state.ChainID,
			appState.state.Validators.QuorumType,
			appState.state.Validators.QuorumHash,
			p,
		); err == nil {
			proposal.Signature = p.Signature

			// send proposal and block parts on internal msg queue
			_ = lazyNodeState.msgInfoQueue.send(ctx, &ProposalMessage{proposal}, "")
			for i := 0; i < int(blockParts.Total()); i++ {
				part := blockParts.GetPart(i)
				_ = lazyNodeState.msgInfoQueue.send(ctx, &BlockPartMessage{
					appState.Height, appState.Round, part,
				}, "")
			}
			lazyNodeState.logger.Debug("signed proposal block", "block", block)
		} else if !lazyNodeState.replayMode {
			lazyNodeState.logger.Error("enterPropose: Error signing proposal", "height", height, "round", round, "err", err)
		}
		return nil, nil
	})
	lazyNodeState.behaviour.RegisterCommand(DecideProposalType, decideProposalCmd)

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
