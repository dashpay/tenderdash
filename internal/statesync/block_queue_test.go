package statesync

import (
	"container/heap"
	"context"
	"errors"
	"math/rand"
	"testing"
	"time"

	sync "github.com/sasha-s/go-deadlock"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/types"
)

var (
	startHeight int64 = 200
	stopHeight  int64 = 100
	stopTime          = time.Date(2019, 1, 1, 1, 0, 0, 0, time.UTC)
	endTime           = stopTime.Add(-1 * time.Second)
	numWorkers        = 1

	// testFetchAhead is small enough that the concurrent tests run against the
	// fetch-ahead bound rather than past it.
	testFetchAhead int64 = 8

	// spanFetchAhead admits the whole height span at once, for the tests that
	// deliberately drain every height without verifying any. The bound itself is
	// covered by TestBlockQueueBoundsFetchAhead.
	spanFetchAhead = startHeight - stopHeight + 1
)

func TestBlockQueueBasic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	peerID, err := types.NewNodeID("0011223344556677889900112233445566778899")
	require.NoError(t, err)

	queue := newBlockQueue(startHeight, stopHeight, 1, stopTime, 1, testFetchAhead)
	wg := &sync.WaitGroup{}

	// asynchronously fetch blocks and add it to the queue
	for i := 0; i <= numWorkers; i++ {
		wg.Add(1)
		go func() {
			for {
				select {
				case height := <-queue.nextHeight():
					queue.add(mockLBResp(ctx, t, peerID, height, endTime))
				case <-queue.done():
					wg.Done()
					return
				}
			}
		}()
	}

	trackingHeight := startHeight
	wg.Add(1)

loop:
	for {
		select {
		case <-queue.done():
			wg.Done()
			break loop

		case resp := <-queue.verifyNext():
			// assert that the queue serializes the blocks
			require.Equal(t, resp.block.Height, trackingHeight)
			trackingHeight--
			queue.success()
		}

	}

	wg.Wait()
	assert.Less(t, trackingHeight, stopHeight)
}

// Test with spurious failures and retries
func TestBlockQueueWithFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	peerID, err := types.NewNodeID("0011223344556677889900112233445566778899")
	require.NoError(t, err)

	queue := newBlockQueue(startHeight, stopHeight, 1, stopTime, 200, testFetchAhead)
	wg := &sync.WaitGroup{}

	failureRate := 4
	for i := 0; i <= numWorkers; i++ {
		wg.Add(1)
		go func() {
			for {
				select {
				case height := <-queue.nextHeight():
					if rand.Intn(failureRate) == 0 {
						queue.retry(height)
					} else {
						queue.add(mockLBResp(ctx, t, peerID, height, endTime))
					}
				case <-queue.done():
					wg.Done()
					return
				}
			}
		}()
	}

	trackingHeight := startHeight
	for {
		select {
		case resp := <-queue.verifyNext():
			// assert that the queue serializes the blocks
			assert.Equal(t, resp.block.Height, trackingHeight)
			if rand.Intn(failureRate) == 0 {
				queue.retry(resp.block.Height)
			} else {
				trackingHeight--
				queue.success()
			}

		case <-queue.done():
			wg.Wait()
			assert.Less(t, trackingHeight, stopHeight)
			return
		}
	}
}

// Test that when all the blocks are retrieved that the queue still holds on to
// it's workers and in the event of failure can still fetch the failed block
func TestBlockQueueBlocks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	peerID, err := types.NewNodeID("0011223344556677889900112233445566778899")
	require.NoError(t, err)
	queue := newBlockQueue(startHeight, stopHeight, 1, stopTime, 2, spanFetchAhead)
	expectedHeight := startHeight
	retryHeight := stopHeight + 2

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

loop:
	for {
		select {
		case height := <-queue.nextHeight():
			require.Equal(t, height, expectedHeight)
			require.GreaterOrEqual(t, height, stopHeight)
			expectedHeight--
			queue.add(mockLBResp(ctx, t, peerID, height, endTime))
		case <-time.After(1 * time.Second):
			if expectedHeight >= stopHeight {
				t.Fatalf("expected next height %d", expectedHeight)
			}
			break loop
		}
	}

	// close any waiter channels that the previous worker left hanging
	for _, ch := range queue.waiters {
		close(ch)
	}
	queue.waiters = make([]chan int64, 0)

	wg := &sync.WaitGroup{}
	wg.Add(1)
	// so far so good. The worker is waiting. Now we fail a previous
	// block and check that the worker fetches them
	go func(t *testing.T) {
		defer wg.Done()
		select {
		case height := <-queue.nextHeight():
			require.Equal(t, retryHeight, height)
		case <-time.After(1 * time.Second):
			require.Fail(t, "queue didn't ask worker to fetch failed height")
		}
	}(t)
	queue.retry(retryHeight)
	wg.Wait()

}

func TestBlockQueueAcceptsNoMoreBlocks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	peerID, err := types.NewNodeID("0011223344556677889900112233445566778899")
	require.NoError(t, err)
	queue := newBlockQueue(startHeight, stopHeight, 1, stopTime, 1, spanFetchAhead)
	defer queue.close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

loop:
	for {
		select {
		case height := <-queue.nextHeight():
			require.GreaterOrEqual(t, height, stopHeight)
			queue.add(mockLBResp(ctx, t, peerID, height, endTime))
		case <-time.After(1 * time.Second):
			break loop
		}
	}

	require.Len(t, queue.pending, int(startHeight-stopHeight)+1)

	queue.add(mockLBResp(ctx, t, peerID, stopHeight-1, endTime))
	require.Len(t, queue.pending, int(startHeight-stopHeight)+1)
}

// Test a scenario where more blocks are needed then just the stopheight because
// we haven't found a block with a small enough time.
func TestBlockQueueStopTime(t *testing.T) {
	peerID, err := types.NewNodeID("0011223344556677889900112233445566778899")
	require.NoError(t, err)

	queue := newBlockQueue(startHeight, stopHeight, 1, stopTime, 1, testFetchAhead)
	wg := &sync.WaitGroup{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	baseTime := stopTime.Add(-50 * time.Second)

	// asynchronously fetch blocks and add it to the queue
	for i := 0; i <= numWorkers; i++ {
		wg.Add(1)
		go func() {
			for {
				select {
				case height := <-queue.nextHeight():
					blockTime := baseTime.Add(time.Duration(height) * time.Second)
					queue.add(mockLBResp(ctx, t, peerID, height, blockTime))
				case <-queue.done():
					wg.Done()
					return
				}
			}
		}()
	}

	trackingHeight := startHeight
	for {
		select {
		case resp := <-queue.verifyNext():
			// assert that the queue serializes the blocks
			assert.Equal(t, resp.block.Height, trackingHeight)
			trackingHeight--
			queue.success()

		case <-queue.done():
			wg.Wait()
			assert.Less(t, trackingHeight, stopHeight-50)
			return
		}
	}
}

func TestBlockQueueInitialHeight(t *testing.T) {
	peerID, err := types.NewNodeID("0011223344556677889900112233445566778899")
	require.NoError(t, err)
	const initialHeight int64 = 120

	queue := newBlockQueue(startHeight, stopHeight, initialHeight, stopTime, 1, testFetchAhead)
	wg := &sync.WaitGroup{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// asynchronously fetch blocks and add it to the queue
	for i := 0; i <= numWorkers; i++ {
		wg.Add(1)
		go func() {
			for {
				select {
				case height := <-queue.nextHeight():
					require.GreaterOrEqual(t, height, initialHeight)
					queue.add(mockLBResp(ctx, t, peerID, height, endTime))
				case <-queue.done():
					wg.Done()
					return
				}
			}
		}()
	}

loop:
	for {
		select {
		case <-queue.done():
			wg.Wait()
			require.NoError(t, queue.error())
			break loop

		case resp := <-queue.verifyNext():
			require.GreaterOrEqual(t, resp.block.Height, initialHeight)
			queue.success()
		}
	}
}

// TestBlockQueueTerminationIsIdempotent asserts that the queue survives being
// terminated twice. fail() and close() are reached from different goroutines -
// the verify loop closes on context cancellation while a rejection can terminate
// the queue at the same moment - so a second termination must be a no-op rather
// than a close-of-closed-channel panic.
func TestBlockQueueTerminationIsIdempotent(t *testing.T) {
	reason := errors.New("every usable peer was quarantined")
	queue := newBlockQueue(startHeight, stopHeight, 1, stopTime, 1, testFetchAhead)

	queue.fail(reason)
	require.NotPanics(t, queue.close, "close after a terminal failure must be a no-op")
	require.NotPanics(t, queue.close, "close must stay a no-op however often it is called")
	require.NotPanics(t, func() { queue.fail(errors.New("later reason")) })

	require.ErrorIs(t, queue.error(), reason, "the first failure reason must survive later terminations")
}

// TestBlockQueueDiscardPeer asserts that responses already supplied by a peer can
// be dropped and their heights re-queued without charging the retry budget, which
// is what keeps a peer that pipelines several bad blocks from spending the budget
// shared with honest peers.
func TestBlockQueueDiscardPeer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	badPeer, err := types.NewNodeID("0011223344556677889900112233445566778899")
	require.NoError(t, err)
	goodPeer, err := types.NewNodeID("1122334455667788990011223344556677889900")
	require.NoError(t, err)

	queue := newBlockQueue(startHeight, stopHeight, 1, stopTime, 1, testFetchAhead)
	// verifyHeight sits at startHeight, so responses below it stay pending.
	queue.add(mockLBResp(ctx, t, badPeer, startHeight-1, endTime))
	queue.add(mockLBResp(ctx, t, goodPeer, startHeight-2, endTime))

	queue.discardPeer(badPeer)

	require.NotContains(t, queue.pending, startHeight-1, "the evicted peer's response must be dropped")
	require.Contains(t, queue.pending, startHeight-2, "other peers' responses must be untouched")
	require.Zero(t, queue.retries, "re-queueing an evicted peer's work must not charge the retry budget")
	require.Equal(t, startHeight-1, heap.Pop(queue.failed), "the dropped height must be re-queued for another peer")
}

// TestBlockQueueBoundsFetchAhead asserts that the queue stops allocating heights
// once as many are fetched-but-unverified as the bound admits, and releases
// exactly one more per verified block.
//
// Fetching is cheap beside verifying - one serialized loop paying a BLS commit
// check - so peers that answer faster than this node verifies decide the depth
// of the backlog unless the allocator consults verification progress. Bounding
// it makes the backlog a property of the fetcher count rather than of the
// backfill span, which on a new node joining an established chain is the
// difference between a fixed cost and an unbounded one.
func TestBlockQueueBoundsFetchAhead(t *testing.T) {
	const maxFetchAhead int64 = 4

	queue := newBlockQueue(startHeight, stopHeight, 1, stopTime, 1, maxFetchAhead)
	defer queue.close()

	for i := int64(0); i < maxFetchAhead; i++ {
		require.Equal(t, startHeight-i, requireHeight(t, queue.nextHeight()),
			"the queue must fill the fetch pipeline up to its bound")
	}

	blocked := queue.nextHeight()
	requireNoHeight(t, blocked, "fetching must not run further ahead than the bound admits")

	queue.success()
	require.Equal(t, startHeight-maxFetchAhead, requireHeight(t, blocked),
		"a verified block must release the next height to the waiting fetcher")

	requireNoHeight(t, queue.nextHeight(),
		"a verified block must release exactly one height, not reopen the pipeline")
}

// TestBlockQueueRetriesOutrankFetchAheadBound asserts that a height needing a
// re-fetch reaches a fetcher even when the bound is saturated.
//
// It is the bound's one hard requirement: every height inside the bound is one
// the verify loop must still see, so withholding a re-fetch would leave the loop
// waiting on a height nobody is allowed to request - a bound that deadlocks the
// run it was added to protect.
func TestBlockQueueRetriesOutrankFetchAheadBound(t *testing.T) {
	const maxFetchAhead int64 = 2

	queue := newBlockQueue(startHeight, stopHeight, 1, stopTime, 10, maxFetchAhead)
	defer queue.close()

	first := requireHeight(t, queue.nextHeight())
	requireHeight(t, queue.nextHeight())

	blocked := queue.nextHeight()
	requireNoHeight(t, blocked, "the bound must stop the queue allocating a fresh height")

	queue.retry(first)
	require.Equal(t, first, requireHeight(t, blocked),
		"a height that must be re-fetched has to reach a fetcher even at the bound")
}

// requireHeight reads the height the queue handed a fetcher. The queue populates
// the channel under its own lock before returning it, so a height that is coming
// is already there and no waiting is needed to tell the two apart.
func requireHeight(t *testing.T, ch <-chan int64) int64 {
	t.Helper()
	select {
	case height := <-ch:
		return height
	default:
		require.FailNow(t, "the queue handed out no height")
		return 0
	}
}

func requireNoHeight(t *testing.T, ch <-chan int64, msg string) {
	t.Helper()
	select {
	case height := <-ch:
		require.Fail(t, msg, "the queue handed out height %d", height)
	default:
	}
}

func mockLBResp(ctx context.Context, t *testing.T, peer types.NodeID, height int64, time time.Time) lightBlockResponse {
	t.Helper()
	vals, pv := types.RandValidatorSet(3)
	_, _, lb := mockLB(ctx, t, height, time, factory.MakeBlockID(), vals, pv)
	return lightBlockResponse{
		block: lb,
		peer:  peer,
	}
}
