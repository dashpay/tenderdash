package blocksync

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/p2p/client"
	"github.com/dashpay/tenderdash/internal/p2p/client/mocks"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/promise"
	"github.com/dashpay/tenderdash/libs/workerpool"
	bcproto "github.com/dashpay/tenderdash/proto/tendermint/blocksync"
	"github.com/dashpay/tenderdash/types"
)

// TestRetryOfTheHeightToApplyAsksEveryPeerWhileTheProducerWaits checks that a
// failed request for the height block sync waits to apply is asked of every
// peer that advertises it when that height became the one to apply while the
// producer was waiting, inside nextJob, for a peer to advertise a higher one.
// That is where the producer of a node following the network spends its time
// while some peer reports a range further up.
func TestRetryOfTheHeightToApplyAsksEveryPeerWhileTheProducerWaits(t *testing.T) {
	const (
		applying = int64(100)
		next     = applying + 1
	)
	peerIDs := []types.NodeID{"first", "second", "third"}
	store := NewInMemPeerStore()
	for _, peerID := range peerIDs {
		store.Upsert(newPeerData(peerID, 1, next))
	}
	store.Upsert(newPeerData("far range", 1_000_000_000, maxPlausiblePeerHeight))
	asked := map[types.NodeID]int{}
	blockClient := mocks.NewBlockClient(t)
	blockClient.
		On("GetBlock", mock.Anything, mock.Anything, mock.Anything).
		Return(func(_ context.Context, h int64, peerID types.NodeID) *promise.Promise[*bcproto.BlockResponse] {
			if h == next {
				asked[peerID]++
			}
			return promise.New(func(_ func(*bcproto.BlockResponse), reject func(error)) {
				reject(client.ErrPeerNotResponded)
			})
		}, nil)
	// applying and next are requested; the producer finds another job due while
	// block sync waits for applying, and waits for a peer to advertise next+1
	gen := newJobGenerator(next+1, log.NewNopLogger(), blockClient, store)
	require.True(t, gen.shouldJobBeGenerated(applying, 0))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	jobs := make(chan *jobOrErr, 1)
	go func() {
		job, err := gen.nextJob(ctx)
		jobs <- &jobOrErr{job: job, err: err}
	}()
	time.Sleep(50 * time.Millisecond)

	// applying is applied meanwhile, and the request for next, sent to the
	// first peer, fails
	store.Update("first", AddNumPending(1))
	store.AddFailure("first", maxConsecutiveFailures)
	gen.pushBack(next)

	// the producer goes on as produceJob does
	res := <-jobs
	require.NoError(t, res.err)
	res.job.Execute(ctx)
	for range 2 * len(peerIDs) {
		if !gen.shouldJobBeGenerated(next, 0) {
			break
		}
		jobCtx, jobCancel := context.WithTimeout(ctx, 200*time.Millisecond)
		job, err := gen.nextJob(jobCtx)
		jobCancel()
		if err != nil {
			// waiting for a peer to advertise next+1 again
			break
		}
		job.Execute(ctx)
	}
	for _, peerID := range peerIDs[1:] {
		require.Equal(t, 1, asked[peerID], "%s asked for the height to apply %d times (requests: %v)",
			peerID, asked[peerID], asked)
	}
}

type jobOrErr struct {
	job *workerpool.Job
	err error
}
