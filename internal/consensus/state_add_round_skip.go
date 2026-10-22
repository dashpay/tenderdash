package consensus

import (
	"context"

	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

// AddRoundSkipCertificateEvent carries a certificate that +2/3 of the validators
// voted to skip a round.
type AddRoundSkipCertificateEvent struct {
	Certificate *types.RoundSkipCertificate
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

// Execute verifies and applies the certificate.
func (c *AddRoundSkipCertificateAction) Execute(ctx context.Context, stateEvent StateEvent) error {
	stateData := stateEvent.StateData
	certificate := stateEvent.Data.(*AddRoundSkipCertificateEvent).Certificate

	if !stateData.state.ConsensusParams.Version.ProposerLivenessEnabled() {
		return nil
	}
	// A certificate for a round this node has left, or already holds one for,
	// changes nothing.
	if certificate.Height != stateData.Height || certificate.Round < stateData.Round {
		return nil
	}
	if stateData.RoundSkip != nil && stateData.RoundSkip.Round >= certificate.Round {
		return nil
	}
	if budget := verificationBudgetFromCtx(ctx); budget != nil && !budget.Allow(baseMessageCost) {
		return types.ErrVerificationBudgetExhausted
	}
	if err := certificate.Verify(stateData.state.ChainID, stateData.Validators); err != nil {
		return err
	}

	stateData.RoundSkip = certificate
	if err := stateData.Save(); err != nil {
		return err
	}
	c.metrics.RoundSkips.Add(1)
	c.logger.Info("skipping round: +2/3 of the validators stopped hearing from its proposer",
		"height", certificate.Height,
		"round", certificate.Round,
		"next_round", certificate.Round+1)
	c.eventPublisher.emitter.Emit(roundSkipCertificateEventName, certificate)

	err := stateEvent.Ctrl.Dispatch(ctx, &EnterNewRoundEvent{
		Height: certificate.Height,
		Round:  certificate.Round + 1,
	}, stateData)
	if err != nil {
		return err
	}
	// A round that waits for transactions announces nothing on entry, and
	// peers send a proposal only to a node they know is in its round. Without
	// this, the next proposer's proposal would reach this node only once it
	// stepped into the propose step on its own.
	c.eventPublisher.PublishNewRoundStepEvent(stateData.RoundState)
	return nil
}
