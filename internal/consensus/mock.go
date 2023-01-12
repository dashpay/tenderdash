package consensus

import "github.com/stretchr/testify/mock"

type mockWAL struct {
	WAL
	mock.Mock
}

func (m *mockWAL) FlushAndSync() error {
	ret := m.Called()
	var r0 any
	if rf, ok := ret.Get(0).(func() error); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0)
	}
	if r0 != nil {
		return r0.(error)
	}
	return nil
}
