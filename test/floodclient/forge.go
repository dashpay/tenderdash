//go:build floodclient

package floodclient

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/cosmos/gogoproto/proto"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	tmcrypto "github.com/dashpay/tenderdash/proto/tendermint/crypto"
	tmbits "github.com/dashpay/tenderdash/proto/tendermint/libs/bits"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

// Profile produces forged consensus messages for a single attack shape. Each
// profile is data, not code duplication: the client routes whatever proto
// message a profile emits to the correct channel by type, so adding an attack
// shape is a matter of implementing Next.
type Profile interface {
	// Name is the selector used on the command line.
	Name() string
	// Next returns the next forged message for the given height/round. Each call
	// should return a distinct message (fresh forged signatures) so the target
	// cannot dedup the flood by content hash — except where a profile
	// deliberately repeats a message to exercise dedup.
	Next(height int64, round int32) proto.Message
}

// ForgedValidator is a real validator identity (proTxHash and its index in the
// validator set) the vote profiles attribute their forged votes to.
//
// A forged vote must carry a real validator's identity to reach the target's
// signature verification at all: the vote set rejects a vote whose proTxHash
// does not match the validator at its index BEFORE it charges the verification
// budget (see types/vote_set.go), so a vote with a random identity is rejected
// cheaply and never exercises the defense the flood is meant to stress. These
// identities are public on a real network (they are on-chain), which is exactly
// why the cheap-flood threat model assumes the attacker has them.
type ForgedValidator struct {
	ProTxHash []byte
	Index     int32
}

// ForgeConfig carries the target-specific data the profiles need to build
// messages that reach verification rather than being rejected at admission.
type ForgeConfig struct {
	// Validators are real validator identities from the target's set. The vote
	// profiles cycle through them so each forged vote is attributed to a real
	// validator and reaches the verification budget. When empty, vote profiles
	// fall back to a random identity — which still exercises dial, handshake,
	// admission and decode, but is rejected before the budget and so does not
	// stress it. On a real devnet, supply the known validator proTxHashes.
	Validators []ForgedValidator

	// QuorumHash is the target's active quorum hash. The commit profile needs it:
	// a commit whose quorum hash disagrees with the node's is rejected before
	// signature verification (and without penalty), so only a commit carrying the
	// real quorum hash reaches — and is charged against — the verification
	// budget. It is obtainable off the live network. When empty, the commit
	// profile uses a random quorum hash (rejected before the budget).
	QuorumHash []byte

	// Signer, when set, is a real validator's signing capability. It is required
	// by the precommit-invalid-extension profile, which needs a genuine block
	// signature. When nil, that profile is not registered at all (the tool cannot
	// forge a valid block signature without a key). On a real network only a
	// validator you control provides this — see SigningIdentity.
	Signer *SigningIdentity

	// CoreChainLockedHeight is the target's committed core-chain-locked height.
	// The proposal profile needs it: a proposal carrying a lower core height is
	// rejected before its signature is verified (and without penalty), so only a
	// proposal at or above the node's core height reaches the signature check
	// that moves ProposalVerifyFailures. It is obtainable off the live network.
	// When zero, the proposal profile forges core height 0, which any chain with
	// a non-zero core height rejects before verification.
	CoreChainLockedHeight uint32
}

// numExtensions is how many vote extensions the extension-carrying precommit
// declares. It is the protocol maximum, which is the whole point of the profile:
// a forged block signature is verified first and fails, so the node charges one
// unit and never reaches the 32 extensions — proving the staged permits charge
// ~1, not 33.
const numExtensions = 32

// randBytes returns n cryptographically-random bytes. Forged signatures and
// hashes are random so every message is unique (defeating content-hash dedup)
// and so verification is the step that rejects them, not deserialization.
func randBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is not recoverable and not expected
	}
	return b
}

// forgeBlockID builds a complete, structurally-valid BlockID with random
// hashes. It passes BlockID.ValidateBasic (hash 32B, part-set header present,
// state ID 32B) so a vote carrying it reaches signature verification.
func forgeBlockID() tmproto.BlockID {
	return tmproto.BlockID{
		Hash: randBytes(crypto.HashSize),
		PartSetHeader: tmproto.PartSetHeader{
			Total: 1,
			Hash:  randBytes(crypto.HashSize),
		},
		StateID: randBytes(crypto.HashSize),
	}
}

// forgeExtensions builds n structurally-valid threshold-recover vote extensions
// with forged signatures. Each passes VoteExtension.Validate (non-default type,
// a non-empty extension paired with a signature no longer than the max), so a
// precommit carrying them passes ValidateBasic and is rejected only at
// verification.
func forgeExtensions(n int) []*tmproto.VoteExtension {
	exts := make([]*tmproto.VoteExtension, n)
	for i := range exts {
		exts[i] = &tmproto.VoteExtension{
			Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
			Extension: randBytes(crypto.HashSize),
			Signature: randBytes(bls12381.SignatureSize),
		}
	}
	return exts
}

// unknownExtensionType is a vote-extension type outside the defined enum
// (DEFAULT, THRESHOLD_RECOVER, THRESHOLD_RECOVER_RAW). The membership check that
// converts a proto extension to a concrete one — reached both by a commit's
// ValidateBasic and by the vote decode path — has no case for it and returns an
// error, so a message carrying it is refused at decode, before any signature is
// verified. proto3's open enums let this undefined varint round-trip intact,
// which is exactly the boundary being probed.
const unknownExtensionType = tmproto.VoteExtensionType(99)

// tooManyExtensions is one past the protocol maximum a legitimate participant
// produces. A message declaring this many extensions is priced above the ceiling
// and refused before conversion — dropped locally, the sender not penalized,
// because an over-long list says nothing about the sender's honesty.
const tooManyExtensions = numExtensions + 1

// forgeUnknownExtensions builds n structurally-present extensions whose type is
// undefined, to drive the unknown-vote-extension-type decode boundary. Their
// contents are irrelevant: the type is rejected before the contents are read.
func forgeUnknownExtensions(n int) []*tmproto.VoteExtension {
	exts := make([]*tmproto.VoteExtension, n)
	for i := range exts {
		exts[i] = &tmproto.VoteExtension{
			Type:      unknownExtensionType,
			Extension: randBytes(crypto.HashSize),
			Signature: randBytes(bls12381.SignatureSize),
		}
	}
	return exts
}

// validatorPicker hands out validator identities in rotation. It is shared by
// every vote-shaped profile so the flood spreads across the validator set
// instead of hammering one lane, and so each forged vote carries a real
// identity that reaches verification.
type validatorPicker struct {
	validators []ForgedValidator
	counter    atomic.Uint64
}

func newValidatorPicker(cfg ForgeConfig) *validatorPicker {
	return &validatorPicker{validators: cfg.Validators}
}

// pick returns the proTxHash and index a forged vote should claim. With no
// configured validators it returns a random proTxHash at index 0, which is
// structurally valid but is rejected before verification.
func (p *validatorPicker) pick() (proTxHash []byte, index int32) {
	if len(p.validators) == 0 {
		return randBytes(crypto.ProTxHashSize), 0
	}
	i := p.counter.Add(1) - 1
	v := p.validators[int(i%uint64(len(p.validators)))]
	return v.ProTxHash, v.Index
}

// prevoteProfile floods prevotes carrying a forged block signature. This is the
// cheap flood: the victim spends ~1 verification unit rejecting each one.
type prevoteProfile struct{ picker *validatorPicker }

func (prevoteProfile) Name() string { return "prevote" }

func (p prevoteProfile) Next(height int64, round int32) proto.Message {
	proTxHash, index := p.picker.pick()
	return &tmcons.Vote{Vote: &tmproto.Vote{
		Type:               tmproto.PrevoteType,
		Height:             height,
		Round:              round,
		BlockID:            forgeBlockID(),
		ValidatorProTxHash: proTxHash,
		ValidatorIndex:     index,
		BlockSignature:     randBytes(bls12381.SignatureSize),
	}}
}

// precommitExtensionsProfile floods precommits that declare the maximum vote
// extensions with a forged block signature. It proves the staged permits charge
// for the block signature alone: the forged block signature fails first, so the
// 32 declared extensions are never verified.
type precommitExtensionsProfile struct{ picker *validatorPicker }

func (precommitExtensionsProfile) Name() string { return "precommit-extensions" }

func (p precommitExtensionsProfile) Next(height int64, round int32) proto.Message {
	proTxHash, index := p.picker.pick()
	return &tmcons.Vote{Vote: &tmproto.Vote{
		Type:               tmproto.PrecommitType,
		Height:             height,
		Round:              round,
		BlockID:            forgeBlockID(),
		ValidatorProTxHash: proTxHash,
		ValidatorIndex:     index,
		BlockSignature:     randBytes(bls12381.SignatureSize),
		VoteExtensions:     forgeExtensions(numExtensions),
	}}
}

// precommitInvalidExtensionProfile floods precommits with a GENUINE block
// signature but an invalid final vote-extension signature. It is the mirror of
// the forged-block-signature profile: because the block signature verifies, the
// node goes on to charge for and verify the extensions, and only the last one
// fails. Where the forged-block-signature precommit proves the staged permits
// charge ~1, this proves the node does pay for the extensions once the block
// signature is real — the other half of the staged-permit contract.
//
// It requires a real validator key (ForgeConfig.Signer) and so is only
// registered when one is supplied; the tool cannot forge a valid block
// signature without it.
type precommitInvalidExtensionProfile struct{ signer *SigningIdentity }

func (precommitInvalidExtensionProfile) Name() string { return "precommit-invalid-extension" }

func (p precommitInvalidExtensionProfile) Next(height int64, round int32) proto.Message {
	proTxHash, err := p.signer.proTxHash(context.Background())
	if err != nil {
		panic(fmt.Errorf("precommit-invalid-extension: proTxHash: %w", err))
	}
	v := &tmproto.Vote{
		Type:               tmproto.PrecommitType,
		Height:             height,
		Round:              round,
		BlockID:            forgeBlockID(),
		ValidatorProTxHash: proTxHash,
		ValidatorIndex:     p.signer.Index,
		VoteExtensions:     forgeExtensions(numExtensions),
	}
	// Sign genuinely: this fills in a valid block signature and valid extension
	// signatures over the vote's own fields.
	if err := p.signer.signVote(context.Background(), v); err != nil {
		panic(fmt.Errorf("precommit-invalid-extension: sign: %w", err))
	}
	// Corrupt the final extension signature so the block signature verifies but
	// the last extension does not — after the node has paid for the rest.
	v.VoteExtensions[len(v.VoteExtensions)-1].Signature = randBytes(bls12381.SignatureSize)
	return &tmcons.Vote{Vote: v}
}

// commitProfile floods commits carrying a forged threshold block signature. A
// commit is not attributable to a signer — the threshold signature has no
// individual identity — but a forged threshold signature is provably malicious,
// so the node evicts the sender after verifying one. That is correct,
// attributable behavior, and unlike the vote floods it is expected to move the
// peer-error path; the flood's leverage comes from rotating identities.
type commitProfile struct{ quorumHash []byte }

func (commitProfile) Name() string { return "commit" }

func (p commitProfile) Next(height int64, round int32) proto.Message {
	quorumHash := p.quorumHash
	if len(quorumHash) == 0 {
		quorumHash = randBytes(crypto.HashSize)
	}
	return &tmcons.Commit{Commit: &tmproto.Commit{
		Height:                  height,
		Round:                   round,
		BlockID:                 forgeBlockID(),
		QuorumHash:              quorumHash,
		ThresholdBlockSignature: randBytes(bls12381.SignatureSize),
	}}
}

// commitUnknownExtensionProfile floods commits carrying a threshold vote
// extension of an undefined type. The commit passes its earlier structural
// checks (block signature size, block ID) and reaches the extension membership
// check, which rejects the unknown type — so the commit is dropped at decode,
// before its threshold signature is verified. The property under test is that
// this is a local drop and never a panic, however malformed the extension list.
type commitUnknownExtensionProfile struct{ quorumHash []byte }

func (commitUnknownExtensionProfile) Name() string { return "commit-unknown-extension" }

func (p commitUnknownExtensionProfile) Next(height int64, round int32) proto.Message {
	quorumHash := p.quorumHash
	if len(quorumHash) == 0 {
		quorumHash = randBytes(crypto.HashSize)
	}
	return &tmcons.Commit{Commit: &tmproto.Commit{
		Height:                  height,
		Round:                   round,
		BlockID:                 forgeBlockID(),
		QuorumHash:              quorumHash,
		ThresholdBlockSignature: randBytes(bls12381.SignatureSize),
		ThresholdVoteExtensions: forgeUnknownExtensions(1),
	}}
}

// commitTooManyExtensionsProfile floods commits declaring more threshold vote
// extensions than the protocol permits, with otherwise well-formed extensions.
// The declared count alone prices the message above the ceiling, so it is
// refused before conversion — without penalizing the sender, since an over-long
// list is a version-skew or bug signal, not a peer offense.
type commitTooManyExtensionsProfile struct{ quorumHash []byte }

func (commitTooManyExtensionsProfile) Name() string { return "commit-too-many-extensions" }

func (p commitTooManyExtensionsProfile) Next(height int64, round int32) proto.Message {
	quorumHash := p.quorumHash
	if len(quorumHash) == 0 {
		quorumHash = randBytes(crypto.HashSize)
	}
	return &tmcons.Commit{Commit: &tmproto.Commit{
		Height:                  height,
		Round:                   round,
		BlockID:                 forgeBlockID(),
		QuorumHash:              quorumHash,
		ThresholdBlockSignature: randBytes(bls12381.SignatureSize),
		ThresholdVoteExtensions: forgeExtensions(tooManyExtensions),
	}}
}

// precommitUnknownExtensionProfile floods precommits carrying a vote extension
// of an undefined type. The vote decode path converts each declared extension
// and rejects the unknown type, so the precommit is refused at decode, before
// its block signature is verified. Mirror of the commit variant on the vote
// path; the property under test is drop-not-panic.
type precommitUnknownExtensionProfile struct{ picker *validatorPicker }

func (precommitUnknownExtensionProfile) Name() string { return "precommit-unknown-extension" }

func (p precommitUnknownExtensionProfile) Next(height int64, round int32) proto.Message {
	proTxHash, index := p.picker.pick()
	return &tmcons.Vote{Vote: &tmproto.Vote{
		Type:               tmproto.PrecommitType,
		Height:             height,
		Round:              round,
		BlockID:            forgeBlockID(),
		ValidatorProTxHash: proTxHash,
		ValidatorIndex:     index,
		BlockSignature:     randBytes(bls12381.SignatureSize),
		VoteExtensions:     forgeUnknownExtensions(1),
	}}
}

// precommitTooManyExtensionsProfile floods precommits declaring more vote
// extensions than the protocol permits. Like the commit variant, the declared
// count prices the message above the ceiling and it is refused before
// conversion, without penalizing the sender.
type precommitTooManyExtensionsProfile struct{ picker *validatorPicker }

func (precommitTooManyExtensionsProfile) Name() string { return "precommit-too-many-extensions" }

func (p precommitTooManyExtensionsProfile) Next(height int64, round int32) proto.Message {
	proTxHash, index := p.picker.pick()
	return &tmcons.Vote{Vote: &tmproto.Vote{
		Type:               tmproto.PrecommitType,
		Height:             height,
		Round:              round,
		BlockID:            forgeBlockID(),
		ValidatorProTxHash: proTxHash,
		ValidatorIndex:     index,
		BlockSignature:     randBytes(bls12381.SignatureSize),
		VoteExtensions:     forgeExtensions(tooManyExtensions),
	}}
}

// proposalProfile floods proposals with a forged signature. A proposal is
// verified against the expected proposer's key for its height and round and is
// not deduplicated, so every copy re-verifies — the ungated, budget-charged
// path. A forged signature moves ProposalVerifyFailures.
//
// It must carry a core-chain-locked height at or above the target's committed
// one, or the proposal is rejected before verification; coreHeight supplies it
// from ForgeConfig. Left zero, the profile forges core height 0, which reaches
// the signature check only on a chain whose core height is itself 0.
type proposalProfile struct{ coreHeight uint32 }

func (proposalProfile) Name() string { return "proposal" }

func (p proposalProfile) Next(height int64, round int32) proto.Message {
	return &tmcons.Proposal{Proposal: tmproto.Proposal{
		Type:                  tmproto.ProposalType,
		Height:                height,
		Round:                 round,
		PolRound:              -1,
		BlockID:               forgeBlockID(),
		Timestamp:             time.Now(),
		Signature:             randBytes(bls12381.SignatureSize),
		CoreChainLockedHeight: p.coreHeight,
	}}
}

// forgeProof builds a structurally-valid but unverifiable merkle proof for a
// block part at index. It carries the maximum aunts (each a random 32-byte
// hash), so it is as large as a valid proof can be, and its index matches the
// part's — both required by Part.ValidateBasic. It hashes to nothing, so the
// part fails proof verification against the part-set root.
func forgeProof(index uint32) tmcrypto.Proof {
	aunts := make([][]byte, crypto.HashSize) // as many aunts as the max allows
	for i := range aunts {
		aunts[i] = randBytes(crypto.HashSize)
	}
	return tmcrypto.Proof{
		Total:    1,
		Index:    int64(index),
		LeafHash: randBytes(crypto.HashSize),
		Aunts:    aunts,
	}
}

// blockPartProfile floods near-maximal block parts with unverifiable proofs. It
// alternates a repeated identical part with a freshly-mutated one: the identical
// copies test that dedup drops the repeat, the mutated ones test that a novel
// part still costs a proof check and is dropped without penalty
// (BlockPartProofDrops).
type blockPartProfile struct {
	counter atomic.Uint64
	fixed   *tmcons.BlockPart
}

func (*blockPartProfile) Name() string { return "blockpart" }

func (p *blockPartProfile) Next(height int64, round int32) proto.Message {
	// Every other message is the same part resent, so the dedup path is exercised
	// alongside the novel-part path.
	if p.counter.Add(1)%2 == 0 && p.fixed != nil {
		return p.fixed
	}
	part := &tmcons.BlockPart{
		Height: height,
		Round:  round,
		Part: tmproto.Part{
			Index: 0,
			// Just under the 64 KB part ceiling so the message is near-maximal
			// but still passes Part.ValidateBasic.
			Bytes: randBytes(int(64*1024 - 1024)),
			Proof: forgeProof(0),
		},
	}
	if p.fixed == nil {
		p.fixed = part
	}
	return part
}

// numElems is the number of 64-bit words a bit array of n bits needs, matching
// libs/bits so a forged bit array decodes rather than being rejected for a
// wrong element count.
func numElems(bits int) int { return (bits + 63) / 64 }

// bigBitArray builds a full bit array of the given size with random contents. It
// is sized at the protocol maximum so it is as large as a structurally-valid one
// can be.
func bigBitArray(nbits int) tmbits.BitArray {
	elems := make([]uint64, numElems(nbits))
	for i := range elems {
		elems[i] = uint64(randBytes(8)[0]) | uint64(randBytes(8)[0])<<8
	}
	return tmbits.BitArray{Bits: int64(nbits), Elems: elems}
}

// maxBitArrayBits is the largest bit array the State/VoteSetBits ceilings admit;
// larger fails ValidateBasic (MaxVotesCount) and is rejected at decode.
const maxBitArrayBits = 10000

// stateProfile floods the State and VoteSetBits channels, which verify no
// signature and carry ceilings of their own. It alternates a NewRoundStep
// (State channel) with a VoteSetBits carrying a maximum-size bit array
// (VoteSetBits channel). Over the per-peer state ceiling these move
// StateChannelDrops.
type stateProfile struct{ counter atomic.Uint64 }

func (*stateProfile) Name() string { return "state" }

func (p *stateProfile) Next(height int64, round int32) proto.Message {
	if p.counter.Add(1)%2 == 0 {
		return &tmcons.VoteSetBits{
			Height:  height,
			Round:   round,
			Type:    tmproto.PrevoteType,
			BlockID: forgeBlockID(),
			Votes:   bigBitArray(maxBitArrayBits),
		}
	}
	return &tmcons.NewRoundStep{
		Height:          height,
		Round:           round,
		Step:            uint32(0x03), // RoundStepPropose; must be in [1,8]
		LastCommitRound: -1,
	}
}

// maj23Profile floods VoteSetMaj23 claims on the State channel. Answering one
// makes the node build a bit array over every validator, so it is priced far
// above the other State-channel messages; over the ceiling it moves
// StateChannelDrops. It alternates a repeated claim (the duplicate path, which
// tests answer suppression) with a novel claim at a fresh round.
//
// The novel claim advances the round rather than changing the block: two
// different majority claims for the SAME height/round/type are a provable
// conflict and are correctly punished by eviction, which is not what this flood
// is meant to exercise. A claim for a new round each time is non-conflicting, so
// the flood is shed over the ceiling without punishing the sender.
type maj23Profile struct {
	counter atomic.Uint64
	fixed   *tmcons.VoteSetMaj23
}

func (*maj23Profile) Name() string { return "maj23" }

func (p *maj23Profile) Next(height int64, round int32) proto.Message {
	n := p.counter.Add(1)
	if n%2 == 0 && p.fixed != nil {
		return p.fixed
	}
	m := &tmcons.VoteSetMaj23{
		Height:  height,
		Round:   round + int32(n), // a fresh, non-conflicting round each time
		Type:    tmproto.PrecommitType,
		BlockID: forgeBlockID(),
	}
	if p.fixed == nil {
		p.fixed = &tmcons.VoteSetMaj23{
			Height:  height,
			Round:   round,
			Type:    tmproto.PrecommitType,
			BlockID: forgeBlockID(),
		}
	}
	return m
}

// BuildProfiles constructs the registry of selectable attack shapes for a
// target described by cfg. Profiles are data: a run selects one by name and the
// client routes whatever message it emits to the right channel by type.
func BuildProfiles(cfg ForgeConfig) map[string]Profile {
	picker := newValidatorPicker(cfg)
	profiles := []Profile{
		prevoteProfile{picker: picker},
		precommitExtensionsProfile{picker: picker},
		commitProfile{quorumHash: cfg.QuorumHash},
		commitUnknownExtensionProfile{quorumHash: cfg.QuorumHash},
		commitTooManyExtensionsProfile{quorumHash: cfg.QuorumHash},
		precommitUnknownExtensionProfile{picker: picker},
		precommitTooManyExtensionsProfile{picker: picker},
		proposalProfile{coreHeight: cfg.CoreChainLockedHeight},
		&blockPartProfile{},
		&stateProfile{},
		&maj23Profile{},
	}
	// The valid-block-signature profile needs a real key; register it only when
	// one is supplied.
	if cfg.Signer != nil {
		profiles = append(profiles, precommitInvalidExtensionProfile{signer: cfg.Signer})
	}
	m := make(map[string]Profile, len(profiles))
	for _, p := range profiles {
		m[p.Name()] = p
	}
	return m
}

// Profiles is the keyless default registry: no target validator identities, so
// vote profiles forge random identities. It is what the CLI lists by name and
// what a run uses until validator identities are supplied.
var Profiles = BuildProfiles(ForgeConfig{})

// MalformedProfiles names the profiles whose messages the node refuses cheaply
// instead of carrying to the verification defense. Two declare a vote-extension
// type outside the defined enum, which the conversion rejects at the decode
// boundary; two declare more extensions than the protocol permits, which prices
// them out before conversion. Their output is deliberately not valid-at-
// verification — that rejection is the property they exist to exercise — so a
// test asserting decode-validity over the whole profile set must exclude them.
// Each profile's own rejection is pinned in the package tests.
func MalformedProfiles() map[string]bool {
	return map[string]bool{
		commitUnknownExtensionProfile{}.Name():     true,
		precommitUnknownExtensionProfile{}.Name():  true,
		commitTooManyExtensionsProfile{}.Name():    true,
		precommitTooManyExtensionsProfile{}.Name(): true,
	}
}
