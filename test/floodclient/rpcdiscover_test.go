//go:build floodclient

package floodclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	rpctypes "github.com/dashpay/tenderdash/rpc/jsonrpc/types"

	"github.com/dashpay/tenderdash/rpc/coretypes"
	"github.com/dashpay/tenderdash/types"
)

// fakeRPCServer stands up an httptest server that speaks the Tenderdash JSON-RPC
// protocol for exactly the three methods discovery calls (status, validators,
// consensus_state), so DiscoverFromRPC can be exercised end-to-end through the
// real HTTP client without a live node.
func fakeRPCServer(t *testing.T, chainID string, result map[string]interface{}) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpctypes.RPCRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		res, ok := result[req.Method]
		require.Truef(t, ok, "unexpected rpc method %q", req.Method)
		resp := req.MakeResponse(res)
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestDiscoverFromRPC_PopulatesParams is the friction-1 proof: pointed at a
// node's RPC, the tool reads the validator set (with each validator's canonical
// index), the quorum type and hash, the chain ID and the current consensus
// height/round — the exact values the operator would otherwise hand-assemble
// into --validators/--quorum-hash/--quorum-type/--chain-id.
func TestDiscoverFromRPC_PopulatesParams(t *testing.T) {
	const chainID = "flood-devnet-1"

	// A realistic three-validator set, each with a real BLS key so the RPC
	// payload marshals exactly as a node's would. The set is returned in the same
	// canonical order the node uses, so the discovered index must be the position.
	vals := make([]*types.Validator, 3)
	for i := range vals {
		vals[i] = &types.Validator{
			ProTxHash:   crypto.RandProTxHash(),
			PubKey:      bls12381.GenPrivKey().PubKey(),
			VotingPower: types.DefaultDashVotingPower,
		}
	}
	quorumHash := crypto.RandQuorumHash()
	thresholdPub := bls12381.GenPrivKey().PubKey()
	quorumType := btcjson.LLMQType_100_67

	validatorsResult := &coretypes.ResultValidators{
		BlockHeight:        41,
		Validators:         vals,
		Count:              len(vals),
		Total:              len(vals),
		ThresholdPublicKey: &thresholdPub,
		QuorumType:         quorumType,
		QuorumHash:         &quorumHash,
	}

	statusResult := &coretypes.ResultStatus{
		NodeInfo: types.NodeInfo{Network: chainID},
	}

	// The node reports its consensus position as "height/round/step". Discovery
	// must read height 42 round 2 out of it (the current consensus height/round,
	// one past the last committed block).
	roundState, err := json.Marshal(map[string]string{"height/round/step": "42/2/3"})
	require.NoError(t, err)
	consensusResult := &coretypes.ResultConsensusState{RoundState: roundState}

	srv := fakeRPCServer(t, chainID, map[string]interface{}{
		"status":          statusResult,
		"validators":      validatorsResult,
		"consensus_state": consensusResult,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	disc, err := DiscoverFromRPC(ctx, srv.URL)
	require.NoError(t, err)

	require.Equal(t, chainID, disc.ChainID, "chain ID must come from the node's status")
	require.Equal(t, quorumType, disc.QuorumType)
	require.Equal(t, []byte(quorumHash), disc.QuorumHash)
	require.EqualValues(t, 42, disc.Height, "height must be the current consensus height, not the committed one")
	require.EqualValues(t, 2, disc.Round)

	require.Len(t, disc.Validators, len(vals))
	for i, got := range disc.Validators {
		require.EqualValues(t, i, got.Index, "validator index must be its canonical position in the set")
		require.Equal(t, []byte(vals[i].ProTxHash), got.ProTxHash,
			"validator %d proTxHash must match the set order", i)
	}
}

// TestDiscoverFromRPC_EmptyValidatorSet asserts discovery fails loudly rather
// than silently producing a run that cannot reach the node's verification path:
// with no validator identities every forged vote is rejected before the budget.
func TestDiscoverFromRPC_EmptyValidatorSet(t *testing.T) {
	srv := fakeRPCServer(t, "x", map[string]interface{}{
		"status":     &coretypes.ResultStatus{NodeInfo: types.NodeInfo{Network: "x"}},
		"validators": &coretypes.ResultValidators{BlockHeight: 1, Validators: nil, Count: 0, Total: 0},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := DiscoverFromRPC(ctx, srv.URL)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty validator set")
}
