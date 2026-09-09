package statesync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sasha-s/go-deadlock"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/test/factory"
	ssproto "github.com/dashpay/tenderdash/proto/tendermint/statesync"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// backfillProbeResult records one span's measurement.
type backfillProbeResult struct {
	span          int64
	peakGap       int64
	gapAtHalf     int64
	finalVerified int64
	baseHeapInuse uint64
	peakHeapInuse uint64
	duration      time.Duration
	buildDuration time.Duration
	err           error
}

// TestBackfillFetchVerifyBackpressure asserts that the backfill fetch workers
// cannot run further ahead of the single verification loop than the block queue's
// fetch-ahead bound admits, and measures what the bounded backlog costs in heap.
//
// Fetching a light block costs a round trip; verifying one costs a BLS commit
// check on a single serialized loop. Wherever aggregate fetch throughput exceeds
// verification throughput - a property of the deployment rather than of the code -
// the fetched-but-unverified backlog grows until something stops it, and
// blockQueue.maxFetchAhead is the only thing that does. This probe pins the fetch
// side at the fastest it can go, serving from memory, so that the bound is what
// the measurement finds.
//
// Every span asserts the same ceiling, which is why several are run: a bounded
// design holds the same depth whatever span it is given, whereas without a bound
// the peak tracks the span and the heap cost tracks it with it.
//
// Opt-in, and must run without -race: it takes minutes at the larger spans, and it
// samples Reactor.BackFilledBlocks, a counter the verify loop increments without
// taking the reactor lock its reader holds.
//
//	BACKFILL_PROBE=1 go test ./internal/statesync/ \
//	    -run TestBackfillFetchVerifyBackpressure -v
func TestBackfillFetchVerifyBackpressure(t *testing.T) {
	if os.Getenv("BACKFILL_PROBE") == "" {
		t.Skip("set BACKFILL_PROBE=1 to run the backfill backpressure probe")
	}

	spans := []int64{200, 800, 3200}
	results := make([]backfillProbeResult, 0, len(spans))
	for _, span := range spans {
		t.Run(fmt.Sprintf("span=%d", span), func(t *testing.T) {
			results = append(results, runBackfillProbe(t, span))
		})
	}

	t.Log("span | peak gap | gap@50% | heap growth | bytes/block | duration | build")
	for _, r := range results {
		heapGrowth := int64(r.peakHeapInuse) - int64(r.baseHeapInuse)
		var perBlock int64
		if r.peakGap > 0 {
			perBlock = heapGrowth / r.peakGap
		}
		t.Logf("%5d | %8d | %7d | %8.1f MiB | %9d B | %8s | %s",
			r.span, r.peakGap, r.gapAtHalf,
			float64(heapGrowth)/(1<<20), perBlock,
			r.duration.Round(time.Millisecond), r.buildDuration.Round(time.Millisecond))
		if r.err != nil {
			t.Logf("      backfill returned: %v", r.err)
		}
	}
}

func runBackfillProbe(t *testing.T, span int64) backfillProbeResult {
	// The default sample rate is one sample per 512KiB allocated, too coarse to
	// attribute a retained population of light blocks to the site holding it.
	if os.Getenv("BACKFILL_PROBE_HEAP_DIR") != "" {
		defer func(rate int) { runtime.MemProfileRate = rate }(runtime.MemProfileRate)
		runtime.MemProfileRate = 8192
	}

	// The deadlock detector retains a caller stack per lock acquisition, which
	// lands on the same profile as the light blocks under measurement without
	// having anything to do with them. Disabling it isolates what the queue holds.
	if os.Getenv("BACKFILL_PROBE_NO_DEADLOCK") != "" {
		defer func(disabled bool) { deadlock.Opts.Disable = disabled }(deadlock.Opts.Disable)
		deadlock.Opts.Disable = true
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	const stopHeight int64 = 10
	const (
		numPeers    = 4
		numHandlers = 4
	)
	startHeight := stopHeight + span - 1

	rts := setup(ctx, t, nil, nil, nil, 100)
	rts.stateStore.
		On("SaveValidatorSets",
			mock.AnythingOfType("int64"),
			mock.AnythingOfType("int64"),
			mock.AnythingOfType("*types.ValidatorSet")).
		Maybe().
		Return(nil)

	for _, peer := range genPeerIDs(numPeers) {
		rts.peerUpdateCh <- p2p.PeerUpdate{
			NodeID: types.NodeID(peer),
			Status: p2p.PeerStatusUp,
			Channels: p2p.ChannelIDSet{
				SnapshotChannel:   struct{}{},
				ChunkChannel:      struct{}{},
				LightBlockChannel: struct{}{},
				ParamsChannel:     struct{}{},
			},
		}
	}

	buildStart := time.Now()
	chain := buildStaticValidatorSetChain(ctx, t, stopHeight-2, startHeight+1, rts.privVal)
	buildDuration := time.Since(buildStart)

	closeCh := make(chan struct{})
	defer close(closeCh)

	var served atomic.Int64
	for i := 0; i < numHandlers; i++ {
		go serveLightBlocks(ctx, t, chain, rts.blockOutCh, rts.blockInCh, closeCh, &served)
	}

	// stopTime far in the future makes every block older than it, so the run
	// terminates on stopHeight rather than on the evidence-age clock.
	stopTime := time.Now().Add(24 * time.Hour)

	var (
		peakGap       int64
		gapAtHalf     int64
		peakHeapInuse uint64
	)
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	baseHeapInuse := base.HeapInuse

	// The sampler owns the measurements below and is stopped, and awaited, before
	// they are read; closeCh cannot serve for that, since the response handlers
	// share it and must outlive the run.
	samplerStop := make(chan struct{})
	samplerDone := make(chan struct{})
	go func() {
		defer close(samplerDone)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		var ms runtime.MemStats
		for {
			select {
			case <-samplerStop:
				return
			case <-ticker.C:
				verified := rts.reactor.BackFilledBlocks()
				gap := served.Load() - verified
				if gap > peakGap {
					peakGap = gap
				}
				if gapAtHalf == 0 && verified >= span/2 {
					gapAtHalf = gap
					writeHeapProfile(t, span)
				}
				runtime.ReadMemStats(&ms)
				if ms.HeapInuse > peakHeapInuse {
					peakHeapInuse = ms.HeapInuse
				}
			}
		}
	}()

	runStart := time.Now()
	err := rts.reactor.backfill(
		ctx,
		factory.DefaultTestChainID,
		startHeight,
		stopHeight,
		1,
		factory.MakeBlockIDWithHash(chain[startHeight].Hash()),
		stopTime,
		10*time.Millisecond,
		5*time.Second,
	)
	duration := time.Since(runStart)
	close(samplerStop)
	<-samplerDone

	verified := rts.reactor.BackFilledBlocks()
	t.Logf("span=%d verified=%d served=%d peakGap=%d gapAtHalf=%d heap=%.1f->%.1f MiB in %s (chain build %s) err=%v",
		span, verified, served.Load(), peakGap, gapAtHalf,
		float64(baseHeapInuse)/(1<<20), float64(peakHeapInuse)/(1<<20),
		duration.Round(time.Millisecond), buildDuration.Round(time.Millisecond), err)

	// The queue admits this many heights fetched but not yet verified, and nothing
	// else limits the fetch side here: every response is served from memory by
	// handlers that never fail or stall.
	//
	// The tolerance is the instrument's, not the bound's. This samples served
	// minus Reactor.BackFilledBlocks, and the verify loop frees a slot in
	// queue.success() a few statements before it bumps that counter, so the
	// fetcher which takes the freed slot can serve inside the window and be
	// counted against a verification that has already happened. The loop is
	// serial, so at most one verification is ever in that window.
	fetchAhead := int64(rts.reactor.cfg.Fetchers) * backfillFetchAheadPerFetcher
	require.LessOrEqual(t, peakGap, fetchAhead+1,
		"fetching ran %d blocks ahead of verification, past the %d the bound admits",
		peakGap, fetchAhead)

	// The bound must cost the happy path nothing: these peers are honest and
	// answer everything, so the whole span still has to be backfilled.
	require.NoError(t, err, "an honest, responsive peer set must still complete the backfill")
	require.EqualValues(t, span, verified, "every height in the span must be verified")

	return backfillProbeResult{
		span:          span,
		peakGap:       peakGap,
		gapAtHalf:     gapAtHalf,
		finalVerified: verified,
		baseHeapInuse: baseHeapInuse,
		peakHeapInuse: peakHeapInuse,
		duration:      duration,
		buildDuration: buildDuration,
		err:           err,
	}
}

// writeHeapProfile dumps a live heap profile mid-run when a directory is named in
// BACKFILL_PROBE_HEAP_DIR, so that the growth can be attributed to a retention site
// rather than inferred from a total.
func writeHeapProfile(t *testing.T, span int64) {
	dir := os.Getenv("BACKFILL_PROBE_HEAP_DIR")
	if dir == "" {
		return
	}
	path := filepath.Join(dir, fmt.Sprintf("heap-span%d.pprof", span))
	f, err := os.Create(path)
	if err != nil {
		t.Logf("heap profile: %v", err)
		return
	}
	defer f.Close()
	runtime.GC()
	if err := pprof.WriteHeapProfile(f); err != nil {
		t.Logf("heap profile: %v", err)
		return
	}
	t.Logf("heap profile written to %s", path)
}

// serveLightBlocks answers every light block request from the prebuilt chain as
// fast as it can, which is the condition the probe is about: fetching cheaper
// than verifying.
func serveLightBlocks(
	ctx context.Context,
	t *testing.T,
	chain map[int64]*types.LightBlock,
	receiving, sending chan p2p.Envelope,
	closeCh chan struct{},
	served *atomic.Int64,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-closeCh:
			return
		case envelope := <-receiving:
			msg, ok := envelope.Message.(*ssproto.LightBlockRequest)
			if !ok {
				continue
			}
			block, ok := chain[int64(msg.Height)]
			if !ok {
				sendMsgToChan(ctx, sending, newLBMessage(envelope.To, nil))
				continue
			}
			lb, err := block.ToProto()
			if err != nil {
				t.Errorf("light block to proto: %v", err)
				return
			}
			sendMsgToChan(ctx, sending, newLBMessage(envelope.To, lb))
			served.Add(1)
		}
	}
}

// buildStaticValidatorSetChain builds a light block chain that keeps one validator
// set for every height. A chain that rotates its set per height costs a key
// generation per block to build, which dominates the run at the spans this probe
// needs; holding the set constant also matches how a real chain behaves between
// quorum rotations.
func buildStaticValidatorSetChain(
	ctx context.Context,
	t *testing.T,
	fromHeight, toHeight int64,
	privVal *types.MockPV,
) map[int64]*types.LightBlock {
	t.Helper()

	vals, pv := types.RandValidatorSet(3)
	pk, err := pv[0].GetPrivateKey(ctx, vals.QuorumHash)
	require.NoError(t, err)

	chain := make(map[int64]*types.LightBlock, toHeight-fromHeight)
	lastBlockID := factory.MakeBlockID()
	blockTime := time.Now().Add(-time.Duration(toHeight-fromHeight) * time.Second)

	for height := fromHeight; height < toHeight; height++ {
		privVal.UpdatePrivateKey(ctx, pk, vals.QuorumHash, vals.ThresholdPublicKey, height)

		header := factory.MakeHeader(t, &types.Header{
			Height:      height,
			LastBlockID: lastBlockID,
			Time:        blockTime,
			AppHash:     make([]byte, crypto.DefaultHashSize),
		})
		header.Version.App = testAppVersion
		header.ValidatorsHash = vals.Hash()
		header.NextValidatorsHash = vals.Hash()
		header.ConsensusHash = types.DefaultConsensusParams().HashConsensusParams()

		stateID := header.StateID()
		blockID := types.BlockID{
			Hash: header.Hash(),
			PartSetHeader: types.PartSetHeader{
				Total: 100,
				Hash:  factory.RandomHash(),
			},
			StateID: stateID.Hash(),
		}

		voteSet := types.NewVoteSet(factory.DefaultTestChainID, height, 0, tmproto.PrecommitType, vals)
		commit, err := factory.MakeCommit(ctx, blockID, height, 0, voteSet, vals, pv,
			tmproto.VoteExtension{
				Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
				Extension: []byte("backfill probe threshold extension"),
			})
		require.NoError(t, err)

		chain[height] = &types.LightBlock{
			SignedHeader: &types.SignedHeader{Header: header, Commit: commit},
			ValidatorSet: vals,
		}

		lastBlockID = blockID
		blockTime = blockTime.Add(time.Second)
	}
	return chain
}
