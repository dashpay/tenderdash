package blocksync

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/test/factory"
	bcproto "github.com/dashpay/tenderdash/proto/tendermint/blocksync"
	"github.com/dashpay/tenderdash/types"
)

// TestBlockResponseRefusesPaddedCommitBeacon is the regression test for a block sync peer relaying an
// honest block and its validly signed commit with the commit's beacon padded: neither the commit hash
// nor its threshold signature covers the beacon, so before the size bound the padded commit passed
// verification, was stored as the seen commit and reached the application. The response is now
// refused while it is decoded, before anything is verified, stored or passed on.
func TestBlockResponseRefusesPaddedCommitBeacon(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	genDoc, privVals := factory.RandGenesisDoc(1, factory.ConsensusParams())
	state, err := sm.MakeGenesisState(genDoc)
	require.NoError(t, err)
	block, _, _, seen := makeNextBlock(ctx, t, state, privVals[0], 1, types.NewCommit(0, 0, types.BlockID{}, nil, nil))

	response := func(beacon, appHash []byte) (*BlockResponse, error) {
		t.Helper()
		pbBlock, err := block.ToProto()
		require.NoError(t, err)
		pbCommit := seen.ToProto()
		pbCommit.Beacon = beacon
		pbCommit.AppHash = appHash
		bz, err := (&bcproto.BlockResponse{Block: pbBlock, Commit: pbCommit}).Marshal()
		require.NoError(t, err)
		var decoded bcproto.BlockResponse
		require.NoError(t, decoded.Unmarshal(bz))
		return BlockResponseFromProto(&decoded, "peer")
	}

	honest, err := response(nil, nil)
	require.NoError(t, err)
	require.Equal(t, seen.Hash(), honest.Commit.Hash())

	for name, fields := range map[string][2][]byte{
		"25 MiB beacon":           {make([]byte, 25<<20), []byte("junk")},
		"5 MiB beacon":            {make([]byte, 5<<20), make([]byte, 32)},
		"app hash without beacon": {nil, make([]byte, 32)},
	} {
		t.Run(name, func(t *testing.T) {
			resp, err := response(fields[0], fields[1])
			require.ErrorIs(t, err, types.ErrInvalidCommitRandomness)
			require.Nil(t, resp)
		})
	}
}
