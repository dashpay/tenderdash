package consensus

import (
	"context"

	"github.com/stretchr/testify/mock"

	"github.com/tendermint/tendermint/types"
)

type mockConstructorTesting interface {
	mock.TestingT
	Cleanup(func())
}

type mockWAL struct {
	WAL
	mock.Mock
}

func newMockWAL(t mockConstructorTesting) *mockWAL {
	wal := &mockWAL{}
	wal.Mock.Test(t)
	t.Cleanup(func() { wal.AssertExpectations(t) })
	return wal
}

func (m *mockWAL) FlushAndSync() error {
	ret := m.Called()
	var r0 error
	if rf, ok := ret.Get(0).(func() error); ok {
		r0 = rf()
	} else {
		r0 = ret.Error(0)
	}
	return r0
}

type mockQueueSender struct {
	mock.Mock
}

func newMockQueueSender(t mockConstructorTesting) *mockQueueSender {
	sender := &mockQueueSender{}
	sender.Mock.Test(t)
	t.Cleanup(func() { sender.AssertExpectations(t) })
	return sender
}

func (m *mockQueueSender) send(ctx context.Context, msg Message, peerID types.NodeID) error {
	ret := m.Called(ctx, msg, peerID)
	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, Message, types.NodeID) error); ok {
		r0 = rf(ctx, msg, peerID)
	} else {
		r0 = ret.Error(0)
	}
	return r0
}
