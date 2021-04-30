// Code generated by mockery v2.7.4. DO NOT EDIT.

package credentials

import (
	kafka_nais_io_v1 "github.com/nais/liberator/pkg/apis/kafka.nais.io/v1"
	mock "github.com/stretchr/testify/mock"

	v1 "k8s.io/api/core/v1"
)

// MockHandler is an autogenerated mock type for the Handler type
type MockHandler struct {
	mock.Mock
}

// Apply provides a mock function with given fields: application, secret
func (_m *MockHandler) Apply(application *kafka_nais_io_v1.AivenApplication, secret *v1.Secret) error {
	ret := _m.Called(application, secret)

	var r0 error
	if rf, ok := ret.Get(0).(func(*kafka_nais_io_v1.AivenApplication, *v1.Secret) error); ok {
		r0 = rf(application, secret)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}
