package consensus

import (
	"context"
	"fmt"
	"time"

	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	sm "github.com/tendermint/tendermint/internal/state"
	tmbytes "github.com/tendermint/tendermint/libs/bytes"
	"github.com/tendermint/tendermint/libs/events"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/types"
)

type proposaler struct {
	logger         log.Logger
	metrics        *Metrics
	privVal        privValidator
	msgInfoQueue   *msgInfoQueue
	blockExec      *blockExecutor
	replayMode     bool
	committedState sm.State
}

func newProposaler(
	logger log.Logger,
	metrics *Metrics,
	privVal privValidator,
	queue *msgInfoQueue,
	blockExec *blockExecutor,
) *proposaler {
	return &proposaler{
		logger:       logger,
		metrics:      metrics,
		privVal:      privVal,
		msgInfoQueue: queue,
		blockExec:    blockExec,
	}
}

func (p *proposaler) Set(proposal *types.Proposal, receivedAt time.Time, rs *cstypes.RoundState) error {
	// Does not apply
	if rs.Proposal != nil || proposal.Height != rs.Height || proposal.Round != rs.Round {
		return nil
	}

	// Verify POLRound, which must be -1 or in range [0, proposal.Round).
	if proposal.POLRound < -1 ||
		(proposal.POLRound >= 0 && proposal.POLRound >= proposal.Round) {
		return ErrInvalidProposalPOLRound
	}

	if proposal.CoreChainLockedHeight < p.committedState.LastCoreChainLockedBlockHeight {
		return ErrInvalidProposalCoreHeight
	}

	err := p.verifyProposal(proposal, rs)
	if err != nil {
		return err
	}
	rs.Proposal = proposal
	rs.ProposalReceiveTime = receivedAt
	p.proposalTimestampDifferenceMetric(*rs)
	// We don't update cs.ProposalBlockParts if it is already set.
	// This happens if we're already in cstypes.RoundStepApplyCommit or if there is a valid block in the current round.
	// TODO: We can check if Proposal is for a different block as this is a sign of misbehavior!
	if rs.ProposalBlockParts == nil {
		p.metrics.MarkBlockGossipStarted()
		rs.ProposalBlockParts = types.NewPartSetFromHeader(proposal.BlockID.PartSetHeader)
	}

	p.logger.Info("received proposal", "proposal", proposal)
	return nil
}

func (p *proposaler) Decide(ctx context.Context, height int64, round int32, rs *cstypes.RoundState) error {
	// If there is valid block, choose that.
	block, blockParts := rs.ValidBlock, rs.ValidBlockParts
	// Decide on block
	if !p.checkValidBlock(rs) {
		var err error
		block, blockParts, err = p.createProposalBlock(ctx, round, rs)
		if err != nil {
			return err
		}
	}
	pubKey, err := p.privVal.GetPubKey(ctx, rs.Validators.QuorumHash)
	if err != nil {
		p.logger.Error("propose step; failed signing proposal; couldn't get pubKey",
			"height", height,
			"round", round,
			"error", err)
		return err
	}
	logger := p.logger.With(
		"height", height,
		"round", round,
		"pubKey", pubKey.HexString())
	// Make proposal
	proposal := makeProposal(height, round, rs.ValidRound, block, blockParts)
	// Sign proposal
	err = p.signProposal(ctx, height, proposal)
	if err != nil {
		if !p.replayMode {
			logger.Error("propose step; failed signing proposal", "error", err)
			return err
		}
		p.logger.Debug("replay; failed signing proposal", "proposal", proposal, "error", err)
		return err
	}
	p.logger.Debug("signed proposal", "proposal", proposal)
	p.sendMessages(ctx, &ProposalMessage{proposal})
	p.sendMessages(ctx, blockPartsToMessages(rs.Height, rs.Round, blockParts)...)
	return nil
}

func (p *proposaler) createProposalBlock(ctx context.Context, round int32, rs *cstypes.RoundState) (*types.Block, *types.PartSet, error) {
	// Create a new proposal block from state/txs from the mempool.
	block, err := p.blockExec.create(ctx, rs, round)
	if err != nil {
		p.logger.Error("unable to create proposal block", "error", err)
		return nil, nil, err
	}
	if block == nil {
		return nil, nil, err
	}
	p.metrics.ProposalCreateCount.Add(1)
	blockParts, err := block.MakePartSet(types.BlockPartSizeBytes)
	if err != nil {
		p.logger.Error("unable to create proposal block part set", "error", err)
		return nil, nil, err
	}
	return block, blockParts, nil
}

func (p *proposaler) signProposal(ctx context.Context, height int64, proposal *types.Proposal) error {
	protoProposal := proposal.ToProto()

	// validator-set at a proposal height
	valSetAtHeight := p.committedState.ValidatorsAtHeight(height)
	quorumHash := valSetAtHeight.QuorumHash

	// wait the max amount we would wait for a proposal
	ctxto, cancel := context.WithTimeout(ctx, p.committedState.ConsensusParams.Timeout.Propose)
	defer cancel()

	_, err := p.privVal.SignProposal(ctxto, p.committedState.ChainID, valSetAtHeight.QuorumType, quorumHash, protoProposal)
	if err != nil {
		return err
	}
	proposal.Signature = protoProposal.Signature
	return nil
}

func (p *proposaler) checkValidBlock(rs *cstypes.RoundState) bool {
	if rs.ValidBlock == nil {
		return false
	}
	sp := p.committedState.ConsensusParams.Synchrony.SynchronyParamsOrDefaults()
	if rs.Height == p.committedState.InitialHeight {
		// by definition, initial block must have genesis time
		return rs.ValidBlock.Time.Equal(p.committedState.LastBlockTime)
	}
	if !rs.ValidBlock.IsTimely(rs.ValidBlockRecvTime, sp, rs.ValidRound) {
		p.logger.Debug(
			"proposal block is outdated",
			"height", rs.Height,
			"round", rs.ValidRound,
			"received", rs.ValidBlockRecvTime,
			"block", rs.ValidBlock)
		return false
	}
	return true
}

func (p *proposaler) proposalTimestampDifferenceMetric(rs cstypes.RoundState) {
	if rs.Proposal != nil && rs.Proposal.POLRound == -1 {
		sp := p.committedState.ConsensusParams.Synchrony.SynchronyParamsOrDefaults()
		recvTime := rs.ProposalReceiveTime
		if rs.Height == p.committedState.InitialHeight {
			recvTime = p.committedState.LastBlockTime // genesis time
		}
		isTimely := rs.Proposal.IsTimely(recvTime, sp, rs.Round)
		p.metrics.ProposalTimestampDifference.With("is_timely", fmt.Sprintf("%t", isTimely)).
			Observe(rs.ProposalReceiveTime.Sub(rs.Proposal.Timestamp).Seconds())
	}
}

func (p *proposaler) sendMessages(ctx context.Context, msgs ...Message) {
	for _, msg := range msgs {
		_ = p.msgInfoQueue.send(ctx, msg, "")
	}
}

func (p *proposaler) verifyProposal(proposal *types.Proposal, rs *cstypes.RoundState) error {
	protoProposal := proposal.ToProto()
	stateValSet := p.committedState.Validators
	// Verify signature
	proposalBlockSignID := types.ProposalBlockSignID(
		p.committedState.ChainID,
		protoProposal,
		stateValSet.QuorumType,
		stateValSet.QuorumHash,
	)
	vset := rs.Validators
	height := rs.Height
	proposer := vset.GetProposer()
	if proposer.PubKey == nil {
		return p.verifyProposalForNonValidatorSet(proposal, *rs)
	}
	// We are part of the validator set
	if !proposer.PubKey.VerifySignatureDigest(proposalBlockSignID, proposal.Signature) {
		p.logger.Debug(
			"error verifying signature",
			"height", height,
			"proposal_height", proposal.Height,
			"proposal_round", proposal.Round,
			"proposal", proposal,
			"proposer_proTxHash", proposer.ProTxHash.ShortString(),
			"proposer_pubkey", proposer.PubKey.HexString(),
			"quorumType", stateValSet.QuorumType,
			"quorumHash", stateValSet.QuorumHash,
			"proposalSignId", tmbytes.HexBytes(proposalBlockSignID))
		return ErrInvalidProposalSignature
	}
	return nil
}

func (p *proposaler) verifyProposalForNonValidatorSet(proposal *types.Proposal, rs cstypes.RoundState) error {
	commit := rs.Commit
	if commit == nil && commit.Height != proposal.Height || commit.Round != proposal.Round {
		// We received a proposal we can not check
		return ErrUnableToVerifyProposal
	}
	// We are not part of the validator set
	// We might have a commit already for the Round State
	// We need to verify that the commit block id is equal to the proposal block id
	if !proposal.BlockID.Equals(commit.BlockID) {
		proposer := rs.Validators.GetProposer()
		p.logger.Debug("proposal blockID isn't the same as the commit blockID",
			"height", proposal.Height,
			"round", proposal.Round,
			"proposer_proTxHash", proposer.ProTxHash.ShortString())
		return ErrInvalidProposalForCommit
	}
	return nil
}

func (p *proposaler) subscribe(evsw events.EventSwitch) {
	const listenerID = "propDecider"
	_ = evsw.AddListenerForEvent(listenerID, committedStateUpdate, func(obj events.EventData) error {
		p.committedState = obj.(sm.State)
		return nil
	})
	_ = evsw.AddListenerForEvent(listenerID, setReplayMode, func(obj events.EventData) error {
		p.replayMode = obj.(bool)
		return nil
	})
	_ = evsw.AddListenerForEvent(listenerID, setPrivValidator, func(obj events.EventData) error {
		p.privVal = obj.(privValidator)
		return nil
	})
}

func makeProposal(height int64, round, polRound int32, block *types.Block, blockParts *types.PartSet) *types.Proposal {
	propBlockID := block.BlockID(blockParts)
	proposal := types.NewProposal(
		height,
		block.CoreChainLockedHeight,
		round,
		polRound,
		propBlockID,
		block.Header.Time,
	)
	proposal.SetCoreChainLockUpdate(block.CoreChainLock)
	return proposal
}

func blockPartsToMessages(height int64, round int32, blockParts *types.PartSet) []Message {
	msgs := make([]Message, blockParts.Total())
	for i := 0; i < int(blockParts.Total()); i++ {
		part := blockParts.GetPart(i)
		msgs[i] = &BlockPartMessage{height, round, part}
	}
	return msgs
}
