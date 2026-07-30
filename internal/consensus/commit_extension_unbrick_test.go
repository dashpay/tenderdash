package consensus

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	tmtypes "github.com/dashpay/tenderdash/types"
)

// A Commit carrying a vote-extension with an undefined proto enum type used to
// panic the consensus goroutine. Because peer messages are written to the WAL
// before they are processed, the poison was fsynced to disk and re-read on
// restart, crash-looping the node.
//
// The fix removes the panic on the verification path but deliberately leaves
// Commit.ValidateBasic unchanged. This test pins the two properties that make
// an already-attacked node recoverable on restart:
//
//  1. The poison commit still DECODES from the WAL. Commit.ValidateBasic runs
//     during WAL decode (WALDecoder.Decode -> WALFromProto -> MsgFromProto ->
//     ValidateBasic); a rejection there would surface as a DataCorruptionError
//     that aborts catchupReplay and re-bricks the node.
//  2. Verifying the decoded commit returns an error instead of panicking, so
//     the replay dispatch (whose error loggingMiddleware swallows) continues
//     rather than crash-looping.
func TestWAL_PoisonCommitExtension_DecodesAndVerifiesWithoutPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := log.NewTestingLogger(t)
	walFile := filepath.Join(t.TempDir(), "wal")

	wal, err := NewWAL(ctx, logger, walFile)
	require.NoError(t, err)
	require.NoError(t, wal.Start(ctx))
	defer func() {
		wal.Stop()
		wal.Wait()
	}()

	blockID := tmtypes.BlockID{
		Hash: crypto.CRandBytes(crypto.HashSize),
		PartSetHeader: tmtypes.PartSetHeader{
			Total: 1,
			Hash:  crypto.CRandBytes(crypto.HashSize),
		},
		StateID: crypto.CRandBytes(crypto.HashSize),
	}
	poison := &CommitMessage{
		Commit: &tmtypes.Commit{
			Height:                  100,
			Round:                   0,
			BlockID:                 blockID,
			ThresholdBlockSignature: crypto.CRandBytes(96),
			ThresholdVoteExtensions: []*tmproto.VoteExtension{
				{Type: tmproto.VoteExtensionType(42), Extension: []byte("x"), Signature: make([]byte, 96)},
			},
		},
	}
	require.NoError(t, wal.Write(msgInfo{Msg: poison}))
	require.NoError(t, wal.FlushAndSync())

	gr, err := wal.Group().NewReader(0)
	require.NoError(t, err)
	defer gr.Close()

	dec := NewWALDecoder(gr)
	// First decoded record is the #ENDHEIGHT{0} marker written on WAL start.
	_, err = dec.Decode()
	require.NoError(t, err)

	// Property 1: the poison commit survives WAL decode.
	readMsg, err := dec.Decode()
	require.NoError(t, err,
		"poison commit must survive WAL decode; a Commit.ValidateBasic rejection here would abort replay and re-brick the node")
	require.NotNil(t, readMsg)
	mi, ok := readMsg.Msg.(msgInfo)
	require.True(t, ok, "expected msgInfo, got %T", readMsg.Msg)
	commitMsg, ok := mi.Msg.(*CommitMessage)
	require.True(t, ok, "expected *CommitMessage, got %T", mi.Msg)
	require.Len(t, commitMsg.Commit.ThresholdVoteExtensions, 1)
	require.Equal(t, tmproto.VoteExtensionType(42), commitMsg.Commit.ThresholdVoteExtensions[0].Type)

	// Property 2: verifying the decoded poison returns an error, never panics —
	// which is what lets replay continue instead of crash-looping.
	vals, _ := tmtypes.RandValidatorSet(4)
	commitMsg.Commit.QuorumHash = vals.QuorumHash
	var verifyErr error
	require.NotPanics(t, func() {
		verifyErr = vals.VerifyCommit("test-chain", commitMsg.Commit.BlockID, commitMsg.Commit.Height, commitMsg.Commit)
	}, "verifying a WAL-replayed poison commit must not panic")
	require.Error(t, verifyErr, "the poison commit must be rejected, not accepted")
}
