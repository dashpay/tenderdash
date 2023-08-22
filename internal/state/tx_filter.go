package state

import (
	"time"

	sync "github.com/sasha-s/go-deadlock"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/internal/mempool"
	"github.com/dashpay/tenderdash/types"
)

func cachingStateFetcher(store Store) func() (State, error) {
	const ttl = time.Second

	var (
		last  time.Time
		mutex = &sync.Mutex{}
		cache State
		err   error
	)

	return func() (State, error) {
		mutex.Lock()
		defer mutex.Unlock()

		if time.Since(last) < ttl && cache.ChainID != "" {
			return cache, nil
		}

		cache, err = store.Load()
		if err != nil {
			return State{}, err
		}
		last = time.Now()

		return cache, nil
	}

}

// TxPreCheckFromStore returns a function to filter transactions before processing.
// The function limits the size of a transaction to the block's maximum data size.
func TxPreCheckFromStore(store Store) mempool.PreCheckFunc {
	fetch := cachingStateFetcher(store)

	return func(tx types.Tx) error {
		state, err := fetch()
		if err != nil {
			return err
		}

		return TxPreCheckForState(state)(tx)
	}
}

func TxPreCheckForState(state State) mempool.PreCheckFunc {
	return func(tx types.Tx) error {
		maxDataBytes, err := types.MaxDataBytesNoEvidence(state.ConsensusParams.Block.MaxBytes)
		if err != nil {
			return err
		}
		return mempool.PreCheckMaxBytes(maxDataBytes)(tx)
	}
}

// TxPostCheckFromStore returns a function to filter transactions after processing.
// The function limits the gas wanted by a transaction to the block's maximum total gas.
func TxPostCheckFromStore(store Store) mempool.PostCheckFunc {
	fetch := cachingStateFetcher(store)

	return func(tx types.Tx, resp *abci.ResponseCheckTx) error {
		state, err := fetch()
		if err != nil {
			return err
		}
		return mempool.PostCheckMaxGas(state.ConsensusParams.Block.MaxGas)(tx, resp)
	}
}

func TxPostCheckForState(state State) mempool.PostCheckFunc {
	return func(tx types.Tx, resp *abci.ResponseCheckTx) error {
		return mempool.PostCheckMaxGas(state.ConsensusParams.Block.MaxGas)(tx, resp)
	}
}
