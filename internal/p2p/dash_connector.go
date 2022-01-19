package p2p

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/types"
)

const (
	DNSLookupTimeout = 1 * time.Second
)

type errPeerNotFound error

// This file contains interface between dash/quorum and p2p connectivity subsystem

// DashDialer defines a service that can be used to establish and manage peer connections
type DashDialer interface {
	// SetPersistentPeer(peerID types.NodeID, persistent bool)
	ConnectAsync(NodeAddress) error
	IsDialingOrConnected(types.NodeID) bool
	DisconnectAsync(types.NodeID) error

	Resolve(types.ValidatorAddress) (NodeAddress, error)
}

type routerPeerManager struct {
	peerManager *PeerManager
	logger      log.Logger
}

func NewDashConnectionManager(peerManager *PeerManager, logger log.Logger) DashDialer {
	return &routerPeerManager{
		peerManager: peerManager,
		logger:      logger,
	}
}

// // // It was Peers().Get()
// // func (cm *routerPeerManager) GetPeer(nodeID types.NodeID) IPeerSet {
// // 	return cm.peerManager.store.peers[nodeID].AddressInfo
// // }
// func (cm *routerPeerManager) SetPersistentPeer(peerID types.NodeID, persistent bool) {

// 	cm.peerManager.mtx.Lock()
// 	defer cm.peerManager.mtx.Unlock()
// 	if persistent {
// 		cm.peerManager.options.persistentPeers[peerID] = true
// 	} else {
// 		delete(cm.peerManager.options.persistentPeers, peerID)
// 	}

// }

// TODO rename to Connect()
func (cm *routerPeerManager) ConnectAsync(addr NodeAddress) error {
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
func (cm *routerPeerManager) setPeerScore(nodeID types.NodeID, newScore PeerScore) error {
	// peerManager.store assumes that peerManager is managing it
	cm.peerManager.mtx.Lock()
	defer cm.peerManager.mtx.Unlock()

	peer, ok := cm.peerManager.store.Get(nodeID)
	if !ok {
		return errPeerNotFound(fmt.Errorf("peer with id %s not found", nodeID))
	}
	peer.MutableScore = int64(newScore)
	if err := cm.peerManager.store.Set(cm.peerManager.configurePeer(peer)); err != nil {
		return err
	}
	return nil
}

func (cm *routerPeerManager) IsDialingOrConnected(id types.NodeID) bool {
	return cm.peerManager.dialing[id] || cm.peerManager.connected[id]
}

func (cm *routerPeerManager) DisconnectAsync(id types.NodeID) error {
	if err := cm.setPeerScore(id, 0); err != nil {
		return err
	}

	cm.peerManager.mtx.Lock()
	cm.peerManager.evict[id] = true
	cm.peerManager.mtx.Unlock()

	cm.peerManager.evictWaker.Wake()
	return nil
}

// Resolve implements dashquorum.NodeIDResolver
func (cm *routerPeerManager) Resolve(va types.ValidatorAddress) (nodeAddress NodeAddress, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), DNSLookupTimeout)
	defer cancel()

	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", va.Hostname)
	if err != nil {
		return nodeAddress, err
	}
	for _, ip := range ips {
		nodeAddress, err = cm.LookupIPPort(ctx, ip, va.Port)
		// First match is returned
		if err == nil {
			return nodeAddress, nil
		}
	}
	return nodeAddress, err
}

func (cm *routerPeerManager) LookupIPPort(ctx context.Context, ip net.IP, port uint16) (NodeAddress, error) {
	for nodeID, peer := range cm.peerManager.store.peers {
		for addr := range peer.AddressInfo {
			if endpoints, err := addr.Resolve(ctx); err != nil {
				for _, item := range endpoints {
					if item.IP.Equal(ip) && item.Port == port {
						return item.NodeAddress(nodeID), nil
					}
				}
			}
		}
	}

	return NodeAddress{}, errPeerNotFound(fmt.Errorf("peer %s:%dd not found in the address book", ip, port))
}
