// Code generated by mockery. DO NOT EDIT.

package mocks

import (
	btcjson "github.com/dashpay/dashd-go/btcjson"
	bytes "github.com/dashpay/tenderdash/libs/bytes"

	context "context"

	crypto "github.com/dashpay/tenderdash/crypto"

	log "github.com/dashpay/tenderdash/libs/log"

	mock "github.com/stretchr/testify/mock"

	tenderminttypes "github.com/dashpay/tenderdash/proto/tendermint/types"

	types "github.com/dashpay/tenderdash/types"
)

// PrivValidator is an autogenerated mock type for the PrivValidator type
type PrivValidator struct {
	mock.Mock
}

type PrivValidator_Expecter struct {
	mock *mock.Mock
}

func (_m *PrivValidator) EXPECT() *PrivValidator_Expecter {
	return &PrivValidator_Expecter{mock: &_m.Mock}
}

// ExtractIntoValidator provides a mock function with given fields: ctx, quorumHash
func (_m *PrivValidator) ExtractIntoValidator(ctx context.Context, quorumHash crypto.QuorumHash) *types.Validator {
	ret := _m.Called(ctx, quorumHash)

	if len(ret) == 0 {
		panic("no return value specified for ExtractIntoValidator")
	}

	var r0 *types.Validator
	if rf, ok := ret.Get(0).(func(context.Context, crypto.QuorumHash) *types.Validator); ok {
		r0 = rf(ctx, quorumHash)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.Validator)
		}
	}

	return r0
}

// PrivValidator_ExtractIntoValidator_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'ExtractIntoValidator'
type PrivValidator_ExtractIntoValidator_Call struct {
	*mock.Call
}

// ExtractIntoValidator is a helper method to define mock.On call
//   - ctx context.Context
//   - quorumHash crypto.QuorumHash
func (_e *PrivValidator_Expecter) ExtractIntoValidator(ctx interface{}, quorumHash interface{}) *PrivValidator_ExtractIntoValidator_Call {
	return &PrivValidator_ExtractIntoValidator_Call{Call: _e.mock.On("ExtractIntoValidator", ctx, quorumHash)}
}

func (_c *PrivValidator_ExtractIntoValidator_Call) Run(run func(ctx context.Context, quorumHash crypto.QuorumHash)) *PrivValidator_ExtractIntoValidator_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(crypto.QuorumHash))
	})
	return _c
}

func (_c *PrivValidator_ExtractIntoValidator_Call) Return(_a0 *types.Validator) *PrivValidator_ExtractIntoValidator_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *PrivValidator_ExtractIntoValidator_Call) RunAndReturn(run func(context.Context, crypto.QuorumHash) *types.Validator) *PrivValidator_ExtractIntoValidator_Call {
	_c.Call.Return(run)
	return _c
}

// GetFirstQuorumHash provides a mock function with given fields: _a0
func (_m *PrivValidator) GetFirstQuorumHash(_a0 context.Context) (crypto.QuorumHash, error) {
	ret := _m.Called(_a0)

	if len(ret) == 0 {
		panic("no return value specified for GetFirstQuorumHash")
	}

	var r0 crypto.QuorumHash
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context) (crypto.QuorumHash, error)); ok {
		return rf(_a0)
	}
	if rf, ok := ret.Get(0).(func(context.Context) crypto.QuorumHash); ok {
		r0 = rf(_a0)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(crypto.QuorumHash)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context) error); ok {
		r1 = rf(_a0)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// PrivValidator_GetFirstQuorumHash_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetFirstQuorumHash'
type PrivValidator_GetFirstQuorumHash_Call struct {
	*mock.Call
}

// GetFirstQuorumHash is a helper method to define mock.On call
//   - _a0 context.Context
func (_e *PrivValidator_Expecter) GetFirstQuorumHash(_a0 interface{}) *PrivValidator_GetFirstQuorumHash_Call {
	return &PrivValidator_GetFirstQuorumHash_Call{Call: _e.mock.On("GetFirstQuorumHash", _a0)}
}

func (_c *PrivValidator_GetFirstQuorumHash_Call) Run(run func(_a0 context.Context)) *PrivValidator_GetFirstQuorumHash_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context))
	})
	return _c
}

func (_c *PrivValidator_GetFirstQuorumHash_Call) Return(_a0 crypto.QuorumHash, _a1 error) *PrivValidator_GetFirstQuorumHash_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *PrivValidator_GetFirstQuorumHash_Call) RunAndReturn(run func(context.Context) (crypto.QuorumHash, error)) *PrivValidator_GetFirstQuorumHash_Call {
	_c.Call.Return(run)
	return _c
}

// GetHeight provides a mock function with given fields: ctx, quorumHash
func (_m *PrivValidator) GetHeight(ctx context.Context, quorumHash crypto.QuorumHash) (int64, error) {
	ret := _m.Called(ctx, quorumHash)

	if len(ret) == 0 {
		panic("no return value specified for GetHeight")
	}

	var r0 int64
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, crypto.QuorumHash) (int64, error)); ok {
		return rf(ctx, quorumHash)
	}
	if rf, ok := ret.Get(0).(func(context.Context, crypto.QuorumHash) int64); ok {
		r0 = rf(ctx, quorumHash)
	} else {
		r0 = ret.Get(0).(int64)
	}

	if rf, ok := ret.Get(1).(func(context.Context, crypto.QuorumHash) error); ok {
		r1 = rf(ctx, quorumHash)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// PrivValidator_GetHeight_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetHeight'
type PrivValidator_GetHeight_Call struct {
	*mock.Call
}

// GetHeight is a helper method to define mock.On call
//   - ctx context.Context
//   - quorumHash crypto.QuorumHash
func (_e *PrivValidator_Expecter) GetHeight(ctx interface{}, quorumHash interface{}) *PrivValidator_GetHeight_Call {
	return &PrivValidator_GetHeight_Call{Call: _e.mock.On("GetHeight", ctx, quorumHash)}
}

func (_c *PrivValidator_GetHeight_Call) Run(run func(ctx context.Context, quorumHash crypto.QuorumHash)) *PrivValidator_GetHeight_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(crypto.QuorumHash))
	})
	return _c
}

func (_c *PrivValidator_GetHeight_Call) Return(_a0 int64, _a1 error) *PrivValidator_GetHeight_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *PrivValidator_GetHeight_Call) RunAndReturn(run func(context.Context, crypto.QuorumHash) (int64, error)) *PrivValidator_GetHeight_Call {
	_c.Call.Return(run)
	return _c
}

// GetPrivateKey provides a mock function with given fields: ctx, quorumHash
func (_m *PrivValidator) GetPrivateKey(ctx context.Context, quorumHash crypto.QuorumHash) (crypto.PrivKey, error) {
	ret := _m.Called(ctx, quorumHash)

	if len(ret) == 0 {
		panic("no return value specified for GetPrivateKey")
	}

	var r0 crypto.PrivKey
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, crypto.QuorumHash) (crypto.PrivKey, error)); ok {
		return rf(ctx, quorumHash)
	}
	if rf, ok := ret.Get(0).(func(context.Context, crypto.QuorumHash) crypto.PrivKey); ok {
		r0 = rf(ctx, quorumHash)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(crypto.PrivKey)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, crypto.QuorumHash) error); ok {
		r1 = rf(ctx, quorumHash)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// PrivValidator_GetPrivateKey_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetPrivateKey'
type PrivValidator_GetPrivateKey_Call struct {
	*mock.Call
}

// GetPrivateKey is a helper method to define mock.On call
//   - ctx context.Context
//   - quorumHash crypto.QuorumHash
func (_e *PrivValidator_Expecter) GetPrivateKey(ctx interface{}, quorumHash interface{}) *PrivValidator_GetPrivateKey_Call {
	return &PrivValidator_GetPrivateKey_Call{Call: _e.mock.On("GetPrivateKey", ctx, quorumHash)}
}

func (_c *PrivValidator_GetPrivateKey_Call) Run(run func(ctx context.Context, quorumHash crypto.QuorumHash)) *PrivValidator_GetPrivateKey_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(crypto.QuorumHash))
	})
	return _c
}

func (_c *PrivValidator_GetPrivateKey_Call) Return(_a0 crypto.PrivKey, _a1 error) *PrivValidator_GetPrivateKey_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *PrivValidator_GetPrivateKey_Call) RunAndReturn(run func(context.Context, crypto.QuorumHash) (crypto.PrivKey, error)) *PrivValidator_GetPrivateKey_Call {
	_c.Call.Return(run)
	return _c
}

// GetProTxHash provides a mock function with given fields: _a0
func (_m *PrivValidator) GetProTxHash(_a0 context.Context) (crypto.ProTxHash, error) {
	ret := _m.Called(_a0)

	if len(ret) == 0 {
		panic("no return value specified for GetProTxHash")
	}

	var r0 crypto.ProTxHash
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context) (crypto.ProTxHash, error)); ok {
		return rf(_a0)
	}
	if rf, ok := ret.Get(0).(func(context.Context) crypto.ProTxHash); ok {
		r0 = rf(_a0)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(crypto.ProTxHash)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context) error); ok {
		r1 = rf(_a0)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// PrivValidator_GetProTxHash_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetProTxHash'
type PrivValidator_GetProTxHash_Call struct {
	*mock.Call
}

// GetProTxHash is a helper method to define mock.On call
//   - _a0 context.Context
func (_e *PrivValidator_Expecter) GetProTxHash(_a0 interface{}) *PrivValidator_GetProTxHash_Call {
	return &PrivValidator_GetProTxHash_Call{Call: _e.mock.On("GetProTxHash", _a0)}
}

func (_c *PrivValidator_GetProTxHash_Call) Run(run func(_a0 context.Context)) *PrivValidator_GetProTxHash_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context))
	})
	return _c
}

func (_c *PrivValidator_GetProTxHash_Call) Return(_a0 crypto.ProTxHash, _a1 error) *PrivValidator_GetProTxHash_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *PrivValidator_GetProTxHash_Call) RunAndReturn(run func(context.Context) (crypto.ProTxHash, error)) *PrivValidator_GetProTxHash_Call {
	_c.Call.Return(run)
	return _c
}

// GetPubKey provides a mock function with given fields: ctx, quorumHash
func (_m *PrivValidator) GetPubKey(ctx context.Context, quorumHash crypto.QuorumHash) (crypto.PubKey, error) {
	ret := _m.Called(ctx, quorumHash)

	if len(ret) == 0 {
		panic("no return value specified for GetPubKey")
	}

	var r0 crypto.PubKey
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, crypto.QuorumHash) (crypto.PubKey, error)); ok {
		return rf(ctx, quorumHash)
	}
	if rf, ok := ret.Get(0).(func(context.Context, crypto.QuorumHash) crypto.PubKey); ok {
		r0 = rf(ctx, quorumHash)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(crypto.PubKey)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, crypto.QuorumHash) error); ok {
		r1 = rf(ctx, quorumHash)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// PrivValidator_GetPubKey_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetPubKey'
type PrivValidator_GetPubKey_Call struct {
	*mock.Call
}

// GetPubKey is a helper method to define mock.On call
//   - ctx context.Context
//   - quorumHash crypto.QuorumHash
func (_e *PrivValidator_Expecter) GetPubKey(ctx interface{}, quorumHash interface{}) *PrivValidator_GetPubKey_Call {
	return &PrivValidator_GetPubKey_Call{Call: _e.mock.On("GetPubKey", ctx, quorumHash)}
}

func (_c *PrivValidator_GetPubKey_Call) Run(run func(ctx context.Context, quorumHash crypto.QuorumHash)) *PrivValidator_GetPubKey_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(crypto.QuorumHash))
	})
	return _c
}

func (_c *PrivValidator_GetPubKey_Call) Return(_a0 crypto.PubKey, _a1 error) *PrivValidator_GetPubKey_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *PrivValidator_GetPubKey_Call) RunAndReturn(run func(context.Context, crypto.QuorumHash) (crypto.PubKey, error)) *PrivValidator_GetPubKey_Call {
	_c.Call.Return(run)
	return _c
}

// GetThresholdPublicKey provides a mock function with given fields: ctx, quorumHash
func (_m *PrivValidator) GetThresholdPublicKey(ctx context.Context, quorumHash crypto.QuorumHash) (crypto.PubKey, error) {
	ret := _m.Called(ctx, quorumHash)

	if len(ret) == 0 {
		panic("no return value specified for GetThresholdPublicKey")
	}

	var r0 crypto.PubKey
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, crypto.QuorumHash) (crypto.PubKey, error)); ok {
		return rf(ctx, quorumHash)
	}
	if rf, ok := ret.Get(0).(func(context.Context, crypto.QuorumHash) crypto.PubKey); ok {
		r0 = rf(ctx, quorumHash)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(crypto.PubKey)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, crypto.QuorumHash) error); ok {
		r1 = rf(ctx, quorumHash)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// PrivValidator_GetThresholdPublicKey_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetThresholdPublicKey'
type PrivValidator_GetThresholdPublicKey_Call struct {
	*mock.Call
}

// GetThresholdPublicKey is a helper method to define mock.On call
//   - ctx context.Context
//   - quorumHash crypto.QuorumHash
func (_e *PrivValidator_Expecter) GetThresholdPublicKey(ctx interface{}, quorumHash interface{}) *PrivValidator_GetThresholdPublicKey_Call {
	return &PrivValidator_GetThresholdPublicKey_Call{Call: _e.mock.On("GetThresholdPublicKey", ctx, quorumHash)}
}

func (_c *PrivValidator_GetThresholdPublicKey_Call) Run(run func(ctx context.Context, quorumHash crypto.QuorumHash)) *PrivValidator_GetThresholdPublicKey_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(crypto.QuorumHash))
	})
	return _c
}

func (_c *PrivValidator_GetThresholdPublicKey_Call) Return(_a0 crypto.PubKey, _a1 error) *PrivValidator_GetThresholdPublicKey_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *PrivValidator_GetThresholdPublicKey_Call) RunAndReturn(run func(context.Context, crypto.QuorumHash) (crypto.PubKey, error)) *PrivValidator_GetThresholdPublicKey_Call {
	_c.Call.Return(run)
	return _c
}

// SignProposal provides a mock function with given fields: ctx, chainID, quorumType, quorumHash, proposal
func (_m *PrivValidator) SignProposal(ctx context.Context, chainID string, quorumType btcjson.LLMQType, quorumHash crypto.QuorumHash, proposal *tenderminttypes.Proposal) (bytes.HexBytes, error) {
	ret := _m.Called(ctx, chainID, quorumType, quorumHash, proposal)

	if len(ret) == 0 {
		panic("no return value specified for SignProposal")
	}

	var r0 bytes.HexBytes
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, string, btcjson.LLMQType, crypto.QuorumHash, *tenderminttypes.Proposal) (bytes.HexBytes, error)); ok {
		return rf(ctx, chainID, quorumType, quorumHash, proposal)
	}
	if rf, ok := ret.Get(0).(func(context.Context, string, btcjson.LLMQType, crypto.QuorumHash, *tenderminttypes.Proposal) bytes.HexBytes); ok {
		r0 = rf(ctx, chainID, quorumType, quorumHash, proposal)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(bytes.HexBytes)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, string, btcjson.LLMQType, crypto.QuorumHash, *tenderminttypes.Proposal) error); ok {
		r1 = rf(ctx, chainID, quorumType, quorumHash, proposal)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// PrivValidator_SignProposal_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'SignProposal'
type PrivValidator_SignProposal_Call struct {
	*mock.Call
}

// SignProposal is a helper method to define mock.On call
//   - ctx context.Context
//   - chainID string
//   - quorumType btcjson.LLMQType
//   - quorumHash crypto.QuorumHash
//   - proposal *tenderminttypes.Proposal
func (_e *PrivValidator_Expecter) SignProposal(ctx interface{}, chainID interface{}, quorumType interface{}, quorumHash interface{}, proposal interface{}) *PrivValidator_SignProposal_Call {
	return &PrivValidator_SignProposal_Call{Call: _e.mock.On("SignProposal", ctx, chainID, quorumType, quorumHash, proposal)}
}

func (_c *PrivValidator_SignProposal_Call) Run(run func(ctx context.Context, chainID string, quorumType btcjson.LLMQType, quorumHash crypto.QuorumHash, proposal *tenderminttypes.Proposal)) *PrivValidator_SignProposal_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(string), args[2].(btcjson.LLMQType), args[3].(crypto.QuorumHash), args[4].(*tenderminttypes.Proposal))
	})
	return _c
}

func (_c *PrivValidator_SignProposal_Call) Return(_a0 bytes.HexBytes, _a1 error) *PrivValidator_SignProposal_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *PrivValidator_SignProposal_Call) RunAndReturn(run func(context.Context, string, btcjson.LLMQType, crypto.QuorumHash, *tenderminttypes.Proposal) (bytes.HexBytes, error)) *PrivValidator_SignProposal_Call {
	_c.Call.Return(run)
	return _c
}

// SignVote provides a mock function with given fields: ctx, chainID, quorumType, quorumHash, vote, logger
func (_m *PrivValidator) SignVote(ctx context.Context, chainID string, quorumType btcjson.LLMQType, quorumHash crypto.QuorumHash, vote *tenderminttypes.Vote, logger log.Logger) error {
	ret := _m.Called(ctx, chainID, quorumType, quorumHash, vote, logger)

	if len(ret) == 0 {
		panic("no return value specified for SignVote")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, string, btcjson.LLMQType, crypto.QuorumHash, *tenderminttypes.Vote, log.Logger) error); ok {
		r0 = rf(ctx, chainID, quorumType, quorumHash, vote, logger)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// PrivValidator_SignVote_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'SignVote'
type PrivValidator_SignVote_Call struct {
	*mock.Call
}

// SignVote is a helper method to define mock.On call
//   - ctx context.Context
//   - chainID string
//   - quorumType btcjson.LLMQType
//   - quorumHash crypto.QuorumHash
//   - vote *tenderminttypes.Vote
//   - logger log.Logger
func (_e *PrivValidator_Expecter) SignVote(ctx interface{}, chainID interface{}, quorumType interface{}, quorumHash interface{}, vote interface{}, logger interface{}) *PrivValidator_SignVote_Call {
	return &PrivValidator_SignVote_Call{Call: _e.mock.On("SignVote", ctx, chainID, quorumType, quorumHash, vote, logger)}
}

func (_c *PrivValidator_SignVote_Call) Run(run func(ctx context.Context, chainID string, quorumType btcjson.LLMQType, quorumHash crypto.QuorumHash, vote *tenderminttypes.Vote, logger log.Logger)) *PrivValidator_SignVote_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(string), args[2].(btcjson.LLMQType), args[3].(crypto.QuorumHash), args[4].(*tenderminttypes.Vote), args[5].(log.Logger))
	})
	return _c
}

func (_c *PrivValidator_SignVote_Call) Return(_a0 error) *PrivValidator_SignVote_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *PrivValidator_SignVote_Call) RunAndReturn(run func(context.Context, string, btcjson.LLMQType, crypto.QuorumHash, *tenderminttypes.Vote, log.Logger) error) *PrivValidator_SignVote_Call {
	_c.Call.Return(run)
	return _c
}

// UpdatePrivateKey provides a mock function with given fields: ctx, privateKey, quorumHash, thresholdPublicKey, height
func (_m *PrivValidator) UpdatePrivateKey(ctx context.Context, privateKey crypto.PrivKey, quorumHash crypto.QuorumHash, thresholdPublicKey crypto.PubKey, height int64) {
	_m.Called(ctx, privateKey, quorumHash, thresholdPublicKey, height)
}

// PrivValidator_UpdatePrivateKey_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'UpdatePrivateKey'
type PrivValidator_UpdatePrivateKey_Call struct {
	*mock.Call
}

// UpdatePrivateKey is a helper method to define mock.On call
//   - ctx context.Context
//   - privateKey crypto.PrivKey
//   - quorumHash crypto.QuorumHash
//   - thresholdPublicKey crypto.PubKey
//   - height int64
func (_e *PrivValidator_Expecter) UpdatePrivateKey(ctx interface{}, privateKey interface{}, quorumHash interface{}, thresholdPublicKey interface{}, height interface{}) *PrivValidator_UpdatePrivateKey_Call {
	return &PrivValidator_UpdatePrivateKey_Call{Call: _e.mock.On("UpdatePrivateKey", ctx, privateKey, quorumHash, thresholdPublicKey, height)}
}

func (_c *PrivValidator_UpdatePrivateKey_Call) Run(run func(ctx context.Context, privateKey crypto.PrivKey, quorumHash crypto.QuorumHash, thresholdPublicKey crypto.PubKey, height int64)) *PrivValidator_UpdatePrivateKey_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(crypto.PrivKey), args[2].(crypto.QuorumHash), args[3].(crypto.PubKey), args[4].(int64))
	})
	return _c
}

func (_c *PrivValidator_UpdatePrivateKey_Call) Return() *PrivValidator_UpdatePrivateKey_Call {
	_c.Call.Return()
	return _c
}

func (_c *PrivValidator_UpdatePrivateKey_Call) RunAndReturn(run func(context.Context, crypto.PrivKey, crypto.QuorumHash, crypto.PubKey, int64)) *PrivValidator_UpdatePrivateKey_Call {
	_c.Run(run)
	return _c
}

// NewPrivValidator creates a new instance of PrivValidator. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewPrivValidator(t interface {
	mock.TestingT
	Cleanup(func())
}) *PrivValidator {
	mock := &PrivValidator{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
