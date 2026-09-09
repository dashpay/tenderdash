//go:build floodclient

package floodclient

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/dashpay/dashd-go/btcjson"

	rpchttp "github.com/dashpay/tenderdash/rpc/client/http"
	"github.com/dashpay/tenderdash/rpc/coretypes"
)

// rpcDiscoveryClient is the subset of the Tenderdash RPC client the flood tool
// uses to read a target's parameters off its RPC. The production HTTP client
// (rpc/client/http) satisfies it, and a fake satisfies it in tests, so the
// discovery logic can be exercised without a live node.
type rpcDiscoveryClient interface {
	Status(ctx context.Context) (*coretypes.ResultStatus, error)
	Validators(ctx context.Context, height *int64, page, perPage *int, requestQuorumInfo *bool) (*coretypes.ResultValidators, error)
	ConsensusState(ctx context.Context) (*coretypes.ResultConsensusState, error)
}

// DiscoveredParams is the target description auto-read from a node's RPC. It
// supplies exactly the values a forged vote needs to reach the target's
// verification path — the real validator identities and the active quorum — plus
// the chain ID and the height/round the network is currently on, which the
// operator would otherwise assemble by hand and pass as flags.
type DiscoveredParams struct {
	// ChainID is the target's network, read from its Status node info. A peer
	// whose network does not match is rejected as incompatible.
	ChainID string
	// Validators are the target's real validator identities in canonical
	// (voting-power) order — which is the index order a forged vote must claim.
	Validators []ForgedValidator
	// QuorumType and QuorumHash are the active quorum the commit and signing
	// paths need.
	QuorumType btcjson.LLMQType
	QuorumHash []byte
	// Height and Round are the consensus height/round the target is currently on,
	// used as the default height/round to forge for so the flood is current.
	Height int64
	Round  int32
}

// discoveryPerPage bounds a single validators page request. It matches the
// node's maximum per-page, so a set of any size is retrieved in as few requests
// as the node allows.
const discoveryPerPage = 100

// discover reads the validator set, quorum params, chain ID and current
// consensus height/round from a target's RPC. It is the testable core of
// DiscoverFromRPC: it takes the client interface so a fake RPC can drive it.
func discover(ctx context.Context, client rpcDiscoveryClient) (*DiscoveredParams, error) {
	status, err := client.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("rpc status: %w", err)
	}
	out := &DiscoveredParams{ChainID: status.NodeInfo.Network}

	if err := discoverValidators(ctx, client, out); err != nil {
		return nil, err
	}

	height, round, err := consensusHeightRound(ctx, client)
	if err != nil {
		return nil, err
	}
	out.Height, out.Round = height, round
	return out, nil
}

// discoverValidators fills out.Validators and the quorum params by paging
// through the validator set. Validators come back sorted by voting power (the
// canonical order used for the set's Merkle root), so a validator's index in the
// set is its absolute position across pages — which is the index a forged vote
// must carry to match the identity it claims.
func discoverValidators(ctx context.Context, client rpcDiscoveryClient, out *DiscoveredParams) error {
	requestQuorumInfo := true
	perPage := discoveryPerPage
	for page := 1; ; page++ {
		p := page
		res, err := client.Validators(ctx, nil, &p, &perPage, &requestQuorumInfo)
		if err != nil {
			return fmt.Errorf("rpc validators (page %d): %w", page, err)
		}
		base := (page - 1) * perPage
		for i, v := range res.Validators {
			out.Validators = append(out.Validators, ForgedValidator{
				ProTxHash: v.ProTxHash,
				Index:     int32(base + i),
			})
		}
		// The quorum info rides on every page; record it once available.
		out.QuorumType = res.QuorumType
		if res.QuorumHash != nil {
			out.QuorumHash = *res.QuorumHash
		}
		// Stop when the whole set has been collected or the page came back empty
		// (guards against a Total that disagrees with the pages returned).
		if len(res.Validators) == 0 || len(out.Validators) >= res.Total {
			break
		}
	}
	if len(out.Validators) == 0 {
		return fmt.Errorf("rpc validators: target returned an empty validator set")
	}
	return nil
}

// roundStateSimple is the subset of the node's RoundStateSimple the tool reads:
// the "height/round/step" string, which carries the target's current consensus
// height and round.
type roundStateSimple struct {
	HeightRoundStep string `json:"height/round/step"`
}

// consensusHeightRound reads the target's current consensus height and round
// from its consensus_state round state. This is the height/round the network is
// actually voting on, as opposed to the last committed block height reported by
// Status.
func consensusHeightRound(ctx context.Context, client rpcDiscoveryClient) (int64, int32, error) {
	cs, err := client.ConsensusState(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("rpc consensus_state: %w", err)
	}
	var rs roundStateSimple
	if err := json.Unmarshal(cs.RoundState, &rs); err != nil {
		return 0, 0, fmt.Errorf("decode round state: %w", err)
	}
	height, round, err := parseHeightRound(rs.HeightRoundStep)
	if err != nil {
		return 0, 0, fmt.Errorf("round state %q: %w", rs.HeightRoundStep, err)
	}
	return height, round, nil
}

// parseHeightRound parses the "height/round/step" form the node reports (e.g.
// "42/1/3") into its height and round. Only the first two fields are needed.
func parseHeightRound(hrs string) (int64, int32, error) {
	parts := strings.SplitN(hrs, "/", 3)
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("want height/round/step")
	}
	height, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("height: %w", err)
	}
	round, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("round: %w", err)
	}
	return height, int32(round), nil
}

// DiscoverFromRPC connects to a Tenderdash RPC endpoint (e.g.
// http://host:26657) and reads the target's validator set, quorum params, chain
// ID and current consensus height/round, so the flood run can be pointed at a
// live node without hand-assembling those values. The returned client needs no
// Start: the discovery calls are plain request/response, not subscriptions.
func DiscoverFromRPC(ctx context.Context, rpcURL string) (*DiscoveredParams, error) {
	client, err := rpchttp.New(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("build rpc client for %q: %w", rpcURL, err)
	}
	return discover(ctx, client)
}

// RPCLiveState reads a target's current consensus height and round from its RPC
// on demand. The honest voter uses it to vote at the height/round the network is
// actually on rather than a value captured at startup, which is what makes the
// mixed-mode honest-latency signal meaningful on a live network.
type RPCLiveState struct {
	client rpcDiscoveryClient
}

// NewRPCLiveState builds a live-state source over a Tenderdash RPC endpoint.
func NewRPCLiveState(rpcURL string) (*RPCLiveState, error) {
	client, err := rpchttp.New(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("build rpc client for %q: %w", rpcURL, err)
	}
	return &RPCLiveState{client: client}, nil
}

// CurrentHeightRound returns the target's current consensus height and round.
func (s *RPCLiveState) CurrentHeightRound(ctx context.Context) (int64, int32, error) {
	return consensusHeightRound(ctx, s.client)
}
