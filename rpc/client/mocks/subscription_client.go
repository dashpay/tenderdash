// Code generated by mockery. DO NOT EDIT.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"
	coretypes "github.com/tendermint/tendermint/rpc/coretypes"
)

// SubscriptionClient is an autogenerated mock type for the SubscriptionClient type
type SubscriptionClient struct {
	mock.Mock
}

// Subscribe provides a mock function with given fields: ctx, subscriber, query, outCapacity
func (_m *SubscriptionClient) Subscribe(ctx context.Context, subscriber string, query string, outCapacity ...int) (<-chan coretypes.ResultEvent, error) {
	_va := make([]interface{}, len(outCapacity))
	for _i := range outCapacity {
		_va[_i] = outCapacity[_i]
	}
	var _ca []interface{}
	_ca = append(_ca, ctx, subscriber, query)
	_ca = append(_ca, _va...)
	ret := _m.Called(_ca...)

	var r0 <-chan coretypes.ResultEvent
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, string, string, ...int) (<-chan coretypes.ResultEvent, error)); ok {
		return rf(ctx, subscriber, query, outCapacity...)
	}
	if rf, ok := ret.Get(0).(func(context.Context, string, string, ...int) <-chan coretypes.ResultEvent); ok {
		r0 = rf(ctx, subscriber, query, outCapacity...)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(<-chan coretypes.ResultEvent)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, string, string, ...int) error); ok {
		r1 = rf(ctx, subscriber, query, outCapacity...)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Unsubscribe provides a mock function with given fields: ctx, subscriber, query
func (_m *SubscriptionClient) Unsubscribe(ctx context.Context, subscriber string, query string) error {
	ret := _m.Called(ctx, subscriber, query)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, string, string) error); ok {
		r0 = rf(ctx, subscriber, query)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// UnsubscribeAll provides a mock function with given fields: ctx, subscriber
func (_m *SubscriptionClient) UnsubscribeAll(ctx context.Context, subscriber string) error {
	ret := _m.Called(ctx, subscriber)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, string) error); ok {
		r0 = rf(ctx, subscriber)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// NewSubscriptionClient creates a new instance of SubscriptionClient. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewSubscriptionClient(t interface {
	mock.TestingT
	Cleanup(func())
}) *SubscriptionClient {
	mock := &SubscriptionClient{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
