package quorum

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
	tmbytes "github.com/tendermint/tendermint/libs/bytes"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/libs/rand"
	"github.com/tendermint/tendermint/libs/service"
	dashproto "github.com/tendermint/tendermint/proto/tendermint/dash"
	"github.com/tendermint/tendermint/types"
)

const (
	handshakeTimeout = 3 * time.Second
)

var (
	ErrPeerAuthTimeout = errors.New("remote validator authentication failed")
)

// Reactor defines a reactor for the consensus service.
type Reactor struct {
	service.BaseService
	logger log.Logger
	mtx    sync.RWMutex

	// chCreator
	chCreator      p2p.ChannelCreator // channel creator used to create DashControlChannel
	controlChannel *p2p.Channel

	peerEvents p2p.PeerEventSubscriber // peerEvents is a subscription for peer up/down notifications

	nodeID           types.NodeID
	consensusPrivKey crypto.PrivKey
	proTxHash        crypto.ProTxHash

	challenges map[types.NodeID]tmbytes.HexBytes
	timers     map[types.NodeID]*time.Timer

	eventBus   *eventbus.EventBus // used for validator set updates
	validators *types.ValidatorSet
}

// NewReactor returns a reference to a new consensus reactor, which implements
// the service.Service interface. It accepts a logger, consensus state, references
// to relevant p2p Channels and a channel to listen for peer updates on. The
// reactor will close all p2p Channels when stopping.
func NewReactor(
	logger log.Logger,
	peerEvents p2p.PeerEventSubscriber,
	eventBus *eventbus.EventBus,
	chCreator p2p.ChannelCreator,
	myNodeID types.NodeID,
	myProTxHash crypto.ProTxHash,
	consensusPrivKey crypto.PrivKey,
) *Reactor {
	r := &Reactor{
		logger:           logger,
		eventBus:         eventBus,
		peerEvents:       peerEvents,
		chCreator:        chCreator,
		consensusPrivKey: consensusPrivKey,
		proTxHash:        myProTxHash,
		nodeID:           myNodeID,

		challenges: map[types.NodeID]tmbytes.HexBytes{},
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
	var (
		err error
	)

	peerUpdates := r.peerEvents(ctx)

	channels := getChannelDescriptors()

	r.controlChannel, err = r.chCreator(ctx, channels[DashControlChannel])
	if err != nil {
		return fmt.Errorf("cannot create Dash control channel: %w", err)
	}

	valUpdateSub, err := r.valUpdateSubscribe()
	if err != nil {
		return fmt.Errorf("cannot subscribe to validator updates: %w", err)
	}

	go r.recvControlChannelRoutine(ctx)
	go r.peerUpdatesRoutine(ctx, peerUpdates)
	go r.valUpdatesRoutine(ctx, valUpdateSub)

	return nil
}

// OnStop stops the reactor by signaling to all spawned goroutines to exit and
// blocking until they all exit, as well as unsubscribing from events and stopping
// state.
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

// peerUpdatesRoutine initiates a blocking process where we listen for and handle PeerUpdate messages
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
		if err := r.punishPeer(peerID); err != nil {
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
	challenge := r.newPeerChallenge(peerID)
	signature, err := r.consensusPrivKey.SignDigest(challenge)
	if err != nil {
		return fmt.Errorf("cannot sign challenge for peer %s: %w", peerID, err)
	}

	envelope := p2p.Envelope{
		From:      r.nodeID,
		To:        peerID,
		ChannelID: DashControlChannel,
		Message: &dashproto.ControlMessage{
			Sum: &dashproto.ControlMessage_ValidatorChallenge{
				ValidatorChallenge: &dashproto.ValidatorChallenge{
					Challenge: challenge,
					Signature: signature,
				},
			},
		},
	}

	return r.controlChannel.Send(ctx, envelope)
}

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
			if err := r.respondToChallenge(ctx, envelope.From, msg.ValidatorChallenge); err != nil {
				r.logger.Error("cannot respond to peer challenge", "peer", envelope.From, "error", "err")
			}

		case *dashproto.ControlMessage_ValidatorChallengeResponse:
			if err := r.unscheduleTimeout(envelope.From); err != nil {
				r.logger.Error("cannot unschedule timeout for peer", "peer", envelope.From, "error", err)
			}

			if err := r.checkChallengeResponse(ctx, envelope.From, msg.ValidatorChallengeResponse); err != nil {
				r.logger.Error("peer challenge response is invalid", "peer", envelope.From, "envelope", envelope, "error", "err")
				if err := r.punishPeer(envelope.From); err != nil {
					r.logger.Error("cannot punish peer", "peer", envelope.From, "error", "err")
				}
			} else {
				r.logger.Info("remote validator authenticated successfully", "peer", envelope.From)
			}

		default:
			// sth went wrong
			r.logger.Error("invalid message type received in DashControlChannel", "type", fmt.Sprintf("%T", msg))
		}
	}
}

// respondToChallenge signs challenge and sends it to the peer
func (r *Reactor) respondToChallenge(ctx context.Context, peerID types.NodeID, challenge *dashproto.ValidatorChallenge) error {
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

// checkChallengeResponse checks if received response matches the challenge
func (r *Reactor) checkChallengeResponse(
	ctx context.Context,
	peerID types.NodeID,
	response *dashproto.ValidatorChallengeResponse,
) error {
	challenge := r.popPeerChallenge(peerID)
	if len(challenge) == 0 {
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

func (r *Reactor) punishPeer(nodeID types.NodeID) error {
	return r.controlChannel.SendError(context.Background(), p2p.PeerError{
		NodeID: nodeID,
		Err:    ErrPeerAuthTimeout,
	})
}

// newPeerChallenge generates and saves a new challenge for a peer.
// Challenge consists of: sha256(some random bytes + our proTxHash + peer proTxHash + current time)
func (r *Reactor) newPeerChallenge(peer types.NodeID) tmbytes.HexBytes {
	const randBytesLen = 4

	_, val := r.getValidatorSet().GetByNodeID(peer)
	if val == nil {
		r.logger.Error("validator not found", "peer", peer)
		return nil
	}

	currentTime, err := time.Now().MarshalBinary()
	if err != nil {
		r.logger.Error("cannot marshal time", "peer", peer, "error", err)
		return nil
	}

	randBytes := rand.Bytes(randBytesLen)

	challenge := make(tmbytes.HexBytes, len(randBytes)+2*crypto.ProTxHashSize+len(currentTime))
	challenge = append(challenge, randBytes...)
	challenge = append(challenge, r.proTxHash...)
	challenge = append(challenge, val.ProTxHash...)
	challenge = append(challenge, currentTime...)
	challenge = crypto.Checksum(challenge)

	r.mtx.Lock()
	r.challenges[peer] = challenge
	r.mtx.Unlock()

	return challenge
}

// popPeerChallenge reads peer challenge for a peer and resets it
func (r *Reactor) popPeerChallenge(peer types.NodeID) tmbytes.HexBytes {
	r.mtx.Lock()
	defer r.mtx.Unlock()

	challenge := r.challenges[peer]
	delete(r.challenges, peer)

	return challenge
}

// subscribe subscribes to event bus to receive validator update messages
func (r *Reactor) valUpdateSubscribe() (eventbus.Subscription, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	updatesSub, err := r.eventBus.SubscribeWithArgs(
		ctx,
		tmpubsub.SubscribeArgs{
			ClientID: validatorConnExecutorName,
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
