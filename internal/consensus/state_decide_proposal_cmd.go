package consensus

import (
	"context"

	tmbytes "github.com/tendermint/tendermint/libs/bytes"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/types"
)

type DecideProposalEvent struct {
	Height int64
	Round  int32
}

type DecideProposalCommand struct {
	logger        log.Logger
	privValidator privValidator
	msgInfoQueue  *msgInfoQueue
	wal           WALWriteFlusher
	metrics       *Metrics
	blockExec     *blockExecutor
	replayMode    bool
}

func (cs *DecideProposalCommand) Execute(ctx context.Context, behaviour *Behaviour, stateEvent StateEvent) (any, error) {
	event := stateEvent.Data.(DecideProposalEvent)
	height := event.Height
	round := event.Round
	appState := stateEvent.AppState
	var block *types.Block
	var blockParts *types.PartSet

	// Decide on block
	if appState.checkValidBlock() {
		// If there is valid block, choose that.
		block, blockParts = appState.ValidBlock, appState.ValidBlockParts
	} else {
		// Create a new proposal block from state/txs from the mempool.
		var err error
		block, err = cs.blockExec.create(ctx, appState, round)
		if err != nil {
			cs.logger.Error("unable to create proposal block", "error", err)
			return nil, nil
		} else if block == nil {
			return nil, nil
		}
		cs.metrics.ProposalCreateCount.Add(1)
		blockParts, err = block.MakePartSet(types.BlockPartSizeBytes)
		if err != nil {
			cs.logger.Error("unable to create proposal block part set", "error", err)
			return nil, nil
		}
	}

	// Flush the WAL. Otherwise, we may not recompute the same proposal to sign,
	// and the privValidator will refuse to sign anything.
	if err := cs.wal.FlushAndSync(); err != nil {
		cs.logger.Error("failed flushing WAL to disk")
	}

	// Make proposal
	propBlockID := block.BlockID(blockParts)
	proposal := types.NewProposal(height, block.CoreChainLockedHeight, round, appState.ValidRound, propBlockID, block.Header.Time)
	proposal.SetCoreChainLockUpdate(block.CoreChainLock)
	p := proposal.ToProto()
	validatorsAtProposalHeight := appState.state.ValidatorsAtHeight(p.Height)
	quorumHash := validatorsAtProposalHeight.QuorumHash

	proTxHash, err := cs.privValidator.GetProTxHash(ctx)
	if err != nil {
		cs.logger.Error(
			"propose step; failed signing proposal; couldn't get proTxHash",
			"height", height,
			"round", round,
			"err", err,
		)
		return nil, nil
	}
	pubKey, err := cs.privValidator.GetPubKey(ctx, quorumHash)
	if err != nil {
		cs.logger.Error(
			"propose step; failed signing proposal; couldn't get pubKey",
			"height", height,
			"round", round,
			"err", err,
		)
		return nil, nil
	}
	messageBytes := types.ProposalBlockSignBytes(appState.state.ChainID, p)
	cs.logger.Debug(
		"signing proposal",
		"height", proposal.Height,
		"round", proposal.Round,
		"proposer_ProTxHash", proTxHash.ShortString(),
		"publicKey", tmbytes.HexBytes(pubKey.Bytes()).ShortString(),
		"proposalBytes", tmbytes.HexBytes(messageBytes).ShortString(),
		"quorumType", validatorsAtProposalHeight.QuorumType,
		"quorumHash", quorumHash.ShortString(),
	)
	// wait the max amount we would wait for a proposal
	ctxto, cancel := context.WithTimeout(ctx, appState.state.ConsensusParams.Timeout.Propose)
	defer cancel()
	if _, err := cs.privValidator.SignProposal(ctxto,
		appState.state.ChainID,
		validatorsAtProposalHeight.QuorumType,
		quorumHash,
		p,
	); err == nil {
		proposal.Signature = p.Signature

		// send proposal and block parts on internal msg queue
		_ = cs.msgInfoQueue.send(ctx, &ProposalMessage{proposal}, "")

		for i := 0; i < int(blockParts.Total()); i++ {
			part := blockParts.GetPart(i)
			_ = cs.msgInfoQueue.send(ctx, &BlockPartMessage{appState.Height, appState.Round, part}, "")
		}

		cs.logger.Debug("signed proposal", "height", height, "round", round, "proposal", proposal, "pubKey", pubKey.HexString())
	} else if !cs.replayMode {
		cs.logger.Error("propose step; failed signing proposal", "height", height, "round", round, "err", err)
	} else {
		cs.logger.Debug("replay; failed signing proposal",
			"height", height,
			"round", round,
			"proposal", proposal,
			"pubKey", pubKey.HexString(),
			"error", err)

	}
	return nil, nil
}

func (cs *DecideProposalCommand) Subscribe(observer *Observer) {
	observer.Subscribe(SetMetrics, func(a any) error {
		cs.metrics = a.(*Metrics)
		return nil
	})
}
