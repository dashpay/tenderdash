package types

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/dashpay/dashd-go/btcjson"

	"github.com/dashpay/tenderdash/crypto"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

// ErrInvalidCommitBeacon is returned when the random beacon or the app hash a commit carries from
// consensus version 2 does not match the block it commits.
var ErrInvalidCommitBeacon = errors.New("invalid commit random beacon")

// RandomBeaconSignItem returns the sign item of a prevote for blockID at height and round. Every such
// prevote signs these bytes, and the random beacon of the block and round is the threshold signature
// recovered from those prevotes, so it verifies against this item and the quorum's threshold public key.
//
// blockID must carry the state ID derived from the block's header, as prevotes do.
func RandomBeaconSignItem(
	chainID string,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
	height int64,
	round int32,
	blockID BlockID,
) SignItem {
	prevote := tmproto.Vote{
		Type:    tmproto.PrevoteType,
		Height:  height,
		Round:   round,
		BlockID: blockID.ToProto(),
	}
	return MakeBlockSignItem(chainID, &prevote, quorumType, quorumHash)
}

// StateIDWithAppHash returns the header's StateID with its app hash replaced by appHash. From
// consensus version 2, precommits and the commit sign the hash of this state ID with the app hash
// returned by ApplyBlockRandomness.
func (h *Header) StateIDWithAppHash(appHash []byte) tmproto.StateID {
	stateID := h.StateID()
	stateID.AppHash = bytes.Clone(appHash)
	return stateID
}

// VerifyAppHash returns the app hash that commit's state ID commits to for header, the header of the
// block commit commits. It is header.AppHash when commit.BlockID.StateID is the hash of header's
// StateID, as before consensus version 2 and whenever the block's randomness left the app hash
// unchanged, and otherwise commit.AppHash, which then must be the app hash whose state ID the commit
// signs. Any other commit fails.
//
// The threshold signature of a commit covers commit.BlockID.StateID, so once it is verified, the
// returned app hash is the one the validators signed, whatever consensus version the block used.
// commit.AppHash is not covered by the signature: before consensus version 2 a proposer can fill a
// block's LastCommit with an app hash that binds nothing, and the state ID still decides.
func (commit *Commit) VerifyAppHash(header *Header) (tmbytes.HexBytes, error) {
	if commit == nil || header == nil {
		return nil, errors.New("nil commit or header")
	}
	if bytes.Equal(header.StateID().Hash(), commit.BlockID.StateID) {
		return header.AppHash, nil
	}
	if len(commit.AppHash) == 0 {
		return nil, fmt.Errorf("commit state ID %X does not match the header's state ID", commit.BlockID.StateID)
	}
	if err := commit.verifySignedAppHash(header); err != nil {
		return nil, err
	}
	return commit.AppHash, nil
}

// verifySignedAppHash checks that commit.BlockID.StateID is the hash of header's StateID with
// commit.AppHash.
func (commit *Commit) verifySignedAppHash(header *Header) error {
	if len(commit.AppHash) != crypto.DefaultAppHashSize {
		return fmt.Errorf("%w: app hash size is %d, expected %d",
			ErrInvalidCommitBeacon, len(commit.AppHash), crypto.DefaultAppHashSize)
	}
	stateID := header.StateIDWithAppHash(commit.AppHash)
	if !bytes.Equal(stateID.Hash(), commit.BlockID.StateID) {
		return fmt.Errorf("%w: commit state ID %X is not the state ID of the header with app hash %X",
			ErrInvalidCommitBeacon, commit.BlockID.StateID, commit.AppHash)
	}
	return nil
}

// CommittedAppHash returns the app hash the signed header's commit signs: from consensus version 2 the
// one after the block's randomness is applied (Commit.AppHash), otherwise the header's. Proofs of the
// application state at this height verify against it. See Commit.VerifyAppHash.
func (sh SignedHeader) CommittedAppHash() (tmbytes.HexBytes, error) {
	return sh.Commit.VerifyAppHash(sh.Header)
}

// VerifyRandomBeacon checks, for a block at consensus version 2 or later, the random beacon and the
// app hash that commit carries against header, the header of the block it commits, and vals, the
// validator set that voted for it:
//
//   - the beacon is the threshold signature of vals over the prevote sign item rebuilt from header
//     and the commit round (see RandomBeaconSignItem);
//   - commit.BlockID.StateID is the hash of header's StateID with commit.AppHash.
//
// It does not verify the commit's own threshold signature; see ValidatorSet.VerifyCommit.
func (commit *Commit) VerifyRandomBeacon(chainID string, vals *ValidatorSet, header *Header) error {
	if commit == nil || header == nil || vals == nil {
		return fmt.Errorf("%w: nil commit, header or validator set", ErrInvalidCommitBeacon)
	}
	if len(commit.Beacon) != SignatureSize {
		return fmt.Errorf("%w: beacon size is %d, expected %d", ErrInvalidCommitBeacon, len(commit.Beacon), SignatureSize)
	}
	if len(commit.AppHash) == 0 {
		return fmt.Errorf("%w: missing app hash", ErrInvalidCommitBeacon)
	}
	if header.Height != commit.Height {
		return fmt.Errorf("%w: header height %d, commit height %d", ErrInvalidCommitBeacon, header.Height, commit.Height)
	}
	if !bytes.Equal(header.Hash(), commit.BlockID.Hash) {
		return fmt.Errorf("%w: header hash %X does not match commit block hash %X",
			ErrInvalidCommitBeacon, header.Hash(), commit.BlockID.Hash)
	}
	// The commit's own app hash must be the signed one: unlike VerifyAppHash, this does not fall back
	// to the header's when the state IDs happen to match.
	if err := commit.verifySignedAppHash(header); err != nil {
		return err
	}
	prevoteBlockID := BlockID{
		Hash:          commit.BlockID.Hash,
		PartSetHeader: commit.BlockID.PartSetHeader,
		StateID:       header.StateID().Hash(),
	}
	signItem := RandomBeaconSignItem(chainID, vals.QuorumType, vals.QuorumHash, commit.Height, commit.Round, prevoteBlockID)
	if vals.ThresholdPublicKey == nil || !signItem.VerifySignature(vals.ThresholdPublicKey, commit.Beacon) {
		return fmt.Errorf("%w: beacon does not verify against the quorum's threshold public key", ErrInvalidCommitBeacon)
	}
	return nil
}

// WithoutRandomBeacon returns commit without its random beacon and app hash. Before consensus version 2
// both fields must be empty, but neither is covered by the commit's threshold signature, so a peer could
// fill them; nodes drop them from commits of such heights instead of storing or passing them on. It
// returns commit itself when both are already empty.
func (commit *Commit) WithoutRandomBeacon() *Commit {
	if commit == nil || (len(commit.Beacon) == 0 && len(commit.AppHash) == 0) {
		return commit
	}
	stripped := *commit
	stripped.Beacon = nil
	stripped.AppHash = nil
	return &stripped
}
