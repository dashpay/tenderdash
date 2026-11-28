package statesync

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	sync "github.com/sasha-s/go-deadlock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/log"
	ssproto "github.com/dashpay/tenderdash/proto/tendermint/statesync"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

type channelInternal struct {
	In    chan p2p.Envelope
	Out   chan p2p.Envelope
	Error chan p2p.PeerError
}

func testChannel(size int) (*channelInternal, p2p.Channel) {
	in := &channelInternal{
		In:    make(chan p2p.Envelope, size),
		Out:   make(chan p2p.Envelope, size),
		Error: make(chan p2p.PeerError, size),
	}
	return in, p2p.NewChannel(0, "test", in.In, in.Out, in.Error)
}

func TestDispatcherBasic(t *testing.T) {
	t.Cleanup(leaktest.Check(t))
	const numPeers = 5

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	chans, ch := testChannel(100)

	d := NewDispatcher(ch, log.NewTestingLogger(t))
	go handleRequests(ctx, t, d, chans.Out)

	peers := createPeerSet(numPeers)
	wg := sync.WaitGroup{}

	// make a bunch of async requests and require that the correct responses are
	// given
	for i := 0; i < numPeers; i++ {
		wg.Add(1)
		go func(height int64) {
			defer wg.Done()
			lb, err := d.LightBlock(ctx, height, peers[height-1])
			require.NoError(t, err)
			require.NotNil(t, lb)
			require.Equal(t, lb.Height, height)
		}(int64(i + 1))
	}
	wg.Wait()

	// assert that all calls were responded to
	assert.Empty(t, d.calls)
}

func TestDispatcherReturnsNoBlock(t *testing.T) {
	t.Cleanup(leaktest.Check(t))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	chans, ch := testChannel(100)

	d := NewDispatcher(ch, log.NewTestingLogger(t))

	peer := factory.NodeID(t, "a")

	responded := make(chan error, 1)
	go func() {
		select {
		case <-chans.Out:
			responded <- d.Respond(nil, peer, 0)
		case <-ctx.Done():
			responded <- ctx.Err()
		}
	}()

	lb, err := d.LightBlock(ctx, 1, peer)

	require.Nil(t, lb)
	require.NoError(t, err)
	require.NoError(t, <-responded)
}

// TestDispatcherReportsDeliveringConnection pins what fences a refusal to its sender:
// calls are tracked per peer, so a response from a connection that has since been
// replaced can resolve a newer call, and only its own generation can tell them apart.
func TestDispatcherReportsDeliveringConnection(t *testing.T) {
	const staleConnID = uint64(7)

	for _, tc := range []struct {
		name    string
		tamper  func(*tmproto.LightBlock)
		wantErr error
	}{
		{name: "usable response", tamper: func(*tmproto.LightBlock) {}},
		{
			name:    "undecodable response",
			tamper:  func(lb *tmproto.LightBlock) { lb.ValidatorSet.QuorumType = int32(outOfRangeQuorumType) },
			wantErr: errMalformedLightBlock,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			chans, ch := testChannel(100)
			d := NewDispatcher(ch, log.NewTestingLogger(t))
			peer := factory.NodeID(t, "a")

			resp := mockLBResp(ctx, t, peer, 1, time.Now())
			block, err := resp.block.ToProto()
			require.NoError(t, err)
			tc.tamper(block)

			responded := make(chan error, 1)
			go func() {
				<-chans.Out
				responded <- d.Respond(block, peer, staleConnID)
			}()

			_, connID, err := d.lightBlockWithConn(ctx, 1, peer)
			require.ErrorIs(t, err, tc.wantErr)
			require.Equal(t, staleConnID, connID,
				"the caller must learn which connection delivered the response")
			require.ErrorIs(t, <-responded, tc.wantErr)
		})
	}
}

// TestDispatcherDeliversUndecodableResponse pins the contract the backfill loop
// depends on to tell a peer's bad answer from a slow one: a response that cannot be
// decoded is handed to the waiting caller, marked as such, rather than dropped.
//
// Dropping it is indistinguishable from silence, and the caller can only resolve
// silence by waiting out its own deadline - so the assertion is not just that an
// error comes back, but that it comes back well before the deadline the caller set.
func TestDispatcherDeliversUndecodableResponse(t *testing.T) {
	t.Cleanup(leaktest.Check(t))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	chans, ch := testChannel(100)
	d := NewDispatcher(ch, log.NewTestingLogger(t))
	peer := factory.NodeID(t, "a")

	go func() {
		<-chans.Out
		resp := mockLBResp(ctx, t, peer, 1, time.Now())
		block, err := resp.block.ToProto()
		require.NoError(t, err)
		// Survives ToProto and the header hashes, and is refused by the
		// ValidatorSet.ValidateBasic that decoding runs.
		block.ValidatorSet.QuorumType = int32(outOfRangeQuorumType)
		require.Error(t, d.Respond(block, peer, 0),
			"Respond must still report an undecodable response to its own caller")
	}()

	const deadline = 5 * time.Second
	callCtx, callCancel := context.WithTimeout(ctx, deadline)
	defer callCancel()

	started := time.Now()
	lb, err := d.LightBlock(callCtx, 1, peer)
	elapsed := time.Since(started)

	require.Nil(t, lb)
	require.ErrorIs(t, err, errMalformedLightBlock,
		"the caller must be able to tell an unusable answer from a peer that said nothing")
	require.ErrorContains(t, err, "unsupported quorum type",
		"and must be told why, so the reason can reach the operator")
	require.NotErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, elapsed, deadline/2,
		"the refusal must arrive when the response does, not when the caller gives up")
}

// TestDispatcherSurplusResponseDoesNotBlock pins that one request takes one answer.
// Respond runs on the loop that handles every statesync channel, so a further
// response must be refused at once, whether or not the caller has already read the
// first one, and must not replace it: the caller attributes what it got to the
// connection that delivered the first answer.
func TestDispatcherSurplusResponseDoesNotBlock(t *testing.T) {
	const firstConnID, surplusConnID = uint64(11), uint64(22)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	peer := factory.NodeID(t, "a")
	first, err := mockLBResp(ctx, t, peer, 1, time.Now()).block.ToProto()
	require.NoError(t, err)
	other := mockLBResp(ctx, t, peer, 2, time.Now()).block
	block, err := other.ToProto()
	require.NoError(t, err)
	malformed, err := other.ToProto()
	require.NoError(t, err)
	malformed.ValidatorSet.QuorumType = int32(outOfRangeQuorumType)

	testCases := map[string]*tmproto.LightBlock{
		"no block":  nil,
		"block":     block,
		"malformed": malformed,
	}
	for name, surplus := range testCases {
		for _, readFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/first answer read %t", name, readFirst), func(t *testing.T) {
				t.Cleanup(leaktest.Check(t))

				ctx, cancel := context.WithCancel(ctx)
				defer cancel()

				_, ch := testChannel(1)
				d := NewDispatcher(ch, log.NewTestingLogger(t))

				callCh, err := d.dispatch(ctx, peer, 1)
				require.NoError(t, err)
				require.NoError(t, d.Respond(first, peer, firstConnID))

				var result lightBlockResult
				if readFirst {
					// The call stays registered until its caller cleans up.
					result = <-callCh
				}

				done := make(chan error, 1)
				go func() { done <- d.Respond(surplus, peer, surplusConnID) }()

				select {
				case err := <-done:
					require.ErrorIs(t, err, errUnsolicitedResponse)
				case <-time.After(5 * time.Second):
					t.Fatal("a surplus response blocked the dispatcher")
				}

				if readFirst {
					require.Empty(t, callCh, "a surplus response must not be queued for the caller")
				} else {
					result = <-callCh
				}
				require.NoError(t, result.err)
				require.NotNil(t, result.block)
				require.EqualValues(t, 1, result.block.Height, "the first answer must survive the surplus one")
				require.Equal(t, firstConnID, result.connID,
					"the answer must stay attributed to the connection that delivered it")
			})
		}
	}
}

// blockingSendChannel holds each request until the request's context ends, and
// reports it on sending; once release is set, requests pass through.
type blockingSendChannel struct {
	p2p.Channel
	sending chan p2p.Envelope
	release atomic.Bool
}

func (c *blockingSendChannel) Send(ctx context.Context, e p2p.Envelope) error {
	if c.release.Load() {
		return c.Channel.Send(ctx, e)
	}
	c.sending <- e
	<-ctx.Done()
	return ctx.Err()
}

// TestDispatcherFailedRequestFreesPeer pins that a request that could not be sent
// leaves nothing behind: the peer can be asked again, and a response it sends
// anyway is refused as unsolicited - not a panic, which the reactor would report
// against the peer.
func TestDispatcherFailedRequestFreesPeer(t *testing.T) {
	t.Cleanup(leaktest.Check(t))
	const lateConnID, nextConnID = uint64(33), uint64(44)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	peer := factory.NodeID(t, "a")
	block, err := mockLBResp(ctx, t, peer, 1, time.Now()).block.ToProto()
	require.NoError(t, err)

	chans, inner := testChannel(1)
	ch := &blockingSendChannel{Channel: inner, sending: make(chan p2p.Envelope, 1)}
	d := NewDispatcher(ch, log.NewTestingLogger(t))

	// Fail the send only once the call is registered and the request is in Send.
	sendCtx, sendCancel := context.WithCancel(ctx)
	defer sendCancel()
	failed := make(chan error, 1)
	failedConnID := make(chan uint64, 1)
	go func() {
		_, connID, err := d.lightBlockWithConn(sendCtx, 1, peer)
		failedConnID <- connID
		failed <- err
	}()
	<-ch.sending
	sendCancel()
	require.ErrorIs(t, <-failed, context.Canceled)
	require.Zero(t, <-failedConnID, "a request that was never sent has no delivering connection")

	require.NotPanics(t, func() {
		require.ErrorIs(t, d.Respond(block, peer, lateConnID), errUnsolicitedResponse)
	})

	ch.release.Store(true)
	responded := make(chan error, 1)
	go func() {
		select {
		case <-chans.Out:
			responded <- d.Respond(block, peer, nextConnID)
		case <-ctx.Done():
			responded <- ctx.Err()
		}
	}()
	lb, connID, err := d.lightBlockWithConn(ctx, 1, peer)
	require.NoError(t, err, "a failed request must not leave the peer busy")
	require.NoError(t, <-responded)
	require.NotNil(t, lb)
	require.Equal(t, nextConnID, connID)
	require.Empty(t, d.calls)
}

// TestDispatcherRespondDoesNotWaitForPendingRequest pins that a request waiting to
// be sent does not hold up responses, which arrive on the loop that serves every
// statesync channel.
func TestDispatcherRespondDoesNotWaitForPendingRequest(t *testing.T) {
	t.Cleanup(leaktest.Check(t))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, inner := testChannel(1)
	ch := &blockingSendChannel{Channel: inner, sending: make(chan p2p.Envelope, 1)}
	d := NewDispatcher(ch, log.NewTestingLogger(t))
	pending, other := factory.NodeID(t, "a"), factory.NodeID(t, "b")

	callCtx, callCancel := context.WithCancel(ctx)
	defer callCancel()
	failed := make(chan error, 1)
	go func() {
		_, _, err := d.lightBlockWithConn(callCtx, 1, pending)
		failed <- err
	}()
	<-ch.sending

	done := make(chan error, 1)
	go func() { done <- d.Respond(nil, other, 1) }()
	select {
	case err := <-done:
		require.ErrorIs(t, err, errUnsolicitedResponse)
	case <-time.After(5 * time.Second):
		t.Fatal("a response waited for a request still being sent")
	}

	callCancel()
	require.ErrorIs(t, <-failed, context.Canceled)
	require.Empty(t, d.calls)
}

// TestDispatcherFailedRequestKeepsNewerCall pins that a request that fails to send
// removes only its own call: once Close has dropped it, the peer can be asked again,
// and the older failure must not cancel that newer call.
func TestDispatcherFailedRequestKeepsNewerCall(t *testing.T) {
	t.Cleanup(leaktest.Check(t))
	const connID = uint64(55)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	peer := factory.NodeID(t, "a")
	block, err := mockLBResp(ctx, t, peer, 1, time.Now()).block.ToProto()
	require.NoError(t, err)

	_, inner := testChannel(1)
	ch := &blockingSendChannel{Channel: inner, sending: make(chan p2p.Envelope, 1)}
	d := NewDispatcher(ch, log.NewTestingLogger(t))

	oldCtx, oldCancel := context.WithCancel(ctx)
	defer oldCancel()
	oldDone := make(chan error, 1)
	go func() {
		_, _, err := d.lightBlockWithConn(oldCtx, 1, peer)
		oldDone <- err
	}()
	<-ch.sending

	closed := make(chan struct{})
	go func() {
		d.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close waited for a request still being sent")
	}

	ch.release.Store(true)
	newCh, err := d.dispatch(ctx, peer, 1)
	require.NoError(t, err)

	oldCancel()
	require.ErrorIs(t, <-oldDone, context.Canceled)

	require.NoError(t, d.Respond(block, peer, connID), "the newer call must still be registered")
	result := <-newCh
	require.NoError(t, result.err)
	require.Equal(t, connID, result.connID)
}

func TestDispatcherTimeOutWaitingOnLightBlock(t *testing.T) {
	t.Cleanup(leaktest.Check(t))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, ch := testChannel(100)
	d := NewDispatcher(ch, log.NewTestingLogger(t))
	peer := factory.NodeID(t, "a")

	ctx, cancelFunc := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancelFunc()

	lb, err := d.LightBlock(ctx, 1, peer)

	require.Error(t, err)
	require.Equal(t, context.DeadlineExceeded, err)
	require.Nil(t, lb)
}

func TestDispatcherProviders(t *testing.T) {
	t.Cleanup(leaktest.Check(t))

	chainID := "test-chain"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	chans, ch := testChannel(100)

	d := NewDispatcher(ch, log.NewTestingLogger(t))
	go handleRequests(ctx, t, d, chans.Out)

	peers := createPeerSet(5)
	providers := make([]*BlockProvider, len(peers))
	for idx, peer := range peers {
		providers[idx] = NewBlockProvider(peer, chainID, d)
	}
	require.Len(t, providers, 5)

	for i, p := range providers {
		assert.Equal(t, string(peers[i]), p.String(), i)
		lb, err := p.LightBlock(ctx, 10)
		assert.NoError(t, err)
		assert.NotNil(t, lb)
	}
}

func TestPeerListBasic(t *testing.T) {
	t.Cleanup(leaktest.Check(t))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	peerList := newPeerList()
	assert.Zero(t, peerList.Len())
	numPeers := 10
	peerSet := createPeerSet(numPeers)

	for _, peer := range peerSet {
		peerList.Append(peer)
	}

	for idx, peer := range peerList.All() {
		assert.Equal(t, peer, peerSet[idx])
	}

	assert.Equal(t, numPeers, peerList.Len())

	half := numPeers / 2
	for i := 0; i < half; i++ {
		assert.Equal(t, peerSet[i], peerList.Pop(ctx))
	}
	assert.Equal(t, half, peerList.Len())

	// removing a peer that doesn't exist should not change the list
	peerList.Remove(types.NodeID("lp"))
	assert.Equal(t, half, peerList.Len())

	// removing a peer that exists should decrease the list size by one
	peerList.Remove(peerSet[half])
	assert.Equal(t, numPeers-half-1, peerList.Len())

	// popping the next peer should work as expected
	assert.Equal(t, peerSet[half+1], peerList.Pop(ctx))
	assert.Equal(t, numPeers-half-2, peerList.Len())

	// append the two peers back
	peerList.Append(peerSet[half])
	peerList.Append(peerSet[half+1])
	assert.Equal(t, half, peerList.Len())
}

func TestPeerListBlocksWhenEmpty(t *testing.T) {
	t.Cleanup(leaktest.Check(t))
	peerList := newPeerList()
	require.Zero(t, peerList.Len())
	doneCh := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		peerList.Pop(ctx)
		close(doneCh)
	}()
	select {
	case <-doneCh:
		t.Error("empty peer list should not have returned result")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestEmptyPeerListReturnsWhenContextCanceled(t *testing.T) {
	t.Cleanup(leaktest.Check(t))
	peerList := newPeerList()
	require.Zero(t, peerList.Len())
	doneCh := make(chan struct{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wrapped, cancel := context.WithCancel(ctx)
	go func() {
		peerList.Pop(wrapped)
		close(doneCh)
	}()
	select {
	case <-doneCh:
		t.Error("empty peer list should not have returned result")
	case <-time.After(100 * time.Millisecond):
	}

	cancel()

	select {
	case <-doneCh:
	case <-time.After(100 * time.Millisecond):
		t.Error("peer list should have returned after context canceled")
	}
}

func TestPeerListConcurrent(t *testing.T) {
	t.Cleanup(leaktest.Check(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	peerList := newPeerList()
	numPeers := 10

	wg := sync.WaitGroup{}
	// we run a set of goroutines requesting the next peer in the list. As the
	// peer list hasn't been populated each these go routines should block
	for i := 0; i < numPeers/2; i++ {
		go func() {
			_ = peerList.Pop(ctx)
			wg.Done()
		}()
	}

	// now we add the peers to the list, this should allow the previously
	// blocked go routines to unblock
	for _, peer := range createPeerSet(numPeers) {
		wg.Add(1)
		peerList.Append(peer)
	}

	// we request the second half of the peer set
	for i := 0; i < numPeers/2; i++ {
		go func() {
			_ = peerList.Pop(ctx)
			wg.Done()
		}()
	}

	// we use a context with cancel and a separate go routine to wait for all
	// the other goroutines to close.
	go func() { wg.Wait(); cancel() }()

	select {
	case <-time.After(time.Second):
		// not all of the blocked go routines waiting on peers have closed after
		// one second. This likely means the list got blocked.
		t.Failed()
	case <-ctx.Done():
		// there should be no peers remaining
		require.Equal(t, 0, peerList.Len())
	}
}

func TestPeerListRemove(t *testing.T) {
	peerList := newPeerList()
	numPeers := 10

	peerSet := createPeerSet(numPeers)
	for _, peer := range peerSet {
		peerList.Append(peer)
	}

	for _, peer := range peerSet {
		peerList.Remove(peer)
		for _, p := range peerList.All() {
			require.NotEqual(t, p, peer)
		}
		numPeers--
		require.Equal(t, numPeers, peerList.Len())
	}
}

// handleRequests is a helper function usually run in a separate go routine to
// imitate the expected responses of the reactor wired to the dispatcher
func handleRequests(ctx context.Context, t *testing.T, d *Dispatcher, ch chan p2p.Envelope) {
	t.Helper()
	for {
		select {
		case request := <-ch:
			height := request.Message.(*ssproto.LightBlockRequest).Height
			peer := request.To
			resp := mockLBResp(ctx, t, peer, int64(height), time.Now())
			block, _ := resp.block.ToProto()
			require.NoError(t, d.Respond(block, resp.peer, 0))
		case <-ctx.Done():
			return
		}
	}
}

func createPeerSet(num int) []types.NodeID {
	peers := make([]types.NodeID, num)
	for i := 0; i < num; i++ {
		peers[i], _ = types.NewNodeID(strings.Repeat(fmt.Sprintf("%d", i), 2*types.NodeIDByteLength))
	}
	return peers
}
