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

// AddProposalBlockPartCommand ...
// NOTE: block is not necessarily valid.
// Asynchronously triggers either enterPrevote (before we timeout of propose) or tryFinalizeCommit,
// once we have the full block.
type AddProposalBlockPartCommand struct {
	logger         log.Logger
	metrics        *Metrics
	blockExec      *blockExecutor
	eventPublisher *EventPublisher
}

// Execute ...
func (cs *AddProposalBlockPartCommand) Execute(ctx context.Context, behaviour *Behaviour, stateEvent StateEvent) (any, error) {
	event := stateEvent.Data.(AddProposalBlockPartEvent)
	appState := stateEvent.AppState
	commitNotExist := appState.Commit == nil

	added, err := cs.addProposalBlockPart(ctx, behaviour, appState, event.Msg, event.PeerID)
	if err != nil {
		return added, err
	}

	if added && appState.ProposalBlockParts != nil && appState.ProposalBlockParts.IsComplete() && event.FromReplay {
		cs.blockExec.processOrPanic(ctx, appState, event.Msg.Round)
	}

	if added && commitNotExist && appState.ProposalBlockParts.IsComplete() {
		err = cs.handleCompleteProposal(ctx, behaviour, appState, event.Msg.Height, event.FromReplay)
		if err != nil {
			return nil, err
		}
	}
	return added, nil
}

func (cs *AddProposalBlockPartCommand) addProposalBlockPart(
	ctx context.Context,
	behaviour *Behaviour,
	appState *AppState,
	msg *BlockPartMessage,
	peerID types.NodeID,
) (bool, error) {
	height, round, part := msg.Height, msg.Round, msg.Part
	cs.logger.Info(
		"addProposalBlockPart",
		"height", appState.Height,
		"round", appState.Round,
		"msg_height", height,
		"msg_round", round,
	)

	// Blocks might be reused, so round mismatch is OK
	if appState.Height != height {
		cs.logger.Debug(
			"received block part from wrong height",
			"height", appState.Height,
			"round", appState.Round,
			"msg_height", height,
			"msg_round", round)
		cs.metrics.BlockGossipPartsReceived.With("matches_current", "false").Add(1)
		return false, nil
	}

	// We're not expecting a block part.
	if appState.ProposalBlockParts == nil {
		cs.metrics.BlockGossipPartsReceived.With("matches_current", "false").Add(1)
		// NOTE: this can happen when we've gone to a higher round and
		// then receive parts from the previous round - not necessarily a bad peer.
		cs.logger.Debug(
			"received a block part when we are not expecting any",
			"height", appState.Height,
			"round", appState.Round,
			"block_height", height,
			"block_round", round,
			"index", part.Index,
			"peer", peerID,
		)
		return false, nil
	}

	added, err := appState.ProposalBlockParts.AddPart(part)
	if err != nil {
		if errors.Is(err, types.ErrPartSetInvalidProof) || errors.Is(err, types.ErrPartSetUnexpectedIndex) {
			cs.metrics.BlockGossipPartsReceived.With("matches_current", "false").Add(1)
		}
		return added, err
	}

	cs.metrics.BlockGossipPartsReceived.With("matches_current", "true").Add(1)

	if appState.ProposalBlockParts.ByteSize() > appState.state.ConsensusParams.Block.MaxBytes {
		return added, fmt.Errorf("total size of proposal block parts exceeds maximum block bytes (%d > %d)",
			appState.ProposalBlockParts.ByteSize(), appState.state.ConsensusParams.Block.MaxBytes,
		)
	}
	if added && appState.ProposalBlockParts.IsComplete() {
		cs.metrics.MarkBlockGossipComplete()
		bz, err := io.ReadAll(appState.ProposalBlockParts.GetReader())
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

		if appState.RoundState.Proposal != nil &&
			block.Header.CoreChainLockedHeight != appState.RoundState.Proposal.CoreChainLockedHeight {
			return added, fmt.Errorf("core chain lock height of block %d does not match proposal %d",
				block.Header.CoreChainLockedHeight, appState.RoundState.Proposal.CoreChainLockedHeight)
		}

		appState.ProposalBlock = block

		// NOTE: it's possible to receive complete proposal blocks for future rounds without having the proposal
		cs.logger.Info(
			"received complete proposal block",
			"height", appState.ProposalBlock.Height,
			"hash", appState.ProposalBlock.Hash(),
			"round_height", appState.RoundState.GetHeight(),
		)

		if appState.ProposalBlock.Height != appState.RoundState.GetHeight() {
			err = cs.blockExec.process(ctx, appState, round)
			if err != nil {
				return false, err
			}
		}

		err = appState.Save()
		if err != nil {
			return false, err
		}

		cs.eventPublisher.PublishCompleteProposalEvent(appState.CompleteProposalEvent())

		if appState.Commit != nil {
			cs.logger.Info("Proposal block fully received", "proposal", appState.ProposalBlock)
			cs.logger.Info("Commit already present", "commit", appState.Commit)
			cs.logger.Debug("adding commit after complete proposal",
				"height", appState.ProposalBlock.Height,
				"hash", appState.ProposalBlock.Hash(),
			)
			// We received a commit before the block
			return behaviour.AddCommit(ctx, appState, AddCommitEvent{Commit: appState.Commit})
		}

		return added, nil
	}

	return added, nil
}

func (cs *AddProposalBlockPartCommand) handleCompleteProposal(
	ctx context.Context,
	behaviour *Behaviour,
	appState *AppState,
	height int64,
	fromReplay bool,
) error {
	// Update Valid* if we can.
	prevotes := appState.Votes.Prevotes(appState.Round)
	blockID, hasTwoThirds := prevotes.TwoThirdsMajority()
	if hasTwoThirds && !blockID.IsNil() && (appState.ValidRound < appState.Round) {
		if appState.ProposalBlock.HashesTo(blockID.Hash) {
			cs.logger.Debug("updating valid block to new proposal block",
				"valid_round", appState.Round,
				"valid_block_hash", tmstrings.LazyBlockHash(appState.ProposalBlock))

			appState.ValidRound = appState.Round
			appState.ValidBlock = appState.ProposalBlock
			appState.ValidBlockParts = appState.ProposalBlockParts
			err := appState.Save()
			if err != nil {
				return err
			}
		}
		// TODO: In case there is +2/3 majority in Prevotes set for some
		// block and cs.ProposalBlock contains different block, either
		// proposer is faulty or voting power of faulty processes is more
		// than 1/3. We should trigger in the future accountability
		// procedure at this point.
	}

	if appState.Step <= cstypes.RoundStepPropose && appState.isProposalComplete() {
		// Move onto the next step
		// We should allow old blocks if we are recovering from replay
		allowOldBlocks := fromReplay
		cs.logger.Debug("entering prevote after complete proposal",
			"height", appState.ProposalBlock.Height,
			"hash", appState.ProposalBlock.Hash(),
		)
		_ = behaviour.EnterPrevote(ctx, appState, EnterPrevoteEvent{
			Height:         height,
			Round:          appState.Round,
			AllowOldBlocks: allowOldBlocks,
		})
		if hasTwoThirds { // this is optimisation as this will be triggered when prevote is added
			cs.logger.Debug(
				"entering precommit after complete proposal with threshold received",
				"height", appState.ProposalBlock.Height,
				"hash", appState.ProposalBlock.Hash(),
			)
			_ = behaviour.EnterPrecommit(ctx, appState, EnterPrecommitEvent{
				Height: height,
				Round:  appState.Round,
			})
		}
	} else if appState.Step == cstypes.RoundStepApplyCommit {
		// If we're waiting on the proposal block...
		cs.logger.Debug("trying to finalize commit after complete proposal",
			"height", appState.ProposalBlock.Height,
			"hash", appState.ProposalBlock.Hash(),
		)
		behaviour.TryFinalizeCommit(ctx, appState, TryFinalizeCommitEvent{Height: height})
	}
	return nil
}

func (cs *AddProposalBlockPartCommand) Subscribe(observer *Observer) {
	observer.Subscribe(SetMetrics, func(a any) error {
		cs.metrics = a.(*Metrics)
		return nil
	})
}
