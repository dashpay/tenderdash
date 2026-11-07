package p2p

import (
	"container/heap"
	"context"
	"sort"
	"time"

	"github.com/cosmos/gogoproto/proto"
)

// simpleQueueTrimFraction sets how far below maxSize an overflowing heap is
// trimmed: by 1/simpleQueueTrimFraction of maxSize.
const simpleQueueTrimFraction = 16

type simpleQueue struct {
	input   chan Envelope
	output  chan Envelope
	closeFn func()
	closeCh <-chan struct{}

	maxSize int
	chDescs []*ChannelDescriptor
}

func newSimplePriorityQueue(ctx context.Context, size int) *simpleQueue {
	if size%2 != 0 {
		size++
	}

	ctx, cancel := context.WithCancel(ctx)
	q := &simpleQueue{
		input:   make(chan Envelope, size*2),
		output:  make(chan Envelope, size/2),
		maxSize: size * size,
		closeCh: ctx.Done(),
		closeFn: cancel,
	}

	go q.run(ctx)
	return q
}

func (q *simpleQueue) enqueue() chan<- Envelope { return q.input }
func (q *simpleQueue) dequeue() <-chan Envelope { return q.output }
func (q *simpleQueue) close()                   { q.closeFn() }
func (q *simpleQueue) closed() <-chan struct{}  { return q.closeCh }

func (q *simpleQueue) run(ctx context.Context) {
	defer q.closeFn()

	var chPriorities = make(map[ChannelID]uint, len(q.chDescs))
	for _, chDesc := range q.chDescs {
		chID := chDesc.ID
		chPriorities[chID] = uint(chDesc.Priority)
	}

	pq := make(priorityQueue, 0, q.maxSize)
	heap.Init(&pq)
	ticker := time.NewTicker(10 * time.Millisecond)
	// must have a buffer of exactly one because both sides of
	// this channel are used in this loop, and simply signals adds
	// to the heap
	signal := make(chan struct{}, 1)
	for {
		select {
		case <-ctx.Done():
			return
		case <-q.closeCh:
			return
		case e := <-q.input:
			// enqueue the incoming Envelope
			heap.Push(&pq, &pqEnvelope{
				envelope:  e,
				size:      uint(proto.Size(e.Message)),
				priority:  chPriorities[e.ChannelID],
				timestamp: time.Now().UTC(),
			})

			// Check on every push, so the heap never holds more than
			// maxSize. Checking only when the signal was already pending
			// let every push that refilled it skip the trim, so with
			// nobody draining, the heap ran past maxSize until the next
			// tick.
			if len(pq) > q.maxSize {
				pq = q.trim(pq)
			}

			select {
			case signal <- struct{}{}:
			default:
			}

		case <-ticker.C:
			if len(pq) > 0 {
				select {
				case signal <- struct{}{}:
				default:
				}
			}
		case <-signal:
		SEND:
			for len(pq) > 0 {
				next := pq[0].envelope
				select {
				case <-ctx.Done():
					return
				case <-q.closeCh:
					return
				case q.output <- next:
					heap.Pop(&pq)
					continue SEND
				default:
					break SEND
				}
			}
		}
	}
}

// trim drops the lowest-ranked envelopes from a heap that has outgrown maxSize.
// Sorting takes time in proportion to the whole heap, and the run loop accepts
// nothing while it sorts, so trimming back to exactly maxSize would sort again
// on the very next push. Trimming a fraction below it leaves room for the pushes
// that follow, which spreads the cost of one sort across all of them. The sorted
// slice it returns is still a valid heap.
func (q *simpleQueue) trim(pq priorityQueue) priorityQueue {
	sort.Sort(pq)
	keep := q.maxSize - q.maxSize/simpleQueueTrimFraction
	for _, dropped := range pq[keep:] {
		dropped.envelope.NotifyDelivery()
	}
	// Later pushes reclaim these slots only one at a time, so release the
	// dropped envelopes now.
	clear(pq[keep:])
	return pq[:keep]
}
