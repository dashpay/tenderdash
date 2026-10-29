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
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
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
	ErrInvalidProposalForPartSet = errors.New("error invalid proposal for the block parts being collected")
	ErrInvalidProposalBlockID    = errors.New("proposal block ID does not describe the assembled block")
	ErrUnableToVerifyProposal    = errors.New("error unable to verify proposal")
	ErrInvalidProposalSignature  = errors.New("error invalid proposal signature")
	ErrInvalidProposalCoreHeight = errors.New("error invalid proposal core height")
	ErrInvalidProposalPOLRound   = errors.New("error invalid proposal POL round")
	ErrAddingVote                = errors.New("error adding vote")
	ErrProposalBlockNotSet       = errors.New("proposal block is not set")

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

// peerErrorMsg signals to the reactor that a peer should be disconnected due to an error
type peerErrorMsg struct {
	PeerID types.NodeID
	Err    error
	Fatal  bool
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
	config             *config.ConsensusConfig
	privValidator      privValidator
	verificationBudget types.VerificationBudget

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

	// peer errors (e.g., for invalid signatures) are queued here for the reactor
	// to handle disconnections
	peerErrorQueue *chanQueue[peerErrorMsg]

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

	msgInfoQueue   *msgInfoQueue
	msgDispatcher  *msgInfoDispatcher
	blockExecutor  *blockExecutor
	eventPublisher *EventPublisher
	voteSigner     *voteSigner
	ctrl           *Controller
	roundScheduler *roundScheduler
	// holds back proposals while the node catches up after a block-sync handover
	catchup *catchupTracker
	// sends or watches proposer heartbeats while the node waits for transactions
	liveness *proposerLiveness
	// gathers the votes to skip a round whose proposer went silent
	roundSkips     *roundSkipCollector
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

// WithVerificationBudget replaces the peer verification budget used by the
// consensus state.
//
// The budget must be able to defer a message until its whole cost is affordable,
// not merely report affordability: the scheduler makes room for a message's whole
// cost before dispatching it and relies on the budget to still cover that cost
// when the staged verification draws it (see budgetCanWait). The bundled budget
// can; a replacement that cannot is rejected by NewState with an error rather
// than accepted and later found wanting.
func WithVerificationBudget(budget types.VerificationBudget) func(cs *State) {
	return func(cs *State) {
		cs.verificationBudget = budget
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
		peerErrorQueue: &chanQueue[peerErrorMsg]{
			ch: make(chan peerErrorMsg, msgQueueSize),
		},
		doWALCatchup: true,
		wal:          nilWAL{},
		evpool:       evpool,
		emitter:      eventemitter.New(eventemitter.WithLogger(logger)),
		metrics:      NopMetrics(),
		verificationBudget: newVerificationBudget(
			cfg.VerificationRateLimit,
		),
	}

	// NOTE: we do not call scheduleRound0 yet, we do that upon Start()
	cs.BaseService = *service.NewBaseService(logger, "State", cs)
	for _, option := range options {
		option(cs)
	}

	// A budget the scheduler cannot wait on would silently give up whole-message
	// atomicity (see budgetCanWait); reject it here rather than let the scheduler
	// run without the guarantee it exists to provide.
	if !budgetCanWait(cs.verificationBudget) {
		return nil, errors.New("verification budget cannot defer a message until it is affordable")
	}

	// Built after the options, since the peer scheduler waits on whichever
	// verification budget and reports to whichever metrics the node ends up with.
	cs.msgInfoQueue = newMsgInfoQueue(
		withLaneBudget(cs.verificationBudget),
		withLaneLogger(cs.logger),
		withLaneMetrics(cs.metrics),
	)

	cs.liveness = newProposerLiveness(cs.logger, cs.metrics, cs.emitter, cs.proposerReady, cs.verificationBudget)
	cs.roundSkips = newRoundSkipCollector(cs.logger, cs.verificationBudget)

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
	cs.catchup = &catchupTracker{}
	propler := NewProposaler(cs.logger, cs.metrics, cs.privValidator, cs.msgInfoQueue, cs.blockExecutor)
	cs.ctrl = NewController(cs, wal, cs.statsMsgQueue, propler)
	subs := []eventemitter.Subscriber{propler, cs.blockExecutor, cs.stateDataStore, cs.voteSigner}
	for _, sub := range subs {
		sub.Subscribe(cs.emitter)
	}
	// Appended last, and therefore outermost: a message the verification budget
	// cannot cover must cost nothing at all, in particular no WAL record.
	cs.msgMiddlewares = append(cs.msgMiddlewares,
		verificationBudgetMiddleware(cs.verificationBudget, cs.metrics, cs.logger))
	cs.msgDispatcher = newMsgInfoDispatcher(cs.ctrl, propler, wal, cs.logger, cs.verificationBudget,
		cs.handleLivenessMessage, cs.msgMiddlewares...)

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

func (cs *State) GetCurrentHeight() int64 {
	return cs.stateDataStore.Get().Height
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

type processingContextKey struct{}

// Start preserves the parent's lifetime for in-flight processing during manual Stop.
func (cs *State) Start(ctx context.Context) error {
	return cs.BaseService.Start(context.WithValue(ctx, processingContextKey{}, ctx))
}

// OnStart loads the latest state via the WAL, and starts the timeout and
// receive routines.
func (cs *State) OnStart(ctx context.Context) (err error) {
	defer func() {
		if err != nil {
			cs.wal.Stop()
			cs.wal.Wait()
			cs.wal = nilWAL{}
			cs.timeoutTicker.Stop()
			cs.timeoutTicker.Wait()
			if _, ok := cs.timeoutTicker.(*timeoutTicker); ok {
				cs.timeoutTicker = NewTimeoutTicker(cs.logger)
				cs.roundScheduler.timeoutTicker = cs.timeoutTicker
			}
		}
	}()
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
				cs.logger.Warn("error on catchup replay; proceeding to start state anyway", "err", err)
				break LOOP

			case repairAttempted:
				return err
			}

			cs.logger.Error("the WAL file is corrupted; attempting repair", "err", err)

			// 1) prep work
			cs.wal.Stop()
			cs.wal.Wait()

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
	if !cs.Go(ctx, func(ctx context.Context) { cs.receiveRoutine(ctx, cs.stopFn) }) {
		return context.Canceled
	}

	// schedule the first round!
	// use GetRoundState so we don't race the receiveRoutine for access
	cs.roundScheduler.ScheduleRound0(cs.GetRoundState())

	return nil
}

// loadWalFile loads WAL data from file. It overwrites cs.wal.
func (cs *State) loadWalFile(ctx context.Context) error {
	// receiveRoutine owns the WAL until its final write and explicit Stop/Wait.
	wal, err := cs.OpenWAL(context.WithoutCancel(ctx), cs.config.WalFile())
	if err != nil {
		cs.logger.Error("failed to load state WAL", "err", err)
		return err
	}

	cs.wal = wal
	return nil
}

// OnStop requests timeout worker shutdown. Wait also joins receiveRoutine.
func (cs *State) OnStop() { cs.timeoutTicker.Stop() }

// OnDrain joins the timeout worker before callers release consensus stores.
func (cs *State) OnDrain() { cs.timeoutTicker.Wait() }

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
		wal.Group().Close()
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
	processCtx, ok := ctx.Value(processingContextKey{}).(context.Context)
	if !ok {
		processCtx = ctx
	}
	onExit := func(cs *State) {
		// NOTE: the internalMsgQueue may have signed messages from our
		// priv_val that haven't hit the WAL, but its ok because
		// priv_val tracks LastSig

		// close wal now that we're done writing to it
		cs.wal.Stop()
		cs.wal.Wait()
		cs.msgInfoQueue.stop()
		cs.liveness.stop()
	}

	defer func() {
		if r := recover(); r != nil {
			cs.logger.Error("CONSENSUS FAILURE!!!", "err", r, "stack", string(debug.Stack()))

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

	queueDone := make(chan struct{})
	go func() {
		defer close(queueDone)
		cs.msgInfoQueue.fanIn(ctx)
	}()
	defer func() { onExit(cs); <-queueDone }()

	cs.followLiveness(ctx)
	for {
		if ctx.Err() != nil {
			return
		}
		if stopFn != nil && stopFn(cs) {
			return
		}

		select {
		case <-cs.txNotifier.TxsAvailable():
			stateData := cs.stateDataStore.Get()
			cs.handleTxsAvailable(processCtx, &stateData)
			err := stateData.Save()
			if err != nil {
				cs.logger.Error("failed update state-data", "err", err)
			}
		case mi := <-cs.msgInfoQueue.read():
			stateData := cs.stateDataStore.Get()
			err := cs.msgDispatcher.dispatch(processCtx, &stateData, mi)
			if mi.PeerID != "" {
				// The peer scheduler makes room in the verification budget for a
				// message before handing it over, which is only sound if what it
				// reads from the budget includes everything already dispatched.
				// Reporting here rather than deeper in the dispatch covers the
				// paths that never reach a handler: a scheduler left waiting for
				// a message that will never settle stops serving peers entirely.
				cs.msgInfoQueue.settlePeerMsg()
			}
			if err != nil {
				// Leaving without this would strand the queue's reader
				// goroutines: nothing else ever tells them to stop.
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
			cs.handleTimeout(processCtx, ti, &stateData)
			err := cs.stateDataStore.Update(stateData)
			if err != nil {
				cs.logger.Error("failed update state-data", "err", err)
			}
		case silence := <-cs.liveness.silent():
			stateData := cs.stateDataStore.Get()
			cs.handleProposerSilence(ctx, &stateData, silence)
			err := stateData.Save()
			if err != nil {
				cs.logger.Error("failed update state-data", "err", err)
			}
		case <-ctx.Done():
			return

		}
		// Whatever was handled may have moved the node into or out of a round
		// that waits for transactions.
		cs.followLiveness(ctx)
		// TODO should we handle context cancels here?
	}
}

// followLiveness points the round skip collector and the proposer liveness
// watch at the round the node is in. The receive routine runs it after every
// input, and once before the first: a node restarted into a waiting round
// replays its WAL before the routine starts, and may get no input for as long
// as the round waits.
func (cs *State) followLiveness(ctx context.Context) {
	stateData := cs.stateDataStore.Get()
	cs.roundSkips.track(&stateData)
	cs.liveness.followRounds(&stateData)
	cs.liveness.reconcile(ctx, &stateData, cs.privValidator)
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
	// Only round 0 waits for transactions, and a round entered because +2/3
	// skipped the one before it while waiting. Whichever round hears it, the
	// report is the only one this height gets, so it is recorded: a round
	// entered later must not wait for another.
	waiting := stateData.Round == 0 || stateData.waitsAfterRoundSkip(stateData.Round)
	stateData.TxsReported = true
	if !waiting {
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

// errProposerCatchingUp is why a node still catching up after a block-sync
// handover sends no proposer heartbeat.
var errProposerCatchingUp = errors.New("catching up after block sync, not proposing yet")

// proposerReady reports whether this node could propose at height right now,
// which is what its proposer heartbeat tells the validators: the propose step
// would not hold it back after a block-sync handover, and the application
// answers.
func (cs *State) proposerReady(ctx context.Context, height int64) error {
	if !cs.catchup.mayPropose(height) {
		return errProposerCatchingUp
	}
	if cs.blockExec == nil {
		return nil
	}
	return cs.blockExec.AppReady(ctx)
}

// handleProposerSilence acts on a report that the proposer of the round this
// node waits in has stopped sending heartbeats.
func (cs *State) handleProposerSilence(ctx context.Context, stateData *StateData, silence proposerSilence) {
	// The report was raised on another goroutine and may describe a round the
	// node has already left, or a proposer whose heartbeat arrived since.
	if silence.Height != stateData.Height || silence.Round != stateData.Round || !waitingRound(stateData) ||
		!cs.liveness.current(silence) {
		return
	}
	if !stateData.isValidator(cs.privValidator.ProTxHash) {
		return
	}
	// A silence reported after this validator voted is the vote's resend timer
	// (see voteToSkipRound), not another silent round.
	if cs.roundSkips.voteOf(silence.Height, silence.Round, cs.privValidator.ProTxHash) == nil {
		cs.metrics.ProposerSilences.Add(1)
		cs.logger.Info("proposer of the round is silent, voting to skip the round",
			"height", silence.Height,
			"round", silence.Round,
			"proposer", silence.Proposer.ShortString())
	}
	cs.voteToSkipRound(ctx, stateData, silence)
}

// voteToSkipRound signs this validator's vote to skip the round it waits in,
// counts it, and hands it to the reactor to broadcast. If the vote completes
// +2/3, the certificate goes to the state machine like one from a peer.
func (cs *State) voteToSkipRound(ctx context.Context, stateData *StateData, silence proposerSilence) {
	pv := cs.privValidator
	index, val := stateData.Validators.GetByProTxHash(pv.ProTxHash)
	if pv.IsZero() || val == nil {
		return
	}
	// The proposer is still silent a silence timeout after this validator's
	// vote, or went silent again after a heartbeat that followed it. The vote
	// stands, as every validator signs the same bytes for the round; it is
	// broadcast again, not signed again.
	if vote := cs.roundSkips.voteOf(stateData.Height, stateData.Round, pv.ProTxHash); vote != nil {
		cs.emitter.Emit(roundSkipVoteEventName, vote)
		cs.liveness.rearm(silence)
		return
	}
	pb := &tmproto.RoundSkipVote{
		Height:             stateData.Height,
		Round:              stateData.Round,
		ValidatorProTxHash: pv.ProTxHash,
		ValidatorIndex:     index,
	}
	timeouts := stateData.state.ConsensusParams.Timeout.TimeoutParamsOrDefaults()
	signCtx, cancel := context.WithTimeout(ctx, timeouts.ProposerSilenceTimeout(stateData.Round))
	defer cancel()
	err := pv.SignRoundSkipVote(signCtx, stateData.state.ChainID,
		stateData.Validators.QuorumType, stateData.Validators.QuorumHash, pb)
	if errors.Is(err, types.ErrSigningNotSupported) {
		cs.logger.Debug("this node's signer cannot sign round skip votes", "height", pb.Height, "round", pb.Round)
		return
	}
	if err != nil {
		// The silence still stands, so the vote is tried again after another
		// silence timeout rather than never.
		cs.logger.Error("cannot sign round skip vote; trying again after the silence timeout",
			"height", pb.Height, "round", pb.Round, "err", err)
		cs.liveness.rearm(silence)
		return
	}
	vote, err := types.RoundSkipVoteFromProto(pb)
	if err != nil {
		cs.logger.Error("signed an invalid round skip vote", "vote", vote, "err", err)
		return
	}
	_, certificate, err := cs.roundSkips.add(vote, false)
	if err != nil {
		cs.logger.Error("cannot count own round skip vote", "vote", vote, "err", err)
		return
	}
	cs.emitter.Emit(roundSkipVoteEventName, vote)
	// The vote is broadcast once, and a broadcast can be dropped on its way to
	// peers. While the round stays silent it goes out again every silence
	// timeout, so the certificate still forms; peers count each validator's
	// vote once. Moving on from the round ends the watch, and with it the timer.
	cs.liveness.rearm(silence)
	if certificate != nil {
		if err := cs.sendMessage(ctx, &RoundSkipCertificateMessage{Certificate: certificate}, ""); err != nil {
			cs.logger.Error("cannot queue round skip certificate", "certificate", certificate, "err", err)
		}
	}
}

// handleLivenessMessage takes a proposer heartbeat or round skip vote from a
// peer, which the peer scheduler handed over with room in the verification
// budget for its signature check. One that is news is passed on to the reactor
// to relay, and a vote that completes +2/3 for its round sends the certificate
// it recovered to the state machine.
func (cs *State) handleLivenessMessage(ctx context.Context, msg Message, peerID types.NodeID) error {
	switch m := msg.(type) {
	case *ProposerHeartbeatMessage:
		relay, err := cs.liveness.heard(m.Heartbeat)
		if err != nil {
			return err
		}
		// Each node relays a heartbeat the first time it verifies one, so it
		// reaches validators that are not connected to the proposer.
		if relay {
			cs.emitter.Emit(livenessRelayEventName, livenessRelay{msg: m, from: peerID})
		}
	case *RoundSkipVoteMessage:
		added, certificate, err := cs.roundSkips.add(m.Vote, true)
		if err != nil {
			return err
		}
		// Relayed the first time it is counted, as heartbeats are, so every
		// validator can recover the certificate whoever it is connected to.
		if added {
			cs.emitter.Emit(livenessRelayEventName, livenessRelay{msg: m, from: peerID})
		}
		if certificate != nil {
			return cs.sendMessage(ctx, &RoundSkipCertificateMessage{Certificate: certificate}, "")
		}
	}
	return nil
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
