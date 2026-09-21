package statesync

import (
	"context"
	"fmt"
	"testing"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/libs/log"
	ssproto "github.com/dashpay/tenderdash/proto/tendermint/statesync"
	"github.com/dashpay/tenderdash/types"
)

// TestIssue1475UnsolicitedMetadata asserts that an idle discovery window retains no advertisements.
func TestIssue1475UnsolicitedMetadata(t *testing.T) {
	pool := newSnapshotPool()
	r := &Reactor{logger: log.NewNopLogger(), syncer: &syncer{
		logger: log.NewNopLogger(), snapshots: pool, metrics: NopMetrics(),
	}}
	const metadataSize = 1 << 20
	for peer := 0; peer < 2; peer++ {
		for i := 0; i < recentSnapshots; i++ {
			msg := &ssproto.SnapshotsResponse{
				Height: uint64(peer*recentSnapshots + i + 1), Version: 1,
				Hash: make([]byte, 32), Metadata: make([]byte, metadataSize),
			}
			if err := r.handleMessage(context.Background(), &p2p.Envelope{
				From: types.NodeID(fmt.Sprintf("%040x", peer+1)), ChannelID: SnapshotChannel, Message: msg,
			}, nil); err != nil {
				t.Fatal(err)
			}
		}
		retained := 0
		for _, snapshot := range pool.snapshots {
			retained += len(snapshot.Metadata)
		}
		t.Logf("peers=%d snapshots=%d retained_metadata_bytes=%d", peer+1, len(pool.snapshots), retained)
	}
	if len(pool.snapshots) != 0 {
		t.Fatalf("retained %d unsolicited snapshots without any discovery request; expected none", len(pool.snapshots))
	}
}
