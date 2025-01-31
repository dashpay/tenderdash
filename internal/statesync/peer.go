package statesync

import (
	"context"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/client"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/store"
	"github.com/dashpay/tenderdash/types"
)

type (
	// HandlerFunc is peer update event handler
	HandlerFunc func(ctx context.Context, update p2p.PeerUpdate) error
	// PeerManager is a manager for peers
	PeerManager struct {
		logger    log.Logger
		client    client.SnapshotClient
		peerStore store.Store[types.NodeID, PeerData]
		peerSubs  *PeerSubscriber
	}
	// PeerSubscriber is a subscriber for peer events
	PeerSubscriber struct {
		logger    log.Logger
		sub       p2p.PeerEventSubscriber
		handles   map[p2p.PeerStatus]HandlerFunc
		stopCh    chan struct{}
		stoppedCh chan struct{}
	}
	// PeerStatus is a status of a peer
	PeerStatus int
	// PeerData is a data of a peer
	PeerData struct {
		Snapshots []Snapshot
		Status    PeerStatus
	}
	// Snapshot is a snapshot of a peer
	Snapshot struct {
		Height int64
	}
)

// List of peer statuses
const (
	PeerNotReady PeerStatus = iota
	PeerReady
)

// NewPeerSubscriber creates a new peer subscriber
func NewPeerSubscriber(logger log.Logger, sub p2p.PeerEventSubscriber) *PeerSubscriber {
	return &PeerSubscriber{
		logger:    logger,
		sub:       sub,
		handles:   make(map[p2p.PeerStatus]HandlerFunc),
		stopCh:    make(chan struct{}),
		stoppedCh: make(chan struct{}),
	}
}

// On adds a handler for a peer update event
func (p *PeerSubscriber) On(eventName p2p.PeerStatus, handler HandlerFunc) {
	p.handles[eventName] = handler
}

// Start starts the peer subscriber
func (p *PeerSubscriber) Start(ctx context.Context) {
	peerUpdates := p.sub(ctx, "statesync")
	defer close(p.stoppedCh)
	for {
		select {
		case <-ctx.Done():
			return
		case peerUpdate := <-peerUpdates.Updates():
			err := p.execute(ctx, peerUpdate)
			if err != nil {
				p.logger.Error("failed to execute peer update event handler ", "err", err.Error())
			}
		case <-p.stopCh:
			return
		}
	}
}

// Stop stops the peer subscriber
func (p *PeerSubscriber) Stop(ctx context.Context) {
	close(p.stopCh)
	select {
	case <-ctx.Done():
		return
	case <-p.stoppedCh:
		return
	}
}

// processPeerUpdate processes a PeerUpdate, returning an error upon failing to
// handle the PeerUpdate or if a panic is recovered.
func (p *PeerSubscriber) execute(ctx context.Context, peerUpdate p2p.PeerUpdate) error {
	p.logger.Trace("received peer update", "peer", peerUpdate.NodeID, "status", peerUpdate.Status)
	handler, ok := p.handles[peerUpdate.Status]
	if !ok {
		// TODO: return error or write a log
		return nil // ignore
	}
	err := handler(ctx, peerUpdate)
	if err != nil {
		return err
	}
	p.logger.Trace("processed peer update", "peer", peerUpdate.NodeID, "status", peerUpdate.Status)
	return nil
}

// NewPeerManager creates a new peer manager
func NewPeerManager(
	logger log.Logger,
	client client.SnapshotClient,
	peerStore store.Store[types.NodeID, PeerData],
	peerSubs *PeerSubscriber,
) *PeerManager {
	return &PeerManager{
		logger:    logger,
		client:    client,
		peerStore: peerStore,
		peerSubs:  peerSubs,
	}
}

// Start starts the peer manager and its peer update listeners
func (p *PeerManager) Start(ctx context.Context) {
	p.peerSubs.On(p2p.PeerStatusUp, func(ctx context.Context, update p2p.PeerUpdate) error {
		p.peerStore.Put(update.NodeID, PeerData{Status: PeerNotReady})
		err := p.client.GetSnapshots(ctx, update.NodeID)
		if err != nil {
			p.logger.Error("failed to get snapshots promise", "err", err)
			return err
		}
		return nil
	})
	p.peerSubs.On(p2p.PeerStatusDown, func(_ctx context.Context, update p2p.PeerUpdate) error {
		p.peerStore.Delete(update.NodeID)
		return nil
	})
	p.peerSubs.Start(ctx)
}

// Stop stops the peer manager and its peer update listeners
func (p *PeerManager) Stop(ctx context.Context) {
	p.peerSubs.Stop(ctx)
}

// NewPeerStore returns a new in-memory peer store
func NewPeerStore() *store.InMemStore[types.NodeID, PeerData] {
	return store.NewInMemStore[types.NodeID, PeerData]()
}
