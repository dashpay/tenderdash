package types

import (
	"context"

	"github.com/dashpay/tenderdash/crypto"
)

// StateSyncer is an interface that handles the state sync protocol
type StateSyncer interface {
	// ListSnapshots returns list available snapshots
	ListSnapshots(context.Context, *RequestListSnapshots) (*ResponseListSnapshots, error)
	// OfferSnapshot accepts or rejects an offered snapshot to the state synchronization
	OfferSnapshot(context.Context, *RequestOfferSnapshot) (*ResponseOfferSnapshot, error)
	// LoadSnapshotChunk loads a chunk of snapshot
	LoadSnapshotChunk(context.Context, *RequestLoadSnapshotChunk) (*ResponseLoadSnapshotChunk, error)
	// ApplySnapshotChunk applies a chunk of snapshot
	ApplySnapshotChunk(context.Context, *RequestApplySnapshotChunk) (*ResponseApplySnapshotChunk, error)
	// FinalizeSnapshot sends light block to ABCI app after successful state sync
	FinalizeSnapshot(context.Context, *RequestFinalizeSnapshot) (*ResponseFinalizeSnapshot, error)
}

// Application is an interface that enables any finite, deterministic state machine
// to be driven by a blockchain-based replication engine via the ABCI.
//
//go:generate ../../scripts/mockery_generate.sh Application
type Application interface {
	StateSyncer

	// Info/Query Connection
	Info(context.Context, *RequestInfo) (*ResponseInfo, error)    // Return application info
	Query(context.Context, *RequestQuery) (*ResponseQuery, error) // Query for state

	// Mempool Connection
	CheckTx(context.Context, *RequestCheckTx) (*ResponseCheckTx, error) // Validate a tx for the mempool

	// Consensus Connection
	InitChain(context.Context, *RequestInitChain) (*ResponseInitChain, error) // Initialize blockchain w validators/other info from TendermintCore
	PrepareProposal(context.Context, *RequestPrepareProposal) (*ResponsePrepareProposal, error)
	ProcessProposal(context.Context, *RequestProcessProposal) (*ResponseProcessProposal, error)
	// Create application specific vote extension
	ExtendVote(context.Context, *RequestExtendVote) (*ResponseExtendVote, error)
	// Verify application's vote extension data
	VerifyVoteExtension(context.Context, *RequestVerifyVoteExtension) (*ResponseVerifyVoteExtension, error)
	// Deliver the decided block with its txs to the Application
	FinalizeBlock(context.Context, *RequestFinalizeBlock) (*ResponseFinalizeBlock, error)
}

//-------------------------------------------------------
// BaseApplication is a base form of Application

var _ Application = (*BaseApplication)(nil)

type BaseApplication struct{}

func NewBaseApplication() *BaseApplication {
	return &BaseApplication{}
}

func (BaseApplication) Info(_ context.Context, _req *RequestInfo) (*ResponseInfo, error) {
	return &ResponseInfo{}, nil
}

func (BaseApplication) CheckTx(_ context.Context, _req *RequestCheckTx) (*ResponseCheckTx, error) {
	return &ResponseCheckTx{Code: CodeTypeOK}, nil
}

func (BaseApplication) ExtendVote(_ context.Context, _req *RequestExtendVote) (*ResponseExtendVote, error) {
	return &ResponseExtendVote{}, nil
}

func (BaseApplication) VerifyVoteExtension(_ context.Context, _req *RequestVerifyVoteExtension) (*ResponseVerifyVoteExtension, error) {
	return &ResponseVerifyVoteExtension{
		Status: ResponseVerifyVoteExtension_ACCEPT,
	}, nil
}

func (BaseApplication) Query(_ context.Context, _req *RequestQuery) (*ResponseQuery, error) {
	return &ResponseQuery{Code: CodeTypeOK}, nil
}

func (BaseApplication) InitChain(_ context.Context, _req *RequestInitChain) (*ResponseInitChain, error) {
	return &ResponseInitChain{}, nil
}

func (BaseApplication) ListSnapshots(_ context.Context, _req *RequestListSnapshots) (*ResponseListSnapshots, error) {
	return &ResponseListSnapshots{}, nil
}

func (BaseApplication) OfferSnapshot(_ context.Context, _req *RequestOfferSnapshot) (*ResponseOfferSnapshot, error) {
	return &ResponseOfferSnapshot{}, nil
}

func (BaseApplication) LoadSnapshotChunk(_ context.Context, _ *RequestLoadSnapshotChunk) (*ResponseLoadSnapshotChunk, error) {
	return &ResponseLoadSnapshotChunk{}, nil
}

func (BaseApplication) ApplySnapshotChunk(_ context.Context, _req *RequestApplySnapshotChunk) (*ResponseApplySnapshotChunk, error) {
	return &ResponseApplySnapshotChunk{}, nil
}

// FinalizeSnapshot provides a no-op implementation for the StateSyncer interface
func (BaseApplication) FinalizeSnapshot(_ context.Context, _req *RequestFinalizeSnapshot) (*ResponseFinalizeSnapshot, error) {
	return &ResponseFinalizeSnapshot{}, nil
}

func (BaseApplication) PrepareProposal(_ context.Context, req *RequestPrepareProposal) (*ResponsePrepareProposal, error) {
	trs := make([]*TxRecord, 0, len(req.Txs))
	var totalBytes int64
	for _, tx := range req.Txs {
		totalBytes += int64(len(tx))
		if totalBytes > req.MaxTxBytes {
			break
		}
		trs = append(trs, &TxRecord{
			Action: TxRecord_UNMODIFIED,
			Tx:     tx,
		})
	}
	return &ResponsePrepareProposal{TxRecords: trs,
		AppHash:    make([]byte, crypto.DefaultAppHashSize),
		TxResults:  txResults(req.Txs),
		AppVersion: 1,
	}, nil
}

func txResults(txs [][]byte) []*ExecTxResult {
	results := make([]*ExecTxResult, len(txs))
	for i := range txs {
		results[i] = &ExecTxResult{Code: CodeTypeOK}
	}
	return results
}

func (BaseApplication) ProcessProposal(_ context.Context, req *RequestProcessProposal) (*ResponseProcessProposal, error) {
	return &ResponseProcessProposal{
		AppHash:   make([]byte, crypto.DefaultAppHashSize),
		Status:    ResponseProcessProposal_ACCEPT,
		TxResults: txResults(req.Txs),
	}, nil
}

func (BaseApplication) FinalizeBlock(_ context.Context, _req *RequestFinalizeBlock) (*ResponseFinalizeBlock, error) {

	return &ResponseFinalizeBlock{}, nil
}
