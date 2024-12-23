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

type Channel_Expecter struct {
	mock *mock.Mock
}

func (_m *Channel) EXPECT() *Channel_Expecter {
	return &Channel_Expecter{mock: &_m.Mock}
}

// Err provides a mock function with no fields
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

// Channel_Err_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Err'
type Channel_Err_Call struct {
	*mock.Call
}

// Err is a helper method to define mock.On call
func (_e *Channel_Expecter) Err() *Channel_Err_Call {
	return &Channel_Err_Call{Call: _e.mock.On("Err")}
}

func (_c *Channel_Err_Call) Run(run func()) *Channel_Err_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *Channel_Err_Call) Return(_a0 error) *Channel_Err_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *Channel_Err_Call) RunAndReturn(run func() error) *Channel_Err_Call {
	_c.Call.Return(run)
	return _c
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

// Channel_Receive_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Receive'
type Channel_Receive_Call struct {
	*mock.Call
}

// Receive is a helper method to define mock.On call
//   - _a0 context.Context
func (_e *Channel_Expecter) Receive(_a0 interface{}) *Channel_Receive_Call {
	return &Channel_Receive_Call{Call: _e.mock.On("Receive", _a0)}
}

func (_c *Channel_Receive_Call) Run(run func(_a0 context.Context)) *Channel_Receive_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context))
	})
	return _c
}

func (_c *Channel_Receive_Call) Return(_a0 p2p.ChannelIterator) *Channel_Receive_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *Channel_Receive_Call) RunAndReturn(run func(context.Context) p2p.ChannelIterator) *Channel_Receive_Call {
	_c.Call.Return(run)
	return _c
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

// Channel_Send_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Send'
type Channel_Send_Call struct {
	*mock.Call
}

// Send is a helper method to define mock.On call
//   - _a0 context.Context
//   - _a1 p2p.Envelope
func (_e *Channel_Expecter) Send(_a0 interface{}, _a1 interface{}) *Channel_Send_Call {
	return &Channel_Send_Call{Call: _e.mock.On("Send", _a0, _a1)}
}

func (_c *Channel_Send_Call) Run(run func(_a0 context.Context, _a1 p2p.Envelope)) *Channel_Send_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(p2p.Envelope))
	})
	return _c
}

func (_c *Channel_Send_Call) Return(_a0 error) *Channel_Send_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *Channel_Send_Call) RunAndReturn(run func(context.Context, p2p.Envelope) error) *Channel_Send_Call {
	_c.Call.Return(run)
	return _c
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

// Channel_SendError_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'SendError'
type Channel_SendError_Call struct {
	*mock.Call
}

// SendError is a helper method to define mock.On call
//   - _a0 context.Context
//   - _a1 p2p.PeerError
func (_e *Channel_Expecter) SendError(_a0 interface{}, _a1 interface{}) *Channel_SendError_Call {
	return &Channel_SendError_Call{Call: _e.mock.On("SendError", _a0, _a1)}
}

func (_c *Channel_SendError_Call) Run(run func(_a0 context.Context, _a1 p2p.PeerError)) *Channel_SendError_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(p2p.PeerError))
	})
	return _c
}

func (_c *Channel_SendError_Call) Return(_a0 error) *Channel_SendError_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *Channel_SendError_Call) RunAndReturn(run func(context.Context, p2p.PeerError) error) *Channel_SendError_Call {
	_c.Call.Return(run)
	return _c
}

// String provides a mock function with no fields
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

// Channel_String_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'String'
type Channel_String_Call struct {
	*mock.Call
}

// String is a helper method to define mock.On call
func (_e *Channel_Expecter) String() *Channel_String_Call {
	return &Channel_String_Call{Call: _e.mock.On("String")}
}

func (_c *Channel_String_Call) Run(run func()) *Channel_String_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *Channel_String_Call) Return(_a0 string) *Channel_String_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *Channel_String_Call) RunAndReturn(run func() string) *Channel_String_Call {
	_c.Call.Return(run)
	return _c
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
