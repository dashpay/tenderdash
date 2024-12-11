package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	db "github.com/cometbft/cometbft-db"
	sync "github.com/sasha-s/go-deadlock"

	"github.com/dashpay/tenderdash/abci/example/code"
	"github.com/dashpay/tenderdash/abci/example/kvstore"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/libs/log"
	types1 "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
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
		kvstore.WithAppVersion(0),
	}, opts...)
	app := Application{
		logger: logger.With("module", "kvstore"),
		cfg:    &cfg,
	}
	app.Application, err = kvstore.NewPersistentApp(cfg, opts...)
	if err != nil {
		return nil, err
	}

	for h, ver := range cfg.ConsensusVersionUpdates {
		height, err := strconv.Atoi(h)
		if err != nil {
			return nil, fmt.Errorf("consensus_version_updates: failed to parse height %s: %w", h, err)
		}
		params := types1.ConsensusParams{
			Version: &types1.VersionParams{
				ConsensusVersion: types1.VersionParams_ConsensusVersion(ver),
				AppVersion:       kvstore.ProtocolVersion,
			},
		}
		app.AddConsensusParamsUpdate(params, int64(height))
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
	if lastHeight != 0 && req.Height != lastHeight+1 {
		app.logger.Error(
			"got unexpected height in ExtendVote request",
			"expectedHeight", lastHeight+1,
			"requestHeight", req.Height,
		)
		return &abci.ResponseExtendVote{}, nil
	}
	ext := make([]byte, crypto.DefaultHashSize)
	copy(ext, big.NewInt(lastHeight+1).Bytes())

	app.logger.Info("generated vote extension",
		"ext", fmt.Sprintf("%x", ext),
		"state.Height", lastHeight+1,
	)
	return &abci.ResponseExtendVote{
		VoteExtensions: []*abci.ExtendVoteExtension{
			{
				Type:      types1.VoteExtensionType_THRESHOLD_RECOVER_RAW,
				Extension: ext,
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
	app.mu.Lock()
	defer app.mu.Unlock()

	// We allow vote extensions to be optional
	if len(req.VoteExtensions) == 0 {
		return &abci.ResponseVerifyVoteExtension{
			Status: abci.ResponseVerifyVoteExtension_ACCEPT,
		}, nil
	}
	lastHeight := app.LastCommittedState.GetHeight()
	if lastHeight != 0 && req.Height != lastHeight+1 {
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
		time.Sleep(time.Duration(app.cfg.VoteExtensionDelayMS) * time.Millisecond) //#nosec G115
	}

	app.logger.Info("verified vote extension value", "req", req, "nums", nums)
	return &abci.ResponseVerifyVoteExtension{
		Status: abci.ResponseVerifyVoteExtension_ACCEPT,
	}, nil
}

func (app *Application) FinalizeBlock(ctx context.Context, req *abci.RequestFinalizeBlock) (*abci.ResponseFinalizeBlock, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	for i, ext := range req.Commit.ThresholdVoteExtensions {
		if len(ext.Signature) == 0 {
			return &abci.ResponseFinalizeBlock{}, fmt.Errorf("vote extension signature is empty: %+v", ext)
		}

		app.logger.Debug("vote extension received in FinalizeBlock", "extension", ext, "i", i)
	}

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
	app.SetLastCommittedState(app.PreviousCommittedState)
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
	txRecords := kvstore.TxRecords{
		Size:  0,
		Limit: req.MaxTxBytes,
		Txs:   make([]*abci.TxRecord, 0, len(req.Txs)+1),
	}
	txs := req.Txs
	extCount := len(req.LocalLastCommit.ThresholdVoteExtensions)

	extTxPrefix := VoteExtensionKey + "="
	extTx := []byte(fmt.Sprintf("%s%d", extTxPrefix, extCount))

	// Our generated transaction takes precedence over any supplied
	// transaction that attempts to modify the "extensionSum" value.
	for _, tx := range txs {
		// we only modify transactions if there is at least 1 extension, eg. extCount > 0
		if extCount > 0 && strings.HasPrefix(string(tx), extTxPrefix) {
			if _, err := txRecords.Add(&abci.TxRecord{
				Action: abci.TxRecord_REMOVED,
				Tx:     tx,
			}); err != nil {
				return nil, err
			}
		} else {
			if _, err := txRecords.Add(&abci.TxRecord{
				Action: abci.TxRecord_UNMODIFIED,
				Tx:     tx,
			}); err != nil {
				return nil, err
			}
		}
	}
	// we only modify transactions if there is at least 1 extension, eg. extCount > 0
	if extCount > 0 {
		tx := abci.TxRecord{
			Action: abci.TxRecord_ADDED,
			Tx:     extTx,
		}
		if _, err := txRecords.Add(&tx); err != nil {
			return nil, err
		}
	}

	return txRecords.Txs, nil
}

func verifyTx(tx types.Tx, _ abci.CheckTxType) (abci.ResponseCheckTx, error) {

	split := bytes.SplitN(tx, []byte{'='}, 2)
	if len(split) != 2 {
		return abci.ResponseCheckTx{Code: code.CodeTypeOK, GasWanted: 1}, nil
	}

	k, v := split[0], split[1]

	if string(k) == VoteExtensionKey {
		_, err := strconv.Atoi(string(v))
		if err != nil {
			return abci.ResponseCheckTx{Code: code.CodeTypeUnknownError},
				fmt.Errorf("malformed vote extension transaction %X=%X: %w", k, v, err)
		}
	}
	// For TestApp_TxTooBig we need to preserve order of transactions
	var priority int64
	// in this case, k is defined as fmt.Sprintf("testapp-big-tx-%v-%08x-%d=", node.Name, session, i)
	// but in general, we take last digit as inverse priority
	split = bytes.Split(k, []byte{'-'})
	if n, err := strconv.ParseInt(string(split[len(split)-1]), 10, 64); err == nil {
		priority = 1000000000 - n
	}

	return abci.ResponseCheckTx{Code: code.CodeTypeOK, GasWanted: 1, Priority: priority}, nil
}
