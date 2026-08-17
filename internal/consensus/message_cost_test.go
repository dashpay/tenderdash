package consensus

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

func testVoteExtensions(n int) []*tmproto.VoteExtension {
	exts := make([]*tmproto.VoteExtension, n)
	for i := range exts {
		exts[i] = &tmproto.VoteExtension{
			Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
			Extension: []byte("extension"),
			Signature: bytes.Repeat([]byte{0x5}, types.SignatureSize),
		}
	}
	return exts
}

func testBlockID() tmproto.BlockID {
	return tmproto.BlockID{
		Hash: bytes.Repeat([]byte{0x1}, crypto.HashSize),
		PartSetHeader: tmproto.PartSetHeader{
			Total: 1,
			Hash:  bytes.Repeat([]byte{0x2}, crypto.HashSize),
		},
		StateID: bytes.Repeat([]byte{0x3}, crypto.HashSize),
	}
}

func testVoteMsg(msgType tmproto.SignedMsgType, blockID tmproto.BlockID, nExtensions int) *tmcons.Vote {
	return &tmcons.Vote{Vote: &tmproto.Vote{
		Type:               msgType,
		Height:             10,
		Round:              1,
		BlockID:            blockID,
		ValidatorProTxHash: bytes.Repeat([]byte{0x6}, crypto.DefaultHashSize),
		BlockSignature:     bytes.Repeat([]byte{0x4}, types.SignatureSize),
		VoteExtensions:     testVoteExtensions(nExtensions),
	}}
}

func testCommitMsg(nExtensions int) *tmcons.Commit {
	return &tmcons.Commit{Commit: &tmproto.Commit{
		Height:                  10,
		Round:                   1,
		BlockID:                 testBlockID(),
		ThresholdBlockSignature: bytes.Repeat([]byte{0x4}, types.SignatureSize),
		ThresholdVoteExtensions: testVoteExtensions(nExtensions),
	}}
}

// The price of every message a peer can put on a consensus channel, in the
// signature verifications it can force. Under-charging a type lets a peer buy
// more verification work than its budget says it may; the exact per-type
// numbers are what makes the budget mean CPU rather than message count.
func TestPeerMessageCost(t *testing.T) {
	nilBlockID := tmproto.BlockID{}

	testCases := []struct {
		name string
		msg  proto.Message
		want int
		why  string
	}{
		{
			name: "prevote",
			msg:  testVoteMsg(tmproto.PrevoteType, testBlockID(), 0),
			want: 1,
			why:  "a prevote forces one block-signature verification and carries no extensions",
		},
		{
			name: "precommit for nil block",
			msg:  testVoteMsg(tmproto.PrecommitType, nilBlockID, 0),
			want: 1,
			why:  "a nil precommit skips the extension pass entirely, leaving one verification",
		},
		{
			name: "precommit without extensions",
			msg:  testVoteMsg(tmproto.PrecommitType, testBlockID(), 0),
			want: 1,
			why:  "a non-nil precommit with nothing to verify but its block signature",
		},
		{
			name: "precommit with four extensions",
			msg:  testVoteMsg(tmproto.PrecommitType, testBlockID(), 4),
			want: 5,
			why:  "one block signature plus one verification per extension",
		},
		{
			name: "precommit with the maximum extensions",
			msg:  testVoteMsg(tmproto.PrecommitType, testBlockID(), types.MaxVoteExtensions),
			want: 33,
			why:  "the most expensive message a peer can send",
		},
		{
			name: "commit without extensions",
			msg:  testCommitMsg(0),
			want: 1,
			why:  "a commit is verified once: the threshold block signature",
		},
		{
			name: "commit with four extensions",
			msg:  testCommitMsg(4),
			want: 5,
			why:  "one threshold block signature plus one verification per extension",
		},
		{
			name: "commit with the maximum extensions",
			msg:  testCommitMsg(types.MaxVoteExtensions),
			want: 33,
			why:  "the most expensive commit a peer can send",
		},
		{
			name: "proposal",
			msg:  &tmcons.Proposal{},
			want: 1,
			why:  "a proposal forces one signature verification and is not deduplicated",
		},
		{
			name: "block part",
			msg:  &tmcons.BlockPart{},
			want: 1,
			why:  "a block part verifies no signature but must still cost a turn",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cost, err := peerMessageCost(tc.msg)
			require.NoError(t, err)
			assert.Equal(t, tc.want, cost, tc.why)
		})
	}
}

// A message declaring more vote extensions than any legitimate participant
// produces is refused a price. Verification would reject it anyway; refusing it
// here keeps the charge derivable from the message alone and bounds the cost of
// the most expensive message a peer can buy.
func TestPeerMessageCost_RejectsExcessiveExtensions(t *testing.T) {
	over := types.MaxVoteExtensions + 1

	_, err := peerMessageCost(testVoteMsg(tmproto.PrecommitType, testBlockID(), over))
	require.ErrorIs(t, err, errTooManyVoteExtensions, "a precommit over the extension cap must not be priced")

	_, err = peerMessageCost(testCommitMsg(over))
	require.ErrorIs(t, err, errTooManyVoteExtensions, "a commit over the extension cap must not be priced")
}

// Every message type the consensus wire union can carry must have an explicit
// price. Pricing an unrecognized type at some fallback would let a message type
// added later be admitted at an invented price, so the mapping is exhaustive and
// this test fails the moment a new type appears without one.
func TestPeerMessageCost_PricesEveryWireMessageType(t *testing.T) {
	wrappers := (&tmcons.Message{}).XXX_OneofWrappers()
	require.NotEmpty(t, wrappers)

	for _, wrapper := range wrappers {
		field := reflect.TypeOf(wrapper).Elem().Field(0)
		msg, ok := reflect.New(field.Type.Elem()).Interface().(proto.Message)
		require.True(t, ok, "%s does not hold a proto message", field.Name)

		t.Run(field.Name, func(t *testing.T) {
			cost, err := peerMessageCost(msg)
			require.NoError(t, err, "message type has no price in the cost model")
			assert.GreaterOrEqual(t, cost, baseMessageCost,
				"every message must cost at least a turn on the consensus goroutine")
		})
	}
}

// A message that is not part of the consensus wire union has no price, and must
// be refused one rather than admitted at the floor: an invented price is work
// charged against the wrong budget, and it would pass unnoticed.
func TestPeerMessageCost_RefusesUnrecognisedMessage(t *testing.T) {
	_, err := peerMessageCost(&tmproto.Vote{})
	require.ErrorIs(t, err, errUnpricedMessageType,
		"a message outside the consensus wire union must not be priced")
}

// The prevote row of the cost table — one verification whatever the message
// declares — holds only because a prevote may not carry vote extensions at all.
// MsgFromProto runs types.Vote.ValidateBasic before the message is dispatched,
// and that rejects extensions on anything but a precommit for a real block.
// Without that rejection a prevote declaring the maximum extensions would force
// 33 verifications for the price of one.
func TestPrevoteWithExtensionsRejectedBeforeVerification(t *testing.T) {
	msg := testVoteMsg(tmproto.PrevoteType, testBlockID(), types.MaxVoteExtensions)

	cost, err := peerMessageCost(msg)
	require.NoError(t, err)
	require.Equal(t, baseMessageCost, cost, "a prevote is priced at one whatever it declares")

	_, err = MsgFromProto(msg)
	require.Error(t, err, "a prevote carrying vote extensions must never reach verification")
	assert.Contains(t, err.Error(), "unexpected vote extensions")
}

// The worst-case costs are what every token bucket charged in these units is
// sized from, so they are pinned here rather than left to be re-derived.
func TestMaxMessageCosts(t *testing.T) {
	assert.Equal(t, 33, maxPrecommitCost, "most expensive precommit")
	assert.Equal(t, 33, maxCommitCost, "most expensive commit")
	assert.Equal(t, maxPrecommitCost, maxPeerMessageCost, "no peer message costs more than a precommit")
}

// The cost is the whole point of the cap: no message may be priced above the
// most expensive one the protocol permits, because the per-peer burst is sized
// from that number.
func TestPeerMessageCost_NeverExceedsMax(t *testing.T) {
	for n := 0; n <= types.MaxVoteExtensions; n++ {
		cost, err := peerMessageCost(testVoteMsg(tmproto.PrecommitType, testBlockID(), n))
		require.NoError(t, err)
		assert.LessOrEqual(t, cost, maxPeerMessageCost)

		cost, err = peerMessageCost(testCommitMsg(n))
		require.NoError(t, err)
		assert.LessOrEqual(t, cost, maxPeerMessageCost)
	}
}
