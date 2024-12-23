// Code generated by mockery. DO NOT EDIT.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"

	types "github.com/dashpay/tenderdash/types"
)

// Provider is an autogenerated mock type for the Provider type
type Provider struct {
	mock.Mock
}

type Provider_Expecter struct {
	mock *mock.Mock
}

func (_m *Provider) EXPECT() *Provider_Expecter {
	return &Provider_Expecter{mock: &_m.Mock}
}

// ID provides a mock function with no fields
func (_m *Provider) ID() string {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for ID")
	}

	var r0 string
	if rf, ok := ret.Get(0).(func() string); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(string)
	}

	return r0
}

// Provider_ID_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'ID'
type Provider_ID_Call struct {
	*mock.Call
}

// ID is a helper method to define mock.On call
func (_e *Provider_Expecter) ID() *Provider_ID_Call {
	return &Provider_ID_Call{Call: _e.mock.On("ID")}
}

func (_c *Provider_ID_Call) Run(run func()) *Provider_ID_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *Provider_ID_Call) Return(_a0 string) *Provider_ID_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *Provider_ID_Call) RunAndReturn(run func() string) *Provider_ID_Call {
	_c.Call.Return(run)
	return _c
}

// LightBlock provides a mock function with given fields: ctx, height
func (_m *Provider) LightBlock(ctx context.Context, height int64) (*types.LightBlock, error) {
	ret := _m.Called(ctx, height)

	if len(ret) == 0 {
		panic("no return value specified for LightBlock")
	}

	var r0 *types.LightBlock
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, int64) (*types.LightBlock, error)); ok {
		return rf(ctx, height)
	}
	if rf, ok := ret.Get(0).(func(context.Context, int64) *types.LightBlock); ok {
		r0 = rf(ctx, height)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.LightBlock)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, int64) error); ok {
		r1 = rf(ctx, height)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Provider_LightBlock_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'LightBlock'
type Provider_LightBlock_Call struct {
	*mock.Call
}

// LightBlock is a helper method to define mock.On call
//   - ctx context.Context
//   - height int64
func (_e *Provider_Expecter) LightBlock(ctx interface{}, height interface{}) *Provider_LightBlock_Call {
	return &Provider_LightBlock_Call{Call: _e.mock.On("LightBlock", ctx, height)}
}

func (_c *Provider_LightBlock_Call) Run(run func(ctx context.Context, height int64)) *Provider_LightBlock_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(int64))
	})
	return _c
}

func (_c *Provider_LightBlock_Call) Return(_a0 *types.LightBlock, _a1 error) *Provider_LightBlock_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *Provider_LightBlock_Call) RunAndReturn(run func(context.Context, int64) (*types.LightBlock, error)) *Provider_LightBlock_Call {
	_c.Call.Return(run)
	return _c
}

// ReportEvidence provides a mock function with given fields: _a0, _a1
func (_m *Provider) ReportEvidence(_a0 context.Context, _a1 types.Evidence) error {
	ret := _m.Called(_a0, _a1)

	if len(ret) == 0 {
		panic("no return value specified for ReportEvidence")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, types.Evidence) error); ok {
		r0 = rf(_a0, _a1)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Provider_ReportEvidence_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'ReportEvidence'
type Provider_ReportEvidence_Call struct {
	*mock.Call
}

// ReportEvidence is a helper method to define mock.On call
//   - _a0 context.Context
//   - _a1 types.Evidence
func (_e *Provider_Expecter) ReportEvidence(_a0 interface{}, _a1 interface{}) *Provider_ReportEvidence_Call {
	return &Provider_ReportEvidence_Call{Call: _e.mock.On("ReportEvidence", _a0, _a1)}
}

func (_c *Provider_ReportEvidence_Call) Run(run func(_a0 context.Context, _a1 types.Evidence)) *Provider_ReportEvidence_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(types.Evidence))
	})
	return _c
}

func (_c *Provider_ReportEvidence_Call) Return(_a0 error) *Provider_ReportEvidence_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *Provider_ReportEvidence_Call) RunAndReturn(run func(context.Context, types.Evidence) error) *Provider_ReportEvidence_Call {
	_c.Call.Return(run)
	return _c
}

// NewProvider creates a new instance of Provider. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewProvider(t interface {
	mock.TestingT
	Cleanup(func())
}) *Provider {
	mock := &Provider{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
