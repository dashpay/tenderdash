package statesync

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

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
			require.NoError(t, r.handleMessage(context.Background(), &p2p.Envelope{
				From: types.NodeID(fmt.Sprintf("%040x", peer+1)), ChannelID: SnapshotChannel, Message: msg,
			}, nil))
		}
	}
	require.Empty(t, pool.snapshots, "retained unsolicited snapshots without any discovery request")
}
