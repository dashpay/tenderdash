package consensus

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/types"
)

func TestVerificationBudgetBurstAdmitsMostExpensiveRemotePrecommit(t *testing.T) {
	now := time.Unix(100, 0)
	budget := newVerificationBudget(1)

	for _, cost := range []int{1, types.MaxVoteExtensions, 1, types.MaxVoteExtensions} {
		require.True(t, budget.allowN(now, cost),
			"a fresh burst must admit both staged verification passes of one legitimate precommit")
	}
	require.False(t, budget.allowN(now, verificationBudgetBurst), "the burst must stay bounded")
}

func TestVerificationBudgetRefillsAtConfiguredRate(t *testing.T) {
	now := time.Unix(100, 0)
	budget := newVerificationBudget(10)

	require.True(t, budget.allowN(now, verificationBudgetBurst))
	require.False(t, budget.allowN(now, 1))
	require.True(t, budget.allowN(now.Add(100*time.Millisecond), 1),
		"ten tokens per second must refill one token in 100ms")
	require.False(t, budget.allowN(now.Add(100*time.Millisecond), 1),
		"refill must be rate-based rather than an unbounded reset")
}

func TestVerificationBudgetZeroDisablesLimit(t *testing.T) {
	now := time.Unix(100, 0)
	budget := newVerificationBudget(0)

	for i := 0; i < 1000; i++ {
		require.True(t, budget.allowN(now, verificationBudgetBurst))
	}
}

func TestPeerVerificationBudgetBypassesLocalAndReplayMessages(t *testing.T) {
	budget := newVerificationBudget(1)
	testCases := []struct {
		name       string
		peerID     types.NodeID
		fromReplay bool
		wantBudget bool
	}{
		{name: "remote peer", peerID: "peer", wantBudget: true},
		{name: "local message"},
		{name: "replayed peer message", peerID: "peer", fromReplay: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := ctxWithPeerVerificationBudget(
				context.Background(),
				tc.peerID,
				tc.fromReplay,
				budget,
			)

			got := verificationBudgetFromCtx(ctx)
			if tc.wantBudget {
				require.Same(t, budget, got)
			} else {
				require.Nil(t, got)
			}
		})
	}
}
