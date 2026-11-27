package statesync

import (
	"context"
	"errors"
	"fmt"
	"time"

	sync "github.com/sasha-s/go-deadlock"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/light/provider"
	ssproto "github.com/dashpay/tenderdash/proto/tendermint/statesync"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

var (
	errNoConnectedPeers    = errors.New("no available peers to dispatch request to")
	errUnsolicitedResponse = errors.New("unsolicited light block response")
	errPeerAlreadyBusy     = errors.New("peer is already processing a request")
	// errMalformedLightBlock marks a response that could not be turned into a light
	// block at all. What a peer sends is its own choice, so unlike a timeout this is
	// attributable, and callers must be able to tell the two apart.
	errMalformedLightBlock = errors.New("malformed light block response")
)

// lightBlockResult is what one dispatched call resolves to. A nil block with a nil
// error is the peer answering that it does not have the height; a non-nil error is
// a response that arrived and could not be used, carrying the reason it was refused.
//
// connID is the generation of the connection that delivered the response. Calls are
// tracked per peer, so it can be older than the connection the request went out on.
type lightBlockResult struct {
	block  *types.LightBlock
	err    error
	connID uint64
}

// pendingCall is a light block request awaiting its one answer. answered is set,
// under the dispatcher lock, once that answer is queued.
type pendingCall struct {
	ch       chan lightBlockResult
	answered bool
}

// A Dispatcher multiplexes concurrent requests by multiple peers for light blocks.
// Only one request per peer can be sent at a time. Subsequent concurrent requests will
// report an error from the LightBlock method.
// NOTE: It is not the responsibility of the dispatcher to verify the light blocks.
type Dispatcher struct {
	// the channel with which to send light block requests on
	requestCh p2p.Channel
	logger    log.Logger
	mtx       sync.Mutex
	// all pending calls that have been dispatched and are awaiting an answer
	calls map[types.NodeID]*pendingCall
}

func NewDispatcher(requestChannel p2p.Channel, logger log.Logger) *Dispatcher {
	return &Dispatcher{
		logger:    logger.With("module", "lb-dispatcher"),
		requestCh: requestChannel,
		calls:     make(map[types.NodeID]*pendingCall),
	}
}

// LightBlock uses the request channel to fetch a light block from a given peer
// tracking, the call and waiting for the reactor to pass back the response. A nil
// LightBlock response is used to signal that the peer doesn't have the requested LightBlock.
func (d *Dispatcher) LightBlock(ctx context.Context, height int64, peer types.NodeID) (*types.LightBlock, error) {
	lb, _, err := d.lightBlockWithConn(ctx, height, peer)
	return lb, err
}

// lightBlockWithConn is LightBlock that also returns the generation of the connection
// that delivered the response, for attributing a refusal of it to that connection.
func (d *Dispatcher) lightBlockWithConn(ctx context.Context, height int64, peer types.NodeID) (*types.LightBlock, uint64, error) {
	// dispatch the request to the peer
	callCh, err := d.dispatch(ctx, peer, height)
	if err != nil {
		return nil, 0, err
	}

	defer d.release(peer, callCh)

	// wait for a response, cancel or timeout
	start := time.Now()
	select {
	case resp := <-callCh:
		if resp.err != nil {
			d.logger.Debug("received an unusable light-block response",
				"height", height,
				"took", time.Since(start).String(),
				"error", resp.err,
			)
			return nil, resp.connID, resp.err
		}
		d.logger.Debug("received light-block",
			"height", height,
			"took", time.Since(start).String(),
		)
		return resp.block, resp.connID, nil

	case <-ctx.Done():
		d.logger.Debug("failed to get a light-block",
			"height", height,
			"took", time.Since(start).String(),
		)
		return nil, 0, ctx.Err()
	}
}

// dispatch registers a call for a peer that is not already busy, then sends the
// request. The send happens outside the lock, so responses are not held up by it.
func (d *Dispatcher) dispatch(ctx context.Context, peer types.NodeID, height int64) (chan lightBlockResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	d.mtx.Lock()
	if _, ok := d.calls[peer]; ok {
		d.mtx.Unlock()
		return nil, errPeerAlreadyBusy
	}
	ch := make(chan lightBlockResult, 1)
	d.calls[peer] = &pendingCall{ch: ch}
	d.mtx.Unlock()

	if err := d.requestCh.Send(ctx, p2p.Envelope{
		To: peer,
		Message: &ssproto.LightBlockRequest{
			Height: uint64(height),
		},
	}); err != nil {
		d.release(peer, ch)
		return nil, err
	}

	return ch, nil
}

// release removes the peer's call if it is still the one that owns ch. Close may
// have dropped it already, and the peer may have a newer call by now.
func (d *Dispatcher) release(peer types.NodeID, ch chan lightBlockResult) {
	d.mtx.Lock()
	defer d.mtx.Unlock()
	if call, ok := d.calls[peer]; ok && call.ch == ch {
		delete(d.calls, peer)
	}
}

// Respond allows the underlying process which receives requests on the
// requestCh to respond with the respective light block. A nil response is used to
// represent that the receiver of the request does not have a light block at that height.
//
// A response that cannot be decoded is delivered to the caller as a refusal rather
// than dropped. Dropping it leaves the caller waiting out its own deadline and
// reading the sender as slow, which charges a shared retry budget for one peer's
// choice and hides the reason the response was unusable.
func (d *Dispatcher) Respond(lb *tmproto.LightBlock, peer types.NodeID, connID uint64) error {
	d.mtx.Lock()
	defer d.mtx.Unlock()

	// A call takes one answer; anything else, including a response after the
	// caller's timeout, is refused.
	call, ok := d.calls[peer]
	if !ok || call.answered {
		return errUnsolicitedResponse
	}

	// A nil lb means the peer does not have the requested light block; the caller
	// gets a nil block.
	result := lightBlockResult{connID: connID}
	var err error
	if lb != nil {
		// LightBlockFromProto runs the ValidateBasic of every part it decodes, so this
		// covers the whole of the header, commit and validator set - including the
		// quorum type, whose sign-hash conversion panics on a value outside a uint8.
		if result.block, err = types.LightBlockFromProto(lb); err != nil {
			err = fmt.Errorf("%w: %w", errMalformedLightBlock, err)
			result.err = err
		}
	}

	// The channel has room for exactly this one answer, so the send never waits.
	call.answered = true
	call.ch <- result
	return err
}

// Close shuts down the dispatcher and cancels any pending calls awaiting responses.
// Peers awaiting responses that have not arrived are delivered a nil block.
func (d *Dispatcher) Close() {
	d.mtx.Lock()
	defer d.mtx.Unlock()
	for peer := range d.calls {
		// Channels are never closed: a caller still waiting gives up on its own
		// context, and an answer is never sent to a call no longer registered.
		delete(d.calls, peer)
	}
}

//----------------------------------------------------------------

// BlockProvider is a p2p based light provider which uses a dispatcher connected
// to the state sync reactor to serve light blocks to the light client
//
// TODO: This should probably be moved over to the light package but as we're
// not yet officially supporting p2p light clients we'll leave this here for now.
//
// NOTE: BlockProvider will return an error with concurrent calls. However, we don't
// need a mutex because a light client (and the backfill process) will never call a
// method more than once at the same time
type BlockProvider struct {
	peer       types.NodeID
	chainID    string
	dispatcher *Dispatcher
}

// Creates a block provider which implements the light client Provider interface.
func NewBlockProvider(peer types.NodeID, chainID string, dispatcher *Dispatcher) *BlockProvider {
	return &BlockProvider{
		peer:       peer,
		chainID:    chainID,
		dispatcher: dispatcher,
	}
}

// LightBlock fetches a light block from the peer at a specified height returning either a
// light block or an appropriate error.
func (p *BlockProvider) LightBlock(ctx context.Context, height int64) (*types.LightBlock, error) {
	lb, err := p.dispatcher.LightBlock(ctx, height, p.peer)
	switch err {
	case nil:
		if lb == nil {
			return nil, provider.ErrLightBlockNotFound
		}
	case context.DeadlineExceeded, context.Canceled:
		return nil, err
	case errPeerAlreadyBusy:
		return nil, provider.ErrLightBlockNotFound
	default:
		return nil, provider.ErrUnreliableProvider{Reason: err}
	}

	// check that the height requested is the same one returned
	if lb.Height != height {
		return nil, provider.ErrBadLightBlock{
			Reason: fmt.Errorf("expected height %d, got height %d", height, lb.Height),
		}
	}

	// perform basic validation
	if err := lb.ValidateBasic(p.chainID); err != nil {
		return nil, provider.ErrBadLightBlock{Reason: err}
	}

	return lb, nil
}

// ReportEvidence should allow for the light client to report any light client
// attacks. This is a no op as there currently isn't a way to wire this up to
// the evidence reactor (we should endeavor to do this in the future but for now
// it's not critical for backwards verification)
func (p *BlockProvider) ReportEvidence(_ctx context.Context, _ev types.Evidence) error {
	return nil
}

// String implements stringer interface
func (p *BlockProvider) String() string { return string(p.peer) }

// Returns the ID address of the provider (NodeID of peer)
func (p *BlockProvider) ID() string { return string(p.peer) }

//----------------------------------------------------------------

// peerList is a rolling list of peers. This is used to distribute the load of
// retrieving blocks over all the peers the reactor is connected to
type peerList struct {
	mtx     sync.Mutex
	peers   []types.NodeID
	waiting []chan types.NodeID
	// taken counts every peer ever handed to a caller, including one passed
	// straight to a waiting Pop. A caller that must tell an empty list apart from
	// one whose peers are merely in use compares it against the hand-outs it has
	// accounted for: a peer is out of the list from the moment it is handed over,
	// not from the moment its new owner records it.
	taken uint64
}

func newPeerList() *peerList {
	return &peerList{
		peers:   make([]types.NodeID, 0),
		waiting: make([]chan types.NodeID, 0),
	}
}

func (l *peerList) Len() int {
	l.mtx.Lock()
	defer l.mtx.Unlock()
	return len(l.peers)
}

func (l *peerList) Pop(ctx context.Context) types.NodeID {
	l.mtx.Lock()
	if len(l.peers) == 0 {
		// if we don't have any peers in the list we block until a peer is
		// appended
		wait := make(chan types.NodeID, 1)
		l.waiting = append(l.waiting, wait)
		// unlock whilst waiting so that the list can be appended to
		l.mtx.Unlock()
		select {
		case peer := <-wait:
			return peer

		case <-ctx.Done():
			return ""
		}
	}

	peer := l.peers[0]
	l.peers = l.peers[1:]
	l.taken++
	l.mtx.Unlock()
	return peer
}

// Availability samples together the peers the list holds and the peers it has
// handed out, so a caller can observe both without either changing in between.
func (l *peerList) Availability() (available int, taken uint64) {
	l.mtx.Lock()
	defer l.mtx.Unlock()
	return len(l.peers), l.taken
}

func (l *peerList) Append(peer types.NodeID) {
	l.mtx.Lock()
	defer l.mtx.Unlock()
	if len(l.waiting) > 0 {
		wait := l.waiting[0]
		l.waiting = l.waiting[1:]
		l.taken++
		wait <- peer
		close(wait)
	} else {
		l.peers = append(l.peers, peer)
	}
}

func (l *peerList) Remove(peer types.NodeID) {
	l.mtx.Lock()
	defer l.mtx.Unlock()
	for i, p := range l.peers {
		if p == peer {
			l.peers = append(l.peers[:i], l.peers[i+1:]...)
			return
		}
	}
}

func (l *peerList) All() []types.NodeID {
	l.mtx.Lock()
	defer l.mtx.Unlock()
	return append([]types.NodeID(nil), l.peers...)
}

func (l *peerList) Contains(id types.NodeID) bool {
	l.mtx.Lock()
	defer l.mtx.Unlock()

	for _, p := range l.peers {
		if id == p {
			return true
		}
	}

	return false
}
