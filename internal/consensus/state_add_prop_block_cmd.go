package consensus

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/gogo/protobuf/proto"

	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	tmstrings "github.com/tendermint/tendermint/internal/libs/strings"
	"github.com/tendermint/tendermint/libs/log"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/types"
)

type AddProposalBlockPartEvent struct {
	Msg        *BlockPartMessage
	PeerID     types.NodeID
	FromReplay bool
}

// GetType returns AddProposalBlockPartType event-type
func (e *AddProposalBlockPartEvent) GetType() EventType {
	return AddProposalBlockPartType
}

// AddProposalBlockPartCommand ...
// NOTE: block is not necessarily valid.
// Asynchronously triggers either enterPrevote (before we timeout of propose) or tryFinalizeCommit,
// once we have the full block.
type AddProposalBlockPartCommand struct {
	logger         log.Logger
	metrics        *Metrics
	blockExec      *blockExecutor
	eventPublisher *EventPublisher
	statsQueue     *chanQueue[msgInfo]
}

// Execute ...
func (c *AddProposalBlockPartCommand) Execute(ctx context.Context, stateEvent StateEvent) error {
	event := stateEvent.Data.(*AddProposalBlockPartEvent)
	stateData := stateEvent.StateData
	commitNotExist := stateData.Commit == nil
	var (
		added bool
		err   error
	)
	defer func() {
		if added {
			_ = c.statsQueue.send(ctx, msgInfoFromCtx(ctx))
		}
	}()
	// if the proposal is complete, we'll enterPrevote or tryFinalizeCommit
	added, err = c.addProposalBlockPart(ctx, stateEvent.FSM, stateData, event.Msg, event.PeerID)
	if err != nil {
		return err
	}

	if added && commitNotExist && stateData.ProposalBlockParts.IsComplete() {
		return stateEvent.FSM.Dispatch(ctx, &ProposalCompletedEvent{
			Height:     event.Msg.Height,
			FromReplay: event.FromReplay,
		}, stateData)
	}
	return nil
}

func (c *AddProposalBlockPartCommand) addProposalBlockPart(
	ctx context.Context,
	fsm *FSM,
	stateData *StateData,
	msg *BlockPartMessage,
	peerID types.NodeID,
) (bool, error) {
	height, round, part := msg.Height, msg.Round, msg.Part
	c.logger.Info(
		"addProposalBlockPart",
		"height", stateData.Height,
		"round", stateData.Round,
		"msg_height", height,
		"msg_round", round,
	)

	// Blocks might be reused, so round mismatch is OK
	if stateData.Height != height {
		c.logger.Debug(
			"received block part from wrong height",
			"height", stateData.Height,
			"round", stateData.Round,
			"msg_height", height,
			"msg_round", round)
		c.metrics.BlockGossipPartsReceived.With("matches_current", "false").Add(1)
		return false, nil
	}

	// We're not expecting a block part.
	if stateData.ProposalBlockParts == nil {
		c.metrics.BlockGossipPartsReceived.With("matches_current", "false").Add(1)
		// NOTE: this can happen when we've gone to a higher round and
		// then receive parts from the previous round - not necessarily a bad peer.
		c.logger.Debug(
			"received a block part when we are not expecting any",
			"height", stateData.Height,
			"round", stateData.Round,
			"block_height", height,
			"block_round", round,
			"index", part.Index,
			"peer", peerID,
		)
		return false, nil
	}

	added, err := stateData.ProposalBlockParts.AddPart(part)
	if err != nil {
		if errors.Is(err, types.ErrPartSetInvalidProof) || errors.Is(err, types.ErrPartSetUnexpectedIndex) {
			c.metrics.BlockGossipPartsReceived.With("matches_current", "false").Add(1)
		}
		return added, err
	}

	c.metrics.BlockGossipPartsReceived.With("matches_current", "true").Add(1)

	if stateData.ProposalBlockParts.ByteSize() > stateData.state.ConsensusParams.Block.MaxBytes {
		return added, fmt.Errorf("total size of proposal block parts exceeds maximum block bytes (%d > %d)",
			stateData.ProposalBlockParts.ByteSize(), stateData.state.ConsensusParams.Block.MaxBytes,
		)
	}
	if added && stateData.ProposalBlockParts.IsComplete() {
		c.metrics.MarkBlockGossipComplete()
		bz, err := io.ReadAll(stateData.ProposalBlockParts.GetReader())
		if err != nil {
			return added, err
		}

		var pbb = new(tmproto.Block)
		err = proto.Unmarshal(bz, pbb)
		if err != nil {
			return added, err
		}

		block, err := types.BlockFromProto(pbb)
		if err != nil {
			return added, err
		}

		if stateData.RoundState.Proposal != nil &&
			block.Header.CoreChainLockedHeight != stateData.RoundState.Proposal.CoreChainLockedHeight {
			return added, fmt.Errorf("core chain lock height of block %d does not match proposal %d",
				block.Header.CoreChainLockedHeight, stateData.RoundState.Proposal.CoreChainLockedHeight)
		}

		stateData.ProposalBlock = block
		err = stateData.Save()
		if err != nil {
			return false, err
		}

		// NOTE: it's possible to receive complete proposal blocks for future rounds without having the proposal
		c.logger.Info(
			"received complete proposal block",
			"height", stateData.ProposalBlock.Height,
			"hash", stateData.ProposalBlock.Hash(),
			"round_height", stateData.RoundState.GetHeight(),
		)

		c.eventPublisher.PublishCompleteProposalEvent(stateData.CompleteProposalEvent())

		if stateData.Commit != nil {
			c.logger.Info("Proposal block fully received", "proposal", stateData.ProposalBlock)
			c.logger.Info("Commit already present", "commit", stateData.Commit)
			c.logger.Debug("adding commit after complete proposal",
				"height", stateData.ProposalBlock.Height,
				"hash", stateData.ProposalBlock.Hash(),
			)
			// We received a commit before the block
			// Transit to AddCommit
			return added, fsm.Dispatch(ctx, &AddCommitEvent{Commit: stateData.Commit}, stateData)
		}

		return added, nil
	}

	return added, nil
}

func (c *AddProposalBlockPartCommand) Subscribe(observer *Observer) {
	observer.Subscribe(SetMetrics, func(a any) error {
		c.metrics = a.(*Metrics)
		return nil
	})
}

type ProposalCompletedEvent struct {
	Height     int64
	FromReplay bool
}

// GetType ...
func (e *ProposalCompletedEvent) GetType() EventType {
	return ProposalCompletedType
}

type ProposalCompletedCommand struct {
	logger log.Logger
}

func (c *ProposalCompletedCommand) Execute(ctx context.Context, stateEvent StateEvent) error {
	stateData := stateEvent.StateData
	event := stateEvent.Data.(*ProposalCompletedEvent)
	height := event.Height
	fromReplay := event.FromReplay

	// Update Valid* if we can.
	prevotes := stateData.Votes.Prevotes(stateData.Round)
	blockID, hasTwoThirds := prevotes.TwoThirdsMajority()
	if hasTwoThirds && !blockID.IsNil() && (stateData.ValidRound < stateData.Round) {
		if stateData.ProposalBlock.HashesTo(blockID.Hash) {
			c.logger.Debug("updating valid block to new proposal block",
				"valid_round", stateData.Round,
				"valid_block_hash", tmstrings.LazyBlockHash(stateData.ProposalBlock))

			stateData.updateValidBlock()
			err := stateData.Save()
			if err != nil {
				return err
			}
		}
		// TODO: In case there is +2/3 majority in Prevotes set for some
		// block and c.ProposalBlock contains different block, either
		// proposer is faulty or voting power of faulty processes is more
		// than 1/3. We should trigger in the future accountability
		// procedure at this point.
	}

	if stateData.Step <= cstypes.RoundStepPropose && stateData.isProposalComplete() {
		// Move onto the next step
		// We should allow old blocks if we are recovering from replay
		c.logger.Debug("entering prevote after complete proposal",
			"height", stateData.ProposalBlock.Height,
			"hash", stateData.ProposalBlock.Hash(),
		)
		err := stateEvent.FSM.Dispatch(ctx, &EnterPrevoteEvent{
			Height:         height,
			Round:          stateData.Round,
			AllowOldBlocks: fromReplay,
		}, stateData)
		if err != nil {
			return err
		}
		if hasTwoThirds { // this is optimisation as this will be triggered when prevote is added
			c.logger.Debug(
				"entering precommit after complete proposal with threshold received",
				"height", stateData.ProposalBlock.Height,
				"hash", stateData.ProposalBlock.Hash(),
			)
			return stateEvent.FSM.Dispatch(ctx, &EnterPrecommitEvent{
				Height: height,
				Round:  stateData.Round,
			}, stateData)
		}
		return nil
	}
	if stateData.Step == cstypes.RoundStepApplyCommit {
		// If we're waiting on the proposal block...
		c.logger.Debug("trying to finalize commit after complete proposal",
			"height", stateData.ProposalBlock.Height,
			"hash", stateData.ProposalBlock.Hash(),
		)
		// Transit to EnterPrecommit
		return stateEvent.FSM.Dispatch(ctx, &TryFinalizeCommitEvent{Height: height}, stateData)
	}
	return nil
}
