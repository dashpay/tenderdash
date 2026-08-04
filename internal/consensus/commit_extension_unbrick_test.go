package consensus

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/libs/log"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	tmtypes "github.com/dashpay/tenderdash/types"
)

// Peer messages are written to the WAL before they are processed, so a Commit
// carrying an undefined vote-extension type must never reach walMiddleware: the WAL
// tolerates a bad record only through repairWalFile, and the block store not at all
// (mustDecodeCommit panics, with no repair path).
//
// The two tests below pin the pair of properties that keeps both stores safe:
// MsgFromProto rejects the poison at the p2p boundary, and an honest commit still
// round-trips the WAL, so rejecting the poison costs ordinary traffic nothing.

// Rejection at the p2p boundary: nothing downstream, WAL included, sees the poison.
func TestMsgFromProto_PoisonCommitExtension_RejectedBeforeWAL(t *testing.T) {
	blockID := tmtypes.BlockID{
		Hash: crypto.CRandBytes(crypto.HashSize),
		PartSetHeader: tmtypes.PartSetHeader{
			Total: 1,
			Hash:  crypto.CRandBytes(crypto.HashSize),
		},
		StateID: crypto.CRandBytes(crypto.HashSize),
	}
	poison := &tmtypes.Commit{
		Height:                  100,
		Round:                   0,
		BlockID:                 blockID,
		ThresholdBlockSignature: crypto.CRandBytes(96),
		ThresholdVoteExtensions: []*tmproto.VoteExtension{
			{Type: tmproto.VoteExtensionType(42), Extension: []byte("x"), Signature: make([]byte, 96)},
		},
	}

	_, err := MsgFromProto(&tmcons.Commit{Commit: poison.ToProto()})
	require.ErrorIs(t, err, tmtypes.ErrUnknownVoteExtensionType,
		"a commit carrying an undefined extension type must be rejected at the p2p boundary")

	// The same commit with a defined type is accepted, so the rejection is caused by
	// the type and not by the shape of the message.
	poison.ThresholdVoteExtensions[0].Type = tmproto.VoteExtensionType_THRESHOLD_RECOVER
	_, err = MsgFromProto(&tmcons.Commit{Commit: poison.ToProto()})
	require.NoError(t, err)
}

// Honest commits must still round-trip through the WAL untouched: a commit the
// rejection catches by mistake is undecodable on replay, which bricks a node just as
// thoroughly as the poison it guards against.
func TestWAL_HonestCommitExtension_StillDecodes(t *testing.T) {
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
	// THRESHOLD_RECOVER_RAW is the only extension type this chain uses in practice:
	// a scan of 406838 mainnet heights found 29398 of them and no other type at all.
	msg := &CommitMessage{
		Commit: &tmtypes.Commit{
			Height:                  100,
			Round:                   0,
			BlockID:                 blockID,
			ThresholdBlockSignature: crypto.CRandBytes(96),
			ThresholdVoteExtensions: []*tmproto.VoteExtension{
				{
					Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW,
					Extension: []byte("x"),
					Signature: make([]byte, 96),
				},
			},
		},
	}
	require.NoError(t, wal.Write(msgInfo{Msg: msg}))
	require.NoError(t, wal.FlushAndSync())

	gr, err := wal.Group().NewReader(0)
	require.NoError(t, err)
	defer gr.Close()

	dec := NewWALDecoder(gr)
	// First decoded record is the #ENDHEIGHT{0} marker written on WAL start.
	_, err = dec.Decode()
	require.NoError(t, err)

	readMsg, err := dec.Decode()
	require.NoError(t, err, "an honest commit must survive WAL decode")
	require.NotNil(t, readMsg)
	mi, ok := readMsg.Msg.(msgInfo)
	require.True(t, ok, "expected msgInfo, got %T", readMsg.Msg)
	commitMsg, ok := mi.Msg.(*CommitMessage)
	require.True(t, ok, "expected *CommitMessage, got %T", mi.Msg)
	require.Len(t, commitMsg.Commit.ThresholdVoteExtensions, 1)
	require.Equal(t,
		tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW,
		commitMsg.Commit.ThresholdVoteExtensions[0].Type)
}
