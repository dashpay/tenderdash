package p2p_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	gogotypes "github.com/gogo/protobuf/types"
	sync "github.com/sasha-s/go-deadlock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	dbm "github.com/tendermint/tm-db"

	"github.com/dashpay/tenderdash/crypto"
	tmsync "github.com/dashpay/tenderdash/internal/libs/sync"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/mocks"
	"github.com/dashpay/tenderdash/internal/p2p/p2ptest"
	"github.com/dashpay/tenderdash/libs/log"
	p2pproto "github.com/dashpay/tenderdash/proto/tendermint/p2p"
	"github.com/dashpay/tenderdash/types"
)

func echoReactor(ctx context.Context, channel p2p.Channel) {
	iter := channel.Receive(ctx)
	for iter.Next(ctx) {
		envelope := iter.Envelope()
		value := envelope.Message.(*p2ptest.Message).Value
		if err := channel.Send(ctx, p2p.Envelope{
			To:      envelope.From,
			Message: &p2ptest.Message{Value: value},
		}); err != nil {
			return
		}
	}
}

func TestRouter_Network(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	t.Cleanup(leaktest.Check(t))

	// Create a test network and open a channel where all peers run echoReactor.
	network := p2ptest.MakeNetwork(ctx, t, p2ptest.NetworkOptions{NumNodes: 8}, log.NewNopLogger())
	local := network.AnyNode()
	peers := network.Peers(local.NodeID)
	channels := network.MakeChannels(ctx, t, chDesc)

	network.Start(ctx, t)

	channel := channels[local.NodeID]
	for _, peer := range peers {
		go echoReactor(ctx, channels[peer.NodeID])
	}

	// Sending a message to each peer should work.
	for _, peer := range peers {
		p2ptest.RequireSendReceive(ctx, t, channel, peer.NodeID, &p2ptest.Message{Value: "foo"})
	}

	// Sending a broadcast should return back a message from all peers.
	p2ptest.RequireSend(ctx, t, channel, p2p.Envelope{
		Broadcast: true,
		Message:   &p2ptest.Message{Value: "bar"},
	})
	expect := make([]*p2p.Envelope, len(peers))
	for i, peer := range peers {
		expect[i] = &p2p.Envelope{
			From:       peer.NodeID,
			ChannelID:  1,
			Message:    &p2ptest.Message{Value: "bar"},
			Attributes: make(map[string]string),
		}
	}
	p2ptest.RequireReceiveUnordered(ctx, t, channel, expect)

	// We then submit an error for a peer, and watch it get disconnected and
	// then reconnected as the router retries it.
	peerUpdates := local.MakePeerUpdatesNoRequireEmpty(ctx, t)
	require.NoError(t, channel.SendError(ctx, p2p.PeerError{
		NodeID: peers[0].NodeID,
		Err:    errors.New("boom"),
	}))
	p2ptest.RequireUpdates(t, peerUpdates, []p2p.PeerUpdate{
		{NodeID: peers[0].NodeID, Status: p2p.PeerStatusDown},
		{NodeID: peers[0].NodeID, Status: p2p.PeerStatusUp},
	})
}

func TestRouter_Channel_Basic(t *testing.T) {
	t.Cleanup(leaktest.Check(t))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up a router with no transports (so no peers).
	peerManager, err := p2p.NewPeerManager(ctx, selfID, dbm.NewMemDB(), p2p.PeerManagerOptions{})
	require.NoError(t, err)

	testnet := p2ptest.MakeNetwork(ctx, t, p2ptest.NetworkOptions{NumNodes: 1}, log.NewNopLogger())

	router, err := p2p.NewRouter(
		log.NewNopLogger(),
		p2p.NopMetrics(),
		selfKey,
		peerManager,
		func() *types.NodeInfo { return &selfInfo },
		testnet.AnyNode().Transport,
		&p2p.Endpoint{},
		p2p.RouterOptions{},
	)
	require.NoError(t, err)

	require.NoError(t, router.Start(ctx))
	t.Cleanup(router.Wait)

	// Opening a channel should work.
	chctx, chcancel := context.WithCancel(ctx)
	defer chcancel()

	channel, err := router.OpenChannel(chctx, chDesc)
	require.NoError(t, err)
	require.NotNil(t, channel)

	// Opening the same channel again should fail.
	_, err = router.OpenChannel(ctx, chDesc)
	require.Error(t, err)

	// Opening a different channel should work.
	chDesc2 := &p2p.ChannelDescriptor{ID: 2}
	_, err = router.OpenChannel(ctx, chDesc2)
	require.NoError(t, err)

	// Closing the channel, then opening it again should be fine.
	chcancel()
	time.Sleep(200 * time.Millisecond) // yes yes, but Close() is async...

	channel, err = router.OpenChannel(ctx, chDesc)
	require.NoError(t, err)

	// We should be able to send on the channel, even though there are no peers.
	p2ptest.RequireSend(ctx, t, channel, p2p.Envelope{
		To:      types.NodeID(strings.Repeat("a", 40)),
		Message: &p2ptest.Message{Value: "foo"},
	})

	// A message to ourselves should be dropped.
	p2ptest.RequireSend(ctx, t, channel, p2p.Envelope{
		To:      selfID,
		Message: &p2ptest.Message{Value: "self"},
	})
	p2ptest.RequireEmpty(ctx, t, channel)
}

// Channel tests are hairy to mock, so we use an in-memory network instead.
func TestRouter_Channel_SendReceive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	t.Cleanup(leaktest.Check(t))

	// Create a test network and open a channel on all nodes.
	network := p2ptest.MakeNetwork(ctx, t, p2ptest.NetworkOptions{NumNodes: 3}, log.NewNopLogger())

	ids := network.NodeIDs()
	aID, bID, cID := ids[0], ids[1], ids[2]
	channels := network.MakeChannels(ctx, t, chDesc)
	a, b, c := channels[aID], channels[bID], channels[cID]
	otherChannels := network.MakeChannels(ctx, t, p2ptest.MakeChannelDesc(9))

	network.Start(ctx, t)

	// Sending a message a->b should work, and not send anything
	// further to a, b, or c.
	p2ptest.RequireSend(ctx, t, a, p2p.Envelope{To: bID, Message: &p2ptest.Message{Value: "foo"}})
	p2ptest.RequireReceive(ctx, t, b, p2p.Envelope{From: aID, Message: &p2ptest.Message{Value: "foo"}})
	p2ptest.RequireEmpty(ctx, t, a, b, c)

	// Sending a nil message a->b should be dropped.
	p2ptest.RequireSend(ctx, t, a, p2p.Envelope{To: bID, Message: nil})
	p2ptest.RequireEmpty(ctx, t, a, b, c)

	// Sending a different message type should be dropped.
	p2ptest.RequireSend(ctx, t, a, p2p.Envelope{To: bID, Message: &gogotypes.BoolValue{Value: true}})
	p2ptest.RequireEmpty(ctx, t, a, b, c)

	// Sending to an unknown peer should be dropped.
	p2ptest.RequireSend(ctx, t, a, p2p.Envelope{
		To:      types.NodeID(strings.Repeat("a", 40)),
		Message: &p2pproto.PexRequest{},
	})
	p2ptest.RequireEmpty(ctx, t, a, b, c)

	// Sending without a recipient should be dropped.
	p2ptest.RequireSend(ctx, t, a, p2p.Envelope{Message: &p2ptest.Message{Value: "noto"}})
	p2ptest.RequireEmpty(ctx, t, a, b, c)

	// Sending to self should be dropped.
	p2ptest.RequireSend(ctx, t, a, p2p.Envelope{To: aID, Message: &p2ptest.Message{Value: "self"}})
	p2ptest.RequireEmpty(ctx, t, a, b, c)

	// Removing b and sending to it should be dropped.
	network.Remove(ctx, t, bID)
	p2ptest.RequireSend(ctx, t, a, p2p.Envelope{To: bID, Message: &p2ptest.Message{Value: "nob"}})
	p2ptest.RequireEmpty(ctx, t, a, b, c)

	// After all this, sending a message c->a should work.
	p2ptest.RequireSend(ctx, t, c, p2p.Envelope{To: aID, Message: &p2ptest.Message{Value: "bar"}})
	p2ptest.RequireReceive(ctx, t, a, p2p.Envelope{From: cID, Message: &p2ptest.Message{Value: "bar"}})
	p2ptest.RequireEmpty(ctx, t, a, b, c)

	// None of these messages should have made it onto the other channels.
	for _, other := range otherChannels {
		p2ptest.RequireEmpty(ctx, t, other)
	}
}

func TestRouter_Channel_Broadcast(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	t.Cleanup(leaktest.Check(t))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a test network and open a channel on all nodes.
	network := p2ptest.MakeNetwork(ctx, t, p2ptest.NetworkOptions{NumNodes: 4}, log.NewNopLogger())

	ids := network.NodeIDs()
	aID, bID, cID, dID := ids[0], ids[1], ids[2], ids[3]
	channels := network.MakeChannels(ctx, t, chDesc)
	a, b, c, d := channels[aID], channels[bID], channels[cID], channels[dID]

	network.Start(ctx, t)

	// Sending a broadcast from b should work.
	p2ptest.RequireSend(ctx, t, b, p2p.Envelope{Broadcast: true, Message: &p2ptest.Message{Value: "foo"}})
	p2ptest.RequireReceive(ctx, t, a, p2p.Envelope{From: bID, Message: &p2ptest.Message{Value: "foo"}})
	p2ptest.RequireReceive(ctx, t, c, p2p.Envelope{From: bID, Message: &p2ptest.Message{Value: "foo"}})
	p2ptest.RequireReceive(ctx, t, d, p2p.Envelope{From: bID, Message: &p2ptest.Message{Value: "foo"}})
	p2ptest.RequireEmpty(ctx, t, a, b, c, d)

	// Removing one node from the network shouldn't prevent broadcasts from working.
	network.Remove(ctx, t, dID)
	p2ptest.RequireSend(ctx, t, a, p2p.Envelope{Broadcast: true, Message: &p2ptest.Message{Value: "bar"}})
	p2ptest.RequireReceive(ctx, t, b, p2p.Envelope{From: aID, Message: &p2ptest.Message{Value: "bar"}})
	p2ptest.RequireReceive(ctx, t, c, p2p.Envelope{From: aID, Message: &p2ptest.Message{Value: "bar"}})
	p2ptest.RequireEmpty(ctx, t, a, b, c, d)
}

func TestRouter_Channel_Error(t *testing.T) {
	t.Cleanup(leaktest.Check(t))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a test network and open a channel on all nodes.
	network := p2ptest.MakeNetwork(ctx, t, p2ptest.NetworkOptions{NumNodes: 3}, log.NewNopLogger())
	network.Start(ctx, t)

	ids := network.NodeIDs()
	aID, bID := ids[0], ids[1]
	channels := network.MakeChannels(ctx, t, chDesc)
	a := channels[aID]

	// Erroring b should cause it to be disconnected. It will reconnect shortly after.
	sub := network.Nodes[aID].MakePeerUpdates(ctx, t)
	p2ptest.RequireError(ctx, t, a, p2p.PeerError{NodeID: bID, Err: errors.New("boom")})
	p2ptest.RequireUpdates(t, sub, []p2p.PeerUpdate{
		{NodeID: bID, Status: p2p.PeerStatusDown},
		{NodeID: bID, Status: p2p.PeerStatusUp},
	})
}

func TestRouter_AcceptPeers(t *testing.T) {
	testcases := map[string]struct {
		peerInfo types.NodeInfo
		peerKey  crypto.PubKey
		ok       bool
	}{
		"valid handshake": {peerInfo, peerKey.PubKey(), true},
		"empty handshake": {types.NodeInfo{}, nil, false},
		"invalid key":     {peerInfo, selfKey.PubKey(), false},
		"self handshake":  {selfInfo, selfKey.PubKey(), false},
		"incompatible peer": {
			types.NodeInfo{
				NodeID:     peerID,
				ListenAddr: "0.0.0.0:0",
				Network:    "other-network",
				Moniker:    string(peerID),
				Channels:   tmsync.NewConcurrentSlice[uint16](),
			},
			peerKey.PubKey(),
			false,
		},
	}

	bctx, bcancel := context.WithCancel(context.Background())
	defer bcancel()

	for name, tc := range testcases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(bctx)
			defer cancel()

			t.Cleanup(leaktest.Check(t))

			// Set up a mock transport that handshakes.
			connCtx, connCancel := context.WithCancel(context.Background())
			mockConnection := &mocks.Connection{}
			mockConnection.On("String").Maybe().Return("mock")
			mockConnection.On("Handshake", mock.Anything, mock.Anything, selfInfo, selfKey).
				Return(tc.peerInfo, tc.peerKey, nil)
			mockConnection.On("Close").Run(func(_ mock.Arguments) { connCancel() }).Return(nil).Maybe()
			mockConnection.On("RemoteEndpoint").Return(p2p.Endpoint{})
			if tc.ok {
				mockConnection.On("ReceiveMessage", mock.Anything).Return(chID, nil, io.EOF).Maybe()
			}

			mockTransport := &mocks.Transport{}
			mockTransport.On("String").Maybe().Return("mock")
			mockTransport.On("Close").Return(nil).Maybe()
			mockTransport.On("Accept", mock.Anything).Once().Return(mockConnection, nil)
			mockTransport.On("Accept", mock.Anything).Maybe().Return(nil, io.EOF)
			mockTransport.On("Listen", mock.Anything).Return(nil)

			// Set up and start the router.
			peerManager, err := p2p.NewPeerManager(ctx, selfID, dbm.NewMemDB(), p2p.PeerManagerOptions{})
			require.NoError(t, err)

			sub := peerManager.Subscribe(ctx, "p2p")

			router, err := p2p.NewRouter(
				log.NewNopLogger(),
				p2p.NopMetrics(),
				selfKey,
				peerManager,
				func() *types.NodeInfo { return &selfInfo },
				mockTransport,
				nil,
				p2p.RouterOptions{},
			)
			require.NoError(t, err)
			require.NoError(t, router.Start(ctx))

			if tc.ok {
				p2ptest.RequireUpdate(t, sub, p2p.PeerUpdate{
					NodeID: tc.peerInfo.NodeID,
					Status: p2p.PeerStatusUp,
				})
				// force a context switch so that the
				// connection is handled.
				time.Sleep(time.Millisecond)
			} else {
				select {
				case <-connCtx.Done():
				case <-time.After(100 * time.Millisecond):
					require.Fail(t, "connection not closed")
				}
			}

			router.Stop()
			mockTransport.AssertExpectations(t)
			mockConnection.AssertExpectations(t)
		})
	}
}

func TestRouter_AcceptPeers_Errors(t *testing.T) {
	if testing.Short() {
		// Each subtest takes more than one second due to the time.Sleep call,
		// so just skip from the parent test in short mode.
		t.Skip("skipping test in short mode")
	}

	for _, err := range []error{io.EOF, context.Canceled, context.DeadlineExceeded} {
		t.Run(err.Error(), func(t *testing.T) {
			t.Cleanup(leaktest.Check(t))

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Set up a mock transport that returns io.EOF once, which should prevent
			// the router from calling Accept again.
			mockTransport := &mocks.Transport{}
			mockTransport.On("String").Maybe().Return("mock")
			mockTransport.On("Accept", mock.Anything).Once().Return(nil, err)
			mockTransport.On("Close").Return(nil)
			mockTransport.On("Listen", mock.Anything).Return(nil)

			// Set up and start the router.
			peerManager, err := p2p.NewPeerManager(ctx, selfID, dbm.NewMemDB(), p2p.PeerManagerOptions{})
			require.NoError(t, err)

			router, err := p2p.NewRouter(
				log.NewNopLogger(),
				p2p.NopMetrics(),
				selfKey,
				peerManager,
				func() *types.NodeInfo { return &selfInfo },
				mockTransport,
				nil,
				p2p.RouterOptions{},
			)
			require.NoError(t, err)

			require.NoError(t, router.Start(ctx))
			time.Sleep(time.Second)
			router.Stop()

			mockTransport.AssertExpectations(t)
		})
	}
}

func TestRouter_AcceptPeers_HeadOfLineBlocking(t *testing.T) {
	t.Cleanup(leaktest.Check(t))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up a mock transport that returns a connection that blocks during the
	// handshake. It should be able to accept several of these in parallel, i.e.
	// a single connection can't halt other connections being accepted.
	acceptCh := make(chan bool, 3)
	closeCh := make(chan time.Time)

	mockConnection := &mocks.Connection{}
	mockConnection.On("String").Maybe().Return("mock")
	mockConnection.On("Handshake", mock.Anything, mock.Anything, selfInfo, selfKey).
		WaitUntil(closeCh).Return(types.NodeInfo{}, nil, io.EOF)
	mockConnection.On("Close").Return(nil)
	mockConnection.On("RemoteEndpoint").Return(p2p.Endpoint{})

	mockTransport := &mocks.Transport{}
	mockTransport.On("String").Maybe().Return("mock")
	mockTransport.On("Close").Return(nil)
	mockTransport.On("Accept", mock.Anything).Times(3).Run(func(_ mock.Arguments) {
		acceptCh <- true
	}).Return(mockConnection, nil)
	mockTransport.On("Accept", mock.Anything).Once().Return(nil, io.EOF)
	mockTransport.On("Listen", mock.Anything).Return(nil)

	// Set up and start the router.
	peerManager, err := p2p.NewPeerManager(ctx, selfID, dbm.NewMemDB(), p2p.PeerManagerOptions{})
	require.NoError(t, err)

	router, err := p2p.NewRouter(
		log.NewNopLogger(),
		p2p.NopMetrics(),
		selfKey,
		peerManager,
		func() *types.NodeInfo { return &selfInfo },
		mockTransport,
		nil,
		p2p.RouterOptions{},
	)
	require.NoError(t, err)
	require.NoError(t, router.Start(ctx))

	require.Eventually(t, func() bool {
		return len(acceptCh) == 3
	}, time.Second, 10*time.Millisecond, "num", len(acceptCh))
	close(closeCh)
	time.Sleep(100 * time.Millisecond)

	router.Stop()
	mockTransport.AssertExpectations(t)
	mockConnection.AssertExpectations(t)
}

func TestRouter_DialPeers(t *testing.T) {
	testcases := map[string]struct {
		dialID   types.NodeID
		peerInfo types.NodeInfo
		peerKey  crypto.PubKey
		dialErr  error
		ok       bool
	}{
		"valid dial":         {peerInfo.NodeID, peerInfo, peerKey.PubKey(), nil, true},
		"empty handshake":    {peerInfo.NodeID, types.NodeInfo{}, nil, nil, false},
		"invalid key":        {peerInfo.NodeID, peerInfo, selfKey.PubKey(), nil, false},
		"unexpected node ID": {peerInfo.NodeID, selfInfo, selfKey.PubKey(), nil, false},
		"dial error":         {peerInfo.NodeID, peerInfo, peerKey.PubKey(), errors.New("boom"), false},
		"incompatible peer": {
			peerInfo.NodeID,
			types.NodeInfo{
				NodeID:     peerID,
				ListenAddr: "0.0.0.0:0",
				Network:    "other-network",
				Moniker:    string(peerID),
				Channels:   tmsync.NewConcurrentSlice[uint16](),
			},
			peerKey.PubKey(),
			nil,
			false,
		},
	}

	bctx, bcancel := context.WithCancel(context.Background())
	defer bcancel()

	for name, tc := range testcases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Cleanup(leaktest.Check(t))
			ctx, cancel := context.WithCancel(bctx)
			defer cancel()

			address := p2p.NodeAddress{Protocol: "mock", NodeID: tc.dialID}
			endpoint := &p2p.Endpoint{Protocol: "mock", Path: string(tc.dialID)}

			// Set up a mock transport that handshakes.
			connCtx, connCancel := context.WithCancel(context.Background())
			defer connCancel()
			mockConnection := &mocks.Connection{}
			mockConnection.On("String").Maybe().Return("mock")
			if tc.dialErr == nil {
				mockConnection.On("Handshake", mock.Anything, mock.Anything, selfInfo, selfKey).
					Return(tc.peerInfo, tc.peerKey, nil)
				mockConnection.On("Close").Run(func(_ mock.Arguments) { connCancel() }).Return(nil).Maybe()
			}
			if tc.ok {
				mockConnection.On("ReceiveMessage", mock.Anything).Return(chID, nil, io.EOF).Maybe()
			}

			mockTransport := &mocks.Transport{}
			mockTransport.On("String").Maybe().Return("mock")
			mockTransport.On("Close").Return(nil).Maybe()
			mockTransport.On("Listen", mock.Anything).Return(nil)
			mockTransport.On("Accept", mock.Anything).Maybe().Return(nil, io.EOF)
			if tc.dialErr == nil {
				mockTransport.On("Dial", mock.Anything, endpoint).Once().Return(mockConnection, nil)
				// This handles the retry when a dialed connection gets closed after ReceiveMessage
				// returns io.EOF above.
				mockTransport.On("Dial", mock.Anything, endpoint).Maybe().Return(nil, io.EOF)
			} else {
				mockTransport.On("Dial", mock.Anything, endpoint).Once().
					Run(func(_ mock.Arguments) { connCancel() }).
					Return(nil, tc.dialErr)
			}

			// Set up and start the router.
			peerManager, err := p2p.NewPeerManager(ctx, selfID, dbm.NewMemDB(), p2p.PeerManagerOptions{})
			require.NoError(t, err)

			added, err := peerManager.Add(address)
			require.NoError(t, err)
			require.True(t, added)
			sub := peerManager.Subscribe(ctx, "p2p")

			router, err := p2p.NewRouter(
				log.NewNopLogger(),
				p2p.NopMetrics(),
				selfKey,
				peerManager,
				func() *types.NodeInfo { return &selfInfo },
				mockTransport,
				nil,
				p2p.RouterOptions{},
			)
			require.NoError(t, err)
			require.NoError(t, router.Start(ctx))

			if tc.ok {
				p2ptest.RequireUpdate(t, sub, p2p.PeerUpdate{
					NodeID: tc.peerInfo.NodeID,
					Status: p2p.PeerStatusUp,
				})
				// force a context switch so that the
				// connection is handled.
				time.Sleep(time.Millisecond)
			} else {
				select {
				case <-connCtx.Done():
				case <-time.After(100 * time.Millisecond):
					require.Fail(t, "connection not closed")
				}
			}

			router.Stop()
			mockTransport.AssertExpectations(t)
			mockConnection.AssertExpectations(t)
		})
	}
}

func TestRouter_DialPeers_Parallel(t *testing.T) {
	t.Cleanup(leaktest.Check(t))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a := p2p.NodeAddress{Protocol: "mock", NodeID: types.NodeID(strings.Repeat("a", 40))}
	b := p2p.NodeAddress{Protocol: "mock", NodeID: types.NodeID(strings.Repeat("b", 40))}
	c := p2p.NodeAddress{Protocol: "mock", NodeID: types.NodeID(strings.Repeat("c", 40))}

	// Set up a mock transport that returns a connection that blocks during the
	// handshake. It should dial all peers in parallel.
	dialCh := make(chan bool, 3)
	closeCh := make(chan time.Time)

	mockConnection := &mocks.Connection{}
	mockConnection.On("String").Maybe().Return("mock")
	mockConnection.On("Handshake", mock.Anything, mock.Anything, selfInfo, selfKey).
		WaitUntil(closeCh).Return(types.NodeInfo{}, nil, io.EOF)
	mockConnection.On("Close").Return(nil)

	mockTransport := &mocks.Transport{}
	mockTransport.On("String").Maybe().Return("mock")
	mockTransport.On("Close").Return(nil)
	mockTransport.On("Listen", mock.Anything).Return(nil)
	mockTransport.On("Accept", mock.Anything).Once().Return(nil, io.EOF)
	for _, address := range []p2p.NodeAddress{a, b, c} {
		endpoint := &p2p.Endpoint{Protocol: address.Protocol, Path: string(address.NodeID)}
		mockTransport.On("Dial", mock.Anything, endpoint).Run(func(_ mock.Arguments) {
			dialCh <- true
		}).Return(mockConnection, nil)
	}

	// Set up and start the router.
	peerManager, err := p2p.NewPeerManager(ctx, selfID, dbm.NewMemDB(), p2p.PeerManagerOptions{})
	require.NoError(t, err)

	added, err := peerManager.Add(a)
	require.NoError(t, err)
	require.True(t, added)

	added, err = peerManager.Add(b)
	require.NoError(t, err)
	require.True(t, added)

	added, err = peerManager.Add(c)
	require.NoError(t, err)
	require.True(t, added)

	router, err := p2p.NewRouter(
		log.NewNopLogger(),
		p2p.NopMetrics(),
		selfKey,
		peerManager,
		func() *types.NodeInfo { return &selfInfo },
		mockTransport,
		nil,
		p2p.RouterOptions{
			NumConcurrentDials: func() int {
				ncpu := runtime.NumCPU()
				if ncpu <= 3 {
					return 3
				}
				return ncpu
			},
		},
	)

	require.NoError(t, err)
	require.NoError(t, router.Start(ctx))

	require.Eventually(t,
		func() bool {
			return len(dialCh) == 3
		},
		5*time.Second,
		100*time.Millisecond,
		"reached %d rather than 3", len(dialCh))

	close(closeCh)
	time.Sleep(500 * time.Millisecond)

	router.Stop()
	mockTransport.AssertExpectations(t)
	mockConnection.AssertExpectations(t)
}

func TestRouter_EvictPeers(t *testing.T) {
	t.Cleanup(leaktest.Check(t))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up a mock transport that we can evict.
	closeCh := make(chan time.Time)
	closeOnce := sync.Once{}

	mockConnection := &mocks.Connection{}
	mockConnection.On("String").Maybe().Return("mock")
	mockConnection.On("Handshake", mock.Anything, mock.Anything, selfInfo, selfKey).
		Return(peerInfo, peerKey.PubKey(), nil)
	mockConnection.On("ReceiveMessage", mock.Anything).WaitUntil(closeCh).Return(chID, nil, io.EOF)
	mockConnection.On("RemoteEndpoint").Return(p2p.Endpoint{})
	mockConnection.On("Close").Run(func(_ mock.Arguments) {
		closeOnce.Do(func() {
			close(closeCh)
		})
	}).Return(nil)

	mockTransport := &mocks.Transport{}
	mockTransport.On("String").Maybe().Return("mock")
	mockTransport.On("Close").Return(nil)
	mockTransport.On("Accept", mock.Anything).Once().Return(mockConnection, nil)
	mockTransport.On("Accept", mock.Anything).Maybe().Return(nil, io.EOF)
	mockTransport.On("Listen", mock.Anything).Return(nil)

	// Set up and start the router.
	peerManager, err := p2p.NewPeerManager(ctx, selfID, dbm.NewMemDB(), p2p.PeerManagerOptions{})
	require.NoError(t, err)

	sub := peerManager.Subscribe(ctx, "p2p")

	router, err := p2p.NewRouter(
		log.NewNopLogger(),
		p2p.NopMetrics(),
		selfKey,
		peerManager,
		func() *types.NodeInfo { return &selfInfo },
		mockTransport,
		nil,
		p2p.RouterOptions{},
	)
	require.NoError(t, err)
	require.NoError(t, router.Start(ctx))

	// Wait for the mock peer to connect, then evict it by reporting an error.
	p2ptest.RequireUpdate(t, sub, p2p.PeerUpdate{
		NodeID: peerInfo.NodeID,
		Status: p2p.PeerStatusUp,
	})

	peerManager.Errored(peerInfo.NodeID, errors.New("boom"))

	p2ptest.RequireUpdate(t, sub, p2p.PeerUpdate{
		NodeID: peerInfo.NodeID,
		Status: p2p.PeerStatusDown,
	})

	router.Stop()
	mockTransport.AssertExpectations(t)
	mockConnection.AssertExpectations(t)
}

func TestRouter_ChannelCompatability(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	t.Cleanup(leaktest.Check(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	incompatiblePeer := types.NodeInfo{
		NodeID:     peerID,
		ListenAddr: "0.0.0.0:0",
		Network:    "test",
		Moniker:    string(peerID),
		Channels:   tmsync.NewConcurrentSlice[uint16](0x03),
	}

	mockConnection := &mocks.Connection{}
	mockConnection.On("String").Maybe().Return("mock")
	mockConnection.On("Handshake", mock.Anything, mock.Anything, selfInfo, selfKey).
		Return(incompatiblePeer, peerKey.PubKey(), nil)
	mockConnection.On("RemoteEndpoint").Return(p2p.Endpoint{})
	mockConnection.On("Close").Return(nil)

	mockTransport := &mocks.Transport{}
	mockTransport.On("String").Maybe().Return("mock")
	mockTransport.On("Close").Return(nil)
	mockTransport.On("Accept", mock.Anything).Once().Return(mockConnection, nil)
	mockTransport.On("Accept", mock.Anything).Once().Return(nil, io.EOF)
	mockTransport.On("Listen", mock.Anything).Return(nil)

	// Set up and start the router.
	peerManager, err := p2p.NewPeerManager(ctx, selfID, dbm.NewMemDB(), p2p.PeerManagerOptions{})
	require.NoError(t, err)

	router, err := p2p.NewRouter(
		log.NewNopLogger(),
		p2p.NopMetrics(),
		selfKey,
		peerManager,
		func() *types.NodeInfo { return &selfInfo },
		mockTransport,
		nil,
		p2p.RouterOptions{},
	)
	require.NoError(t, err)
	require.NoError(t, router.Start(ctx))
	time.Sleep(1 * time.Second)
	router.Stop()
	require.Empty(t, peerManager.Peers())

	mockConnection.AssertExpectations(t)
	mockTransport.AssertExpectations(t)
}

func TestRouter_DontSendOnInvalidChannel(t *testing.T) {
	t.Cleanup(leaktest.Check(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	peer := types.NodeInfo{
		NodeID:     peerID,
		ListenAddr: "0.0.0.0:0",
		Network:    "test",
		Moniker:    string(peerID),
		Channels:   tmsync.NewConcurrentSlice[uint16](0x02),
	}

	mockConnection := &mocks.Connection{}
	mockConnection.On("String").Maybe().Return("mock")
	mockConnection.On("Handshake", mock.Anything, mock.Anything, selfInfo, selfKey).
		Return(peer, peerKey.PubKey(), nil)
	mockConnection.On("RemoteEndpoint").Return(p2p.Endpoint{})
	mockConnection.On("Close").Return(nil)
	mockConnection.On("ReceiveMessage", mock.Anything).Return(chID, nil, io.EOF)

	mockTransport := &mocks.Transport{}
	mockTransport.On("AddChannelDescriptors", mock.Anything).Return()
	mockTransport.On("String").Maybe().Return("mock")
	mockTransport.On("Close").Return(nil)
	mockTransport.On("Accept", mock.Anything).Once().Return(mockConnection, nil)
	mockTransport.On("Accept", mock.Anything).Maybe().Return(nil, io.EOF)
	mockTransport.On("Listen", mock.Anything).Return(nil)

	// Set up and start the router.
	peerManager, err := p2p.NewPeerManager(ctx, selfID, dbm.NewMemDB(), p2p.PeerManagerOptions{})
	require.NoError(t, err)

	sub := peerManager.Subscribe(ctx, "p2p")

	router, err := p2p.NewRouter(
		log.NewNopLogger(),
		p2p.NopMetrics(),
		selfKey,
		peerManager,
		func() *types.NodeInfo { return &selfInfo },
		mockTransport,
		nil,
		p2p.RouterOptions{},
	)
	require.NoError(t, err)
	require.NoError(t, router.Start(ctx))

	p2ptest.RequireUpdate(t, sub, p2p.PeerUpdate{
		NodeID: peerInfo.NodeID,
		Status: p2p.PeerStatusUp,
	})

	channel, err := router.OpenChannel(ctx, chDesc)
	require.NoError(t, err)

	require.NoError(t, channel.Send(ctx, p2p.Envelope{
		To:      peer.NodeID,
		Message: &p2ptest.Message{Value: "Hi"},
	}))

	router.Stop()
	mockTransport.AssertExpectations(t)
}

// Given a channel with non-zero enqueue timeout,
// when I send more messages than recv channel capacity,
// and I wait longer than enqueue timeout,
// then I should receive only the messages that fit into the recv channel capacity.
func TestRouter_Channel_Enqueue_Timeout(t *testing.T) {
	type testCase struct {
		sendCount         int
		expectedRecvCount int
		delay             time.Duration
	}

	chDesc := &p2p.ChannelDescriptor{
		ID:                  chID,
		Priority:            5,
		SendQueueCapacity:   100,
		RecvMessageCapacity: 10240,                 //10kB
		EnqueueTimeout:      10 * time.Millisecond, // FIXME: Check if this doesn't affect other tests
		RecvBufferCapacity:  10,
	}
	const processingTime = 10 * time.Millisecond

	testCases := []testCase{
		{sendCount: chDesc.RecvBufferCapacity, expectedRecvCount: chDesc.RecvBufferCapacity, delay: 0},
		{sendCount: chDesc.RecvBufferCapacity * 2, expectedRecvCount: chDesc.RecvBufferCapacity * 2, delay: 0},
		{sendCount: 1, expectedRecvCount: 1, delay: chDesc.EnqueueTimeout + 10*time.Millisecond},
		{sendCount: chDesc.RecvBufferCapacity - 1, expectedRecvCount: chDesc.RecvBufferCapacity - 1, delay: chDesc.EnqueueTimeout + processingTime},
		{sendCount: chDesc.RecvBufferCapacity, expectedRecvCount: chDesc.RecvBufferCapacity, delay: chDesc.EnqueueTimeout + processingTime},
		{sendCount: chDesc.RecvBufferCapacity + 1, expectedRecvCount: chDesc.RecvBufferCapacity, delay: 2*chDesc.EnqueueTimeout + processingTime},
		{sendCount: chDesc.RecvBufferCapacity + 5, expectedRecvCount: chDesc.RecvBufferCapacity, delay: 6*chDesc.EnqueueTimeout + processingTime},
	}

	// how many more messages we send than the recv channel capacity

	logger := log.NewTestingLoggerWithLevel(t, log.LogLevelTrace).WithTimestamp()

	t.Cleanup(leaktest.Check(t))

	// ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	// defer cancel()

	for _, tc := range testCases {
		tc := tc
		t.Run(fmt.Sprintf("send=%d,recv=%d,delay=%s", tc.sendCount, tc.expectedRecvCount, tc.delay), func(t *testing.T) {
			// timeout that will expire if we don't receive some of the expected messages
			ctxTimeout := tc.delay + 50*time.Millisecond
			ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
			defer cancel()
			// Create a test network and open a channel on all nodes.
			network := p2ptest.MakeNetwork(ctx, t, p2ptest.NetworkOptions{NumNodes: 2}, logger)

			ids := network.NodeIDs()
			aID, bID := ids[0], ids[1]
			channels := network.MakeChannels(ctx, t, chDesc)
			a, b := channels[aID], channels[bID]

			network.Start(ctx, t)

			wg := sync.WaitGroup{}

			// Start the test - send messages in a goroutine to not block on full chan
			wg.Add(1)
			go func() {
				for i := 0; i < tc.sendCount; i++ {
					sentEnvelope := p2p.Envelope{To: bID, Message: &p2ptest.Message{Value: strconv.Itoa(i)}}
					p2ptest.RequireSend(ctx, t, a, sentEnvelope)
					logger.Trace("Sent message", "id", i)
				}

				wg.Done()
			}()
			runtime.Gosched()

			// sleep to ensure the timeout expired and at least some msgs will be dropped
			time.Sleep(tc.delay)
			count := 0

			// check if we received all the messages we expected
			iter := b.Receive(ctx)
			for count < tc.expectedRecvCount && iter.Next(ctx) {
				// this will hang if we don't receive the expected number of messages
				e := iter.Envelope()
				logger.Trace("received message", "message", e.Message)
				count++
			}
			logger.Info("Received %d messages", count)

			wg.Wait()

			// this will error if we receive too many messages
			p2ptest.RequireEmpty(ctx, t, a, b)

			// this will error if we don't receive the expected number of messages
			assert.NoError(t, ctx.Err(), "timed out, received %d msgs, expected %d", count, tc.expectedRecvCount)
		})
	}
}
