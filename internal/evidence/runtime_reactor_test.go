package evidence

import (
	"context"
	"testing"
	"time"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/p2p"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

func TestDisabledReactorDropsBeforeDecoding(t *testing.T) {
	pool := NewPool(log.NewNopLogger(), nil, nil, nil, NopMetrics(), nil)
	require.NoError(t, pool.Start(sm.State{}))
	reactor := &Reactor{evpool: pool}
	require.NoError(t, reactor.handleEvidenceMessage(t.Context(), nil))
	require.NoError(t, reactor.broadcastEvidence(t.Context(), tmproto.Evidence{}, nil))
}

type blockedEvidenceChannel struct {
	p2p.Channel
	started chan struct{}
}

func (channel blockedEvidenceChannel) Send(ctx context.Context, _ p2p.Envelope) error {
	close(channel.started)
	<-ctx.Done()
	return ctx.Err()
}

func TestEvidenceTransitionDoesNotWaitForNetwork(t *testing.T) {
	pool := NewPool(log.NewNopLogger(), dbm.NewMemDB(), nil, nil, NopMetrics(), nil)
	state := sm.State{ConsensusParams: *types.DefaultConsensusParams()}
	pool.updateState(state)
	reactor := &Reactor{evpool: pool}
	channel := blockedEvidenceChannel{started: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- reactor.broadcastEvidence(ctx, tmproto.Evidence{}, channel) }()
	<-channel.started
	state.LastBlockHeight++
	state.ConsensusParams.Evidence.MaxBytes = 0
	updated := make(chan struct{})
	go func() { pool.Update(t.Context(), state, nil); close(updated) }()
	select {
	case <-updated:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("evidence transition blocked on peer backpressure")
	}
	require.NoError(t, reactor.broadcastEvidence(t.Context(), tmproto.Evidence{}, channel))
	cancel()
	require.ErrorIs(t, <-result, context.Canceled)
	require.NoError(t, pool.Close())
}
