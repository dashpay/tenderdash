package statesync

import (
	"context"
	"fmt"
	"sort"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	abci "github.com/dashpay/tenderdash/abci/types"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/store"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

// LightBlockRepository is a repository for light blocks
type LightBlockRepository struct {
	stateStore sm.Store
	blockStore *store.BlockStore
}

// Get works out whether the node has a light block at a particular
// height and if so returns it so it can be gossiped to peers
func (r *LightBlockRepository) Get(height uint64) (*types.LightBlock, error) {
	h := int64(height)

	blockMeta := r.blockStore.LoadBlockMeta(h)
	if blockMeta == nil {
		return nil, nil
	}

	commit := r.blockStore.LoadBlockCommit(h)
	if commit == nil {
		return nil, nil
	}

	vals, err := r.stateStore.LoadValidators(h, r.blockStore)
	if err != nil {
		return nil, err
	}
	if vals == nil {
		return nil, nil
	}

	return &types.LightBlock{
		SignedHeader: &types.SignedHeader{
			Header: &blockMeta.Header,
			Commit: commit,
		},
		ValidatorSet: vals,
	}, nil
}

// snapshotRepository is a repository for snapshots
type snapshotRepository struct {
	logger log.Logger
	client abci.StateSyncer
}

// newSnapshotRepository creates a new snapshot repository
func newSnapshotRepository(client abciclient.Client, logger log.Logger) *snapshotRepository {
	return &snapshotRepository{
		logger: logger,
		client: client,
	}
}

// offerSnapshot offers a snapshot to the app. It returns various errors depending on the app's
// response, or nil if the snapshot was accepted.
func (r *snapshotRepository) offerSnapshot(ctx context.Context, snapshot *snapshot) error { //nolint:dupl
	r.logger.Info("Offering snapshot to ABCI app",
		"height", snapshot.Height,
		"version", snapshot.Version,
		"hash", snapshot.Hash)
	resp, err := r.client.OfferSnapshot(ctx, &abci.RequestOfferSnapshot{
		Snapshot: &abci.Snapshot{
			Height:   snapshot.Height,
			Version:  snapshot.Version,
			Hash:     snapshot.Hash,
			Metadata: snapshot.Metadata,
		},
		AppHash: snapshot.trustedAppHash,
	})
	if err != nil {
		return fmt.Errorf("failed to offer snapshot: %w", err)
	}
	switch resp.Result {
	case abci.ResponseOfferSnapshot_ACCEPT:
		r.logger.Info("Snapshot accepted, restoring",
			"height", snapshot.Height,
			"version", snapshot.Version,
			"hash", snapshot.Hash)
		return nil
	case abci.ResponseOfferSnapshot_ABORT:
		return errAbort
	case abci.ResponseOfferSnapshot_REJECT:
		return errRejectSnapshot
	case abci.ResponseOfferSnapshot_REJECT_FORMAT:
		return errRejectFormat
	case abci.ResponseOfferSnapshot_REJECT_SENDER:
		return errRejectSender
	default:
		return fmt.Errorf("unknown ResponseOfferSnapshot result %v", resp.Result)
	}
}

// loadSnapshotChunk loads a chunk of a snapshot from the app
func (r *snapshotRepository) loadSnapshotChunk(
	ctx context.Context,
	height uint64,
	version uint32,
	chunkID []byte,
) (*abci.ResponseLoadSnapshotChunk, error) {
	return r.client.LoadSnapshotChunk(ctx, &abci.RequestLoadSnapshotChunk{
		Height:  height,
		Version: version,
		ChunkId: chunkID,
	})
}

// recentSnapshots fetches the n most recent snapshots from the app
func (r *snapshotRepository) recentSnapshots(ctx context.Context, n uint32) ([]*snapshot, error) {
	resp, err := r.client.ListSnapshots(ctx, &abci.RequestListSnapshots{})
	if err != nil {
		return nil, err
	}
	sortSnapshots(resp.Snapshots)
	if n > recentSnapshots {
		n = recentSnapshots
	}
	snapshots := make([]*snapshot, 0, n)
	for _, s := range resp.Snapshots[:n] {
		snapshots = append(snapshots, newSnapshotFromABCI(s))
	}
	return snapshots, nil
}

func sortSnapshots(snapshots []*abci.Snapshot) {
	sort.Slice(snapshots, func(i, j int) bool {
		a := snapshots[i]
		b := snapshots[j]
		switch {
		case a.Height > b.Height:
			return true
		case a.Height == b.Height && a.Version > b.Version:
			return true
		default:
			return false
		}
	})
}

func newSnapshotFromABCI(s *abci.Snapshot) *snapshot {
	return &snapshot{
		Height:   s.Height,
		Version:  s.Version,
		Hash:     s.Hash,
		Metadata: s.Metadata,
	}
}
