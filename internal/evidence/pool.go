package evidence

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	sync "github.com/sasha-s/go-deadlock"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/cosmos/gogoproto/proto"
	gogotypes "github.com/cosmos/gogoproto/types"
	"github.com/google/orderedcode"

	"github.com/dashpay/tenderdash/internal/eventbus"
	clist "github.com/dashpay/tenderdash/internal/libs/clist"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// key prefixes
// NB: Before modifying these, cross-check them with those in
// * internal/store/store.go    [0..4, 13]
// * internal/state/store.go    [5..8, 14]
// * internal/evidence/pool.go  [9..10]
// * light/store/db/db.go       [11..12]
// TODO(sergio): Move all these to their own package.
// TODO: what about these (they already collide):
// * scripts/scmigrate/migrate.go [3]
// * internal/p2p/peermanager.go  [1]
const (
	// prefixes are unique across all tm db's
	prefixCommitted = int64(9)
	prefixPending   = int64(10)
)

// Pool maintains a pool of valid evidence to be broadcasted and committed
type Pool struct {
	logger log.Logger

	evidenceStore dbm.DB
	evidenceList  *clist.CList // concurrent linked-list of evidence
	evidenceSize  uint32       // amount of pending evidence

	// needed to load headers and commits to verify evidence
	blockStore BlockStore
	stateDB    sm.Store

	// identities records which equivocations the pool already holds evidence
	// for, keyed on what the evidence alleges rather than on its encoding, so a
	// re-signed or otherwise re-encoded copy of evidence we have is recognised
	// as the same accusation. Only accepted evidence is recorded; see
	// identitySet.
	identities *identitySet

	// blockBase and blockTop cache the heights the block store can serve.
	// Asking the store costs an iterator per call, which is more than the single
	// point lookup the answer is meant to save, so the bounds are read once per
	// block instead of once per message.
	blockBase atomic.Int64
	blockTop  atomic.Int64

	mtx sync.Mutex
	// latest state
	state     sm.State
	isStarted bool
	// evidence from consensus is buffered to this slice, awaiting until the next height
	// before being flushed to the pool. This prevents broadcasting and proposing of
	// evidence before the height with which the evidence happened is finished.
	consensusBuffer []duplicateVoteSet

	pruningHeight int64
	pruningTime   time.Time

	// Eventbus to emit events when evidence is validated
	// Not part of the constructor, use SetEventBus to set it
	// The eventBus must be started in order for event publishing not to block
	eventBus *eventbus.EventBus

	Metrics *Metrics
}

// NewPool creates an evidence pool. If using an existing evidence store,
// it will add all pending evidence to the concurrent list.
func NewPool(logger log.Logger, evidenceDB dbm.DB, stateStore sm.Store, blockStore BlockStore, metrics *Metrics, eventBus *eventbus.EventBus) *Pool {
	return &Pool{
		blockStore:      blockStore,
		stateDB:         stateStore,
		logger:          logger,
		evidenceStore:   evidenceDB,
		evidenceList:    clist.New(),
		identities:      newIdentitySet(maxTrackedIdentities),
		consensusBuffer: make([]duplicateVoteSet, 0),
		Metrics:         metrics,
		eventBus:        eventBus,
	}
}

// PendingEvidence is used primarily as part of block proposal and returns up to
// maxNum of uncommitted evidence.
func (evpool *Pool) PendingEvidence(maxBytes int64) ([]types.Evidence, int64) {
	if evpool.Size() == 0 {
		return []types.Evidence{}, 0
	}

	evidence, size, err := evpool.listEvidence(prefixPending, maxBytes)
	if err != nil {
		evpool.logger.Error("failed to retrieve pending evidence", "err", err)
	}

	return evidence, size
}

// Update takes both the new state and the evidence committed at that height and performs
// the following operations:
//  1. Take any conflicting votes from consensus and use the state's LastBlockTime to form
//     DuplicateVoteEvidence and add it to the pool.
//  2. Update the pool's state which contains evidence params relating to expiry.
//  3. Moves pending evidence that has now been committed into the committed pool.
//  4. Removes any expired evidence based on both height and time.
func (evpool *Pool) Update(ctx context.Context, state sm.State, ev types.EvidenceList) {
	// sanity check
	if state.LastBlockHeight <= evpool.state.LastBlockHeight {
		panic(fmt.Sprintf(
			"failed EvidencePool.Update new state height is less than or equal to previous state height: %d <= %d",
			state.LastBlockHeight,
			evpool.state.LastBlockHeight,
		))
	}

	evpool.logger.Debug(
		"updating evidence pool",
		"last_block_height", state.LastBlockHeight,
		"last_block_time", state.LastBlockTime,
	)

	// flush conflicting vote pairs from the buffer, producing DuplicateVoteEvidence and
	// adding it to the pool
	evpool.processConsensusBuffer(ctx, state)
	// update state
	evpool.updateState(state)

	// move committed evidence out from the pending pool and into the committed pool
	evpool.markEvidenceAsCommitted(ev, state.LastBlockHeight)

	evpool.refreshBlockRange()

	// Prune pending evidence when it has expired. This also updates when the next
	// evidence will expire.
	if evpool.Size() > 0 && state.LastBlockHeight > evpool.pruningHeight &&
		state.LastBlockTime.After(evpool.pruningTime) {
		evpool.pruningHeight, evpool.pruningTime = evpool.removeExpiredPendingEvidence()
	}
}

// AddEvidence checks the evidence is valid and adds it to the pool.
func (evpool *Pool) AddEvidence(ctx context.Context, ev types.Evidence) error {
	evpool.logger.Debug("attempting to add evidence", "evidence", ev)

	// Both questions are asked of the same hash, and hashing means marshalling
	// and digesting the whole message, so it is computed once.
	height, hash := ev.Height(), ev.Hash()

	// We have already verified this piece of evidence - no need to do it again
	if evpool.isPendingHash(height, hash) {
		evpool.logger.Debug("evidence already pending; ignoring", "evidence", ev)
		return nil
	}

	// check that the evidence isn't already committed
	if evpool.isCommittedHash(height, hash) {
		// This can happen if the peer that sent us the evidence is behind so we
		// shouldn't punish the peer.
		evpool.logger.Debug("evidence was already committed; ignoring", "evidence", ev)
		return nil
	}

	// 1) Verify against state.
	verified, err := evpool.verify(ctx, ev)
	if err != nil {
		return err
	}

	// 2) Save to store.
	if err := evpool.addPendingEvidence(ctx, ev); err != nil {
		return fmt.Errorf("failed to add evidence to pending list: %w", err)
	}

	// 3) Remember the equivocation, but only now that it is stored and only if
	// its signatures were actually checked.
	evpool.rememberIdentity(ev, verified)

	// 4) Add evidence to clist.
	evpool.evidenceList.PushBack(ev)

	evpool.logger.Info("verified new evidence of byzantine behavior", "evidence", ev)
	return nil
}

// ReportConflictingVotes takes two conflicting votes and forms duplicate vote evidence,
// adding it eventually to the evidence pool.
//
// Duplicate vote attacks happen before the block is committed and the timestamp is
// finalized, thus the evidence pool holds these votes in a buffer, forming the
// evidence from them once consensus at that height has been reached and `Update()` with
// the new state called.
//
// Votes are not verified.
func (evpool *Pool) ReportConflictingVotes(voteA, voteB *types.Vote) {
	evpool.mtx.Lock()
	defer evpool.mtx.Unlock()
	evpool.consensusBuffer = append(evpool.consensusBuffer, duplicateVoteSet{
		VoteA: voteA,
		VoteB: voteB,
	})
}

// CheckEvidence takes an array of evidence from a block and verifies all the evidence there.
// If it has already verified the evidence then it jumps to the next one. It ensures that no
// evidence has already been committed or is being proposed twice. It also adds any
// evidence that it doesn't currently have so that it can quickly form ABCI Evidence later.
func (evpool *Pool) CheckEvidence(ctx context.Context, evList types.EvidenceList) error {
	hashes := make([][]byte, len(evList))
	for idx, ev := range evList {

		// We must verify light client attack evidence regardless because there could be a
		// different conflicting block with the same hash.
		if !evpool.isPending(ev) {
			// check that the evidence isn't already committed
			if evpool.isCommitted(ev) {
				return &types.ErrInvalidEvidence{Evidence: ev, Reason: errors.New("evidence was already committed")}
			}

			verified, err := evpool.verify(ctx, ev)
			if err != nil {
				return err
			}

			if err := evpool.addPendingEvidence(ctx, ev); err != nil {
				// Something went wrong with adding the evidence but we already know it is valid
				// hence we log an error and continue
				evpool.logger.Error("failed to add evidence to pending list", "err", err, "evidence", ev)
			} else {
				evpool.rememberIdentity(ev, verified)
			}

			evpool.logger.Info("check evidence: verified evidence of byzantine behavior", "evidence", ev)
		}

		// check for duplicate evidence. We cache hashes so we don't have to work them out again.
		hashes[idx] = ev.Hash()
		for i := idx - 1; i >= 0; i-- {
			if bytes.Equal(hashes[i], hashes[idx]) {
				return &types.ErrInvalidEvidence{Evidence: ev, Reason: errors.New("duplicate evidence")}
			}
		}
	}

	return nil
}

// EvidenceFront goes to the first evidence in the clist
func (evpool *Pool) EvidenceFront() *clist.CElement {
	return evpool.evidenceList.Front()
}

// EvidenceWaitChan is a channel that closes once the first evidence in the list
// is there. i.e Front is not nil.
func (evpool *Pool) EvidenceWaitChan() <-chan struct{} {
	return evpool.evidenceList.WaitChan()
}

// Size returns the number of evidence in the pool.
func (evpool *Pool) Size() uint32 {
	return atomic.LoadUint32(&evpool.evidenceSize)
}

// State returns the current state of the evpool.
func (evpool *Pool) State() sm.State {
	evpool.mtx.Lock()
	defer evpool.mtx.Unlock()
	return evpool.state
}

func (evpool *Pool) Start(state sm.State) error {
	if evpool.isStarted {
		return errors.New("pool is already running")
	}

	evpool.state = state

	// If pending evidence already in db, in event of prior failure, then check
	// for expiration, update the size and load it back to the evidenceList.
	evpool.refreshBlockRange()
	evpool.pruningHeight, evpool.pruningTime = evpool.removeExpiredPendingEvidence()
	evList, _, err := evpool.listEvidence(prefixPending, -1)
	if err != nil {
		return err
	}

	atomic.StoreUint32(&evpool.evidenceSize, uint32(len(evList)))
	evpool.Metrics.NumEvidence.Set(float64(evpool.evidenceSize))

	for _, ev := range evList {
		evpool.evidenceList.PushBack(ev)
	}

	return nil
}

// hasBlockFor reports whether the block store can serve the block the evidence
// at this height would be verified against. Outside that range verification
// cannot succeed, so there is nothing to gain from attempting it.
//
// The block store, not the pool's own last-block height, is the authority: a
// block is saved before the state update that advances the pool, and a node
// restoring from a state-sync snapshot keeps its pre-sync height for as long as
// the restore takes. Asking the store directly closes both windows, and it also
// covers heights below the pruning base, which no height ceiling can see.
//
// This refuses nothing that verification could have accepted only because Base
// and Height are the lowest and highest block meta the store actually holds —
// so a height with a meta always falls inside them. A store that ever reported
// those bounds independently of the metas would break that.
func (evpool *Pool) hasBlockFor(height int64) bool {
	return height > 0 &&
		height >= evpool.blockBase.Load() &&
		height <= evpool.blockTop.Load()
}

// refreshBlockRange re-reads the heights the block store can serve. It is called
// once per block, not once per message: each read builds a database iterator,
// which is more work than the block-meta lookup hasBlockFor exists to avoid.
//
// Being a block behind is harmless in both directions. Too low a ceiling refuses
// evidence for a height we have just reached, and senders re-offer every pending
// item once a second, so it is a delay. Too high a one admits evidence whose
// lookup then misses, which the work budget has already paid for.
func (evpool *Pool) refreshBlockRange() {
	evpool.blockBase.Store(evpool.blockStore.Base())
	evpool.blockTop.Store(evpool.blockStore.Height())
}

// hasIdentity reports whether the pool already holds evidence of the very same
// equivocation this evidence alleges, however differently it is encoded.
//
// Answering yes is safe to act on: an identity is recorded only when the
// evidence that produced it had its signatures checked, which for a duplicate
// vote means two valid signatures from the accused validator over two different
// blocks in the same step. That cannot be forged, so a match means we already
// hold — and gossip, and can propose — a proof of the same misbehaviour.
func (evpool *Pool) hasIdentity(ev types.Evidence) bool {
	return evpool.identities.has(ev)
}

// rememberIdentity records the equivocation this evidence alleges, so a
// re-encoded copy of it can be refused without repeating the work.
//
// It deliberately refuses to remember evidence whose signatures were not
// checked. Verification is skipped when the validator set at the evidence
// height carries no public keys — a real situation for a node that joined the
// quorum recently — and such evidence is stored anyway. Remembering it would
// hand an attacker an evidence-suppression tool: the equivocations a validator
// commits are public, so a forged copy of one could claim the identity and the
// genuine proof would be refused ever after. Forgetting is the safe direction:
// the worst it costs is a repeated verification.
func (evpool *Pool) rememberIdentity(ev types.Evidence, verified bool) {
	if !verified {
		evpool.logger.Debug("not remembering evidence whose signatures could not be checked",
			"evidence", ev)
		return
	}
	evpool.identities.add(ev)
}

func (evpool *Pool) Close() error {
	return evpool.evidenceStore.Close()
}

// IsExpired checks whether evidence or a polc is expired by checking whether a height and time is older
// than set by the evidence consensus parameters
func (evpool *Pool) isExpired(height int64, time time.Time) bool {
	var (
		params       = evpool.State().ConsensusParams.Evidence
		ageDuration  = evpool.State().LastBlockTime.Sub(time)
		ageNumBlocks = evpool.State().LastBlockHeight - height
	)
	return ageNumBlocks > params.MaxAgeNumBlocks &&
		ageDuration > params.MaxAgeDuration
}

// IsCommitted returns true if we have already seen this exact evidence and it is already marked as committed.
func (evpool *Pool) isCommitted(evidence types.Evidence) bool {
	return evpool.isCommittedHash(evidence.Height(), evidence.Hash())
}

// IsPending checks whether the evidence is already pending. DB errors are passed to the logger.
func (evpool *Pool) isPending(evidence types.Evidence) bool {
	return evpool.isPendingHash(evidence.Height(), evidence.Hash())
}

// alreadyHave reports whether this evidence is pending or committed.
//
// It exists so that answering both questions costs one hash rather than two.
// Hashing means marshalling the whole message and digesting it, which for
// evidence arriving from a peer means up to the channel's size limit — work the
// sender does not pay for, on a path that runs before the work budget is
// consulted.
func (evpool *Pool) alreadyHave(evidence types.Evidence) bool {
	height, hash := evidence.Height(), evidence.Hash()
	return evpool.isPendingHash(height, hash) || evpool.isCommittedHash(height, hash)
}

// isCommittedHash reports whether evidence with this hash has been committed.
// DB errors are passed to the logger.
func (evpool *Pool) isCommittedHash(height int64, hash []byte) bool {
	ok, err := evpool.evidenceStore.Has(keyCommittedFor(height, hash))
	if err != nil {
		evpool.logger.Error("failed to find committed evidence", "err", err)
	}
	return ok
}

// isPendingHash reports whether evidence with this hash is already pending. DB
// errors are passed to the logger.
func (evpool *Pool) isPendingHash(height int64, hash []byte) bool {
	ok, err := evpool.evidenceStore.Has(keyPendingFor(height, hash))
	if err != nil {
		evpool.logger.Error("failed to find pending evidence", "err", err)
	}
	return ok
}

func (evpool *Pool) addPendingEvidence(_ctx context.Context, ev types.Evidence) error {
	evpb, err := types.EvidenceToProto(ev)
	if err != nil {
		return fmt.Errorf("failed to convert to proto: %w", err)
	}

	evBytes, err := evpb.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal evidence: %w", err)
	}

	key := keyPending(ev)

	err = evpool.evidenceStore.Set(key, evBytes)
	if err != nil {
		return fmt.Errorf("failed to persist evidence: %w", err)
	}

	atomic.AddUint32(&evpool.evidenceSize, 1)
	evpool.Metrics.NumEvidence.Set(float64(evpool.evidenceSize))

	// This should normally never be true
	if evpool.eventBus == nil {
		evpool.logger.Debug("event bus is not configured")
		return nil

	}
	return evpool.eventBus.PublishEventEvidenceValidated(types.EventDataEvidenceValidated{
		Evidence: ev,
		Height:   ev.Height(),
	})
}

// markEvidenceAsCommitted processes all the evidence in the block, marking it as
// committed and removing it from the pending database.
func (evpool *Pool) markEvidenceAsCommitted(evidence types.EvidenceList, height int64) {
	blockEvidenceMap := make(map[string]struct{}, len(evidence))
	batch := evpool.evidenceStore.NewBatch()
	defer batch.Close()

	for _, ev := range evidence {
		if evpool.isPending(ev) {
			if err := batch.Delete(keyPending(ev)); err != nil {
				evpool.logger.Error("failed to batch delete pending evidence", "err", err)
			}
			blockEvidenceMap[evMapKey(ev)] = struct{}{}
		}

		// Add evidence to the committed list. As the evidence is stored in the block store
		// we only need to record the height that it was saved at.
		key := keyCommitted(ev)

		h := gogotypes.Int64Value{Value: height}
		evBytes, err := proto.Marshal(&h)
		if err != nil {
			evpool.logger.Error("failed to marshal committed evidence", "key(height/hash)", key, "err", err)
			continue
		}

		if err := evpool.evidenceStore.Set(key, evBytes); err != nil {
			evpool.logger.Error("failed to save committed evidence", "key(height/hash)", key, "err", err)
		}

		evpool.logger.Debug("marked evidence as committed", "evidence", ev)
	}

	// check if we need to remove any pending evidence
	if len(blockEvidenceMap) == 0 {
		return
	}

	// remove committed evidence from pending bucket
	if err := batch.WriteSync(); err != nil {
		evpool.logger.Error("failed to batch delete pending evidence", "err", err)
		return
	}

	// remove committed evidence from the clist
	evpool.removeEvidenceFromList(blockEvidenceMap)

	// update the evidence size
	atomic.AddUint32(&evpool.evidenceSize, ^uint32(len(blockEvidenceMap)-1))
	evpool.Metrics.NumEvidence.Set(float64(evpool.evidenceSize))
}

// listEvidence retrieves lists evidence from oldest to newest within maxBytes.
// If maxBytes is -1, there's no cap on the size of returned evidence.
func (evpool *Pool) listEvidence(prefixKey int64, maxBytes int64) ([]types.Evidence, int64, error) {
	var (
		evSize    int64
		totalSize int64
		evidence  []types.Evidence
		evList    tmproto.EvidenceList // used for calculating the bytes size
	)

	iter, err := dbm.IteratePrefix(evpool.evidenceStore, prefixToBytes(prefixKey))
	if err != nil {
		return nil, totalSize, fmt.Errorf("database error: %w", err)
	}

	defer iter.Close()

	for ; iter.Valid(); iter.Next() {
		var evpb tmproto.Evidence

		if err := evpb.Unmarshal(iter.Value()); err != nil {
			return evidence, totalSize, err
		}

		evList.Evidence = append(evList.Evidence, evpb)
		evSize = int64(evList.Size())

		if maxBytes != -1 && evSize > maxBytes {
			if err := iter.Error(); err != nil {
				return evidence, totalSize, err
			}
			return evidence, totalSize, nil
		}

		ev, err := types.EvidenceFromProto(&evpb)
		if err != nil {
			return nil, totalSize, err
		}

		totalSize = evSize
		evidence = append(evidence, ev)
	}

	if err := iter.Error(); err != nil {
		return evidence, totalSize, err
	}

	return evidence, totalSize, nil
}

func (evpool *Pool) removeExpiredPendingEvidence() (int64, time.Time) {

	batch := evpool.evidenceStore.NewBatch()
	defer batch.Close()

	height, time, blockEvidenceMap := evpool.batchExpiredPendingEvidence(batch)

	// if we haven't removed any evidence then return early
	if len(blockEvidenceMap) == 0 {
		return height, time
	}

	evpool.logger.Debug("removing expired evidence",
		"height", evpool.State().LastBlockHeight,
		"time", evpool.State().LastBlockTime,
		"expired evidence", len(blockEvidenceMap),
	)

	// remove expired evidence from pending bucket
	if err := batch.WriteSync(); err != nil {
		evpool.logger.Error("failed to batch delete pending evidence", "err", err)
		return evpool.State().LastBlockHeight, evpool.State().LastBlockTime
	}

	// remove evidence from the clist
	evpool.removeEvidenceFromList(blockEvidenceMap)
	// update the evidence size
	atomic.AddUint32(&evpool.evidenceSize, ^uint32(len(blockEvidenceMap)-1))

	return height, time
}

func (evpool *Pool) batchExpiredPendingEvidence(batch dbm.Batch) (int64, time.Time, map[string]struct{}) {
	blockEvidenceMap := make(map[string]struct{})
	iter, err := dbm.IteratePrefix(evpool.evidenceStore, prefixToBytes(prefixPending))
	if err != nil {
		evpool.logger.Error("failed to iterate over pending evidence", "err", err)
		return evpool.State().LastBlockHeight, evpool.State().LastBlockTime, blockEvidenceMap
	}
	defer iter.Close()

	for ; iter.Valid(); iter.Next() {
		ev, err := bytesToEv(iter.Value())
		if err != nil {
			evpool.logger.Error("failed to transition evidence from protobuf", "err", err, "ev", ev)
			continue
		}

		// if true, we have looped through all expired evidence
		if !evpool.isExpired(ev.Height(), ev.Time()) {
			// Return the height and time with which this evidence will have expired
			// so we know when to prune next.
			return ev.Height() + evpool.State().ConsensusParams.Evidence.MaxAgeNumBlocks + 1,
				ev.Time().Add(evpool.State().ConsensusParams.Evidence.MaxAgeDuration).Add(time.Second),
				blockEvidenceMap
		}

		// else add to the batch
		if err := batch.Delete(iter.Key()); err != nil {
			evpool.logger.Error("failed to batch delete evidence", "err", err, "ev", ev)
			continue
		}

		// and add to the map to remove the evidence from the clist
		blockEvidenceMap[evMapKey(ev)] = struct{}{}
	}

	return evpool.State().LastBlockHeight, evpool.State().LastBlockTime, blockEvidenceMap
}

func (evpool *Pool) removeEvidenceFromList(
	blockEvidenceMap map[string]struct{}) {

	for e := evpool.evidenceList.Front(); e != nil; e = e.Next() {
		// Remove from clist
		ev := e.Value.(types.Evidence)
		if _, ok := blockEvidenceMap[evMapKey(ev)]; ok {
			evpool.evidenceList.Remove(e)
			e.DetachPrev()
		}
	}
}

func (evpool *Pool) updateState(state sm.State) {
	evpool.mtx.Lock()
	defer evpool.mtx.Unlock()
	evpool.state = state
}

// hasPublicKeys returns true if we have public keys of the current validator set.
func (evpool *Pool) hasPublicKeys() bool {
	evpool.mtx.Lock()
	defer evpool.mtx.Unlock()
	return evpool.state.Validators.HasPublicKeys
}

// processConsensusBuffer converts all the duplicate votes witnessed from consensus
// into DuplicateVoteEvidence. It sets the evidence timestamp to the block height
// from the most recently committed block.
// Evidence is then added to the pool so as to be ready to be broadcasted and proposed.
func (evpool *Pool) processConsensusBuffer(ctx context.Context, state sm.State) {
	evpool.mtx.Lock()
	defer evpool.mtx.Unlock()
	for _, voteSet := range evpool.consensusBuffer {

		// Check the height of the conflicting votes and fetch the corresponding time and validator set
		// to produce the valid evidence
		var (
			dve *types.DuplicateVoteEvidence
			err error
		)
		switch {
		case voteSet.VoteA.Height == state.LastBlockHeight:
			dve, err = types.NewDuplicateVoteEvidence(
				voteSet.VoteA,
				voteSet.VoteB,
				state.LastBlockTime,
				state.LastValidators,
			)

		case voteSet.VoteA.Height < state.LastBlockHeight:
			valSet, dbErr := evpool.stateDB.LoadValidators(voteSet.VoteA.Height, evpool.blockStore)
			if dbErr != nil {
				evpool.logger.Error("failed to load validator set for conflicting votes",
					"height", voteSet.VoteA.Height, "err", err)
				continue
			}
			blockMeta := evpool.blockStore.LoadBlockMeta(voteSet.VoteA.Height)
			if blockMeta == nil {
				evpool.logger.Error("failed to load block time for conflicting votes", "height", voteSet.VoteA.Height)
				continue
			}
			dve, err = types.NewDuplicateVoteEvidence(
				voteSet.VoteA,
				voteSet.VoteB,
				blockMeta.Header.Time,
				valSet,
			)

		default:
			// evidence pool shouldn't expect to get votes from consensus of a height that is above the current
			// state. If this error is seen then perhaps consider keeping the votes in the buffer and retry
			// in following heights
			evpool.logger.Error("inbound duplicate votes from consensus are of a greater height than current state",
				"duplicate vote height", voteSet.VoteA.Height,
				"state.LastBlockHeight", state.LastBlockHeight)
			continue
		}
		if err != nil {
			evpool.logger.Error("error in generating evidence from votes", "err", err)
			continue
		}

		// check if we already have this evidence
		if evpool.isPending(dve) {
			evpool.logger.Debug("evidence already pending; ignoring", "evidence", dve)
			continue
		}

		// check that the evidence is not already committed on chain
		if evpool.isCommitted(dve) {
			evpool.logger.Debug("evidence already committed; ignoring", "evidence", dve)
			continue
		}

		if err := evpool.addPendingEvidence(ctx, dve); err != nil {
			evpool.logger.Error("failed to flush evidence from consensus buffer to pending list: %w", err)
			continue
		}

		// Consensus only reports conflicting votes it has verified the
		// signatures of, so this equivocation is as proven as a peer's would be.
		evpool.rememberIdentity(dve, true)

		evpool.evidenceList.PushBack(dve)

		evpool.logger.Info("verified new evidence of byzantine behavior", "evidence", dve)
	}
	// reset consensus buffer
	evpool.consensusBuffer = make([]duplicateVoteSet, 0)
}

type duplicateVoteSet struct {
	VoteA *types.Vote
	VoteB *types.Vote
}

func bytesToEv(evBytes []byte) (types.Evidence, error) {
	var evpb tmproto.Evidence
	err := evpb.Unmarshal(evBytes)
	if err != nil {
		return &types.DuplicateVoteEvidence{}, err
	}

	return types.EvidenceFromProto(&evpb)
}

func evMapKey(ev types.Evidence) string {
	return string(ev.Hash())
}

func prefixToBytes(prefix int64) []byte {
	key, err := orderedcode.Append(nil, prefix)
	if err != nil {
		panic(err)
	}
	return key
}

func keyCommitted(evidence types.Evidence) []byte {
	return keyCommittedFor(evidence.Height(), evidence.Hash())
}

func keyCommittedFor(height int64, hash []byte) []byte {
	key, err := orderedcode.Append(nil, prefixCommitted, height, string(hash))
	if err != nil {
		panic(err)
	}
	return key
}

func keyPending(evidence types.Evidence) []byte {
	return keyPendingFor(evidence.Height(), evidence.Hash())
}

func keyPendingFor(height int64, hash []byte) []byte {
	key, err := orderedcode.Append(nil, prefixPending, height, string(hash))
	if err != nil {
		panic(err)
	}
	return key
}
