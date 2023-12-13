package types

import (
	"errors"
	"time"

	"github.com/dashpay/tenderdash/internal/libs/protoio"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

// Canonical* wraps the structs in types for amino encoding them for use in SignBytes / the Signable interface.

// TimeFormat is used for generating the sigs
const TimeFormat = time.RFC3339Nano

//-----------------------------------
// Canonicalize the structs

// CanonicalizeVote transforms the given Proposal to a CanonicalProposal.
func CanonicalizeProposal(chainID string, proposal *tmproto.Proposal) tmproto.CanonicalProposal {
	return tmproto.CanonicalProposal{
		Type:      tmproto.ProposalType,
		Height:    proposal.Height,       // encoded as sfixed64
		Round:     int64(proposal.Round), // encoded as sfixed64
		POLRound:  int64(proposal.PolRound),
		BlockID:   proposal.BlockID.ToCanonicalBlockID(),
		Timestamp: proposal.Timestamp,
		ChainID:   chainID,
	}
}

// CanonicalizeVoteExtension extracts the vote extension from the given vote
// and constructs a CanonicalizeVoteExtension struct, whose representation in
// bytes is what is signed in order to produce the vote extension's signature.
func CanonicalizeVoteExtension(chainID string, ext *tmproto.VoteExtension, height int64, round int32) ([]byte, error) {
	switch ext.Type {
	case tmproto.VoteExtensionType_DEFAULT, tmproto.VoteExtensionType_THRESHOLD_RECOVER:
		{
			canonical := tmproto.CanonicalVoteExtension{
				Extension: ext.Extension,
				Type:      ext.Type,
				Height:    height,
				Round:     int64(round),
				ChainId:   chainID,
			}
			return protoio.MarshalDelimited(&canonical)
		}
	case tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW:
		return ext.Extension, nil
	}
	return nil, errors.New("provided vote extension type does not have canonical form for signing")
}

// CanonicalTime can be used to stringify time in a canonical way.
func CanonicalTime(t time.Time) string {
	// Note that sending time over amino resets it to
	// local time, we need to force UTC here, so the
	// signatures match
	return tmtime.Canonical(t).Format(TimeFormat)
}
