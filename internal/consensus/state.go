package consensus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"time"

	sync "github.com/sasha-s/go-deadlock"

	"github.com/dashpay/tenderdash/config"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/internal/eventbus"
	"github.com/dashpay/tenderdash/internal/jsontypes"
	"github.com/dashpay/tenderdash/internal/libs/autofile"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/eventemitter"
	"github.com/dashpay/tenderdash/libs/log"
	tmos "github.com/dashpay/tenderdash/libs/os"
	"github.com/dashpay/tenderdash/libs/service"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	"github.com/dashpay/tenderdash/types"
)

// consensus events
const (
	setProposedAppVersionEventName = "setProposedAppVersion"
	setPrivValidatorEventName      = "setPrivValidator"
	setReplayModeEventName         = "setReplayMode"
	committedStateUpdateEventName  = "committedStateUpdate"
)

// Consensus sentinel errors
var (
	ErrInvalidProposalNotSet     = errors.New("error invalid proposal not set")
	ErrInvalidProposalForCommit  = errors.New("error invalid proposal for commit")
	ErrUnableToVerifyProposal    = errors.New("error unable to verify proposal")
	ErrInvalidProposalSignature  = errors.New("error invalid proposal signature")
	ErrInvalidProposalCoreHeight = errors.New("error invalid proposal core height")
	ErrInvalidProposalPOLRound   = errors.New("error invalid proposal POL round")
	ErrAddingVote                = errors.New("error adding vote")

	ErrPrivValidatorNotSet = errors.New("priv-validator is not set")
)

// msgInfoQueue must fit info about all votes from all validators 100 for Dash Evo mainnet),
// multiplied by number of peers that can broadcast these votes (also assuming 100),
// multiplied by some factor (here 2) to address multiple rounds.
//
// Note this allows potential OOM condition if we put 20 000 proposal block of size
// types.BlockPartSizeBytes==64 kB, what gives us 1.2 GB of memory usage.
var msgQueueSize = 100 * 100 * 2

// msgs from the reactor which may update the state
type msgInfo struct {
	Msg         Message
	PeerID      types.NodeID
	ReceiveTime time.Time
}

func (msgInfo) TypeTag() string { return "tendermint/wal/MsgInfo" }

func (msgInfo) ValidateBasic() error { return nil }

type msgInfoJSON struct {
	Msg         json.RawMessage `json:"msg"`
	PeerID      types.NodeID    `json:"peer_key"`
	ReceiveTime time.Time       `json:"receive_time"`
}

func (m msgInfo) MarshalJSON() ([]byte, error) {
	msg, err := jsontypes.Marshal(m.Msg)
	if err != nil {
		return nil, err
	}
	return json.Marshal(msgInfoJSON{Msg: msg, PeerID: m.PeerID, ReceiveTime: m.ReceiveTime})
}

func (m *msgInfo) UnmarshalJSON(data []byte) error {
	var msg msgInfoJSON
	if err := json.Unmarshal(data, &msg); err != nil {
		return err
	}
	if err := jsontypes.Unmarshal(msg.Msg, &m.Msg); err != nil {
		return err
	}
	m.PeerID = msg.PeerID
	return nil
}

// internally generated messages which may update the state
type timeoutInfo struct {
	Duration time.Duration         `json:"duration,string"`
	Height   int64                 `json:"height,string"`
	Round    int32                 `json:"round"`
	Step     cstypes.RoundStepType `json:"step"`
}

func (timeoutInfo) TypeTag() string { return "tendermint/wal/TimeoutInfo" }

func (ti *timeoutInfo) String() string {
	return fmt.Sprintf("%v ; %d/%d %v", ti.Duration, ti.Height, ti.Round, ti.Step)
}

// interface to the mempool
type txNotifier interface {
	TxsAvailable() <-chan struct{}
}

// interface to the evidence pool
type evidencePool interface {
	// reports conflicting votes to the evidence pool to be processed into evidence
	ReportConflictingVotes(voteA, voteB *types.Vote)
}

// State handles execution of the consensus algorithm.
// It processes votes and proposals, and upon reaching agreement,
// commits blocks to the chain and executes them against the application.
// The internal state machine receives input from peers, the internal validator, and from a timer.
type State struct {
	service.BaseService
	logger log.Logger

	// config details
	config        *config.ConsensusConfig
	privValidator privValidator

	// store blocks and commits
	blockStore sm.BlockStore

	stateStore        sm.Store
	skipBootstrapping bool

	// create and execute blocks
	blockExec *sm.BlockExecutor

	// notify us if txs are available
	txNotifier txNotifier

	// add evidence to the pool
	// when it's detected
	evpool evidencePool

	stateDataStore *StateDataStore

	mtx sync.RWMutex

	// state changes may be triggered by: msgs from peers,
	// msgs from ourself, or by timeouts
	timeoutTicker TimeoutTicker

	// information about about added votes and block parts are written on this channel
	// so statistics can be computed by reactor
	statsMsgQueue *chanQueue[msgInfo]

	// we use eventBus to trigger msg broadcasts in the reactor,
	// and to notify external subscribers, eg. through a websocket
	eventBus *eventbus.EventBus

	// a Write-Ahead Log ensures we can recover from any kind of crash
	// and helps us avoid signing conflicting votes
	wal          WAL
	replayMode   bool // so we don't log signing errors during replay
	doWALCatchup bool // determines if we even try to do the catchup

	// synchronous pubsub between consensus state and reactor.
	// state only emits EventNewRoundStep, EventValidBlock, and EventVote
	emitter *eventemitter.EventEmitter

	// for reporting metrics
	metrics *Metrics

	// proposer's latest available app protocol version that goes to block header
	proposedAppVersion uint64

	// wait the channel event happening for shutting down the state gracefully
	onStopCh chan *cstypes.RoundState

	msgInfoQueue   *msgInfoQueue
	msgDispatcher  *msgInfoDispatcher
	blockExecutor  *blockExecutor
	eventPublisher *EventPublisher
	voteSigner     *voteSigner
	ctrl           *Controller
	roundScheduler *roundScheduler
	msgMiddlewares []msgMiddlewareFunc

	stopFn func(cs *State) bool
}

// StateOption sets an optional parameter on the State.
type StateOption func(*State)

// SkipStateStoreBootstrap is a state option forces the constructor to
// skip state bootstrapping during construction.
func SkipStateStoreBootstrap(sm *State) {
	sm.skipBootstrapping = true
}

func WithTimeoutTicker(timeoutTicker TimeoutTicker) func(cs *State) {
	return func(cs *State) {
		cs.timeoutTicker = timeoutTicker
	}
}

func WithStopFunc(stopFns ...func(cs *State) bool) func(cs *State) {
	return func(cs *State) {
		// we assume that even if one function returns true, then the consensus must be stopped
		cs.stopFn = func(cs *State) bool {
			for _, fn := range stopFns {
				ret := fn(cs)
				if ret {
					return true
				}
			}
			return false
		}
	}
}

// NewState returns a new State.
func NewState(
	logger log.Logger,
	cfg *config.ConsensusConfig,
	store sm.Store,
	blockExec *sm.BlockExecutor,
	blockStore sm.BlockStore,
	txNotifier txNotifier,
	evpool evidencePool,
	eventBus *eventbus.EventBus,
	options ...StateOption,
) (*State, error) {
	cs := &State{
		eventBus:      eventBus,
		logger:        logger,
		config:        cfg,
		blockExec:     blockExec,
		blockStore:    blockStore,
		stateStore:    store,
		txNotifier:    txNotifier,
		timeoutTicker: NewTimeoutTicker(logger),
		statsMsgQueue: &chanQueue[msgInfo]{
			ch: make(chan msgInfo, msgQueueSize),
		},
		doWALCatchup: true,
		wal:          nilWAL{},
		evpool:       evpool,
		emitter:      eventemitter.New(eventemitter.WithLogger(logger)),
		metrics:      NopMetrics(),
		onStopCh:     make(chan *cstypes.RoundState),
		msgInfoQueue: newMsgInfoQueue(),
	}

	// NOTE: we do not call scheduleRound0 yet, we do that upon Start()
	cs.BaseService = *service.NewBaseService(logger, "State", cs)
	for _, option := range options {
		option(cs)
	}

	cs.stateDataStore = NewStateDataStore(cs.metrics, logger, cfg, cs.emitter)
	wal := &wrapWAL{getter: func() WALWriteFlusher { return cs.wal }}

	cs.voteSigner = &voteSigner{
		privValidator: cs.privValidator,
		logger:        cs.logger,
		queueSender:   cs.msgInfoQueue,
		wal:           wal,
		voteExtender:  cs.blockExec,
	}
	cs.blockExecutor = &blockExecutor{
		logger:             cs.logger,
		privValidator:      cs.privValidator,
		blockExec:          cs.blockExec,
		proposedAppVersion: cs.proposedAppVersion,
	}
	cs.eventPublisher = &EventPublisher{
		emitter:  cs.emitter,
		eventBus: cs.eventBus,
		logger:   cs.logger,
		wal:      wal,
	}
	cs.roundScheduler = &roundScheduler{timeoutTicker: cs.timeoutTicker}
	propler := NewProposaler(cs.logger, cs.metrics, cs.privValidator, cs.msgInfoQueue, cs.blockExecutor)
	cs.ctrl = NewController(cs, wal, cs.statsMsgQueue, propler)
	subs := []eventemitter.Subscriber{propler, cs.blockExecutor, cs.stateDataStore, cs.voteSigner}
	for _, sub := range subs {
		sub.Subscribe(cs.emitter)
	}
	cs.msgDispatcher = newMsgInfoDispatcher(cs.ctrl, propler, wal, cs.logger, cs.msgMiddlewares...)

	// this is not ideal, but it lets the consensus tests start
	// node-fragments gracefully while letting the nodes
	// themselves avoid this.
	if !cs.skipBootstrapping {
		if err := cs.updateStateFromStore(); err != nil {
			return nil, err
		}
	}
	return cs, nil
}

func (cs *State) SetProposedAppVersion(ver uint64) {
	cs.proposedAppVersion = ver
	cs.emitter.Emit(setProposedAppVersionEventName, ver)
}

func (cs *State) updateStateFromStore() error {
	state, err := cs.stateStore.Load()
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}
	if state.IsEmpty() {
		return nil
	}

	stateData := cs.GetStateData()
	eq, err := state.Equals(stateData.state)
	if err != nil {
		return fmt.Errorf("comparing state: %w", err)
	}
	// if the new state is equivalent to the old state, we should not trigger a state update.
	if eq {
		return nil
	}

	// We have no votes, so reconstruct LastCommit from SeenCommit.
	if state.LastBlockHeight > 0 {
		stateData.LastCommit, err = cs.loadLastCommit(state.LastBlockHeight)
		if err != nil {
			panic(fmt.Sprintf("failed to reconstruct last commit; %s", err))
		}
	}

	stateData.updateToState(state, nil, cs.blockStore)
	err = cs.stateDataStore.Update(stateData)
	if err != nil {
		return err
	}
	cs.eventPublisher.PublishNewRoundStepEvent(stateData.RoundState)
	return nil
}

// StateMetrics sets the metrics.
func StateMetrics(metrics *Metrics) StateOption {
	return func(cs *State) { cs.metrics = metrics }
}

// String returns a string.
func (cs *State) String() string {
	// better not to access shared variables
	return "ConsensusState"
}

// GetRoundState returns a shallow copy of the internal consensus state.
func (cs *State) GetRoundState() cstypes.RoundState {
	stateData := cs.GetStateData()
	// NOTE: this might be dodgy, as RoundState itself isn't thread
	// safe as it contains a number of pointers and is explicitly
	// not thread safe.
	return stateData.RoundState // copy
}

// GetRoundStateJSON returns a json of RoundState.
func (cs *State) GetRoundStateJSON() ([]byte, error) {
	stateData := cs.stateDataStore.Get()
	return json.Marshal(stateData.RoundState)
}

// GetRoundStateSimpleJSON returns a json of RoundStateSimple
func (cs *State) GetRoundStateSimpleJSON() ([]byte, error) {
	stateData := cs.stateDataStore.Get()
	return json.Marshal(stateData.RoundState.RoundStateSimple())
}

// GetValidators returns a copy of the current validators.
func (cs *State) GetValidators() (int64, []*types.Validator) {
	stateData := cs.stateDataStore.Get()
	return stateData.state.LastBlockHeight, stateData.state.Validators.Copy().Validators
}

// GetValidatorSet returns a copy of the current validator set.
func (cs *State) GetValidatorSet() (int64, *types.ValidatorSet) {
	stateData := cs.stateDataStore.Get()
	return stateData.state.LastBlockHeight, stateData.state.Validators.Copy()
}

// SetPrivValidator sets the private validator account for signing votes. It
// immediately requests pubkey and caches it.
func (cs *State) SetPrivValidator(ctx context.Context, priv types.PrivValidator) {
	cs.mtx.Lock()
	defer cs.mtx.Unlock()
	defer func() {
		cs.emitter.Emit(setPrivValidatorEventName, cs.privValidator)
	}()
	if priv == nil {
		cs.privValidator = privValidator{}
		cs.logger.Error("attempting to set private validator to nil")
		return
	}
	cs.privValidator = privValidator{PrivValidator: priv}
	err := cs.privValidator.init(ctx)
	if err != nil {
		cs.logger.Error("failed to initialize private validator", "err", err)
		return
	}
}

// OnStart loads the latest state via the WAL, and starts the timeout and
// receive routines.
func (cs *State) OnStart(ctx context.Context) error {
	if err := cs.updateStateFromStore(); err != nil {
		return err
	}

	// We may set the WAL in testing before calling Start, so only OpenWAL if its
	// still the nilWAL.
	if _, ok := cs.wal.(nilWAL); ok {
		if err := cs.loadWalFile(ctx); err != nil {
			return err
		}
	}

	// we need the timeoutRoutine for replay so
	// we don't block on the tick chan.
	// NOTE: we will get a build up of garbage go routines
	// firing on the tockChan until the receiveRoutine is started
	// to deal with them (by that point, at most one will be valid)
	if err := cs.timeoutTicker.Start(ctx); err != nil {
		return err
	}

	// We may have lost some votes if the process crashed reload from consensus
	// log to catchup.
	if cs.doWALCatchup {
		repairAttempted := false

	LOOP:
		for {
			stateData := cs.stateDataStore.Get()
			err := cs.catchupReplay(ctx, stateData)
			switch {
			case err == nil:
				break LOOP

			case !IsDataCorruptionError(err):
				cs.logger.Error("error on catchup replay; proceeding to start state anyway", "err", err)
				break LOOP

			case repairAttempted:
				return err
			}

			cs.logger.Error("the WAL file is corrupted; attempting repair", "err", err)

			// 1) prep work
			cs.wal.Stop()

			repairAttempted = true

			// 2) backup original WAL file
			corruptedFile := fmt.Sprintf("%s.CORRUPTED", cs.config.WalFile())
			if err := tmos.CopyFile(cs.config.WalFile(), corruptedFile); err != nil {
				return err
			}

			cs.logger.Info("backed up WAL file", "src", cs.config.WalFile(), "dst", corruptedFile)

			// 3) try to repair (WAL file will be overwritten!)
			if err := repairWalFile(corruptedFile, cs.config.WalFile()); err != nil {
				cs.logger.Error("the WAL repair failed", "err", err)
				return err
			}

			cs.logger.Info("successful WAL repair")

			// reload WAL file
			if err := cs.loadWalFile(ctx); err != nil {
				return err
			}
		}
	}

	// now start the receiveRoutine
	go cs.receiveRoutine(ctx, cs.stopFn)

	// schedule the first round!
	// use GetRoundState so we don't race the receiveRoutine for access
	cs.roundScheduler.ScheduleRound0(cs.GetRoundState())

	return nil
}

// loadWalFile loads WAL data from file. It overwrites cs.wal.
func (cs *State) loadWalFile(ctx context.Context) error {
	wal, err := cs.OpenWAL(ctx, cs.config.WalFile())
	if err != nil {
		cs.logger.Error("failed to load state WAL", "err", err)
		return err
	}

	cs.wal = wal
	return nil
}

func (cs *State) getOnStopCh() chan *cstypes.RoundState {
	cs.mtx.RLock()
	defer cs.mtx.RUnlock()

	return cs.onStopCh
}

// OnStop implements service.Service.
func (cs *State) OnStop() {
	stateData := cs.stateDataStore.Get()
	// If the node is committing a new block, wait until it is finished!
	if cs.GetRoundState().Step == cstypes.RoundStepApplyCommit {
		select {
		case <-cs.getOnStopCh():
		case <-time.After(stateData.state.ConsensusParams.Timeout.Vote):
			// we wait vote timeout, just in case
			cs.logger.Error("OnStop: timeout waiting for commit to finish", "time", stateData.state.ConsensusParams.Timeout.Vote)
		}
	}

	if cs.timeoutTicker.IsRunning() {
		cs.timeoutTicker.Stop()
	}
	// WAL is stopped in receiveRoutine.
}

// OpenWAL opens a file to log all consensus messages and timeouts for
// deterministic accountability.
func (cs *State) OpenWAL(ctx context.Context, walFile string) (WAL, error) {
	wal, err := NewWAL(ctx, cs.logger.With("wal", walFile), walFile)
	if err != nil {
		cs.logger.Error("failed to open WAL", "file", walFile, "err", err)
		return nil, err
	}

	if err := wal.Start(ctx); err != nil {
		cs.logger.Error("failed to start WAL", "err", err)
		return nil, err
	}

	return wal, nil
}

//------------------------------------------------------------
// Public interface for passing messages into the consensus state, possibly causing a state transition.
// If peerID == "", the msg is considered internal.
// Messages are added to the appropriate queue (peer or internal).
// If the queue is full, the function may block.
// TODO: should these return anything or let callers just use events?

// SetProposal inputs a proposal.
func (cs *State) SetProposal(ctx context.Context, proposal *types.Proposal, peerID types.NodeID) error {
	return cs.msgInfoQueue.send(ctx, &ProposalMessage{proposal}, peerID)
}

// AddProposalBlockPart inputs a part of the proposal block.
func (cs *State) AddProposalBlockPart(ctx context.Context, height int64, round int32, part *types.Part, peerID types.NodeID) error {
	return cs.msgInfoQueue.send(ctx, &BlockPartMessage{height, round, part}, peerID)
}

// SetProposalAndBlock inputs the proposal and all block parts.
func (cs *State) SetProposalAndBlock(
	ctx context.Context,
	proposal *types.Proposal,
	parts *types.PartSet,
	peerID types.NodeID,
) error {
	if err := cs.SetProposal(ctx, proposal, peerID); err != nil {
		return err
	}

	for i := 0; i < int(parts.Total()); i++ {
		part := parts.GetPart(i)
		if err := cs.AddProposalBlockPart(ctx, proposal.Height, proposal.Round, part, peerID); err != nil {
			return err
		}
	}

	return nil
}

func (cs *State) GetStateData() StateData {
	return cs.stateDataStore.Get()
}

// PrivValidator returns safely a PrivValidator
func (cs *State) PrivValidator() types.PrivValidator {
	cs.mtx.RLock()
	defer cs.mtx.RUnlock()
	return cs.privValidator
}

//------------------------------------------------------------
// internal functions for managing the state

func (cs *State) sendMessage(ctx context.Context, msg Message, peerID types.NodeID) error {
	return cs.msgInfoQueue.send(ctx, msg, peerID)
}

func (cs *State) loadLastCommit(lastBlockHeight int64) (*types.Commit, error) {
	commit := cs.blockStore.LoadSeenCommit()
	if commit == nil || commit.Height != lastBlockHeight {
		commit = cs.blockStore.LoadBlockCommit(lastBlockHeight)
	}
	if commit == nil {
		return nil, fmt.Errorf("commit for height %v not found", lastBlockHeight)
	}
	return commit, nil
}

//-----------------------------------------
// the main go routines

// receiveRoutine handles messages which may cause state transitions.
// it's argument (n) is the number of messages to process before exiting - use 0 to run forever
// It keeps the RoundState and is the only thing that updates it.
// Updates (state transitions) happen on timeouts, complete proposals, and 2/3 majorities.
// State must be locked before any internal state is updated.
func (cs *State) receiveRoutine(ctx context.Context, stopFn func(*State) bool) {
	onExit := func(cs *State) {
		// NOTE: the internalMsgQueue may have signed messages from our
		// priv_val that haven't hit the WAL, but its ok because
		// priv_val tracks LastSig

		// close wal now that we're done writing to it
		cs.wal.Stop()
		cs.wal.Wait()
		cs.msgInfoQueue.stop()
	}

	defer func() {
		if r := recover(); r != nil {
			cs.logger.Error("CONSENSUS FAILURE!!!", "err", r, "stack", string(debug.Stack()))

			// Make a best-effort attempt to close the WAL, but otherwise do not
			// attempt to gracefully terminate. Once consensus has irrecoverably
			// failed, any additional progress we permit the node to make may
			// complicate diagnosing and recovering from the failure.
			onExit(cs)

			// There are a couple of cases where the we
			// panic with an error from deeper within the
			// state machine and in these cases, typically
			// during a normal shutdown, we can continue
			// with normal shutdown with safety. These
			// cases are:
			if err, ok := r.(error); ok {
				// TODO(creachadair): In ordinary operation, the WAL autofile should
				// never be closed. This only happens during shutdown and production
				// nodes usually halt by panicking. Many existing tests, however,
				// assume a clean shutdown is possible. Prior to #8111, we were
				// swallowing the panic in receiveRoutine, making that appear to
				// work. Filtering this specific error is slightly risky, but should
				// affect only unit tests. In any case, not re-panicking here only
				// preserves the pre-existing behavior for this one error type.
				if errors.Is(err, autofile.ErrAutoFileClosed) {
					return
				}

				// don't re-panic if the panic is just an
				// error and we're already trying to shut down
				if ctx.Err() != nil {
					return

				}
			}

			// Re-panic to ensure the node terminates.
			//
			panic(r)
		}
	}()

	go cs.msgInfoQueue.fanIn(ctx)

	for {
		if stopFn != nil && stopFn(cs) {
			return
		}

		select {
		case <-cs.txNotifier.TxsAvailable():
			stateData := cs.stateDataStore.Get()
			cs.handleTxsAvailable(ctx, &stateData)
			err := stateData.Save()
			if err != nil {
				cs.logger.Error("failed update state-data", "err", err)
			}
		case mi := <-cs.msgInfoQueue.read():
			stateData := cs.stateDataStore.Get()
			err := cs.msgDispatcher.dispatch(ctx, &stateData, mi)
			if err != nil {
				return
			}
			err = stateData.Save()
			if err != nil {
				cs.logger.Error("failed update state-data", "err", err)
			}
		case ti := <-cs.timeoutTicker.Chan(): // tockChan:
			if err := cs.wal.Write(ti); err != nil {
				cs.logger.Error("failed writing to WAL", "err", err)
			}
			stateData := cs.stateDataStore.Get()

			// if the timeout is relevant to the rs
			// go to the next step
			cs.handleTimeout(ctx, ti, &stateData)
			err := cs.stateDataStore.Update(stateData)
			if err != nil {
				cs.logger.Error("failed update state-data", "err", err)
			}
		case <-ctx.Done():
			onExit(cs)
			return

		}
		// TODO should we handle context cancels here?
	}
}

func (cs *State) handleTimeout(
	ctx context.Context,
	ti timeoutInfo,
	stateData *StateData,
) {

	// timeouts must be for current height, round, step
	if ti.Height != stateData.Height || ti.Round < stateData.Round || (ti.Round == stateData.Round && ti.Step < stateData.Step) {
		cs.logger.Trace("ignoring tock because we are ahead",
			"timeout", ti.Duration, "tock_height", ti.Height, "tock_round", ti.Round, "tock_step", ti.Step,
			"height", stateData.Height, "round", stateData.Round, "step", stateData.Step.String(),
		)
		return
	}

	cs.logger.Trace("received tock", "timeout", ti.Duration, "height", ti.Height, "round", ti.Round, "step", ti.Step)

	// the timeout will now cause a state transition
	cs.mtx.Lock()
	defer cs.mtx.Unlock()

	switch ti.Step {
	case cstypes.RoundStepNewHeight:
		// NewRound event fired from enterNewRoundCommand.
		// XXX: should we fire timeout here (for timeout commit)?
		_ = cs.ctrl.Dispatch(ctx, &EnterNewRoundEvent{Height: ti.Height}, stateData)
	case cstypes.RoundStepNewRound:
		_ = cs.ctrl.Dispatch(ctx, &EnterProposeEvent{Height: ti.Height, Round: ti.Round}, stateData)
	case cstypes.RoundStepPropose:
		if err := cs.eventBus.PublishEventTimeoutPropose(stateData.RoundStateEvent()); err != nil {
			cs.logger.Error("failed publishing timeout propose", "err", err)
		}
		_ = cs.ctrl.Dispatch(ctx, &EnterPrevoteEvent{Height: ti.Height, Round: ti.Round}, stateData)
	case cstypes.RoundStepPrevoteWait:
		if err := cs.eventBus.PublishEventTimeoutWait(stateData.RoundStateEvent()); err != nil {
			cs.logger.Error("failed publishing timeout wait", "err", err)
		}
		_ = cs.ctrl.Dispatch(ctx, &EnterPrecommitEvent{Height: ti.Height, Round: ti.Round}, stateData)
	case cstypes.RoundStepPrecommitWait:
		if err := cs.eventBus.PublishEventTimeoutWait(stateData.RoundStateEvent()); err != nil {
			cs.logger.Error("failed publishing timeout wait", "err", err)
		}
		_ = cs.ctrl.Dispatch(ctx, &EnterPrecommitEvent{Height: ti.Height, Round: ti.Round}, stateData)
		_ = cs.ctrl.Dispatch(ctx, &EnterNewRoundEvent{Height: ti.Height, Round: ti.Round + 1}, stateData)
	default:
		panic(fmt.Sprintf("invalid timeout step: %v", ti.Step))
	}
}

func (cs *State) handleTxsAvailable(ctx context.Context, stateData *StateData) {
	// TODO: Change to trace
	cs.logger.Debug("new transactions are available", "height", stateData.Height, "round", stateData.Round, "step", stateData.Step)
	// We only need to do this for round 0.
	if stateData.Round != 0 {
		return
	}

	switch stateData.Step {
	case cstypes.RoundStepNewHeight: // timeoutCommit phase
		if stateData.state.InitialHeight == stateData.Height {
			// enterPropose will be called by enterNewRoundCommand
			return
		}

		// +1ms to ensure RoundStepNewRound timeout always happens after RoundStepNewHeight
		timeoutCommit := stateData.StartTime.Sub(tmtime.Now()) + 1*time.Millisecond
		cs.roundScheduler.ScheduleTimeout(timeoutCommit, stateData.Height, 0, cstypes.RoundStepNewRound)

	case cstypes.RoundStepNewRound: // after timeoutCommit
		_ = cs.ctrl.Dispatch(ctx, &EnterProposeEvent{Height: stateData.Height, Round: stateData.Round}, stateData)
	}
}

//-----------------------------------------------------------------------------
// State functions
// Used internally by handleTimeout and handleMsg to make state transitions

// CreateProposalBlock safely creates a proposal block.
// Only used in tests.
func (cs *State) CreateProposalBlock(ctx context.Context) (*types.Block, error) {
	stateData := cs.GetStateData()
	return cs.blockExecutor.create(ctx, &stateData.RoundState, stateData.Round)
}

// PublishCommitEvent ...
func (cs *State) PublishCommitEvent(commit *types.Commit) error {
	cs.logger.Trace("publish commit event", "commit", commit)
	if err := cs.eventBus.PublishEventCommit(types.EventDataCommit{Commit: commit}); err != nil {
		return err
	}
	cs.emitter.Emit(types.EventCommitValue, commit)
	return nil
}

//-----------------------------------------------------------------------------

func CompareHRS(h1 int64, r1 int32, s1 cstypes.RoundStepType, h2 int64, r2 int32, s2 cstypes.RoundStepType) int {
	if h1 < h2 {
		return -1
	} else if h1 > h2 {
		return 1
	}
	if r1 < r2 {
		return -1
	} else if r1 > r2 {
		return 1
	}
	if s1 < s2 {
		return -1
	} else if s1 > s2 {
		return 1
	}
	return 0
}

// repairWalFile decodes messages from src (until the decoder errors) and
// writes them to dst.
func repairWalFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	var (
		dec = NewWALDecoder(in)
		enc = NewWALEncoder(out)
	)

	// best-case repair (until first error is encountered)
	for {
		msg, err := dec.Decode()
		if err != nil {
			break
		}

		err = enc.Encode(msg)
		if err != nil {
			return fmt.Errorf("failed to encode msg: %w", err)
		}
	}

	return nil
}

// proposerWaitTime determines how long the proposer should wait to propose its next block.
// If the result is zero, a block can be proposed immediately.
//
// Block times must be monotonically increasing, so if the block time of the previous
// block is larger than the proposer's current time, then the proposer will sleep
// until its local clock exceeds the previous block time.
func proposerWaitTime(t time.Time, bt time.Time) time.Duration {
	if bt.After(t) {
		return bt.Sub(t)
	}
	return 0
}

type privValidator struct {
	types.PrivValidator
	ProTxHash types.ProTxHash
}

func (pv *privValidator) IsProTxHashEqual(proTxHash types.ProTxHash) bool {
	return pv.ProTxHash.Equal(proTxHash)
}

func (pv *privValidator) IsZero() bool {
	return pv.PrivValidator == nil
}

func (pv *privValidator) init(ctx context.Context) error {
	var err error
	pv.ProTxHash, err = pv.GetProTxHash(ctx)
	return err
}
