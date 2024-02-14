package p2p

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"runtime"
	"time"

	sync "github.com/sasha-s/go-deadlock"

	"github.com/gogo/protobuf/proto"

	"github.com/dashpay/tenderdash/crypto"
	tmstrings "github.com/dashpay/tenderdash/internal/libs/strings"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/service"
	"github.com/dashpay/tenderdash/proto/tendermint/p2p"
	"github.com/dashpay/tenderdash/types"
)

const queueBufferDefault = 32

// RouterOptions specifies options for a Router.
type RouterOptions struct {
	// ResolveTimeout is the timeout for resolving NodeAddress URLs.
	// 0 means no timeout.
	ResolveTimeout time.Duration

	// DialTimeout is the timeout for dialing a peer. 0 means no timeout.
	DialTimeout time.Duration

	// HandshakeTimeout is the timeout for handshaking with a peer. 0 means
	// no timeout.
	HandshakeTimeout time.Duration

	// QueueType must be, "priority", or "fifo". Defaults to
	// "fifo".
	QueueType string

	// MaxIncomingConnectionAttempts rate limits the number of incoming connection
	// attempts per IP address. Defaults to 100.
	MaxIncomingConnectionAttempts uint

	// IncomingConnectionWindow describes how often an IP address
	// can attempt to create a new connection. Defaults to 10
	// milliseconds, and cannot be less than 1 millisecond.
	IncomingConnectionWindow time.Duration

	// FilterPeerByIP is used by the router to inject filtering
	// behavior for new incoming connections. The router passes
	// the remote IP of the incoming connection the port number as
	// arguments. Functions should return an error to reject the
	// peer.
	FilterPeerByIP func(context.Context, net.IP, uint16) error

	// FilterPeerByID is used by the router to inject filtering
	// behavior for new incoming connections. The router passes
	// the NodeID of the node before completing the connection,
	// but this occurs after the handshake is complete. Filter by
	// IP address to filter before the handshake. Functions should
	// return an error to reject the peer.
	FilterPeerByID func(context.Context, types.NodeID) error

	// NumConcrruentDials controls how many parallel go routines
	// are used to dial peers. This defaults to the value of
	// runtime.NumCPU.
	NumConcurrentDials func() int
}

const (
	queueTypeFifo           = "fifo"
	queueTypePriority       = "priority"
	queueTypeSimplePriority = "simple-priority"
)

// Validate validates router options.
func (o *RouterOptions) Validate() error {
	switch o.QueueType {
	case "":
		o.QueueType = queueTypeFifo
	case queueTypeFifo, queueTypePriority, queueTypeSimplePriority:
		// pass
	default:
		return fmt.Errorf("queue type %q is not supported", o.QueueType)
	}

	switch {
	case o.IncomingConnectionWindow == 0:
		o.IncomingConnectionWindow = 100 * time.Millisecond
	case o.IncomingConnectionWindow < time.Millisecond:
		return fmt.Errorf("incomming connection window must be grater than 1m [%s]",
			o.IncomingConnectionWindow)
	}

	if o.MaxIncomingConnectionAttempts == 0 {
		o.MaxIncomingConnectionAttempts = 100
	}

	return nil
}

// Router manages peer connections and routes messages between peers and reactor
// channels. It takes a PeerManager for peer lifecycle management (e.g. which
// peers to dial and when) and a set of Transports for connecting and
// communicating with peers.
//
// On startup, three main goroutines are spawned to maintain peer connections:
//
//	dialPeers(): in a loop, calls PeerManager.DialNext() to get the next peer
//	address to dial and spawns a goroutine that dials the peer, handshakes
//	with it, and begins to route messages if successful.
//
//	acceptPeers(): in a loop, waits for an inbound connection via
//	Transport.Accept() and spawns a goroutine that handshakes with it and
//	begins to route messages if successful.
//
//	evictPeers(): in a loop, calls PeerManager.EvictNext() to get the next
//	peer to evict, and disconnects it by closing its message queue.
//
// When a peer is connected, an outbound peer message queue is registered in
// peerQueues, and routePeer() is called to spawn off two additional goroutines:
//
//	sendPeer(): waits for an outbound message from the peerQueues queue,
//	marshals it, and passes it to the peer transport which delivers it.
//
//	receivePeer(): waits for an inbound message from the peer transport,
//	unmarshals it, and passes it to the appropriate inbound channel queue
//	in channelQueues.
//
// When a reactor opens a channel via OpenChannel, an inbound channel message
// queue is registered in channelQueues, and a channel goroutine is spawned:
//
//	routeChannel(): waits for an outbound message from the channel, looks
//	up the recipient peer's outbound message queue in peerQueues, and submits
//	the message to it.
//
// All channel sends in the router are blocking. It is the responsibility of the
// queue interface in peerQueues and channelQueues to prioritize and drop
// messages as appropriate during contention to prevent stalls and ensure good
// quality of service.
type Router struct {
	*service.BaseService
	logger log.Logger

	metrics *Metrics
	lc      *metricsLabelCache

	options     RouterOptions
	privKey     crypto.PrivKey
	peerManager *PeerManager
	chDescs     map[ChannelID]*ChannelDescriptor
	transport   Transport
	endpoint    *Endpoint
	connTracker connectionTracker

	peerMtx    sync.RWMutex
	peerQueues map[types.NodeID]queue // outbound messages per peer for all channels
	// the channels that the peer queue has open
	peerChannels     map[types.NodeID]ChannelIDSet
	queueFactory     func(int) queue
	nodeInfoProducer func() *types.NodeInfo

	// FIXME: We don't strictly need to use a mutex for this if we seal the
	// channels on router start. This depends on whether we want to allow
	// dynamic channels in the future.
	channelMtx    sync.RWMutex
	channelQueues map[ChannelID]queue // inbound messages from all peers to a single channel
}

// NewRouter creates a new Router. The given Transports must already be
// listening on appropriate interfaces, and will be closed by the Router when it
// stops.
func NewRouter(
	logger log.Logger,
	metrics *Metrics,
	privKey crypto.PrivKey,
	peerManager *PeerManager,
	nodeInfoProducer func() *types.NodeInfo,
	transport Transport,
	endpoint *Endpoint,
	options RouterOptions,
) (*Router, error) {

	if err := options.Validate(); err != nil {
		return nil, err
	}

	router := &Router{
		logger:           logger,
		metrics:          metrics,
		lc:               newMetricsLabelCache(),
		privKey:          privKey,
		nodeInfoProducer: nodeInfoProducer,
		connTracker: newConnTracker(
			options.MaxIncomingConnectionAttempts,
			options.IncomingConnectionWindow,
		),
		chDescs:       make(map[ChannelID]*ChannelDescriptor, 0),
		transport:     transport,
		endpoint:      endpoint,
		peerManager:   peerManager,
		options:       options,
		channelQueues: map[ChannelID]queue{},
		peerQueues:    map[types.NodeID]queue{},
		peerChannels:  make(map[types.NodeID]ChannelIDSet),
	}

	router.BaseService = service.NewBaseService(logger, "router", router)

	return router, nil
}

func (r *Router) createQueueFactory(ctx context.Context) (func(int) queue, error) {
	switch r.options.QueueType {
	case queueTypeFifo:
		return newFIFOQueue, nil

	case queueTypePriority:
		return func(size int) queue {
			if size%2 != 0 {
				size++
			}

			q := newPQScheduler(r.logger, r.metrics, r.lc, r.chDescs, uint(size)/2, uint(size)/2, defaultCapacity)
			q.start(ctx)
			return q
		}, nil

	case queueTypeSimplePriority:
		return func(size int) queue { return newSimplePriorityQueue(ctx, size) }, nil

	default:
		return nil, fmt.Errorf("cannot construct queue of type %q", r.options.QueueType)
	}
}

// ChannelCreator allows routers to construct their own channels,
// either by receiving a reference to Router.OpenChannel or using some
// kind shim for testing purposes.
type ChannelCreator func(context.Context, *ChannelDescriptor) (Channel, error)

// OpenChannel opens a new channel for the given message type. The caller must
// close the channel when done, before stopping the Router. messageType is the
// type of message passed through the channel (used for unmarshaling), which can
// implement Wrapper to automatically (un)wrap multiple message types in a
// wrapper message. The caller may provide a size to make the channel buffered,
// which internally makes the inbound, outbound, and error channel buffered.
func (r *Router) OpenChannel(ctx context.Context, chDesc *ChannelDescriptor) (Channel, error) {
	r.channelMtx.Lock()
	defer r.channelMtx.Unlock()

	id := chDesc.ID
	if _, ok := r.channelQueues[id]; ok {
		return nil, fmt.Errorf("channel %v already exists", id)
	}
	r.chDescs[id] = chDesc

	queue := r.queueFactory(chDesc.RecvBufferCapacity)
	outCh := make(chan Envelope, chDesc.RecvBufferCapacity)
	errCh := make(chan PeerError, chDesc.RecvBufferCapacity)
	channel := NewChannel(chDesc.ID, chDesc.Name, queue.dequeue(), outCh, errCh)
	if chDesc.SendRateLimit > 0 {
		channel = NewThrottledChannel(channel, chDesc.SendRateLimit, chDesc.SendRateBurst)
	}

	r.channelQueues[id] = queue

	// add the channel to the nodeInfo if it's not already there.
	r.nodeInfoProducer().AddChannel(uint16(chDesc.ID))

	r.transport.AddChannelDescriptors([]*ChannelDescriptor{chDesc})

	go func() {
		defer func() {
			r.channelMtx.Lock()
			delete(r.channelQueues, id)
			r.channelMtx.Unlock()
			queue.close()
		}()

		r.routeChannel(ctx, chDesc.ID, outCh, errCh)
	}()

	return channel, nil
}

// routeChannel receives outbound channel messages and routes them to the
// appropriate peer. It also receives peer errors and reports them to the peer
// manager. It returns when either the outbound channel or error channel is
// closed, or the Router is stopped. wrapper is an optional message wrapper
// for messages, see Wrapper for details.
func (r *Router) routeChannel(
	ctx context.Context,
	chID ChannelID,
	outCh <-chan Envelope,
	errCh <-chan PeerError,
) {
	for {
		select {
		case envelope := <-outCh:
			if envelope.IsZero() {
				continue
			}
			// Mark the envelope with the channel ID to allow sendPeer() to pass
			// it on to Transport.SendMessage().
			envelope.ChannelID = chID

			// collect peer queues to pass the message via
			var queues []queue
			if envelope.Broadcast {
				r.peerMtx.RLock()

				queues = make([]queue, 0, len(r.peerQueues))
				for nodeID, q := range r.peerQueues {
					peerChs := r.peerChannels[nodeID]

					// check whether the peer is receiving on that channel
					if _, ok := peerChs[chID]; ok {
						queues = append(queues, q)
					}
				}

				r.peerMtx.RUnlock()
			} else {
				r.peerMtx.RLock()

				q, ok := r.peerQueues[envelope.To]
				contains := false
				if ok {
					peerChs := r.peerChannels[envelope.To]

					// check whether the peer is receiving on that channel
					_, contains = peerChs[chID]
				}
				r.peerMtx.RUnlock()

				if !ok {
					r.logger.Debug("dropping message for unconnected peer", "peer", envelope.To, "channel", chID)
					continue
				}

				if !contains {
					// reactor tried to send a message across a channel that the
					// peer doesn't have available. This is a known issue due to
					// how peer subscriptions work:
					// https://github.com/tendermint/tendermint/issues/6598
					continue
				}

				queues = []queue{q}
			}

			// send message to peers
			for _, q := range queues {
				start := time.Now().UTC()

				select {
				case q.enqueue() <- envelope:
					r.metrics.RouterPeerQueueSend.Observe(time.Since(start).Seconds())

				case <-q.closed():
					r.logger.Debug("dropping message on closed channel", "peer", envelope.To, "channel", chID)

				case <-ctx.Done():
					return
				}
			}

		case peerError := <-errCh:
			maxPeerCapacity := r.peerManager.HasMaxPeerCapacity()
			r.logger.Error("peer error",
				"peer", peerError.NodeID,
				"err", peerError.Err,
				"disconnecting", peerError.Fatal || maxPeerCapacity,
			)

			if peerError.Fatal || maxPeerCapacity {
				// if the error is fatal or all peer
				// slots are in use, we can error
				// (disconnect) from the peer.
				r.peerManager.Errored(peerError.NodeID, peerError.Err)
			} else {
				// this just decrements the peer
				// score.
				r.peerManager.processPeerEvent(ctx, PeerUpdate{
					NodeID: peerError.NodeID,
					Status: PeerStatusBad,
				})
			}

		case <-ctx.Done():
			return
		}
	}
}

func (r *Router) numConcurrentDials() int {
	if r.options.NumConcurrentDials == nil {
		return runtime.NumCPU() * 32
	}

	return r.options.NumConcurrentDials()
}

func (r *Router) filterPeersIP(ctx context.Context, ip net.IP, port uint16) error {
	if r.options.FilterPeerByIP == nil {
		return nil
	}

	return r.options.FilterPeerByIP(ctx, ip, port)
}

func (r *Router) filterPeersID(ctx context.Context, id types.NodeID) error {
	if r.options.FilterPeerByID == nil {
		return nil
	}

	return r.options.FilterPeerByID(ctx, id)
}

// acceptPeers accepts inbound connections from peers on the given transport,
// and spawns goroutines that route messages to/from them.
func (r *Router) acceptPeers(ctx context.Context, transport Transport) {
	for {
		conn, err := transport.Accept(ctx)
		switch {
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			r.logger.Debug("stopping accept routine", "transport", transport, "err", "context canceled")
			return
		case errors.Is(err, io.EOF):
			r.logger.Debug("stopping accept routine", "transport", transport, "err", "EOF")
			return
		case err != nil:
			// in this case we got an error from the net.Listener.
			r.logger.Error("failed to accept connection", "transport", transport, "err", err)
			continue
		}

		incomingIP := conn.RemoteEndpoint().IP
		if err := r.connTracker.AddConn(incomingIP); err != nil {
			closeErr := conn.Close()
			r.logger.Debug("rate limiting incoming peer",
				"err", err,
				"ip", tmstrings.LazyStringer(incomingIP),
				"close_err", closeErr,
			)

			continue
		}

		// Spawn a goroutine for the handshake, to avoid head-of-line blocking.
		go r.openConnection(ctx, conn)

	}
}

func (r *Router) openConnection(ctx context.Context, conn Connection) {
	defer conn.Close()
	defer r.connTracker.RemoveConn(conn.RemoteEndpoint().IP)

	re := conn.RemoteEndpoint()
	incomingIP := re.IP

	if err := r.filterPeersIP(ctx, incomingIP, re.Port); err != nil {
		r.logger.Debug("peer filtered by IP", "ip", incomingIP.String(), "err", err)
		return
	}

	// FIXME: The peer manager may reject the peer during Accepted()
	// after we've handshaked with the peer (to find out which peer it
	// is). However, because the handshake has no ack, the remote peer
	// will think the handshake was successful and start sending us
	// messages.
	//
	// This can cause problems in tests, where a disconnection can cause
	// the local node to immediately redial, while the remote node may
	// not have completed the disconnection yet and therefore reject the
	// reconnection attempt (since it thinks we're still connected from
	// before).
	//
	// The Router should do the handshake and have a final ack/fail
	// message to make sure both ends have accepted the connection, such
	// that it can be coordinated with the peer manager.
	peerInfo, err := r.handshakePeer(ctx, conn, "")
	switch {
	case errors.Is(err, context.Canceled):
		return
	case err != nil:
		r.logger.Error("peer handshake failed", "endpoint", conn, "err", err)
		return
	}
	if err := r.filterPeersID(ctx, peerInfo.NodeID); err != nil {
		r.logger.Debug("peer filtered by node ID", "node", peerInfo.NodeID, "err", err)
		return
	}

	err = r.peerManager.Accepted(peerInfo.NodeID, SetProTxHashToPeerInfo(peerInfo.ProTxHash))
	if err != nil {
		r.logger.Error("failed to accept connection",
			"op", "incoming/accepted", "peer", peerInfo.NodeID, "err", err)
		return
	}

	r.routePeer(ctx, peerInfo.NodeID, conn, toChannelIDs(peerInfo.Channels.ToSlice()))
}

// dialPeers maintains outbound connections to peers by dialing them.
func (r *Router) dialPeers(ctx context.Context) {
	addresses := make(chan NodeAddress)
	wg := &sync.WaitGroup{}

	// Start a limited number of goroutines to dial peers in
	// parallel. the goal is to avoid starting an unbounded number
	// of goroutines thereby spamming the network, but also being
	// able to add peers at a reasonable pace, though the number
	// is somewhat arbitrary. The action is further throttled by a
	// sleep after sending to the addresses channel.
	for i := 0; i < r.numConcurrentDials(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for {
				select {
				case <-ctx.Done():
					return
				case address := <-addresses:
					r.connectPeer(ctx, address)
				}
			}
		}()
	}

LOOP:
	for {
		address, err := r.peerManager.DialNext(ctx)
		switch {
		case errors.Is(err, context.Canceled):
			break LOOP
		case address == NodeAddress{}:
			continue LOOP
		}

		select {
		case addresses <- address:
			continue LOOP
		case <-ctx.Done():
			close(addresses)
			break LOOP
		}
	}

	wg.Wait()
}

func (r *Router) connectPeer(ctx context.Context, address NodeAddress) {
	conn, err := r.dialPeer(ctx, address)
	switch {
	case errors.Is(err, context.Canceled):
		return
	case err != nil:
		r.logger.Debug("failed to dial peer", "peer", address, "err", err)
		if err = r.peerManager.DialFailed(ctx, address); err != nil {
			r.logger.Error("failed to report dial failure", "peer", address, "err", err)
		}
		return
	}

	peerInfo, err := r.handshakePeer(ctx, conn, address.NodeID)
	switch {
	case errors.Is(err, context.Canceled):
		conn.Close()
		return
	case err != nil:
		r.logger.Error("failed to handshake with peer", "peer", address, "err", err)
		if err = r.peerManager.DialFailed(ctx, address); err != nil {
			r.logger.Error("failed to report dial failure", "peer", address, "err", err)
		}
		conn.Close()
		return
	}

	err = r.peerManager.Dialed(address, SetProTxHashToPeerInfo(peerInfo.ProTxHash))
	if err != nil {
		r.logger.Error("failed to dial peer", "op", "outgoing/dialing", "peer", address.NodeID, "err", err)
		r.peerManager.dialWaker.Wake()
		conn.Close()
		return
	}

	// routePeer (also) calls connection close
	go r.routePeer(ctx, address.NodeID, conn, toChannelIDs(peerInfo.Channels.ToSlice()))
}

func (r *Router) getOrMakeQueue(peerID types.NodeID, channels ChannelIDSet) queue {
	r.peerMtx.Lock()
	defer r.peerMtx.Unlock()

	if peerQueue, ok := r.peerQueues[peerID]; ok {
		return peerQueue
	}

	peerQueue := r.queueFactory(queueBufferDefault)
	r.peerQueues[peerID] = peerQueue
	r.peerChannels[peerID] = channels
	return peerQueue
}

// dialPeer connects to a peer by dialing it.
func (r *Router) dialPeer(ctx context.Context, address NodeAddress) (Connection, error) {
	resolveCtx := ctx
	if r.options.ResolveTimeout > 0 {
		var cancel context.CancelFunc
		resolveCtx, cancel = context.WithTimeout(resolveCtx, r.options.ResolveTimeout)
		defer cancel()
	}

	r.logger.Debug("resolving peer address", "peer", address)
	endpoints, err := address.Resolve(resolveCtx)
	switch {
	case err != nil:
		return nil, fmt.Errorf("failed to resolve address %q: %w", address, err)
	case len(endpoints) == 0:
		return nil, fmt.Errorf("address %q did not resolve to any endpoints", address)
	}

	for _, endpoint := range endpoints {
		dialCtx := ctx
		if r.options.DialTimeout > 0 {
			var cancel context.CancelFunc
			dialCtx, cancel = context.WithTimeout(dialCtx, r.options.DialTimeout)
			defer cancel()
		}

		// FIXME: When we dial and handshake the peer, we should pass it
		// appropriate address(es) it can use to dial us back. It can't use our
		// remote endpoint, since TCP uses different port numbers for outbound
		// connections than it does for inbound. Also, we may need to vary this
		// by the peer's endpoint, since e.g. a peer on 192.168.0.0 can reach us
		// on a private address on this endpoint, but a peer on the public
		// Internet can't and needs a different public address.
		conn, err := r.transport.Dial(dialCtx, endpoint)
		if err != nil {
			r.logger.Debug("failed to dial endpoint", "peer", address.NodeID, "endpoint", endpoint, "err", err)
		} else {
			r.logger.Debug("dialed peer", "peer", address.NodeID, "endpoint", endpoint)
			return conn, nil
		}
	}

	return nil, errors.New("all endpoints failed")
}

// handshakePeer handshakes with a peer, validating the peer's information. If
// expectID is given, we check that the peer's info matches it.
func (r *Router) handshakePeer(
	ctx context.Context,
	conn Connection,
	expectID types.NodeID,
) (types.NodeInfo, error) {

	nodeInfo := r.nodeInfoProducer()
	peerInfo, peerKey, err := conn.Handshake(ctx, r.options.HandshakeTimeout, *nodeInfo, r.privKey)
	if err != nil {
		return peerInfo, err
	}
	if err = peerInfo.Validate(); err != nil {
		return peerInfo, fmt.Errorf("invalid handshake NodeInfo: %w", err)
	}

	if types.NodeIDFromPubKey(peerKey) != peerInfo.NodeID {
		return peerInfo, fmt.Errorf("peer's public key did not match its node ID %q (expected %q)",
			peerInfo.NodeID, types.NodeIDFromPubKey(peerKey))
	}
	if expectID != "" && expectID != peerInfo.NodeID {
		return peerInfo, fmt.Errorf("expected to connect with peer %q, got %q",
			expectID, peerInfo.NodeID)
	}

	if err := nodeInfo.CompatibleWith(peerInfo); err != nil {
		if err := r.peerManager.Inactivate(peerInfo.NodeID); err != nil {
			return peerInfo, fmt.Errorf("problem inactivating peer %q: %w", peerInfo.ID(), err)
		}

		return peerInfo, ErrRejected{
			err:            err,
			id:             peerInfo.ID(),
			isIncompatible: true,
		}
	}
	return peerInfo, nil
}

// routePeer routes inbound and outbound messages between a peer and the reactor
// channels. It will close the given connection and send queue when done, or if
// they are closed elsewhere it will cause this method to shut down and return.
func (r *Router) routePeer(ctx context.Context, peerID types.NodeID, conn Connection, channels ChannelIDSet) {
	r.metrics.PeersConnected.Add(1)
	r.peerManager.Ready(ctx, peerID, channels)

	// we use context to manage the lifecycle of the peer
	// note that original ctx will be used in cleanup
	ioCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	sendQueue := r.getOrMakeQueue(peerID, channels)
	defer func() {
		r.logger.Debug("routePeer cleanup: terminated peer", "peer", peerID)
		r.peerMtx.Lock()
		delete(r.peerQueues, peerID)
		delete(r.peerChannels, peerID)
		r.peerMtx.Unlock()
		r.logger.Debug("routePeer cleanup: deleted peer queues", "peer", peerID)

		_ = conn.Close()
		sendQueue.close()
		r.logger.Debug("routePeer cleanup: closed send queue", "peer", peerID)

		r.peerManager.Disconnected(ctx, peerID)
		r.logger.Debug("routePeer cleanup: peer marked as disconnected", "peer", peerID)
		r.metrics.PeersConnected.Add(-1)
	}()

	r.logger.Info("peer connected", "peer", peerID, "endpoint", conn)

	errCh := make(chan error, 2)
	wg := sync.WaitGroup{}
	wg.Add(2)

	go func() {
		select {
		case errCh <- r.receivePeer(ioCtx, peerID, conn):
		case <-ioCtx.Done():
		}
		wg.Done()
	}()

	go func() {
		select {
		case errCh <- r.sendPeer(ioCtx, peerID, conn, sendQueue):
		case <-ioCtx.Done():
		}
		wg.Done()
	}()

	// wait for error from first goroutine

	var (
		err    error
		ctxErr error
	)

	select {
	case err = <-errCh:
		r.logger.Debug("routePeer: received error from subroutine 1", "peer", peerID, "err", err)
	case <-ioCtx.Done():
		r.logger.Debug("routePeer: ctx done", "peer", peerID)
		ctxErr = ioCtx.Err()
	}

	// goroutine 1 has finished, so we can cancel the context and close everything
	cancel()
	_ = conn.Close()
	sendQueue.close()

	r.logger.Debug("routePeer: closed conn and send queue, waiting for all goroutines to finish", "peer", peerID, "err", err)
	wg.Wait()
	r.logger.Debug("routePeer: all goroutines finished", "peer", peerID, "err", err)

	// Drain the error channel; these should typically not be interesting
FOR:
	for i := 0; ; i++ {
		select {
		case e := <-errCh:
			r.logger.Debug("routePeer: received error when draining errCh", "peer", peerID, "err", e)
			// if we received non-context error, we should return it
			if err == nil && !errors.Is(e, context.Canceled) && !errors.Is(e, context.DeadlineExceeded) {
				err = e
			}
		default:
			break FOR
		}
	}
	close(errCh)

	// if the context was canceled, and no other error received on errCh
	if err == nil {
		err = ctxErr
	}

	switch err {
	case nil, io.EOF:
		r.logger.Info("peer disconnected", "peer", peerID, "endpoint", conn)
	default:
		r.logger.Error("peer failure", "peer", peerID, "endpoint", conn, "err", err)
	}
}

// receivePeer receives inbound messages from a peer, deserializes them and
// passes them on to the appropriate channel.
func (r *Router) receivePeer(ctx context.Context, peerID types.NodeID, conn Connection) error {
	// default timeout; by default, we set it to ~ 10 years so that it will practically never fire
	const DefaultTimeout = 24 * 30 * 12 * 10 * time.Hour
	timeout := time.NewTimer(DefaultTimeout)
	defer timeout.Stop()

	for {
		chID, bz, err := conn.ReceiveMessage(ctx)
		if err != nil {
			return err
		}

		r.channelMtx.RLock()
		queue, queueOk := r.channelQueues[chID]
		chDesc, chDescOk := r.chDescs[chID]
		r.channelMtx.RUnlock()

		if !queueOk || !chDescOk {
			r.logger.Debug("dropping message for unknown channel",
				"peer", peerID, "channel", chID,
				"queue", queueOk, "chDesc", chDescOk)
			continue
		}

		var protoEnvelope p2p.Envelope
		if err := proto.Unmarshal(bz, &protoEnvelope); err != nil {
			r.logger.Error("message decoding failed, dropping message", "peer", peerID, "err", err)
			continue
		}
		start := time.Now().UTC()
		envelope, err := EnvelopeFromProto(protoEnvelope)
		if err != nil {
			r.logger.Error("message decoding failed, dropping message", "peer", peerID, "err", err)
			continue
		}
		envelope.From = peerID
		envelope.ChannelID = chID

		timeout.Stop()
		select {
		case <-timeout.C:
		default:
		}

		if chDesc.EnqueueTimeout > 0 {
			timeout.Reset(chDesc.EnqueueTimeout)
		} else {
			timeout.Reset(DefaultTimeout)
		}

		select {
		case queue.enqueue() <- envelope:
			r.metrics.PeerReceiveBytesTotal.With(
				"chID", fmt.Sprint(chID),
				"peer_id", string(peerID),
				"message_type", r.lc.ValueToMetricLabel(envelope.Message)).Add(float64(proto.Size(&protoEnvelope)))
			r.metrics.RouterChannelQueueSend.Observe(time.Since(start).Seconds())
			// r.logger.Debug("received message", "peer", peerID, "msg", msg)

		case <-queue.closed():
			r.logger.Debug("channel closed, dropping message", "peer", peerID, "channel", chID)

		case <-timeout.C:
			// TODO change to Trace
			r.logger.Debug("dropping message from peer due to enqueue timeout",
				"peer", peerID,
				"channel", chID,
				"channel_name", chDesc.Name,
				"timeout", chDesc.EnqueueTimeout.String(),
				"type", reflect.TypeOf((envelope.Message)).Name(),
			)

		case <-ctx.Done():
			r.logger.Debug("receivePeer: ctx is done", "peer", peerID, "channel", chID)
			return nil
		}
	}
}

// sendPeer sends queued messages to a peer.
func (r *Router) sendPeer(ctx context.Context, peerID types.NodeID, conn Connection, peerQueue queue) error {
	for {
		start := time.Now().UTC()

		select {
		case envelope := <-peerQueue.dequeue():
			r.metrics.RouterPeerQueueRecv.Observe(time.Since(start).Seconds())
			if envelope.Message == nil {
				r.logger.Error("dropping nil message", "peer", peerID)
				continue
			}

			protoEnvelope, err := envelope.ToProto()
			if err != nil {
				r.logger.Error("failed to marshal message", "peer", peerID, "err", err)
				continue
			}
			bz, err := proto.Marshal(protoEnvelope)
			if err != nil {
				r.logger.Error("failed to marshal message", "peer", peerID, "err", err)
				continue
			}

			if err = conn.SendMessage(ctx, envelope.ChannelID, bz); err != nil {
				return err
			}

			// r.logger.Debug("sent message", "peer", envelope.To, "message", envelope.Message)

		case <-peerQueue.closed():
			return nil

		case <-ctx.Done():
			return nil
		}
	}
}

// evictPeers evicts connected peers as requested by the peer manager.
func (r *Router) evictPeers(ctx context.Context) {
	for {
		peerID, err := r.peerManager.EvictNext(ctx)

		switch {
		case errors.Is(err, context.Canceled):
			return
		case err != nil:
			r.logger.Error("failed to find next peer to evict", "err", err)
			return
		}

		r.logger.Info("evicting peer", "peer", peerID)

		r.peerMtx.RLock()
		queue, ok := r.peerQueues[peerID]
		r.peerMtx.RUnlock()

		r.metrics.PeersEvicted.Add(1)

		if ok {
			queue.close()
		}
	}
}

func (r *Router) setupQueueFactory(ctx context.Context) error {
	qf, err := r.createQueueFactory(ctx)
	if err != nil {
		return err
	}

	r.queueFactory = qf
	return nil
}

// OnStart implements service.Service.
func (r *Router) OnStart(ctx context.Context) error {
	if err := r.setupQueueFactory(ctx); err != nil {
		return err
	}

	if err := r.transport.Listen(r.endpoint); err != nil {
		return err
	}

	go r.dialPeers(ctx)
	go r.evictPeers(ctx)
	go r.acceptPeers(ctx, r.transport)

	return nil
}

// OnStop implements service.Service.
//
// All channels must be closed by OpenChannel() callers before stopping the
// router, to prevent blocked channel sends in reactors. Channels are not closed
// here, since that would cause any reactor senders to panic, so it is the
// sender's responsibility.
func (r *Router) OnStop() {
	// Close transport listeners (unblocks Accept calls).
	if err := r.transport.Close(); err != nil {
		r.logger.Error("failed to close transport", "err", err)
	}

	// Collect all remaining queues, and wait for them to close.
	queues := []queue{}

	r.channelMtx.RLock()
	for _, q := range r.channelQueues {
		queues = append(queues, q)
	}
	r.channelMtx.RUnlock()

	r.peerMtx.RLock()
	for _, q := range r.peerQueues {
		queues = append(queues, q)
	}
	r.peerMtx.RUnlock()

	for _, q := range queues {
		q.close()
		<-q.closed()
	}
}

type ChannelIDSet map[ChannelID]struct{}

func (cs ChannelIDSet) Contains(id ChannelID) bool {
	_, ok := cs[id]
	return ok
}

func toChannelIDs(ids []uint16) ChannelIDSet {
	c := make(map[ChannelID]struct{}, len(ids))
	for _, b := range ids {
		c[ChannelID(b)] = struct{}{}
	}
	return c
}
