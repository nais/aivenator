// Code generated by mockery v2.53.2. DO NOT EDIT.

package credentials

import (
	context "context"

	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"

	logrus "github.com/sirupsen/logrus"

	mock "github.com/stretchr/testify/mock"

	v1 "k8s.io/api/core/v1"
)

// MockHandler is an autogenerated mock type for the Handler type
type MockHandler struct {
	mock.Mock
}

type MockHandler_Expecter struct {
	mock *mock.Mock
}

func (_m *MockHandler) EXPECT() *MockHandler_Expecter {
	return &MockHandler_Expecter{mock: &_m.Mock}
}

// Apply provides a mock function with given fields: ctx, application, secret, logger
func (_m *MockHandler) Apply(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, secret *v1.Secret, logger logrus.FieldLogger) ([]*v1.Secret, error) {
	ret := _m.Called(ctx, application, secret, logger)

	if len(ret) == 0 {
		panic("no return value specified for Apply")
	}

	var r0 []*v1.Secret
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *aiven_nais_io_v1.AivenApplication, *v1.Secret, logrus.FieldLogger) ([]*v1.Secret, error)); ok {
		return rf(ctx, application, secret, logger)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *aiven_nais_io_v1.AivenApplication, *v1.Secret, logrus.FieldLogger) []*v1.Secret); ok {
		r0 = rf(ctx, application, secret, logger)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]*v1.Secret)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *aiven_nais_io_v1.AivenApplication, *v1.Secret, logrus.FieldLogger) error); ok {
		r1 = rf(ctx, application, secret, logger)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// MockHandler_Apply_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Apply'
type MockHandler_Apply_Call struct {
	*mock.Call
}

// Apply is a helper method to define mock.On call
//   - ctx context.Context
//   - application *aiven_nais_io_v1.AivenApplication
//   - secret *v1.Secret
//   - logger logrus.FieldLogger
func (_e *MockHandler_Expecter) Apply(ctx interface{}, application interface{}, secret interface{}, logger interface{}) *MockHandler_Apply_Call {
	return &MockHandler_Apply_Call{Call: _e.mock.On("Apply", ctx, application, secret, logger)}
}

func (_c *MockHandler_Apply_Call) Run(run func(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, secret *v1.Secret, logger logrus.FieldLogger)) *MockHandler_Apply_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(*aiven_nais_io_v1.AivenApplication), args[2].(*v1.Secret), args[3].(logrus.FieldLogger))
	})
	return _c
}

func (_c *MockHandler_Apply_Call) Return(_a0 []*v1.Secret, _a1 error) *MockHandler_Apply_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *MockHandler_Apply_Call) RunAndReturn(run func(context.Context, *aiven_nais_io_v1.AivenApplication, *v1.Secret, logrus.FieldLogger) ([]*v1.Secret, error)) *MockHandler_Apply_Call {
	_c.Call.Return(run)
	return _c
}

// Cleanup provides a mock function with given fields: ctx, secret, logger
func (_m *MockHandler) Cleanup(ctx context.Context, secret *v1.Secret, logger logrus.FieldLogger) error {
	ret := _m.Called(ctx, secret, logger)

	if len(ret) == 0 {
		panic("no return value specified for Cleanup")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, *v1.Secret, logrus.FieldLogger) error); ok {
		r0 = rf(ctx, secret, logger)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// MockHandler_Cleanup_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Cleanup'
type MockHandler_Cleanup_Call struct {
	*mock.Call
}

// Cleanup is a helper method to define mock.On call
//   - ctx context.Context
//   - secret *v1.Secret
//   - logger logrus.FieldLogger
func (_e *MockHandler_Expecter) Cleanup(ctx interface{}, secret interface{}, logger interface{}) *MockHandler_Cleanup_Call {
	return &MockHandler_Cleanup_Call{Call: _e.mock.On("Cleanup", ctx, secret, logger)}
}

func (_c *MockHandler_Cleanup_Call) Run(run func(ctx context.Context, secret *v1.Secret, logger logrus.FieldLogger)) *MockHandler_Cleanup_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(*v1.Secret), args[2].(logrus.FieldLogger))
	})
	return _c
}

func (_c *MockHandler_Cleanup_Call) Return(_a0 error) *MockHandler_Cleanup_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockHandler_Cleanup_Call) RunAndReturn(run func(context.Context, *v1.Secret, logrus.FieldLogger) error) *MockHandler_Cleanup_Call {
	_c.Call.Return(run)
	return _c
}

// NewMockHandler creates a new instance of MockHandler. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewMockHandler(t interface {
	mock.TestingT
	Cleanup(func())
}) *MockHandler {
	mock := &MockHandler{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
