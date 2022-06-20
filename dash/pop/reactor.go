package pop

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/internal/eventbus"
	"github.com/tendermint/tendermint/internal/p2p"
	tmpubsub "github.com/tendermint/tendermint/internal/pubsub"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/libs/service"
	dashproto "github.com/tendermint/tendermint/proto/tendermint/dash"
	"github.com/tendermint/tendermint/types"
)

const (
	// defaultTimeout is timeout that is applied to setup / cleanup code
	defaultTimeout = 1 * time.Second
	// defaultEventBusCapacity determines how many events can wait in the event bus for processing. 10 looks very safe.
	defaultEventBusCapacity = 10

	// TODO move to config file
	handshakeTimeout     = 3 * time.Second
	proofOfPosessionName = "PoP"
)

var (
	ErrPeerAuthTimeout = errors.New("remote validator authentication failed")
)

// Reactor executes Proof-of-Possession protocol against other validators
// to ensure they really have their consensus key
type Reactor struct {
	service.BaseService
	logger log.Logger
	mtx    sync.RWMutex

	// chCreator
	chCreator      p2p.ChannelCreator // channel creator used to create DashControlChannel
	controlChannel *p2p.Channel

	peerEvents p2p.PeerEventSubscriber // peerEvents is a subscription for peer up/down notifications

	nodeID types.NodeID
	// p2pPrivKey       crypto.PrivKey
	consensusPrivKey crypto.PrivKey
	proTxHash        crypto.ProTxHash

	challenges map[types.NodeID]dashproto.ValidatorChallenge
	timers     map[types.NodeID]*time.Timer

	eventBus   *eventbus.EventBus // used for validator set updates
	validators *types.ValidatorSet
}

// NewReactor creates new reactor that will execute proof-of-possession protocol against peer validators.
// Implements service.Service
func NewReactor(
	logger log.Logger,
	peerEvents p2p.PeerEventSubscriber,
	eventBus *eventbus.EventBus,
	chCreator p2p.ChannelCreator,
	myNodeID types.NodeID,
	myProTxHash crypto.ProTxHash,
	consensusPrivKey crypto.PrivKey,
	// p2pPrivKey crypto.PrivKey,
) *Reactor {
	r := &Reactor{
		logger:           logger,
		eventBus:         eventBus,
		peerEvents:       peerEvents,
		chCreator:        chCreator,
		consensusPrivKey: consensusPrivKey,

		proTxHash: myProTxHash,
		nodeID:    myNodeID,

		challenges: map[types.NodeID]dashproto.ValidatorChallenge{},
		timers:     map[types.NodeID]*time.Timer{},
		validators: types.NewEmptyValidatorSet(),
	}
	r.BaseService = *service.NewBaseService(logger, "SecurityReactor", r)

	return r
}

// OnStart starts separate go routines for each p2p Channel and listens for
// envelopes on each. In addition, it also listens for peer updates and handles
// messages on that p2p channel accordingly. The caller must be sure to execute
// OnStop to ensure the outbound p2p Channels are closed.
func (r *Reactor) OnStart(ctx context.Context) error {
	var err error

	channels := getChannelDescriptors()
	r.controlChannel, err = r.chCreator(ctx, channels[DashControlChannel])
	if err != nil {
		return fmt.Errorf("cannot create Dash control channel: %w", err)
	}
	go r.recvControlChannelRoutine(ctx)

	peerUpdates := r.peerEvents(ctx)
	go r.peerUpdatesRoutine(ctx, peerUpdates)

	valUpdateSub, err := r.valUpdateSubscribe()
	if err != nil {
		return fmt.Errorf("cannot subscribe to validator updates: %w", err)
	}
	go r.valUpdatesRoutine(ctx, valUpdateSub)

	return nil
}

func (r *Reactor) OnStop() {
}

func (r *Reactor) getValidatorSet() *types.ValidatorSet {
	r.mtx.RLock()
	defer r.mtx.RUnlock()
	return r.validators
}

func (r *Reactor) setValidatorSet(vset *types.ValidatorSet) {
	r.mtx.Lock()
	defer r.mtx.Unlock()
	r.validators = vset
}

// peerUpdatesRoutine initiates a blocking process where we listen for and handle PeerUpdate messages.
// If the peer update refers to new validator connection, proof-of-possession protocol is initiated.
func (r *Reactor) peerUpdatesRoutine(ctx context.Context, peerUpdates *p2p.PeerUpdates) {
	for {
		select {
		case <-ctx.Done():
			return
		case peerUpdate := <-peerUpdates.Updates():
			r.logger.Debug("received peer update", "peer", peerUpdate.NodeID, "update", peerUpdate)

			if peerUpdate.Status == p2p.PeerStatusUp && len(peerUpdate.ProTxHash) > 0 {
				if err := r.sendChallenge(ctx, peerUpdate.NodeID); err != nil {
					r.logger.Error("cannot send challenge to peer", "peer", peerUpdate.NodeID, "error", err)
					continue
				}
				if err := r.scheduleTimeout(peerUpdate.NodeID); err != nil {
					r.logger.Error("cannot schedule timeout for peer", "peer", peerUpdate.NodeID, "error", err)
					continue
				}
			}
		}
	}
}

func (r *Reactor) scheduleTimeout(peerID types.NodeID) error {
	// stop timer if it is running; ignoring errors by purpose
	_ = r.unscheduleTimeout(peerID)

	r.mtx.Lock()
	defer r.mtx.Unlock()
	r.timers[peerID] = time.AfterFunc(handshakeTimeout, func() {
		if err := r.punishPeer(peerID, ErrPeerAuthTimeout); err != nil {
			r.logger.Error("cannot punish peer", "peer", peerID, "error", err)
		}
	})

	return nil
}

func (r *Reactor) unscheduleTimeout(peerID types.NodeID) error {
	r.mtx.Lock()
	defer r.mtx.Unlock()

	if timer, ok := r.timers[peerID]; ok {
		timer.Stop()
		r.timers[peerID] = nil
	} else {
		return errors.New("timer not found")
	}

	return nil
}

func (r *Reactor) sendChallenge(ctx context.Context, peerID types.NodeID) error {
	challenge, err := r.newPeerChallenge(peerID)
	if err != nil {
		return err
	}
	if err = challenge.Sign(r.consensusPrivKey); err != nil {
		return fmt.Errorf("cannot sign challenge for peer %s: %w", peerID, err)
	}
	envelope := p2p.Envelope{
		From:      r.nodeID,
		To:        peerID,
		ChannelID: DashControlChannel,
		Message: &dashproto.ControlMessage{
			Sum: &dashproto.ControlMessage_ValidatorChallenge{
				ValidatorChallenge: &challenge,
			},
		},
	}

	return r.controlChannel.Send(ctx, envelope)
}

// recvControlChannelRoutine handles messages received on the control channel
func (r *Reactor) recvControlChannelRoutine(ctx context.Context) {
	iterator := r.controlChannel.Receive(ctx)
	for iterator.Next(ctx) {
		envelope := iterator.Envelope()
		controlMsg, ok := envelope.Message.(*dashproto.ControlMessage)
		if !ok {
			r.logger.Error("invalid message type received in DashControlChannel", "type", fmt.Sprintf("%T", envelope.Message))
			continue
		}

		switch msg := controlMsg.Sum.(type) {
		case *dashproto.ControlMessage_ValidatorChallenge:
			if err := r.processValidatorChallenge(ctx, msg.ValidatorChallenge, envelope.From); err != nil {
				r.logger.Error("cannot respond to peer challenge", "peer", envelope.From, "error", err)
			}

		case *dashproto.ControlMessage_ValidatorChallengeResponse:
			if err := r.processValidatorChallengeResponse(ctx, msg.ValidatorChallengeResponse, envelope.From); err != nil {
				r.logger.Error("cannot process challenge response", "peer", envelope.From, "error", err)
			}

		default:
			r.logger.Error("invalid message type received in DashControlChannel", "type", fmt.Sprintf("%T", msg))
		}
	}
}

// processValidatorChallenge processes validator challenges received on the control channel
func (r *Reactor) processValidatorChallenge(ctx context.Context, challenge *dashproto.ValidatorChallenge, senderID types.NodeID) error {
	_, val := r.getValidatorSet().GetByNodeID(senderID)
	if val == nil || val.PubKey == nil {
		return fmt.Errorf("validator with node ID %s not found", senderID)
	}
	if err := challenge.Validate(senderID, r.nodeID, val.ProTxHash, r.proTxHash, nil); err != nil {
		return fmt.Errorf("challenge validation failed: %w", err)
	}
	if err := challenge.Verify(val.PubKey); err != nil {
		return fmt.Errorf("challenge signature verification failed: %w", err)
	}

	if err := r.respondToChallenge(ctx, challenge, senderID); err != nil {
		return fmt.Errorf("cannot respond to challenge: %w", err)
	}

	return nil
}

// respondToChallenge signs response and sends it to the peer
func (r *Reactor) respondToChallenge(ctx context.Context, challenge *dashproto.ValidatorChallenge, peerID types.NodeID) error {
	response, err := challenge.Response(r.consensusPrivKey)
	if err != nil {
		return fmt.Errorf("cannot create response for a challenge: %w", err)
	}

	err = r.controlChannel.Send(ctx, p2p.Envelope{
		From: r.nodeID,
		To:   peerID,
		Message: &dashproto.ControlMessage{
			Sum: &dashproto.ControlMessage_ValidatorChallengeResponse{
				ValidatorChallengeResponse: &response,
			}},
	})
	if err != nil {
		return fmt.Errorf("cannot send challenge response: %w", err)
	}

	return nil
}

// processValidatorChallengeResponse verifies response and punishes peer on error
func (r *Reactor) processValidatorChallengeResponse(ctx context.Context, response *dashproto.ValidatorChallengeResponse, peerID types.NodeID) error {
	err := r.unscheduleTimeout(peerID)
	if err == nil {
		err = r.checkChallengeResponse(ctx, response, peerID)
	}
	if err != nil {
		if err2 := r.punishPeer(peerID, err); err != nil {
			return err2
		}
		return err
	}

	r.logger.Info("remote validator authenticated successfully", "peer", peerID)
	return nil
}

// checkChallengeResponse checks if received response matches the challenge
func (r *Reactor) checkChallengeResponse(ctx context.Context, response *dashproto.ValidatorChallengeResponse, peerID types.NodeID) error {
	if err := response.Validate(); err != nil {
		return err
	}

	challenge, err := r.popPeerChallenge(peerID)
	if err != nil {
		return fmt.Errorf("challenge for peer %s not found", peerID)
	}

	_, val := r.getValidatorSet().GetByNodeID(peerID)
	if val == nil || val.PubKey == nil {
		return fmt.Errorf("validator key for peer %s not found", peerID)
	}

	if err := response.Verify(challenge, val.PubKey); err != nil {
		return fmt.Errorf("cannot verify challenge response sig for peer %s: %w", peerID, err)
	}

	return nil
}

func (r *Reactor) punishPeer(nodeID types.NodeID, reason error) error {
	err := r.controlChannel.SendError(context.Background(), p2p.PeerError{
		NodeID: nodeID,
		Err:    reason,
	})
	if err != nil {
		return fmt.Errorf("cannot punish peer %s with reason %s: %w", nodeID, reason, err)
	}

	return nil
}

// newPeerChallenge generates and saves a new challenge for a peer.
// Challenge consists of:
// sha256(our proTxHash + peer proTxHash + our nodeID + peer nodeID + some random bytes + current time)

func (r *Reactor) newPeerChallenge(peerNodeID types.NodeID) (dashproto.ValidatorChallenge, error) {
	_, val := r.getValidatorSet().GetByNodeID(peerNodeID)
	if val == nil {
		return dashproto.ValidatorChallenge{}, fmt.Errorf("validator with node id %s not found", peerNodeID)
	}

	challenge := dashproto.NewValidatorChallenge(r.nodeID, peerNodeID, r.proTxHash, val.ProTxHash)

	r.mtx.Lock()
	r.challenges[peerNodeID] = challenge
	r.mtx.Unlock()

	return challenge, nil
}

// popPeerChallenge reads peer challenge for a peer and resets it
func (r *Reactor) popPeerChallenge(peerID types.NodeID) (dashproto.ValidatorChallenge, error) {
	r.mtx.Lock()
	defer r.mtx.Unlock()

	challenge, ok := r.challenges[peerID]
	if !ok {
		return dashproto.ValidatorChallenge{}, fmt.Errorf("challenge for peer %s not found", peerID)
	}
	if challenge.RecipientNodeId != string(peerID) {
		return dashproto.ValidatorChallenge{}, fmt.Errorf("challenge peer id mismatch - got: %s, expected: %s", challenge.RecipientProtxhash, peerID)
	}

	delete(r.challenges, peerID)
	return challenge, nil
}

// subscribe subscribes to event bus to receive validator update messages
func (r *Reactor) valUpdateSubscribe() (eventbus.Subscription, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	updatesSub, err := r.eventBus.SubscribeWithArgs(
		ctx,
		tmpubsub.SubscribeArgs{
			ClientID: proofOfPosessionName,
			Query:    types.EventQueryValidatorSetUpdates,
			Limit:    defaultEventBusCapacity,
		},
	)
	if err != nil {
		return nil, err
	}

	return updatesSub, nil
}

// valUpdatesRoutine ensures that active validator set is up to date
func (r *Reactor) valUpdatesRoutine(ctx context.Context, validatorUpdatesSub eventbus.Subscription) {
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			r.logger.Debug("validator updates ctx is done", "error", ctx.Err())
			return
		default:
		}

		msg, err := validatorUpdatesSub.Next(subCtx)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				r.logger.Error("error when reading validator update", "error", err)
				return
			}
		}

		event, ok := msg.Data().(types.EventDataValidatorSetUpdate)
		if !ok {
			r.logger.Error("invalid type of validator set update message", "type", fmt.Sprintf("%T", event))
			continue
		}
		// r.logger.Debug("processing validators update", "event", event)

		r.setValidatorSet(types.NewValidatorSet(
			event.ValidatorSetUpdates,
			event.ThresholdPublicKey,
			event.QuorumType,
			event.QuorumHash,
			true,
		))

		// r.logger.Debug("validator updates processed successfully", "event", event)
	}
}
