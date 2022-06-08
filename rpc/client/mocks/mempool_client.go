// Code generated by mockery. DO NOT EDIT.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"
	coretypes "github.com/tendermint/tendermint/rpc/coretypes"

	testing "testing"

	types "github.com/tendermint/tendermint/types"
)

// MempoolClient is an autogenerated mock type for the MempoolClient type
type MempoolClient struct {
	mock.Mock
}

// CheckTx provides a mock function with given fields: _a0, _a1
func (_m *MempoolClient) CheckTx(_a0 context.Context, _a1 types.Tx) (*coretypes.ResultCheckTx, error) {
	ret := _m.Called(_a0, _a1)

	var r0 *coretypes.ResultCheckTx
	if rf, ok := ret.Get(0).(func(context.Context, types.Tx) *coretypes.ResultCheckTx); ok {
		r0 = rf(_a0, _a1)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*coretypes.ResultCheckTx)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, types.Tx) error); ok {
		r1 = rf(_a0, _a1)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// NumUnconfirmedTxs provides a mock function with given fields: _a0
func (_m *MempoolClient) NumUnconfirmedTxs(_a0 context.Context) (*coretypes.ResultUnconfirmedTxs, error) {
	ret := _m.Called(_a0)

	var r0 *coretypes.ResultUnconfirmedTxs
	if rf, ok := ret.Get(0).(func(context.Context) *coretypes.ResultUnconfirmedTxs); ok {
		r0 = rf(_a0)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*coretypes.ResultUnconfirmedTxs)
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

// RemoveTx provides a mock function with given fields: _a0, _a1
func (_m *MempoolClient) RemoveTx(_a0 context.Context, _a1 types.TxKey) error {
	ret := _m.Called(_a0, _a1)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, types.TxKey) error); ok {
		r0 = rf(_a0, _a1)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// UnconfirmedTxs provides a mock function with given fields: ctx, page, perPage
func (_m *MempoolClient) UnconfirmedTxs(ctx context.Context, page *int, perPage *int) (*coretypes.ResultUnconfirmedTxs, error) {
	ret := _m.Called(ctx, page, perPage)

	var r0 *coretypes.ResultUnconfirmedTxs
	if rf, ok := ret.Get(0).(func(context.Context, *int, *int) *coretypes.ResultUnconfirmedTxs); ok {
		r0 = rf(ctx, page, perPage)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*coretypes.ResultUnconfirmedTxs)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, *int, *int) error); ok {
		r1 = rf(ctx, page, perPage)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// NewMempoolClient creates a new instance of MempoolClient. It also registers the testing.TB interface on the mock and a cleanup function to assert the mocks expectations.
func NewMempoolClient(t testing.TB) *MempoolClient {
	mock := &MempoolClient{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
