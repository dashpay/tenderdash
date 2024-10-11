// Code generated by mockery. DO NOT EDIT.

package mocks

import (
	context "context"

	p2p "github.com/dashpay/tenderdash/internal/p2p"
	mock "github.com/stretchr/testify/mock"
)

// Transport is an autogenerated mock type for the Transport type
type Transport struct {
	mock.Mock
}

type Transport_Expecter struct {
	mock *mock.Mock
}

func (_m *Transport) EXPECT() *Transport_Expecter {
	return &Transport_Expecter{mock: &_m.Mock}
}

// Accept provides a mock function with given fields: _a0
func (_m *Transport) Accept(_a0 context.Context) (p2p.Connection, error) {
	ret := _m.Called(_a0)

	if len(ret) == 0 {
		panic("no return value specified for Accept")
	}

	var r0 p2p.Connection
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context) (p2p.Connection, error)); ok {
		return rf(_a0)
	}
	if rf, ok := ret.Get(0).(func(context.Context) p2p.Connection); ok {
		r0 = rf(_a0)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(p2p.Connection)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context) error); ok {
		r1 = rf(_a0)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Transport_Accept_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Accept'
type Transport_Accept_Call struct {
	*mock.Call
}

// Accept is a helper method to define mock.On call
//   - _a0 context.Context
func (_e *Transport_Expecter) Accept(_a0 interface{}) *Transport_Accept_Call {
	return &Transport_Accept_Call{Call: _e.mock.On("Accept", _a0)}
}

func (_c *Transport_Accept_Call) Run(run func(_a0 context.Context)) *Transport_Accept_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context))
	})
	return _c
}

func (_c *Transport_Accept_Call) Return(_a0 p2p.Connection, _a1 error) *Transport_Accept_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *Transport_Accept_Call) RunAndReturn(run func(context.Context) (p2p.Connection, error)) *Transport_Accept_Call {
	_c.Call.Return(run)
	return _c
}

// AddChannelDescriptors provides a mock function with given fields: _a0
func (_m *Transport) AddChannelDescriptors(_a0 []*p2p.ChannelDescriptor) {
	_m.Called(_a0)
}

// Transport_AddChannelDescriptors_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'AddChannelDescriptors'
type Transport_AddChannelDescriptors_Call struct {
	*mock.Call
}

// AddChannelDescriptors is a helper method to define mock.On call
//   - _a0 []*p2p.ChannelDescriptor
func (_e *Transport_Expecter) AddChannelDescriptors(_a0 interface{}) *Transport_AddChannelDescriptors_Call {
	return &Transport_AddChannelDescriptors_Call{Call: _e.mock.On("AddChannelDescriptors", _a0)}
}

func (_c *Transport_AddChannelDescriptors_Call) Run(run func(_a0 []*p2p.ChannelDescriptor)) *Transport_AddChannelDescriptors_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].([]*p2p.ChannelDescriptor))
	})
	return _c
}

func (_c *Transport_AddChannelDescriptors_Call) Return() *Transport_AddChannelDescriptors_Call {
	_c.Call.Return()
	return _c
}

func (_c *Transport_AddChannelDescriptors_Call) RunAndReturn(run func([]*p2p.ChannelDescriptor)) *Transport_AddChannelDescriptors_Call {
	_c.Call.Return(run)
	return _c
}

// Close provides a mock function with given fields:
func (_m *Transport) Close() error {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for Close")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func() error); ok {
		r0 = rf()
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Transport_Close_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Close'
type Transport_Close_Call struct {
	*mock.Call
}

// Close is a helper method to define mock.On call
func (_e *Transport_Expecter) Close() *Transport_Close_Call {
	return &Transport_Close_Call{Call: _e.mock.On("Close")}
}

func (_c *Transport_Close_Call) Run(run func()) *Transport_Close_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *Transport_Close_Call) Return(_a0 error) *Transport_Close_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *Transport_Close_Call) RunAndReturn(run func() error) *Transport_Close_Call {
	_c.Call.Return(run)
	return _c
}

// Dial provides a mock function with given fields: _a0, _a1
func (_m *Transport) Dial(_a0 context.Context, _a1 *p2p.Endpoint) (p2p.Connection, error) {
	ret := _m.Called(_a0, _a1)

	if len(ret) == 0 {
		panic("no return value specified for Dial")
	}

	var r0 p2p.Connection
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *p2p.Endpoint) (p2p.Connection, error)); ok {
		return rf(_a0, _a1)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *p2p.Endpoint) p2p.Connection); ok {
		r0 = rf(_a0, _a1)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(p2p.Connection)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *p2p.Endpoint) error); ok {
		r1 = rf(_a0, _a1)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Transport_Dial_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Dial'
type Transport_Dial_Call struct {
	*mock.Call
}

// Dial is a helper method to define mock.On call
//   - _a0 context.Context
//   - _a1 *p2p.Endpoint
func (_e *Transport_Expecter) Dial(_a0 interface{}, _a1 interface{}) *Transport_Dial_Call {
	return &Transport_Dial_Call{Call: _e.mock.On("Dial", _a0, _a1)}
}

func (_c *Transport_Dial_Call) Run(run func(_a0 context.Context, _a1 *p2p.Endpoint)) *Transport_Dial_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(*p2p.Endpoint))
	})
	return _c
}

func (_c *Transport_Dial_Call) Return(_a0 p2p.Connection, _a1 error) *Transport_Dial_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *Transport_Dial_Call) RunAndReturn(run func(context.Context, *p2p.Endpoint) (p2p.Connection, error)) *Transport_Dial_Call {
	_c.Call.Return(run)
	return _c
}

// Endpoint provides a mock function with given fields:
func (_m *Transport) Endpoint() (*p2p.Endpoint, error) {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for Endpoint")
	}

	var r0 *p2p.Endpoint
	var r1 error
	if rf, ok := ret.Get(0).(func() (*p2p.Endpoint, error)); ok {
		return rf()
	}
	if rf, ok := ret.Get(0).(func() *p2p.Endpoint); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*p2p.Endpoint)
		}
	}

	if rf, ok := ret.Get(1).(func() error); ok {
		r1 = rf()
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Transport_Endpoint_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Endpoint'
type Transport_Endpoint_Call struct {
	*mock.Call
}

// Endpoint is a helper method to define mock.On call
func (_e *Transport_Expecter) Endpoint() *Transport_Endpoint_Call {
	return &Transport_Endpoint_Call{Call: _e.mock.On("Endpoint")}
}

func (_c *Transport_Endpoint_Call) Run(run func()) *Transport_Endpoint_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *Transport_Endpoint_Call) Return(_a0 *p2p.Endpoint, _a1 error) *Transport_Endpoint_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *Transport_Endpoint_Call) RunAndReturn(run func() (*p2p.Endpoint, error)) *Transport_Endpoint_Call {
	_c.Call.Return(run)
	return _c
}

// Listen provides a mock function with given fields: _a0
func (_m *Transport) Listen(_a0 *p2p.Endpoint) error {
	ret := _m.Called(_a0)

	if len(ret) == 0 {
		panic("no return value specified for Listen")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(*p2p.Endpoint) error); ok {
		r0 = rf(_a0)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Transport_Listen_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Listen'
type Transport_Listen_Call struct {
	*mock.Call
}

// Listen is a helper method to define mock.On call
//   - _a0 *p2p.Endpoint
func (_e *Transport_Expecter) Listen(_a0 interface{}) *Transport_Listen_Call {
	return &Transport_Listen_Call{Call: _e.mock.On("Listen", _a0)}
}

func (_c *Transport_Listen_Call) Run(run func(_a0 *p2p.Endpoint)) *Transport_Listen_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(*p2p.Endpoint))
	})
	return _c
}

func (_c *Transport_Listen_Call) Return(_a0 error) *Transport_Listen_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *Transport_Listen_Call) RunAndReturn(run func(*p2p.Endpoint) error) *Transport_Listen_Call {
	_c.Call.Return(run)
	return _c
}

// Protocols provides a mock function with given fields:
func (_m *Transport) Protocols() []p2p.Protocol {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for Protocols")
	}

	var r0 []p2p.Protocol
	if rf, ok := ret.Get(0).(func() []p2p.Protocol); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]p2p.Protocol)
		}
	}

	return r0
}

// Transport_Protocols_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Protocols'
type Transport_Protocols_Call struct {
	*mock.Call
}

// Protocols is a helper method to define mock.On call
func (_e *Transport_Expecter) Protocols() *Transport_Protocols_Call {
	return &Transport_Protocols_Call{Call: _e.mock.On("Protocols")}
}

func (_c *Transport_Protocols_Call) Run(run func()) *Transport_Protocols_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *Transport_Protocols_Call) Return(_a0 []p2p.Protocol) *Transport_Protocols_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *Transport_Protocols_Call) RunAndReturn(run func() []p2p.Protocol) *Transport_Protocols_Call {
	_c.Call.Return(run)
	return _c
}

// String provides a mock function with given fields:
func (_m *Transport) String() string {
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

// Transport_String_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'String'
type Transport_String_Call struct {
	*mock.Call
}

// String is a helper method to define mock.On call
func (_e *Transport_Expecter) String() *Transport_String_Call {
	return &Transport_String_Call{Call: _e.mock.On("String")}
}

func (_c *Transport_String_Call) Run(run func()) *Transport_String_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *Transport_String_Call) Return(_a0 string) *Transport_String_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *Transport_String_Call) RunAndReturn(run func() string) *Transport_String_Call {
	_c.Call.Return(run)
	return _c
}

// NewTransport creates a new instance of Transport. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewTransport(t interface {
	mock.TestingT
	Cleanup(func())
}) *Transport {
	mock := &Transport{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
