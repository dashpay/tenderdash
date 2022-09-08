// Code generated by mockery. DO NOT EDIT.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"

	types "github.com/tendermint/tendermint/types"
)

// Provider is an autogenerated mock type for the Provider type
type Provider struct {
	mock.Mock
}

// ID provides a mock function with given fields:
func (_m *Provider) ID() string {
	ret := _m.Called()

	var r0 string
	if rf, ok := ret.Get(0).(func() string); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(string)
	}

	return r0
}

// LightBlock provides a mock function with given fields: ctx, height
func (_m *Provider) LightBlock(ctx context.Context, height int64) (*types.LightBlock, error) {
	ret := _m.Called(ctx, height)

	var r0 *types.LightBlock
	if rf, ok := ret.Get(0).(func(context.Context, int64) *types.LightBlock); ok {
		r0 = rf(ctx, height)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.LightBlock)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, int64) error); ok {
		r1 = rf(ctx, height)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// ReportEvidence provides a mock function with given fields: _a0, _a1
func (_m *Provider) ReportEvidence(_a0 context.Context, _a1 types.Evidence) error {
	ret := _m.Called(_a0, _a1)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, types.Evidence) error); ok {
		r0 = rf(_a0, _a1)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

type mockConstructorTestingTNewProvider interface {
	mock.TestingT
	Cleanup(func())
}

// NewProvider creates a new instance of Provider. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
func NewProvider(t mockConstructorTestingTNewProvider) *Provider {
	mock := &Provider{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
