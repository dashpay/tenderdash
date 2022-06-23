package pop

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	tmcrypto "github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/dash/quorum"
	"github.com/tendermint/tendermint/internal/eventbus"
	"github.com/tendermint/tendermint/internal/p2p"
	tmpubsub "github.com/tendermint/tendermint/internal/pubsub"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/libs/service"
	"github.com/tendermint/tendermint/privval"
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

	chCreator      p2p.ChannelCreator // channel creator used to create DashControlChannel
	controlChannel *p2p.Channel
	peerEvents     p2p.PeerEventSubscriber // peerEvents is a subscription for peer up/down notifications
	eventBus       *eventbus.EventBus      // used for validator set updates
	peerManager    *p2p.PeerManager

	nodeID        types.NodeID
	privValidator types.PrivValidator
	quorumHash    tmcrypto.QuorumHash
	validators    *types.ValidatorSet

	challenges         map[types.NodeID]dashproto.ValidatorChallenge
	timers             map[types.NodeID]*time.Timer
	authenticatedPeers map[types.NodeID]tmcrypto.ProTxHash
}

// NewReactor creates new reactor that will execute proof-of-possession protocol against peer validators.
// Implements service.Service
func NewReactor(
	logger log.Logger,
	peerEvents p2p.PeerEventSubscriber,
	eventBus *eventbus.EventBus,
	chCreator p2p.ChannelCreator,
	myNodeID types.NodeID,
	privval types.PrivValidator,
	validatorSet *types.ValidatorSet,
	peerManager *p2p.PeerManager,
) (*Reactor, error) {
	if peerManager == nil {
		return nil, fmt.Errorf("peer manager is nil")
	}
	r := &Reactor{
		logger: logger,

		chCreator:   chCreator,
		peerEvents:  peerEvents,
		eventBus:    eventBus,
		peerManager: peerManager,

		nodeID:        myNodeID,
		privValidator: privval,
		validators:    validatorSet,
		quorumHash:    validatorSet.QuorumHash,

		challenges: map[types.NodeID]dashproto.ValidatorChallenge{},
		timers:     map[types.NodeID]*time.Timer{},
	}
	r.BaseService = *service.NewBaseService(logger, "SecurityReactor", r)

	return r, nil
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
	r.quorumHash = vset.QuorumHash
}

// func (r *Reactor) addValidator(ctx context.Context, id types.NodeID, pubkey tmcrypto.PubKey, proTxHash tmcrypto.ProTxHash) {
// 	validator := types.NewValidatorDefaultVotingPower(pubkey, proTxHash)
// 	validator.NodeAddress.NodeID = id

// 	r.mtx.Lock()
// 	defer r.mtx.Unlock()

// 	r.validators[id] = validator
// }

// func (r *Reactor) getValidator(id types.NodeID) (*types.Validator, bool) {
// 	r.mtx.RLock()
// 	defer r.mtx.RUnlock()

// 	validator, ok := r.validators[id]
// 	return validator, ok
// }

// peerUpdatesRoutine initiates a blocking process where we listen for and handle PeerUpdate messages.
// If the peer update refers to new validator connection, proof-of-possession protocol is initiated.
func (r *Reactor) peerUpdatesRoutine(ctx context.Context, peerUpdates *p2p.PeerUpdates) {
	for {
		select {
		case <-ctx.Done():
			return
		case peerUpdate := <-peerUpdates.Updates():
			r.logger.Debug("received peer update", "peer", peerUpdate.NodeID, "update", peerUpdate)

			if peerUpdate.Status == p2p.PeerStatusUp {
				if err := r.peerUp(ctx, peerUpdate.NodeID, peerUpdate.ProTxHash); err != nil {
					r.logger.Error("cannot execute peer update", "peer", peerUpdate.NodeID, "peer_protxhash", peerUpdate.ProTxHash, "error", err)
				}
			}
		}
	}
}
func (r *Reactor) peerUp(ctx context.Context, peerID types.NodeID, peerProTxHash tmcrypto.ProTxHash) error {
	if r.needsPoP(ctx, peerID, peerProTxHash) {
		r.logger.Debug("executing proof-of-possession", "peer", peerID, "peer_protxhash", peerProTxHash)
		if err := r.sendChallenge(ctx, peerID, peerProTxHash); err != nil {
			return fmt.Errorf("cannot send challenge to peer: %w", err)
		}
		if err := r.scheduleTimeout(peerID); err != nil {
			return fmt.Errorf("cannot schedule timeout for peer: %w", err)
		}
	}

	r.logger.Debug("proof-of-possession not needed", "peer", peerID, "peer_protxhash", peerProTxHash)
	return nil
}

func (r *Reactor) scheduleTimeout(peerID types.NodeID) error {
	// stop timer if it is running; ignoring errors by purpose
	_ = r.unscheduleTimeout(peerID)

	r.mtx.Lock()
	defer r.mtx.Unlock()
	r.timers[peerID] = time.AfterFunc(handshakeTimeout, func() {
		r.logger.Error("validator challenge timed out", "peer", peerID)
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
		delete(r.timers, peerID)
	} else {
		return errors.New("timer not found")
	}

	return nil
}

func (r *Reactor) getConsensusPrivKey(ctx context.Context) (tmcrypto.PrivKey, error) {
	quorumHash := r.getQuorumHash(ctx)
	privKey, err := r.privValidator.GetPrivateKey(ctx, quorumHash)
	if err != nil {
		return nil, fmt.Errorf("cannot get consensus privkey for quorum hash %s: %w", quorumHash.String(), err)
	}
	if privKey == nil {
		return nil, fmt.Errorf("nil consensus private key for quorum hash %s, privval: %T %+v", quorumHash.String(), r.privValidator, r.privValidator)
	}

	return privKey, nil
}

func (r *Reactor) sendChallenge(ctx context.Context, peerID types.NodeID, peerProTxHash tmcrypto.ProTxHash) error {
	challenge, err := r.newPeerChallenge(ctx, peerID, peerProTxHash)
	if err != nil {
		return err
	}

	privKey, err := r.getConsensusPrivKey(ctx)
	if err != nil {
		return err
	}

	if err = challenge.Sign(privKey); err != nil {
		return fmt.Errorf("cannot sign challenge for peer %s: %w", peerID, err)
	}
	pubkey := privval.NewDashConsensusPublicKey(privKey.PubKey(), r.getQuorumHash(ctx), r.getValidatorSet().QuorumType)
	if err = challenge.Verify(pubkey); err != nil {
		return fmt.Errorf("cannot verify just signed challenge: %w", err)
	}

	envelope := p2p.Envelope{
		From:      r.nodeID,
		To:        peerID,
		ChannelID: DashControlChannel,
		Message:   &challenge,
	}

	if err = r.controlChannel.Send(ctx, envelope); err != nil {
		return err
	}
	r.logger.Debug("challenge sent", "peer", peerID, "envelope", envelope)

	return nil
}

// recvControlChannelRoutine handles messages received on the control channel
func (r *Reactor) recvControlChannelRoutine(ctx context.Context) {
	iterator := r.controlChannel.Receive(ctx)
	for iterator.Next(ctx) {
		envelope := iterator.Envelope()
		// controlMsg, ok := envelope.Message.(*dashproto.ControlMessage)
		// if !ok {
		// 	r.logger.Error("invalid message type received in DashControlChannel", "type", fmt.Sprintf("%T", envelope.Message))
		// 	continue
		// }

		switch msg := envelope.Message.(type) {
		case *dashproto.ValidatorChallenge:
			r.logger.Debug("validator challenge received", "peer", envelope.From)
			if err := r.processValidatorChallenge(ctx, msg, envelope.From); err != nil {
				r.logger.Error("cannot respond to peer challenge", "peer", envelope.From, "error", err)
			}

		case *dashproto.ValidatorChallengeResponse:
			r.logger.Debug("validator challenge response received", "peer", envelope.From)
			if err := r.processValidatorChallengeResponse(ctx, msg, envelope.From); err != nil {
				r.logger.Error("cannot process challenge response", "peer", envelope.From, "error", err)
			}

		default:
			r.logger.Error("invalid message type received in DashControlChannel", "type", fmt.Sprintf("%T", msg))
		}
	}
}

func (r *Reactor) getProTxHash(ctx context.Context) (tmcrypto.ProTxHash, error) {
	return r.privValidator.GetProTxHash(ctx)
}

// processValidatorChallenge processes validator challenges received on the control channel
func (r *Reactor) processValidatorChallenge(ctx context.Context, challenge *dashproto.ValidatorChallenge, senderID types.NodeID) error {
	_, val := r.getValidatorSet().GetByNodeID(senderID)
	if val == nil || val.PubKey == nil {
		return fmt.Errorf("validator with node ID %s not found", senderID)
	}

	proTxHash, err := r.getProTxHash(ctx)
	if err != nil {
		return err
	}

	if err := challenge.Validate(senderID, r.nodeID, val.ProTxHash, proTxHash, nil); err != nil {
		r.logger.Debug("invalid challenge", "challenge", challenge, "peer", senderID)
		return fmt.Errorf("challenge validation failed: %w", err)
	}

	pubkey := privval.NewDashConsensusPublicKey(val.PubKey, r.getQuorumHash(ctx), r.getValidatorSet().QuorumType)
	if err := challenge.Verify(pubkey); err != nil {
		r.logger.Debug("challenge signature verification failed", "validator", val, "pubkey", val.PubKey, "error", err)
		return fmt.Errorf("challenge signature verification failed: %w", err)
	}

	if err := r.respondToChallenge(ctx, challenge, senderID); err != nil {
		return fmt.Errorf("cannot respond to challenge: %w", err)
	}

	r.logger.Debug("challenge response sent", "peer", senderID)
	return nil
}

// respondToChallenge signs response and sends it to the peer
func (r *Reactor) respondToChallenge(ctx context.Context, challenge *dashproto.ValidatorChallenge, peerID types.NodeID) error {
	consensusPrivKey, err := r.getConsensusPrivKey(ctx)
	if err != nil {
		return err
	}
	response, err := challenge.Response(consensusPrivKey)
	if err != nil {
		return fmt.Errorf("cannot create response for a challenge: %w", err)
	}

	err = r.controlChannel.Send(ctx, p2p.Envelope{
		From:    r.nodeID,
		To:      peerID,
		Message: &response,
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

	r.logger.Info("peer validator consensus private key verified successfully", "peer", peerID)
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

	pubkey := privval.NewDashConsensusPublicKey(val.PubKey, r.getQuorumHash(ctx), r.getValidatorSet().QuorumType)
	if err := response.Verify(challenge, pubkey); err != nil {
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
// sha256(our proTxHash + peer proTxHash + our nodeID + peer nodeID + some hard-to-predict bytes)
func (r *Reactor) newPeerChallenge(ctx context.Context, peerNodeID types.NodeID, peerProTxHash tmcrypto.ProTxHash) (dashproto.ValidatorChallenge, error) {

	myProTxHash, err := r.getProTxHash(ctx)
	if err != nil {
		return dashproto.ValidatorChallenge{}, err
	}

	challenge := dashproto.NewValidatorChallenge(r.nodeID, peerNodeID, myProTxHash, peerProTxHash)

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

func (r *Reactor) needsPoP(ctx context.Context, peerID types.NodeID, protxhash tmcrypto.ProTxHash) bool {
	if len(protxhash) == 0 {
		return false // not a validator
	}

	if r.peerManager.Status(peerID) == p2p.PeerStatusDown {
		return false // not connected
	}
	valSet := r.getValidatorSet()
	myProTxHash, err := r.getProTxHash(ctx)
	if err != nil || len(myProTxHash) == 0 {
		r.logger.Error("cannot retrieve our proTxHash", "error", err)
		return false // we don't have proTxHash == we assume we are not a validator
	}
	if _, validator := valSet.GetByProTxHash(myProTxHash); validator == nil {
		return false // we are not a validator
	}
	if _, validator := valSet.GetByProTxHash(protxhash); validator == nil {
		return false // not a member of active validator set
	}

	r.mtx.RLock()
	authenticatedProTXHash, ok := r.authenticatedPeers[peerID]
	r.mtx.RUnlock()
	if ok && protxhash.Equal(authenticatedProTXHash) {
		// already authenticated
		return false
	}

	return true
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
			r.logger.Error("invalid type of validator set update message", "type", fmt.Sprintf("%T", msg.Data()))
			continue
		}
		r.logger.Debug("processing validators update", "quorum", event.QuorumHash)

		r.setValidatorSet(types.NewValidatorSet(
			event.ValidatorSetUpdates,
			event.ThresholdPublicKey,
			event.QuorumType,
			event.QuorumHash,
			true,
		))

		for _, validator := range event.ValidatorSetUpdates {
			peerID := validator.NodeAddress.NodeID
			if peerID == "" {
				if err := quorum.ResolveNodeID(&validator.NodeAddress, nil, r.logger); err != nil {
					r.logger.Error("cannot determine node ID for validator, skipping", "address", validator.NodeAddress, "peer_protxhash", validator.ProTxHash)
					continue
				}
			}
			if r.needsPoP(ctx, peerID, validator.ProTxHash) {
				if err := r.peerUp(ctx, peerID, validator.ProTxHash); err != nil {
					r.logger.Error("cannot execute peer update", "peer", peerID, "peer_protxhash", validator.ProTxHash, "error", err)
					continue
				}
			}
		}

		// r.logger.Debug("validator updates processed successfully", "event", event)
	}
}

func (r *Reactor) getQuorumHash(ctx context.Context) tmcrypto.QuorumHash {
	r.mtx.RLock()
	defer r.mtx.RUnlock()

	return r.quorumHash
}
