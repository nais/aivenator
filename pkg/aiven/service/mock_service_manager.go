// Code generated by mockery v2.53.2. DO NOT EDIT.

package service

import (
	context "context"

	aiven "github.com/aiven/aiven-go-client/v2"

	mock "github.com/stretchr/testify/mock"
)

// MockServiceManager is an autogenerated mock type for the ServiceManager type
type MockServiceManager struct {
	mock.Mock
}

type MockServiceManager_Expecter struct {
	mock *mock.Mock
}

func (_m *MockServiceManager) EXPECT() *MockServiceManager_Expecter {
	return &MockServiceManager_Expecter{mock: &_m.Mock}
}

// Get provides a mock function with given fields: ctx, projectName, serviceName
func (_m *MockServiceManager) Get(ctx context.Context, projectName string, serviceName string) (*aiven.Service, error) {
	ret := _m.Called(ctx, projectName, serviceName)

	if len(ret) == 0 {
		panic("no return value specified for Get")
	}

	var r0 *aiven.Service
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, string, string) (*aiven.Service, error)); ok {
		return rf(ctx, projectName, serviceName)
	}
	if rf, ok := ret.Get(0).(func(context.Context, string, string) *aiven.Service); ok {
		r0 = rf(ctx, projectName, serviceName)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*aiven.Service)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, string, string) error); ok {
		r1 = rf(ctx, projectName, serviceName)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// MockServiceManager_Get_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Get'
type MockServiceManager_Get_Call struct {
	*mock.Call
}

// Get is a helper method to define mock.On call
//   - ctx context.Context
//   - projectName string
//   - serviceName string
func (_e *MockServiceManager_Expecter) Get(ctx interface{}, projectName interface{}, serviceName interface{}) *MockServiceManager_Get_Call {
	return &MockServiceManager_Get_Call{Call: _e.mock.On("Get", ctx, projectName, serviceName)}
}

func (_c *MockServiceManager_Get_Call) Run(run func(ctx context.Context, projectName string, serviceName string)) *MockServiceManager_Get_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(string), args[2].(string))
	})
	return _c
}

func (_c *MockServiceManager_Get_Call) Return(_a0 *aiven.Service, _a1 error) *MockServiceManager_Get_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *MockServiceManager_Get_Call) RunAndReturn(run func(context.Context, string, string) (*aiven.Service, error)) *MockServiceManager_Get_Call {
	_c.Call.Return(run)
	return _c
}

// GetServiceAddresses provides a mock function with given fields: ctx, projectName, serviceName
func (_m *MockServiceManager) GetServiceAddresses(ctx context.Context, projectName string, serviceName string) (*ServiceAddresses, error) {
	ret := _m.Called(ctx, projectName, serviceName)

	if len(ret) == 0 {
		panic("no return value specified for GetServiceAddresses")
	}

	var r0 *ServiceAddresses
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, string, string) (*ServiceAddresses, error)); ok {
		return rf(ctx, projectName, serviceName)
	}
	if rf, ok := ret.Get(0).(func(context.Context, string, string) *ServiceAddresses); ok {
		r0 = rf(ctx, projectName, serviceName)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*ServiceAddresses)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, string, string) error); ok {
		r1 = rf(ctx, projectName, serviceName)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// MockServiceManager_GetServiceAddresses_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetServiceAddresses'
type MockServiceManager_GetServiceAddresses_Call struct {
	*mock.Call
}

// GetServiceAddresses is a helper method to define mock.On call
//   - ctx context.Context
//   - projectName string
//   - serviceName string
func (_e *MockServiceManager_Expecter) GetServiceAddresses(ctx interface{}, projectName interface{}, serviceName interface{}) *MockServiceManager_GetServiceAddresses_Call {
	return &MockServiceManager_GetServiceAddresses_Call{Call: _e.mock.On("GetServiceAddresses", ctx, projectName, serviceName)}
}

func (_c *MockServiceManager_GetServiceAddresses_Call) Run(run func(ctx context.Context, projectName string, serviceName string)) *MockServiceManager_GetServiceAddresses_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(string), args[2].(string))
	})
	return _c
}

func (_c *MockServiceManager_GetServiceAddresses_Call) Return(_a0 *ServiceAddresses, _a1 error) *MockServiceManager_GetServiceAddresses_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *MockServiceManager_GetServiceAddresses_Call) RunAndReturn(run func(context.Context, string, string) (*ServiceAddresses, error)) *MockServiceManager_GetServiceAddresses_Call {
	_c.Call.Return(run)
	return _c
}

// NewMockServiceManager creates a new instance of MockServiceManager. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewMockServiceManager(t interface {
	mock.TestingT
	Cleanup(func())
}) *MockServiceManager {
	mock := &MockServiceManager{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
