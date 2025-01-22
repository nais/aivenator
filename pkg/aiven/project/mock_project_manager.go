// Code generated by mockery v2.51.1. DO NOT EDIT.

package project

import (
	context "context"

	mock "github.com/stretchr/testify/mock"
)

// MockProjectManager is an autogenerated mock type for the ProjectManager type
type MockProjectManager struct {
	mock.Mock
}

type MockProjectManager_Expecter struct {
	mock *mock.Mock
}

func (_m *MockProjectManager) EXPECT() *MockProjectManager_Expecter {
	return &MockProjectManager_Expecter{mock: &_m.Mock}
}

// GetCA provides a mock function with given fields: ctx, projectName
func (_m *MockProjectManager) GetCA(ctx context.Context, projectName string) (string, error) {
	ret := _m.Called(ctx, projectName)

	if len(ret) == 0 {
		panic("no return value specified for GetCA")
	}

	var r0 string
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, string) (string, error)); ok {
		return rf(ctx, projectName)
	}
	if rf, ok := ret.Get(0).(func(context.Context, string) string); ok {
		r0 = rf(ctx, projectName)
	} else {
		r0 = ret.Get(0).(string)
	}

	if rf, ok := ret.Get(1).(func(context.Context, string) error); ok {
		r1 = rf(ctx, projectName)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// MockProjectManager_GetCA_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetCA'
type MockProjectManager_GetCA_Call struct {
	*mock.Call
}

// GetCA is a helper method to define mock.On call
//   - ctx context.Context
//   - projectName string
func (_e *MockProjectManager_Expecter) GetCA(ctx interface{}, projectName interface{}) *MockProjectManager_GetCA_Call {
	return &MockProjectManager_GetCA_Call{Call: _e.mock.On("GetCA", ctx, projectName)}
}

func (_c *MockProjectManager_GetCA_Call) Run(run func(ctx context.Context, projectName string)) *MockProjectManager_GetCA_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(string))
	})
	return _c
}

func (_c *MockProjectManager_GetCA_Call) Return(_a0 string, _a1 error) *MockProjectManager_GetCA_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *MockProjectManager_GetCA_Call) RunAndReturn(run func(context.Context, string) (string, error)) *MockProjectManager_GetCA_Call {
	_c.Call.Return(run)
	return _c
}

// NewMockProjectManager creates a new instance of MockProjectManager. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewMockProjectManager(t interface {
	mock.TestingT
	Cleanup(func())
}) *MockProjectManager {
	mock := &MockProjectManager{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
