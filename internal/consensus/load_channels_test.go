package consensus

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/libs/bits"
	"github.com/dashpay/tenderdash/libs/log"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// The channels that carry no signature verification are not bounded by the
// verification budget at all, so each needs a ceiling of its own. These record
// what an attacker holding every connection slot gets out of them.

// Verifying a block part's merkle proof hashes the whole leaf before it can
// find a mismatch, and a proof that fails leaves the slot empty — so the next
// copy aimed at the same index is hashed from scratch. Neither the part-set
// caps nor the verification budget charges for that.
//
// Repeating one part and mutating it must cost the same, or an attacker gets
// the bound back by changing a byte.
func TestLoadInvalidBlockPartProofsAreBoundedPerPeer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	testCases := []struct {
		name string
		// part returns the nth part the attacker sends.
		part func(n int) *types.Part
	}{
		{
			name: "the same part over and over",
			part: func(int) *types.Part { return invalidProofPart(0) },
		},
		{
			// Every copy is a different message with a different proof, so
			// nothing that remembers what it has already seen can recognize it.
			name: "a part mutated on every attempt",
			part: func(n int) *types.Part {
				part := invalidProofPart(0)
				part.Proof.LeafHash = bytes.Repeat([]byte{byte(n), byte(n >> 8)}, crypto.HashSize/2)
				return part
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()

			clock := clockwork.NewFakeClock()
			// The drop counter is the oracle. Both a refusal and an accepted
			// part return no error, so the error alone cannot say which
			// happened — and a change that stopped verifying proofs altogether
			// would look exactly like the budget refusing every one of them.
			refusals := &syncCounter{}
			metrics := NopMetrics()
			metrics.BlockPartProofDrops = refusals
			action := &AddProposalBlockPartAction{
				logger:          log.NewNopLogger(),
				metrics:         metrics,
				statsQueue:      newChanQueue[msgInfo](),
				partProofBudget: newBlockPartProofBudget(withBlockPartProofClock(clock)),
			}

			// Every connection slot sending as fast as it can, with the clock
			// frozen so nothing refills: what gets through is the allowance
			// each peer started with, and no more.
			const attempts = 1000
			hashed := 0
			for peer := 0; peer < maxConnectionSlots; peer++ {
				stateData := partSetStateData(1)
				before := refusals.count()
				for n := 0; n < attempts; n++ {
					msg := &BlockPartMessage{Height: 1, Round: 0, Part: tc.part(n)}
					_, err := action.addProposalBlockPart(ctx, nil, stateData, msg, attackerID(peer), false)
					if err == nil {
						require.Greater(t, refusals.count(), before,
							"the part was neither hashed nor refused for its proof, so this "+
								"peer stopped for some reason other than the budget")
						break
					}
					require.ErrorIs(t, err, types.ErrPartSetInvalidProof)
					hashed += int(types.BlockPartSizeBytes)
				}
			}

			offered := maxConnectionSlots * attempts * int(types.BlockPartSizeBytes)
			ceiling := maxConnectionSlots * blockPartProofBurstBytes
			reportf(t, "%s: %d peers offered %d MiB of leaf hashing, %d MiB was spent (ceiling %d MiB)",
				tc.name, maxConnectionSlots, offered>>20, hashed>>20, ceiling>>20)
			require.LessOrEqual(t, hashed, ceiling,
				"more leaf hashing was spent on bad proofs than every peer's allowance together")
			require.Positive(t, hashed, "nothing was hashed, so the test never reached the bound")
		})
	}
}

// A VoteSetMaj23 asks this node to build and send a bit array over every
// validator, and a VoteSetBits carries one in. Neither verifies a signature, so
// the verification budget does not see them: their own per-peer and node-wide
// ceilings are all that stands between a peer and the channel goroutine.
//
// The bit arrays here are far larger than any real validator set, because
// nothing on the wire stops a sender declaring one that size.
func TestLoadStateChannelCeilingsAtMaxConnections(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	// Two orders of magnitude above the largest quorum Dash runs.
	const oversizedBits = 10000

	testCases := []struct {
		name     string
		envelope func(from types.NodeID) *p2p.Envelope
		// perPeer is what one peer may spend before its own ceiling stops it.
		perPeer int
	}{
		{
			name:     "majority claims",
			envelope: oversizedMaj23Envelope,
			perPeer:  peerStateRateBurst / maj23TokenCost,
		},
		{
			name:     "vote set bits carrying ten thousand bits",
			envelope: oversizedVoteSetBitsEnvelope(oversizedBits),
			perPeer:  peerStateRateBurst,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Frozen: nothing refills, so what gets through is what the buckets
			// held when the flood started.
			r := newStateRateLimitedReactor(ctx, clockwork.NewFakeClock())
			r.Metrics = NopMetrics()

			// More than one peer's whole bucket, so the ceiling is reached
			// rather than merely approached.
			perPeerAttempts := 2 * tc.perPeer
			admitted, worst := 0, 0
			for peer := 0; peer < maxConnectionSlots; peer++ {
				fromPeer := 0
				for i := 0; i < perPeerAttempts; i++ {
					if r.allowStateChannelMessage(ctx, tc.envelope(attackerID(peer))) {
						fromPeer++
					}
				}
				admitted += fromPeer
				if fromPeer > worst {
					worst = fromPeer
				}
			}

			offered := maxConnectionSlots * perPeerAttempts
			reportf(t, "%s: %d peers offered %d messages, %d admitted (worst single peer %d, its ceiling %d)",
				tc.name, maxConnectionSlots, offered, admitted, worst, tc.perPeer)
			require.LessOrEqual(t, worst, tc.perPeer,
				"one peer got more than its own ceiling allows")
			require.Positive(t, admitted, "nothing was admitted, so the test never exercised the channel")
			require.Less(t, admitted, offered,
				"nothing was refused, so no ceiling was reached")
		})
	}
}

// A majority claim asks this node to build and send a bit array over every
// validator, so it is the most expensive thing either of these channels
// carries. Two ceilings hold it down and neither substitutes for the other: the
// per-peer one keeps a single sender from occupying the channel goroutine, and
// the node-wide one keeps that guarantee from being bought around with fresh
// identities.
//
// Repeating one claim and inventing new ones must both be bounded. Only the
// second is what an attacker with free identities would actually do, and it is
// the one the per-peer ceiling alone would miss.
//
// Bounding is not enough on its own, though: with every slot asking as fast as
// it can, no slot may come away with nothing. A peer that is refused every
// claim it makes cannot find out which votes this node is missing, which is how
// a vote lost to any of these ceilings gets sent again.
func TestLoadMajorityClaimCeilingsAtMaxConnections(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	// Enough distinct claims across every connection slot to pass what the node
	// answers at once — every slot's share plus the surplus — several times over.
	const perPeer = 4 * (maxConnectionSlots*maj23PeerShareBurst + maj23SurplusBurst) / maxConnectionSlots

	testCases := []struct {
		name string
		// claim returns the nth claim peer sends.
		claim func(peer types.NodeID, n int) *p2p.Envelope
	}{
		{
			name:  "one claim repeated",
			claim: func(peer types.NodeID, _ int) *p2p.Envelope { return maj23Envelope(peer) },
		},
		{
			// A claim over a round this node has never been asked about, so no
			// answer it has already given can serve it.
			name: "a new claim every time",
			claim: func(peer types.NodeID, n int) *p2p.Envelope {
				env := maj23Envelope(peer)
				env.Message.(*tmcons.VoteSetMaj23).Round = int32(n)
				return env
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Frozen: nothing refills, so what gets through is what the buckets
			// held when the flood started.
			r := newStateRateLimitedReactor(ctx, clockwork.NewFakeClock())

			// Peer by peer, which is what an attacker asking in a tight loop
			// looks like from here: whoever asks first meets the node-wide
			// ceiling first.
			admitted, worst, starved := 0, 0, 0
			for peer := 0; peer < maxConnectionSlots; peer++ {
				fromPeer := 0
				for n := 0; n < perPeer; n++ {
					if r.allowStateChannelMessage(ctx, tc.claim(attackerID(peer), n)) {
						fromPeer++
					}
				}
				admitted += fromPeer
				if fromPeer > worst {
					worst = fromPeer
				}
				if fromPeer == 0 {
					starved++
				}
			}

			offered := maxConnectionSlots * perPeer
			ceiling := maxConnectionSlots*maj23PeerShareBurst + maj23SurplusBurst
			reportf(t, "%s: %d peers offered %d majority claims, %d admitted "+
				"(most from one peer %d; %d peers answered nothing at all; "+
				"per-peer ceiling %d, one share each plus the surplus %d)",
				tc.name, maxConnectionSlots, offered, admitted, worst, starved,
				peerStateRateBurst/maj23TokenCost, ceiling)
			require.LessOrEqual(t, admitted, ceiling,
				"more majority claims were served than a share per slot plus the surplus allows, "+
					"so fresh identities buy their way past the per-peer one")
			require.Positive(t, admitted,
				"nothing was admitted, so a peer could not learn what this node holds")
			require.Zero(t, starved,
				"a peer was refused every claim it made, so it cannot find out which votes "+
					"this node is missing and a vote dropped by any other ceiling is never resent")
		})
	}
}

// oversizedMaj23Envelope is a majority claim over a round far beyond any this
// node is on, so nothing about the node's own state can shorten the answer.
func oversizedMaj23Envelope(from types.NodeID) *p2p.Envelope {
	return &p2p.Envelope{
		From:      from,
		ChannelID: p2p.ConsensusStateChannel,
		Message:   &tmcons.VoteSetMaj23{Height: 1, Round: 0, Type: tmproto.PrevoteType},
	}
}

// oversizedVoteSetBitsEnvelope carries a bit array of the given size, which is
// what a sender can declare regardless of how many validators there are.
func oversizedVoteSetBitsEnvelope(size int) func(from types.NodeID) *p2p.Envelope {
	array := bits.NewBitArray(size)
	for i := 0; i < size; i++ {
		array.SetIndex(i, i%2 == 0)
	}
	return func(from types.NodeID) *p2p.Envelope {
		return &p2p.Envelope{
			From:      from,
			ChannelID: p2p.VoteSetBitsChannel,
			Message: &tmcons.VoteSetBits{
				Height: 1, Round: 0, Type: tmproto.PrevoteType,
				Votes: *array.ToProto(),
			},
		}
	}
}
