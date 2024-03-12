// Code generated by mockery. DO NOT EDIT.

package mocks

import (
	context "context"

	p2p "github.com/dashpay/tenderdash/internal/p2p"
	mock "github.com/stretchr/testify/mock"
)

// Channel is an autogenerated mock type for the Channel type
type Channel struct {
	mock.Mock
}

// Err provides a mock function with given fields:
func (_m *Channel) Err() error {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for Err")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func() error); ok {
		r0 = rf()
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Receive provides a mock function with given fields: _a0
func (_m *Channel) Receive(_a0 context.Context) p2p.ChannelIterator {
	ret := _m.Called(_a0)

	if len(ret) == 0 {
		panic("no return value specified for Receive")
	}

	var r0 p2p.ChannelIterator
	if rf, ok := ret.Get(0).(func(context.Context) p2p.ChannelIterator); ok {
		r0 = rf(_a0)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(p2p.ChannelIterator)
		}
	}

	return r0
}

// Send provides a mock function with given fields: _a0, _a1
func (_m *Channel) Send(_a0 context.Context, _a1 p2p.Envelope) error {
	ret := _m.Called(_a0, _a1)

	if len(ret) == 0 {
		panic("no return value specified for Send")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, p2p.Envelope) error); ok {
		r0 = rf(_a0, _a1)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// SendError provides a mock function with given fields: _a0, _a1
func (_m *Channel) SendError(_a0 context.Context, _a1 p2p.PeerError) error {
	ret := _m.Called(_a0, _a1)

	if len(ret) == 0 {
		panic("no return value specified for SendError")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, p2p.PeerError) error); ok {
		r0 = rf(_a0, _a1)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// String provides a mock function with given fields:
func (_m *Channel) String() string {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for String")
	}

	var r0 string
	if rf, ok := ret.Get(0).(func() string); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(string)
	}

	return r0
}

// NewChannel creates a new instance of Channel. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewChannel(t interface {
	mock.TestingT
	Cleanup(func())
}) *Channel {
	mock := &Channel{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
