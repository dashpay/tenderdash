package statesync

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	clientmocks "github.com/dashpay/tenderdash/abci/client/mocks"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/internal/p2p"
	p2pmocks "github.com/dashpay/tenderdash/internal/p2p/mocks"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/statesync/mocks"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/log"
	ssproto "github.com/dashpay/tenderdash/proto/tendermint/statesync"
	"github.com/dashpay/tenderdash/types"
)

func TestSnapshotDiscoveryLegalMessageAndDuplicateAllowance(t *testing.T) {
	p := newSnapshotPool()
	r := &Reactor{logger: log.NewNopLogger(), syncer: &syncer{logger: log.NewNopLogger(), snapshots: p, metrics: NopMetrics()}}
	msg := &ssproto.SnapshotsResponse{Height: 1, Version: 1, Hash: []byte{1}, Metadata: make([]byte, 3_999_900)}
	require.LessOrEqual(t, msg.Size(), 4_000_000)
	require.True(t, p.RequestPeer("peer"))
	for i := 0; i < recentSnapshots; i++ {
		require.NoError(t, r.handleSnapshotMessage(context.Background(), &p2p.Envelope{From: "peer", Message: msg}, nil))
	}
	require.Len(t, p.snapshots, 1)
	require.Equal(t, len(msg.Hash)+len(msg.Metadata), p.retainedBytes)
	msg.Height = 2
	require.NoError(t, r.handleSnapshotMessage(context.Background(), &p2p.Envelope{From: "peer", Message: msg}, nil))
	require.Len(t, p.snapshots, 1, "duplicates exhaust the response allowance even below the stored count quota")
	p.DiscoveryBatch([]types.NodeID{"peer"})
	require.NoError(t, r.handleSnapshotMessage(context.Background(), &p2p.Envelope{From: "peer", Message: msg}, nil))
	require.Len(t, p.snapshots, 2, "a new request grants a new bounded response opportunity")
}

func TestSnapshotDiscoveryFailedSendRevokesAllowance(t *testing.T) {
	ch := p2pmocks.NewChannel(t)
	ch.On("Send", mock.Anything, mock.Anything).Return(context.Canceled)
	s := &syncer{logger: log.NewNopLogger(), snapshots: newSnapshotPool(), snapshotCh: ch}
	require.ErrorIs(t, s.AddPeer(context.Background(), "joined"), context.Canceled)
	require.False(t, s.snapshots.AcceptResponse("joined"))
	require.ErrorIs(t, s.RequestSnapshots(context.Background(), []types.NodeID{"a", "b"}), context.Canceled)
	require.False(t, s.snapshots.AcceptResponse("a"))
	require.False(t, s.snapshots.AcceptResponse("b"))
}

func TestSnapshotDiscoverySingleRetryCompletesFrozenSweep(t *testing.T) {
	for _, respond := range []bool{false, true} {
		t.Run(fmt.Sprint(respond), func(t *testing.T) {
			peers := make([]types.NodeID, maxDiscoveryPeers+1)
			for i := range peers {
				peers[i] = types.NodeID(fmt.Sprint(i))
			}
			lastPeer := peers[len(peers)-1]
			provider := mocks.NewStateProvider(t)
			if respond {
				provider.On("AppHash", mock.Anything, uint64(1)).Return(tmbytes.HexBytes(nil), errors.New("untrusted snapshot")).Once()
			}
			ch := p2pmocks.NewChannel(t)
			s := &syncer{logger: log.NewNopLogger(), snapshots: newSnapshotPool(), snapshotCh: ch, stateProvider: provider, metrics: NopMetrics(), tempDir: t.TempDir()}
			r := &Reactor{logger: log.NewNopLogger(), syncer: s}
			requests := 0
			ch.On("Send", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				envelope := args.Get(1).(p2p.Envelope)
				requests++
				if respond && envelope.To == lastPeer {
					require.NoError(t, r.handleSnapshotMessage(context.Background(), &p2p.Envelope{From: lastPeer, Message: &ssproto.SnapshotsResponse{
						Height: 1, Version: 1, Hash: []byte{1}, Metadata: []byte{1},
					}}, nil))
				}
			}).Return(nil)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			_, _, err := s.SyncAny(ctx, minimumDiscoveryTime, 1, func() error {
				err := s.RequestSnapshots(ctx, peers)
				peers = append(peers, types.NodeID(fmt.Sprintf("churn-%d", len(peers))))
				return err
			})
			require.ErrorIs(t, err, errNoSnapshots)
			require.Equal(t, maxDiscoveryPeers+1, requests, "one retry finishes the original sweep despite new peer arrivals")
			require.False(t, s.snapshots.DiscoveryPending())
			require.Nil(t, s.snapshots.active)
			require.Zero(t, s.snapshots.retainedBytes)
		})
	}
}

func TestSnapshotDiscoveryCancellationAndQueueErrorReleasePin(t *testing.T) {
	t.Run("canceled discovery", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		s := &syncer{logger: log.NewNopLogger(), snapshots: newSnapshotPool()}
		_, _, err := s.SyncAny(ctx, minimumDiscoveryTime, 1, func() error { return nil })
		require.ErrorIs(t, err, context.Canceled)
		require.Nil(t, s.snapshots.active)
	})
	t.Run("queue creation failure", func(t *testing.T) {
		p := newSnapshotPool()
		_, err := p.Add("peer", &snapshot{Height: 1, Version: 1, Hash: []byte{1}})
		require.NoError(t, err)
		s := &syncer{logger: log.NewNopLogger(), snapshots: p, tempDir: t.TempDir() + "/missing"}
		_, _, err = s.SyncAny(context.Background(), 0, 1, func() error { return nil })
		require.Error(t, err)
		require.Nil(t, p.active)
		require.Nil(t, s.processingSnapshot)
		p.RemovePeer("peer")
		require.Zero(t, p.retainedBytes)
	})
}

func TestSnapshotDiscoveryRetainsActiveUntilFetchersExit(t *testing.T) {
	p := newSnapshotPool()
	_, err := p.Add("peer", &snapshot{Height: 1, Version: 1, Hash: []byte{1}, Metadata: []byte{2}})
	require.NoError(t, err)
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	ch := p2pmocks.NewChannel(t)
	ch.On("Send", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		close(entered)
		<-args.Get(0).(context.Context).Done()
		close(canceled)
		<-release
	}).Return(nil).Once()
	provider := mocks.NewStateProvider(t)
	provider.On("AppHash", mock.Anything, uint64(1)).Return(tmbytes.HexBytes{1}, nil)
	provider.On("State", mock.Anything, uint64(1)).Run(func(_ mock.Arguments) { <-entered }).Return(sm.State{}, errors.New("untrusted state"))
	conn := clientmocks.NewClient(t)
	conn.On("OfferSnapshot", mock.Anything, mock.Anything).Return(&abci.ResponseOfferSnapshot{Result: abci.ResponseOfferSnapshot_ACCEPT}, nil)
	s := &syncer{logger: log.NewNopLogger(), snapshots: p, stateProvider: provider, conn: conn, chunkCh: ch, fetchers: 1, retryTimeout: time.Second, tempDir: t.TempDir()}
	done := make(chan error, 1)
	go func() { _, _, err := s.SyncAny(context.Background(), 0, 1, func() error { return nil }); done <- err }()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("fetch request was not canceled")
	}
	var returned bool
	select {
	case <-done:
		returned = true
		t.Error("SyncAny returned before the canceled fetcher released its snapshot")
	case <-time.After(20 * time.Millisecond):
		p.Lock()
		active := p.active
		retained := p.retainedBytes
		p.Unlock()
		require.NotNil(t, active)
		require.Equal(t, 2, retained)
	}
	close(release)
	if !returned {
		select {
		case err := <-done:
			require.ErrorIs(t, err, errNoSnapshots)
		case <-time.After(time.Second):
			t.Fatal("SyncAny did not finish after fetcher exit")
		}
	}
	require.Nil(t, p.active)
	require.Zero(t, p.retainedBytes)
}
