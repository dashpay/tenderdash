package consensus

import (
	"context"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

// AddRoundSkipCertificateEvent carries a certificate that +2/3 of the validators
// voted to skip a round.
type AddRoundSkipCertificateEvent struct {
	Certificate *types.RoundSkipCertificate
	// Verified is set for a certificate this node recovered itself, whose
	// signature was checked when it was recovered.
	Verified bool
}

// GetType returns AddRoundSkipCertificateType event-type
func (e *AddRoundSkipCertificateEvent) GetType() EventType {
	return AddRoundSkipCertificateType
}

// AddRoundSkipCertificateAction moves the node past a round that +2/3 of the
// validators voted to skip.
//
// Moving to a higher round is always safe in Tendermint: what keeps two blocks
// from being committed is the locking rule on prevotes and precommits, which a
// certificate does not touch. The certificate only tells a node that the round
// it is in will not produce a block, because the validators that could make it
// do so have stopped waiting for its proposer.
type AddRoundSkipCertificateAction struct {
	logger         log.Logger
	eventPublisher *EventPublisher
	metrics        *Metrics
}

// roundSkipCertificateWanted reports whether certificate would move the node on,
// before anything is spent on it: the reactor drops one that would not before
// it reaches the WAL, and the action checks again on the state it applies to.
func roundSkipCertificateWanted(stateData *StateData, certificate *types.RoundSkipCertificate) bool {
	// Before CONSENSUS_VERSION_2, or where a certificate cannot prove what
	// the validator set needs, no round is skipped.
	if !roundSkipping(stateData) {
		return false
	}
	// A certificate for a round this node has left changes nothing. That
	// includes every round it holds a certificate for: applying one moves the
	// node past its round.
	if certificate.Height != stateData.Height || certificate.Round < stateData.Round {
		return false
	}
	// Nor does one for a node that is committing the height: skip votes lock
	// nothing, so +2/3 precommits for a late proposal can exist next to a
	// certificate for its round, and moving rounds would drop the block being
	// assembled for the commit.
	return stateData.Step < cstypes.RoundStepApplyCommit
}

// Execute verifies and applies the certificate.
func (c *AddRoundSkipCertificateAction) Execute(ctx context.Context, stateEvent StateEvent) error {
	stateData := stateEvent.StateData
	event := stateEvent.Data.(*AddRoundSkipCertificateEvent)
	certificate := event.Certificate

	if !roundSkipCertificateWanted(stateData, certificate) {
		return nil
	}
	if !event.Verified {
		if budget := verificationBudgetFromCtx(ctx); budget != nil && !budget.Allow(baseMessageCost) {
			return types.ErrVerificationBudgetExhausted
		}
		if err := certificate.Verify(stateData.state.ChainID, stateData.Validators); err != nil {
			return err
		}
	}

	// Saved by entering the next round below.
	stateData.RoundSkip = certificate
	c.metrics.RoundSkips.Add(1)
	c.logger.Info("skipping round: +2/3 of the validators stopped hearing from its proposer",
		"height", certificate.Height,
		"round", certificate.Round,
		"next_round", certificate.Round+1)
	c.eventPublisher.emitter.Emit(roundSkipCertificateEventName, certificate)

	return stateEvent.Ctrl.Dispatch(ctx, &EnterNewRoundEvent{
		Height: certificate.Height,
		Round:  certificate.Round + 1,
	}, stateData)
}
