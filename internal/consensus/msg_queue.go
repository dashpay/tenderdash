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

type msgHandlerFunc func(ctx context.Context, msg msgEnvelope) error

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
	ch  chan T
	wal WALWriter
}

func newChanQueue[T Message]() *chanQueue[T] {
	return &chanQueue[T]{
		ch: make(chan T, msgQueueSize),
	}
}

func (q *chanQueue[T]) receive(ctx context.Context) (T, error) {
	select {
	case msg := <-q.ch:
		return msg, nil
	case <-ctx.Done():
		var msg T
		return msg, ctx.Err()
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

type chanMsgSender struct {
	internalQueue *chanQueue[msgInfo]
	peerQueue     *chanQueue[msgInfo]
}

func (s *chanMsgSender) send(ctx context.Context, msg Message, peerID types.NodeID) error {
	mi := msgInfo{msg, peerID, tmtime.Now()}
	usePeerQueue := PeerQueueFromContext(ctx)
	ch := s.peerQueue
	if peerID == "" && usePeerQueue == false {
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

func (r *chanMsgReader[T]) stop() {
	close(r.quitCh)
	<-r.quitedCh
}

func (r *chanMsgReader[T]) readQueueMessages(ctx context.Context, queue *chanQueue[T], quitCh chan struct{}) {
	for {
		select {
		case msg := <-queue.ch:
			r.safeSend(ctx, msg, quitCh)
		case <-quitCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (r *chanMsgReader[T]) safeSend(ctx context.Context, msg T, quitCh <-chan struct{}) {
	defer func() {
		_ = recover()
	}()
	select {
	case r.outCh <- msg:
	case <-quitCh:
		return
	case <-ctx.Done():
		return
	}
}

func (r *chanMsgReader[T]) readMessages(ctx context.Context) {
	quitChs := makeChs[struct{}](len(r.queues))
	defer close(r.outCh)
	ctx, cancel := context.WithCancel(ctx)
	for i, queue := range r.queues {
		go func(queue *chanQueue[T], quitCh chan struct{}, i int) {
			r.readQueueMessages(ctx, queue, quitCh)
		}(queue, quitChs[i], i)
	}
	<-r.quitCh
	cancel()
	r.quitedCh <- struct{}{}
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
