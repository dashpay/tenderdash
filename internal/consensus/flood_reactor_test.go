//go:build floodclient

package consensus

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/dash"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/conn"
	"github.com/dashpay/tenderdash/libs/log"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	"github.com/dashpay/tenderdash/test/floodclient"
	"github.com/dashpay/tenderdash/types"
)

// This file is the reactor-level proof the load suite cannot give (see
// load_harness_test.go): the load suite injects messages straight into the peer
// lanes, downstream of the reactor, so it measures the scheduler and the budget
// but says nothing about the layer that actually admits traffic off the wire.
//
// Here a real consensus Reactor is stood up over the production p2p stack —
// MConnTransport + Router + PeerManager, wired exactly as node.go wires it
// (router.OpenChannel as the channel creator, peerManager.Subscribe as the peer
// event source) — and the floodclient dials in over TCP as an ordinary peer and
// floods forged consensus messages. What the test asserts is §0 of the rollout
// spec: the message reaches the reactor, the reactor sheds it (a drop counter
// moves), and it does so non-punitively (no PeerError is raised, the attacker is
// never evicted). That is the whole chain the load suite skips: dial,
// SecretConnection, NodeInfo exchange, router admission, channel framing,
// envelope decode, per-peer rate limit, peer lanes and the verification budget.

// floodReactorTarget is a single consensus node running its real reactor over
// the production p2p transport, instrumented so a test can read the drop
// counters and the peer-error queue while the node runs.
type floodReactorTarget struct {
	reactor     *Reactor
	state       *State
	peerManager *p2p.PeerManager
	nodeID      types.NodeID
	endpoint    p2p.Endpoint
	chainID     string
	validators  []floodclient.ForgedValidator
	vss         []*validatorStub

	laneDrops   *syncCounter
	budgetDrops *syncCounter
	stateDrops  *syncCounter
	partDrops   *syncCounter
	verifyFails *syncCounter
}

// newFloodReactorTarget builds the node and starts it. The node runs in a
// four-validator set but is the only member live, so it can never reach a 2/3
// majority and stays at its initial height while the flood runs — which keeps
// the height the attacker forges for current for the whole run.
func newFloodReactorTarget(ctx context.Context, t *testing.T, pmOpts p2p.PeerManagerOptions) *floodReactorTarget {
	t.Helper()

	logger := log.NewNopLogger()

	tgt := &floodReactorTarget{
		laneDrops:   &syncCounter{},
		budgetDrops: &syncCounter{},
		stateDrops:  &syncCounter{},
		partDrops:   &syncCounter{},
		verifyFails: &syncCounter{},
	}

	m := NopMetrics()
	m.PeerLaneDrops = tgt.laneDrops
	m.VerificationBudgetDrops = tgt.budgetDrops
	m.StateChannelDrops = tgt.stateDrops
	m.BlockPartProofDrops = tgt.partDrops
	m.ProposalVerifyFailures = tgt.verifyFails

	cs, vss := makeState(ctx, t, makeStateArgs{
		validators: 4,
		logger:     log.NewNopLogger(), // the flood makes the node log at debug per message
		stateOpts:  []StateOption{StateMetrics(m)},
	})
	tgt.state = cs
	tgt.vss = vss
	sd := cs.GetStateData()
	tgt.chainID = sd.state.ChainID

	// The target's real validator identities. A forged vote must carry one of
	// these to reach signature verification; an attacker on a real network reads
	// them off-chain. Without them the flood is rejected before the budget.
	for i, v := range sd.Validators.Validators {
		tgt.validators = append(tgt.validators, floodclient.ForgedValidator{
			ProTxHash: v.ProTxHash,
			Index:     int32(i),
		})
	}

	// The node's own identity for the p2p handshake. The chain ID advertised on
	// the wire is the consensus chain ID, so a peer that matches it is admitted.
	nodeKey := types.GenNodeKey()
	tgt.nodeID = nodeKey.ID
	nodeInfo := floodclient.Identity{NodeKey: nodeKey}.NodeInfo(tgt.chainID, "127.0.0.1:26656")

	descs := p2p.ConsensusChannelDescriptors()
	descSlice := make([]*p2p.ChannelDescriptor, 0, len(descs))
	for _, d := range descs {
		descSlice = append(descSlice, d)
	}

	transport := p2p.NewMConnTransport(logger, conn.DefaultMConnConfig(), descSlice, p2p.MConnTransportOptions{})
	listenEP := &p2p.Endpoint{Protocol: p2p.MConnProtocol, IP: net.IPv4(127, 0, 0, 1), Port: 0}

	peerManager, err := p2p.NewPeerManager(ctx, nodeKey.ID, dbm.NewMemDB(), pmOpts)
	require.NoError(t, err)
	tgt.peerManager = peerManager

	router, err := p2p.NewRouter(
		logger,
		p2p.NopMetrics(),
		nodeKey.PrivKey,
		peerManager,
		func() *types.NodeInfo { return &nodeInfo },
		transport,
		listenEP,
		p2p.RouterOptions{HandshakeTimeout: 5 * time.Second},
	)
	require.NoError(t, err)
	require.NoError(t, router.Start(ctx))
	t.Cleanup(router.Wait)

	// Wire the reactor exactly as node.go does: channels come from the router,
	// peer events from the peer manager. waitSync=false so the node processes
	// consensus messages immediately rather than waiting for block sync.
	sCtx := dash.ContextWithProTxHash(ctx, cs.privValidator.ProTxHash)
	reactor := NewReactor(
		logger,
		cs,
		router.OpenChannel,
		peerManager.Subscribe,
		cs.eventBus,
		false,
		m,
	)
	tgt.reactor = reactor
	require.NoError(t, reactor.Start(sCtx))
	t.Cleanup(reactor.Wait)

	boundEP, err := transport.Endpoint()
	require.NoError(t, err)
	require.NotZero(t, boundEP.Port)
	tgt.endpoint = *boundEP

	return tgt
}

// floodTarget describes tgt to the floodclient.
func (tgt *floodReactorTarget) floodTarget() floodclient.Target {
	return floodclient.Target{
		Host:    tgt.endpoint.IP.String(),
		Port:    tgt.endpoint.Port,
		NodeID:  tgt.nodeID,
		Network: tgt.chainID,
	}
}

// forgeConfig is the target-specific data the profiles need to reach the node's
// verification path: its real validator identities, quorum hash, and committed
// core-chain-locked height.
func (tgt *floodReactorTarget) forgeConfig() floodclient.ForgeConfig {
	sd := tgt.state.GetStateData()
	return floodclient.ForgeConfig{
		Validators:            tgt.validators,
		QuorumHash:            sd.Validators.QuorumHash,
		CoreChainLockedHeight: sd.state.LastCoreChainLockedBlockHeight,
	}
}

// signingIdentity builds the real signing capability of validator i. In this
// harness the test holds the validator keys (a devnet where you control a
// validator); on a real adversarial network you would not.
func (tgt *floodReactorTarget) signingIdentity(i int) *floodclient.SigningIdentity {
	sd := tgt.state.GetStateData()
	return &floodclient.SigningIdentity{
		PrivVal:    tgt.vss[i].PrivValidator,
		Index:      tgt.vss[i].Index,
		ChainID:    sd.state.ChainID,
		QuorumType: sd.Validators.QuorumType,
		QuorumHash: sd.Validators.QuorumHash,
	}
}

// runReactorFlood dials `identities` connections into tgt and floods the named
// profile until `metric` moves past its starting value or the deadline passes.
// It reads the node's current height and round each tick so the forged messages
// stay current as the node advances rounds, and returns the messages sent.
func runReactorFlood(
	ctx context.Context,
	t *testing.T,
	tgt *floodReactorTarget,
	profile string,
	identities int,
	perIdentityRate float64,
	deadline time.Duration,
	metric func() float64,
) (sent int64, moved bool) {
	t.Helper()

	prof, ok := floodclient.BuildProfiles(tgt.forgeConfig())[profile]
	require.Truef(t, ok, "unknown profile %q", profile)

	floodCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	start := metric()

	conns := make([]*floodclient.Conn, 0, identities)
	for i := 0; i < identities; i++ {
		c, err := floodclient.Dial(floodCtx, log.NewNopLogger(), floodclient.NewIdentity(), tgt.floodTarget(), 5*time.Second)
		require.NoError(t, err, "flood identity %d must complete the handshake", i)
		t.Cleanup(func() { _ = c.Close() })
		conns = append(conns, c)
	}

	interval := time.Duration(float64(time.Second) / perIdentityRate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var total int64
	for {
		select {
		case <-floodCtx.Done():
			return total, metric() > start
		case <-ticker.C:
			sd := tgt.state.GetStateData()
			height, round := sd.Height, sd.Round
			for _, c := range conns {
				if err := c.Send(floodCtx, prof.Next(height, round)); err == nil {
					total++
				}
			}
			if metric() > start {
				return total, true // confirmed; stop early to keep the run short
			}
		}
	}
}

// watchEvictions returns a function reporting how many times the peer manager
// has taken a peer down since the watch began — the observable for whether the
// node evicted the attacker.
func watchEvictions(ctx context.Context, tgt *floodReactorTarget) func() int {
	watch := tgt.peerManager.Subscribe(ctx, "flood-test-watch")
	var mu sync.Mutex
	var n int
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case up := <-watch.Updates():
				if up.Status == p2p.PeerStatusDown {
					mu.Lock()
					n++
					mu.Unlock()
				}
			}
		}
	}()
	return func() int { mu.Lock(); defer mu.Unlock(); return n }
}

// TestFloodClient_ReactorShedsFloodNonPunitively is the reactor-level proof for
// the rollout spec's flood profiles that the node must shed WITHOUT punishing
// the sender (§0). Each case stands up a real consensus reactor over the real
// p2p stack, floods one profile over TCP, and asserts the profile's drop counter
// moves while the attacker is never evicted and no peer error is queued.
//
// This is the assertion the load suite cannot make: it injects below the
// reactor, so it never exercises admission, decode, the per-peer rate limits or
// the lanes. Here the forged message travels the whole path a real peer's does.
func TestFloodClient_ReactorShedsFloodNonPunitively(t *testing.T) {
	cases := []struct {
		name    string
		profile string
		metric  func(*floodReactorTarget) float64
		// identities and rate are sized so the profile's ceiling is crossed:
		// the vote flood must beat the 300 work-unit/s node budget, the maj23
		// flood the per-peer State-channel ceiling.
		identities int
		rate       float64
	}{
		{
			// Cheap flood (spec §3 profile 1): ~1 verification unit each, sheds
			// via the node-wide verification budget or the per-peer lanes.
			name: "prevote", profile: "prevote",
			metric:     func(tg *floodReactorTarget) float64 { return tg.budgetDrops.count() + tg.laneDrops.count() },
			identities: 8, rate: 400,
		},
		{
			// State/VoteSetBits verify no signature, so the verification budget
			// does not bound them; a VoteSetMaj23 is priced highest of all on
			// those channels because answering one costs a bit array over every
			// validator. Over the per-peer ceiling it moves StateChannelDrops.
			name: "maj23", profile: "maj23",
			metric:     func(tg *floodReactorTarget) float64 { return tg.stateDrops.count() },
			identities: 4, rate: 400,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			tgt := newFloodReactorTarget(ctx, t, p2p.PeerManagerOptions{})
			evictions := watchEvictions(ctx, tgt)

			sent, moved := runReactorFlood(ctx, t, tgt, tc.profile, tc.identities, tc.rate,
				30*time.Second, func() float64 { return tc.metric(tgt) })

			require.Positive(t, sent, "the flood must have put messages on the wire")
			require.True(t, moved, "the reactor must shed the %s flood (metric did not move)", tc.name)

			// Non-punitive: these floods are unattributable, so the node must not
			// blame the sender. No peer error is queued and no peer is taken down.
			require.Empty(t, tgt.state.peerErrorQueue.ch,
				"%s flood must be shed silently, not reported as peer errors", tc.name)
			require.Zero(t, evictions(), "the attacker must not be evicted for the %s flood", tc.name)

			reportf(t, "%s flood: sent=%d budget=%v lane=%v state=%v part=%v verifyfail=%v",
				tc.name, sent, tgt.budgetDrops.count(), tgt.laneDrops.count(),
				tgt.stateDrops.count(), tgt.partDrops.count(), tgt.verifyFails.count())
		})
	}
}

// TestFloodProfiles_StructurallyValidAtReactorDecode asserts every flood
// profile produces a message the reactor accepts at its decode boundary
// (MsgFromProto, which runs the message's ValidateBasic). This is the property
// the tool depends on: a structurally-invalid message would be rejected at
// decode, before any defense, and would prove nothing about the node shedding
// attack traffic. So each profile must be rejected only at or after
// verification — never at decode.
func TestFloodProfiles_StructurallyValidAtReactorDecode(t *testing.T) {
	cfg := floodclient.ForgeConfig{
		Validators: []floodclient.ForgedValidator{{ProTxHash: make([]byte, crypto.ProTxHashSize), Index: 0}},
		QuorumHash: make([]byte, crypto.HashSize),
	}
	profiles := floodclient.BuildProfiles(cfg)
	require.NotEmpty(t, profiles)

	// The malformed profiles emit messages the node must refuse — an undefined
	// vote-extension type or an over-long extension list — so they are not valid
	// at decode by design and are excluded here; their rejection is pinned in the
	// floodclient package tests.
	malformed := floodclient.MalformedProfiles()

	for name, prof := range profiles {
		if malformed[name] {
			continue
		}
		t.Run(name, func(t *testing.T) {
			// Several messages: the profiles that alternate shapes (blockpart,
			// state, maj23) must produce a valid message on every branch.
			for i := 0; i < 4; i++ {
				msg := prof.Next(1, 0)
				decoded, err := MsgFromProto(msg)
				require.NoErrorf(t, err, "%s message %d must decode and pass ValidateBasic", name, i)
				require.NotNil(t, decoded)
			}
		})
	}
}

// connectedPeerIDs returns the distinct peer IDs the reactor currently has peer
// state for — the peers whose connection the node has admitted and set up.
func (tgt *floodReactorTarget) connectedPeerIDs() map[types.NodeID]struct{} {
	tgt.reactor.mtx.RLock()
	defer tgt.reactor.mtx.RUnlock()
	ids := make(map[types.NodeID]struct{}, len(tgt.reactor.peers))
	for id := range tgt.reactor.peers {
		ids[id] = struct{}{}
	}
	return ids
}

// TestFloodClient_HoldsManyConnectionSlots validates the multi-identity slot
// behavior the flood needs to saturate a node: --identities N must open N real
// connections, each a distinct node ID (IDs are free, which is the threat
// model), and the node must admit and hold them all when it has room.
func TestFloodClient_HoldsManyConnectionSlots(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Room for well over the identities dialed, so admission is not the thing
	// under test here (the cap is exercised by the sibling test below).
	tgt := newFloodReactorTarget(ctx, t, p2p.PeerManagerOptions{})

	const identities = 12
	want := make(map[types.NodeID]struct{}, identities)
	for i := 0; i < identities; i++ {
		id := floodclient.NewIdentity()
		c, err := floodclient.Dial(ctx, log.NewNopLogger(), id, tgt.floodTarget(), 5*time.Second)
		require.NoErrorf(t, err, "identity %d must connect", i)
		t.Cleanup(func() { _ = c.Close() })
		require.NotContains(t, want, c.NodeID(), "each identity must be a distinct node ID")
		want[c.NodeID()] = struct{}{}
	}
	require.Len(t, want, identities, "the client must mint distinct node IDs")

	// The node admits and sets up each connection; wait for its peer state to
	// reflect all of them.
	require.Eventually(t, func() bool {
		got := tgt.connectedPeerIDs()
		for id := range want {
			if _, ok := got[id]; !ok {
				return false
			}
		}
		return true
	}, 15*time.Second, 100*time.Millisecond, "the node must hold all %d connection slots", identities)

	reportf(t, "held %d distinct connection slots", identities)
}

// TestFloodClient_AdmissionRejectsExcessSlots is the counterpart: at and above
// MaxConnected+MaxConnectedUpgrade the node's admission must reject the excess.
// That is the correct behavior to observe under a slot flood, not a bug — the
// tool cannot hold more slots than the node offers.
func TestFloodClient_AdmissionRejectsExcessSlots(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const maxConnected, upgrade = 5, 2
	const capSlots = maxConnected + upgrade
	tgt := newFloodReactorTarget(ctx, t, p2p.PeerManagerOptions{
		MaxConnected:        maxConnected,
		MaxConnectedUpgrade: upgrade,
	})

	// Dial well past the cap. Excess dials either fail the handshake or are
	// dropped right after; keep whatever connects alive so the slots stay held.
	const identities = capSlots + 6
	for i := 0; i < identities; i++ {
		c, err := floodclient.Dial(ctx, log.NewNopLogger(), floodclient.NewIdentity(), tgt.floodTarget(), 3*time.Second)
		if err != nil {
			continue
		}
		t.Cleanup(func() { _ = c.Close() })
	}

	// However many identities dial, the node never holds more than its cap. Give
	// it a moment to settle, then assert the ceiling holds over a short window.
	require.Eventually(t, func() bool { return len(tgt.connectedPeerIDs()) > 0 }, 10*time.Second, 100*time.Millisecond)
	for i := 0; i < 10; i++ {
		require.LessOrEqualf(t, len(tgt.connectedPeerIDs()), capSlots,
			"the node must never hold more than MaxConnected+MaxConnectedUpgrade=%d slots", capSlots)
		time.Sleep(100 * time.Millisecond)
	}
	reportf(t, "node held at most %d slots under a %d-identity slot flood", capSlots, identities)
}

// TestFloodClient_ValidBlockSigInvalidExtension proves the key-requiring profile
// (rollout spec §3 profile 3): a precommit whose block signature is GENUINE but
// whose final vote-extension signature is not. The profile needs a real
// validator key and is not registered without one; with one, the block
// signature it produces verifies against the validator's public key while the
// full vote does not (the corrupt extension). This is the shape that makes the
// node pay for extension verification, the other half of the staged-permit
// contract the forged-block-signature precommit exercises from the cheap side.
func TestFloodClient_ValidBlockSigInvalidExtension(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cs, vss := makeState(ctx, t, makeStateArgs{validators: 4, logger: log.NewNopLogger()})
	sd := cs.GetStateData()

	// Without a signer the profile is not available: the tool cannot forge a
	// valid block signature without a key.
	require.NotContains(t, floodclient.BuildProfiles(floodclient.ForgeConfig{}),
		"precommit-invalid-extension", "the profile must require a key")

	signer := &floodclient.SigningIdentity{
		PrivVal:    vss[1].PrivValidator,
		Index:      vss[1].Index,
		ChainID:    sd.state.ChainID,
		QuorumType: sd.Validators.QuorumType,
		QuorumHash: sd.Validators.QuorumHash,
	}
	prof, ok := floodclient.BuildProfiles(floodclient.ForgeConfig{Signer: signer})["precommit-invalid-extension"]
	require.True(t, ok, "the profile must be registered when a signer is supplied")

	msg := prof.Next(1, 0).(*tmcons.Vote)
	v, err := types.VoteFromProto(msg.Vote)
	require.NoError(t, err)
	require.NoError(t, v.ValidateBasic(), "the message must be structurally valid")

	val := sd.Validators.GetByIndex(1)
	require.NotNil(t, val)

	// The block signature is genuine: it verifies against the validator's key.
	blockSignID := types.VoteBlockSignID(sd.state.ChainID, msg.Vote, sd.Validators.QuorumType, sd.Validators.QuorumHash)
	require.True(t, val.PubKey.VerifySignatureDigest(blockSignID, msg.Vote.BlockSignature),
		"the block signature must be a genuine signature by the validator")

	// But the full vote does not verify: the final extension signature is forged,
	// so the node only discovers the problem after paying for the block signature
	// and the earlier extensions.
	require.Error(t, v.Verify(sd.state.ChainID, sd.Validators.QuorumType, sd.Validators.QuorumHash, val.PubKey, val.ProTxHash),
		"the corrupt final extension must make the whole vote fail verification")

	reportf(t, "valid-block-sig/invalid-extension: block signature genuine, extension forged")
}

// fixedLiveState is a floodclient.LiveState that reports a height/round the test
// controls, standing in for the RPC-backed source the CLI uses. Reporting a
// round the node is not already on lets the test prove the honest voter votes at
// whatever live state reports rather than a value frozen at construction.
type fixedLiveState struct {
	height int64
	round  int32
}

func (s fixedLiveState) CurrentHeightRound(context.Context) (int64, int32, error) {
	return s.height, s.round, nil
}

// TestFloodClient_HonestVoterTracksLiveHeightRound is the friction-2 proof: the
// honest voter derives its vote's height and round from live state each time it
// votes, rather than from a static value baked in at startup. The test points it
// at a live-state source reporting the node's real height but a round the node
// is not currently on; a live-tracking voter votes at that round (and the node
// accepts it), whereas the previous static voter could only ever vote at the
// one round it was configured with.
func TestFloodClient_HonestVoterTracksLiveHeightRound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tgt := newFloodReactorTarget(ctx, t, p2p.PeerManagerOptions{})
	evictions := watchEvictions(ctx, tgt)

	sd := tgt.state.GetStateData()
	liveHeight := sd.Height
	baseRound := sd.Round

	// Live state reports the node's real height (so the vote is accepted) and a
	// round the node is not on. A voter pinned to a static round could not land a
	// vote here.
	reportedRound := baseRound + 1
	live := fixedLiveState{height: liveHeight, round: reportedRound}

	conn, err := floodclient.Dial(ctx, log.NewNopLogger(), floodclient.NewIdentity(), tgt.floodTarget(), 5*time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	honest := floodclient.NewHonestVoter(conn, tgt.signingIdentity(1))

	gotHeight, gotRound, err := honest.SendPrevoteLive(ctx, live)
	require.NoError(t, err)
	require.Equal(t, liveHeight, gotHeight, "the voter must vote at the height live state reports")
	require.Equal(t, reportedRound, gotRound, "the voter must vote at the round live state reports")

	// The vote is accepted into the round live state reported — not the round the
	// node happens to be on. Resend each poll: early sends can race the peer-state
	// setup the vote path needs.
	require.Eventually(t, func() bool {
		_, _, _ = honest.SendPrevoteLive(ctx, live)
		prevotes := tgt.state.GetStateData().Votes.Prevotes(reportedRound)
		return prevotes != nil && prevotes.GetByIndex(1) != nil
	}, 20*time.Second, 200*time.Millisecond,
		"the honest vote must be accepted at the live-reported round, proving the voter tracks live state")

	// A genuinely-signed vote at the live height/round is honest traffic: it must
	// not be punished.
	require.Empty(t, tgt.state.peerErrorQueue.ch, "the honest vote must not be reported as a peer error")
	require.Zero(t, evictions(), "the honest voter must not be evicted")

	reportf(t, "honest voter tracked live state: voted at height %d round %d (node base round %d)",
		gotHeight, gotRound, baseRound)
}

// TestFloodClient_MixedModeHonestVoteAccepted is the mixed-mode proof: an honest
// client sending a genuinely-signed vote is served by the same node that is
// shedding a forged-prevote flood. This is what answers §0 directly — the node
// sheds ATTACK traffic while continuing to accept HONEST traffic.
//
// The honest vote is a nil prevote signed by a real validator key. Voting FOR
// the proposed block would need the node's round state to learn the block ID;
// that tracking is what an honest client needs on a real devnet, and is out of
// scope here where the point is that a correctly-signed vote is accepted at all.
func TestFloodClient_MixedModeHonestVoteAccepted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tgt := newFloodReactorTarget(ctx, t, p2p.PeerManagerOptions{})
	evictions := watchEvictions(ctx, tgt)

	// The height the node sits at (it cannot advance height alone); a vote for
	// this height and any round is retained in its height-vote-set.
	baseHeight := tgt.state.GetStateData().Height

	// Start the attack: forged prevotes from several identities the node sheds.
	// This goroutine must not touch t/require — it runs past the test body.
	attackers := make([]*floodclient.Conn, 0, 6)
	forge := floodclient.BuildProfiles(tgt.forgeConfig())["prevote"]
	for i := 0; i < 6; i++ {
		c, derr := floodclient.Dial(ctx, log.NewNopLogger(), floodclient.NewIdentity(), tgt.floodTarget(), 5*time.Second)
		require.NoError(t, derr, "attacker %d must connect", i)
		t.Cleanup(func() { _ = c.Close() })
		attackers = append(attackers, c)
	}
	go func() {
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h := tgt.state.GetStateData().Height
				for _, c := range attackers {
					_ = c.Send(ctx, forge.Next(h, 0))
				}
			}
		}
	}()

	// The honest client: validator 1 connects and sends a genuinely-signed nil
	// prevote for the node's height at round 0. A correctly-signed vote is
	// verified and added to the height-vote-set, which retains it.
	honestConn, err := floodclient.Dial(ctx, log.NewNopLogger(), floodclient.NewIdentity(), tgt.floodTarget(), 5*time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { _ = honestConn.Close() })
	honest := floodclient.NewHonestVoter(honestConn, tgt.signingIdentity(1))

	// While the node is demonstrably shedding the flood, it must ACCEPT the honest
	// vote: it lands in the prevote set for the round it was signed for. Resend
	// each poll until both hold — early honest sends can race the peer-state setup
	// the vote path needs, and the flood takes a moment to saturate the budget.
	honestAccepted := func() bool {
		prevotes := tgt.state.GetStateData().Votes.Prevotes(0)
		return prevotes != nil && prevotes.GetByIndex(1) != nil
	}
	require.Eventually(t, func() bool {
		_ = honest.SendPrevote(ctx, baseHeight, 0)
		return honestAccepted() && tgt.budgetDrops.count()+tgt.laneDrops.count() > 0
	}, 25*time.Second, 200*time.Millisecond,
		"the node must accept the honest vote while shedding the flood")

	// The honest voter was not punished for the company it keeps.
	require.True(t, honestAccepted(), "the honest vote must be recorded")
	require.Empty(t, tgt.state.peerErrorQueue.ch, "the honest vote must not be reported as a peer error")
	require.Zero(t, evictions(), "no peer may be evicted")

	reportf(t, "mixed mode: honest prevote accepted while flood shed (budget=%v lane=%v)",
		tgt.budgetDrops.count(), tgt.laneDrops.count())
}
