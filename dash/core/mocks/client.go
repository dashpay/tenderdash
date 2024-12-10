// Code generated by mockery. DO NOT EDIT.

package mocks

import (
	btcjson "github.com/dashpay/dashd-go/btcjson"
	bytes "github.com/dashpay/tenderdash/libs/bytes"

	crypto "github.com/dashpay/tenderdash/crypto"

	mock "github.com/stretchr/testify/mock"
)

// Client is an autogenerated mock type for the Client type
type Client struct {
	mock.Mock
}

type Client_Expecter struct {
	mock *mock.Mock
}

func (_m *Client) EXPECT() *Client_Expecter {
	return &Client_Expecter{mock: &_m.Mock}
}

// Close provides a mock function with no fields
func (_m *Client) Close() error {
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

// Client_Close_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Close'
type Client_Close_Call struct {
	*mock.Call
}

// Close is a helper method to define mock.On call
func (_e *Client_Expecter) Close() *Client_Close_Call {
	return &Client_Close_Call{Call: _e.mock.On("Close")}
}

func (_c *Client_Close_Call) Run(run func()) *Client_Close_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *Client_Close_Call) Return(_a0 error) *Client_Close_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *Client_Close_Call) RunAndReturn(run func() error) *Client_Close_Call {
	_c.Call.Return(run)
	return _c
}

// GetNetworkInfo provides a mock function with no fields
func (_m *Client) GetNetworkInfo() (*btcjson.GetNetworkInfoResult, error) {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetNetworkInfo")
	}

	var r0 *btcjson.GetNetworkInfoResult
	var r1 error
	if rf, ok := ret.Get(0).(func() (*btcjson.GetNetworkInfoResult, error)); ok {
		return rf()
	}
	if rf, ok := ret.Get(0).(func() *btcjson.GetNetworkInfoResult); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*btcjson.GetNetworkInfoResult)
		}
	}

	if rf, ok := ret.Get(1).(func() error); ok {
		r1 = rf()
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Client_GetNetworkInfo_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetNetworkInfo'
type Client_GetNetworkInfo_Call struct {
	*mock.Call
}

// GetNetworkInfo is a helper method to define mock.On call
func (_e *Client_Expecter) GetNetworkInfo() *Client_GetNetworkInfo_Call {
	return &Client_GetNetworkInfo_Call{Call: _e.mock.On("GetNetworkInfo")}
}

func (_c *Client_GetNetworkInfo_Call) Run(run func()) *Client_GetNetworkInfo_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *Client_GetNetworkInfo_Call) Return(_a0 *btcjson.GetNetworkInfoResult, _a1 error) *Client_GetNetworkInfo_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *Client_GetNetworkInfo_Call) RunAndReturn(run func() (*btcjson.GetNetworkInfoResult, error)) *Client_GetNetworkInfo_Call {
	_c.Call.Return(run)
	return _c
}

// MasternodeListJSON provides a mock function with given fields: filter
func (_m *Client) MasternodeListJSON(filter string) (map[string]btcjson.MasternodelistResultJSON, error) {
	ret := _m.Called(filter)

	if len(ret) == 0 {
		panic("no return value specified for MasternodeListJSON")
	}

	var r0 map[string]btcjson.MasternodelistResultJSON
	var r1 error
	if rf, ok := ret.Get(0).(func(string) (map[string]btcjson.MasternodelistResultJSON, error)); ok {
		return rf(filter)
	}
	if rf, ok := ret.Get(0).(func(string) map[string]btcjson.MasternodelistResultJSON); ok {
		r0 = rf(filter)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(map[string]btcjson.MasternodelistResultJSON)
		}
	}

	if rf, ok := ret.Get(1).(func(string) error); ok {
		r1 = rf(filter)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Client_MasternodeListJSON_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'MasternodeListJSON'
type Client_MasternodeListJSON_Call struct {
	*mock.Call
}

// MasternodeListJSON is a helper method to define mock.On call
//   - filter string
func (_e *Client_Expecter) MasternodeListJSON(filter interface{}) *Client_MasternodeListJSON_Call {
	return &Client_MasternodeListJSON_Call{Call: _e.mock.On("MasternodeListJSON", filter)}
}

func (_c *Client_MasternodeListJSON_Call) Run(run func(filter string)) *Client_MasternodeListJSON_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(string))
	})
	return _c
}

func (_c *Client_MasternodeListJSON_Call) Return(_a0 map[string]btcjson.MasternodelistResultJSON, _a1 error) *Client_MasternodeListJSON_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *Client_MasternodeListJSON_Call) RunAndReturn(run func(string) (map[string]btcjson.MasternodelistResultJSON, error)) *Client_MasternodeListJSON_Call {
	_c.Call.Return(run)
	return _c
}

// MasternodeStatus provides a mock function with no fields
func (_m *Client) MasternodeStatus() (*btcjson.MasternodeStatusResult, error) {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for MasternodeStatus")
	}

	var r0 *btcjson.MasternodeStatusResult
	var r1 error
	if rf, ok := ret.Get(0).(func() (*btcjson.MasternodeStatusResult, error)); ok {
		return rf()
	}
	if rf, ok := ret.Get(0).(func() *btcjson.MasternodeStatusResult); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*btcjson.MasternodeStatusResult)
		}
	}

	if rf, ok := ret.Get(1).(func() error); ok {
		r1 = rf()
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Client_MasternodeStatus_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'MasternodeStatus'
type Client_MasternodeStatus_Call struct {
	*mock.Call
}

// MasternodeStatus is a helper method to define mock.On call
func (_e *Client_Expecter) MasternodeStatus() *Client_MasternodeStatus_Call {
	return &Client_MasternodeStatus_Call{Call: _e.mock.On("MasternodeStatus")}
}

func (_c *Client_MasternodeStatus_Call) Run(run func()) *Client_MasternodeStatus_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *Client_MasternodeStatus_Call) Return(_a0 *btcjson.MasternodeStatusResult, _a1 error) *Client_MasternodeStatus_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *Client_MasternodeStatus_Call) RunAndReturn(run func() (*btcjson.MasternodeStatusResult, error)) *Client_MasternodeStatus_Call {
	_c.Call.Return(run)
	return _c
}

// Ping provides a mock function with no fields
func (_m *Client) Ping() error {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for Ping")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func() error); ok {
		r0 = rf()
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Client_Ping_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Ping'
type Client_Ping_Call struct {
	*mock.Call
}

// Ping is a helper method to define mock.On call
func (_e *Client_Expecter) Ping() *Client_Ping_Call {
	return &Client_Ping_Call{Call: _e.mock.On("Ping")}
}

func (_c *Client_Ping_Call) Run(run func()) *Client_Ping_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *Client_Ping_Call) Return(_a0 error) *Client_Ping_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *Client_Ping_Call) RunAndReturn(run func() error) *Client_Ping_Call {
	_c.Call.Return(run)
	return _c
}

// QuorumInfo provides a mock function with given fields: quorumType, quorumHash
func (_m *Client) QuorumInfo(quorumType btcjson.LLMQType, quorumHash crypto.QuorumHash) (*btcjson.QuorumInfoResult, error) {
	ret := _m.Called(quorumType, quorumHash)

	if len(ret) == 0 {
		panic("no return value specified for QuorumInfo")
	}

	var r0 *btcjson.QuorumInfoResult
	var r1 error
	if rf, ok := ret.Get(0).(func(btcjson.LLMQType, crypto.QuorumHash) (*btcjson.QuorumInfoResult, error)); ok {
		return rf(quorumType, quorumHash)
	}
	if rf, ok := ret.Get(0).(func(btcjson.LLMQType, crypto.QuorumHash) *btcjson.QuorumInfoResult); ok {
		r0 = rf(quorumType, quorumHash)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*btcjson.QuorumInfoResult)
		}
	}

	if rf, ok := ret.Get(1).(func(btcjson.LLMQType, crypto.QuorumHash) error); ok {
		r1 = rf(quorumType, quorumHash)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Client_QuorumInfo_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'QuorumInfo'
type Client_QuorumInfo_Call struct {
	*mock.Call
}

// QuorumInfo is a helper method to define mock.On call
//   - quorumType btcjson.LLMQType
//   - quorumHash crypto.QuorumHash
func (_e *Client_Expecter) QuorumInfo(quorumType interface{}, quorumHash interface{}) *Client_QuorumInfo_Call {
	return &Client_QuorumInfo_Call{Call: _e.mock.On("QuorumInfo", quorumType, quorumHash)}
}

func (_c *Client_QuorumInfo_Call) Run(run func(quorumType btcjson.LLMQType, quorumHash crypto.QuorumHash)) *Client_QuorumInfo_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(btcjson.LLMQType), args[1].(crypto.QuorumHash))
	})
	return _c
}

func (_c *Client_QuorumInfo_Call) Return(_a0 *btcjson.QuorumInfoResult, _a1 error) *Client_QuorumInfo_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *Client_QuorumInfo_Call) RunAndReturn(run func(btcjson.LLMQType, crypto.QuorumHash) (*btcjson.QuorumInfoResult, error)) *Client_QuorumInfo_Call {
	_c.Call.Return(run)
	return _c
}

// QuorumSign provides a mock function with given fields: quorumType, requestID, messageHash, quorumHash
func (_m *Client) QuorumSign(quorumType btcjson.LLMQType, requestID bytes.HexBytes, messageHash bytes.HexBytes, quorumHash bytes.HexBytes) (*btcjson.QuorumSignResult, error) {
	ret := _m.Called(quorumType, requestID, messageHash, quorumHash)

	if len(ret) == 0 {
		panic("no return value specified for QuorumSign")
	}

	var r0 *btcjson.QuorumSignResult
	var r1 error
	if rf, ok := ret.Get(0).(func(btcjson.LLMQType, bytes.HexBytes, bytes.HexBytes, bytes.HexBytes) (*btcjson.QuorumSignResult, error)); ok {
		return rf(quorumType, requestID, messageHash, quorumHash)
	}
	if rf, ok := ret.Get(0).(func(btcjson.LLMQType, bytes.HexBytes, bytes.HexBytes, bytes.HexBytes) *btcjson.QuorumSignResult); ok {
		r0 = rf(quorumType, requestID, messageHash, quorumHash)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*btcjson.QuorumSignResult)
		}
	}

	if rf, ok := ret.Get(1).(func(btcjson.LLMQType, bytes.HexBytes, bytes.HexBytes, bytes.HexBytes) error); ok {
		r1 = rf(quorumType, requestID, messageHash, quorumHash)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Client_QuorumSign_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'QuorumSign'
type Client_QuorumSign_Call struct {
	*mock.Call
}

// QuorumSign is a helper method to define mock.On call
//   - quorumType btcjson.LLMQType
//   - requestID bytes.HexBytes
//   - messageHash bytes.HexBytes
//   - quorumHash bytes.HexBytes
func (_e *Client_Expecter) QuorumSign(quorumType interface{}, requestID interface{}, messageHash interface{}, quorumHash interface{}) *Client_QuorumSign_Call {
	return &Client_QuorumSign_Call{Call: _e.mock.On("QuorumSign", quorumType, requestID, messageHash, quorumHash)}
}

func (_c *Client_QuorumSign_Call) Run(run func(quorumType btcjson.LLMQType, requestID bytes.HexBytes, messageHash bytes.HexBytes, quorumHash bytes.HexBytes)) *Client_QuorumSign_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(btcjson.LLMQType), args[1].(bytes.HexBytes), args[2].(bytes.HexBytes), args[3].(bytes.HexBytes))
	})
	return _c
}

func (_c *Client_QuorumSign_Call) Return(_a0 *btcjson.QuorumSignResult, _a1 error) *Client_QuorumSign_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *Client_QuorumSign_Call) RunAndReturn(run func(btcjson.LLMQType, bytes.HexBytes, bytes.HexBytes, bytes.HexBytes) (*btcjson.QuorumSignResult, error)) *Client_QuorumSign_Call {
	_c.Call.Return(run)
	return _c
}

// QuorumVerify provides a mock function with given fields: quorumType, requestID, messageHash, signature, quorumHash
func (_m *Client) QuorumVerify(quorumType btcjson.LLMQType, requestID bytes.HexBytes, messageHash bytes.HexBytes, signature bytes.HexBytes, quorumHash bytes.HexBytes) (bool, error) {
	ret := _m.Called(quorumType, requestID, messageHash, signature, quorumHash)

	if len(ret) == 0 {
		panic("no return value specified for QuorumVerify")
	}

	var r0 bool
	var r1 error
	if rf, ok := ret.Get(0).(func(btcjson.LLMQType, bytes.HexBytes, bytes.HexBytes, bytes.HexBytes, bytes.HexBytes) (bool, error)); ok {
		return rf(quorumType, requestID, messageHash, signature, quorumHash)
	}
	if rf, ok := ret.Get(0).(func(btcjson.LLMQType, bytes.HexBytes, bytes.HexBytes, bytes.HexBytes, bytes.HexBytes) bool); ok {
		r0 = rf(quorumType, requestID, messageHash, signature, quorumHash)
	} else {
		r0 = ret.Get(0).(bool)
	}

	if rf, ok := ret.Get(1).(func(btcjson.LLMQType, bytes.HexBytes, bytes.HexBytes, bytes.HexBytes, bytes.HexBytes) error); ok {
		r1 = rf(quorumType, requestID, messageHash, signature, quorumHash)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Client_QuorumVerify_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'QuorumVerify'
type Client_QuorumVerify_Call struct {
	*mock.Call
}

// QuorumVerify is a helper method to define mock.On call
//   - quorumType btcjson.LLMQType
//   - requestID bytes.HexBytes
//   - messageHash bytes.HexBytes
//   - signature bytes.HexBytes
//   - quorumHash bytes.HexBytes
func (_e *Client_Expecter) QuorumVerify(quorumType interface{}, requestID interface{}, messageHash interface{}, signature interface{}, quorumHash interface{}) *Client_QuorumVerify_Call {
	return &Client_QuorumVerify_Call{Call: _e.mock.On("QuorumVerify", quorumType, requestID, messageHash, signature, quorumHash)}
}

func (_c *Client_QuorumVerify_Call) Run(run func(quorumType btcjson.LLMQType, requestID bytes.HexBytes, messageHash bytes.HexBytes, signature bytes.HexBytes, quorumHash bytes.HexBytes)) *Client_QuorumVerify_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(btcjson.LLMQType), args[1].(bytes.HexBytes), args[2].(bytes.HexBytes), args[3].(bytes.HexBytes), args[4].(bytes.HexBytes))
	})
	return _c
}

func (_c *Client_QuorumVerify_Call) Return(_a0 bool, _a1 error) *Client_QuorumVerify_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *Client_QuorumVerify_Call) RunAndReturn(run func(btcjson.LLMQType, bytes.HexBytes, bytes.HexBytes, bytes.HexBytes, bytes.HexBytes) (bool, error)) *Client_QuorumVerify_Call {
	_c.Call.Return(run)
	return _c
}

// NewClient creates a new instance of Client. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewClient(t interface {
	mock.TestingT
	Cleanup(func())
}) *Client {
	mock := &Client{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
