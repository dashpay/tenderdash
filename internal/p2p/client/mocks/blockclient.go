// Code generated by mockery. DO NOT EDIT.

package mocks

import (
	blocksync "github.com/dashpay/tenderdash/proto/tendermint/blocksync"

	context "context"

	mock "github.com/stretchr/testify/mock"

	promise "github.com/dashpay/tenderdash/libs/promise"

	types "github.com/dashpay/tenderdash/types"
)

// BlockClient is an autogenerated mock type for the BlockClient type
type BlockClient struct {
	mock.Mock
}

type BlockClient_Expecter struct {
	mock *mock.Mock
}

func (_m *BlockClient) EXPECT() *BlockClient_Expecter {
	return &BlockClient_Expecter{mock: &_m.Mock}
}

// GetBlock provides a mock function with given fields: ctx, height, peerID
func (_m *BlockClient) GetBlock(ctx context.Context, height int64, peerID types.NodeID) (*promise.Promise[*blocksync.BlockResponse], error) {
	ret := _m.Called(ctx, height, peerID)

	if len(ret) == 0 {
		panic("no return value specified for GetBlock")
	}

	var r0 *promise.Promise[*blocksync.BlockResponse]
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, int64, types.NodeID) (*promise.Promise[*blocksync.BlockResponse], error)); ok {
		return rf(ctx, height, peerID)
	}
	if rf, ok := ret.Get(0).(func(context.Context, int64, types.NodeID) *promise.Promise[*blocksync.BlockResponse]); ok {
		r0 = rf(ctx, height, peerID)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*promise.Promise[*blocksync.BlockResponse])
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, int64, types.NodeID) error); ok {
		r1 = rf(ctx, height, peerID)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// BlockClient_GetBlock_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetBlock'
type BlockClient_GetBlock_Call struct {
	*mock.Call
}

// GetBlock is a helper method to define mock.On call
//   - ctx context.Context
//   - height int64
//   - peerID types.NodeID
func (_e *BlockClient_Expecter) GetBlock(ctx interface{}, height interface{}, peerID interface{}) *BlockClient_GetBlock_Call {
	return &BlockClient_GetBlock_Call{Call: _e.mock.On("GetBlock", ctx, height, peerID)}
}

func (_c *BlockClient_GetBlock_Call) Run(run func(ctx context.Context, height int64, peerID types.NodeID)) *BlockClient_GetBlock_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(int64), args[2].(types.NodeID))
	})
	return _c
}

func (_c *BlockClient_GetBlock_Call) Return(_a0 *promise.Promise[*blocksync.BlockResponse], _a1 error) *BlockClient_GetBlock_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *BlockClient_GetBlock_Call) RunAndReturn(run func(context.Context, int64, types.NodeID) (*promise.Promise[*blocksync.BlockResponse], error)) *BlockClient_GetBlock_Call {
	_c.Call.Return(run)
	return _c
}

// GetSyncStatus provides a mock function with given fields: ctx
func (_m *BlockClient) GetSyncStatus(ctx context.Context) error {
	ret := _m.Called(ctx)

	if len(ret) == 0 {
		panic("no return value specified for GetSyncStatus")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context) error); ok {
		r0 = rf(ctx)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// BlockClient_GetSyncStatus_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetSyncStatus'
type BlockClient_GetSyncStatus_Call struct {
	*mock.Call
}

// GetSyncStatus is a helper method to define mock.On call
//   - ctx context.Context
func (_e *BlockClient_Expecter) GetSyncStatus(ctx interface{}) *BlockClient_GetSyncStatus_Call {
	return &BlockClient_GetSyncStatus_Call{Call: _e.mock.On("GetSyncStatus", ctx)}
}

func (_c *BlockClient_GetSyncStatus_Call) Run(run func(ctx context.Context)) *BlockClient_GetSyncStatus_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context))
	})
	return _c
}

func (_c *BlockClient_GetSyncStatus_Call) Return(_a0 error) *BlockClient_GetSyncStatus_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *BlockClient_GetSyncStatus_Call) RunAndReturn(run func(context.Context) error) *BlockClient_GetSyncStatus_Call {
	_c.Call.Return(run)
	return _c
}

// Send provides a mock function with given fields: ctx, msg
func (_m *BlockClient) Send(ctx context.Context, msg any) error {
	ret := _m.Called(ctx, msg)

	if len(ret) == 0 {
		panic("no return value specified for Send")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, any) error); ok {
		r0 = rf(ctx, msg)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// BlockClient_Send_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Send'
type BlockClient_Send_Call struct {
	*mock.Call
}

// Send is a helper method to define mock.On call
//   - ctx context.Context
//   - msg any
func (_e *BlockClient_Expecter) Send(ctx interface{}, msg interface{}) *BlockClient_Send_Call {
	return &BlockClient_Send_Call{Call: _e.mock.On("Send", ctx, msg)}
}

func (_c *BlockClient_Send_Call) Run(run func(ctx context.Context, msg any)) *BlockClient_Send_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(any))
	})
	return _c
}

func (_c *BlockClient_Send_Call) Return(_a0 error) *BlockClient_Send_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *BlockClient_Send_Call) RunAndReturn(run func(context.Context, any) error) *BlockClient_Send_Call {
	_c.Call.Return(run)
	return _c
}

// NewBlockClient creates a new instance of BlockClient. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewBlockClient(t interface {
	mock.TestingT
	Cleanup(func())
}) *BlockClient {
	mock := &BlockClient{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
