// Code generated by mockery. DO NOT EDIT.

package mocks

import (
	selectproposer "github.com/dashpay/tenderdash/internal/consensus/versioned/selectproposer"
	state "github.com/dashpay/tenderdash/internal/state"
	mock "github.com/stretchr/testify/mock"

	tendermintstate "github.com/dashpay/tenderdash/proto/tendermint/state"

	types "github.com/dashpay/tenderdash/types"
)

// Store is an autogenerated mock type for the Store type
type Store struct {
	mock.Mock
}

type Store_Expecter struct {
	mock *mock.Mock
}

func (_m *Store) EXPECT() *Store_Expecter {
	return &Store_Expecter{mock: &_m.Mock}
}

// Bootstrap provides a mock function with given fields: _a0
func (_m *Store) Bootstrap(_a0 state.State) error {
	ret := _m.Called(_a0)

	if len(ret) == 0 {
		panic("no return value specified for Bootstrap")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(state.State) error); ok {
		r0 = rf(_a0)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Store_Bootstrap_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Bootstrap'
type Store_Bootstrap_Call struct {
	*mock.Call
}

// Bootstrap is a helper method to define mock.On call
//   - _a0 state.State
func (_e *Store_Expecter) Bootstrap(_a0 interface{}) *Store_Bootstrap_Call {
	return &Store_Bootstrap_Call{Call: _e.mock.On("Bootstrap", _a0)}
}

func (_c *Store_Bootstrap_Call) Run(run func(_a0 state.State)) *Store_Bootstrap_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(state.State))
	})
	return _c
}

func (_c *Store_Bootstrap_Call) Return(_a0 error) *Store_Bootstrap_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *Store_Bootstrap_Call) RunAndReturn(run func(state.State) error) *Store_Bootstrap_Call {
	_c.Call.Return(run)
	return _c
}

// Close provides a mock function with no fields
func (_m *Store) Close() error {
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

// Store_Close_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Close'
type Store_Close_Call struct {
	*mock.Call
}

// Close is a helper method to define mock.On call
func (_e *Store_Expecter) Close() *Store_Close_Call {
	return &Store_Close_Call{Call: _e.mock.On("Close")}
}

func (_c *Store_Close_Call) Run(run func()) *Store_Close_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *Store_Close_Call) Return(_a0 error) *Store_Close_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *Store_Close_Call) RunAndReturn(run func() error) *Store_Close_Call {
	_c.Call.Return(run)
	return _c
}

// Load provides a mock function with no fields
func (_m *Store) Load() (state.State, error) {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for Load")
	}

	var r0 state.State
	var r1 error
	if rf, ok := ret.Get(0).(func() (state.State, error)); ok {
		return rf()
	}
	if rf, ok := ret.Get(0).(func() state.State); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(state.State)
	}

	if rf, ok := ret.Get(1).(func() error); ok {
		r1 = rf()
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Store_Load_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Load'
type Store_Load_Call struct {
	*mock.Call
}

// Load is a helper method to define mock.On call
func (_e *Store_Expecter) Load() *Store_Load_Call {
	return &Store_Load_Call{Call: _e.mock.On("Load")}
}

func (_c *Store_Load_Call) Run(run func()) *Store_Load_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *Store_Load_Call) Return(_a0 state.State, _a1 error) *Store_Load_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *Store_Load_Call) RunAndReturn(run func() (state.State, error)) *Store_Load_Call {
	_c.Call.Return(run)
	return _c
}

// LoadABCIResponses provides a mock function with given fields: _a0
func (_m *Store) LoadABCIResponses(_a0 int64) (*tendermintstate.ABCIResponses, error) {
	ret := _m.Called(_a0)

	if len(ret) == 0 {
		panic("no return value specified for LoadABCIResponses")
	}

	var r0 *tendermintstate.ABCIResponses
	var r1 error
	if rf, ok := ret.Get(0).(func(int64) (*tendermintstate.ABCIResponses, error)); ok {
		return rf(_a0)
	}
	if rf, ok := ret.Get(0).(func(int64) *tendermintstate.ABCIResponses); ok {
		r0 = rf(_a0)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*tendermintstate.ABCIResponses)
		}
	}

	if rf, ok := ret.Get(1).(func(int64) error); ok {
		r1 = rf(_a0)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Store_LoadABCIResponses_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'LoadABCIResponses'
type Store_LoadABCIResponses_Call struct {
	*mock.Call
}

// LoadABCIResponses is a helper method to define mock.On call
//   - _a0 int64
func (_e *Store_Expecter) LoadABCIResponses(_a0 interface{}) *Store_LoadABCIResponses_Call {
	return &Store_LoadABCIResponses_Call{Call: _e.mock.On("LoadABCIResponses", _a0)}
}

func (_c *Store_LoadABCIResponses_Call) Run(run func(_a0 int64)) *Store_LoadABCIResponses_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(int64))
	})
	return _c
}

func (_c *Store_LoadABCIResponses_Call) Return(_a0 *tendermintstate.ABCIResponses, _a1 error) *Store_LoadABCIResponses_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *Store_LoadABCIResponses_Call) RunAndReturn(run func(int64) (*tendermintstate.ABCIResponses, error)) *Store_LoadABCIResponses_Call {
	_c.Call.Return(run)
	return _c
}

// LoadConsensusParams provides a mock function with given fields: _a0
func (_m *Store) LoadConsensusParams(_a0 int64) (types.ConsensusParams, error) {
	ret := _m.Called(_a0)

	if len(ret) == 0 {
		panic("no return value specified for LoadConsensusParams")
	}

	var r0 types.ConsensusParams
	var r1 error
	if rf, ok := ret.Get(0).(func(int64) (types.ConsensusParams, error)); ok {
		return rf(_a0)
	}
	if rf, ok := ret.Get(0).(func(int64) types.ConsensusParams); ok {
		r0 = rf(_a0)
	} else {
		r0 = ret.Get(0).(types.ConsensusParams)
	}

	if rf, ok := ret.Get(1).(func(int64) error); ok {
		r1 = rf(_a0)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Store_LoadConsensusParams_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'LoadConsensusParams'
type Store_LoadConsensusParams_Call struct {
	*mock.Call
}

// LoadConsensusParams is a helper method to define mock.On call
//   - _a0 int64
func (_e *Store_Expecter) LoadConsensusParams(_a0 interface{}) *Store_LoadConsensusParams_Call {
	return &Store_LoadConsensusParams_Call{Call: _e.mock.On("LoadConsensusParams", _a0)}
}

func (_c *Store_LoadConsensusParams_Call) Run(run func(_a0 int64)) *Store_LoadConsensusParams_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(int64))
	})
	return _c
}

func (_c *Store_LoadConsensusParams_Call) Return(_a0 types.ConsensusParams, _a1 error) *Store_LoadConsensusParams_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *Store_LoadConsensusParams_Call) RunAndReturn(run func(int64) (types.ConsensusParams, error)) *Store_LoadConsensusParams_Call {
	_c.Call.Return(run)
	return _c
}

// LoadValidators provides a mock function with given fields: _a0, _a1
func (_m *Store) LoadValidators(_a0 int64, _a1 selectproposer.BlockStore) (*types.ValidatorSet, error) {
	ret := _m.Called(_a0, _a1)

	if len(ret) == 0 {
		panic("no return value specified for LoadValidators")
	}

	var r0 *types.ValidatorSet
	var r1 error
	if rf, ok := ret.Get(0).(func(int64, selectproposer.BlockStore) (*types.ValidatorSet, error)); ok {
		return rf(_a0, _a1)
	}
	if rf, ok := ret.Get(0).(func(int64, selectproposer.BlockStore) *types.ValidatorSet); ok {
		r0 = rf(_a0, _a1)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.ValidatorSet)
		}
	}

	if rf, ok := ret.Get(1).(func(int64, selectproposer.BlockStore) error); ok {
		r1 = rf(_a0, _a1)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Store_LoadValidators_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'LoadValidators'
type Store_LoadValidators_Call struct {
	*mock.Call
}

// LoadValidators is a helper method to define mock.On call
//   - _a0 int64
//   - _a1 selectproposer.BlockStore
func (_e *Store_Expecter) LoadValidators(_a0 interface{}, _a1 interface{}) *Store_LoadValidators_Call {
	return &Store_LoadValidators_Call{Call: _e.mock.On("LoadValidators", _a0, _a1)}
}

func (_c *Store_LoadValidators_Call) Run(run func(_a0 int64, _a1 selectproposer.BlockStore)) *Store_LoadValidators_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(int64), args[1].(selectproposer.BlockStore))
	})
	return _c
}

func (_c *Store_LoadValidators_Call) Return(_a0 *types.ValidatorSet, _a1 error) *Store_LoadValidators_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *Store_LoadValidators_Call) RunAndReturn(run func(int64, selectproposer.BlockStore) (*types.ValidatorSet, error)) *Store_LoadValidators_Call {
	_c.Call.Return(run)
	return _c
}

// PruneStates provides a mock function with given fields: _a0
func (_m *Store) PruneStates(_a0 int64) error {
	ret := _m.Called(_a0)

	if len(ret) == 0 {
		panic("no return value specified for PruneStates")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(int64) error); ok {
		r0 = rf(_a0)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Store_PruneStates_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'PruneStates'
type Store_PruneStates_Call struct {
	*mock.Call
}

// PruneStates is a helper method to define mock.On call
//   - _a0 int64
func (_e *Store_Expecter) PruneStates(_a0 interface{}) *Store_PruneStates_Call {
	return &Store_PruneStates_Call{Call: _e.mock.On("PruneStates", _a0)}
}

func (_c *Store_PruneStates_Call) Run(run func(_a0 int64)) *Store_PruneStates_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(int64))
	})
	return _c
}

func (_c *Store_PruneStates_Call) Return(_a0 error) *Store_PruneStates_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *Store_PruneStates_Call) RunAndReturn(run func(int64) error) *Store_PruneStates_Call {
	_c.Call.Return(run)
	return _c
}

// Save provides a mock function with given fields: _a0
func (_m *Store) Save(_a0 state.State) error {
	ret := _m.Called(_a0)

	if len(ret) == 0 {
		panic("no return value specified for Save")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(state.State) error); ok {
		r0 = rf(_a0)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Store_Save_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Save'
type Store_Save_Call struct {
	*mock.Call
}

// Save is a helper method to define mock.On call
//   - _a0 state.State
func (_e *Store_Expecter) Save(_a0 interface{}) *Store_Save_Call {
	return &Store_Save_Call{Call: _e.mock.On("Save", _a0)}
}

func (_c *Store_Save_Call) Run(run func(_a0 state.State)) *Store_Save_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(state.State))
	})
	return _c
}

func (_c *Store_Save_Call) Return(_a0 error) *Store_Save_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *Store_Save_Call) RunAndReturn(run func(state.State) error) *Store_Save_Call {
	_c.Call.Return(run)
	return _c
}

// SaveABCIResponses provides a mock function with given fields: _a0, _a1
func (_m *Store) SaveABCIResponses(_a0 int64, _a1 tendermintstate.ABCIResponses) error {
	ret := _m.Called(_a0, _a1)

	if len(ret) == 0 {
		panic("no return value specified for SaveABCIResponses")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(int64, tendermintstate.ABCIResponses) error); ok {
		r0 = rf(_a0, _a1)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Store_SaveABCIResponses_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'SaveABCIResponses'
type Store_SaveABCIResponses_Call struct {
	*mock.Call
}

// SaveABCIResponses is a helper method to define mock.On call
//   - _a0 int64
//   - _a1 tendermintstate.ABCIResponses
func (_e *Store_Expecter) SaveABCIResponses(_a0 interface{}, _a1 interface{}) *Store_SaveABCIResponses_Call {
	return &Store_SaveABCIResponses_Call{Call: _e.mock.On("SaveABCIResponses", _a0, _a1)}
}

func (_c *Store_SaveABCIResponses_Call) Run(run func(_a0 int64, _a1 tendermintstate.ABCIResponses)) *Store_SaveABCIResponses_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(int64), args[1].(tendermintstate.ABCIResponses))
	})
	return _c
}

func (_c *Store_SaveABCIResponses_Call) Return(_a0 error) *Store_SaveABCIResponses_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *Store_SaveABCIResponses_Call) RunAndReturn(run func(int64, tendermintstate.ABCIResponses) error) *Store_SaveABCIResponses_Call {
	_c.Call.Return(run)
	return _c
}

// SaveValidatorSets provides a mock function with given fields: _a0, _a1, _a2
func (_m *Store) SaveValidatorSets(_a0 int64, _a1 int64, _a2 *types.ValidatorSet) error {
	ret := _m.Called(_a0, _a1, _a2)

	if len(ret) == 0 {
		panic("no return value specified for SaveValidatorSets")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(int64, int64, *types.ValidatorSet) error); ok {
		r0 = rf(_a0, _a1, _a2)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Store_SaveValidatorSets_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'SaveValidatorSets'
type Store_SaveValidatorSets_Call struct {
	*mock.Call
}

// SaveValidatorSets is a helper method to define mock.On call
//   - _a0 int64
//   - _a1 int64
//   - _a2 *types.ValidatorSet
func (_e *Store_Expecter) SaveValidatorSets(_a0 interface{}, _a1 interface{}, _a2 interface{}) *Store_SaveValidatorSets_Call {
	return &Store_SaveValidatorSets_Call{Call: _e.mock.On("SaveValidatorSets", _a0, _a1, _a2)}
}

func (_c *Store_SaveValidatorSets_Call) Run(run func(_a0 int64, _a1 int64, _a2 *types.ValidatorSet)) *Store_SaveValidatorSets_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(int64), args[1].(int64), args[2].(*types.ValidatorSet))
	})
	return _c
}

func (_c *Store_SaveValidatorSets_Call) Return(_a0 error) *Store_SaveValidatorSets_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *Store_SaveValidatorSets_Call) RunAndReturn(run func(int64, int64, *types.ValidatorSet) error) *Store_SaveValidatorSets_Call {
	_c.Call.Return(run)
	return _c
}

// NewStore creates a new instance of Store. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewStore(t interface {
	mock.TestingT
	Cleanup(func())
}) *Store {
	mock := &Store{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
