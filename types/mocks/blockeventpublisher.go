// Code generated by mockery. DO NOT EDIT.

package mocks

import (
	types "github.com/dashpay/tenderdash/types"
	mock "github.com/stretchr/testify/mock"
)

// BlockEventPublisher is an autogenerated mock type for the BlockEventPublisher type
type BlockEventPublisher struct {
	mock.Mock
}

type BlockEventPublisher_Expecter struct {
	mock *mock.Mock
}

func (_m *BlockEventPublisher) EXPECT() *BlockEventPublisher_Expecter {
	return &BlockEventPublisher_Expecter{mock: &_m.Mock}
}

// PublishEventNewBlock provides a mock function with given fields: _a0
func (_m *BlockEventPublisher) PublishEventNewBlock(_a0 types.EventDataNewBlock) error {
	ret := _m.Called(_a0)

	if len(ret) == 0 {
		panic("no return value specified for PublishEventNewBlock")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(types.EventDataNewBlock) error); ok {
		r0 = rf(_a0)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// BlockEventPublisher_PublishEventNewBlock_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'PublishEventNewBlock'
type BlockEventPublisher_PublishEventNewBlock_Call struct {
	*mock.Call
}

// PublishEventNewBlock is a helper method to define mock.On call
//   - _a0 types.EventDataNewBlock
func (_e *BlockEventPublisher_Expecter) PublishEventNewBlock(_a0 interface{}) *BlockEventPublisher_PublishEventNewBlock_Call {
	return &BlockEventPublisher_PublishEventNewBlock_Call{Call: _e.mock.On("PublishEventNewBlock", _a0)}
}

func (_c *BlockEventPublisher_PublishEventNewBlock_Call) Run(run func(_a0 types.EventDataNewBlock)) *BlockEventPublisher_PublishEventNewBlock_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(types.EventDataNewBlock))
	})
	return _c
}

func (_c *BlockEventPublisher_PublishEventNewBlock_Call) Return(_a0 error) *BlockEventPublisher_PublishEventNewBlock_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *BlockEventPublisher_PublishEventNewBlock_Call) RunAndReturn(run func(types.EventDataNewBlock) error) *BlockEventPublisher_PublishEventNewBlock_Call {
	_c.Call.Return(run)
	return _c
}

// PublishEventNewBlockHeader provides a mock function with given fields: _a0
func (_m *BlockEventPublisher) PublishEventNewBlockHeader(_a0 types.EventDataNewBlockHeader) error {
	ret := _m.Called(_a0)

	if len(ret) == 0 {
		panic("no return value specified for PublishEventNewBlockHeader")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(types.EventDataNewBlockHeader) error); ok {
		r0 = rf(_a0)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// BlockEventPublisher_PublishEventNewBlockHeader_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'PublishEventNewBlockHeader'
type BlockEventPublisher_PublishEventNewBlockHeader_Call struct {
	*mock.Call
}

// PublishEventNewBlockHeader is a helper method to define mock.On call
//   - _a0 types.EventDataNewBlockHeader
func (_e *BlockEventPublisher_Expecter) PublishEventNewBlockHeader(_a0 interface{}) *BlockEventPublisher_PublishEventNewBlockHeader_Call {
	return &BlockEventPublisher_PublishEventNewBlockHeader_Call{Call: _e.mock.On("PublishEventNewBlockHeader", _a0)}
}

func (_c *BlockEventPublisher_PublishEventNewBlockHeader_Call) Run(run func(_a0 types.EventDataNewBlockHeader)) *BlockEventPublisher_PublishEventNewBlockHeader_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(types.EventDataNewBlockHeader))
	})
	return _c
}

func (_c *BlockEventPublisher_PublishEventNewBlockHeader_Call) Return(_a0 error) *BlockEventPublisher_PublishEventNewBlockHeader_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *BlockEventPublisher_PublishEventNewBlockHeader_Call) RunAndReturn(run func(types.EventDataNewBlockHeader) error) *BlockEventPublisher_PublishEventNewBlockHeader_Call {
	_c.Call.Return(run)
	return _c
}

// PublishEventNewEvidence provides a mock function with given fields: _a0
func (_m *BlockEventPublisher) PublishEventNewEvidence(_a0 types.EventDataNewEvidence) error {
	ret := _m.Called(_a0)

	if len(ret) == 0 {
		panic("no return value specified for PublishEventNewEvidence")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(types.EventDataNewEvidence) error); ok {
		r0 = rf(_a0)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// BlockEventPublisher_PublishEventNewEvidence_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'PublishEventNewEvidence'
type BlockEventPublisher_PublishEventNewEvidence_Call struct {
	*mock.Call
}

// PublishEventNewEvidence is a helper method to define mock.On call
//   - _a0 types.EventDataNewEvidence
func (_e *BlockEventPublisher_Expecter) PublishEventNewEvidence(_a0 interface{}) *BlockEventPublisher_PublishEventNewEvidence_Call {
	return &BlockEventPublisher_PublishEventNewEvidence_Call{Call: _e.mock.On("PublishEventNewEvidence", _a0)}
}

func (_c *BlockEventPublisher_PublishEventNewEvidence_Call) Run(run func(_a0 types.EventDataNewEvidence)) *BlockEventPublisher_PublishEventNewEvidence_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(types.EventDataNewEvidence))
	})
	return _c
}

func (_c *BlockEventPublisher_PublishEventNewEvidence_Call) Return(_a0 error) *BlockEventPublisher_PublishEventNewEvidence_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *BlockEventPublisher_PublishEventNewEvidence_Call) RunAndReturn(run func(types.EventDataNewEvidence) error) *BlockEventPublisher_PublishEventNewEvidence_Call {
	_c.Call.Return(run)
	return _c
}

// PublishEventTx provides a mock function with given fields: _a0
func (_m *BlockEventPublisher) PublishEventTx(_a0 types.EventDataTx) error {
	ret := _m.Called(_a0)

	if len(ret) == 0 {
		panic("no return value specified for PublishEventTx")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(types.EventDataTx) error); ok {
		r0 = rf(_a0)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// BlockEventPublisher_PublishEventTx_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'PublishEventTx'
type BlockEventPublisher_PublishEventTx_Call struct {
	*mock.Call
}

// PublishEventTx is a helper method to define mock.On call
//   - _a0 types.EventDataTx
func (_e *BlockEventPublisher_Expecter) PublishEventTx(_a0 interface{}) *BlockEventPublisher_PublishEventTx_Call {
	return &BlockEventPublisher_PublishEventTx_Call{Call: _e.mock.On("PublishEventTx", _a0)}
}

func (_c *BlockEventPublisher_PublishEventTx_Call) Run(run func(_a0 types.EventDataTx)) *BlockEventPublisher_PublishEventTx_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(types.EventDataTx))
	})
	return _c
}

func (_c *BlockEventPublisher_PublishEventTx_Call) Return(_a0 error) *BlockEventPublisher_PublishEventTx_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *BlockEventPublisher_PublishEventTx_Call) RunAndReturn(run func(types.EventDataTx) error) *BlockEventPublisher_PublishEventTx_Call {
	_c.Call.Return(run)
	return _c
}

// PublishEventValidatorSetUpdates provides a mock function with given fields: _a0
func (_m *BlockEventPublisher) PublishEventValidatorSetUpdates(_a0 types.EventDataValidatorSetUpdate) error {
	ret := _m.Called(_a0)

	if len(ret) == 0 {
		panic("no return value specified for PublishEventValidatorSetUpdates")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(types.EventDataValidatorSetUpdate) error); ok {
		r0 = rf(_a0)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// BlockEventPublisher_PublishEventValidatorSetUpdates_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'PublishEventValidatorSetUpdates'
type BlockEventPublisher_PublishEventValidatorSetUpdates_Call struct {
	*mock.Call
}

// PublishEventValidatorSetUpdates is a helper method to define mock.On call
//   - _a0 types.EventDataValidatorSetUpdate
func (_e *BlockEventPublisher_Expecter) PublishEventValidatorSetUpdates(_a0 interface{}) *BlockEventPublisher_PublishEventValidatorSetUpdates_Call {
	return &BlockEventPublisher_PublishEventValidatorSetUpdates_Call{Call: _e.mock.On("PublishEventValidatorSetUpdates", _a0)}
}

func (_c *BlockEventPublisher_PublishEventValidatorSetUpdates_Call) Run(run func(_a0 types.EventDataValidatorSetUpdate)) *BlockEventPublisher_PublishEventValidatorSetUpdates_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(types.EventDataValidatorSetUpdate))
	})
	return _c
}

func (_c *BlockEventPublisher_PublishEventValidatorSetUpdates_Call) Return(_a0 error) *BlockEventPublisher_PublishEventValidatorSetUpdates_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *BlockEventPublisher_PublishEventValidatorSetUpdates_Call) RunAndReturn(run func(types.EventDataValidatorSetUpdate) error) *BlockEventPublisher_PublishEventValidatorSetUpdates_Call {
	_c.Call.Return(run)
	return _c
}

// NewBlockEventPublisher creates a new instance of BlockEventPublisher. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewBlockEventPublisher(t interface {
	mock.TestingT
	Cleanup(func())
}) *BlockEventPublisher {
	mock := &BlockEventPublisher{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
