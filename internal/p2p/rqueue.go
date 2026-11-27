package p2p

import (
	"container/heap"
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/cosmos/gogoproto/proto"
)

// Allow bursts while bounding heap memory and trimming work by buffer size.
const simpleQueueCapacityMultiplier = 16

type simpleQueue struct {
	input   chan Envelope
	output  chan Envelope
	closeFn func()
	closeCh chan struct{}

	maxSize    int
	metrics    *Metrics
	priorityFn func(ChannelID) uint
}

func newSimplePriorityQueue(ctx context.Context, size int, metrics *Metrics, priorityFn func(ChannelID) uint) *simpleQueue {
	maxSize := size * simpleQueueCapacityMultiplier
	if size%2 != 0 {
		size++
	}

	ctx, cancel := context.WithCancel(ctx)
	q := &simpleQueue{
		input:      make(chan Envelope, size*2),
		output:     make(chan Envelope, size/2),
		maxSize:    maxSize,
		metrics:    metrics,
		priorityFn: priorityFn,
		closeCh:    make(chan struct{}),
		closeFn:    cancel,
	}

	go q.run(ctx)
	return q
}

func (q *simpleQueue) enqueue() chan<- Envelope { return q.input }
func (q *simpleQueue) dequeue() <-chan Envelope { return q.output }
func (q *simpleQueue) close()                   { q.closeFn() }
func (q *simpleQueue) closed() <-chan struct{}  { return q.closeCh }

func (q *simpleQueue) run(ctx context.Context) {
	defer close(q.closeCh)
	defer q.closeFn()

	pq := make(priorityQueue, 0, q.maxSize)
	heap.Init(&pq)
	for {
		// Sending on a nil channel blocks forever, so the send case stays disabled
		// while the heap is empty.
		var out chan<- Envelope
		var next Envelope
		if len(pq) > 0 {
			out, next = q.output, pq[0].envelope
		}

		select {
		case <-ctx.Done():
			return
		case e := <-q.input:
			// enqueue the incoming Envelope
			heap.Push(&pq, &pqEnvelope{
				envelope:  e,
				size:      uint(proto.Size(e.Message)),
				priority:  q.priorityFn(e.ChannelID),
				timestamp: time.Now().UTC(),
			})

			// Trim every push to bound the heap even while output is blocked.
			if len(pq) > q.maxSize {
				sort.Sort(pq)
				for _, dropped := range pq[q.maxSize:] {
					q.metrics.PeerQueueDroppedMsgs.With("ch_id", strconv.Itoa(int(dropped.envelope.ChannelID))).Add(1)
					dropped.envelope.NotifyDelivery()
				}
				pq = pq[:q.maxSize]
			}

		case out <- next:
			heap.Pop(&pq)
		}
	}
}
