package state

import (
	"bytes"
	"errors"
	"fmt"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/gogo/protobuf/proto"
	"github.com/google/orderedcode"

	abci "github.com/dashpay/tenderdash/abci/types"
	selectproposer "github.com/dashpay/tenderdash/internal/consensus/versioned/selectproposer"
	"github.com/dashpay/tenderdash/libs/log"
	tmmath "github.com/dashpay/tenderdash/libs/math"
	tmstate "github.com/dashpay/tenderdash/proto/tendermint/state"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

const (
	// persist validators every valSetCheckpointInterval blocks to avoid
	// LoadValidators taking too much time.
	// https://github.com/tendermint/tendermint/pull/3438
	// 100000 results in ~ 100ms to get 100 validators (see BenchmarkLoadValidators)
	valSetCheckpointInterval = 100000
)

//------------------------------------------------------------------------

// key prefixes
// NB: Before modifying these, cross-check them with those in
// * internal/store/store.go    [0..4, 13]
// * internal/state/store.go    [5..8, 14]
// * internal/evidence/pool.go  [9..10]
// * light/store/db/db.go       [11..12]
// TODO(thane): Move all these to their own package.
// TODO: what about these (they already collide):
// * scripts/scmigrate/migrate.go [3]
// * internal/p2p/peermanager.go  [1]
const (
	// prefixes are unique across all tm db's
	prefixValidators             = int64(5)
	prefixConsensusParams        = int64(6)
	prefixABCIResponses          = int64(7)
	prefixState                  = int64(8)
	prefixFinalizeBlockResponses = int64(14)
)

func encodeKey(prefix int64, height int64) []byte {
	res, err := orderedcode.Append(nil, prefix, height)
	if err != nil {
		panic(err)
	}
	return res
}

func validatorsKey(height int64) []byte {
	return encodeKey(prefixValidators, height)
}

func consensusParamsKey(height int64) []byte {
	return encodeKey(prefixConsensusParams, height)
}

func abciResponsesKey(height int64) []byte {
	return encodeKey(prefixABCIResponses, height)
}

func finalizeBlockResponsesKey(height int64) []byte {
	return encodeKey(prefixFinalizeBlockResponses, height)
}

// stateKey should never change after being set in init()
var stateKey []byte

func init() {
	var err error
	stateKey, err = orderedcode.Append(nil, prefixState)
	if err != nil {
		panic(err)
	}
}

//----------------------

//go:generate ../../scripts/mockery_generate.sh Store

// Store defines the state store interface
//
// It is used to retrieve current state and save and load ABCI responses,
// validators and consensus parameters
type Store interface {
	// Load loads the current state of the blockchain
	Load() (State, error)
	// LoadValidators loads the validator set that is used to validate the given height
	LoadValidators(int64, selectproposer.BlockStore) (*types.ValidatorSet, error)
	// LoadABCIResponses loads the abciResponse for a given height
	LoadABCIResponses(int64) (*tmstate.ABCIResponses, error)
	// LoadConsensusParams loads the consensus params for a given height
	LoadConsensusParams(int64) (types.ConsensusParams, error)
	// Save overwrites the previous state with the updated one
	Save(State) error
	// SaveABCIResponses saves ABCIResponses for a given height
	SaveABCIResponses(int64, tmstate.ABCIResponses) error
	// SaveValidatorSet saves the validator set at a given height
	SaveValidatorSets(int64, int64, *types.ValidatorSet) error
	// Bootstrap is used for bootstrapping state when not starting from a initial height.
	Bootstrap(State) error
	// PruneStates takes the height from which to prune up to (exclusive)
	PruneStates(int64) error
	// Close closes the connection with the database
	Close() error
}

// dbStore wraps a db (github.com/cometbft/cometbft-db)
type dbStore struct {
	db     dbm.DB
	logger log.Logger
}

var _ Store = (*dbStore)(nil)

// NewStore creates the dbStore of the state pkg.
//
// ## Parameters
//
//   - `db` - the database to use
//   - `logger` - the logger to use; optional, defaults to a nop logger if not provided; if more than one is provided,
//     it will panic
//
// ##Panics
//
// If more than one logger is provided.
func NewStore(db dbm.DB, logger ...log.Logger) Store {
	// To avoid changing the API, we use `logger ...log.Logger` in function signature, so that old code can
	// provide only `db`. In this case, we use NopLogger.
	if len(logger) == 0 || logger[0] == nil {
		logger = []log.Logger{log.NewNopLogger()}
	}

	if len(logger) > 1 {
		panic("NewStore(): maximum one logger is allowed")
	}

	return dbStore{db, logger[0]}
}

// Load loads the State from the database.
func (store dbStore) Load() (State, error) {
	return store.loadState(stateKey)
}

func (store dbStore) loadState(key []byte) (state State, err error) {
	buf, err := store.db.Get(key)
	if err != nil {
		return state, err
	}
	if len(buf) == 0 {
		return state, nil
	}

	sp := new(tmstate.State)

	err = proto.Unmarshal(buf, sp)
	if err != nil {
		// DATA HAS BEEN CORRUPTED OR THE SPEC HAS CHANGED
		panic(fmt.Sprintf("data has been corrupted or its spec has changed: %+v", err))
	}

	sm, err := FromProto(sp)
	if err != nil {
		return state, err
	}
	return *sm, nil
}

// Save persists the State, the ValidatorsInfo, and the ConsensusParamsInfo to the database.
// This flushes the writes (e.g. calls SetSync).
func (store dbStore) Save(state State) error {
	return store.save(state, stateKey)
}

func (store dbStore) save(state State, key []byte) error {
	batch := store.db.NewBatch()
	defer batch.Close()

	// We assume that the state was already updated, so state.LastBlockHeight represents height of already generated
	// block.
	nextBlockHeight := state.LastBlockHeight + 1
	lastHeightValidatorsChanged := state.LastHeightValidatorsChanged
	// If first block, save validators for the block.
	if nextBlockHeight == 1 {
		// This extra logic due to Tendermint validator set changes being delayed 1 block.
		// It may get overwritten due to InitChain validator updates.
		nextBlockHeight = state.InitialHeight
		lastHeightValidatorsChanged = nextBlockHeight
	}
	// Save next validators.
	err := store.saveValidatorsInfo(nextBlockHeight, lastHeightValidatorsChanged, state.Validators, batch)
	if err != nil {
		return err
	}

	// Save next consensus params.
	if err := store.saveConsensusParamsInfo(nextBlockHeight,
		state.LastHeightConsensusParamsChanged, state.ConsensusParams, batch); err != nil {
		return err
	}

	stateBz, err := state.Bytes()
	if err != nil {
		return err
	}

	if err := batch.Set(key, stateBz); err != nil {
		return err
	}

	return batch.WriteSync()
}

// BootstrapState saves a new state, used e.g. by state sync when starting from non-zero height.
func (store dbStore) Bootstrap(state State) error {
	height := state.LastBlockHeight + 1
	if height == 1 {
		height = state.InitialHeight
	}

	batch := store.db.NewBatch()
	defer batch.Close()

	if height > 1 && !state.LastValidators.IsNilOrEmpty() {
		if err := store.saveValidatorsInfo(height-1, height-1, state.LastValidators, batch); err != nil {
			return err
		}
	}

	if err := store.saveValidatorsInfo(height, height, state.Validators, batch); err != nil {
		return err
	}

	if err := store.saveConsensusParamsInfo(height,
		state.LastHeightConsensusParamsChanged, state.ConsensusParams, batch); err != nil {
		return err
	}

	stateBz, err := state.Bytes()
	if err != nil {
		return err
	}

	if err := batch.Set(stateKey, stateBz); err != nil {
		return err
	}

	return batch.WriteSync()
}

// PruneStates deletes states up to the height specified (exclusive). It is not
// guaranteed to delete all states, since the last checkpointed state and states being pointed to by
// e.g. `LastHeightChanged` must remain. The state at retain height must also exist.
// Pruning is done in descending order.
func (store dbStore) PruneStates(retainHeight int64) error {
	if retainHeight <= 0 {
		return fmt.Errorf("height %v must be greater than 0", retainHeight)
	}

	// NOTE: We need to prune consensus params first because the validator
	// sets have always one extra height. If validator sets were pruned first
	// we could get a situation where we prune up to the last validator set
	// yet don't have the respective consensus params at that height and thus
	// return an error
	if err := store.pruneConsensusParams(retainHeight); err != nil {
		return err
	}

	if err := store.pruneValidatorSets(retainHeight); err != nil {
		return err
	}

	if err := store.pruneFinalizeBlockResponses(retainHeight); err != nil {
		return err
	}

	return nil
}

// pruneValidatorSets calls a reverse iterator from base height to retain height (exclusive), deleting
// all validator sets in between. Due to the fact that most validator sets stored reference an earlier
// validator set, it is likely that there will remain one validator set left after pruning.
func (store dbStore) pruneValidatorSets(retainHeight int64) error {
	valInfo, err := loadValidatorsInfo(store.db, retainHeight)
	if err != nil {
		return fmt.Errorf("validators at height %v not found: %w", retainHeight, err)
	}

	// We will prune up to the validator set at the given "height". As we don't save validator sets every
	// height but only when they change or at a check point, it is likely that the validator set at the height
	// we prune to is empty and thus dependent on the validator set saved at a previous height. We must find
	// that validator set and make sure it is not pruned.
	lastRecordedValSetHeight := lastStoredHeightFor(retainHeight, valInfo.LastHeightChanged)
	lastRecordedValSet, err := loadValidatorsInfo(store.db, lastRecordedValSetHeight)
	if err != nil || lastRecordedValSet.ValidatorSet == nil {
		return fmt.Errorf("couldn't find validators at height %d (height %d was originally requested): %w",
			lastStoredHeightFor(retainHeight, valInfo.LastHeightChanged),
			retainHeight,
			err,
		)
	}

	// if this is not equal to the retain height, prune from the retain height to the height above
	// the last saved validator set. This way we can skip over the dependent validator set.
	if lastRecordedValSetHeight < retainHeight {
		err := store.pruneRange(
			validatorsKey(lastRecordedValSetHeight+1),
			validatorsKey(retainHeight),
		)
		if err != nil {
			return err
		}
	}

	// prune all the validators sets up to last saved validator set
	return store.pruneRange(
		validatorsKey(1),
		validatorsKey(lastRecordedValSetHeight),
	)
}

// pruneConsensusParams calls a reverse iterator from base height to retain height batch deleting
// all consensus params in between. If the consensus params at the new base height is dependent
// on a prior height then this will keep that lower height too.
func (store dbStore) pruneConsensusParams(retainHeight int64) error {
	paramsInfo, err := store.loadConsensusParamsInfo(retainHeight)
	if err != nil {
		return fmt.Errorf("consensus params at height %v not found: %w", retainHeight, err)
	}

	// As we don't save the consensus params at every height, only when there is a consensus params change,
	// we must not prune (or save) the last consensus params that the consensus params info at height
	// is dependent on.
	if paramsInfo.ConsensusParams.Equal(&tmproto.ConsensusParams{}) {
		// sanity check that the consensus params at the last height it was changed is there
		lastRecordedConsensusParams, err := store.loadConsensusParamsInfo(paramsInfo.LastHeightChanged)
		if err != nil || lastRecordedConsensusParams.ConsensusParams.Equal(&tmproto.ConsensusParams{}) {
			return fmt.Errorf(
				"couldn't find consensus params at height %d (height %d was originally requested): %w",
				paramsInfo.LastHeightChanged,
				retainHeight,
				err,
			)
		}

		// prune the params above the height with which it last changed and below the retain height.
		err = store.pruneRange(
			consensusParamsKey(paramsInfo.LastHeightChanged+1),
			consensusParamsKey(retainHeight),
		)
		if err != nil {
			return err
		}
	}

	// prune all the consensus params up to either the last height the params changed or if the params
	// last changed at the retain height, then up to the retain height.
	return store.pruneRange(
		consensusParamsKey(1),
		consensusParamsKey(paramsInfo.LastHeightChanged),
	)
}

// pruneFinalizeBlockResponses calls a reverse iterator from base height to retain height
// batch deleting all responses to FinalizeBlock, and legacy ABCI responses, in between
func (store dbStore) pruneFinalizeBlockResponses(height int64) error {
	err := store.pruneRange(finalizeBlockResponsesKey(1), finalizeBlockResponsesKey(height))
	if err == nil {
		// Remove any stale legacy ABCI responses
		err = store.pruneRange(abciResponsesKey(1), abciResponsesKey(height))
	}
	return err
}

// pruneRange is a generic function for deleting a range of keys in reverse order.
// we keep filling up batches of at most 1000 keys, perform a deletion and continue until
// we have gone through all of keys in the range. This avoids doing any writes whilst
// iterating.
func (store dbStore) pruneRange(start []byte, end []byte) error {
	var err error
	batch := store.db.NewBatch()
	defer batch.Close()

	end, err = store.reverseBatchDelete(batch, start, end)
	if err != nil {
		return err
	}

	// iterate until the last batch of the pruning range in which case we will perform a
	// write sync
	for !bytes.Equal(start, end) {
		if err := batch.Write(); err != nil {
			return err
		}

		if err := batch.Close(); err != nil {
			return err
		}

		batch = store.db.NewBatch()

		// fill a new batch of keys for deletion over the remainding range
		end, err = store.reverseBatchDelete(batch, start, end)
		if err != nil {
			return err
		}
	}

	return batch.WriteSync()
}

// reverseBatchDelete runs a reverse iterator (from end to start) filling up a batch until either
// (a) the iterator reaches the start or (b) the iterator has added a 1000 keys (this avoids the
// batch from growing too large)
func (store dbStore) reverseBatchDelete(batch dbm.Batch, start, end []byte) ([]byte, error) {
	iter, err := store.db.ReverseIterator(start, end)
	if err != nil {
		return end, fmt.Errorf("iterator error: %w", err)
	}
	defer iter.Close()

	size := 0
	for ; iter.Valid(); iter.Next() {
		if err := batch.Delete(iter.Key()); err != nil {
			return end, fmt.Errorf("pruning error at key %X: %w", iter.Key(), err)
		}

		// avoid batches growing too large by capping them
		size++
		if size == 1000 {
			return iter.Key(), iter.Error()
		}
	}
	return start, iter.Error()
}

//------------------------------------------------------------------------

// LoadABCIResponses loads the ABCIResponses for the given height from the
// database. If not found, ErrNoABCIResponsesForHeight is returned.
//
// This is useful for recovering from crashes where we called app.Commit and
// before we called s.Save(). It can also be used to produce Merkle proofs of
// the result of txs.
func (store dbStore) LoadABCIResponses(height int64) (*tmstate.ABCIResponses, error) {
	buf, err := store.db.Get(abciResponsesKey(height))
	if err != nil {
		return nil, err
	}
	if len(buf) == 0 {

		return nil, ErrNoABCIResponsesForHeight{height}
	}

	abciResponses := new(tmstate.ABCIResponses)
	err = abciResponses.Unmarshal(buf)
	if err != nil {
		// DATA HAS BEEN CORRUPTED OR THE SPEC HAS CHANGED
		panic(fmt.Sprintf("data has been corrupted or its spec has changed: %+v", err))
	}
	// TODO: ensure that buf is completely read.

	return abciResponses, nil
}

// SaveABCIResponses persists the ABCIResponses to the database.
// This is useful in case we crash after app.Commit and before s.Save().
// Responses are indexed by height so they can also be loaded later to produce
// Merkle proofs.
//
// Exposed for testing.
func (store dbStore) SaveABCIResponses(height int64, abciResponses tmstate.ABCIResponses) error {
	return store.saveABCIResponses(height, abciResponses)
}

func (store dbStore) saveABCIResponses(height int64, abciResponses tmstate.ABCIResponses) error {
	var dtxs []*abci.ExecTxResult
	// strip nil values,
	if abciResponses.ProcessProposal != nil {
		for _, tx := range abciResponses.ProcessProposal.TxResults {
			if tx != nil {
				dtxs = append(dtxs, tx)
			}
		}
		abciResponses.ProcessProposal.TxResults = dtxs
	}
	bz, err := abciResponses.Marshal()
	if err != nil {
		return err
	}
	return store.db.SetSync(abciResponsesKey(height), bz)
}

// SaveValidatorSets is used to save the validator set over multiple heights.
// It is exposed so that a backfill operation during state sync can populate
// the store with the necessary amount of validator sets to verify any evidence
// it may encounter.
func (store dbStore) SaveValidatorSets(lowerHeight, upperHeight int64, vals *types.ValidatorSet) error {
	batch := store.db.NewBatch()
	defer batch.Close()

	// batch together all the validator sets from lowerHeight to upperHeight
	for height := lowerHeight; height <= upperHeight; height++ {
		if err := store.saveValidatorsInfo(height, lowerHeight, vals, batch); err != nil {
			return err
		}
	}

	return batch.WriteSync()
}

// -----------------------------------------------------------------------------

// loadValidators is a helper that loads the validator set from height or last stored height.
// It does NOT set a correct proposer.
func (store dbStore) loadValidators(height int64) (*tmstate.ValidatorsInfo, error) {
	valInfo, err := loadValidatorsInfo(store.db, height)
	if err != nil {
		return nil, ErrNoValSetForHeight{Height: height, Err: fmt.Errorf("failed to load validators info: %w", err)}
	}

	if valInfo.ValidatorSet == nil {
		lastStoredHeight := lastStoredHeightFor(height, valInfo.LastHeightChanged)
		store.logger.Debug("Validator set is nil, loading last stored height", "height", height, "last_height_changed", valInfo.LastHeightChanged, "last_stored_height", lastStoredHeight)
		valInfo, err = loadValidatorsInfo(store.db, lastStoredHeight)
		if err != nil || valInfo.ValidatorSet == nil {
			return nil, ErrNoValSetForHeight{Height: height, Err: fmt.Errorf("couldn't find validators at height %d (height %d was originally requested): %w",
				lastStoredHeight,
				height,
				err,
			)}
		}
	}

	return valInfo, nil
}

// LoadValidators loads the ValidatorSet for a given height and round 0.
//
// Returns ErrNoValSetForHeight if the validator set can't be found for this height.
func (store dbStore) LoadValidators(height int64, bs selectproposer.BlockStore) (*types.ValidatorSet, error) {
	store.logger.Debug("Loading validators", "height", height)

	valInfo, err := store.loadValidators(height)
	if err != nil {
		return nil, ErrNoValSetForHeight{Height: height, Err: err}
	}

	valSet, err := types.ValidatorSetFromProto(valInfo.ValidatorSet)
	if err != nil {
		return nil, err
	}

	// FIND PROPOSER

	// As per definition, proposer at height 1 is the first validator in the validator set.
	if height == 1 {
		proposer := valSet.GetByIndex(0)
		if err := valSet.SetProposer(proposer.ProTxHash); err != nil {
			return nil, fmt.Errorf("could not set first proposer: %w", err)
		}
		return valSet, nil
	}

	// load consensus params to determine algorithm to use for proposer selection
	cp, err := store.LoadConsensusParams(height)
	if err != nil {
		store.logger.Warn("failed to load consensus params, falling back to defaults", "height", height, "err", err)
		cp = *types.DefaultConsensusParams()
	}

	// if we have that block in block store, we just rolllback to round 0
	if meta := bs.LoadBlockMeta(height); meta != nil {
		proposer := meta.Header.ProposerProTxHash
		if err := valSet.SetProposer(proposer); err != nil {
			return nil, fmt.Errorf("could not set proposer: %w", err)
		}

		strategy, err := selectproposer.NewProposerSelector(cp, valSet, meta.Header.Height, meta.Round, bs, store.logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create validator scoring strategy: %w", err)
		}
		if err := strategy.UpdateHeightRound(meta.Header.Height, 0); err != nil {
			return nil, fmt.Errorf("failed to update validator scores at height %d, round 0: %w", meta.Header.Height, err)
		}
		return strategy.ValidatorSet(), nil
	}

	// If we don't have that height in the block store, we just take proposer from previous block and advance it.
	// We don't use current height block because we want to return proposer at round 0.
	prevMeta := bs.LoadBlockMeta(height - 1)
	if prevMeta == nil {
		return nil, fmt.Errorf("could not find block meta for height %d", height-1)
	}
	// Configure proposer strategy; first check if we have a quorum change
	if !prevMeta.Header.ValidatorsHash.Equal(prevMeta.Header.NextValidatorsHash) {
		// rotation happened - we select 1st validator as proposer
		valSetHash := valSet.Hash()
		if !prevMeta.Header.NextValidatorsHash.Equal(valSetHash) {
			return nil, ErrNoValSetForHeight{
				Height: height,
				Err: fmt.Errorf("next validators hash mismatch at height %d, expected %X, got %X", height,
					prevMeta.Header.NextValidatorsHash, valSetHash),
			}
		}

		if err = valSet.SetProposer(valSet.GetByIndex(0).ProTxHash); err != nil {
			return nil, ErrNoValSetForHeight{height, fmt.Errorf("could not set proposer: %w", err)}
		}

		return valSet, nil
	} else {

		// set proposer from previous block
		if err := valSet.SetProposer(prevMeta.Header.ProposerProTxHash); err != nil {
			return nil, fmt.Errorf("could not set proposer: %w", err)
		}
		strategy, err := selectproposer.NewProposerSelector(cp, valSet, prevMeta.Header.Height, prevMeta.Round, bs, store.logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create validator scoring strategy: %w", err)
		}

		// now, advance to (height,0)
		if err := strategy.UpdateHeightRound(height, 0); err != nil {
			return nil, fmt.Errorf("failed to update validator scores at height %d, round 0: %w", height, err)
		}

		return strategy.ValidatorSet(), nil
	}

}

func lastStoredHeightFor(height, lastHeightChanged int64) int64 {
	checkpointHeight := height - height%valSetCheckpointInterval
	return tmmath.MaxInt64(checkpointHeight, lastHeightChanged)
}

// CONTRACT: Returned ValidatorsInfo can be mutated.
func loadValidatorsInfo(db dbm.DB, height int64) (*tmstate.ValidatorsInfo, error) {
	buf, err := db.Get(validatorsKey(height))
	if err != nil {
		return nil, err
	}

	if len(buf) == 0 {
		return nil, errors.New("value retrieved from db is empty")
	}

	v := new(tmstate.ValidatorsInfo)
	err = v.Unmarshal(buf)
	if err != nil {
		// DATA HAS BEEN CORRUPTED OR THE SPEC HAS CHANGED
		panic(fmt.Sprintf("data has been corrupted or its spec has changed: %+v", err))
	}
	// TODO: ensure that buf is completely read.

	return v, nil
}

// saveValidatorsInfo persists the validator set.
//
// `height` is the effective height for which the validator is responsible for
// signing. It should be called from s.Save(), right before the state itself is
// persisted.
func (store dbStore) saveValidatorsInfo(
	height, lastHeightChanged int64,
	valSet *types.ValidatorSet,
	batch dbm.Batch,
) error {
	if lastHeightChanged > height {
		return errors.New("lastHeightChanged cannot be greater than ValidatorsInfo height")
	}
	valInfo := &tmstate.ValidatorsInfo{
		LastHeightChanged: lastHeightChanged,
	}
	// Only persist validator set if it was updated or checkpoint height (see
	// valSetCheckpointInterval) is reached.
	if height == lastHeightChanged || height%valSetCheckpointInterval == 0 {
		pv, err := valSet.ToProto()
		if err != nil {
			return err
		}
		valInfo.ValidatorSet = pv
	}

	bz, err := valInfo.Marshal()
	if err != nil {
		return err
	}

	store.logger.Debug("saving validator set", "height", height, "last_height_changed", lastHeightChanged, "validators", valSet)
	return batch.Set(validatorsKey(height), bz)
}

//-----------------------------------------------------------------------------

// ConsensusParamsInfo represents the latest consensus params, or the last height it changed

// Allocate empty Consensus params at compile time to avoid multiple allocations during runtime
var (
	empty   = types.ConsensusParams{}
	emptypb = tmproto.ConsensusParams{}
)

// LoadConsensusParams loads the ConsensusParams for a given height.
func (store dbStore) LoadConsensusParams(height int64) (types.ConsensusParams, error) {
	paramsInfo, err := store.loadConsensusParamsInfo(height)
	if err != nil {
		return empty, fmt.Errorf("could not find consensus params for height #%d: %w", height, err)
	}

	if paramsInfo.ConsensusParams.Equal(&emptypb) {
		paramsInfo2, err := store.loadConsensusParamsInfo(paramsInfo.LastHeightChanged)
		if err != nil || paramsInfo2.ConsensusParams.Equal(&emptypb) {
			return empty, fmt.Errorf(
				"couldn't find consensus params at height %d (height %d was originally requested): %w",
				paramsInfo.LastHeightChanged,
				height,
				err,
			)
		}

		paramsInfo = paramsInfo2
	}

	return types.ConsensusParamsFromProto(paramsInfo.ConsensusParams), nil
}

func (store dbStore) loadConsensusParamsInfo(height int64) (*tmstate.ConsensusParamsInfo, error) {
	buf, err := store.db.Get(consensusParamsKey(height))
	if err != nil {
		return nil, err
	}
	if len(buf) == 0 {
		return nil, errors.New("value retrieved from db is empty")
	}

	paramsInfo := new(tmstate.ConsensusParamsInfo)
	if err = paramsInfo.Unmarshal(buf); err != nil {
		// DATA HAS BEEN CORRUPTED OR THE SPEC HAS CHANGED
		panic(fmt.Sprintf(`data has been corrupted or its spec has changed: %+v`, err))
	}
	// TODO: ensure that buf is completely read.

	return paramsInfo, nil
}

// saveConsensusParamsInfo persists the consensus params for the next block to disk.
// It should be called from s.Save(), right before the state itself is persisted.
// If the consensus params did not change after processing the latest block,
// only the last height for which they changed is persisted.
func (store dbStore) saveConsensusParamsInfo(
	height, changeHeight int64,
	params types.ConsensusParams,
	batch dbm.Batch,
) error {
	paramsInfo := &tmstate.ConsensusParamsInfo{
		LastHeightChanged: changeHeight,
	}

	if changeHeight == height {
		paramsInfo.ConsensusParams = params.ToProto()
	}
	bz, err := paramsInfo.Marshal()
	if err != nil {
		return err
	}

	return batch.Set(consensusParamsKey(height), bz)
}

func (store dbStore) Close() error {
	return store.db.Close()
}
