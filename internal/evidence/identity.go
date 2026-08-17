package evidence

import (
	"fmt"
	"sort"

	sync "github.com/sasha-s/go-deadlock"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/types"
)

const (
	// maxTrackedIdentities bounds the identity set. An unbounded set of
	// remembered evidence would itself be a memory target, and the set only
	// ever needs to cover evidence recent enough to still be worth gossiping.
	maxTrackedIdentities = 1024

	// identityEvictionDivisor sets how much of the set is discarded when it
	// fills: evicting a slice at a time rather than a single entry amortizes
	// the ordering scan over many insertions.
	identityEvictionDivisor = 8
)

// evidenceIdentity returns a key for the equivocation a piece of evidence
// alleges. Two messages share a key exactly when they accuse the same validator
// of the same double vote, whatever else differs between them.
//
// The key covers the height, round and step, the accused validator, and the two
// conflicting block IDs. It deliberately excludes the signatures and the ABCI
// fields (voting powers, timestamp): those are the bytes an attacker is free to
// change, and changing them does not change what is being alleged. Keying on
// the evidence hash instead — which covers the whole encoded message — is what
// let one flipped signature byte masquerade as a fresh accusation.
//
// The block IDs are ordered so the key is independent of which vote is carried
// as A and which as B. Fields are hex-encoded and separated by a character hex
// cannot contain, so no field can be shifted into its neighbor to make two
// different accusations collide — a collision would mean holding evidence of
// one silently suppressed the other. Within a block-ID key the components are
// concatenated without separators, so that half of the argument rests on the
// vote validation every decoded message passes first, which fixes the hash,
// part-set header and state ID at their full lengths. The result is digested to
// keep an entry a fixed, small size.
//
// The second return value is false for evidence types that allege no
// equivocation, which have no identity to compare.
func evidenceIdentity(ev types.Evidence) (string, bool) {
	dve, ok := ev.(*types.DuplicateVoteEvidence)
	if !ok || dve.VoteA == nil || dve.VoteB == nil {
		return "", false
	}

	first, second := dve.VoteA.BlockID.Key(), dve.VoteB.BlockID.Key()
	if first > second {
		first, second = second, first
	}

	allegation := fmt.Sprintf("%d/%d/%d/%X/%X/%X",
		dve.VoteA.Height,
		dve.VoteA.Round,
		dve.VoteA.Type,
		dve.VoteA.ValidatorProTxHash,
		first,
		second,
	)
	return string(crypto.Checksum([]byte(allegation))), true
}

// identitySet remembers the equivocations a pool holds evidence for, so a
// re-encoded copy of evidence we already have can be refused without repeating
// the verification that produced it.
//
// Only evidence that survived verification is ever recorded. A set that also
// remembered failures would be a suppression weapon: an attacker who knows a
// real equivocation — it is public gossip — could send it with a corrupted
// signature, and the genuine proof would then be refused forever.
// The set is a bounded, best-effort memory, not a durable guarantee: it holds
// the most recent maxTrackedIdentities equivocations and forgets the oldest, so
// an equivocating validator that mints many provable equivocations at a high
// height can push older ones out and make their re-verification possible again.
// The work budget, not this set, is what bounds the cost of that.
//
// Its mutex is a leaf: nothing here calls back into the pool, so it can be
// taken while the pool's own mutex is held — which processConsensusBuffer does.
type identitySet struct {
	mtx      sync.Mutex
	capacity int
	// heights maps an identity to the height of the evidence that produced it.
	// The height is what eviction orders by: the oldest identities are the ones
	// closest to expiring out of the age window, where a re-verification is
	// cheap to allow again.
	heights map[string]int64
}

func newIdentitySet(capacity int) *identitySet {
	return &identitySet{
		capacity: capacity,
		heights:  make(map[string]int64),
	}
}

// add records the equivocation alleged by ev. Callers must only pass evidence
// the pool has accepted.
func (s *identitySet) add(ev types.Evidence) {
	key, ok := evidenceIdentity(ev)
	if !ok {
		return
	}

	s.mtx.Lock()
	defer s.mtx.Unlock()

	if _, exists := s.heights[key]; exists {
		return
	}
	if len(s.heights) >= s.capacity {
		s.evictOldest()
	}
	s.heights[key] = ev.Height()
}

// has reports whether the set already holds this equivocation. A false negative
// only forfeits a free refusal — the caller still de-duplicates by hash and
// still meters the work — so eviction can never cost correctness.
func (s *identitySet) has(ev types.Evidence) bool {
	key, ok := evidenceIdentity(ev)
	if !ok {
		return false
	}

	s.mtx.Lock()
	defer s.mtx.Unlock()
	_, exists := s.heights[key]
	return exists
}

func (s *identitySet) size() int {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	return len(s.heights)
}

// evictOldest drops the lowest-height slice of the set. Callers hold the mutex.
func (s *identitySet) evictOldest() {
	type entry struct {
		key    string
		height int64
	}

	entries := make([]entry, 0, len(s.heights))
	for key, height := range s.heights {
		entries = append(entries, entry{key: key, height: height})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].height < entries[j].height })

	drop := len(entries) / identityEvictionDivisor
	if drop < 1 {
		drop = 1
	}
	for i := 0; i < drop && i < len(entries); i++ {
		delete(s.heights, entries[i].key)
	}
}
