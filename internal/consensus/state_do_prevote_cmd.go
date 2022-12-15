package consensus

import (
	"context"
	"errors"
	"fmt"
	"time"

	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/libs/log"
	tmtime "github.com/tendermint/tendermint/libs/time"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/types"
)

type DoPrevoteEvent struct {
	Height         int64
	Round          int32
	AllowOldBlocks bool
}

type DoPrevoteCommand struct {
	logger                  log.Logger
	voteSigner              *VoteSigner
	blockExec               *blockExecutor
	metrics                 *Metrics
	proposedBlockTimeWindow time.Duration
	replayMode              bool
}

func (cs *DoPrevoteCommand) Execute(ctx context.Context, behaviour *Behaviour, stateEvent StateEvent) (any, error) {
	appState := stateEvent.AppState
	event := stateEvent.Data.(DoPrevoteEvent)
	height := event.Height
	round := event.Round
	logger := cs.logger.With("height", height, "round", round)

	// Check that a proposed block was not received within this round (and thus executing this from a timeout).
	if appState.ProposalBlock == nil {
		logger.Debug("prevote step: ProposalBlock is nil; prevoting nil")
		cs.voteSigner.signAddVote(ctx, appState, tmproto.PrevoteType, types.BlockID{})
		return nil, nil
	}

	if appState.Proposal == nil {
		logger.Debug("prevote step: did not receive proposal; prevoting nil")
		cs.voteSigner.signAddVote(ctx, appState, tmproto.PrevoteType, types.BlockID{})
		return nil, nil
	}

	if !appState.Proposal.Timestamp.Equal(appState.ProposalBlock.Header.Time) {
		logger.Debug("prevote step: proposal timestamp not equal; prevoting nil")
		cs.voteSigner.signAddVote(ctx, appState, tmproto.PrevoteType, types.BlockID{})
		return nil, nil
	}

	sp := appState.state.ConsensusParams.Synchrony.SynchronyParamsOrDefaults()
	//TODO: Remove this temporary fix when the complete solution is ready. See #8739
	if !cs.replayMode && appState.Proposal.POLRound == -1 && appState.LockedRound == -1 && !appState.proposalIsTimely() {
		logger.Debug("prevote step: Proposal is not timely; prevoting nil",
			"proposed", tmtime.Canonical(appState.Proposal.Timestamp).Format(time.RFC3339Nano),
			"received", tmtime.Canonical(appState.ProposalReceiveTime).Format(time.RFC3339Nano),
			"msg_delay", sp.MessageDelay,
			"precision", sp.Precision)
		cs.voteSigner.signAddVote(ctx, appState, tmproto.PrevoteType, types.BlockID{})
		return nil, nil
	}

	/*
		The block has now passed Tendermint's validation rules.
		Before prevoting the block received from the proposer for the current round and height,
		we request the Application, via the ProcessProposal, ABCI call to confirm that the block is
		valid. If the Application does not accept the block, Tendermint prevotes nil.

		WARNING: misuse of block rejection by the Application can seriously compromise Tendermint's
		liveness properties. Please see PrepareProposal-ProcessProposal coherence and determinism
		properties in the ABCI++ specification.
	*/
	err := cs.blockExec.process(ctx, appState, appState.Round)
	if err != nil {
		cs.metrics.MarkProposalProcessed(false)
		if errors.Is(err, sm.ErrBlockRejected) {
			logger.Error("prevote step: state machine rejected a proposed block; this should not happen:"+
				"the proposer may be misbehaving; prevoting nil", "err", err)
			cs.voteSigner.signAddVote(ctx, appState, tmproto.PrevoteType, types.BlockID{})
			return nil, nil
		}

		if errors.As(err, &sm.ErrInvalidBlock{}) {
			logger.Error("prevote step: consensus deems this block invalid; prevoting nil", "err", err)
			cs.voteSigner.signAddVote(ctx, appState, tmproto.PrevoteType, types.BlockID{})
			return nil, nil
		}

		// Unknown error, so we panic
		panic(fmt.Sprintf("ProcessProposal: %v", err))
	}
	cs.metrics.MarkProposalProcessed(true)

	/*
		22: upon <PROPOSAL, h_p, round_p, v, −1> from proposer(h_p, round_p) while step_p = propose do
		23: if valid(v) && (lockedRound_p = −1 || lockedValue_p = v) then
		24: broadcast <PREVOTE, h_p, round_p, id(v)>

		Here, cs.Proposal.POLRound corresponds to the -1 in the above algorithm rule.
		This means that the proposer is producing a new proposal that has not previously
		seen a 2/3 majority by the network.

		If we have already locked on a different value that is different from the proposed value,
		we prevote nil since we are locked on a different value. Otherwise, if we're not locked on a block
		or the proposal matches our locked block, we prevote the proposal.
	*/
	blockID := appState.BlockID()
	if appState.Proposal.POLRound == -1 {
		if appState.LockedRound == -1 {
			logger.Debug("prevote step: ProposalBlock is valid and there is no locked block; prevoting the proposal")
			cs.voteSigner.signAddVote(ctx, appState, tmproto.PrevoteType, blockID)
			return nil, nil
		}
		if appState.ProposalBlock.HashesTo(appState.LockedBlock.Hash()) {
			logger.Debug("prevote step: ProposalBlock is valid and matches our locked block; prevoting the proposal")
			cs.voteSigner.signAddVote(ctx, appState, tmproto.PrevoteType, blockID)
			return nil, nil
		}
	}

	/*
		28: upon <PROPOSAL, h_p, round_p, v, v_r> from proposer(h_p, round_p) AND 2f + 1 <PREVOTE, h_p, v_r, id(v)> while
		step_p = propose && (v_r ≥ 0 && v_r < round_p) do
		29: if valid(v) && (lockedRound_p ≤ v_r || lockedValue_p = v) then
		30: broadcast <PREVOTE, h_p, round_p, id(v)>

		This rule is a bit confusing but breaks down as follows:

		If we see a proposal in the current round for value 'v' that lists its valid round as 'v_r'
		AND this validator saw a 2/3 majority of the voting power prevote 'v' in round 'v_r', then we will
		issue a prevote for 'v' in this round if 'v' is valid and either matches our locked value OR
		'v_r' is a round greater than or equal to our current locked round.

		'v_r' can be a round greater than to our current locked round if a 2/3 majority of
		the network prevoted a value in round 'v_r' but we did not lock on it, possibly because we
		missed the proposal in round 'v_r'.
	*/
	blockID, ok := appState.Votes.Prevotes(appState.Proposal.POLRound).TwoThirdsMajority()
	if ok && appState.ProposalBlock.HashesTo(blockID.Hash) && appState.Proposal.POLRound >= 0 && appState.Proposal.POLRound < appState.Round {
		if appState.LockedRound <= appState.Proposal.POLRound {
			logger.Debug("prevote step: ProposalBlock is valid and received a 2/3 majority in a round later than the locked round",
				"outcome", "prevoting the proposal")
			cs.voteSigner.signAddVote(ctx, appState, tmproto.PrevoteType, blockID)
			return nil, nil
		}
		if appState.ProposalBlock.HashesTo(appState.LockedBlock.Hash()) {
			logger.Debug("prevote step: ProposalBlock is valid and matches our locked block; prevoting the proposal")
			cs.voteSigner.signAddVote(ctx, appState, tmproto.PrevoteType, blockID)
			return nil, nil
		}
	}

	// Validate proposal block
	err = sm.ValidateBlockChainLock(appState.state, appState.ProposalBlock)
	if err != nil {
		// ProposalBlock is invalid, prevote nil.
		logger.Error("enterPrevote: ProposalBlock chain lock is invalid", "err", err)
		cs.voteSigner.signAddVote(ctx, appState, tmproto.PrevoteType, types.BlockID{})
		return nil, nil
	}

	// Validate proposal block time
	if !event.AllowOldBlocks {
		err = sm.ValidateBlockTime(cs.proposedBlockTimeWindow, appState.state, appState.ProposalBlock)
		if err != nil {
			// ProposalBlock is invalid, prevote nil.
			logger.Error("enterPrevote: ProposalBlock time is invalid", "err", err)
			cs.voteSigner.signAddVote(ctx, appState, tmproto.PrevoteType, types.BlockID{})
			return nil, nil
		}
	}

	logger.Debug("prevote step: ProposalBlock is valid but was not our locked block or " +
		"did not receive a more recent majority; prevoting nil")
	cs.voteSigner.signAddVote(ctx, appState, tmproto.PrevoteType, types.BlockID{})
	return nil, nil
}

func (cs *DoPrevoteCommand) Subscribe(observer *Observer) {
	observer.Subscribe(SetMetrics, func(a any) error {
		cs.metrics = a.(*Metrics)
		return nil
	})
	observer.Subscribe(SetReplayMode, func(a any) error {
		cs.replayMode = a.(bool)
		return nil
	})
}
