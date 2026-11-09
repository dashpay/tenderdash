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
	// Record is the message the certificate came in, written to the WAL once
	// the certificate turns out news and valid, before it is applied. Nil for
	// one replayed from the WAL.
	Record WALMessage
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
	wal            WALWriter
}

// roundSkipCertificateWanted reports whether certificate is news to the node,
// before anything is spent on it: the reactor drops one that is not before it
// is queued, and the action checks again on the state it applies to.
//
// News is a certificate for a later round than the one the node holds, not
// only one that moves the node on. A node can reach a later round without a
// certificate, by following +2/3 prevotes of that round, while peers it
// connects to still wait in the skipped round. Nothing but the certificate
// moves those peers on: the skipped round has no votes to send them, and the
// later round's votes and proposal go only to peers in it.
func roundSkipCertificateWanted(stateData *StateData, certificate *types.RoundSkipCertificate) bool {
	// Before CONSENSUS_VERSION_2, or where a certificate cannot prove what
	// the validator set needs, no round is skipped.
	if !roundSkipping(stateData) || certificate.Height != stateData.Height {
		return false
	}
	// One for a round no later than the one it holds is not news.
	if held := stateData.RoundSkip; held != nil && certificate.Round <= held.Round {
		return false
	}
	// Nor is one for a node that is committing the height: skip votes lock
	// nothing, so +2/3 precommits for a late proposal can exist next to a
	// certificate for its round, and moving rounds would drop the block being
	// assembled for the commit. A peer behind catches up on the commit.
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
	// Written only now, so replay checks no signature that did not verify:
	// a peer could send any number of certificates that look like news and
	// do not, and replay draws on no verification budget. A certificate
	// carries no signature of this node's, so it needs no fsync; one lost in
	// a crash is sent again by peers.
	if event.Record != nil {
		if err := c.wal.Write(event.Record); err != nil {
			c.logger.Error("failed writing round skip certificate to WAL", "error", err)
		}
	}

	// Kept, and broadcast, whether or not it moves the node on, for the peers
	// still in the round it skips (see roundSkipGossipHandler).
	stateData.RoundSkip = certificate
	c.eventPublisher.emitter.Emit(roundSkipCertificateEventName, certificate)
	if certificate.Round < stateData.Round {
		c.logger.Debug("keeping round skip certificate for peers in the round it skips",
			"height", certificate.Height,
			"round", certificate.Round,
			"node_round", stateData.Round)
		return nil
	}

	c.metrics.RoundSkips.Add(1)
	c.logger.Info("skipping round: +2/3 of the validators stopped hearing from its proposer",
		"height", certificate.Height,
		"round", certificate.Round,
		"next_round", certificate.Round+1)
	return stateEvent.Ctrl.Dispatch(ctx, &EnterNewRoundEvent{
		Height: certificate.Height,
		Round:  certificate.Round + 1,
	}, stateData)
}
