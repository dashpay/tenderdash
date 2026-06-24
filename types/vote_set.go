package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rs/zerolog"
	sync "github.com/sasha-s/go-deadlock"

	"github.com/dashpay/tenderdash/libs/bits"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

const (
	// MaxVotesCount is the maximum number of votes in a set. Used in ValidateBasic funcs for
	// protection against DOS attacks. Note this implies a corresponding equal limit to
	// the number of validators.
	MaxVotesCount = 10000

	// maxPeerMaj23s bounds how many peers' majority claims a VoteSet tracks.
	// Each new claim from a previously unseen peer may allocate a votesByBlock
	// entry, so unbounded growth is a memory-exhaustion vector. The cap is set
	// well above any realistic node peer count and above the largest supported
	// Dash LLMQ size (LLMQ_400_60 = 400 members). Excess claims are dropped
	// silently (returning nil, not an error) so honest peers are not
	// disconnected by the caller's error handling.
	maxPeerMaj23s = 4096
)

/*
VoteSet helps collect signatures from validators at each height+round for a
predefined vote type.

We need VoteSet to be able to keep track of conflicting votes when validators
double-sign.  Yet, we can't keep track of *all* the votes seen, as that could
be a DoS attack vector.

There are two storage areas for votes.
1. voteSet.votes
2. voteSet.votesByBlock

`.votes` is the "canonical" list of votes.  It always has at least one vote,
if a vote from a validator had been seen at all.  Usually it keeps track of
the first vote seen, but when a 2/3 majority is found, votes for that get
priority and are copied over from `.votesByBlock`.

`.votesByBlock` keeps track of a list of votes for a particular block.  There
are two ways a &blockVotes{} gets created in `.votesByBlock`.
1. the first vote seen by a validator was for the particular block.
2. a peer claims to have seen 2/3 majority for the particular block.

Since the first vote from a validator will always get added in `.votesByBlock`
, all votes in `.votes` will have a corresponding entry in `.votesByBlock`.

When a &blockVotes{} in `.votesByBlock` reaches a 2/3 majority quorum, its
votes are copied into `.votes`.

All this is memory bounded because conflicting votes only get added if a peer
told us to track that block, each peer only gets to tell us 1 such block, and,
there's only a limited number of peers.

NOTE: Assumes that the sum total of voting power does not exceed MaxUInt64.
*/
type VoteSet struct {
	chainID       string
	height        int64
	round         int32
	signedMsgType tmproto.SignedMsgType
	valSet        *ValidatorSet
	logger        log.Logger

	mtx           sync.Mutex
	votesBitArray *bits.BitArray
	votes         []*Vote                // Primary votes to share
	sum           int64                  // Sum of voting power for seen votes, discounting conflicts
	maj23         *BlockID               // First 2/3 majority seen
	votesByBlock  map[string]*blockVotes // string(blockHash|blockParts) -> blockVotes
	peerMaj23s    map[string]maj23Info   // Maj23 for each peer

	// dash fields
	thresholdBlockSig    []byte         // If a 2/3 majority is seen, recover the block sig
	thresholdVoteExtSigs VoteExtensions // If a 2/3 majority is seen, recover the vote extension sigs
}

type maj23Info struct {
	BlockID
	Height int64
	Round  int32
}

// VoteSetOption configures optional VoteSet behavior passed to NewVoteSet.
type VoteSetOption func(*VoteSet)

// WithLogger sets the logger used by the VoteSet. When unset (or passed nil) the
// VoteSet uses a no-op logger, so existing callers stay silent by default.
func WithLogger(logger log.Logger) VoteSetOption {
	return func(voteSet *VoteSet) {
		if logger != nil {
			voteSet.logger = logger
		}
	}
}

// NewVoteSet instantiates all fields of a new vote set. This constructor requires
// that no vote extension data be present on the votes that are added to the set.
func NewVoteSet(chainID string, height int64, round int32,
	signedMsgType tmproto.SignedMsgType, valSet *ValidatorSet, opts ...VoteSetOption) *VoteSet {
	if height == 0 {
		panic("Cannot make VoteSet for height == 0, doesn't make sense.")
	}
	if !valSet.HasPublicKeys {
		panic("Cannot make VoteSet when the validator set doesn't have public keys.")
	}

	voteSet := &VoteSet{
		chainID:       chainID,
		height:        height,
		round:         round,
		signedMsgType: signedMsgType,
		valSet:        valSet,
		logger:        log.NewNopLogger(),
		votesBitArray: bits.NewBitArray(valSet.Size()),
		votes:         make([]*Vote, valSet.Size()),
		sum:           0,
		maj23:         nil,
		votesByBlock:  make(map[string]*blockVotes, valSet.Size()),
		peerMaj23s:    make(map[string]maj23Info),
	}
	for _, opt := range opts {
		opt(voteSet)
	}
	return voteSet
}

func (voteSet *VoteSet) ChainID() string {
	return voteSet.chainID
}

// Implements VoteSetReader.
func (voteSet *VoteSet) GetHeight() int64 {
	if voteSet == nil {
		return 0
	}
	return voteSet.height
}

// Implements VoteSetReader.
func (voteSet *VoteSet) GetRound() int32 {
	if voteSet == nil {
		return -1
	}
	return voteSet.round
}

// Implements VoteSetReader.
func (voteSet *VoteSet) Type() byte {
	if voteSet == nil {
		return 0x00
	}
	return byte(voteSet.signedMsgType)
}

// Implements VoteSetReader.
func (voteSet *VoteSet) Size() int {
	if voteSet == nil {
		return 0
	}
	return voteSet.valSet.Size()
}

// AddVote
// Returns added=true if vote is valid and new.
// Otherwise returns err=ErrVote[
//
//	UnexpectedStep | InvalidIndex | InvalidAddress |
//	InvalidSignature | InvalidBlockHash | ConflictingVotes ]
//
// Duplicate votes return added=false, err=nil.
// Conflicting votes return added=*, err=ErrVoteConflictingVotes.
// NOTE: vote should not be mutated after adding.
// NOTE: VoteSet must not be nil
// NOTE: Vote must not be nil
func (voteSet *VoteSet) AddVote(vote *Vote) (added bool, err error) {
	if voteSet == nil {
		panic("AddVote() on nil VoteSet")
	}
	voteSet.mtx.Lock()
	defer voteSet.mtx.Unlock()

	return voteSet.addVote(vote)
}

// NOTE: Validates as much as possible before attempting to verify the signature.
func (voteSet *VoteSet) addVote(vote *Vote) (added bool, err error) {
	if vote == nil {
		return false, ErrVoteNil
	}
	valIndex := vote.ValidatorIndex
	valProTxHash := vote.ValidatorProTxHash
	blockKey := vote.BlockID.Key()

	// Ensure that validator index was set
	if valIndex < 0 {
		return false, fmt.Errorf("index < 0: %w", ErrVoteInvalidValidatorIndex)
	} else if len(valProTxHash) == 0 {
		return false, fmt.Errorf("empty pro_tx_hash: %w", ErrVoteInvalidValidatorProTxHash)
	}

	// Make sure the step matches.
	if (vote.Height != voteSet.height) ||
		(vote.Round != voteSet.round) ||
		(vote.Type != voteSet.signedMsgType) {
		return false, fmt.Errorf("expected %d/%d/%d, but got %d/%d/%d: %w",
			voteSet.height, voteSet.round, voteSet.signedMsgType,
			vote.Height, vote.Round, vote.Type, ErrVoteUnexpectedStep)
	}

	// Ensure that signer is a validator.
	val := voteSet.valSet.GetByIndex(valIndex)
	if val == nil {
		return false, fmt.Errorf(
			"cannot find validator %d in valSet of size %d: %w",
			valIndex, voteSet.valSet.Size(), ErrVoteInvalidValidatorIndex)
	}

	// Ensure that the signer has the right proTxHash.
	if !bytes.Equal(valProTxHash, val.ProTxHash) {
		return false, fmt.Errorf(
			"vote.ValidatorProTxHash (%X) does not match proTxHash (%X) for vote.ValidatorIndex (%d)\n"+
				"Ensure the genesis file is correct across all validators: %w",
			valProTxHash, val.ProTxHash, valIndex, ErrVoteInvalidValidatorProTxHash)
	}

	// If we already know of this vote, return false.
	if existing, ok := voteSet.getVote(valIndex, blockKey); ok {
		if bytes.Equal(existing.BlockSignature, vote.BlockSignature) {
			return false, nil // duplicate
		}
		return false, fmt.Errorf(
			"existing vote: %v; new vote: %v: %w", existing, vote, ErrVoteNonDeterministicSignature,
		)
	}

	// Check signature.
	err = vote.Verify(
		voteSet.chainID,
		voteSet.valSet.QuorumType,
		voteSet.valSet.QuorumHash,
		val.PubKey,
		val.ProTxHash,
	)
	if err != nil {
		return false, ErrInvalidVoteSignature(
			fmt.Errorf("failed to verify vote with ChainID %s and PubKey %s ProTxHash %s: %w",
				voteSet.chainID, val.PubKey, val.ProTxHash, err))
	}

	// Add vote and get conflicting vote if any.
	added, conflicting := voteSet.addVerifiedVote(vote, blockKey, val.VotingPower)
	if conflicting != nil {
		return added, NewConflictingVoteError(conflicting, vote)
	}
	if !added {
		panic("Expected to add non-conflicting vote")
	}
	return added, nil
}

// Returns (vote, true) if vote exists for valIndex and blockKey.
func (voteSet *VoteSet) getVote(valIndex int32, blockKey string) (vote *Vote, ok bool) {
	if existing := voteSet.votes[valIndex]; existing != nil && existing.BlockID.Key() == blockKey {
		return existing, true
	}
	if existing := voteSet.votesByBlock[blockKey].getByIndex(valIndex); existing != nil {
		return existing, true
	}
	return nil, false
}

// Assumes signature is valid.
// If conflicting vote exists, returns it.
func (voteSet *VoteSet) addVerifiedVote(
	vote *Vote,
	blockKey string,
	votingPower int64,
) (added bool, conflicting *Vote) {
	valIndex := vote.ValidatorIndex

	// Already exists in voteSet.votes?
	if existing := voteSet.votes[valIndex]; existing != nil {
		if existing.BlockID.Equals(vote.BlockID) {
			panic("addVerifiedVote does not expect duplicate votes")
		} else {
			conflicting = existing
		}
		// Replace vote if blockKey matches voteSet.maj23.
		if voteSet.maj23 != nil && voteSet.maj23.Key() == blockKey {
			voteSet.votes[valIndex] = vote
			voteSet.votesBitArray.SetIndex(int(valIndex), true)
		}
		// Otherwise don't add it to voteSet.votes
	} else {
		// Add to voteSet.votes and incr .sum
		voteSet.votes[valIndex] = vote
		voteSet.votesBitArray.SetIndex(int(valIndex), true)
		voteSet.sum += votingPower
	}

	votesByBlock, ok := voteSet.votesByBlock[blockKey]
	if ok {
		if conflicting != nil && !votesByBlock.peerMaj23 {
			// There's a conflict and no peer claims that this block is special.
			return false, conflicting
		}
		// We'll add the vote in a bit.
	} else {
		// .votesByBlock doesn't exist...
		if conflicting != nil {
			// ... and there's a conflicting vote.
			// We're not even tracking this blockKey, so just forget it.
			return false, conflicting
		}
		// ... and there's no conflicting vote.
		// Start tracking this blockKey
		votesByBlock = newBlockVotes(false, voteSet.valSet.Size())
		voteSet.votesByBlock[blockKey] = votesByBlock
		// We'll add the vote in a bit.
	}

	quorum := voteSet.valSet.QuorumVotingThresholdPower()

	// Add vote to votesByBlock
	votesByBlock.addVerifiedVote(vote, votingPower)

	// Once this block reaches the quorum voting power, try to finalize it.
	//
	// We retry on every subsequently added vote (while maj23 is still unset),
	// rather than acting only on the single vote that crosses the threshold,
	// because the minimal quorum-crossing set may not yet contain enough
	// *consistent* votes to recover the threshold signatures - see the SEC-001
	// note below. maj23 is committed only after recovery actually succeeds.
	//
	// TODO(perf): re-attempt full BLS recovery only when a new vote makes a
	// count-group's aggregate power newly cross QuorumTypeThresholdVotingPower.
	// Currently every post-gate vote re-runs O(threshold) BLS interpolations even
	// though only the first successful attempt matters; for large quorums (e.g.
	// LLMQ_400_60) this is O(N·threshold) total work. A per-count running-power
	// cache would let us skip attempts where no count-group gained new qualifying
	// power since the previous attempt. Deferred: correctness first.
	if voteSet.maj23 == nil && quorum <= votesByBlock.sum {
		// Tentatively record the majority block so that the threshold recovery sees
		// the correct "quorum reached" state (IsQuorumReached, used via
		// WithQuorumReached, depends on maj23). It is rolled back below if recovery
		// does not yet succeed, so externally maj23 is observed only once the block
		// is actually finalized (the whole method runs under voteSet.mtx).
		maj23BlockID := vote.BlockID
		voteSet.maj23 = &maj23BlockID
		if voteSet.signedMsgType == tmproto.PrecommitType {
			if err := voteSet.recoverThresholdSignsAndVerify(votesByBlock); err != nil {
				// SEC-001: do NOT halt the whole process on a recovery/verification
				// failure here. A vote-extension count mismatch (zero OR non-zero) can
				// no longer cause this: recoverThresholdSigns recovers from the
				// count-consistent group whose voting power reaches the recovery
				// threshold and excludes any differing-count minority (see
				// canonicalVoteExtensionCount). The remaining reason to fail is timing -
				// the just-crossed minimal quorum may not yet contain enough honest,
				// count-consistent extension shares to reach the threshold - in which
				// case we roll back maj23 and retry as more honest votes arrive. Once the
				// honest count-consistent group reaches the threshold, recovery succeeds
				// and the block is finalized; if it never does, the round simply times
				// out and consensus advances - no fork, no crash.
				//
				// The hard fail is retained only for the genuinely unattributable case:
				// every validator has voted for this block yet still no count is backed
				// by the threshold voting power (i.e. > 1/3 Byzantine power, a BFT-safety
				// violation) or the BLS layer itself is broken. There is then no safe way
				// to continue.
				voteSet.maj23 = nil
				// Roll back the threshold signatures derived from this attempt too:
				// recoverThresholdSigns may have populated thresholdBlockSig/
				// thresholdVoteExtSigs before the later verification failed, and
				// leaving them set would expose stale, unverified signatures keyed
				// off a now-nil maj23. They are recomputed on the next successful
				// recovery (and all external readers gate on maj23 anyway).
				voteSet.thresholdBlockSig = nil
				voteSet.thresholdVoteExtSigs = nil
				if votesByBlock.sum >= voteSet.valSet.TotalVotingPower() {
					panic(fmt.Errorf("failed recovering or verifying threshold signature with all votes present: %w", err))
				}
				// Debug, not Warn/Error: this is an expected, benign retry that fires
				// on every post-gate vote while maj23 is unset (see the SEC-001 note
				// above). It is the only place err is observable on the soft-retry
				// path; the panic path above already wraps it.
				voteSet.logger.Debug("threshold signature recovery not yet possible; rolling back maj23 and retrying as more votes arrive",
					"height", voteSet.height,
					"round", voteSet.round,
					"block_key", blockKey,
					"votes_by_block_sum", votesByBlock.sum,
					"quorum", quorum,
					"err", err,
				)
				// Note: the just-added vote is not lost here. It was recorded in the
				// persistent votesByBlock bucket above (votesByBlock.addVerifiedVote),
				// which is what the next recovery attempt reads. We intentionally skip
				// the voteSet.votes publish step (the copy below) because that happens
				// only on finalization, and maj23 was just rolled back.
				return true, conflicting
			}
		}
		// And also copy votes over to voteSet.votes
		for i, v := range votesByBlock.votes {
			if v != nil {
				voteSet.votes[i] = v
			}
		}
	}

	return true, conflicting
}

// recoverThresholdSignsAndVerify recovers threshold signatures and verifies them.
// precondition: quorum reached
func (voteSet *VoteSet) recoverThresholdSignsAndVerify(blockVotes *blockVotes) error {
	if len(blockVotes.votes) == 0 {
		return nil
	}
	if len(blockVotes.votes) == 1 {
		// there is only 1 validator
		vote := blockVotes.votes[0]
		voteSet.thresholdBlockSig = vote.BlockSignature
		voteSet.thresholdVoteExtSigs = vote.VoteExtensions.
			Filter(func(ext VoteExtensionIf) bool {
				return ext.IsThresholdRecoverable()
			}).Copy()
		return nil
	}
	err := voteSet.recoverThresholdSigns(blockVotes)
	if err != nil {
		return err
	}

	// Build verification sign data from a canonical vote — a vote whose total
	// extension count matches the canonical count selected by recoverThresholdSigns.
	// Using the triggering vote's sign data (as was done previously) is incorrect
	// when the triggering vote carries a Byzantine-crafted extension count that
	// differs from the canonical count: the recovered QuorumSigns would then
	// have a different VoteExtensionSignatures length than the triggering vote's
	// VoteExtensionSignItems, causing VerifyVoteExtensions to return a spurious
	// length-mismatch error even though recovery succeeded (SEC-001 liveness fix).
	//
	// After recoverThresholdSigns returns, voteSet.thresholdVoteExtSigs contains
	// all extensions from the first canonical vote, so its length equals the
	// total canonical extension count. Any vote in blockVotes with that count is
	// guaranteed to be in the honest, count-consistent majority group.
	canonicalExtCount := len(voteSet.thresholdVoteExtSigs)
	var canonicalVote *Vote
	for _, v := range blockVotes.votes {
		if v != nil && len(v.VoteExtensions) == canonicalExtCount {
			canonicalVote = v
			break
		}
	}
	if canonicalVote == nil {
		// Unreachable: recoverThresholdSigns just succeeded using votes with
		// canonicalExtCount extensions, so at least one must exist.
		return fmt.Errorf("internal: no canonical vote (ext count %d) found after successful threshold recovery", canonicalExtCount)
	}
	canonicalSignData, err := MakeQuorumSignsWithVoteSet(voteSet, canonicalVote.ToProto())
	if err != nil {
		return fmt.Errorf("building canonical verification sign data: %w", err)
	}

	sigs := voteSet.makeQuorumSigns()
	// we assume quorum is reached
	return canonicalSignData.Verify(voteSet.valSet.ThresholdPublicKey, sigs)
}

func (voteSet *VoteSet) recoverThresholdSigns(blockVotes *blockVotes) error {
	if len(blockVotes.votes) < 2 {
		return fmt.Errorf("attempting to recover a threshold signature with only 1 vote")
	}

	// Determine the canonical vote-extension count for this block before
	// recovering: the count shared by a set of votes whose aggregate voting power
	// reaches the recovery threshold. Honest validators run the same deterministic
	// ABCI ExtendVote and so all produce the same count; under < 1/3 Byzantine
	// voting power at most one count can reach the threshold (Byzantine validators
	// hold < 1/3 of power, strictly below the recovery threshold for all supported
	// quorum types, so they cannot form a qualifying group on their own), so the
	// canonical count is unambiguous and is the honest one. Votes carrying any other count (a
	// Byzantine minority, with either fewer or more extensions) are excluded from
	// vote-extension recovery but still contribute their block signature. If no
	// count reaches the threshold yet, recovery is not possible; the caller
	// (addVerifiedVote) retries as more votes arrive and only fails hard once
	// every validator has voted (SEC-001).
	canonicalCount, ok := voteSet.canonicalVoteExtensionCount(blockVotes)
	if !ok {
		return fmt.Errorf("no vote-extension count is backed by the recovery threshold voting power")
	}

	// if the vote is voting for nil, then we do not care to Recover the state signature
	signsRecoverer, err := NewSignsRecoverer(blockVotes.votes,
		WithQuorumReached(voteSet.IsQuorumReached()),
		WithCanonicalVoteExtensionCount(canonicalCount),
	)
	if err != nil {
		return err
	}
	thresholdSigns, err := signsRecoverer.Recover()
	if err != nil {
		return err
	}

	voteSet.thresholdBlockSig = thresholdSigns.BlockSign
	voteSet.thresholdVoteExtSigs = signsRecoverer.GetVoteExtensions(*thresholdSigns)
	return nil
}

// canonicalVoteExtensionCount returns the vote-extension count that is backed by
// at least the threshold-signature recovery power among the votes for a single
// block, and whether such a count exists.
//
// Votes are grouped by their extension count and each group's aggregate voting
// power is summed; the count of the group reaching the threshold is canonical.
//
// The bar is the FIXED, LLMQ-type-derived recovery threshold
// (QuorumTypeThresholdVotingPower), NOT the configurable commit gate
// (QuorumVotingThresholdPower): a count-group holding less than the recovery
// threshold cannot produce a verifiable threshold signature anyway, so it can
// never be canonical. Keying off the recovery threshold is also what makes the
// selection correct - the configurable VotingPowerThreshold override can be set
// below 1/2 of the power (see ValidatorSet.BelowStrictThreshold), under which two
// count-groups could both clear it and the tie-break could wrongly pick a
// Byzantine count. The recovery threshold is quorum-type-dependent (see
// QuorumTypeThresholdVotingPower and dash/llmq/llmq.go): production Platform
// types are >= 60% (e.g. LLMQType_100_67 at 67%), while some devnet/test types
// are exactly 50% (e.g. LLMQType_DEVNET, LLMQType_TEST_DIP0024). For types with
// threshold > 1/2, at most one group can reach it by the pigeonhole principle.
// For types at exactly 50%, two equal-power groups could theoretically both reach
// it, but only under BFT-violating (>= 50%) Byzantine stake — under the standard
// < 1/3 Byzantine assumption the Byzantine group holds < 1/3 < 50% <= threshold
// and therefore cannot form a qualifying group on its own. The result is therefore
// deterministic across nodes regardless of map iteration order. Under < 1/3
// Byzantine voting power the honest validators - which share one count - always
// form the sole qualifying group, so the canonical count is the honest one and a
// differing-count Byzantine minority is excluded from vote-extension recovery.
// The smallest-count tie-break below is not exercisable under correct BFT
// operation.
func (voteSet *VoteSet) canonicalVoteExtensionCount(blockVotes *blockVotes) (int, bool) {
	threshold := voteSet.valSet.QuorumTypeThresholdVotingPower()
	powerByCount := make(map[int]int64)
	for _, vote := range blockVotes.votes {
		if vote == nil {
			continue
		}
		val := voteSet.valSet.GetByIndex(vote.ValidatorIndex)
		if val == nil {
			continue
		}
		powerByCount[len(vote.VoteExtensions)] += val.VotingPower
	}

	canonical, found := 0, false
	for count, power := range powerByCount {
		if power >= threshold && (!found || count < canonical) {
			canonical, found = count, true
		}
	}
	return canonical, found
}

// If a peer claims that it has 2/3 majority for given blockKey, call this.
// The number of tracked peers is bounded by maxPeerMaj23s; claims from
// previously unseen peers beyond that cap are ignored.
// TODO: implement ability to remove peers too
// NOTE: VoteSet must not be nil
func (voteSet *VoteSet) SetPeerMaj23(peerID string, blockID BlockID, height int64, round int32) error {
	if voteSet == nil {
		panic("SetPeerMaj23() on nil VoteSet")
	}
	voteSet.mtx.Lock()
	defer voteSet.mtx.Unlock()

	if voteSet.height != height {
		return fmt.Errorf("voteSet height mismatch: %d != %d", voteSet.height, height)
	}

	blockKey := blockID.Key()

	// Make sure peer hasn't already told us something.
	if existing, ok := voteSet.peerMaj23s[peerID]; ok {
		if existing.Equals(blockID) {
			return nil // Nothing to do
		}
		return fmt.Errorf("setPeerMaj23: Received conflicting blockID from peer %v. Got %s (h:%d r:%d), expected %s (h:%d r:%d)",
			peerID, blockID, height, round, existing.BlockID, existing.Height, existing.Round)
	}

	// Bound peer-driven memory growth. Once the cap is reached, claims from
	// new peers are silently dropped (not an error) so honest peers are not
	// disconnected by the caller's error handling. No logger is available on
	// VoteSet; see maxPeerMaj23s doc for the rationale behind the chosen cap.
	if len(voteSet.peerMaj23s) >= maxPeerMaj23s {
		return nil // drop: cap reached
	}
	voteSet.peerMaj23s[peerID] = maj23Info{blockID, height, round}

	// Create .votesByBlock entry if needed.
	votesByBlock, ok := voteSet.votesByBlock[blockKey]
	if ok {
		if votesByBlock.peerMaj23 {
			return nil // Nothing to do
		}
		votesByBlock.peerMaj23 = true
		// No need to copy votes, already there.
	} else {
		votesByBlock = newBlockVotes(true, voteSet.valSet.Size())
		voteSet.votesByBlock[blockKey] = votesByBlock
		// No need to copy votes, no votes to copy over.
	}
	return nil
}

// Implements VoteSetReader.
func (voteSet *VoteSet) BitArray() *bits.BitArray {
	if voteSet == nil {
		return nil
	}
	voteSet.mtx.Lock()
	defer voteSet.mtx.Unlock()
	return voteSet.votesBitArray.Copy()
}

func (voteSet *VoteSet) BitArrayByBlockID(blockID BlockID) *bits.BitArray {
	if voteSet == nil {
		return nil
	}
	voteSet.mtx.Lock()
	defer voteSet.mtx.Unlock()
	votesByBlock, ok := voteSet.votesByBlock[blockID.Key()]
	if ok {
		return votesByBlock.bitArray.Copy()
	}
	return nil
}

// NOTE: if validator has conflicting votes, returns "canonical" vote
// Implements VoteSetReader.
func (voteSet *VoteSet) GetByIndex(valIndex int32) *Vote {
	if voteSet == nil {
		return nil
	}
	voteSet.mtx.Lock()
	defer voteSet.mtx.Unlock()
	if int(valIndex) >= len(voteSet.votes) {
		return nil
	}
	return voteSet.votes[valIndex]
}

// List returns a copy of the list of votes stored by the VoteSet.
func (voteSet *VoteSet) List() []Vote {
	if voteSet == nil || voteSet.votes == nil {
		return nil
	}
	votes := make([]Vote, 0, len(voteSet.votes))
	for i := range voteSet.votes {
		if voteSet.votes[i] != nil {
			votes = append(votes, *voteSet.votes[i])
		}
	}
	return votes
}

func (voteSet *VoteSet) GetByProTxHash(proTxHash []byte) *Vote {
	if voteSet == nil {
		return nil
	}
	voteSet.mtx.Lock()
	defer voteSet.mtx.Unlock()
	valIndex, val := voteSet.valSet.GetByProTxHash(proTxHash)
	if val == nil {
		panic("GetByProTxHash(address) returned nil")
	}
	return voteSet.votes[valIndex]
}

func (voteSet *VoteSet) HasTwoThirdsMajority() bool {
	if voteSet == nil {
		return false
	}
	voteSet.mtx.Lock()
	defer voteSet.mtx.Unlock()
	return voteSet.maj23 != nil
}

// Implements VoteSetReader.
func (voteSet *VoteSet) IsCommit() bool {
	if voteSet == nil {
		return false
	}
	if voteSet.signedMsgType != tmproto.PrecommitType {
		return false
	}
	voteSet.mtx.Lock()
	defer voteSet.mtx.Unlock()
	return voteSet.maj23 != nil
}

// IsQuorumReached returns true if quorum was reached otherwise returns false.
//
// WARNING: this reads maj23 WITHOUT holding voteSet.mtx. It is intentionally
// lock-free because its only caller (recoverThresholdSigns) already runs under
// the lock, and taking it again would deadlock the non-reentrant mutex. It is
// therefore intended for internal use under voteSet.mtx only.
//
// It is also NON-MONOTONIC during threshold recovery: addVerifiedVote
// tentatively sets maj23 and rolls it back if recovery does not yet succeed
// (nil -> tentative -> nil). A concurrent EXTERNAL caller (there are none today)
// could thus both data-race on maj23 and observe a transient true that later
// reverts. Do not call this from outside the locked region; use the locked
// accessors (HasTwoThirdsMajority, TwoThirdsMajority, IsCommit) instead.
func (voteSet *VoteSet) IsQuorumReached() bool {
	return voteSet.maj23 != nil && voteSet.maj23.Hash != nil
}

// HasTwoThirdsAny returns true if we are above voting threshold, regardless of the block id voted
func (voteSet *VoteSet) HasTwoThirdsAny() bool {
	if voteSet == nil {
		return false
	}
	voteSet.mtx.Lock()
	defer voteSet.mtx.Unlock()
	return voteSet.sum >= voteSet.valSet.QuorumVotingThresholdPower()
}

func (voteSet *VoteSet) HasAll() bool {
	if voteSet == nil {
		return false
	}
	voteSet.mtx.Lock()
	defer voteSet.mtx.Unlock()
	return voteSet.sum == voteSet.valSet.TotalVotingPower()
}

// If there was a +2/3 majority for blockID, return blockID and true.
// Else, return the empty BlockID{} and false.
func (voteSet *VoteSet) TwoThirdsMajority() (blockID BlockID, ok bool) {
	if voteSet == nil {
		return BlockID{}, false
	}
	voteSet.mtx.Lock()
	defer voteSet.mtx.Unlock()
	if voteSet.maj23 != nil {
		return *voteSet.maj23, true
	}
	return BlockID{}, false
}

//--------------------------------------------------------------------------------
// Strings and JSON

const nilVoteSetString = "nil-VoteSet"

// String returns a string representation of VoteSet.
//
// See StringIndented.
func (voteSet *VoteSet) String() string {
	if voteSet == nil {
		return nilVoteSetString
	}
	return voteSet.StringIndented("")
}

// StringIndented returns an indented String.
//
// Height Round Type
// Votes
// Votes bit array
// 2/3+ majority
//
// See Vote#String.
func (voteSet *VoteSet) StringIndented(indent string) string {
	if voteSet == nil {
		return nilVoteSetString
	}
	voteSet.mtx.Lock()
	defer voteSet.mtx.Unlock()

	voteStrings := make([]string, len(voteSet.votes))
	for i, vote := range voteSet.votes {
		if vote == nil {
			voteStrings[i] = absentVoteStr
		} else {
			voteStrings[i] = vote.String()
		}
	}
	return fmt.Sprintf(`VoteSet{
%s  H:%v R:%v T:%v
%s  %v
%s  %v
%s  %v
%s}`,
		indent, voteSet.height, voteSet.round, voteSet.signedMsgType,
		indent, strings.Join(voteStrings, "\n"+indent+"  "),
		indent, voteSet.votesBitArray,
		indent, voteSet.peerMaj23s,
		indent)
}

// Marshal the VoteSet to JSON. Same as String(), just in JSON,
// and without the height/round/signedMsgType (since its already included in the votes).
func (voteSet *VoteSet) MarshalJSON() ([]byte, error) {
	if voteSet == nil {
		return nil, nil
	}
	voteSet.mtx.Lock()
	defer voteSet.mtx.Unlock()
	return json.Marshal(VoteSetJSON{
		voteSet.voteStrings(),
		voteSet.bitArrayString(),
		voteSet.peerMaj23s,
	})
}

// More human readable JSON of the vote set
// NOTE: insufficient for unmarshaling from (compressed votes)
// TODO: make the peerMaj23s nicer to read (eg just the block hash)
type VoteSetJSON struct {
	Votes         []string             `json:"votes"`
	VotesBitArray string               `json:"votes_bit_array"`
	PeerMaj23s    map[string]maj23Info `json:"peer_maj_23s"`
}

// Return the bit-array of votes including
// the fraction of power that has voted like:
// "BA{29:xx__x__x_x___x__x_______xxx__} 856/1304 = 0.66"
func (voteSet *VoteSet) BitArrayString() string {
	if voteSet == nil {
		return nilVoteSetString
	}
	voteSet.mtx.Lock()
	defer voteSet.mtx.Unlock()
	return voteSet.bitArrayString()
}

func (voteSet *VoteSet) bitArrayString() string {
	bAString := voteSet.votesBitArray.String()
	voted, total, fracVoted := voteSet.sumTotalFrac()
	return fmt.Sprintf("%s %d/%d = %.2f", bAString, voted, total, fracVoted)
}

// Returns a list of votes compressed to more readable strings.
func (voteSet *VoteSet) VoteStrings() []string {
	if voteSet == nil {
		return []string{}
	}
	voteSet.mtx.Lock()
	defer voteSet.mtx.Unlock()
	return voteSet.voteStrings()
}

func (voteSet *VoteSet) voteStrings() []string {
	voteStrings := make([]string, len(voteSet.votes))
	for i, vote := range voteSet.votes {
		if vote == nil {
			voteStrings[i] = absentVoteStr
		} else {
			voteStrings[i] = vote.String()
		}
	}
	return voteStrings
}

// StringShort returns a short representation of VoteSet.
//
// 1. height
// 2. round
// 3. signed msg type
// 4. first 2/3+ majority
// 5. fraction of voted power
// 6. votes bit array
// 7. 2/3+ majority for each peer
func (voteSet *VoteSet) StringShort() string {
	if voteSet == nil {
		return nilVoteSetString
	}
	voteSet.mtx.Lock()
	defer voteSet.mtx.Unlock()
	_, _, frac := voteSet.sumTotalFrac()
	return fmt.Sprintf(`VoteSet{H:%v R:%v T:%v +2/3:%v(%v) %v %v}`,
		voteSet.height, voteSet.round, voteSet.signedMsgType, voteSet.maj23, frac, voteSet.votesBitArray, voteSet.peerMaj23s)
}

// MarshalZerologObject marshals vote-set into the zerolog structure
func (voteSet *VoteSet) MarshalZerologObject(e *zerolog.Event) {
	if voteSet == nil {
		return
	}
	voteSet.mtx.Lock()
	defer voteSet.mtx.Unlock()
	voted, total, frac := voteSet.sumTotalFrac()
	e.Int64("voted", voted)
	e.Int64("total", total)
	e.Float64("frac", frac)
}

// return the power voted, the total, and the fraction
func (voteSet *VoteSet) sumTotalFrac() (int64, int64, float64) {
	if voteSet.valSet == nil {
		panic("vote set validator set should be set")
	}
	voted, total := voteSet.sum, voteSet.valSet.TotalVotingPower()
	fracVoted := float64(voted) / float64(total)
	return voted, total, fracVoted
}

//--------------------------------------------------------------------------------
// Commit

// MakeCommit constructs a Commit from the VoteSet. It only includes
// precommits for the block, which has 2/3+ majority, and nil.
//
// Panics if the vote type is not PrecommitType or if there's no +2/3 votes for
// a single block.
func (voteSet *VoteSet) MakeCommit() *Commit {
	if voteSet.signedMsgType != tmproto.PrecommitType {
		panic("Cannot MakeCommit() unless VoteSet.Type is PrecommitType")
	}
	voteSet.mtx.Lock()
	defer voteSet.mtx.Unlock()

	// Make sure we have a 2/3 majority
	if voteSet.maj23 == nil {
		panic("Cannot MakeCommit() unless a blockhash has +2/3")
	}

	if voteSet.thresholdBlockSig == nil {
		panic("Cannot MakeCommit() unless a thresholdBlockSig has been created")
	}

	return NewCommit(
		voteSet.GetHeight(),
		voteSet.GetRound(),
		*voteSet.maj23,
		voteSet.thresholdVoteExtSigs,
		voteSet.makeCommitSigns(),
	)
}

func (voteSet *VoteSet) makeCommitSigns() *CommitSigns {
	return &CommitSigns{
		QuorumSigns: QuorumSigns{
			BlockSign:               voteSet.thresholdBlockSig,
			VoteExtensionSignatures: voteSet.thresholdVoteExtSigs.GetSignatures(),
		},
		QuorumHash: voteSet.valSet.QuorumHash,
	}
}

func (voteSet *VoteSet) makeQuorumSigns() QuorumSigns {
	return QuorumSigns{
		BlockSign:               voteSet.thresholdBlockSig,
		VoteExtensionSignatures: voteSet.thresholdVoteExtSigs.GetSignatures(),
	}
}

//--------------------------------------------------------------------------------

/*
Votes for a particular block
There are two ways a *blockVotes gets created for a blockKey.
1. first (non-conflicting) vote of a validator w/ blockKey (peerMaj23=false)
2. A peer claims to have a 2/3 majority w/ blockKey (peerMaj23=true)
*/
type blockVotes struct {
	peerMaj23 bool           // peer claims to have maj23
	bitArray  *bits.BitArray // valIndex -> hasVote?
	votes     []*Vote        // valIndex -> *Vote
	sum       int64          // vote sum
}

func newBlockVotes(peerMaj23 bool, numValidators int) *blockVotes {
	return &blockVotes{
		peerMaj23: peerMaj23,
		bitArray:  bits.NewBitArray(numValidators),
		votes:     make([]*Vote, numValidators),
		sum:       0,
	}
}

func (vs *blockVotes) addVerifiedVote(vote *Vote, votingPower int64) {
	valIndex := vote.ValidatorIndex
	if existing := vs.votes[valIndex]; existing == nil {
		vs.bitArray.SetIndex(int(valIndex), true)
		vs.votes[valIndex] = vote
		vs.sum += votingPower
	}
}

func (vs *blockVotes) getByIndex(index int32) *Vote {
	if vs == nil {
		return nil
	}
	return vs.votes[index]
}

//--------------------------------------------------------------------------------

// Common interface between *consensus.VoteSet and types.Commit
type VoteSetReader interface {
	GetHeight() int64
	GetRound() int32
	Type() byte
	Size() int
	BitArray() *bits.BitArray
	GetByIndex(int32) *Vote
	IsCommit() bool
}
