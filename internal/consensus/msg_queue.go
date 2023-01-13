package consensus

import (
	"context"
	"fmt"

	tmtime "github.com/tendermint/tendermint/libs/time"
	"github.com/tendermint/tendermint/types"
)

var usePeerQueueCtx = struct{}{}

// ContextWithPeerQueue ...
func ContextWithPeerQueue(ctx context.Context) context.Context {
	return context.WithValue(ctx, usePeerQueueCtx, true)
}

// PeerQueueFromContext ...
func PeerQueueFromContext(ctx context.Context) bool {
	val := ctx.Value(usePeerQueueCtx)
	if val != nil {
		return val.(bool)
	}
	return false
}

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
	select {
	case <-ctx.Done():
		return ctx.Err()
	case q.ch <- msg:
		return nil
	default:
		return fmt.Errorf("msg queue is full")
	}
}

// chanMsgSender routes a msgInfo either to internal or peer queue
// message routing based on peerID or boolean flag in context
// if peerID is passed or the parameter usePeerQueueCtx is true, then message will send through peer channel
// otherwise internal
type chanMsgSender struct {
	internalQueue *chanQueue[msgInfo]
	peerQueue     *chanQueue[msgInfo]
}

func (s *chanMsgSender) send(ctx context.Context, msg Message, peerID types.NodeID) error {
	mi := msgInfo{msg, peerID, tmtime.Now()}
	usePeerQueue := PeerQueueFromContext(ctx)
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
	queues   []*chanQueue[T]
}

func newChanMsgReader[T any](queues []*chanQueue[T]) *chanMsgReader[T] {
	return &chanMsgReader[T]{
		quitCh:   make(chan struct{}),
		quitedCh: make(chan struct{}),
		outCh:    make(chan T),
		queues:   queues,
	}
}

func (q *chanMsgReader[T]) stop() {
	close(q.quitCh)
	<-q.quitedCh
}

func (q *chanMsgReader[T]) readQueueMessages(ctx context.Context, queue *chanQueue[T], quitedCh chan struct{}) {
	defer func() {
		quitedCh <- struct{}{}
	}()
	for {
		select {
		case msg := <-queue.ch:
			flag := q.safeSend(ctx, msg)
			if !flag {
				return
			}
		case <-ctx.Done():
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
	quitedChs := makeChs[struct{}](len(q.queues))
	ctx, cancel := context.WithCancel(ctx)
	for i, queue := range q.queues {
		go func(queue *chanQueue[T], quitedCh chan struct{}) {
			q.readQueueMessages(ctx, queue, quitedCh)
		}(queue, quitedChs[i])
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
}

func newMsgInfoQueue() *msgInfoQueue {
	internalQueue := newChanQueue[msgInfo]()
	peerQueue := newChanQueue[msgInfo]()
	return &msgInfoQueue{
		sender: &chanMsgSender{
			internalQueue: internalQueue,
			peerQueue:     peerQueue,
		},
		reader: newChanMsgReader[msgInfo]([]*chanQueue[msgInfo]{internalQueue, peerQueue}),
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
