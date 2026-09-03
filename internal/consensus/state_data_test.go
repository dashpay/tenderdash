package consensus

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/test/factory"
	tmrequire "github.com/dashpay/tenderdash/internal/test/require"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

func TestIsValidForPrevote(t *testing.T) {
	valSet, _ := factory.MockValidatorSet()
	now := time.Now()
	defState := sm.State{
		Validators: valSet,
	}
	testCases := []struct {
		state   sm.State
		rs      cstypes.RoundState
		wantErr string
	}{
		{
			// invalid proposal-block
			state: defState,
			rs: cstypes.RoundState{
				Validators: valSet,
			},
			wantErr: "proposal-block is nil",
		},
		{
			// invalid proposal
			state: defState,
			rs: cstypes.RoundState{
				ProposalBlock: &types.Block{},
				Validators:    valSet,
			},
			wantErr: "proposal is nil",
		},
		{
			// timestamps is not equal
			state: defState,
			rs: cstypes.RoundState{
				ProposalBlock: &types.Block{
					Header: types.Header{Time: now},
				},
				Proposal:   &types.Proposal{Timestamp: now.Add(time.Second)},
				Validators: valSet,
			},
			wantErr: "proposal timestamp not equal",
		},
		{
			// proposal is not timely
			state: sm.State{
				InitialHeight: 1000,
				LastBlockTime: now.Add(time.Second),
			},
			rs: cstypes.RoundState{
				Height: 1000,
				ProposalBlock: &types.Block{
					Header: types.Header{Time: now},
				},
				LockedRound: -1,
				Proposal: &types.Proposal{
					Timestamp: now,
					POLRound:  -1,
				},
				Validators: valSet,
			},
			wantErr: "proposal is not timely",
		},
		{
			// valid
			state: sm.State{
				InitialHeight: 1000,
				LastBlockTime: now,
			},
			rs: cstypes.RoundState{
				Height: 1000,
				ProposalBlock: &types.Block{
					Header: types.Header{Time: now},
				},
				LockedRound: -1,
				Proposal: &types.Proposal{
					Timestamp: now,
					POLRound:  -1,
				},
				Validators: valSet,
			},
			wantErr: "",
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("test-case #%d", i), func(t *testing.T) {
			stateData := StateData{
				state:      tc.state,
				RoundState: tc.rs,
			}
			tmrequire.Error(t, tc.wantErr, stateData.isValidForPrevote())
		})
	}
}

// TestVerifyCommitAgainstCommitBlockID pins which block a peer-sent commit is
// checked against. The threshold signature only ever covers the commit's own
// BlockID, so verifying it against a proposal we happen to hold rejects the very
// commit the network produced and leaves a lagging node stuck (dashpay/tenderdash#1414).
// A commit for a block other than ours is catch-up traffic: it is adopted like a
// commit that arrived before any proposal. Only a signature that fails against
// the commit's own BlockID is forgery, and that must still evict the sender.
func TestVerifyCommitAgainstCommitBlockID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const (
		chainID = "verify-commit-block-id"
		height  = int64(10)
		round   = int32(0)
	)

	valSet, privVals := factory.MockValidatorSet()
	committedBlockID := factory.MakeBlockID()
	ourBlockID := factory.MakeBlockID()

	makeCommit := func(t *testing.T, blockID types.BlockID, forged bool) *types.Commit {
		t.Helper()
		voteSet := types.NewVoteSet(chainID, height, round, tmproto.PrecommitType, valSet)
		commit, err := factory.MakeCommit(ctx, blockID, height, round, voteSet, valSet, privVals)
		require.NoError(t, err)
		if forged {
			// Flip a bit so the failure is the verification itself rather than a
			// malformed-length rejection.
			commit.ThresholdBlockSignature[0] ^= 0xFF
		}
		return commit
	}

	testCases := []struct {
		name                string
		noProposal          bool
		ignoreProposalBlock bool
		proposalBlockID     types.BlockID
		commitBlockID       types.BlockID
		forged              bool
		wantVerified        bool
		wantEvict           bool
		wantAdopted         bool
	}{
		{
			name:            "genuine commit for our proposal",
			proposalBlockID: committedBlockID,
			commitBlockID:   committedBlockID,
			wantVerified:    true,
		},
		{
			name:          "commit before proposal is adopted",
			noProposal:    true,
			commitBlockID: committedBlockID,
			wantAdopted:   true,
		},
		{
			name:                "commit for a future round is verified and adopted",
			ignoreProposalBlock: true,
			proposalBlockID:     ourBlockID,
			commitBlockID:       committedBlockID,
			wantVerified:        true,
			wantAdopted:         true,
		},
		{
			name:            "genuine commit for another block is adopted",
			proposalBlockID: ourBlockID,
			commitBlockID:   committedBlockID,
			wantAdopted:     true,
		},
		{
			name:            "forged commit for our proposal evicts",
			proposalBlockID: committedBlockID,
			commitBlockID:   committedBlockID,
			forged:          true,
			wantEvict:       true,
		},
		{
			name:            "forged commit for another block evicts",
			proposalBlockID: ourBlockID,
			commitBlockID:   committedBlockID,
			forged:          true,
			wantEvict:       true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var (
				proposal      *types.Proposal
				proposalBlock *types.Block
				parts         *types.PartSet
			)
			if !tc.noProposal {
				proposal = types.NewProposal(height, 1, round, -1, tc.proposalBlockID, time.Now())
				proposalBlock = &types.Block{Header: types.Header{Height: height}}
				parts = types.NewPartSetFromHeader(tc.proposalBlockID.PartSetHeader)
			}
			stateData := StateData{
				logger: log.NewNopLogger(),
				state:  sm.State{ChainID: chainID},
				RoundState: cstypes.RoundState{
					Height:             height,
					Round:              round,
					Proposal:           proposal,
					ProposalBlock:      proposalBlock,
					ProposalBlockParts: parts,
					Validators:         valSet,
				},
			}
			commit := makeCommit(t, tc.commitBlockID, tc.forged)

			verified, err := stateData.verifyCommit(commit, "peer", tc.ignoreProposalBlock, nil)

			assert.Equal(t, tc.wantVerified, verified)
			if !tc.wantEvict {
				require.NoError(t, err, "an honest peer relaying the block the network committed must not fail verification")
			} else {
				require.Error(t, err)
				assert.ErrorAs(t, err, &types.ErrInvalidCommitSignature{},
					"a forged threshold signature must stay evictable")
			}

			queue := &chanQueue[peerErrorMsg]{ch: make(chan peerErrorMsg, 1)}
			action := &TryAddCommitAction{peerErrorQueue: queue, metrics: NopMetrics()}
			action.handleCommitVerifyError(err, "peer", false)
			assert.Equal(t, tc.wantEvict, len(queue.ch) == 1, "eviction must follow forgery and nothing else")

			if !tc.wantAdopted {
				assert.Same(t, proposal, stateData.Proposal, "our proposal must survive")
				assert.Same(t, proposalBlock, stateData.ProposalBlock, "our proposal block must survive")
				if !tc.wantVerified {
					assert.Nil(t, stateData.Commit, "a commit that failed verification must not be stored")
				}
				return
			}

			assert.Same(t, commit, stateData.Commit, "the commit must be kept until its block arrives")
			assert.True(t, stateData.ProposalBlockParts.HasHeader(commit.BlockID.PartSetHeader),
				"the part set must be ready for the committed block")
			assert.Nil(t, stateData.ProposalBlock, "the block we proposed cannot be the committed one")
			assert.Nil(t, stateData.Proposal, "a proposal the network did not commit must not block the real one")
		})
	}
}

// TestVerifyCommitWithRetargetedProposalBlockParts pins commit handling when a
// +2/3 prevote majority has already pointed ProposalBlockParts at the committed
// block while leaving Proposal untouched (addVoteUpdateValidBlockMw). The part
// set header then matches the commit even though the Proposal describes a block
// the network dropped, and answering either question from the other one strands
// the node: a surviving stale Proposal rejects the committed block's last part on
// its core chain lock height, and a Proposal consulted instead of the assembled
// block rejects the very commit that block satisfies. Both leave a parked
// StateData.Commit that no later message can retry (dashpay/tenderdash#1414).
func TestVerifyCommitWithRetargetedProposalBlockParts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const (
		chainID = "retargeted-proposal-block-parts"
		height  = int64(10)
		round   = int32(0)
		// Small enough that the committed block spans several parts, so a partially
		// gossiped part set is representable.
		partSize = uint32(64)
	)

	valSet, privVals := factory.MockValidatorSet()
	committedBlock := &types.Block{
		Header:     *factory.MakeHeader(t, &types.Header{Height: height}),
		LastCommit: &types.Commit{},
	}
	committedParts, err := committedBlock.MakePartSet(partSize)
	require.NoError(t, err)
	require.Greater(t, committedParts.Total(), uint32(1), "a single-part block cannot show a partially received part set")
	committedBlockID := committedBlock.BlockID(committedParts)
	staleBlockID := factory.MakeBlockID()

	voteSet := types.NewVoteSet(chainID, height, round, tmproto.PrecommitType, valSet)
	commit, err := factory.MakeCommit(ctx, committedBlockID, height, round, voteSet, valSet, privVals)
	require.NoError(t, err)

	// A part set retargeted to the committed block, holding what has arrived so far.
	partiallyReceived := func(t *testing.T) *types.PartSet {
		t.Helper()
		parts := types.NewPartSetFromHeader(committedBlockID.PartSetHeader)
		added, err := parts.AddPart(committedParts.GetPart(0))
		require.NoError(t, err)
		require.True(t, added)
		return parts
	}

	testCases := []struct {
		name string
		// proposalFor is the block our Proposal describes, nil for no proposal.
		proposalFor  *types.BlockID
		blockArrived bool
		ignoreBlock  bool
		wantVerified bool
		wantProposal bool
	}{
		{
			name:        "stale proposal is dropped once the parts target the committed block",
			proposalFor: &staleBlockID,
		},
		{
			name:         "assembled block outweighs a stale proposal",
			proposalFor:  &staleBlockID,
			blockArrived: true,
			wantVerified: true,
			wantProposal: true,
		},
		{
			name:         "assembled block is accepted without any proposal",
			blockArrived: true,
			wantVerified: true,
		},
		{
			name:         "proposal for the committed block survives a part set it does not match",
			proposalFor:  &committedBlockID,
			ignoreBlock:  true,
			wantVerified: true,
			wantProposal: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var (
				proposal    *types.Proposal
				receiveTime time.Time
			)
			if tc.proposalFor != nil {
				receiveTime = time.Now()
				proposal = types.NewProposal(height, 1, round, -1, *tc.proposalFor, receiveTime)
			}
			var (
				block *types.Block
				parts = partiallyReceived(t)
			)
			switch {
			case tc.blockArrived:
				block, parts = committedBlock, committedParts
			case tc.ignoreBlock:
				// A commit for a future round reaches adoptCommit while the part set
				// still tracks the block of an earlier round.
				parts = types.NewPartSetFromHeader(staleBlockID.PartSetHeader)
			}
			stateData := StateData{
				logger: log.NewNopLogger(),
				state:  sm.State{ChainID: chainID},
				RoundState: cstypes.RoundState{
					Height:              height,
					Round:               round,
					Proposal:            proposal,
					ProposalReceiveTime: receiveTime,
					ProposalBlock:       block,
					ProposalBlockParts:  parts,
					Validators:          valSet,
				},
			}

			verified, err := stateData.verifyCommit(commit, "peer", tc.ignoreBlock, nil)
			require.NoError(t, err)
			assert.Equal(t, tc.wantVerified, verified,
				"the assembled block, not a proposal that outlived its own block, decides whether the commit is ours")

			if tc.wantProposal {
				assert.Same(t, proposal, stateData.Proposal, "a proposal for the committed block must survive")
				assert.Equal(t, receiveTime, stateData.ProposalReceiveTime,
					"dropping the receive time of a surviving proposal loses its timeliness")
			} else {
				assert.Nil(t, stateData.Proposal, "a proposal for a block the network dropped must not survive")
				assert.True(t, stateData.ProposalReceiveTime.IsZero(), "the receive time belongs to the dropped proposal")
			}

			if tc.ignoreBlock {
				assert.Same(t, commit, stateData.Commit, "a future-round commit is kept until its block arrives")
				assert.True(t, stateData.ProposalBlockParts.HasHeader(commit.BlockID.PartSetHeader),
					"the part set must be ready for the committed block")
				return
			}
			if tc.wantVerified {
				assert.Nil(t, stateData.Commit,
					"the caller parks a verified commit only once the block passed validation")
				return
			}
			assert.Same(t, commit, stateData.Commit, "the commit must be kept until its block arrives")
			assert.Same(t, parts, stateData.ProposalBlockParts, "a part set already collecting the committed block must be kept")
			assert.Equal(t, uint32(1), stateData.ProposalBlockParts.Count(), "no received part may be discarded")
		})
	}
}
