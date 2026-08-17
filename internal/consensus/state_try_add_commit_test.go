package consensus

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/types"
)

// TestHandleCommitVerifyErrorClassification pins which commit-verification
// failures evict a peer. Only a failed threshold-signature check is unambiguous
// misbehavior; every other failure (wrong block ID, wrong quorum hash, a local
// fault) is reachable by an honest relayer or a forked peer and must never cause
// a disconnect. Replayed messages carry the original PeerID, so they are exempt
// regardless of the error.
func TestHandleCommitVerifyErrorClassification(t *testing.T) {
	const peerID = types.NodeID("peer-under-test")

	testCases := []struct {
		name       string
		err        error
		fromReplay bool
		wantEvict  bool
	}{
		{
			name:      "invalid threshold signature evicts",
			err:       types.ErrInvalidCommitSignature{Err: errors.New("threshold signature did not verify")},
			wantEvict: true,
		},
		{
			name:      "wrapped invalid threshold signature evicts",
			err:       fmt.Errorf("error verifying commit: %w", types.ErrInvalidCommitSignature{}),
			wantEvict: true,
		},
		{
			name: "wrong block ID does not evict",
			err: fmt.Errorf("error verifying commit: %w",
				fmt.Errorf("invalid commit -- wrong block ID: want %v, got %v", types.BlockID{}, types.BlockID{})),
			wantEvict: false,
		},
		{
			name:      "wrong quorum hash does not evict",
			err:       fmt.Errorf("invalid commit -- wrong quorum hash: validator set uses %X, commit has %X", []byte{0x1}, []byte{0x2}),
			wantEvict: false,
		},
		{
			name:      "local finalization fault does not evict",
			err:       fmt.Errorf("+2/3 committed an invalid block: %w", errors.New("app hash mismatch")),
			wantEvict: false,
		},
		{
			name:      "verification budget exhaustion does not evict",
			err:       fmt.Errorf("error verifying commit: %w", types.ErrVerificationBudgetExhausted),
			wantEvict: false,
		},
		{
			name:       "invalid threshold signature from replay does not evict",
			err:        types.ErrInvalidCommitSignature{Err: errors.New("threshold signature did not verify")},
			fromReplay: true,
			wantEvict:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			queue := &chanQueue[peerErrorMsg]{ch: make(chan peerErrorMsg, 4)}
			action := &TryAddCommitAction{
				peerErrorQueue: queue,
				metrics:        NopMetrics(),
			}

			action.handleCommitVerifyError(tc.err, peerID, tc.fromReplay)

			if !tc.wantEvict {
				require.Empty(t, queue.ch, "peer must not be evicted for %v", tc.err)
				return
			}

			require.Len(t, queue.ch, 1, "expected an eviction report")
			got := <-queue.ch
			assert.Equal(t, peerID, got.PeerID)
			assert.True(t, got.Fatal, "eviction report must be fatal to disconnect the peer")
			assert.ErrorAs(t, got.Err, &types.ErrInvalidCommitSignature{})
		})
	}
}

func TestHandleCommitVerifyErrorRecordsPeerVerificationBudgetDrop(t *testing.T) {
	testCases := []struct {
		name            string
		peerID          types.NodeID
		fromReplay      bool
		verificationErr error
		wantDrops       float64
	}{
		{
			name:            "remote commit",
			peerID:          "peer",
			verificationErr: fmt.Errorf("error verifying commit: %w", types.ErrVerificationBudgetExhausted),
			wantDrops:       1,
		},
		{
			name:            "remote non-budget error",
			peerID:          "peer",
			verificationErr: errors.New("signature verification failed"),
		},
		{
			name:            "local commit",
			verificationErr: types.ErrVerificationBudgetExhausted,
		},
		{
			name:            "replayed commit",
			peerID:          "peer",
			fromReplay:      true,
			verificationErr: types.ErrVerificationBudgetExhausted,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			counter := &recordingCounter{}
			metrics := NopMetrics()
			metrics.VerificationBudgetDrops = counter
			action := &TryAddCommitAction{metrics: metrics}

			action.handleCommitVerifyError(tc.verificationErr, tc.peerID, tc.fromReplay)

			require.Equal(t, tc.wantDrops, counter.value)
		})
	}
}

// TestHandleCommitVerifyErrorNilQueue ensures a nil queue (as used by tests that
// build the action directly) is tolerated rather than panicking.
func TestHandleCommitVerifyErrorNilQueue(t *testing.T) {
	action := &TryAddCommitAction{}
	assert.NotPanics(t, func() {
		action.handleCommitVerifyError(types.ErrInvalidCommitSignature{}, "peer", false)
	})
}

// TestHandleCommitVerifyErrorQueueFull ensures a saturated queue drops the
// report instead of blocking the consensus goroutine.
func TestHandleCommitVerifyErrorQueueFull(t *testing.T) {
	queue := &chanQueue[peerErrorMsg]{ch: make(chan peerErrorMsg, 1)}
	queue.ch <- peerErrorMsg{PeerID: "other"}

	action := &TryAddCommitAction{peerErrorQueue: queue}
	assert.NotPanics(t, func() {
		action.handleCommitVerifyError(types.ErrInvalidCommitSignature{}, "peer", false)
	})
	assert.Len(t, queue.ch, 1, "the pre-existing report must be preserved")
}
