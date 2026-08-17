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

// TestBackfillFetchVerifyBackpressure measures how far the backfill fetch workers
// run ahead of the single verification loop, and what that costs in heap.
//
// The fetch workers hand their results to the verifier through blockQueue.pending,
// an unbounded map, and nextHeight allocates the next height without reference to
// how far verification has fallen behind. Verification is one serialized loop that
// spends a BLS commit verification per block, so the queue holds whatever the
// fetchers gain: the depth grows only while aggregate fetch throughput exceeds
// verification throughput, which is a property of the deployment rather than of
// the code. This probe pins the fetch side at the fastest it can go, serving from
// memory, to measure the depth the design permits when it does.
//
// The probe reports the peak fetched-but-unverified depth against the backfill span.
// A design with backpressure bounds that depth by the number of fetch workers,
// independent of span; a design without one bounds it only by the span itself, so
// the peak tracks the span and the heap cost tracks it with it.
//
// Opt-in, and must run without -race: it is a measurement rather than an assertion,
// it takes minutes at the larger spans, and it samples a progress counter that the
// verify loop increments unlocked.
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

	samplerDone := make(chan struct{})
	go func() {
		defer close(samplerDone)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		var ms runtime.MemStats
		for {
			select {
			case <-closeCh:
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

	verified := rts.reactor.BackFilledBlocks()
	t.Logf("span=%d verified=%d served=%d peakGap=%d gapAtHalf=%d heap=%.1f->%.1f MiB in %s (chain build %s) err=%v",
		span, verified, served.Load(), peakGap, gapAtHalf,
		float64(baseHeapInuse)/(1<<20), float64(peakHeapInuse)/(1<<20),
		duration.Round(time.Millisecond), buildDuration.Round(time.Millisecond), err)

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
