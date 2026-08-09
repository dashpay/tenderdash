package consensus

import (
	"errors"
	"fmt"

	"github.com/cosmos/gogoproto/proto"

	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// Costs of peer messages, denominated in BLS signature verifications. One unit
// is one verification the consensus goroutine performs, which is the dominant
// per-message cost and the resource an unprivileged peer can otherwise force
// without bound.
const (
	// baseMessageCost prices a message that forces at most one signature
	// verification, and is the floor for one that forces none: every message
	// costs a turn on the single consensus goroutine even when it verifies
	// nothing.
	baseMessageCost = 1

	// maxPrecommitCost is the cost of the most expensive precommit the protocol
	// permits: its block signature and every vote extension it may carry.
	//
	// A precommit received from a peer has its signatures verified once. The
	// result is carried to the vote set as evidence (types.VoteVerification), so
	// the step that stores the vote does not check the same signatures again.
	maxPrecommitCost = baseMessageCost + types.MaxVoteExtensions

	// maxCommitCost is the cost of the most expensive commit the protocol
	// permits: its threshold block signature and every threshold vote extension.
	maxCommitCost = baseMessageCost + types.MaxVoteExtensions

	// maxPeerMessageCost is the most work a single peer message can force. Token
	// buckets charged in these units must hold at least this much, otherwise the
	// most expensive message can never be admitted at all. It is taken over the
	// message types rather than pinned to one of them, so that raising the price
	// of any of them carries the buckets with it.
	maxPeerMessageCost = max(maxPrecommitCost, maxCommitCost)
)

// errTooManyVoteExtensions reports a message declaring more vote extensions
// than any legitimate participant produces. It is not a peer offence: the
// message is dropped locally and the sender is not penalised, so that a future
// protocol revision — or a bug in this cost model — cannot evict honest peers.
var errTooManyVoteExtensions = errors.New("too many vote extensions")

// errUnpricedMessageType reports a message that is not part of the consensus
// wire union and therefore has no price. Like an over-long extension list it is
// not a peer offence: the message is dropped locally and the sender is not
// penalised, since a version skew — or a message type added without a price —
// says nothing about the sender's honesty.
var errUnpricedMessageType = errors.New("message type has no verification cost")

// peerMessageCost returns the verification work a message received from a peer
// can force, in signature verifications.
//
// The cost covers the DIRECT signature verifications the message stages: the
// block or threshold signature and each vote extension it declares. It does not
// cover threshold recovery and interpolation over an assembled vote set (see
// types/vote_set.go), nor the ABCI VerifyVoteExtension round-trip, both of which
// are driven by the state machine rather than by one peer message and are
// budgeted, if at all, elsewhere.
//
// The cost is derived from the message's declared contents alone, so it can be
// charged before the message is converted, validated or verified — charging
// after the work is done would defeat the purpose. The declared extension count
// is an upper bound on the verifications the message can force, since only
// threshold-recoverable extensions are verified; over-charging in that
// direction is safe, under-charging is not.
//
// The mapping is exhaustive over the consensus wire union on purpose. A type
// with no price is refused one rather than priced at the floor: an invented
// price is verification work charged to the wrong budget, and it would go
// unnoticed.
//
// These are absolute verification costs. They are not the data channel's
// weights (see dataChannelMessageCost), which price a proposal relative to a
// block part on a budget of their own.
func peerMessageCost(msg proto.Message) (int, error) {
	switch m := msg.(type) {
	case *tmcons.Vote:
		return voteMessageCost(m.GetVote())
	case *tmcons.Commit:
		return commitMessageCost(m.GetCommit())
	case *tmcons.Proposal:
		// One verification of the proposal signature, and nothing that scales
		// with the message contents.
		return baseMessageCost, nil
	case *tmcons.NewRoundStep, *tmcons.NewValidBlock, *tmcons.ProposalPOL,
		*tmcons.BlockPart, *tmcons.HasVote, *tmcons.HasCommit,
		*tmcons.VoteSetMaj23, *tmcons.VoteSetBits:
		// None of these verifies a signature, but each still costs a turn on
		// the single consensus goroutine, so none is processed uncharged.
		// Whether such a message is acceptable at all is decided by the
		// type-specific handling further down.
		return baseMessageCost, nil
	default:
		return 0, fmt.Errorf("%w: %T", errUnpricedMessageType, msg)
	}
}

// budgetedMessageCost returns the verification work a message dispatched to the
// consensus state can force. Votes, commits and proposals are charged against
// the verification budget; block parts draw on it not at all, and so need no
// room made for them.
func budgetedMessageCost(msg Message) (int, error) {
	switch m := msg.(type) {
	case *ProposalMessage:
		if m.Proposal == nil {
			return 0, nil
		}
		// One signature verification, and nothing that scales with contents.
		return baseMessageCost, nil
	case *VoteMessage:
		if m.Vote == nil {
			return 0, nil
		}
		return voteCost(m.Vote.Type, m.Vote.BlockID.IsNil(), len(m.Vote.VoteExtensions))
	case *CommitMessage:
		if m.Commit == nil {
			return 0, nil
		}
		return commitCost(len(m.Commit.ThresholdVoteExtensions))
	}
	return 0, nil
}

func voteMessageCost(vote *tmproto.Vote) (int, error) {
	return voteCost(vote.GetType(), isNilBlockID(vote.GetBlockID()), len(vote.GetVoteExtensions()))
}

func commitMessageCost(commit *tmproto.Commit) (int, error) {
	return commitCost(len(commit.GetThresholdVoteExtensions()))
}

// voteCost prices a vote from what it declares. A prevote and a precommit for a
// nil block carry no verifiable extensions, so both cost a single
// block-signature verification. A precommit for a real block costs that plus one
// verification per extension it declares.
//
// The price for a prevote depends on validation, not on the wire: a prevote may
// declare extensions, and this ignores them. It is safe only because
// types.Vote.ValidateBasic rejects extensions on anything but a precommit for a
// real block, and MsgFromProto runs it before the message is dispatched — so a
// prevote declaring extensions is never verified at all. Remove that rejection
// and a prevote carrying the maximum extensions forces 33 verifications for the
// price of one.
func voteCost(voteType tmproto.SignedMsgType, nilBlock bool, extensions int) (int, error) {
	n, err := extensionCount(extensions)
	if err != nil {
		return 0, err
	}
	if voteType != tmproto.PrecommitType || nilBlock {
		return baseMessageCost, nil
	}
	return baseMessageCost + n, nil
}

// commitCost prices a commit: one verification of the threshold block signature
// plus one per threshold vote extension.
func commitCost(extensions int) (int, error) {
	n, err := extensionCount(extensions)
	if err != nil {
		return 0, err
	}
	return baseMessageCost + n, nil
}

func extensionCount(n int) (int, error) {
	if n > types.MaxVoteExtensions {
		return 0, fmt.Errorf("%w: %d (max %d)", errTooManyVoteExtensions, n, types.MaxVoteExtensions)
	}
	return n, nil
}

// isNilBlockID reports whether a wire block ID is the block ID of a nil vote.
// It mirrors types.BlockID.IsNil: anything only partially empty is treated as a
// real block ID, which is the direction that over-charges rather than under.
func isNilBlockID(blockID tmproto.BlockID) bool {
	return len(blockID.Hash) == 0 &&
		blockID.PartSetHeader.Total == 0 &&
		len(blockID.PartSetHeader.Hash) == 0 &&
		len(blockID.StateID) == 0
}
