package consensus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"reflect"
	"time"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	"github.com/dashpay/tenderdash/internal/eventbus"
	"github.com/dashpay/tenderdash/internal/proxy"
	sm "github.com/dashpay/tenderdash/internal/state"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

var crc32c = crc32.MakeTable(crc32.Castagnoli)

// Functionality to replay blocks and messages on recovery from a crash.
// There are two general failure scenarios:
//
//  1. failure during consensus
//  2. failure while applying the block
//
// The former is handled by the WAL, the latter by the proxyApp Handshake on
// restart, which ultimately hands off the work to the WAL.

//-----------------------------------------
// 1. Recover from failure during consensus
// (by replaying messages from the WAL)
//-----------------------------------------

// Unmarshal and apply a single message to the consensus state as if it were
// received in receiveRoutine.  Lines that start with "#" are ignored.
// NOTE: receiveRoutine should not be running.
func (cs *State) readReplayMessage(ctx context.Context, msg *TimedWALMessage, newStepSub eventbus.Subscription) error {
	// Skip meta messages which exist for demarcating boundaries.
	if _, ok := msg.Msg.(EndHeightMessage); ok {
		return nil
	}

	stateData := cs.stateDataStore.Get()

	// for logging
	switch m := msg.Msg.(type) {
	case types.EventDataRoundState:
		cs.logger.Trace("Replay: New Step", "height", m.Height, "round", m.Round, "step", m.Step)
		// these are playback checks
		if newStepSub != nil {
			ctxto, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			stepMsg, err := newStepSub.Next(ctxto)
			if errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("subscription timed out: %w", err)
			} else if err != nil {
				return fmt.Errorf("subscription canceled: %w", err)
			}
			m2 := stepMsg.Data().(types.EventDataRoundState)
			if m.Height != m2.Height || m.Round != m2.Round || m.Step != m2.Step {
				return fmt.Errorf("roundState mismatch. Got %v; Expected %v", m2, m)
			}
		}
	case msgInfo:
		peerID := m.PeerID
		if peerID == "" {
			peerID = "local"
		}
		switch msg := m.Msg.(type) {
		case *ProposalMessage:
			p := msg.Proposal
			if cs.config.WalSkipRoundsToLast && p.Round > stateData.Round {
				stateData.Votes.SetRound(p.Round)
				stateData.Round = p.Round
			}
			cs.logger.Trace("Replay: Proposal", "height", p.Height, "round", p.Round, "cs.Round", stateData.Round,
				"header", p.BlockID.PartSetHeader, "pol", p.POLRound, "peer", peerID)
		case *BlockPartMessage:
			cs.logger.Trace("Replay: BlockPart", "height", msg.Height, "round", msg.Round, "peer", peerID)
		case *VoteMessage:
			v := msg.Vote
			cs.logger.Trace("Replay: Vote", "height", v.Height, "round", v.Round, "type", v.Type,
				"blockID", v.BlockID, "peer", peerID)
		}
		_ = cs.msgDispatcher.dispatch(ctx, &stateData, m, msgFromReplay())
	case timeoutInfo:
		cs.logger.Trace("Replay: Timeout", "height", m.Height, "round", m.Round, "step", m.Step, "dur", m.Duration)
		cs.handleTimeout(ctx, m, &stateData)
	default:
		return fmt.Errorf("replay: Unknown TimedWALMessage type: %v", reflect.TypeOf(msg.Msg))
	}
	err := stateData.Save()
	if err != nil {
		return fmt.Errorf("failed to update state-data: %w", err)
	}
	return nil
}

// Replay only those messages since the last block.  `timeoutRoutine` should
// run concurrently to read off tickChan.
func (cs *State) catchupReplay(ctx context.Context, stateData StateData) error {
	csHeight := stateData.Height
	// Set replayMode to true so we don't log signing errors.
	cs.replayMode = true
	cs.emitter.Emit(setReplayModeEventName, cs.replayMode)
	defer func() {
		cs.replayMode = false
		cs.emitter.Emit(setReplayModeEventName, cs.replayMode)
	}()

	// Ensure that #ENDHEIGHT for this height doesn't exist.
	// NOTE: This is just a sanity check. As far as we know things work fine
	// without it, and Handshake could reuse State if it weren't for
	// this check (since we can crash after writing #ENDHEIGHT).
	//
	// Ignore data corruption errors since this is a sanity check.
	gr, found, err := cs.wal.SearchForEndHeight(
		csHeight,
		&WALSearchOptions{IgnoreDataCorruptionErrors: true},
	)
	if err != nil {
		return err
	}
	if gr != nil {
		if err := gr.Close(); err != nil {
			return err
		}
	}
	if found {
		return fmt.Errorf("wal should not contain #ENDHEIGHT %d", csHeight)
	}

	// Search for last height marker.
	//
	// Ignore data corruption errors in previous heights because we only care about last height
	if csHeight < stateData.state.InitialHeight {
		return fmt.Errorf(
			"cannot replay height %v, below initial height %v",
			csHeight,
			stateData.state.InitialHeight,
		)
	}
	endHeight := csHeight - 1
	if csHeight == stateData.state.InitialHeight {
		endHeight = 0
	}
	gr, found, err = cs.wal.SearchForEndHeight(
		endHeight,
		&WALSearchOptions{IgnoreDataCorruptionErrors: true},
	)
	if err == io.EOF {
		cs.logger.Error("Replay: wal.group.Search returned EOF", "#ENDHEIGHT", endHeight)
	} else if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf(
			"cannot replay height %d. WAL does not contain #ENDHEIGHT for %d",
			csHeight,
			endHeight,
		)
	}
	defer gr.Close()

	cs.logger.Info("Catchup by replaying consensus messages", "height", csHeight)

	iter := newWalIter(&WALDecoder{gr}, cs.config.WalSkipRoundsToLast)
	for iter.Next() {
		// NOTE: since the priv key is set when the msgs are received
		// it will attempt to eg double sign but we can just ignore it
		// since the votes will be replayed and we'll get to the next step
		if err := cs.readReplayMessage(ctx, iter.Value(), nil); err != nil {
			return err
		}
	}
	err = iter.Err()
	if err != nil {
		if IsDataCorruptionError(err) {
			cs.logger.Error("data has been corrupted in last height of consensus WAL", "err", err, "height", csHeight)
		}
		return err
	}
	cs.logger.Info("Replay: Done")
	return nil
}

//--------------------------------------------------------------------------------

// Parses marker lines of the form:
// #ENDHEIGHT: 12345
/*
func makeHeightSearchFunc(height int64) auto.SearchFunc {
	return func(line string) (int, error) {
		line = strings.TrimRight(line, "\n")
		parts := strings.Split(line, " ")
		if len(parts) != 2 {
			return -1, errors.New("line did not have 2 parts")
		}
		i, err := strconv.Atoi(parts[1])
		if err != nil {
			return -1, errors.New("failed to parse INFO: " + err.Error())
		}
		if height < i {
			return 1, nil
		} else if height == i {
			return 0, nil
		} else {
			return -1, nil
		}
	}
}*/

//---------------------------------------------------
// 2. Recover from failure while applying the block.
// (by handshaking with the app to figure out where
// we were last, and using the WAL to recover there.)
//---------------------------------------------------

type Handshaker struct {
	initialState sm.State
	logger       log.Logger
	replayer     *BlockReplayer
}

func NewHandshaker(
	blockReplayer *BlockReplayer,
	logger log.Logger,
	state sm.State,
) *Handshaker {
	return &Handshaker{
		initialState: state,
		logger:       logger,
		replayer:     blockReplayer,
	}
}

// NBlocks returns the number of blocks applied to the state.
func (h *Handshaker) NBlocks() int {
	return h.replayer.nBlocks
}

// TODO: retry the handshake/replay if it fails ?
func (h *Handshaker) Handshake(ctx context.Context, appClient abciclient.Client) (uint64, error) {
	// Handshake is done via ABCI Info on the query conn.
	res, err := appClient.Info(ctx, &proxy.RequestInfo)
	if err != nil {
		return 0, fmt.Errorf("error calling Info: %v", err)
	}

	blockHeight := res.LastBlockHeight
	if blockHeight < 0 {
		return 0, fmt.Errorf("got a negative last block height (%d) from the app", blockHeight)
	}
	appHash := tmbytes.HexBytes(res.LastBlockAppHash)

	h.logger.Info("ABCI Handshake App Info",
		"height", blockHeight,
		"hash", appHash,
		"software-version", res.Version,
		"protocol-version", res.AppVersion,
	)

	// Only set the version if there is no existing state.
	if h.initialState.LastBlockHeight == 0 {
		h.initialState.Version.Consensus.App = res.AppVersion
	}

	// Replay blocks up to the latest in the blockstore.
	_, err = h.replayer.Replay(ctx, h.initialState, appHash, blockHeight)
	if err != nil {
		return 0, fmt.Errorf("error on replay: %w", err)
	}

	h.logger.Info("Completed ABCI Handshake - Tendermint and App are synced",
		"appHeight", blockHeight, "appHash", appHash)

	// TODO: (on restart) replay mempool

	return res.AppVersion, nil
}

func checkAppHashEqualsOneFromBlock(appHash []byte, block *types.Block) error {
	if !bytes.Equal(appHash, block.AppHash) {
		return fmt.Errorf(`block.AppHash does not match AppHash after replay. Got '%X', expected '%X'.

Block: %v`,
			appHash, block.AppHash, block)
	}
	return nil
}

func checkAppHashEqualsOneFromState(appHash []byte, state sm.State) error {
	if !bytes.Equal(appHash, state.LastAppHash) {
		return fmt.Errorf(`state.AppHash does not match AppHash after replay. Got '%X', expected '%X'.

State: %v

Did you reset Tendermint without resetting your application's data?`,
			appHash, state.LastAppHash, state)
	}

	return nil
}
