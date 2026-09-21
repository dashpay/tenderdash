package statesync

import (
	"context"
	"testing"
	"time"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	coremocks "github.com/dashpay/tenderdash/dash/core/mocks"
	"github.com/dashpay/tenderdash/internal/p2p"
	p2pmocks "github.com/dashpay/tenderdash/internal/p2p/mocks"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/light"
	"github.com/dashpay/tenderdash/light/provider"
	lightdb "github.com/dashpay/tenderdash/light/store/db"
	ssproto "github.com/dashpay/tenderdash/proto/tendermint/statesync"
	"github.com/dashpay/tenderdash/types"
)

func TestParamsResponseCorrelation(t *testing.T) {
	peerA := types.NodeID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	peerB := types.NodeID("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	for _, tc := range []struct {
		name           string
		peer           types.NodeID
		height, connID uint64
	}{
		{"wrong peer", peerB, 100, 7},
		{"wrong height", peerA, 999, 7},
		{"wrong peer and height", peerB, 999, 7},
		{"old connection", peerA, 100, 6},
		{"missing connection", peerA, 100, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			r, sp, outbound := newParamsTestProvider(t, peerA)
			r.paramsRequests.update(p2p.PeerUpdate{NodeID: peerA, Status: p2p.PeerStatusUp, ConnID: 7})
			result := make(chan error, 1)
			go func() { _, err := sp.consensusParams(ctx, 100); result <- err }()
			select {
			case request := <-outbound:
				require.Equal(t, peerA, request.To)
				require.Equal(t, uint64(100), request.Message.(*ssproto.ParamsRequest).Height)
			case <-ctx.Done():
				t.Fatal("request was not sent")
			}
			deliverParams(t, ctx, r, tc.peer, tc.height, tc.connID)
			select {
			case err := <-result:
				t.Fatalf("mismatched response completed request: %v", err)
			case <-time.After(20 * time.Millisecond):
			}
			deliverParams(t, ctx, r, peerA, 100, 7)
			select {
			case err := <-result:
				require.NoError(t, err)
			case <-ctx.Done():
				t.Fatal("valid response did not complete request")
			}
			deliverParams(t, ctx, r, peerA, 100, 7)
		})
	}
}

func TestParamsResponseIdle(t *testing.T) {
	ctx := context.Background()
	r := &Reactor{logger: log.NewNopLogger()}
	start := time.Now()
	deliverParams(t, ctx, r, "peer", 100, 1)
	require.Less(t, time.Since(start), 250*time.Millisecond)
}

func TestParamsRequestsLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &Reactor{logger: log.NewNopLogger(), peers: newPeerList()}
	peer := types.NodeID("peer")
	r.processPeerUpdate(ctx, p2p.PeerUpdate{NodeID: peer, Status: p2p.PeerStatusUp, ConnID: 1})
	old := r.paramsRequests.register(ctx, peer, 100)
	require.NotNil(t, old)
	r.processPeerUpdate(ctx, p2p.PeerUpdate{NodeID: peer, Status: p2p.PeerStatusDown})
	select {
	case <-old.ctx.Done():
	default:
		t.Fatal("disconnect did not release pending request")
	}
	require.Nil(t, r.paramsRequests.register(ctx, peer, 100))
	r.processPeerUpdate(ctx, p2p.PeerUpdate{NodeID: peer, Status: p2p.PeerStatusUp, ConnID: 2})
	current := r.paramsRequests.register(ctx, peer, 100)
	deliverParams(t, ctx, r, peer, 100, 1)
	require.Empty(t, current.response)
	deliverParams(t, ctx, r, peer, 100, 2)
	require.Len(t, current.response, 1)
	deliverParams(t, ctx, r, peer, 100, 2)
	require.Len(t, current.response, 1)
	r.paramsRequests.remove(current)
	require.Empty(t, r.paramsRequests.pending)
	canceled := r.paramsRequests.register(ctx, peer, 101)
	cancel()
	deliverParams(t, context.Background(), r, peer, 101, 2)
	require.Empty(t, canceled.response)
	require.Empty(t, r.paramsRequests.pending)
	require.Nil(t, r.paramsRequests.register(ctx, peer, 101))
}

func TestParamsRequestWitnessesAndCancellation(t *testing.T) {
	peers := []types.NodeID{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	for _, cancelRequest := range []bool{false, true} {
		t.Run(map[bool]string{false: "second witness wins", true: "canceled request"}[cancelRequest], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			r, sp, outbound := newParamsTestProvider(t, peers...)
			for _, peer := range peers {
				r.paramsRequests.update(p2p.PeerUpdate{NodeID: peer, Status: p2p.PeerStatusUp, ConnID: 1})
			}
			result := make(chan error, 1)
			go func() { _, err := sp.consensusParams(ctx, 100); result <- err }()
			for range peers {
				select {
				case <-outbound:
				case <-ctx.Done():
					t.Fatal("witness request missing")
				}
			}
			if cancelRequest {
				cancel()
			} else {
				deliverParams(t, ctx, r, peers[1], 100, 1)
			}
			select {
			case err := <-result:
				if cancelRequest {
					require.ErrorIs(t, err, context.Canceled)
				} else {
					require.NoError(t, err)
				}
			case <-time.After(time.Second):
				t.Fatal("request did not finish")
			}
			r.paramsRequests.mtx.Lock()
			require.Empty(t, r.paramsRequests.pending)
			r.paramsRequests.mtx.Unlock()
			deliverParams(t, context.Background(), r, peers[0], 100, 1)
		})
	}
}

func newParamsTestProvider(t *testing.T, peers ...types.NodeID) (*Reactor, *stateProviderP2P, chan p2p.Envelope) {
	t.Helper()
	primary := NewBlockProvider("cccccccccccccccccccccccccccccccccccccccc", "test", nil)
	witnesses := make([]provider.Provider, len(peers))
	for i, peer := range peers {
		witnesses[i] = NewBlockProvider(peer, "test", nil)
	}
	lc, err := light.NewClientFromTrustedStore("test", primary, witnesses, lightdb.New(dbm.NewMemDB()), coremocks.NewClient(t))
	require.NoError(t, err)
	outbound := make(chan p2p.Envelope, len(peers))
	sp := &stateProviderP2P{lc: lc, paramsSendCh: p2p.NewChannel(ParamsChannel, "params", nil, outbound, nil)}
	r := &Reactor{logger: log.NewNopLogger()}
	r.setStateProvider(sp)
	return r, sp, outbound
}

func deliverParams(t *testing.T, ctx context.Context, r *Reactor, peer types.NodeID, height, connID uint64) {
	t.Helper()
	require.NoError(t, r.handleMessage(ctx, &p2p.Envelope{ChannelID: ParamsChannel, From: peer, ConnID: connID,
		Message: &ssproto.ParamsResponse{Height: height, ConsensusParams: types.DefaultConsensusParams().ToProto()}}, nil))
}

func TestParamsRequestInvalidHeight(t *testing.T) {
	for _, height := range []int64{-1, 0} {
		_, err := (&stateProviderP2P{}).consensusParams(context.Background(), height)
		require.Error(t, err)
	}
}

func TestParamsRequestCanceledSend(t *testing.T) {
	peer := types.NodeID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	r, sp, _ := newParamsTestProvider(t, peer)
	r.paramsRequests.update(p2p.PeerUpdate{NodeID: peer, Status: p2p.PeerStatusUp, ConnID: 1})
	sp.paramsSendCh = p2p.NewChannel(ParamsChannel, "params", nil, make(chan p2p.Envelope), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := sp.consensusParams(ctx, 100); result <- err }()
	require.Eventually(t, func() bool {
		r.paramsRequests.mtx.Lock()
		defer r.paramsRequests.mtx.Unlock()
		return len(r.paramsRequests.pending) == 1
	}, time.Second, time.Millisecond)
	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("blocked send did not cancel")
	}
	r.paramsRequests.mtx.Lock()
	defer r.paramsRequests.mtx.Unlock()
	require.Empty(t, r.paramsRequests.pending)
}

func TestParamsRequestDisconnectCancelsSend(t *testing.T) {
	peer := types.NodeID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	r, sp, _ := newParamsTestProvider(t, peer)
	r.paramsRequests.update(p2p.PeerUpdate{NodeID: peer, Status: p2p.PeerStatusUp, ConnID: 1})
	sending := make(chan struct{})
	stopped := make(chan struct{})
	channel := p2pmocks.NewChannel(t)
	channel.On("Send", mock.Anything, mock.Anything).Return(func(ctx context.Context, _ p2p.Envelope) error {
		close(sending)
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	}).Once()
	sp.paramsSendCh = channel
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := sp.consensusParams(ctx, 100); result <- err }()
	select {
	case <-sending:
	case <-time.After(time.Second):
		t.Fatal("send not started")
	}
	deliverParams(t, ctx, r, peer, 100, 1)
	r.paramsRequests.update(p2p.PeerUpdate{NodeID: peer, Status: p2p.PeerStatusDown})
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not cancel blocked send")
	}
	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("request did not stop")
	}
}
