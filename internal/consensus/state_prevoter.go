package consensus

import (
	"context"
	"errors"
	"fmt"
	"time"

	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
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
}

func newPrevote(logger log.Logger, voteSigner *voteSigner, blockExec *blockExecutor, metrics *Metrics) *prevoter {
	p := &prevoter{
		logger:     logger,
		voteSigner: voteSigner,
		blockExec:  blockExec,
		metrics:    metrics,
	}
	return p
}

func (p *prevoter) Do(ctx context.Context, stateData *StateData) error {
	err := stateData.isValidForPrevote()
	if err != nil {
		keyVals := append(prevoteKeyVals(stateData), "error", err)
		p.logger.Debug("prevote is invalid", keyVals...)
		p.signAndAddNilVote(ctx, stateData)
		return nil
	}
	err = p.blockExec.ensureProcess(ctx, stateData, stateData.Round)
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
		p.logger.Error("state machine rejected a proposed block; this should not happen:"+
			"the proposer may be misbehaving; prevoting nil", "error", err)
		return
	}
	if errors.As(err, &sm.ErrInvalidBlock{}) {
		p.logger.Error("consensus deems this block invalid", "error", err)
		return
	}
	// Unknown error, so we panic
	panic(fmt.Sprintf("ProcessProposal: %v", err))
}

func (p *prevoter) signAndAddNilVote(ctx context.Context, stateData *StateData) {
	p.logger.Debug("sending prevote nil")
	p.voteSigner.signAddVote(ctx, stateData, tmproto.PrevoteType, types.BlockID{})
}

func (p *prevoter) signVote(ctx context.Context, stateData *StateData) {
	shouldBeSent := p.shouldVoteBeSent(stateData.RoundState)
	if !shouldBeSent {
		p.signAndAddNilVote(ctx, stateData)
		return
	}
	blockID := stateData.BlockID()
	p.voteSigner.signAddVote(ctx, stateData, tmproto.PrevoteType, blockID)
}

// checkProposalBlock returns true when proposal block can be prevoted based on proposal locked state
func (p *prevoter) checkProposalBlock(rs cstypes.RoundState) bool {
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
	if rs.Proposal.POLRound != -1 {
		return false
	}
	if rs.LockedRound == -1 {
		p.logger.Debug("prevote step: ProposalBlock is valid and there is no locked block; prevoting the proposal")
		return true
	}
	if rs.ProposalBlock.HashesTo(rs.LockedBlock.Hash()) {
		p.logger.Debug("prevote step: ProposalBlock is valid and matches our locked block; prevoting the proposal")
		return true
	}
	return false
}

// checkPrevoteMaj23 checks if the proposal can be prevoted based on the majority of prevotes received from other validators
func (p *prevoter) checkPrevoteMaj23(rs cstypes.RoundState) bool {
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
	blockID, ok := rs.Votes.Prevotes(rs.Proposal.POLRound).TwoThirdsMajority()
	if !ok {
		return false
	}
	if !rs.ProposalBlock.HashesTo(blockID.Hash) {
		return false
	}
	if rs.Proposal.POLRound < 0 {
		return false
	}
	if rs.Proposal.POLRound >= rs.Round {
		return false
	}
	if rs.LockedRound <= rs.Proposal.POLRound {
		p.logger.Debug("prevote step: ProposalBlock is valid and received a 2/3 majority in a round later than the locked round",
			"outcome", "prevoting the proposal")
		return true
	}
	if rs.ProposalBlock.HashesTo(rs.LockedBlock.Hash()) {
		p.logger.Debug("prevote step: ProposalBlock is valid and matches our locked block; prevoting the proposal")
		return true
	}
	return false
}

func (p *prevoter) shouldVoteBeSent(rs cstypes.RoundState) bool {
	return p.checkProposalBlock(rs) || p.checkPrevoteMaj23(rs)
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
