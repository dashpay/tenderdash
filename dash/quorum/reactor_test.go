package quorum

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/dashevo/dashd-go/btcjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tmcrypto "github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/bls12381"
	"github.com/tendermint/tendermint/crypto/ed25519"
	"github.com/tendermint/tendermint/internal/eventbus"
	"github.com/tendermint/tendermint/internal/p2p"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/types"
)

func TestReactor(t *testing.T) {
	// logger := log.NewTestingLogger(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	alice := newSecurityReactorInstance(ctx, t, "Alice")
	defer alice.cleanup(t)

	bob := newSecurityReactorInstance(ctx, t, "Bob")
	defer bob.cleanup(t)

	forwardBetweenChannels(ctx, t, alice.controlOutCh, bob.controlInCh)
	forwardBetweenChannels(ctx, t, bob.controlOutCh, alice.controlInCh)

	// set validators
	thresholdPubkey, err := bls12381.RecoverThresholdPublicKeyFromPublicKeys(
		[]tmcrypto.PubKey{alice.consensusPrivKey.PubKey(), bob.consensusPrivKey.PubKey()},
		[][]byte{alice.proTxHash, bob.proTxHash},
	)
	require.NoError(t, err)
	valsetUpdate := types.EventDataValidatorSetUpdate{
		ValidatorSetUpdates: []*types.Validator{alice.Validator(t), bob.Validator(t)},
		ThresholdPublicKey:  thresholdPubkey,
		QuorumHash:          tmcrypto.RandQuorumHash(),
		QuorumType:          btcjson.LLMQType_5_60,
	}

	// logger.Debug("publishing validator set updates", "event", valsetUpdate)
	assert.NoError(t, alice.eventBus.PublishEventValidatorSetUpdates(valsetUpdate))
	assert.NoError(t, bob.eventBus.PublishEventValidatorSetUpdates(valsetUpdate))

	// wait until validator updates get processed
	time.Sleep(10 * time.Millisecond)
	// Bob connected to Alice
	alice.peerUpdatesCh <- p2p.PeerUpdate{
		NodeID:    bob.nodeID(),
		Status:    p2p.PeerStatusUp,
		ProTxHash: bob.proTxHash,
	}

	// ...what means that Alice also connected to Bob
	bob.peerUpdatesCh <- p2p.PeerUpdate{
		NodeID:    alice.nodeID(),
		Status:    p2p.PeerStatusUp,
		ProTxHash: alice.proTxHash,
	}

	// let the handshake timeout happen
	time.Sleep(handshakeTimeout + 10*time.Millisecond)

LOOP:
	for {
		select {
		case msg := <-alice.controlErrCh:
			t.Error("alice: ", msg.Err)
		case msg := <-bob.controlErrCh:
			t.Error("bob: ", msg.Err)
		default:
			break LOOP
		}
	}
}

type securityReactorInstance struct {
	proTxHash types.ProTxHash

	// services
	eventBus *eventbus.EventBus
	reactor  *Reactor

	// keys
	p2pPrivKey       tmcrypto.PrivKey
	consensusPrivKey bls12381.PrivKey

	// channels

	peerUpdatesCh chan p2p.PeerUpdate
	peerUpdates   *p2p.PeerUpdates

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
) securityReactorInstance {
	logger := log.NewTestingLogger(t).With("label", label)
	instance := securityReactorInstance{
		eventBus: eventbus.NewDefault(logger),

		peerUpdatesCh: make(chan p2p.PeerUpdate, 1),

		controlInCh:  make(chan p2p.Envelope, 1),
		controlOutCh: make(chan p2p.Envelope, 1),
		controlErrCh: make(chan p2p.PeerError, 1),
	}

	err := instance.eventBus.Start(ctx)
	require.NoError(t, err)

	instance.peerUpdates = p2p.NewPeerUpdates(instance.peerUpdatesCh, 1)
	instance.p2pPrivKey = ed25519.GenPrivKey()

	instance.proTxHash = tmcrypto.RandProTxHash()
	instance.consensusPrivKey = bls12381.GenPrivKey()

	chCreator := func(ctx context.Context, desc *p2p.ChannelDescriptor) (*p2p.Channel, error) {
		return p2p.NewChannel(
				desc.ID,
				desc.MessageType,
				instance.controlInCh,
				instance.controlOutCh,
				instance.controlErrCh),
			nil
	}

	instance.reactor = NewReactor(
		logger.With("module", "dash"),
		func(ctx context.Context) *p2p.PeerUpdates { return instance.peerUpdates },
		instance.eventBus,
		chCreator,
		instance.nodeID(),
		instance.proTxHash,
		instance.consensusPrivKey,
	)
	require.NotNil(t, instance.reactor)

	err = instance.reactor.Start(ctx)
	require.NoError(t, err)

	return instance
}

// Validator creates a mock validator
func (p securityReactorInstance) Validator(t *testing.T) *types.Validator {
	validator := types.NewValidatorDefaultVotingPower(p.consensusPrivKey.PubKey(), p.proTxHash)
	validator.NodeAddress.NodeID = p.nodeID()
	validator.NodeAddress.Hostname = "127.0.0.1"
	validator.NodeAddress.Port = uint16(rand.Uint32())
	assert.NotZero(t, validator.NodeAddress.NodeID)

	return validator
}

// cleanup frees some resources allocated for tests
func (p securityReactorInstance) cleanup(t *testing.T) {
	p.reactor.Stop()
	p.eventBus.Stop()
}

func forwardBetweenChannels(ctx context.Context, t *testing.T, src, dst chan p2p.Envelope) {
	go func() {
		for {
			select {
			case msg := <-src:
				dst <- msg
			case <-ctx.Done():
				return
			}
		}
	}()
}
