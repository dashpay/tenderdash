package pex

import (
	"context"
	"strings"
	"testing"
	"time"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

func TestPeerUpdatesContinueWhileCalculatingRequestTime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	selfID := types.NodeID(strings.Repeat("a", 40))
	peerID := types.NodeID(strings.Repeat("b", 40))
	manager, err := p2p.NewPeerManager(ctx, selfID, dbm.NewMemDB(), p2p.PeerManagerOptions{})
	require.NoError(t, err)
	defer func() { require.NoError(t, manager.Close()) }()
	require.NoError(t, manager.Accepted(peerID))
	updates := p2p.NewPeerUpdates(make(chan p2p.PeerUpdate), 1, "pex-test")
	manager.Register(ctx, updates)
	reactor := NewReactor(log.NewNopLogger(), manager, nil, nil)

	readyDone := make(chan struct{})
	go func() {
		defer close(readyDone)
		manager.Ready(ctx, peerID, nil)
	}()
	// Keep Ready in broadcast while request scheduling waits for the manager.
	time.Sleep(50 * time.Millisecond)
	calculated := make(chan struct{})
	go func() {
		defer close(calculated)
		reactor.calculateNextRequestTime(1)
	}()
	time.Sleep(50 * time.Millisecond)
	processed := make(chan struct{})
	go func() {
		defer close(processed)
		reactor.processPeerUpdate(p2p.PeerUpdate{NodeID: peerID, Status: p2p.PeerStatusUp})
	}()
	select {
	case <-processed:
	case <-time.After(time.Second):
		t.Error("PEX peer updates blocked while waiting for the peer manager")
	}
	cancel()
	<-readyDone
	<-calculated
	<-processed
}
