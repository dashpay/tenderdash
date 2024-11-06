package kvstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"

	dbm "github.com/cometbft/cometbft-db"

	"github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	types1 "github.com/dashpay/tenderdash/types"
)

// State represents kvstore app state at some height.
// State can be committed or uncommitted.
// Caller of State methods should do proper concurrency locking (eg. mutexes)
type State interface {
	dbm.DB

	// Save writes full content of this state to some output
	Save(output io.Writer) error
	// Load loads state from some input
	Load(from io.Reader) error
	// Copy copies the state to dst
	Copy(dst State) error
	// Close closes the state and frees up resources
	Close() error

	// GetHeight returns height of the state
	GetHeight() int64
	// IncrementHeight increments height by 1, but not lower than initial height.
	IncrementHeight()

	// SetRound sets consensus round for the state
	SetRound(int32)
	// GetRound returns consensus round defined for the state
	GetRound() int32

	// GetAppHash returns app hash for the state. You need to call UpdateAppHash() beforehand.
	GetAppHash() tmbytes.HexBytes

	// UpdateAppHash regenerates apphash for the state. It accepts transactions and tx results from current round.
	// It is deterministic for a given state, txs and txResults.
	UpdateAppHash(lastCommittedState State, txs types1.Txs, txResults []*types.ExecTxResult) error
}

type kvState struct {
	dbm.DB `json:"-"`
	// Height of the state. Special value of 0 means zero state.
	Height        int64            `json:"height"`
	InitialHeight int64            `json:"initial_height,omitempty"`
	Round         int32            `json:"round"`
	AppHash       tmbytes.HexBytes `json:"app_hash"`
}

// NewKvState creates new, empty, uninitialized kvstore State.
// Use Copy() to populate.
func NewKvState(db dbm.DB, initialHeight int64) State {
	return &kvState{
		DB:            db,
		AppHash:       make([]byte, crypto.DefaultAppHashSize),
		Height:        0,
		InitialHeight: initialHeight,
	}
}

// Copy copies the state. It ensures copy is a valid, initialized state.
// Caller should close the state once it's not needed anymore
// newDBfunc can be provided to define DB that will be used for this copy.
func (state kvState) Copy(destination State) error {
	dst, ok := destination.(*kvState)
	if !ok {
		return fmt.Errorf("invalid destination, expected: *kvState, got: %T", destination)
	}

	dst.Height = state.Height
	dst.InitialHeight = state.InitialHeight
	dst.AppHash = state.AppHash.Copy()
	// apphash is required, and should never be nil,zero-length
	if len(dst.AppHash) == 0 {
		dst.AppHash = make(tmbytes.HexBytes, crypto.DefaultAppHashSize)
	}
	if err := copyDB(state.DB, dst.DB); err != nil {
		return fmt.Errorf("copy state db: %w", err)
	}
	return nil
}

func resetDB(dst dbm.DB, batch dbm.Batch) error {
	// cleanup dest DB first
	dstIter, err := dst.Iterator(nil, nil)
	if err != nil {
		return fmt.Errorf("cannot create dest db iterator: %w", err)
	}
	defer dstIter.Close()

	// Delete content of dst, to be sure that it will not contain any unexpected data.
	keys := make([][]byte, 0)
	for dstIter.Valid() {
		keys = append(keys, dstIter.Key())
		dstIter.Next()
	}
	for _, key := range keys {
		_ = batch.Delete(key) // ignore errors
	}

	return nil
}
func copyDB(src dbm.DB, dst dbm.DB) error {
	dstBatch := dst.NewBatch()
	defer dstBatch.Close()

	if err := resetDB(dst, dstBatch); err != nil {
		return err
	}

	// write source to dest
	if src != nil {
		srcIter, err := src.Iterator(nil, nil)
		if err != nil {
			return fmt.Errorf("cannot copy current DB: %w", err)
		}
		defer srcIter.Close()

		for srcIter.Valid() {
			if err = dstBatch.Set(srcIter.Key(), srcIter.Value()); err != nil {
				return err
			}
			srcIter.Next()
		}

		if err = dstBatch.Write(); err != nil {
			return fmt.Errorf("cannot close dest batch: %w", err)
		}
	}

	return nil
}

func (state kvState) GetHeight() int64 {
	return state.Height
}

// GetRound implements State
func (state kvState) GetRound() int32 {
	return state.Round
}

// GetRound implements State
func (state *kvState) SetRound(round int32) {
	state.Round = round
}

// IncrementHeight increments the height of the state.
// If state height is 0 (initial state), it sets the height
// to state.InitialHeight.
func (state *kvState) IncrementHeight() {
	if state.Height == 0 {
		state.Height = state.InitialHeight
	} else {
		state.Height = state.GetHeight() + 1
	}
}

func (state kvState) GetAppHash() tmbytes.HexBytes {
	return state.AppHash.Copy()
}

func (state *kvState) UpdateAppHash(lastCommittedState State, _txs types1.Txs, txResults []*types.ExecTxResult) error {
	// UpdateAppHash updates app hash for the current app state.
	txResultsHash, err := types.TxResultsHash(txResults)
	if err != nil {
		return err
	}
	state.AppHash = crypto.Checksum(append(lastCommittedState.GetAppHash(), txResultsHash...))

	return nil
}

// Load state from the reader.
// It expects json-encoded kvState, followed by all items from the state.
//
// As a special case, io.EOF when reading the header means that the state is empty.
func (state *kvState) Load(from io.Reader) error {
	if state == nil || state.DB == nil {
		return errors.New("cannot load into nil state")
	}

	// We reuse DB as we can use atomic batches to load items.
	newState := NewKvState(state.DB, state.InitialHeight).(*kvState)

	decoder := json.NewDecoder(from)
	if err := decoder.Decode(&newState); err != nil {
		return fmt.Errorf("error reading state header: %w", err)
	}

	// Load items to state DB
	batch := newState.DB.NewBatch()
	defer batch.Close()

	if err := resetDB(newState.DB, batch); err != nil {
		return err
	}

	item := exportItem{}
	var err error
	for err = decoder.Decode(&item); err == nil; err = decoder.Decode(&item) {
		key, err := url.QueryUnescape(item.Key)
		if err != nil {
			return fmt.Errorf("error restoring state item key %+v: %w", item, err)
		}
		value, err := url.QueryUnescape(item.Value)
		if err != nil {
			return fmt.Errorf("error restoring state item value %+v: %w", item, err)
		}

		if err := batch.Set([]byte(key), []byte(value)); err != nil {
			return fmt.Errorf("error restoring state item %+v: %w", item, err)
		}
	}

	if !errors.Is(err, io.EOF) {
		return err
	}

	// commit changes
	if err := batch.Write(); err != nil {
		return fmt.Errorf("error finalizing restore batch: %w", err)
	}

	// copy loaded values to the state
	state.InitialHeight = newState.InitialHeight
	state.Height = newState.Height
	state.Round = newState.Round
	state.AppHash = newState.AppHash
	// apphash cannot be nil,zero-length
	if len(state.AppHash) == 0 {
		state.AppHash = make(tmbytes.HexBytes, crypto.DefaultAppHashSize)
	}

	return nil
}

// Save saves state to the writer.
// First it puts json-encoded kvState, followed by all items from the state.
func (state kvState) Save(to io.Writer) error {
	encoder := json.NewEncoder(to)
	if err := encoder.Encode(state); err != nil {
		return fmt.Errorf("kvState marshal: %w", err)
	}

	iter, err := state.DB.Iterator(nil, nil)
	if err != nil {
		return fmt.Errorf("error creating state iterator: %w", err)
	}
	defer iter.Close()

	for ; iter.Valid(); iter.Next() {
		key := url.QueryEscape(string(iter.Key()))
		value := url.QueryEscape(string(iter.Value()))
		item := exportItem{Key: key, Value: value}
		if err := encoder.Encode(item); err != nil {
			return fmt.Errorf("error encoding state item %+v: %w", item, err)
		}
	}

	return nil
}

type exportItem struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func (state *kvState) Close() error {
	if state.DB != nil {
		return state.DB.Close()
	}
	return nil
}
