package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto/ed25519"
	e2e "github.com/dashpay/tenderdash/test/e2e/pkg"
)

func TestSlowApplyMetrics(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"live counters", `tendermint_consensus_block_syncing{chain_id="test"} 1
 tendermint_p2p_peers_connected{chain_id="test"} 2
 tendermint_p2p_peer_receive_bytes_total{message_type="blocksync_BlockResponse",peer_id="a"} 1000
 tendermint_p2p_peer_receive_bytes_total{message_type="blocksync_BlockResponse",peer_id="b"} 2000
 tendermint_p2p_peer_receive_bytes_total{message_type="blocksync_NoBlockResponse",peer_id="a"} 9000
 tendermint_p2p_peer_receive_bytes_total{message_type="blocksync_BlockRequest",peer_id="a"} 40
`, true},
		{"missing instrumentation", "unrelated_metric 1\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
			defer server.Close()
			m, err := readSlowMetrics(context.Background(), server.URL)
			if !tc.valid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, float64(3000), m.responses)
			require.Equal(t, float64(40), m.requests)
			require.Equal(t, float64(2), m.peers)
			// CounterVec does not expose a sample until the first eviction occurs.
			require.Zero(t, m.evicted)
		})
	}
}

func TestSlowApplyRequestsBelongToTarget(t *testing.T) {
	target := &e2e.Node{NodeKey: ed25519.GenPrivKey()}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `tendermint_p2p_peer_receive_bytes_total{message_type="blocksync_BlockRequest",peer_id="%x"} 55
 tendermint_p2p_peer_receive_bytes_total{message_type="blocksync_BlockRequest",peer_id="someone-else"} 9000
 tendermint_p2p_peer_receive_bytes_total{message_type="blocksync_StatusRequest",peer_id="%x"} 8000
`, target.NodeKey.PubKey().Address().Bytes(), target.NodeKey.PubKey().Address().Bytes())
	}))
	defer server.Close()
	endpoint, err := url.Parse(server.URL)
	require.NoError(t, err)
	port, err := strconv.ParseUint(endpoint.Port(), 10, 32)
	require.NoError(t, err)
	total, err := slowRequestBytes(context.Background(), []*e2e.Node{{ProxyPort: uint32(port) - 1000}}, target)
	require.NoError(t, err)
	require.Equal(t, float64(55), total)
}
