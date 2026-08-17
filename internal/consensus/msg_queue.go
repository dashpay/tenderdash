package consensus

import (
	"context"
	"fmt"

	tmtime "github.com/dashpay/tenderdash/libs/time"
	"github.com/dashpay/tenderdash/types"
)

type msgEnvelope struct {
	msgInfo
	fromReplay bool
}

// msgHandlerFunc must be implemented by function to handle a state message
type msgHandlerFunc func(ctx context.Context, stateData *StateData, msg msgEnvelope) error

type msgMiddlewareFunc func(msgHandlerFunc) msgHandlerFunc

func withMiddleware(hd msgHandlerFunc, mws ...msgMiddlewareFunc) msgHandlerFunc {
	for _, mw := range mws {
		hd = mw(hd)
	}
	return hd
}

func msgFromReplay() func(envelope *msgEnvelope) {
	return func(envelope *msgEnvelope) {
		envelope.fromReplay = true
	}
}

type chanQueue[T any] struct {
	ch chan T
}

func newChanQueue[T Message]() *chanQueue[T] {
	return &chanQueue[T]{
		ch: make(chan T, msgQueueSize),
	}
}

func (q *chanQueue[T]) send(ctx context.Context, msg T) error {
	// first select tries to catch a signal from a context
	// second select sends a message o a channel, if a queue is full returns the error
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	select {
	case q.ch <- msg:
		return nil
	default:
		return fmt.Errorf("msg queue is full")
	}
}

func (q *chanQueue[T]) recv(ctx context.Context) (T, bool) {
	select {
	case msg := <-q.ch:
		return msg, true
	case <-ctx.Done():
		var zero T
		return zero, false
	}
}

// msgSource is one of the queues the reader drains into the consensus
// goroutine. Each source decides for itself in which order — and, for the peer
// side, at what pace — its messages are handed over.
type msgSource[T any] interface {
	recv(ctx context.Context) (T, bool)
}

// peerMsgQueue is the peer side of the state's message queue: the reactor puts
// peer messages in, the reader takes them out.
type peerMsgQueue interface {
	msgSource[msgInfo]
	send(ctx context.Context, msg msgInfo) error
}

// chanMsgSender routes a msgInfo either to internal or peer queue
// message routing based on peerID or boolean flag in context
// if peerID is passed or the parameter usePeerQueueCtx is true, then message will send through peer channel
// otherwise internal
//
// That every message carrying a peer is routed to the peer side is relied on
// elsewhere: the consensus goroutine reports a message as finished to the peer
// scheduler on exactly that condition, and the scheduler consults the
// verification budget again only once the message it handed over has been
// reported. A peer message reaching the internal queue would produce a report
// the scheduler was not waiting for, and it would then read the budget with a
// message's charges still ahead of it.
type chanMsgSender struct {
	internalQueue *chanQueue[msgInfo]
	peerQueue     peerMsgQueue
}

func (s *chanMsgSender) send(ctx context.Context, msg Message, peerID types.NodeID) error {
	mi := msgInfo{msg, peerID, tmtime.Now()}
	usePeerQueue := peerQueueFromCtx(ctx)
	ch := s.peerQueue
	if peerID == "" && !usePeerQueue {
		ch = s.internalQueue
	}
	return ch.send(ctx, mi)
}

type chanMsgReader[T any] struct {
	outCh    chan T
	quitCh   chan struct{}
	quitedCh chan struct{}
	sources  []msgSource[T]
}

func newChanMsgReader[T any](sources []msgSource[T]) *chanMsgReader[T] {
	return &chanMsgReader[T]{
		quitCh:   make(chan struct{}),
		quitedCh: make(chan struct{}),
		outCh:    make(chan T),
		sources:  sources,
	}
}

func (q *chanMsgReader[T]) stop() {
	close(q.quitCh)
	<-q.quitedCh
}

func (q *chanMsgReader[T]) readQueueMessages(ctx context.Context, source msgSource[T], quitedCh chan struct{}) {
	defer func() {
		quitedCh <- struct{}{}
	}()
	for {
		msg, ok := source.recv(ctx)
		if !ok {
			return
		}
		if !q.safeSend(ctx, msg) {
			return
		}
	}
}

func (q *chanMsgReader[T]) safeSend(ctx context.Context, msg T) (res bool) {
	res = false
	defer func() {
		_ = recover()
	}()
	select {
	case q.outCh <- msg:
		res = true
	case <-ctx.Done():
		return
	}
	return
}

func (q *chanMsgReader[T]) fanIn(ctx context.Context) {
	defer close(q.outCh)
	quitedChs := makeChs[struct{}](len(q.sources))
	ctx, cancel := context.WithCancel(ctx)
	for i, source := range q.sources {
		go func(source msgSource[T], quitedCh chan struct{}) {
			q.readQueueMessages(ctx, source, quitedCh)
		}(source, quitedChs[i])
	}
	// graceful stop reading messages
	<-q.quitCh
	cancel()
	for _, quitedCh := range quitedChs {
		<-quitedCh
	}
	q.quitedCh <- struct{}{}
}

type queueSender interface {
	send(ctx context.Context, msg Message, peerID types.NodeID) error
}

type msgInfoQueue struct {
	sender *chanMsgSender
	reader *chanMsgReader[msgInfo]
	lanes  *peerLanes
}

// newMsgInfoQueue builds the state's message queue: one arrival-ordered queue
// for this node's own messages, and per-peer lanes served in rotation for
// everything that arrives from peers.
//
// The two are read independently, so a peer path held up by the verification
// budget leaves this node's own messages — and its timeouts — unaffected.
func newMsgInfoQueue(opts ...peerLanesOptionFunc) *msgInfoQueue {
	internalQueue := newChanQueue[msgInfo]()
	lanes := newPeerLanes(opts...)
	return &msgInfoQueue{
		sender: &chanMsgSender{
			internalQueue: internalQueue,
			peerQueue:     lanes,
		},
		lanes:  lanes,
		reader: newChanMsgReader[msgInfo]([]msgSource[msgInfo]{internalQueue, lanes}),
	}
}

func (q *msgInfoQueue) send(ctx context.Context, msg Message, peerID types.NodeID) error {
	return q.sender.send(ctx, msg, peerID)
}

func (q *msgInfoQueue) read() <-chan msgInfo {
	return q.reader.outCh
}

func (q *msgInfoQueue) fanIn(ctx context.Context) {
	q.reader.fanIn(ctx)
}

func (q *msgInfoQueue) stop() {
	q.reader.stop()
}

// purgePeer retires a disconnected peer's lane.
func (q *msgInfoQueue) purgePeer(peerID types.NodeID) {
	q.lanes.purgePeer(peerID)
}

// admitPeer registers a peer's connection as live and returns the session that
// stands for it. The session must accompany the peer's messages for the
// scheduler to admit them, so a message from a connection that has since been
// purged cannot create or revive a lane.
func (q *msgInfoQueue) admitPeer(peerID types.NodeID) uint64 {
	return q.lanes.admit(peerID)
}

// settlePeerMsg reports that the consensus goroutine has finished with the peer
// message it was handed.
func (q *msgInfoQueue) settlePeerMsg() {
	q.lanes.settle()
}

type wrapWAL struct {
	getter func() WALWriteFlusher
}

func (w *wrapWAL) Write(msg WALMessage) error {
	return w.getter().Write(msg)
}

func (w *wrapWAL) WriteSync(msg WALMessage) error {
	if msg == nil {
		return nil
	}
	return w.getter().WriteSync(msg)
}

func (w *wrapWAL) FlushAndSync() error {
	return w.getter().FlushAndSync()
}

func makeChs[T any](n int) []chan T {
	chs := make([]chan T, n)
	for i := 0; i < n; i++ {
		chs[i] = make(chan T)
	}
	return chs
}
