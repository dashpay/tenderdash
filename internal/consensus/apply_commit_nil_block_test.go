package consensus

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/abci/example/kvstore"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/dash"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	sm "github.com/dashpay/tenderdash/internal/state"
	smmocks "github.com/dashpay/tenderdash/internal/state/mocks"
	sf "github.com/dashpay/tenderdash/internal/state/test/factory"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/log"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	"github.com/dashpay/tenderdash/types"
	"github.com/dashpay/tenderdash/types/mocks"
)

// ensureProcess reads RoundState.ProposalBlock and passes it on without checking
// it. A nil block has two ways to crash in the same three lines, chosen by
// whether the || short-circuits: with Source != ProcessProposalSource the
// condition is satisfied by its first operand and the nil block reaches
// ProcessProposal; with Source == ProcessProposalSource the second operand runs
// and MatchesBlock dereferences it one frame earlier. A missing block is a state
// the caller can produce, so it must be an error rather than either crash.
func TestEnsureProcessRefusesNilProposalBlock(t *testing.T) {
	testCases := []struct {
		name   string
		source string
	}{
		{name: "condition short-circuits before the block is read", source: "ResponsePrepareProposal"},
		{name: "condition reads the block to compare it", source: sm.ProcessProposalSource},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			exec := &blockExecutor{
				logger: log.NewTestingLogger(t),
				privValidator: privValidator{
					PrivValidator: mocks.NewPrivValidator(t),
					ProTxHash:     crypto.RandProTxHash(),
				},
				blockExec: smmocks.NewExecutor(t),
			}
			rs := &cstypes.RoundState{
				Height:            10,
				Round:             0,
				ProposalBlock:     nil,
				CurrentRoundState: sm.CurrentRoundState{Params: sm.RoundParams{Source: tc.source}},
			}

			var err error
			require.NotPanics(t, func() { err = exec.ensureProcess(context.Background(), rs, 0) },
				"a missing proposal block must not crash the node")
			require.Error(t, err, "a missing proposal block must be reported")
			assert.ErrorIs(t, err, ErrProposalBlockNotSet)
		})
	}
}

// A later-round proposal must neither crash reconciliation nor prevent the
// parked commit from being applied when its own block arrives.
func TestLaterRoundProposalDoesNotStrandTheParkedCommit(t *testing.T) {
	chainID := t.Name()
	for _, tc := range []struct {
		name       string
		coreHeight uint32
		retransmit bool
	}{
		{name: "same core height", coreHeight: 1},
		{name: "different core height", coreHeight: 2},
		{name: "retransmission with same core height", coreHeight: 1, retransmit: true},
		{name: "retransmission with different core height", coreHeight: 2, retransmit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cfg := configSetup(t)

			css := makeConsensusState(ctx, t, cfg, 2, chainID, newTickerFunc())
			privVals := make([]types.PrivValidator, 0, len(css))
			for _, c := range css {
				privVals = append(privVals, c.privValidator.PrivValidator)
			}
			proposerStateData := css[0].GetStateData()
			node := css[1]
			stateData := node.GetStateData()
			ctx = dash.ContextWithProTxHash(ctx, node.privValidator.ProTxHash)

			// The block the network commits at round 0.
			block, err := sf.MakeBlock(proposerStateData.state, 1, &types.Commit{}, kvstore.ProtocolVersion)
			require.NoError(t, err)
			block.CoreChainLockedHeight = 1
			committedParts, err := block.MakePartSet(types.BlockPartSizeBytes)
			require.NoError(t, err)
			commit, err := factory.MakeCommit(ctx, block.BlockID(committedParts), block.Height, 0,
				proposerStateData.Votes.Precommits(0), proposerStateData.Validators, privVals)
			require.NoError(t, err)
			peerID := proposerStateData.Validators.Proposer().NodeAddress.NodeID

			// A commit for a block we do not hold is parked at round 0.
			stateData.updateRoundStep(0, cstypes.RoundStepPrevote)
			commitCtx := msgInfoWithCtx(ctx, msgInfo{Msg: &CommitMessage{commit}, PeerID: peerID})
			require.NoError(t, node.ctrl.Dispatch(commitCtx, &TryAddCommitEvent{Commit: commit, PeerID: peerID}, &stateData))
			require.NotNil(t, stateData.Commit, "the commit must be parked while its block is missing")

			// The round advances; EnterNewRound discards the part set it was collecting.
			require.NoError(t, node.ctrl.Dispatch(ctx, &EnterNewRoundEvent{Height: block.Height, Round: 1}, &stateData))
			require.Equal(t, int32(1), stateData.Round)

			// A different block for round 1. Morphed in place rather than copied, since
			// types.Block carries a mutex and copying it trips go vet's copylocks.
			block.Time = block.Time.Add(time.Second)
			block.CoreChainLockedHeight = tc.coreHeight
			otherParts, err := block.MakePartSet(types.BlockPartSizeBytes)
			require.NoError(t, err)
			otherBlockID := block.BlockID(otherParts)
			require.False(t, otherBlockID.Equals(commit.BlockID), "the later-round block must differ from the committed one")

			proposer, err := stateData.ProposerSelector.GetProposer(block.Height, 1)
			require.NoError(t, err)
			var key types.PrivValidator
			for _, pv := range privVals {
				proTxHash, err := pv.GetProTxHash(ctx)
				require.NoError(t, err)
				if proTxHash.Equal(proposer.ProTxHash) {
					key = pv
				}
			}
			require.NotNil(t, key, "the round-1 proposer's key must be available")
			proposal := types.NewProposal(
				block.Height, block.CoreChainLockedHeight, 1, -1, otherBlockID, block.Time)
			protoProposal := proposal.ToProto()
			_, err = key.SignProposal(ctx, stateData.state.ChainID,
				stateData.Validators.QuorumType, stateData.Validators.QuorumHash, protoProposal)
			require.NoError(t, err)
			proposal.Signature = protoProposal.Signature

			require.NoError(t, node.msgDispatcher.dispatch(ctx, &stateData, msgInfo{
				Msg: &ProposalMessage{Proposal: proposal}, PeerID: peerID, ReceiveTime: tmtime.Now()}))
			require.NotNil(t, stateData.Proposal, "a proposal for a later round is not the commit's to refuse")

			require.NotPanics(t, func() {
				for i := 0; i < int(otherParts.Total()); i++ {
					msg := &BlockPartMessage{Height: block.Height, Round: 1, Part: otherParts.GetPart(i)}
					partCtx := msgInfoWithCtx(ctx, msgInfo{Msg: msg, PeerID: peerID})
					require.NoError(t, node.ctrl.Dispatch(partCtx, &AddProposalBlockPartEvent{Msg: msg, PeerID: peerID}, &stateData))
				}
			}, "assembling a block other than the committed one must not crash the node")

			assert.NotNil(t, stateData.Commit, "the commit must stay parked")
			assert.Nil(t, stateData.Proposal, "the mismatching proposal must be discarded")
			assert.True(t, stateData.ProposalReceiveTime.IsZero(), "the mismatching proposal's receive time must be reset")
			assert.True(t, stateData.ProposalBlockParts.HasHeader(commit.BlockID.PartSetHeader),
				"the round state must go back to collecting the committed block")
			assert.Less(t, stateData.Height, int64(2), "a block the network did not commit must not be applied")

			if tc.retransmit {
				require.NoError(t, node.msgDispatcher.dispatch(ctx, &stateData, msgInfo{
					Msg: &ProposalMessage{Proposal: proposal}, PeerID: peerID, ReceiveTime: tmtime.Now()}))
				assert.Nil(t, stateData.Proposal, "a conflicting retransmission must not replace commit metadata")
				assert.True(t, stateData.ProposalReceiveTime.IsZero())
			}

			for i := 0; i < int(committedParts.Total()); i++ {
				msg := &BlockPartMessage{Height: commit.Height, Round: commit.Round, Part: committedParts.GetPart(i)}
				partCtx := msgInfoWithCtx(ctx, msgInfo{Msg: msg, PeerID: peerID})
				require.NoError(t, node.ctrl.Dispatch(partCtx, &AddProposalBlockPartEvent{Msg: msg, PeerID: peerID}, &stateData))
			}
			assert.Equal(t, int64(2), stateData.Height, "the parked commit must be applied when its block arrives")
			assert.True(t, stateData.state.LastBlockID.Equals(commit.BlockID), "only the committed block must be applied")
		})
	}
}
