package consensus

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tendermint/tendermint/crypto"
	dbm "github.com/tendermint/tm-db"

	abciclient "github.com/tendermint/tendermint/abci/client"
	"github.com/tendermint/tendermint/abci/example/kvstore"
	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/internal/eventbus"
	"github.com/tendermint/tendermint/internal/mempool"
	"github.com/tendermint/tendermint/internal/proxy"
	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/internal/store"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/privval"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/types"
)

type nodeGen struct {
	cfg      *config.Config
	app      abci.Application
	logger   log.Logger
	state    *sm.State
	storeDB  dbm.DB
	mempool  mempool.Mempool
	proxyApp abciclient.Client
	eventBus *eventbus.EventBus
}

func (g *nodeGen) initState(t *testing.T) {
	if g.state != nil {
		return
	}
	genDoc, err := types.GenesisDocFromFile(g.cfg.GenesisFile())
	require.NoError(t, err, "failed to read genesis file")
	state, err := sm.MakeGenesisState(genDoc)
	require.NoError(t, err, "failed to make genesis state")
	state.Version.Consensus.App = kvstore.ProtocolVersion
	g.state = &state
}

func (g *nodeGen) initApp(ctx context.Context, t *testing.T) {
	if g.app == nil {
		g.app = kvstore.NewApplication()
	}
	proxyLogger := g.logger.With("module", "proxy")
	proxyApp := proxy.New(abciclient.NewLocalClient(g.logger, g.app), proxyLogger, proxy.NopMetrics())
	g.proxyApp = proxyApp
	err := proxyApp.Start(ctx)
	require.NoError(t, err, "failed to start proxy app connections")
	t.Cleanup(proxyApp.Wait)
}

func (g *nodeGen) initStores() {
	if g.storeDB != nil {
		return
	}
	g.storeDB = dbm.NewMemDB()
}

func (g *nodeGen) initEventbus(ctx context.Context, t *testing.T) {
	g.eventBus = eventbus.NewDefault(g.logger.With("module", "events"))
	err := g.eventBus.Start(ctx)
	require.NoError(t, err, "failed to start event bus")
	t.Cleanup(func() {
		g.eventBus.Stop()
		g.eventBus.Wait()
	})
}

func (g *nodeGen) initMempool() {
	if g.mempool == nil {
		g.mempool = emptyMempool{}
	}
}

func (g *nodeGen) Generate(ctx context.Context, t *testing.T) *fakeNode {
	t.Helper()
	g.initStores()
	g.initApp(ctx, t)
	g.initState(t)
	g.initEventbus(ctx, t)
	g.initMempool()
	stateStore := sm.NewStore(g.storeDB)
	blockStore := store.NewBlockStore(g.storeDB)
	err := stateStore.Save(*g.state)
	require.NoError(t, err)

	evpool := sm.EmptyEvidencePool{}
	blockExec := sm.NewBlockExecutor(
		stateStore,
		g.proxyApp,
		g.mempool,
		evpool,
		blockStore,
		g.eventBus,
	)
	csState, err := NewState(g.logger, g.cfg.Consensus, stateStore, blockExec, blockStore, g.mempool, evpool, g.eventBus)
	require.NoError(t, err)

	privValidator := privval.MustLoadOrGenFilePVFromConfig(g.cfg)
	if privValidator != nil {
		csState.SetPrivValidator(ctx, privValidator)
	}

	return &fakeNode{
		app:     g.app,
		csState: csState,
		pv:      privValidator,
	}
}

type fakeNode struct {
	app     abci.Application
	pv      types.PrivValidator
	csState *State
}

func newDefaultFakeNode(ctx context.Context, t *testing.T, logger log.Logger) *fakeNode {
	ng := nodeGen{cfg: getConfig(t), logger: logger}
	return ng.Generate(ctx, t)
}

func (n *fakeNode) start(ctx context.Context, t *testing.T) {
	require.NoError(t, n.csState.Start(ctx))
	t.Cleanup(n.csState.Wait)
}

func (n *fakeNode) stop() {
	n.csState.Stop()
}

// Chain is generated blockchain data for the first validator in a set.
type Chain struct {
	Config       *config.Config
	GenesisDoc   *types.GenesisDoc
	GenesisState sm.State
	States       []sm.State
	StateStore   sm.Store
	BlockStore   sm.BlockStore
	ProTxHash    crypto.ProTxHash
}

// ChainGenerator generates blockchain data with N validators to M depth
type ChainGenerator struct {
	t     *testing.T
	nVals int
	cfg   *config.Config
	len   int
}

// NewChainGenerator creates and returns ChainGenerator for N validators to M depth
func NewChainGenerator(t *testing.T, nVals int, len int) ChainGenerator {
	return ChainGenerator{
		t:     t,
		cfg:   configSetup(t),
		nVals: nVals,
		len:   len,
	}
}

func (c *ChainGenerator) generateChain(ctx context.Context, css []*State, vss []*validatorStub) []sm.State {
	height, round := css[0].Height, css[0].Round
	newRoundCh := subscribe(ctx, c.t, css[0].eventBus, types.EventQueryNewRound)
	proposalCh := subscribe(ctx, c.t, css[0].eventBus, types.EventQueryCompleteProposal)
	// start the machine; note height should be equal to InitialHeight here,
	// so we don't need to increment it
	startTestRound(ctx, css[0], height, round)
	incrementHeight(vss...)
	ensureNewRound(c.t, newRoundCh, height, 0)
	ensureNewProposal(c.t, proposalCh, height, round)
	rs := css[0].GetRoundState()

	css[0].config.DontAutoPropose = true

	blockID, err := rs.ProposalBlock.BlockID()
	require.NoError(c.t, err)
	signAddVotes(ctx, c.t, css[0], tmproto.PrecommitType, c.cfg.ChainID(), blockID, vss[1:c.nVals]...)

	ensureNewRound(c.t, newRoundCh, height+1, 0)

	states := make([]sm.State, 0, c.len)
	states = append(states, css[0].state)
	height++
	for ; height <= int64(c.len); height++ {
		incrementHeight(vss...)
		blockID = createSignSendProposal(ctx, c.t, css, vss, c.cfg.ChainID(), nil)
		ensureNewProposal(c.t, proposalCh, height, round)
		signAddVotes(ctx, c.t, css[0], tmproto.PrecommitType, c.cfg.ChainID(), blockID, vss[1:c.nVals]...)
		ensureNewRound(c.t, newRoundCh, height+1, 0)
		states = append(states, css[0].state)
	}
	return states
}

// Generate generates and returns blockchain data for a first validator in a set
func (c *ChainGenerator) Generate(ctx context.Context) Chain {
	gen := consensusNetGen{
		cfg:       c.cfg,
		nPeers:    c.nVals,
		nVals:     c.nVals,
		tickerFun: newMockTickerFunc(true),
		appFunc:   newKVStoreFunc(),
	}
	css, genDoc, _, validatorSetUpdate := gen.generate(ctx, c.t)

	pp := validatorSetUpdate[0]
	valSet, err := types.PB2TM.ValidatorSetFromProtoUpdate(genDoc.QuorumType, &pp)
	require.NoError(c.t, err)

	vss := make([]*validatorStub, c.nVals)
	for i := 0; i < c.nVals; i++ {
		vss[i] = newValidatorStub(css[i].privValidator, int32(i), 0)
	}

	proTxHash, err := css[0].PrivValidator().GetProTxHash(ctx)
	require.NoError(c.t, err)

	chain := Chain{
		Config:     c.cfg,
		GenesisDoc: genDoc,
		States:     c.generateChain(ctx, css, vss),
		StateStore: css[0].stateStore,
		BlockStore: css[0].blockStore,
		ProTxHash:  proTxHash,
	}
	chain.GenesisState, err = sm.MakeGenesisState(genDoc)
	require.NoError(c.t, err)
	chain.GenesisState.Validators = valSet
	return chain
}
