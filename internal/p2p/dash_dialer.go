package p2p

import (
	"context"

	"github.com/rs/zerolog"

	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/types"
)

type ErrPeerNotFound error

// This file contains interface between dash/quorum and p2p connectivity subsystem

// NodeIDResolver determines a node ID based on validator address
type NodeIDResolver interface {
	// Resolve determines real node address, including node ID, based on the provided
	// validator address.
	Resolve(context.Context, types.ValidatorAddress) (NodeAddress, error)
}

// DashDialer defines a service that can be used to establish and manage peer connections
type DashDialer interface {
	// ConnectAsync schedules asynchronous job to establish connection with provided node.
	ConnectAsync(NodeAddress) error
	// IsDialingOrConnected determines whether node with provided node ID is already connected,
	// or there is a pending connection attempt.
	IsDialingOrConnected(types.NodeID) bool
	// DisconnectAsync schedules asynchronous job to disconnect from the provided node.
	DisconnectAsync(types.NodeID) error
}

type routerDashDialer struct {
	peerManager *PeerManager
	logger      log.Logger
}

func NewRouterDashDialer(peerManager *PeerManager, logger log.Logger) DashDialer {
	return &routerDashDialer{
		peerManager: peerManager,
		logger:      logger,
	}
}

// ConnectAsync implements DashDialer
func (cm *routerDashDialer) ConnectAsync(addr NodeAddress) error {
	if err := addr.Validate(); err != nil {
		return err
	}
	if _, err := cm.peerManager.Add(addr); err != nil {
		return err
	}
	if err := cm.setPeerScore(addr.NodeID, PeerScorePersistent); err != nil {
		return err
	}
	cm.peerManager.dialWaker.Wake()
	return nil
}

// setPeerScore changes score for a peer
func (cm *routerDashDialer) setPeerScore(nodeID types.NodeID, newScore PeerScore) error {
	return cm.peerManager.UpdatePeerInfo(nodeID, func(peer peerInfo) peerInfo {
		peer.MutableScore = int64(newScore)
		return cm.peerManager.configurePeer(peer)
	})
}

// IsDialingOrConnected implements DashDialer
func (cm *routerDashDialer) IsDialingOrConnected(nodeID types.NodeID) bool {
	return cm.peerManager.IsDialingOrConnected(nodeID)
}

// DisconnectAsync implements DashDialer
func (cm *routerDashDialer) DisconnectAsync(nodeID types.NodeID) error {
	if err := cm.setPeerScore(nodeID, 0); err != nil {
		return err
	}
	cm.peerManager.EvictPeer(nodeID)
	return nil
}

// MarshalZerologObject implements zerolog.LogObjectMarshaler
func (cm *routerDashDialer) MarshalZerologObject(e *zerolog.Event) {
	e.Str("type", "routerDashDialer")
}
