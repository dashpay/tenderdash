package statesync

import (
	"context"
	"testing"
	"time"

	"github.com/tendermint/tendermint/internal/p2p"
	"github.com/tendermint/tendermint/internal/p2p/client/mocks"
	peerMocks "github.com/tendermint/tendermint/internal/peer/mocks"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/types"
)

func TestPeerSubscriberBasic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	inCh := make(chan p2p.PeerUpdate)
	peerUpdates := p2p.NewPeerUpdates(inCh, 0, "test")
	events := []p2p.PeerUpdate{
		{NodeID: "test", Status: p2p.PeerStatusUp},
		{NodeID: "test", Status: p2p.PeerStatusDown},
	}
	p2pSub := func(context.Context, string) *p2p.PeerUpdates {
		return peerUpdates
	}
	peerSub := NewPeerSubscriber(log.NewNopLogger(), p2pSub)
	outCh := make(chan struct{})
	peerSub.On(p2p.PeerStatusUp, func(ctx context.Context, update p2p.PeerUpdate) error {
		outCh <- struct{}{}
		return nil
	})
	peerSub.On(p2p.PeerStatusDown, func(ctx context.Context, update p2p.PeerUpdate) error {
		outCh <- struct{}{}
		return nil
	})
	go func() {
		peerSub.Start(ctx)
	}()
	go func() {
		for _, event := range events {
			inCh <- event
		}
	}()
	for i := 0; i < len(events); i++ {
		<-outCh
	}
	peerSub.Stop(ctx)
}

func TestPeerManagerBasic(t *testing.T) {
	ctx := context.Background()
	peerID := types.NodeID("testID")
	logger := log.NewNopLogger()
	fakeClient := mocks.NewSnapshotClient(t)
	fakeClient.
		On("GetSnapshots", ctx, peerID).
		Once().
		Return(nil)
	peerUpdateCh := make(chan p2p.PeerUpdate)
	peerSubs := NewPeerSubscriber(logger, func(context.Context, string) *p2p.PeerUpdates {
		return p2p.NewPeerUpdates(peerUpdateCh, 0, "test")
	})
	peerStore := peerMocks.NewStore[PeerData](t)
	peerStore.
		On("Put", peerID, PeerData{Status: PeerNotReady}).
		Once().
		Return(nil)
	peerStore.
		On("Remove", peerID).
		Once().
		Return(nil)
	manager := NewPeerManager(logger, fakeClient, peerStore, peerSubs)
	go manager.Start(ctx)
	peerUpdateCh <- p2p.PeerUpdate{NodeID: peerID, Status: p2p.PeerStatusUp}
	peerUpdateCh <- p2p.PeerUpdate{NodeID: peerID, Status: p2p.PeerStatusDown}
	manager.Stop(ctx)
}
