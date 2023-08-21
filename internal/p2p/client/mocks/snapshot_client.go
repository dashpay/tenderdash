// Code generated by mockery. DO NOT EDIT.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"
	promise "github.com/tendermint/tendermint/libs/promise"

	statesync "github.com/tendermint/tendermint/proto/tendermint/statesync"

	types "github.com/tendermint/tendermint/types"
)

// SnapshotClient is an autogenerated mock type for the SnapshotClient type
type SnapshotClient struct {
	mock.Mock
}

// GetChunk provides a mock function with given fields: ctx, peerID, height, format, index
func (_m *SnapshotClient) GetChunk(ctx context.Context, peerID types.NodeID, height uint64, format uint32, index uint32) (*promise.Promise[*statesync.ChunkResponse], error) {
	ret := _m.Called(ctx, peerID, height, format, index)

	var r0 *promise.Promise[*statesync.ChunkResponse]
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, types.NodeID, uint64, uint32, uint32) (*promise.Promise[*statesync.ChunkResponse], error)); ok {
		return rf(ctx, peerID, height, format, index)
	}
	if rf, ok := ret.Get(0).(func(context.Context, types.NodeID, uint64, uint32, uint32) *promise.Promise[*statesync.ChunkResponse]); ok {
		r0 = rf(ctx, peerID, height, format, index)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*promise.Promise[*statesync.ChunkResponse])
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, types.NodeID, uint64, uint32, uint32) error); ok {
		r1 = rf(ctx, peerID, height, format, index)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetLightBlock provides a mock function with given fields: ctx, peerID, height
func (_m *SnapshotClient) GetLightBlock(ctx context.Context, peerID types.NodeID, height uint64) (*promise.Promise[*statesync.LightBlockResponse], error) {
	ret := _m.Called(ctx, peerID, height)

	var r0 *promise.Promise[*statesync.LightBlockResponse]
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, types.NodeID, uint64) (*promise.Promise[*statesync.LightBlockResponse], error)); ok {
		return rf(ctx, peerID, height)
	}
	if rf, ok := ret.Get(0).(func(context.Context, types.NodeID, uint64) *promise.Promise[*statesync.LightBlockResponse]); ok {
		r0 = rf(ctx, peerID, height)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*promise.Promise[*statesync.LightBlockResponse])
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, types.NodeID, uint64) error); ok {
		r1 = rf(ctx, peerID, height)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetParams provides a mock function with given fields: ctx, peerID, height
func (_m *SnapshotClient) GetParams(ctx context.Context, peerID types.NodeID, height uint64) (*promise.Promise[*statesync.ParamsResponse], error) {
	ret := _m.Called(ctx, peerID, height)

	var r0 *promise.Promise[*statesync.ParamsResponse]
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, types.NodeID, uint64) (*promise.Promise[*statesync.ParamsResponse], error)); ok {
		return rf(ctx, peerID, height)
	}
	if rf, ok := ret.Get(0).(func(context.Context, types.NodeID, uint64) *promise.Promise[*statesync.ParamsResponse]); ok {
		r0 = rf(ctx, peerID, height)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*promise.Promise[*statesync.ParamsResponse])
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, types.NodeID, uint64) error); ok {
		r1 = rf(ctx, peerID, height)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetSnapshots provides a mock function with given fields: ctx, peerID
func (_m *SnapshotClient) GetSnapshots(ctx context.Context, peerID types.NodeID) error {
	ret := _m.Called(ctx, peerID)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, types.NodeID) error); ok {
		r0 = rf(ctx, peerID)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// NewSnapshotClient creates a new instance of SnapshotClient. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewSnapshotClient(t interface {
	mock.TestingT
	Cleanup(func())
}) *SnapshotClient {
	mock := &SnapshotClient{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
