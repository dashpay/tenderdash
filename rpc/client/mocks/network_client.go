// Code generated by mockery. DO NOT EDIT.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"
	coretypes "github.com/tendermint/tendermint/rpc/coretypes"
)

// NetworkClient is an autogenerated mock type for the NetworkClient type
type NetworkClient struct {
	mock.Mock
}

// ConsensusParams provides a mock function with given fields: ctx, height
func (_m *NetworkClient) ConsensusParams(ctx context.Context, height *int64) (*coretypes.ResultConsensusParams, error) {
	ret := _m.Called(ctx, height)

	var r0 *coretypes.ResultConsensusParams
	if rf, ok := ret.Get(0).(func(context.Context, *int64) *coretypes.ResultConsensusParams); ok {
		r0 = rf(ctx, height)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*coretypes.ResultConsensusParams)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, *int64) error); ok {
		r1 = rf(ctx, height)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// ConsensusState provides a mock function with given fields: _a0
func (_m *NetworkClient) ConsensusState(_a0 context.Context) (*coretypes.ResultConsensusState, error) {
	ret := _m.Called(_a0)

	var r0 *coretypes.ResultConsensusState
	if rf, ok := ret.Get(0).(func(context.Context) *coretypes.ResultConsensusState); ok {
		r0 = rf(_a0)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*coretypes.ResultConsensusState)
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

// DumpConsensusState provides a mock function with given fields: _a0
func (_m *NetworkClient) DumpConsensusState(_a0 context.Context) (*coretypes.ResultDumpConsensusState, error) {
	ret := _m.Called(_a0)

	var r0 *coretypes.ResultDumpConsensusState
	if rf, ok := ret.Get(0).(func(context.Context) *coretypes.ResultDumpConsensusState); ok {
		r0 = rf(_a0)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*coretypes.ResultDumpConsensusState)
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

// Health provides a mock function with given fields: _a0
func (_m *NetworkClient) Health(_a0 context.Context) (*coretypes.ResultHealth, error) {
	ret := _m.Called(_a0)

	var r0 *coretypes.ResultHealth
	if rf, ok := ret.Get(0).(func(context.Context) *coretypes.ResultHealth); ok {
		r0 = rf(_a0)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*coretypes.ResultHealth)
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

// NetInfo provides a mock function with given fields: _a0
func (_m *NetworkClient) NetInfo(_a0 context.Context) (*coretypes.ResultNetInfo, error) {
	ret := _m.Called(_a0)

	var r0 *coretypes.ResultNetInfo
	if rf, ok := ret.Get(0).(func(context.Context) *coretypes.ResultNetInfo); ok {
		r0 = rf(_a0)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*coretypes.ResultNetInfo)
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

type mockConstructorTestingTNewNetworkClient interface {
	mock.TestingT
	Cleanup(func())
}

// NewNetworkClient creates a new instance of NetworkClient. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
func NewNetworkClient(t mockConstructorTestingTNewNetworkClient) *NetworkClient {
	mock := &NetworkClient{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
