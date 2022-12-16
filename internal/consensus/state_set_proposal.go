package consensus

import (
	"context"
	"time"

	tmbytes "github.com/tendermint/tendermint/libs/bytes"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/types"
)

type SetProposalEvent struct {
	Proposal *types.Proposal
	RecvTime time.Time
}

type SetProposalCommand struct {
	logger  log.Logger
	metrics *Metrics
}

func (cs *SetProposalCommand) Execute(ctx context.Context, behaviour *Behaviour, stateEvent StateEvent) (any, error) {
	appState := stateEvent.AppState
	event := stateEvent.Data.(SetProposalEvent)
	proposal := event.Proposal
	recvTime := event.RecvTime
	// Already have one
	// TODO: possibly catch double proposals
	if appState.Proposal != nil {
		return nil, nil
	}

	// Does not apply
	if proposal.Height != appState.Height || proposal.Round != appState.Round {
		return nil, nil
	}

	// Verify POLRound, which must be -1 or in range [0, proposal.Round).
	if proposal.POLRound < -1 ||
		(proposal.POLRound >= 0 && proposal.POLRound >= proposal.Round) {
		return nil, ErrInvalidProposalPOLRound
	}

	if proposal.CoreChainLockedHeight < appState.state.LastCoreChainLockedBlockHeight {
		return nil, ErrInvalidProposalCoreHeight
	}

	p := proposal.ToProto()
	// Verify signature
	proposalBlockSignID := types.ProposalBlockSignID(
		appState.state.ChainID,
		p,
		appState.state.Validators.QuorumType,
		appState.state.Validators.QuorumHash,
	)

	vset := appState.Validators
	height := appState.Height
	proposer := vset.GetProposer()

	//  fmt.Printf("verifying request Id %s signID %s quorum hash %s proposalBlockSignBytes %s\n",
	//	hex.EncodeToString(proposalRequestId),
	//  hex.EncodeToString(signID),
	//  hex.EncodeToString(cs.state.Validators.QuorumHash),
	//	hex.EncodeToString(proposalBlockSignBytes))

	switch {
	case proposer.PubKey != nil:
		// We are part of the validator set
		if !proposer.PubKey.VerifySignatureDigest(proposalBlockSignID, proposal.Signature) {
			cs.logger.Debug(
				"error verifying signature",
				"height", proposal.Height,
				"cs_height", height,
				"round", proposal.Round,
				"proposal", proposal,
				"proposer", proposer.ProTxHash.ShortString(),
				"pubkey", proposer.PubKey.HexString(),
				"quorumType", appState.state.Validators.QuorumType,
				"quorumHash", appState.state.Validators.QuorumHash,
				"proposalSignId", tmbytes.HexBytes(proposalBlockSignID))
			return nil, ErrInvalidProposalSignature
		}
	case appState.Commit != nil && appState.Commit.Height == proposal.Height && appState.Commit.Round == proposal.Round:
		// We are not part of the validator set
		// We might have a commit already for the Round State
		// We need to verify that the commit block id is equal to the proposal block id
		if !proposal.BlockID.Equals(appState.Commit.BlockID) {
			cs.logger.Debug("proposal blockId isn't the same as the commit blockId", "height", proposal.Height,
				"round", proposal.Round, "proposer", proposer.ProTxHash.ShortString())
			return nil, ErrInvalidProposalForCommit
		}
	default:
		// We received a proposal we can not check
		return nil, ErrUnableToVerifyProposal
	}

	proposal.Signature = p.Signature
	appState.Proposal = proposal
	appState.ProposalReceiveTime = recvTime
	appState.calculateProposalTimestampDifferenceMetric()
	// We don't update cs.ProposalBlockParts if it is already set.
	// This happens if we're already in cstypes.RoundStepApplyCommit or if there is a valid block in the current round.
	// TODO: We can check if Proposal is for a different block as this is a sign of misbehavior!
	if appState.ProposalBlockParts == nil {
		cs.metrics.MarkBlockGossipStarted()
		appState.ProposalBlockParts = types.NewPartSetFromHeader(proposal.BlockID.PartSetHeader)
	}

	cs.logger.Info("received proposal", "proposal", proposal)
	return nil, nil
}

func (cs *SetProposalCommand) Subscribe(observer *Observer) {
	observer.Subscribe(SetMetrics, func(a any) error {
		cs.metrics = a.(*Metrics)
		return nil
	})
}
