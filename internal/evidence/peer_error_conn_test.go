package evidence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/libs/log"
)

// A peer error raised for an envelope must name the connection that sent it.
func TestProcessEvidenceChReportsConnID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	inCh := make(chan p2p.Envelope, 1)
	errCh := make(chan p2p.PeerError, 1)
	r := &Reactor{
		logger:     log.NewNopLogger(),
		evidenceCh: p2p.NewChannel(EvidenceChannel, "evidence", inCh, nil, errCh),
	}
	go r.processEvidenceCh(ctx)

	inCh <- p2p.Envelope{From: "peer", ConnID: 7, ChannelID: 0x7f} // unknown channel: rejected
	require.Equal(t, uint64(7), (<-errCh).ConnID)
}
