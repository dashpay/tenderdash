package counter

import (
	"encoding/binary"
	"fmt"

	tmtypes "github.com/tendermint/tendermint/types"

	"github.com/tendermint/tendermint/abci/example/code"
	"github.com/tendermint/tendermint/abci/types"
)

type Application struct {
	types.BaseApplication

	hashCount                  int
	txCount                    int
	serial                     bool
	HasCoreChainLocks          bool
	CurrentCoreChainLockHeight uint32
	CoreChainLockStep          int32
}

func NewApplication(serial bool) *Application {
	return &Application{serial: serial, CoreChainLockStep: 1}
}

func (app *Application) Info(req types.RequestInfo) types.ResponseInfo {
	return types.ResponseInfo{Data: fmt.Sprintf("{\"hashes\":%v,\"txs\":%v}", app.hashCount, app.txCount)}
}

func (app *Application) DeliverTx(req types.RequestDeliverTx) types.ResponseDeliverTx {
	if app.serial {
		if len(req.Tx) > 8 {
			return types.ResponseDeliverTx{
				Code: code.CodeTypeEncodingError,
				Log:  fmt.Sprintf("Max tx size is 8 bytes, got %d", len(req.Tx))}
		}
		tx8 := make([]byte, 8)
		copy(tx8[len(tx8)-len(req.Tx):], req.Tx)
		txValue := binary.BigEndian.Uint64(tx8)
		if txValue != uint64(app.txCount) {
			return types.ResponseDeliverTx{
				Code: code.CodeTypeBadNonce,
				Log:  fmt.Sprintf("Invalid nonce. Expected %v, got %v", app.txCount, txValue)}
		}
	}
	app.txCount++
	return types.ResponseDeliverTx{Code: code.CodeTypeOK}
}

func (app *Application) CheckTx(req types.RequestCheckTx) types.ResponseCheckTx {
	if app.serial {
		if len(req.Tx) > 8 {
			return types.ResponseCheckTx{
				Code: code.CodeTypeEncodingError,
				Log:  fmt.Sprintf("Max tx size is 8 bytes, got %d", len(req.Tx))}
		}
		tx8 := make([]byte, 8)
		copy(tx8[len(tx8)-len(req.Tx):], req.Tx)
		txValue := binary.BigEndian.Uint64(tx8)
		if txValue < uint64(app.txCount) {
			return types.ResponseCheckTx{
				Code: code.CodeTypeBadNonce,
				Log:  fmt.Sprintf("Invalid nonce. Expected >= %v, got %v", app.txCount, txValue)}
		}
	}
	return types.ResponseCheckTx{Code: code.CodeTypeOK}
}

func (app *Application) Commit() (resp types.ResponseCommit) {
	app.hashCount++
	if app.txCount == 0 {
		return types.ResponseCommit{}
	}
	hash := make([]byte, 24)
	endHash := make([]byte, 8)
	binary.BigEndian.PutUint64(endHash, uint64(app.txCount))
	hash = append(hash, endHash...)
	return types.ResponseCommit{Data: hash}
}

func (app *Application) Query(reqQuery types.RequestQuery) types.ResponseQuery {
	switch reqQuery.Path {
	case "verify-chainlock":
		return types.ResponseQuery{Code: 0}
	case "hash":
		return types.ResponseQuery{Value: []byte(fmt.Sprintf("%v", app.hashCount))}
	case "tx":
		return types.ResponseQuery{Value: []byte(fmt.Sprintf("%v", app.txCount))}
	default:
		return types.ResponseQuery{Log: fmt.Sprintf("Invalid query path. Expected hash or tx, got %v", reqQuery.Path)}
	}
}

func (app *Application) EndBlock(reqEndBlock types.RequestEndBlock) types.ResponseEndBlock {
	var resp types.ResponseEndBlock
	if app.HasCoreChainLocks {
		app.CurrentCoreChainLockHeight = app.CurrentCoreChainLockHeight + uint32(app.CoreChainLockStep)
		coreChainLock := tmtypes.NewMockChainLock(app.CurrentCoreChainLockHeight)
		resp.NextCoreChainLockUpdate = coreChainLock.ToProto()
	}
	return resp
}
