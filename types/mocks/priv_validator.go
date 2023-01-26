// Code generated by mockery. DO NOT EDIT.

package mocks

import (
	btcjson "github.com/dashevo/dashd-go/btcjson"
	bytes "github.com/tendermint/tendermint/libs/bytes"

	context "context"

	crypto "github.com/tendermint/tendermint/crypto"

	log "github.com/tendermint/tendermint/libs/log"

	mock "github.com/stretchr/testify/mock"

	tenderminttypes "github.com/tendermint/tendermint/proto/tendermint/types"

	types "github.com/tendermint/tendermint/types"
)

// PrivValidator is an autogenerated mock type for the PrivValidator type
type PrivValidator struct {
	mock.Mock
}

// ExtractIntoValidator provides a mock function with given fields: ctx, quorumHash
func (_m *PrivValidator) ExtractIntoValidator(ctx context.Context, quorumHash bytes.HexBytes) *types.Validator {
	ret := _m.Called(ctx, quorumHash)

	var r0 *types.Validator
	if rf, ok := ret.Get(0).(func(context.Context, bytes.HexBytes) *types.Validator); ok {
		r0 = rf(ctx, quorumHash)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.Validator)
		}
	}

	return r0
}

// GetFirstQuorumHash provides a mock function with given fields: _a0
func (_m *PrivValidator) GetFirstQuorumHash(_a0 context.Context) (bytes.HexBytes, error) {
	ret := _m.Called(_a0)

	var r0 bytes.HexBytes
	if rf, ok := ret.Get(0).(func(context.Context) bytes.HexBytes); ok {
		r0 = rf(_a0)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(bytes.HexBytes)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context) error); ok {
		r1 = rf(_a0)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetHeight provides a mock function with given fields: ctx, quorumHash
func (_m *PrivValidator) GetHeight(ctx context.Context, quorumHash bytes.HexBytes) (int64, error) {
	ret := _m.Called(ctx, quorumHash)

	var r0 int64
	if rf, ok := ret.Get(0).(func(context.Context, bytes.HexBytes) int64); ok {
		r0 = rf(ctx, quorumHash)
	} else {
		r0 = ret.Get(0).(int64)
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, bytes.HexBytes) error); ok {
		r1 = rf(ctx, quorumHash)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetPrivateKey provides a mock function with given fields: ctx, quorumHash
func (_m *PrivValidator) GetPrivateKey(ctx context.Context, quorumHash bytes.HexBytes) (crypto.PrivKey, error) {
	ret := _m.Called(ctx, quorumHash)

	var r0 crypto.PrivKey
	if rf, ok := ret.Get(0).(func(context.Context, bytes.HexBytes) crypto.PrivKey); ok {
		r0 = rf(ctx, quorumHash)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(crypto.PrivKey)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, bytes.HexBytes) error); ok {
		r1 = rf(ctx, quorumHash)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetProTxHash provides a mock function with given fields: _a0
func (_m *PrivValidator) GetProTxHash(_a0 context.Context) (bytes.HexBytes, error) {
	ret := _m.Called(_a0)

	var r0 bytes.HexBytes
	if rf, ok := ret.Get(0).(func(context.Context) bytes.HexBytes); ok {
		r0 = rf(_a0)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(bytes.HexBytes)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context) error); ok {
		r1 = rf(_a0)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetPubKey provides a mock function with given fields: ctx, quorumHash
func (_m *PrivValidator) GetPubKey(ctx context.Context, quorumHash bytes.HexBytes) (crypto.PubKey, error) {
	ret := _m.Called(ctx, quorumHash)

	var r0 crypto.PubKey
	if rf, ok := ret.Get(0).(func(context.Context, bytes.HexBytes) crypto.PubKey); ok {
		r0 = rf(ctx, quorumHash)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(crypto.PubKey)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, bytes.HexBytes) error); ok {
		r1 = rf(ctx, quorumHash)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetThresholdPublicKey provides a mock function with given fields: ctx, quorumHash
func (_m *PrivValidator) GetThresholdPublicKey(ctx context.Context, quorumHash bytes.HexBytes) (crypto.PubKey, error) {
	ret := _m.Called(ctx, quorumHash)

	var r0 crypto.PubKey
	if rf, ok := ret.Get(0).(func(context.Context, bytes.HexBytes) crypto.PubKey); ok {
		r0 = rf(ctx, quorumHash)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(crypto.PubKey)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, bytes.HexBytes) error); ok {
		r1 = rf(ctx, quorumHash)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// SignProposal provides a mock function with given fields: ctx, chainID, quorumType, quorumHash, proposal
func (_m *PrivValidator) SignProposal(ctx context.Context, chainID string, quorumType btcjson.LLMQType, quorumHash bytes.HexBytes, proposal *tenderminttypes.Proposal) (bytes.HexBytes, error) {
	ret := _m.Called(ctx, chainID, quorumType, quorumHash, proposal)

	var r0 bytes.HexBytes
	if rf, ok := ret.Get(0).(func(context.Context, string, btcjson.LLMQType, bytes.HexBytes, *tenderminttypes.Proposal) bytes.HexBytes); ok {
		r0 = rf(ctx, chainID, quorumType, quorumHash, proposal)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(bytes.HexBytes)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, string, btcjson.LLMQType, bytes.HexBytes, *tenderminttypes.Proposal) error); ok {
		r1 = rf(ctx, chainID, quorumType, quorumHash, proposal)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// SignVote provides a mock function with given fields: ctx, chainID, quorumType, quorumHash, vote, logger
func (_m *PrivValidator) SignVote(ctx context.Context, chainID string, quorumType btcjson.LLMQType, quorumHash bytes.HexBytes, vote *tenderminttypes.Vote, logger log.Logger) error {
	ret := _m.Called(ctx, chainID, quorumType, quorumHash, vote, logger)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, string, btcjson.LLMQType, bytes.HexBytes, *tenderminttypes.Vote, log.Logger) error); ok {
		r0 = rf(ctx, chainID, quorumType, quorumHash, vote, logger)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// UpdatePrivateKey provides a mock function with given fields: ctx, privateKey, quorumHash, thresholdPublicKey, height
func (_m *PrivValidator) UpdatePrivateKey(ctx context.Context, privateKey crypto.PrivKey, quorumHash bytes.HexBytes, thresholdPublicKey crypto.PubKey, height int64) {
	_m.Called(ctx, privateKey, quorumHash, thresholdPublicKey, height)
}

type mockConstructorTestingTNewPrivValidator interface {
	mock.TestingT
	Cleanup(func())
}

// NewPrivValidator creates a new instance of PrivValidator. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
func NewPrivValidator(t mockConstructorTestingTNewPrivValidator) *PrivValidator {
	mock := &PrivValidator{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
