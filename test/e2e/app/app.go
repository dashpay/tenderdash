package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	db "github.com/tendermint/tm-db"

	"github.com/tendermint/tendermint/abci/example/code"
	"github.com/tendermint/tendermint/abci/example/kvstore"
	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/libs/log"
	types1 "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/types"
)

const (
	voteExtensionMaxVal int64  = 128
	VoteExtensionKey    string = "extensionSum"
)

// Application is an ABCI application for use by end-to-end tests. It is a
// simple key/value store for strings, storing data in memory and persisting
// to disk as JSON, taking state sync snapshots if requested.
type Application struct {
	*kvstore.Application
	mu sync.Mutex

	logger log.Logger
	cfg    *kvstore.Config

	PreviousCommittedState kvstore.State
}

// NewApplication creates the application.
func NewApplication(cfg kvstore.Config, opts ...kvstore.OptFunc) (*Application, error) {
	logger, err := log.NewDefaultLogger(log.LogFormatPlain, log.LogLevelDebug)
	if err != nil {
		return nil, err
	}
	opts = append([]kvstore.OptFunc{
		kvstore.WithLogger(logger.With("module", "kvstore")),
		kvstore.WithVerifyTxFunc(verifyTx),
		kvstore.WithPrepareTxsFunc(prepareTxs),
	}, opts...)
	app := Application{
		logger: logger.With("module", "kvstore"),
		cfg:    &cfg,
	}
	app.Application, err = kvstore.NewPersistentApp(cfg, opts...)
	if err != nil {
		return nil, err
	}

	return &app, nil
}

// ExtendVote will produce vote extensions in the form of random numbers to
// demonstrate vote extension nondeterminism.
//
// In the next block, if there are any vote extensions from the previous block,
// a new transaction will be proposed that updates a special value in the
// key/value store ("extensionSum") with the sum of all of the numbers collected
// from the vote extensions.
func (app *Application) ExtendVote(_ context.Context, req *abci.RequestExtendVote) (*abci.ResponseExtendVote, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	// We ignore any requests for vote extensions that don't match our expected
	// next height.
	lastHeight := app.LastCommittedState.GetHeight()
	if req.Height != lastHeight+1 {
		app.logger.Error(
			"got unexpected height in ExtendVote request",
			"expectedHeight", lastHeight+1,
			"requestHeight", req.Height,
		)
		return &abci.ResponseExtendVote{}, nil
	}
	ext := make([]byte, binary.MaxVarintLen64)
	// We don't care that these values are generated by a weak random number
	// generator. It's just for test purposes.
	// nolint:gosec // G404: Use of weak random number generator
	num := rand.Int63n(voteExtensionMaxVal)
	extLen := binary.PutVarint(ext, num)
	app.logger.Info("generated vote extension",
		"num", num,
		"ext", fmt.Sprintf("%x", ext[:extLen]),
		"state.Height", lastHeight,
	)
	return &abci.ResponseExtendVote{
		VoteExtensions: []*abci.ExtendVoteExtension{
			{
				Type:      types1.VoteExtensionType_DEFAULT,
				Extension: ext[:extLen],
			},
			{
				Type:      types1.VoteExtensionType_THRESHOLD_RECOVER,
				Extension: []byte(fmt.Sprintf("threshold-%d", lastHeight+1)),
			},
		},
	}, nil
}

// VerifyVoteExtension simply validates vote extensions from other validators
// without doing anything about them. In this case, it just makes sure that the
// vote extension is a well-formed integer value.
func (app *Application) VerifyVoteExtension(_ context.Context, req *abci.RequestVerifyVoteExtension) (*abci.ResponseVerifyVoteExtension, error) {
	// We allow vote extensions to be optional
	if len(req.VoteExtensions) == 0 {
		return &abci.ResponseVerifyVoteExtension{
			Status: abci.ResponseVerifyVoteExtension_ACCEPT,
		}, nil
	}
	lastHeight := app.LastCommittedState.GetHeight()
	if req.Height != lastHeight+1 {
		app.logger.Error(
			"got unexpected height in VerifyVoteExtension request",
			"expectedHeight", lastHeight+1,
			"requestHeight", req.Height,
		)
		return &abci.ResponseVerifyVoteExtension{
			Status: abci.ResponseVerifyVoteExtension_REJECT,
		}, nil
	}

	nums := make([]int64, 0, len(req.VoteExtensions))
	for _, ext := range req.VoteExtensions {
		num, err := parseVoteExtension(ext.Extension)
		if err != nil {
			app.logger.Error("failed to verify vote extension", "req", req, "err", err)
			return &abci.ResponseVerifyVoteExtension{
				Status: abci.ResponseVerifyVoteExtension_REJECT,
			}, nil
		}
		nums = append(nums, num)
	}

	if app.cfg.VoteExtensionDelayMS != 0 {
		time.Sleep(time.Duration(app.cfg.VoteExtensionDelayMS) * time.Millisecond)
	}

	app.logger.Info("verified vote extension value", "req", req, "nums", nums)
	return &abci.ResponseVerifyVoteExtension{
		Status: abci.ResponseVerifyVoteExtension_ACCEPT,
	}, nil
}

func (app *Application) FinalizeBlock(ctx context.Context, req *abci.RequestFinalizeBlock) (*abci.ResponseFinalizeBlock, error) {
	prevState := kvstore.NewKvState(db.NewMemDB(), 0)
	if err := app.LastCommittedState.Copy(prevState); err != nil {
		return &abci.ResponseFinalizeBlock{}, err
	}
	resp, err := app.Application.FinalizeBlock(ctx, req)
	if err != nil {
		return &abci.ResponseFinalizeBlock{}, err
	}
	app.PreviousCommittedState = prevState
	return resp, nil
}

func (app *Application) Rollback() error {
	app.mu.Lock()
	defer app.mu.Unlock()

	if app.PreviousCommittedState == nil {
		return fmt.Errorf("cannot rollback - no previous state found")
	}
	app.LastCommittedState = app.PreviousCommittedState
	return nil
}

// parseVoteExtension attempts to parse the given extension data into a positive
// integer value.
func parseVoteExtension(ext []byte) (int64, error) {
	num, errVal := binary.Varint(ext)
	if errVal == 0 {
		return 0, errors.New("vote extension is too small to parse")
	}
	if errVal < 0 {
		return 0, errors.New("vote extension value is too large")
	}
	if num >= voteExtensionMaxVal {
		return 0, fmt.Errorf("vote extension value must be smaller than %d (was %d)", voteExtensionMaxVal, num)
	}
	return num, nil
}

func prepareTxs(req abci.RequestPrepareProposal) ([]*abci.TxRecord, error) {
	var (
		totalBytes int64
		txRecords  []*abci.TxRecord
	)

	txs := req.Txs
	extCount := len(req.LocalLastCommit.ThresholdVoteExtensions)

	txRecords = make([]*abci.TxRecord, 0, len(txs)+1)
	extTxPrefix := VoteExtensionKey + "="
	extTx := []byte(fmt.Sprintf("%s%d", extTxPrefix, extCount))

	// Our generated transaction takes precedence over any supplied
	// transaction that attempts to modify the "extensionSum" value.
	for _, tx := range txs {
		// we only modify transactions if there is at least 1 extension, eg. extCount > 0
		if extCount > 0 && strings.HasPrefix(string(tx), extTxPrefix) {
			txRecords = append(txRecords, &abci.TxRecord{
				Action: abci.TxRecord_REMOVED,
				Tx:     tx,
			})
			totalBytes -= int64(len(tx))
		} else {
			txRecords = append(txRecords, &abci.TxRecord{
				Action: abci.TxRecord_UNMODIFIED,
				Tx:     tx,
			})
			totalBytes += int64(len(tx))
		}
	}
	// we only modify transactions if there is at least 1 extension, eg. extCount > 0
	if extCount > 0 {
		if totalBytes+int64(len(extTx)) < req.MaxTxBytes {
			txRecords = append(txRecords, &abci.TxRecord{
				Action: abci.TxRecord_ADDED,
				Tx:     extTx,
			})
		}
	}

	return txRecords, nil
}

func verifyTx(tx types.Tx, _ abci.CheckTxType) (abci.ResponseCheckTx, error) {

	split := bytes.SplitN(tx, []byte{'='}, 2)
	k, v := split[0], split[1]

	if string(k) == VoteExtensionKey {
		_, err := strconv.Atoi(string(v))
		if err != nil {
			return abci.ResponseCheckTx{Code: code.CodeTypeUnknownError},
				fmt.Errorf("malformed vote extension transaction %X=%X: %w", k, v, err)
		}
	}
	return abci.ResponseCheckTx{Code: code.CodeTypeOK, GasWanted: 1}, nil
}
