package pop

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/dashevo/dashd-go/btcjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	db "github.com/tendermint/tm-db"

	tmcrypto "github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/bls12381"
	"github.com/tendermint/tendermint/crypto/ed25519"
	"github.com/tendermint/tendermint/dash/core"
	"github.com/tendermint/tendermint/dash/quorum"
	"github.com/tendermint/tendermint/internal/eventbus"
	"github.com/tendermint/tendermint/internal/p2p"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/privval"
	dashproto "github.com/tendermint/tendermint/proto/tendermint/dash"
	"github.com/tendermint/tendermint/types"
)

const (
	llmqType    = btcjson.LLMQType_100_67
	testTimeout = handshakeTimeout + 100*time.Millisecond //+ 130*time.Second
)

func TestReactorPositive(t *testing.T) {
	// logger := log.NewTestingLogger(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	alice, bob := newAliceAndBob(ctx, t)
	defer alice.cleanup(t)
	defer bob.cleanup(t)

	// bidirectional connection between Alice and Bob
	forwardChannels(ctx, t, alice.controlOutCh, bob.controlInCh)
	forwardChannels(ctx, t, bob.controlOutCh, alice.controlInCh)

	executePoP(ctx, t, &alice, &bob, false, false)

	cancel()
	time.Sleep(time.Millisecond)
}

func TestReactorNoResponse(t *testing.T) {
	// logger := log.NewTestingLogger(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	alice, bob := newAliceAndBob(ctx, t)
	defer alice.cleanup(t)
	defer bob.cleanup(t)

	forwardChannels(ctx, t, alice.controlOutCh, bob.controlInCh)
	// we don't allow connectivity from Bob to Alice to get a timeout

	executePoP(ctx, t, &alice, &bob, true, true)

	cancel()
	time.Sleep(time.Millisecond)
}

func TestReactorWrongBobSig(t *testing.T) {
	// logger := log.NewTestingLogger(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	alice, bob := newAliceAndBob(ctx, t)
	defer alice.cleanup(t)
	defer bob.cleanup(t)

	forwardChannels(ctx, t, alice.controlOutCh, bob.controlInCh)
	// messages from Bob to Alice have malformed signatures
	go func() {
		for {
			select {
			case msg := <-bob.controlOutCh:
				switch resp := msg.Message.(type) {
				case *dashproto.ValidatorChallenge:
					// noop
				case *dashproto.ValidatorChallengeResponse:
					resp.Signature[2] = 0x00
				default:
					t.Errorf("invalid msg type: %T", msg.Message)
				}

				alice.controlInCh <- msg

			case <-ctx.Done():
				return
			}
		}
	}()

	executePoP(ctx, t, &alice, &bob, true, false)

	cancel()
	time.Sleep(time.Millisecond)
}

func executePoP(ctx context.Context, t *testing.T, alice, bob *securityReactorInstance, expectAliceFail, expectBobFail bool) {
	quorumHash := alice.reactor.getQuorumHash(ctx)
	// quorumHash, err := alice.privValidator.GetFirstQuorumHash(ctx)
	// require.NoError(t, err)

	alicePubkey, err := alice.privValidator.GetPubKey(ctx, quorumHash)
	require.NoError(t, err)
	bobPubkey, err := bob.privValidator.GetPubKey(ctx, quorumHash)
	require.NoError(t, err)

	aliceProTxHash, err := alice.reactor.getProTxHash(ctx)
	require.NoError(t, err)
	bobProTxHash, err := bob.reactor.getProTxHash(ctx)
	require.NoError(t, err)

	thresholdPublicKey, err := bls12381.RecoverThresholdPublicKeyFromPublicKeys(
		[]tmcrypto.PubKey{alicePubkey, bobPubkey},
		[][]byte{aliceProTxHash, bobProTxHash},
	)
	require.NoError(t, err)

	valsetUpdate := types.EventDataValidatorSetUpdate{
		ValidatorSetUpdates: []*types.Validator{alice.Validator(ctx, t), bob.Validator(ctx, t)},
		ThresholdPublicKey:  thresholdPublicKey,
		QuorumHash:          quorumHash,
		QuorumType:          llmqType,
	}

	// logger.Debug("publishing validator set updates", "event", valsetUpdate)
	assert.NoError(t, alice.eventBus.PublishEventValidatorSetUpdates(valsetUpdate))
	assert.NoError(t, bob.eventBus.PublishEventValidatorSetUpdates(valsetUpdate))

	// wait until validator updates get processed
	time.Sleep(100 * time.Millisecond)
	// Bob connected to Alice
	err = alice.reactor.peerManager.Accepted(bob.nodeID(), p2p.SetProTxHashToPeerInfo(bobProTxHash))
	require.NoError(t, err)
	alice.reactor.peerManager.Ready(ctx, bob.nodeID(), p2p.ChannelIDSet{})
	err = bob.reactor.peerManager.Accepted(alice.nodeID(), p2p.SetProTxHashToPeerInfo(aliceProTxHash))
	require.NoError(t, err)
	bob.reactor.peerManager.Ready(ctx, alice.nodeID(), p2p.ChannelIDSet{})

	// alice.peerUpdatesCh <- p2p.PeerUpdate{
	// 	NodeID:    bob.nodeID(),
	// 	Status:    p2p.PeerStatusUp,
	// 	ProTxHash: bob.reactor.getQuorumHash(ctx),
	// }

	// ...what means that Alice also connected to Bob
	// bob.peerUpdatesCh <- p2p.PeerUpdate{
	// 	NodeID:    alice.nodeID(),
	// 	Status:    p2p.PeerStatusUp,
	// 	ProTxHash: alice.reactor.getQuorumHash(ctx),
	// }

	aliceFailed := false
	bobFailed := false

LOOP:
	for {
		select {
		case msg := <-alice.controlErrCh:
			t.Log("alice: ", msg.Err)
			aliceFailed = true
		case msg := <-bob.controlErrCh:
			t.Log("bob: ", msg.Err)
			bobFailed = true
		case <-time.After(testTimeout):
			t.Logf("handshake timeout passed")
			break LOOP
		}

		if aliceFailed == expectAliceFail && bobFailed == expectBobFail {
			break LOOP
		}
	}

	assert.Equal(t, expectAliceFail, aliceFailed, "Alice should fail")
	assert.Equal(t, expectBobFail, bobFailed, "Bob should fail")
}

func newAliceAndBob(ctx context.Context, t *testing.T) (securityReactorInstance, securityReactorInstance) {

	quorumHash := tmcrypto.RandQuorumHash()

	aliceProTxHash := tmcrypto.RandProTxHash()
	bobProTxHash := tmcrypto.RandProTxHash()

	aliceConsensusPrivKey := bls12381.GenPrivKey()
	bobConsensusPrivKey := bls12381.GenPrivKey()

	thresholdPubkey, err := bls12381.RecoverThresholdPublicKeyFromPublicKeys(
		[]tmcrypto.PubKey{aliceConsensusPrivKey.PubKey(), bobConsensusPrivKey.PubKey()},
		[][]byte{aliceProTxHash, bobProTxHash},
	)
	require.NoError(t, err)

	alice := newSecurityReactorInstance(ctx, t, "alice", aliceProTxHash, quorumHash, aliceConsensusPrivKey, thresholdPubkey)
	bob := newSecurityReactorInstance(ctx, t, "bob", bobProTxHash, quorumHash, bobConsensusPrivKey, thresholdPubkey)

	return alice, bob
}

type securityReactorInstance struct {
	// services
	eventBus *eventbus.EventBus
	reactor  *Reactor
	peerDB   db.DB

	// keys
	p2pPrivKey    tmcrypto.PrivKey
	privValidator types.PrivValidator
	// proTxHash     tmcrypto.ProTxHash

	// channels

	// peerUpdatesCh chan p2p.PeerUpdate
	// peerUpdates   *p2p.PeerUpdates

	controlInCh  chan p2p.Envelope
	controlOutCh chan p2p.Envelope
	controlErrCh chan p2p.PeerError
}

func (p securityReactorInstance) p2pPubKey() tmcrypto.PubKey { return p.p2pPrivKey.PubKey() }
func (p securityReactorInstance) nodeID() types.NodeID       { return types.NodeIDFromPubKey(p.p2pPubKey()) }

// setup creates ValidatorConnExecutor and some dependencies.
// Use `defer cleanup()` to free the resources.
func newSecurityReactorInstance(
	ctx context.Context,
	t *testing.T,
	label string,
	proTxHash tmcrypto.ProTxHash,
	quorumHash tmcrypto.QuorumHash,
	privKey bls12381.PrivKey,
	thresholdPublicKey tmcrypto.PubKey,
) securityReactorInstance {
	logger := log.NewTestingLogger(t).With("label", label)
	instance := securityReactorInstance{
		eventBus: eventbus.NewDefault(logger),

		// peerUpdatesCh: make(chan p2p.PeerUpdate, 1),

		controlInCh:  make(chan p2p.Envelope, 10),
		controlOutCh: make(chan p2p.Envelope, 10),
		controlErrCh: make(chan p2p.PeerError, 10),
	}

	err := instance.eventBus.Start(ctx)
	require.NoError(t, err)

	// instance.peerUpdates = p2p.NewPeerUpdates(instance.peerUpdatesCh, 1)
	instance.p2pPrivKey = ed25519.GenPrivKey()

	localPrivval := types.NewMockPVWithParams(
		privKey,
		proTxHash,
		quorumHash,
		thresholdPublicKey,
		false,
		false,
	)
	localPrivval.UpdatePrivateKey(ctx, privKey, quorumHash, thresholdPublicKey, 1)
	mockClient := core.NewMockClient("testchain", llmqType, localPrivval, true)
	instance.privValidator, err = privval.NewDashCoreSignerClient(mockClient, llmqType)
	require.NoError(t, err)

	chCreator := func(ctx context.Context, desc *p2p.ChannelDescriptor) (*p2p.Channel, error) {
		return p2p.NewChannel(
				desc.ID,
				desc.MessageType,
				instance.controlInCh,
				instance.controlOutCh,
				instance.controlErrCh),
			nil
	}

	vs := types.NewEmptyValidatorSet()
	vs.QuorumHash = quorumHash

	instance.peerDB = db.NewMemDB()
	peerManager, err := p2p.NewPeerManager(instance.nodeID(), instance.peerDB, p2p.PeerManagerOptions{})
	require.NoError(t, err)

	instance.reactor, err = NewReactor(
		logger.With("module", "dash"),
		peerManager.Subscribe, //func(ctx context.Context) *p2p.PeerUpdates { return instance.peerUpdates },
		instance.eventBus,
		chCreator,
		instance.nodeID(),
		instance.privValidator,
		vs,
		peerManager,
		[]p2p.NodeIDResolver{quorum.NewTCPNodeIDResolver()},
	)
	require.NoError(t, err)
	require.NotNil(t, instance.reactor)

	err = instance.reactor.Start(ctx)
	require.NoError(t, err)

	return instance
}

// Validator creates a mock validator
func (p securityReactorInstance) Validator(ctx context.Context, t *testing.T) *types.Validator {
	quorumHash := p.reactor.getQuorumHash(ctx)
	assert.NotEmpty(t, quorumHash)

	validator := p.privValidator.ExtractIntoValidator(ctx, quorumHash)
	assert.NotNil(t, validator.PubKey)
	validator.NodeAddress.NodeID = p.nodeID()
	validator.NodeAddress.Hostname = "127.0.0.1"
	validator.NodeAddress.Port = uint16(rand.Uint32())
	assert.NotZero(t, validator.NodeAddress.NodeID)
	// t.Logf("Validator: %+v\n", validator)
	return validator
}

// cleanup frees some resources allocated for tests
func (p securityReactorInstance) cleanup(t *testing.T) {
	p.reactor.Stop()
	p.eventBus.Stop()
	require.NoError(t, p.peerDB.Close())
}

func forwardChannels(ctx context.Context, t *testing.T, src, dst chan p2p.Envelope) {
	go func() {
		for {
			select {
			case msg := <-src:
				// t.Logf("forwarding msg %+v\n", msg)
				dst <- msg
				// t.Logf("forwarded msg %+v\n", msg)

			case <-ctx.Done():
				return
			}
		}
	}()
}
