package consensus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	sync "github.com/sasha-s/go-deadlock"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	"github.com/dashpay/tenderdash/abci/example/kvstore"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/dash"
	"github.com/dashpay/tenderdash/dash/llmq"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/internal/eventbus"
	"github.com/dashpay/tenderdash/internal/mempool"
	tmpubsub "github.com/dashpay/tenderdash/internal/pubsub"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/store"
	"github.com/dashpay/tenderdash/internal/test/factory"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/log"
	tmos "github.com/dashpay/tenderdash/libs/os"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	"github.com/dashpay/tenderdash/privval"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

const (
	testSubscriber = "test-client"
	ensureTimeout  = time.Millisecond * 800
)

// A cleanupFunc cleans up any config / test files created for a particular
// test.
type cleanupFunc func()

func configSetup(t *testing.T) *config.Config {
	t.Helper()

	cfg, err := ResetConfig(t, "consensus_reactor_test")
	require.NoError(t, err)
	walDir := filepath.Dir(cfg.Consensus.WalFile())
	ensureDir(t, walDir, 0700)

	return cfg
}

func ensureDir(t *testing.T, dir string, mode os.FileMode) {
	t.Helper()
	require.NoError(t, tmos.EnsureDir(dir, mode))
}

func ResetConfig(t *testing.T, name string) (*config.Config, error) {
	dir := t.TempDir()
	testConfig, err := config.ResetTestRoot(dir, name)
	if err != nil {
		return nil, err
	}
	if t != nil {
		t.Cleanup(func() { _ = os.RemoveAll(testConfig.RootDir) })
	}
	return testConfig, nil
}

//-------------------------------------------------------------------------------
// validator stub (a kvstore consensus peer we control)

type validatorStub struct {
	Index  int32 // Validator index. NOTE: we don't assume validator set changes.
	Height int64
	Round  int32
	types.PrivValidator
	VotingPower int64
	lastVote    *types.Vote
}

const testMinPower int64 = types.DefaultDashVotingPower

func newValidatorStub(privValidator types.PrivValidator, valIndex int32, initialHeight int64) *validatorStub {
	return &validatorStub{
		Index:         valIndex,
		PrivValidator: privValidator,
		VotingPower:   testMinPower,
		Height:        initialHeight,
	}
}

func (vs *validatorStub) signVote(
	ctx context.Context,
	voteType tmproto.SignedMsgType,
	chainID string,
	blockID types.BlockID,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
	voteExtensions types.VoteExtensions) (*types.Vote, error) {

	proTxHash, err := vs.PrivValidator.GetProTxHash(ctx)
	if err != nil {
		return nil, fmt.Errorf("can't get proTxHash: %w", err)
	}

	vote := &types.Vote{
		Type:               voteType,
		Height:             vs.Height,
		Round:              vs.Round,
		BlockID:            blockID,
		ValidatorProTxHash: proTxHash,
		ValidatorIndex:     vs.Index,
		VoteExtensions:     voteExtensions,
	}

	v := vote.ToProto()

	if err := vs.PrivValidator.SignVote(ctx, chainID, quorumType, quorumHash, v, nil); err != nil {
		return nil, fmt.Errorf("sign vote failed: %w", err)
	}

	// ref: signVote in FilePV, the vote should use the previous vote info when the sign data is the same.
	if signDataIsEqual(vs.lastVote, v) {
		err = vs.lastVote.PopulateSignsToProto(v)
		if err != nil {
			return nil, err
		}
	}

	err = vote.PopulateSignsFromProto(v)
	if err != nil {
		return nil, err
	}
	return vote, nil
}

// Sign vote for type/hash/header
func signVote(
	ctx context.Context,
	t *testing.T,
	vs *validatorStub,
	voteType tmproto.SignedMsgType,
	chainID string,
	blockID types.BlockID,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash) *types.Vote {
	exts := make(types.VoteExtensions, 0)
	v, err := vs.signVote(ctx, voteType, chainID, blockID, quorumType, quorumHash, exts)
	require.NoError(t, err, "failed to sign vote")

	vs.lastVote = v

	return v
}

func signVotes(
	ctx context.Context,
	t *testing.T,
	voteType tmproto.SignedMsgType,
	chainID string,
	blockID types.BlockID,
	_appHash []byte,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
	vss ...*validatorStub,
) []*types.Vote {
	votes := make([]*types.Vote, len(vss))
	for i, vs := range vss {
		votes[i] = signVote(ctx, t, vs, voteType, chainID, blockID, quorumType, quorumHash)
	}
	return votes
}

func incrementHeight(vss ...*validatorStub) {
	for _, vs := range vss {
		vs.Height++
	}
}

func incrementRound(vss ...*validatorStub) {
	for _, vs := range vss {
		vs.Round++
	}
}

func sortVValidatorStubsByPower(ctx context.Context, t *testing.T, vss []*validatorStub) []*validatorStub {
	t.Helper()
	sort.Slice(vss, func(i, j int) bool {
		vssi, err := vss[i].GetProTxHash(ctx)
		require.NoError(t, err)

		vssj, err := vss[j].GetProTxHash(ctx)
		require.NoError(t, err)

		if vss[i].VotingPower == vss[j].VotingPower {
			return bytes.Compare(vssi.Bytes(), vssj.Bytes()) == -1
		}
		return vss[i].VotingPower > vss[j].VotingPower
	})

	for idx, vs := range vss {
		vs.Index = int32(idx)
	}

	return vss
}

//-------------------------------------------------------------------------------
// Functions for transitioning the consensus state

// timeoutRoutine: receive requests for timeouts on tickChan and fire timeouts on tockChan
// receiveRoutine: serializes processing of proposoals, block parts, votes; coordinates state transitions
func startConsensusState(ctx context.Context, cs *State, maxSteps int) {
	err := cs.timeoutTicker.Start(ctx)
	if err != nil {
		cs.logger.Error("failed to start timeout ticker", "err", err)
		return
	}
	steps := 0
	sub, err := cs.eventBus.SubscribeWithArgs(ctx, tmpubsub.SubscribeArgs{
		ClientID: "stepsCounter",
		Query:    types.EventQueryNewRound,
	})
	if err != nil {
		panic(err)
	}
	go func() {
		for {
			_, err := sub.Next(ctx)
			if err != nil {
				return
			}
			steps++
		}
	}()
	go cs.receiveRoutine(ctx, func(_state *State) bool {
		return maxSteps > 0 && steps >= maxSteps
	})
}

func startTestRound(ctx context.Context, cs *State, height int64, round int32) {
	stateData := cs.GetStateData()
	ctx = dash.ContextWithProTxHash(ctx, cs.privValidator.ProTxHash)
	_ = cs.ctrl.Dispatch(ctx, &EnterNewRoundEvent{Height: height, Round: round}, &stateData)
	_ = stateData.Save()
	startConsensusState(ctx, cs, 0)
}

// Create proposal block from cs1 but sign it with vs.
func decideProposal(
	ctx context.Context,
	t *testing.T,
	cs1 *State,
	vs *validatorStub,
	height int64,
	round int32,
) (proposal *types.Proposal, block *types.Block) {
	t.Helper()

	stateData := cs1.GetStateData()
	defer func() {
		_ = stateData.Save()
	}()

	block, err := cs1.blockExecutor.create(ctx, &stateData.RoundState, round)
	require.NoError(t, err)
	blockParts, err := block.MakePartSet(types.BlockPartSizeBytes)
	require.NoError(t, err)
	validRound := stateData.ValidRound
	chainID := stateData.state.ChainID

	validatorsAtProposalHeight := stateData.state.ValidatorsAtHeight(height)
	quorumType := validatorsAtProposalHeight.QuorumType
	quorumHash := validatorsAtProposalHeight.QuorumHash

	require.NotNil(t, block, "Failed to createProposalBlock. Did you forget to add commit for previous block?")

	// Make proposal
	polRound := validRound
	propBlockID := block.BlockID(blockParts)
	assert.NoError(t, err)

	proposal = types.NewProposal(height, 1, round, polRound, propBlockID, block.Header.Time)
	p := proposal.ToProto()

	proTxHash, _ := vs.GetProTxHash(ctx)
	pubKey, _ := vs.GetPubKey(ctx, validatorsAtProposalHeight.QuorumHash)

	signID, err := vs.SignProposal(ctx, chainID, quorumType, quorumHash, p)
	require.NoError(t, err)

	cs1.logger.Debug("signed proposal common test", "height", proposal.Height, "round", proposal.Round,
		"proposerProTxHash", proTxHash.ShortString(), "public key", pubKey.HexString(), "quorum type",
		validatorsAtProposalHeight.QuorumType, "quorum hash", validatorsAtProposalHeight.QuorumHash, "signID", signID.String())

	proposal.Signature = p.Signature

	return proposal, block
}

func addVotes(to *State, votes ...*types.Vote) {
	ctx := ctxWithPeerQueue(context.Background())
	for _, vote := range votes {
		_ = to.msgInfoQueue.send(ctx, &VoteMessage{vote}, "")
	}
}

func signAddVotes(
	ctx context.Context,
	t *testing.T,
	to *State,
	voteType tmproto.SignedMsgType,
	chainID string,
	blockID types.BlockID,
	vss ...*validatorStub,
) {
	stateData := to.GetStateData()
	rs := stateData.RoundState
	valSet := stateData.Validators
	addVotes(to, signVotes(ctx, t, voteType, chainID, blockID, rs.AppHash, valSet.QuorumType, valSet.QuorumHash, vss...)...)
}

func validatePrevote(
	ctx context.Context,
	t *testing.T,
	cs *State,
	round int32,
	privVal *validatorStub,
	blockHash []byte,
) {
	t.Helper()

	stateData := cs.GetStateData()

	prevotes := stateData.Votes.Prevotes(round)
	proTxHash, err := privVal.GetProTxHash(ctx)
	require.NoError(t, err)
	var vote *types.Vote
	if vote = prevotes.GetByProTxHash(proTxHash); vote == nil {
		panic("Failed to find prevote from validator")
	}
	if blockHash == nil {
		require.Nil(t, vote.BlockID.Hash, "Expected prevote to be for nil, got %X", vote.BlockID.Hash)
	} else {
		require.True(t, bytes.Equal(vote.BlockID.Hash, blockHash), "Expected prevote to be for %X, got %X", blockHash, vote.BlockID.Hash)
	}
}

func validateLastCommit(_ctx context.Context, t *testing.T, cs *State, _privVal *validatorStub, blockHash []byte) {
	t.Helper()

	stateData := cs.GetStateData()
	commit := stateData.LastCommit
	err := commit.ValidateBasic()
	require.NoError(t, err, "Expected commit to be valid %v, %v", commit, err)
	require.True(t, bytes.Equal(commit.BlockID.Hash, blockHash), "Expected commit to be for %X, got %X", blockHash, commit.BlockID.Hash)
}

func validatePrecommit(
	ctx context.Context,
	t *testing.T,
	cs *State,
	thisRound,
	lockRound int32,
	privVal *validatorStub,
	votedBlockHash,
	lockedBlockHash []byte,
) {
	t.Helper()

	stateData := cs.GetStateData()
	precommits := stateData.Votes.Precommits(thisRound)
	proTxHash, err := privVal.GetProTxHash(ctx)
	require.NoError(t, err)
	vote := precommits.GetByProTxHash(proTxHash)
	require.NotNil(t, vote, "Failed to find precommit from validator")

	if votedBlockHash == nil {
		require.Nil(t, vote.BlockID.Hash, "Expected precommit to be for nil")
	} else {
		require.True(t, bytes.Equal(vote.BlockID.Hash, votedBlockHash), "Expected precommit to be for proposal block")
	}

	rs := cs.GetRoundState()
	if lockedBlockHash == nil {
		require.False(t, rs.LockedRound != lockRound || rs.LockedBlock != nil,
			"Expected to be locked on nil at round %d. Got locked at round %d with block %v",
			lockRound,
			rs.LockedRound,
			rs.LockedBlock)
	} else {
		require.False(t, rs.LockedRound != lockRound || !bytes.Equal(rs.LockedBlock.Hash(), lockedBlockHash),
			"Expected block to be locked on round %d, got %d. Got locked block %X, expected %X",
			lockRound,
			rs.LockedRound,
			rs.LockedBlock.Hash(),
			lockedBlockHash)
	}
}

func subscribeToVoter(ctx context.Context, t *testing.T, cs *State, proTxHash []byte) <-chan tmpubsub.Message {
	t.Helper()

	ch := make(chan tmpubsub.Message, 1)
	if err := cs.eventBus.Observe(ctx, func(msg tmpubsub.Message) error {
		vote := msg.Data().(types.EventDataVote)
		// we only fire for our own votes
		if bytes.Equal(proTxHash, vote.Vote.ValidatorProTxHash) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case ch <- msg:
			}
		}
		return nil
	}, types.EventQueryVote); err != nil {
		t.Fatalf("Failed to observe query %v: %v", types.EventQueryVote, err)
	}
	return ch
}

func subscribeToVoterBuffered(ctx context.Context, t *testing.T, cs *State, proTxHash []byte) <-chan tmpubsub.Message {
	t.Helper()
	votesSub, err := cs.eventBus.SubscribeWithArgs(ctx, tmpubsub.SubscribeArgs{
		ClientID: testSubscriber,
		Query:    types.EventQueryVote,
		Limit:    10})
	if err != nil {
		t.Fatalf("failed to subscribe %s to %v", testSubscriber, types.EventQueryVote)
	}
	ch := make(chan tmpubsub.Message, 10)
	go func() {
		for {
			msg, err := votesSub.Next(ctx)
			if err != nil {
				if !errors.Is(err, tmpubsub.ErrTerminated) && !errors.Is(err, context.Canceled) {
					t.Errorf("error terminating pubsub %s", err)
				}
				return
			}
			vote := msg.Data().(types.EventDataVote)
			// we only fire for our own votes
			if bytes.Equal(proTxHash, vote.Vote.ValidatorProTxHash) {
				select {
				case <-ctx.Done():
				case ch <- msg:
				}
			}
		}
	}()
	return ch
}

//-------------------------------------------------------------------------------
// consensus states

func newState(
	ctx context.Context,
	t *testing.T,
	logger log.Logger,
	state sm.State,
	pv types.PrivValidator,
	app abci.Application,
	opts ...StateOption,
) *State {
	t.Helper()

	cfg, err := config.ResetTestRoot(t.TempDir(), "consensus_state_test")
	require.NoError(t, err)

	return newStateWithConfig(ctx, t, logger, cfg, state, pv, app, opts...)
}

func newStateWithConfig(
	ctx context.Context,
	t *testing.T,
	logger log.Logger,
	thisConfig *config.Config,
	state sm.State,
	pv types.PrivValidator,
	app abci.Application,
	opts ...StateOption,
) *State {
	t.Helper()
	return newStateWithConfigAndBlockStore(ctx, t, logger, thisConfig, state, pv, app, store.NewBlockStore(dbm.NewMemDB()), opts...)
}

func newStateWithConfigAndBlockStore(
	ctx context.Context,
	t *testing.T,
	logger log.Logger,
	thisConfig *config.Config,
	state sm.State,
	pv types.PrivValidator,
	app abci.Application,
	blockStore sm.BlockStore,
	opts ...StateOption,
) *State {
	t.Helper()

	// one for mempool, one for consensus
	proxyAppConnMem := abciclient.NewLocalClient(logger, app)
	proxyAppConnCon := abciclient.NewLocalClient(logger, app)

	// Make Mempool

	mpool := mempool.NewTxMempool(
		logger.With("module", "mempool"),
		thisConfig.Mempool,
		proxyAppConnMem,
	)

	if thisConfig.Consensus.WaitForTxs() {
		mpool.EnableTxsAvailable()
	}

	evpool := sm.EmptyEvidencePool{}

	// Make State
	stateDB := dbm.NewMemDB()
	stateStore := sm.NewStore(stateDB)
	require.NoError(t, stateStore.Save(state))

	eventBus := eventbus.NewDefault(logger.With("module", "events"))
	require.NoError(t, eventBus.Start(ctx))

	blockExec := sm.NewBlockExecutor(stateStore, proxyAppConnCon, mpool, evpool, blockStore, eventBus)
	cs, err := NewState(logger.With("module", "consensus"),
		thisConfig.Consensus,
		stateStore,
		blockExec,
		blockStore,
		mpool,
		evpool,
		eventBus,
		opts...,
	)
	if err != nil {
		t.Fatal(err)
	}

	cs.SetPrivValidator(ctx, pv)

	return cs
}

func loadPrivValidator(t *testing.T, cfg *config.Config) *privval.FilePV {
	t.Helper()
	privValidatorKeyFile := cfg.PrivValidator.KeyFile()
	ensureDir(t, filepath.Dir(privValidatorKeyFile), 0700)
	privValidatorStateFile := cfg.PrivValidator.StateFile()
	privValidator, err := privval.LoadOrGenFilePV(privValidatorKeyFile, privValidatorStateFile)
	require.NoError(t, err)
	require.NoError(t, privValidator.Reset())
	return privValidator
}

type makeStateArgs struct {
	config          *config.Config
	consensusParams *types.ConsensusParams
	logger          log.Logger
	validators      int
	application     abci.Application
}

func makeState(ctx context.Context, t *testing.T, args makeStateArgs) (*State, []*validatorStub) {
	t.Helper()
	// Get State
	validators := 4
	if args.validators != 0 {
		validators = args.validators
	}
	var app abci.Application
	if args.application == nil {
		var err error
		app, err = kvstore.NewMemoryApp()
		require.NoError(t, err)
	} else {
		app = args.application
	}
	if args.config == nil {
		args.config = configSetup(t)
	}
	if args.logger == nil {
		args.logger = consensusLogger(t)
	}
	c := factory.ConsensusParams()
	if args.consensusParams != nil {
		c = args.consensusParams
	}

	// vote timeout increased because of bls12381 signing/verifying operations are longer performed than ed25519
	// and 10ms (previous value) is not enough
	c.Timeout.Vote = 50 * time.Millisecond
	c.Timeout.VoteDelta = 5 * time.Millisecond

	state, privVals := makeGenesisState(ctx, t, args.config, genesisStateArgs{
		Params:     c,
		Validators: validators,
	})

	vss := make([]*validatorStub, validators)

	cs := newState(ctx, t, args.logger, state, privVals[0], app)
	stateData := cs.GetStateData()

	for i := 0; i < validators; i++ {
		vss[i] = newValidatorStub(privVals[i], int32(i), stateData.state.InitialHeight)
	}

	return cs, vss
}

//-------------------------------------------------------------------------------

func ensureNoMessageBeforeTimeout(t *testing.T, ch <-chan tmpubsub.Message, timeout time.Duration,
	errorMessage string) {
	t.Helper()
	select {
	case <-time.After(timeout):
		break
	case e := <-ch:
		t.Logf("received unexpected event of type %T: %+v", e.Data(), e)
		t.Fatal(errorMessage)
	}
}

func ensureNoNewEventOnChannel(t *testing.T, ch <-chan tmpubsub.Message) {
	t.Helper()
	ensureNoMessageBeforeTimeout(
		t,
		ch,
		ensureTimeout,
		"We should be stuck waiting, not receiving new event on the channel")
}

func ensureNoNewRoundStep(t *testing.T, stepCh <-chan tmpubsub.Message) {
	t.Helper()
	ensureNoMessageBeforeTimeout(
		t,
		stepCh,
		ensureTimeout,
		"We should be stuck waiting, not receiving NewRoundStep event")
}

func ensureNoNewTimeout(t *testing.T, stepCh <-chan tmpubsub.Message, timeout int64) {
	t.Helper()
	timeoutDuration := time.Duration(timeout*10) * time.Nanosecond
	ensureNoMessageBeforeTimeout(
		t,
		stepCh,
		timeoutDuration,
		"We should be stuck waiting, not receiving NewTimeout event")
}

func ensureNewEvent(t *testing.T, ch <-chan tmpubsub.Message, height int64, round int32, timeout time.Duration) {
	t.Helper()
	msg := ensureMessageBeforeTimeout(t, ch, timeout)
	roundStateEvent, ok := msg.Data().(types.EventDataRoundState)
	require.True(t, ok,
		"expected a EventDataRoundState, got %T. Wrong subscription channel?",
		msg.Data())

	require.Equal(t, height, roundStateEvent.Height)
	require.Equal(t, round, roundStateEvent.Round)
	// TODO: We could check also for a step at this point!
}

func ensureNewRound(t *testing.T, roundCh <-chan tmpubsub.Message, height int64, round int32) {
	t.Helper()
	msg := ensureMessageBeforeTimeout(t, roundCh, ensureTimeout)
	newRoundEvent, ok := msg.Data().(types.EventDataNewRound)
	assert.True(t, ok, "expected a EventDataNewRound, got %T. Wrong subscription channel?",
		msg.Data())

	assert.Equal(t, height, newRoundEvent.Height, "height")
	assert.Equal(t, round, newRoundEvent.Round, "round")
}

func ensureNewTimeout(t *testing.T, timeoutCh <-chan tmpubsub.Message, height int64, round int32, timeout int64) {
	t.Helper()
	timeoutDuration := time.Duration(timeout*10) * time.Nanosecond
	ensureNewEvent(t, timeoutCh, height, round, timeoutDuration)
}

func ensureNewProposal(t *testing.T, proposalCh <-chan tmpubsub.Message, height int64, round int32) types.BlockID {
	t.Helper()
	msg := ensureMessageBeforeTimeout(t, proposalCh, ensureTimeout)
	proposalEvent, ok := msg.Data().(types.EventDataCompleteProposal)
	require.True(t, ok, "expected a EventDataCompleteProposal, got %T. Wrong subscription channel?",
		msg.Data())
	require.Equal(t, height, proposalEvent.Height)
	require.Equal(t, round, proposalEvent.Round)
	return proposalEvent.BlockID
}

func ensureNewValidBlock(t *testing.T, validBlockCh <-chan tmpubsub.Message, height int64, round int32) {
	t.Helper()
	ensureNewEvent(t, validBlockCh, height, round, ensureTimeout)
}

func ensureNewBlock(t *testing.T, blockCh <-chan tmpubsub.Message, height int64) {
	t.Helper()
	msg := ensureMessageBeforeTimeout(t, blockCh, ensureTimeout)
	blockEvent, ok := msg.Data().(types.EventDataNewBlock)
	require.True(t, ok, "expected a EventDataNewBlock, got %T. Wrong subscription channel?",
		msg.Data())
	require.Equal(t, height, blockEvent.Block.Height)
}

func ensureNewBlockHeader(t *testing.T, blockCh <-chan tmpubsub.Message, height int64, blockHash tmbytes.HexBytes) {
	t.Helper()
	msg := ensureMessageBeforeTimeout(t, blockCh, ensureTimeout)
	blockHeaderEvent, ok := msg.Data().(types.EventDataNewBlockHeader)
	require.True(t, ok, "expected a EventDataNewBlockHeader, got %T. Wrong subscription channel?",
		msg.Data())

	require.Equal(t, height, blockHeaderEvent.Header.Height)
	require.True(t, bytes.Equal(blockHeaderEvent.Header.Hash(), blockHash))
}

func ensureLock(t *testing.T, lockCh <-chan tmpubsub.Message, height int64, round int32) {
	t.Helper()
	ensureNewEvent(t, lockCh, height, round, ensureTimeout)
}

func ensureRelock(t *testing.T, relockCh <-chan tmpubsub.Message, height int64, round int32) {
	t.Helper()
	ensureNewEvent(t, relockCh, height, round, ensureTimeout)
}

func ensureProposal(t *testing.T, proposalCh <-chan tmpubsub.Message, height int64, round int32, propID types.BlockID) types.BlockID {
	t.Helper()
	return ensureProposalWithTimeout(t, proposalCh, height, round, &propID, ensureTimeout)
}

func ensureProposalWithTimeout(t *testing.T, proposalCh <-chan tmpubsub.Message, height int64, round int32, propID *types.BlockID, timeout time.Duration) types.BlockID {
	t.Helper()
	msg := ensureMessageBeforeTimeout(t, proposalCh, timeout)
	proposalEvent, ok := msg.Data().(types.EventDataCompleteProposal)
	require.True(t, ok, "expected a EventDataCompleteProposal, got %T. Wrong subscription channel?",
		msg.Data())
	require.Equal(t, height, proposalEvent.Height)
	require.Equal(t, round, proposalEvent.Round)
	if propID != nil && !propID.IsNil() {
		require.True(t, proposalEvent.BlockID.Equals(*propID),
			"Proposed block does not match expected block (%v != %v)", proposalEvent.BlockID, propID)
	}

	return proposalEvent.BlockID
}

func ensurePrecommit(t *testing.T, voteCh <-chan tmpubsub.Message, height int64, round int32) {
	t.Helper()
	ensureVote(t, voteCh, height, round, tmproto.PrecommitType)
}

func ensurePrevote(t *testing.T, voteCh <-chan tmpubsub.Message, height int64, round int32) {
	t.Helper()
	ensureVote(t, voteCh, height, round, tmproto.PrevoteType)
}

func ensurePrevoteMatch(t *testing.T, voteCh <-chan tmpubsub.Message, height int64, round int32, hash []byte) {
	t.Helper()
	ensureVoteMatch(t, voteCh, height, round, hash, tmproto.PrevoteType)
}

func ensurePrecommitMatch(t *testing.T, voteCh <-chan tmpubsub.Message, height int64, round int32, hash []byte) {
	t.Helper()
	ensureVoteMatch(t, voteCh, height, round, hash, tmproto.PrecommitType)
}

func ensureVoteMatch(t *testing.T, voteCh <-chan tmpubsub.Message, height int64, round int32, hash []byte, voteType tmproto.SignedMsgType) {
	t.Helper()
	select {
	case <-time.After(ensureTimeout):
		t.Fatal("Timeout expired while waiting for NewVote event")
	case msg := <-voteCh:
		voteEvent, ok := msg.Data().(types.EventDataVote)
		require.True(t, ok, "expected a EventDataVote, got %T. Wrong subscription channel?",
			msg.Data())

		vote := voteEvent.Vote
		assert.Equal(t, height, vote.Height, "expected height %d, but got %d", height, vote.Height)
		assert.Equal(t, round, vote.Round, "expected round %d, but got %d", round, vote.Round)
		assert.Equal(t, voteType, vote.Type, "expected type %s, but got %s", voteType, vote.Type)
		if hash == nil {
			require.Nil(t, vote.BlockID.Hash, "Expected prevote to be for nil, got %X", vote.BlockID.Hash)
		} else {
			require.True(t, bytes.Equal(vote.BlockID.Hash, hash), "Expected prevote to be for %X, got %X", hash, vote.BlockID.Hash)
		}
	}
}

func ensureVote(t *testing.T, voteCh <-chan tmpubsub.Message, height int64, round int32, voteType tmproto.SignedMsgType) {
	t.Helper()
	msg := ensureMessageBeforeTimeout(t, voteCh, ensureTimeout)
	voteEvent, ok := msg.Data().(types.EventDataVote)
	require.True(t, ok, "expected a EventDataVote, got %T. Wrong subscription channel?",
		msg.Data())

	vote := voteEvent.Vote
	require.Equal(t, height, vote.Height, "expected height %d, but got %d", height, vote.Height)
	require.Equal(t, round, vote.Round, "expected round %d, but got %d", round, vote.Round)
	require.Equal(t, voteType, vote.Type, "expected type %s, but got %s", voteType, vote.Type)
}

func ensureNewEventOnChannel(t *testing.T, ch <-chan tmpubsub.Message) {
	t.Helper()
	ensureMessageBeforeTimeout(t, ch, ensureTimeout)
}

func ensureMessageBeforeTimeout(t *testing.T, ch <-chan tmpubsub.Message, to time.Duration) tmpubsub.Message {
	t.Helper()
	select {
	case <-time.After(to):
		t.Fatalf("Timeout expired while waiting for message")
	case msg := <-ch:
		return msg
	}
	panic("unreachable")
}

//-------------------------------------------------------------------------------
// consensus nets

// consensusLogger is a TestingLogger which uses a different
// color for each validator ("validator" key must exist).
func consensusLogger(t *testing.T) log.Logger {
	//return log.NewNopLogger().With("module", "consensus")
	if t == nil {
		return log.NewNopLogger().With("module", "consensus")
	}
	return log.NewTestingLogger(t).With("module", "consensus")
}

func makeConsensusState(
	ctx context.Context,
	t *testing.T,
	_cfg *config.Config,
	nValidators int,
	testName string,
	tickerFunc func() TimeoutTicker,
	configOpts ...func(*config.Config),
) []*State {
	t.Helper()

	genDoc, privVals := factory.RandGenesisDoc(nValidators, factory.ConsensusParams())
	css := make([]*State, nValidators)
	logger := consensusLogger(t)

	for i := 0; i < nValidators; i++ {
		blockStore := store.NewBlockStore(dbm.NewMemDB()) // each state needs its own db
		state, err := sm.MakeGenesisState(genDoc)
		require.NoError(t, err)
		thisConfig, err := ResetConfig(t, fmt.Sprintf("%s_%d", testName, i))
		require.NoError(t, err)

		for _, opt := range configOpts {
			opt(thisConfig)
		}

		walDir := filepath.Dir(thisConfig.Consensus.WalFile())
		ensureDir(t, walDir, 0700)

		app, err := kvstore.NewMemoryApp(kvstore.WithLogger(logger))
		require.NoError(t, err)
		t.Cleanup(func() { _ = app.Close() })

		vals := types.TM2PB.ValidatorUpdates(state.Validators)
		_, err = app.InitChain(ctx, &abci.RequestInitChain{ValidatorSet: &vals})
		require.NoError(t, err)

		proTxHash, _ := privVals[i].GetProTxHash(ctx)
		sCtx := dash.ContextWithProTxHash(ctx, proTxHash)

		l := logger.With("validator", i, "module", "consensus")
		css[i] = newStateWithConfigAndBlockStore(sCtx, t, l, thisConfig, state, privVals[i], app, blockStore, WithTimeoutTicker(tickerFunc()))
	}

	return css
}

type consensusNetGen struct {
	cfg              *config.Config
	nPeers           int
	nVals            int
	tickerFun        func() TimeoutTicker
	appFunc          func(log.Logger, string) abci.Application
	validatorUpdates []validatorUpdate
	consensusParams  *types.ConsensusParams
}

type validatorUpdate struct {
	height    int64
	count     int
	operation string
}

func genFilePV(dir string) (types.PrivValidator, error) {
	tempKeyFile, err := os.CreateTemp(dir, "priv_validator_key_")
	if err != nil {
		return nil, err
	}
	tempStateFile, err := os.CreateTemp(dir, "priv_validator_state_")
	if err != nil {
		return nil, err
	}
	privVal := privval.GenFilePV(tempKeyFile.Name(), tempStateFile.Name())

	return privVal, nil
}

func (g *consensusNetGen) newApp(logger log.Logger, state *sm.State, confName string) (abci.Application, func() error) {
	app := g.appFunc(logger, filepath.Join(g.cfg.DBDir(), confName))
	switch app.(type) {
	// simulate handshake, receive app version. If don't do this, replay test will fail
	case *kvstore.Application:
		state.Version.Consensus.App = kvstore.ProtocolVersion
	}
	return app, func() error {
		if appCloser, ok := app.(io.Closer); ok {
			_ = appCloser.Close()
		}
		return nil
	}
}

func (g *consensusNetGen) execValidatorSetUpdater(ctx context.Context, t *testing.T, states []*State, apps []abci.Application, n int) map[int64]abci.ValidatorSetUpdate {
	t.Helper()
	ret := make(map[int64]abci.ValidatorSetUpdate)
	stateData := states[0].GetStateData()
	ret[0] = types.TM2PB.ValidatorUpdates(stateData.state.Validators)
	if g.validatorUpdates == nil {
		return ret
	}
	if _, ok := apps[0].(validatorSetUpdateStore); !ok || len(apps) == 0 {
		return nil
	}
	stores := make([]validatorSetUpdateStore, len(apps))
	for i, app := range apps {
		stores[i] = app.(validatorSetUpdateStore)
	}
	valsUpdater, err := newValidatorUpdater(states, stores, n)
	require.NoError(t, err)
	for _, update := range g.validatorUpdates {
		qd, err := valsUpdater.execOperation(ctx, update.operation, update.height, update.count)
		require.NoError(t, err)
		ret[update.height] = qd.validatorSetUpdate
	}
	return ret
}

// nPeers = nValidators + nNotValidator
func (g *consensusNetGen) generate(ctx context.Context, t *testing.T) ([]*State, *types.GenesisDoc, *config.Config, map[int64]abci.ValidatorSetUpdate) {
	t.Helper()
	if g.consensusParams == nil {
		g.consensusParams = factory.ConsensusParams()
		g.consensusParams.Timeout.Propose = 1 * time.Second
	}

	if g.nPeers == 0 {
		g.nPeers = g.nVals
	}
	css := make([]*State, g.nPeers)
	apps := make([]abci.Application, g.nPeers)
	genDoc, privVals := factory.RandGenesisDoc(g.nVals, g.consensusParams)
	logger := consensusLogger(t)
	var peer0Config *config.Config
	tickerFunc := g.tickerFun
	if tickerFunc == nil {
		tickerFunc = newTickerFunc()
	}
	for i := 0; i < g.nPeers; i++ {
		confName := fmt.Sprintf("%s_%d", t.Name(), i)
		state, _ := sm.MakeGenesisState(genDoc)
		thisConfig, err := ResetConfig(t, confName)
		require.NoError(t, err)

		ensureDir(t, filepath.Dir(thisConfig.Consensus.WalFile()), 0700) // dir for wal
		if i == 0 {
			peer0Config = thisConfig
		}
		if i >= len(privVals) {
			privVal, err := genFilePV(t.TempDir())
			require.NoError(t, err)
			privVals = append(privVals, privVal)
			// These validator might not have the public keys, for testing purposes let's assume they don't
			state.Validators.HasPublicKeys = false
		}
		var closeFunc func() error
		apps[i], closeFunc = g.newApp(logger, &state, confName)
		t.Cleanup(func() { _ = closeFunc() })

		vals := types.TM2PB.ValidatorUpdates(state.Validators)
		_, err = apps[i].InitChain(ctx, &abci.RequestInitChain{ValidatorSet: &vals})
		require.NoError(t, err)
		// sm.SaveState(stateDB,state)	//height 1's validatorsInfo already saved in LoadStateFromDBOrGenesisDoc above
		proTxHash, _ := privVals[i].GetProTxHash(ctx)
		css[i] = newStateWithConfig(ctx, t,
			logger.With("validator", i, "node_proTxHash", proTxHash.ShortString(), "module", "consensus"),
			thisConfig, state, privVals[i], apps[i], WithTimeoutTicker(tickerFunc()))
	}

	validatorSetUpdates := g.execValidatorSetUpdater(ctx, t, css, apps, g.nVals)

	return css, genDoc, peer0Config, validatorSetUpdates
}

type genesisStateArgs struct {
	Validators int
	Power      int64
	Params     *types.ConsensusParams
	Time       time.Time
}

func makeGenesisState(_ctx context.Context, t *testing.T, _cfg *config.Config, args genesisStateArgs) (sm.State, []types.PrivValidator) {
	t.Helper()
	if args.Power == 0 {
		args.Power = 1
	}
	if args.Validators == 0 {
		args.Power = 4
	}
	if args.Params == nil {
		args.Params = types.DefaultConsensusParams()
	}
	if args.Time.IsZero() {
		args.Time = tmtime.Now()
	}
	genDoc, privVals := factory.RandGenesisDoc(args.Validators, args.Params)
	genDoc.GenesisTime = args.Time
	s0, err := sm.MakeGenesisState(genDoc)
	require.NoError(t, err)
	return s0, privVals
}

func newMockTickerFunc(onlyOnce bool) func() TimeoutTicker {
	return func() TimeoutTicker {
		return &mockTicker{
			c:        make(chan timeoutInfo, 100),
			onlyOnce: onlyOnce,
		}
	}
}

func newTickerFunc() func() TimeoutTicker {
	return func() TimeoutTicker { return NewTimeoutTicker(log.NewNopLogger()) }
}

// mock ticker only fires on RoundStepNewHeight
// and only once if onlyOnce=true
type mockTicker struct {
	c chan timeoutInfo

	mtx      sync.Mutex
	onlyOnce bool
	fired    bool
}

func (m *mockTicker) Start(context.Context) error { return nil }
func (m *mockTicker) Stop()                       {}
func (m *mockTicker) IsRunning() bool             { return false }

func (m *mockTicker) ScheduleTimeout(ti timeoutInfo) {
	m.mtx.Lock()
	defer m.mtx.Unlock()
	if m.onlyOnce && m.fired {
		return
	}
	if ti.Step == cstypes.RoundStepNewHeight {
		m.c <- ti
		m.fired = true
	}
}

func (m *mockTicker) Chan() <-chan timeoutInfo {
	return m.c
}

func newKVStoreFunc(t *testing.T, opts ...kvstore.OptFunc) func(_ log.Logger, _ string) abci.Application {
	return func(logger log.Logger, _ string) abci.Application {
		opts = append(opts, kvstore.WithLogger(logger))
		app, err := kvstore.NewMemoryApp(opts...)
		require.NoError(t, err)

		return app
	}
}

func signDataIsEqual(v1 *types.Vote, v2 *tmproto.Vote) bool {
	if v1 == nil || v2 == nil {
		return false
	}
	if v1.VoteExtensions.IsSameWithProto(v2.VoteExtensions) {
		return false
	}
	return v1.Type == v2.Type &&
		bytes.Equal(v1.BlockID.Hash, v2.BlockID.GetHash()) &&
		v1.Height == v2.GetHeight() &&
		v1.Round == v2.Round &&
		bytes.Equal(v1.ValidatorProTxHash.Bytes(), v2.GetValidatorProTxHash()) &&
		v1.ValidatorIndex == v2.GetValidatorIndex()
}

type quorumData struct {
	llmq.Data
	quorumHash         crypto.QuorumHash
	validatorSetUpdate abci.ValidatorSetUpdate
}

type testSigner struct {
	privVals []types.PrivValidator
	valSet   *types.ValidatorSet
	logger   log.Logger
}

func (s *testSigner) signVotes(ctx context.Context, votes ...*types.Vote) error {
	for _, vote := range votes {
		protoVote := vote.ToProto()
		qt, qh := s.valSet.QuorumType, s.valSet.QuorumHash
		err := s.privVals[vote.ValidatorIndex].SignVote(ctx, factory.DefaultTestChainID, qt, qh, protoVote, s.logger)
		if err != nil {
			return err
		}
		vote.BlockSignature = protoVote.BlockSignature
	}
	return nil
}
