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

type Prevoter interface {
	Do(ctx context.Context, stateData *StateData) error
}

type prevoter struct {
	logger     log.Logger
	voteSigner *voteSigner
	blockExec  *blockExecutor
	metrics    *Metrics
	checker    []func(stateData *StateData) bool
}

func newPrevote(logger log.Logger, voteSigner *voteSigner, blockExec *blockExecutor, metrics *Metrics) *prevoter {
	p := &prevoter{
		logger:     logger,
		voteSigner: voteSigner,
		blockExec:  blockExec,
		metrics:    metrics,
	}
	p.checker = []func(stateData *StateData) bool{
		p.checkProposalBlock,
		p.checkPrevoteMaj23,
	}
	return p
}

func (p *prevoter) Do(ctx context.Context, stateData *StateData) error {
	err := stateData.isValidForPrevote()
	if err != nil {
		p.logger.Debug("prevoting nil: "+err.Error(), prevoteKeyVals(stateData)...)
		p.signAndAddNilVote(ctx, stateData)
		return err
	}
	err = p.blockExec.process(ctx, stateData, stateData.Round)
	if err != nil {
		p.handleError(err)
		p.signAndAddNilVote(ctx, stateData)
		return err
	}
	// Validate the block
	p.blockExec.mustValidate(ctx, stateData)
	p.metrics.MarkProposalProcessed(true)
	p.signVote(ctx, stateData)
	return nil
}

func (p *prevoter) handleError(err error) {
	p.metrics.MarkProposalProcessed(false)
	if errors.Is(err, sm.ErrBlockRejected) {
		p.logger.Error("prevoting nil: state machine rejected a proposed block; this should not happen:"+
			"the proposer may be misbehaving; prevoting nil", "error", err)
		return
	}
	if errors.As(err, &sm.ErrInvalidBlock{}) {
		p.logger.Error("prevoting nil: consensus deems this block invalid", "error", err)
		return
	}
	// Unknown error, so we panic
	panic(fmt.Sprintf("ProcessProposal: %v", err))
}

func (p *prevoter) signAndAddNilVote(ctx context.Context, stateData *StateData) {
	p.voteSigner.signAddVote(ctx, stateData, tmproto.PrevoteType, types.BlockID{})
}

func (p *prevoter) signVote(ctx context.Context, stateData *StateData) {
	shouldBeSent := p.shouldVoteBeSent(stateData)
	if !shouldBeSent {
		p.signAndAddNilVote(ctx, stateData)
		return
	}
	blockID := stateData.BlockID()
	p.voteSigner.signAddVote(ctx, stateData, tmproto.PrevoteType, blockID)
}

func (p *prevoter) checkProposalBlock(stateData *StateData) bool {
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
	if stateData.Proposal.POLRound != -1 {
		return false
	}
	if stateData.LockedRound == -1 {
		p.logger.Debug("prevote step: ProposalBlock is valid and there is no locked block; prevoting the proposal")
		return true
	}
	if stateData.ProposalBlock.HashesTo(stateData.LockedBlock.Hash()) {
		p.logger.Debug("prevote step: ProposalBlock is valid and matches our locked block; prevoting the proposal")
		return true
	}
	return false
}

func (p *prevoter) checkPrevoteMaj23(stateData *StateData) bool {
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
	blockID, ok := stateData.Votes.Prevotes(stateData.Proposal.POLRound).TwoThirdsMajority()
	if !ok {
		return false
	}
	if !stateData.ProposalBlock.HashesTo(blockID.Hash) {
		return false
	}
	if stateData.Proposal.POLRound < 0 {
		return false
	}
	if stateData.Proposal.POLRound >= stateData.Round {
		return false
	}
	if stateData.LockedRound <= stateData.Proposal.POLRound {
		p.logger.Debug("prevote step: ProposalBlock is valid and received a 2/3 majority in a round later than the locked round",
			"outcome", "prevoting the proposal")
		return true
	}
	if stateData.ProposalBlock.HashesTo(stateData.LockedBlock.Hash()) {
		p.logger.Debug("prevote step: ProposalBlock is valid and matches our locked block; prevoting the proposal")
		return true
	}
	return false
}

func (p *prevoter) shouldVoteBeSent(stateData *StateData) bool {
	for _, cond := range []func(stateData *StateData) bool{p.checkProposalBlock, p.checkPrevoteMaj23} {
		res := cond(stateData)
		if res {
			return true
		}
	}
	return false
}

func prevoteKeyVals(stateData *StateData) []any {
	sp := stateData.state.ConsensusParams.Synchrony
	keyVals := []any{
		"height", stateData.Height,
		"round", stateData.Round,
		"received", tmtime.Canonical(stateData.ProposalReceiveTime).Format(time.RFC3339Nano),
		"msg_delay", sp.MessageDelay,
		"precision", sp.Precision,
	}
	if stateData.Proposal != nil {
		keyVals = append(keyVals, "proposed", tmtime.Canonical(stateData.Proposal.Timestamp).Format(time.RFC3339Nano))
	}
	return keyVals
}
