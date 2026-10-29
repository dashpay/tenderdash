package types

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

const heartbeatTestChainID = "heartbeat-test-chain"

func signedTestHeartbeat(t *testing.T, valSet *ValidatorSet, pv PrivValidator, sequence uint64) *ProposerHeartbeat {
	t.Helper()
	proTxHash, err := pv.GetProTxHash(context.Background())
	require.NoError(t, err)
	pb := &tmproto.ProposerHeartbeat{
		Height:            12,
		Round:             1,
		Sequence:          sequence,
		ProposerProTxHash: proTxHash,
		Timestamp:         time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC),
	}
	require.NoError(t, pv.SignProposerHeartbeat(
		context.Background(), heartbeatTestChainID, valSet.QuorumType, valSet.QuorumHash, pb))
	hb, err := ProposerHeartbeatFromProto(pb)
	require.NoError(t, err)
	return hb
}

func TestProposerHeartbeatSignature(t *testing.T) {
	valSet, privVals := RandValidatorSet(4)
	proposer := valSet.Validators[0]
	proposerPV := privVals[0]

	verify := func(hb *ProposerHeartbeat, chainID string, pubKey crypto.PubKey) error {
		return hb.Verify(chainID, valSet.QuorumType, valSet.QuorumHash, pubKey)
	}

	t.Run("should verify against the proposer's key share", func(t *testing.T) {
		hb := signedTestHeartbeat(t, valSet, proposerPV, 3)
		assert.NoError(t, verify(hb, heartbeatTestChainID, proposer.PubKey))
	})

	t.Run("should refuse a heartbeat changed after signing", func(t *testing.T) {
		changes := map[string]func(hb *ProposerHeartbeat){
			"sequence":  func(hb *ProposerHeartbeat) { hb.Sequence++ },
			"round":     func(hb *ProposerHeartbeat) { hb.Round++ },
			"height":    func(hb *ProposerHeartbeat) { hb.Height++ },
			"timestamp": func(hb *ProposerHeartbeat) { hb.Timestamp = hb.Timestamp.Add(time.Second) },
		}
		for field, change := range changes {
			hb := signedTestHeartbeat(t, valSet, proposerPV, 3)
			change(hb)
			assert.ErrorIs(t, verify(hb, heartbeatTestChainID, proposer.PubKey), ErrProposerHeartbeatInvalidSignature, field)
		}
	})

	t.Run("should refuse a heartbeat for another chain", func(t *testing.T) {
		hb := signedTestHeartbeat(t, valSet, proposerPV, 3)
		assert.ErrorIs(t, verify(hb, "other-chain", proposer.PubKey), ErrProposerHeartbeatInvalidSignature)
	})

	t.Run("should refuse a heartbeat signed by another validator", func(t *testing.T) {
		hb := signedTestHeartbeat(t, valSet, proposerPV, 3)
		assert.ErrorIs(t, verify(hb, heartbeatTestChainID, valSet.Validators[1].PubKey), ErrProposerHeartbeatInvalidSignature)
	})

	t.Run("should refuse to verify without a public key", func(t *testing.T) {
		hb := signedTestHeartbeat(t, valSet, proposerPV, 3)
		assert.Error(t, verify(hb, heartbeatTestChainID, nil))
	})

	t.Run("should survive a proto round trip", func(t *testing.T) {
		hb := signedTestHeartbeat(t, valSet, proposerPV, 3)
		decoded, err := ProposerHeartbeatFromProto(hb.ToProto())
		require.NoError(t, err)
		assert.Equal(t, hb, decoded)
		assert.NoError(t, verify(decoded, heartbeatTestChainID, proposer.PubKey))
	})
}

func TestProposerHeartbeatRequestID(t *testing.T) {
	at := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)

	t.Run("should give every heartbeat its own signing request", func(t *testing.T) {
		id := ProposerHeartbeatRequestID(5, 0, 1, at)
		assert.NotEqual(t, id, ProposerHeartbeatRequestID(5, 0, 2, at))
		assert.NotEqual(t, id, ProposerHeartbeatRequestID(5, 1, 1, at))
		assert.NotEqual(t, id, ProposerHeartbeatRequestID(6, 0, 1, at))
		assert.NotEqual(t, id, ProposerHeartbeatRequestID(5, 0, 1, at.Add(time.Millisecond)),
			"a restarted proposer whose clock went back can repeat a sequence, never with the same time")
	})

	t.Run("should not share a request ID with a vote or a proposal", func(t *testing.T) {
		id := ProposerHeartbeatRequestID(5, 0, 0, at)
		assert.NotEqual(t, BlockRequestID(5, 0), id)
		assert.NotEqual(t, ProposalRequestID(&Proposal{Height: 5, Round: 0}), id)
	})
}

func TestProposerHeartbeatValidateBasic(t *testing.T) {
	valid := func() *ProposerHeartbeat {
		return &ProposerHeartbeat{
			Height:            1,
			Round:             0,
			Sequence:          1,
			ProposerProTxHash: crypto.RandProTxHash(),
			Timestamp:         time.Now(),
			Signature:         make([]byte, SignatureSize),
		}
	}
	assert.NoError(t, valid().ValidateBasic())

	cases := map[string]func(hb *ProposerHeartbeat){
		"should refuse height 0":              func(hb *ProposerHeartbeat) { hb.Height = 0 },
		"should refuse a negative round":      func(hb *ProposerHeartbeat) { hb.Round = -1 },
		"should refuse a short pro tx hash":   func(hb *ProposerHeartbeat) { hb.ProposerProTxHash = hb.ProposerProTxHash[:10] },
		"should refuse a missing signature":   func(hb *ProposerHeartbeat) { hb.Signature = nil },
		"should refuse an oversize signature": func(hb *ProposerHeartbeat) { hb.Signature = make([]byte, SignatureSize+1) },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			hb := valid()
			change(hb)
			assert.Error(t, hb.ValidateBasic())
		})
	}

	t.Run("should refuse nil", func(t *testing.T) {
		var hb *ProposerHeartbeat
		assert.Error(t, hb.ValidateBasic())
	})
}

// The heartbeat request ID is the "dphb" prefix, little-endian height, round,
// sequence and signing time in Unix nanoseconds, checksummed. Dash Core signs
// under it, so its bytes must not change.
func TestProposerHeartbeatRequestIDLayout(t *testing.T) {
	at := time.Unix(0, 1_700_000_000_123_456_789)
	expected := []byte("dphb")
	expected = binary.LittleEndian.AppendUint64(expected, 5)
	expected = binary.LittleEndian.AppendUint32(expected, 2)
	expected = binary.LittleEndian.AppendUint64(expected, 9)
	expected = binary.LittleEndian.AppendUint64(expected, 1_700_000_000_123_456_789)
	assert.Equal(t, crypto.Checksum(expected), ProposerHeartbeatRequestID(5, 2, 9, at))
}
