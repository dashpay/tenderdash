package consensus

import (
	"bytes"
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/abci/example/kvstore"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/internal/eventbus"
	tmpubsub "github.com/dashpay/tenderdash/internal/pubsub"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/log"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

const (
	// blockTimeIota is used in the test harness as the time between
	// blocks when not otherwise specified.
	blockTimeIota = time.Millisecond
)

// pbtsTestHarness constructs a Tendermint network that can be used for testing the
// implementation of the Proposer-Based timestamps algorithm.
// It runs a series of consensus heights and captures timing of votes and events.
type pbtsTestHarness struct {
	// configuration options set by the user of the test harness.
	pbtsTestConfiguration

	// The Tendermint consensus state machine being run during
	// a run of the pbtsTestHarness.
	observedState *State

	// A stub for signing votes and messages using the key
	// from the observedState.
	observedValidator *validatorStub

	// A list of simulated validators that interact with the observedState and are
	// fully controlled by the test harness.
	otherValidators []*validatorStub

	chainID string

	// channels for verifying that the observed validator completes certain actions.
	ensureProposalCh, roundCh, blockCh, ensureVoteCh <-chan tmpubsub.Message

	// channel of events from the observed validator annotated with the timestamp
	// the event was received.
	eventCh <-chan timestampedEvent

	currentHeight int64
	currentRound  int32

	logger *log.TestingLogger
}

type pbtsTestHeightConfiguration struct {
	// proposalDelay defines how long it will take to create a proposal since previous block creation
	proposalDelay time.Duration
	// deliveryDelay defines time between proposal generation and its delivery
	deliveryDelay time.Duration
}

type pbtsTestConfiguration struct {
	// The timestamp consensus parameters to be used by the state machine under test.
	synchronyParams types.SynchronyParams

	// The setting to use for the TimeoutPropose configuration parameter.
	timeoutPropose time.Duration

	// The genesis time
	genesisTime time.Time

	heights map[int64]pbtsTestHeightConfiguration

	maxHeight int64
}

func newPBTSTestHarness(ctx context.Context, t *testing.T, tc pbtsTestConfiguration) pbtsTestHarness {
	t.Helper()
	const validators = 4
	cfg := configSetup(t)

	if tc.genesisTime.IsZero() {
		tc.genesisTime = tmtime.Now()
	}

	consensusParams := factory.ConsensusParams()
	consensusParams.Timeout.Propose = tc.timeoutPropose
	consensusParams.Synchrony = tc.synchronyParams

	state, privVals := makeGenesisState(ctx, t, cfg, genesisStateArgs{
		Params:     consensusParams,
		Time:       tc.genesisTime,
		Validators: validators,
	})
	logger := log.NewTestingLoggerWithLevel(t, zerolog.LevelDebugValue)
	kvApp, err := kvstore.NewMemoryApp(kvstore.WithLogger(logger))
	require.NoError(t, err)

	msgMw := func(cs *State) {
		cs.msgMiddlewares = append(cs.msgMiddlewares,
			func(hd msgHandlerFunc) msgHandlerFunc {
				return func(ctx context.Context, stateData *StateData, msg msgEnvelope) error {
					if proposal, ok := msg.Msg.(*ProposalMessage); ok {
						if cfg, ok := tc.heights[stateData.Height]; ok {
							msg.ReceiveTime = proposal.Proposal.Timestamp.Add(cfg.deliveryDelay)
						}

					}
					return hd(ctx, stateData, msg)
				}
			})
	}

	cs := newState(ctx, t, logger.With("module", "consensus"), state, privVals[0], kvApp, msgMw)
	vss := make([]*validatorStub, validators)
	for i := 0; i < validators; i++ {
		vss[i] = newValidatorStub(privVals[i], int32(i), 0)
	}
	incrementHeight(vss[1:]...)

	proTxHash, err := vss[0].PrivValidator.GetProTxHash(ctx)
	require.NoError(t, err)

	eventCh := timestampedCollector(ctx, t, cs.eventBus)

	return pbtsTestHarness{
		pbtsTestConfiguration: tc,
		observedValidator:     vss[0],
		observedState:         cs,
		otherValidators:       vss[1:],
		currentHeight:         1,
		chainID:               cfg.ChainID(),
		roundCh:               subscribe(ctx, t, cs.eventBus, types.EventQueryNewRound),
		ensureProposalCh:      subscribe(ctx, t, cs.eventBus, types.EventQueryCompleteProposal),
		blockCh:               subscribe(ctx, t, cs.eventBus, types.EventQueryNewBlock),
		ensureVoteCh:          subscribeToVoterBuffered(ctx, t, cs, proTxHash),
		eventCh:               eventCh,
		logger:                logger,
	}
}

func (p *pbtsTestHarness) newProposal(ctx context.Context, t *testing.T) (types.Proposal, *types.Block, *types.PartSet) {
	stateData := p.observedState.GetStateData()
	proposer := p.pickProposer()
	quorumType := stateData.state.Validators.QuorumType
	quorumHash := stateData.state.Validators.QuorumHash

	b, err := p.observedState.CreateProposalBlock(ctx)
	require.NoError(t, err)

	b.Header.ProposerProTxHash, err = proposer.GetProTxHash(ctx)
	require.NoError(t, err)
	ps, err := b.MakePartSet(types.BlockPartSizeBytes)
	require.NoError(t, err)
	bid := b.BlockID(ps)
	require.NoError(t, err)

	stateData = p.observedState.GetStateData()
	coreChainLockedHeight := stateData.state.LastCoreChainLockedBlockHeight
	prop := types.NewProposal(p.currentHeight, coreChainLockedHeight, 0, -1, bid, b.Time)
	tp := prop.ToProto()

	if _, err := proposer.SignProposal(ctx, stateData.state.ChainID, quorumType, quorumHash, tp); err != nil {
		t.Fatalf("error signing proposal: %s", err)
	}

	prop.Signature = tp.Signature

	return *prop, b, ps
}

// nextHeight executes current height and advances to next height.
// It starts with proposal generation and ends when new block is ready.
func (p *pbtsTestHarness) nextHeight(
	ctx context.Context,
	t *testing.T,
	currentHeightConfig pbtsTestHeightConfiguration,
) heightResult {
	proposalDelay := currentHeightConfig.proposalDelay

	bid := types.BlockID{}

	proTxHash, err := p.observedState.privValidator.GetProTxHash(ctx)
	require.NoError(t, err)

	ensureNewRound(t, p.roundCh, p.currentHeight, p.currentRound)
	stateData := p.observedState.GetStateData()

	if !stateData.isProposer(proTxHash) {
		time.Sleep(proposalDelay)
		prop, _, ps := p.newProposal(ctx, t)

		// time.Sleep(deliveryDelay) -- handled by the middleware
		if err := p.observedState.SetProposalAndBlock(ctx, &prop, ps, "peerID"); err != nil {
			t.Fatal(err)
		}
	}
	// We don't sleep if we are the proposer, as this should be handled by the timeouts
	bid = ensureProposal(t, p.ensureProposalCh, p.currentHeight, 0, bid)

	ensurePrevote(t, p.ensureVoteCh, p.currentHeight, p.currentRound)
	signAddVotes(ctx, t, p.observedState, tmproto.PrevoteType, p.chainID, bid, p.otherValidators...)

	signAddVotes(ctx, t, p.observedState, tmproto.PrecommitType, p.chainID, bid, p.otherValidators...)
	ensurePrecommit(t, p.ensureVoteCh, p.currentHeight, p.currentRound)

	res := collectHeightResults(ctx, t, p.eventCh, p.currentHeight, proTxHash)
	ensureNewBlock(t, p.blockCh, p.currentHeight)

	p.currentHeight++
	incrementHeight(p.otherValidators...)
	return res
}

func (p *pbtsTestHarness) GetHeightConfiguration(height int64) pbtsTestHeightConfiguration {
	if height == 0 {
		return pbtsTestHeightConfiguration{}
	}

	hc := p.pbtsTestConfiguration.heights[height]
	if hc.proposalDelay == 0 {
		hc.proposalDelay = blockTimeIota
	}
	return hc
}

func timestampedCollector(ctx context.Context, t *testing.T, eb *eventbus.EventBus) <-chan timestampedEvent {
	t.Helper()

	// Since eventCh is not read until the end of each height, it must be large
	// enough to hold all of the events produced during a single height.
	eventCh := make(chan timestampedEvent, 100)

	if err := eb.Observe(ctx, func(msg tmpubsub.Message) error {
		eventCh <- timestampedEvent{
			ts: tmtime.Now(),
			m:  msg,
		}
		return nil
	}, types.EventQueryVote, types.EventQueryCompleteProposal); err != nil {
		t.Fatalf("Failed to observe query %v: %v", types.EventQueryVote, err)
	}
	return eventCh
}

func collectHeightResults(_ctx context.Context, t *testing.T, eventCh <-chan timestampedEvent, height int64, proTxHash crypto.ProTxHash) heightResult {
	t.Helper()
	var res heightResult
	for event := range eventCh {
		switch v := event.m.Data().(type) {
		case types.EventDataVote:
			if v.Vote.Height > height {
				t.Fatalf("received prevote from unexpected height, expected: %d, saw: %d", height, v.Vote.Height)
			}
			if !bytes.Equal(proTxHash, v.Vote.ValidatorProTxHash) {
				continue
			}
			if v.Vote.Type != tmproto.PrevoteType {
				continue
			}
			res.prevote = v.Vote
			res.prevoteIssuedAt = event.ts

		case types.EventDataCompleteProposal:
			if v.Height > height {
				t.Fatalf("received proposal from unexpected height, expected: %d, saw: %d", height, v.Height)
			}
			res.proposalIssuedAt = event.ts
		}
		if res.isComplete() {
			return res
		}
	}
	t.Fatalf("complete height result never seen for height %d", height)

	panic("unreachable")
}

type timestampedEvent struct {
	ts time.Time
	m  tmpubsub.Message
}

func (p *pbtsTestHarness) pickProposer() types.PrivValidator {
	stateData := p.observedState.GetStateData()
	proposer := stateData.ProposerSelector.MustGetProposer(p.currentHeight, p.currentRound)
	p.observedState.logger.Debug("picking proposer", "protxhash", proposer.ProTxHash)

	allVals := append(p.otherValidators, p.observedValidator)
	for _, val := range allVals {
		proTxHash, err := val.GetProTxHash(context.TODO())
		if err != nil {
			panic(err)
		}
		if proposer.ProTxHash.Equal(proTxHash) {
			return val
		}
	}

	panic("proposer not found")
}

func (p *pbtsTestHarness) run(ctx context.Context, t *testing.T) map[int64]heightResult {
	startTestRound(ctx, p.observedState, p.currentHeight, p.currentRound)
	results := map[int64]heightResult{}
	for p.currentHeight <= p.maxHeight {
		height := p.currentHeight
		hc := p.GetHeightConfiguration(height)
		results[height] = p.nextHeight(ctx, t, hc)
	}
	return results
}

type heightResult struct {
	proposalIssuedAt time.Time
	prevote          *types.Vote
	prevoteIssuedAt  time.Time
}

func (hr heightResult) isComplete() bool {
	return !hr.proposalIssuedAt.IsZero() && !hr.prevoteIssuedAt.IsZero() && hr.prevote != nil
}

// TestProposerWaitsForGenesisTime tests that a proposer will not propose a block
// until after the genesis time has passed. The test sets the genesis time in the
// future and then ensures that the observed validator waits to propose a block.
func TestProposerWaitsForGenesisTime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// create a genesis time far (enough) in the future.
	genesisTime := tmtime.Now().Add(100 * time.Millisecond)
	cfg := pbtsTestConfiguration{
		synchronyParams: types.SynchronyParams{
			Precision:    10 * time.Millisecond,
			MessageDelay: 10 * time.Millisecond,
		},
		timeoutPropose: 10 * time.Millisecond,
		genesisTime:    genesisTime,
		maxHeight:      2,
		heights: map[int64]pbtsTestHeightConfiguration{
			1: {
				proposalDelay: 10 * time.Millisecond,
				deliveryDelay: 100 * time.Millisecond,
			},
		},
	}

	pbtsTest := newPBTSTestHarness(ctx, t, cfg)
	results := pbtsTest.run(ctx, t)

	// ensure that the proposal was issued after the genesis time.
	assert.True(t, results[1].proposalIssuedAt.After(cfg.genesisTime),
		"Proposal issued at %s, genesis %s", results[1].proposalIssuedAt, cfg.genesisTime)
}

// TestProposerWaitsForPreviousBlock tests that the proposer of a block waits until
// the block time of the previous height has passed to propose the next block.
// The test harness ensures that the observed validator will be the proposer at
// height 1 and height 5. The test sets the block time of height 4 in the future
// and then verifies that the observed validator waits until after the block time
// of height 4 to propose a block at height 5.
func TestProposerWaitsForPreviousBlock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	initialTime := tmtime.Now().Add(time.Millisecond * 50)
	cfg := pbtsTestConfiguration{
		synchronyParams: types.SynchronyParams{
			Precision:    100 * time.Millisecond,
			MessageDelay: 500 * time.Millisecond,
		},
		timeoutPropose: 50 * time.Millisecond,
		genesisTime:    initialTime,
		heights: map[int64]pbtsTestHeightConfiguration{
			2: {
				proposalDelay: 100 * time.Millisecond,
				deliveryDelay: 50 * time.Millisecond, // totals 150 ms
			},
			4: {
				proposalDelay: 650 * time.Millisecond,
			},
		},
		maxHeight: 5,
	}

	pbtsTest := newPBTSTestHarness(ctx, t, cfg)
	results := pbtsTest.run(ctx, t)

	// the observed validator is the proposer at height 5.
	// ensure that the observed validator did not propose a block until after
	// the time configured for height 4.
	proposal5time := results[1].proposalIssuedAt
	for h := int64(2); h <= 5; h++ {
		// first, delivery of previous proposal
		hc := pbtsTest.GetHeightConfiguration(h - 1)
		proposal5time = proposal5time.Add(hc.deliveryDelay)
		// generation of current proposal
		// hc = pbtsTest.GetHeightConfiguration(h - 1)
		proposal5time = proposal5time.Add(hc.proposalDelay)
	}

	assert.True(t, results[5].proposalIssuedAt.After(proposal5time),
		"proposal isssued at %s, expected %s", results[5].proposalIssuedAt, proposal5time)

	// Ensure that the validator issued a prevote for a non-nil block.
	assert.NotNil(t, results[5].prevote.BlockID.Hash)
}

func TestProposerWaitTime(t *testing.T) {
	genesisTime, err := time.Parse(time.RFC3339, "2019-03-13T23:00:00Z")
	require.NoError(t, err)
	testCases := []struct {
		name              string
		previousBlockTime time.Time
		localTime         time.Time
		expectedWait      time.Duration
	}{
		{
			name:              "block time greater than local time",
			previousBlockTime: genesisTime.Add(5 * time.Millisecond),
			localTime:         genesisTime.Add(1 * time.Millisecond),
			expectedWait:      4 * time.Millisecond,
		},
		{
			name:              "local time greater than block time",
			previousBlockTime: genesisTime.Add(1 * time.Millisecond),
			localTime:         genesisTime.Add(5 * time.Millisecond),
			expectedWait:      0,
		},
		{
			name:              "both times equal",
			previousBlockTime: genesisTime.Add(5 * time.Millisecond),
			localTime:         genesisTime.Add(5 * time.Millisecond),
			expectedWait:      0,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ti := proposerWaitTime(testCase.localTime, testCase.previousBlockTime)
			assert.Equal(t, testCase.expectedWait, ti)
		})
	}
}

func TestTimelyProposal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	initialTime := tmtime.Now()

	cfg := pbtsTestConfiguration{
		synchronyParams: types.SynchronyParams{
			Precision:    10 * time.Millisecond,
			MessageDelay: 140 * time.Millisecond,
		},
		timeoutPropose: 50 * time.Millisecond,
		genesisTime:    initialTime,
		heights: map[int64]pbtsTestHeightConfiguration{
			2: {
				proposalDelay: 15 * time.Millisecond,
				deliveryDelay: 15 * time.Millisecond,
			},
		},
		maxHeight: 2,
	}

	pbtsTest := newPBTSTestHarness(ctx, t, cfg)

	results := pbtsTest.run(ctx, t)
	require.NotNil(t, results[2].prevote.BlockID.Hash)
}

// TestTooFarInThePastProposal ensures that outdated proposal is not voted on
func TestTooFarInThePastProposal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// localtime > proposedBlockTime + MsgDelay + Precision
	cfg := pbtsTestConfiguration{
		synchronyParams: types.SynchronyParams{
			Precision:    1 * time.Millisecond,
			MessageDelay: 30 * time.Millisecond,
		},
		timeoutPropose: 50 * time.Millisecond,
		heights: map[int64]pbtsTestHeightConfiguration{
			2: {
				proposalDelay: 15 * time.Millisecond,
				deliveryDelay: 33 * time.Millisecond,
			},
		},
		maxHeight: 2,
	}

	pbtsTest := newPBTSTestHarness(ctx, t, cfg)
	pbtsTest.logger.AssertContains("proposal is not timely: received too late: height 2")
	results := pbtsTest.run(ctx, t)

	require.Nil(t, results[2].prevote.BlockID.Hash)
}

// TestTooFarInTheFutureProposal ensures that future proposal is not voted on, eg. NIL-prevote is generated
func TestTooFarInTheFutureProposal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// localtime < proposedBlockTime - Precision
	cfg := pbtsTestConfiguration{
		synchronyParams: types.SynchronyParams{
			Precision:    1 * time.Millisecond,
			MessageDelay: 30 * time.Millisecond,
		},
		timeoutPropose: 500 * time.Millisecond,
		heights: map[int64]pbtsTestHeightConfiguration{
			2: {
				proposalDelay: 100 * time.Millisecond,
				deliveryDelay: -40 * time.Millisecond, // Recv time will be 40 ms before proposal time
			},
		},
		maxHeight: 2,
	}

	pbtsTest := newPBTSTestHarness(ctx, t, cfg)
	pbtsTest.logger.AssertMatch(regexp.MustCompile("proposal is not timely: received too early: height 2,"))
	results := pbtsTest.run(ctx, t)

	require.Nil(t, results[2].prevote.BlockID.Hash)
}
