// Code generated by mockery 2.9.0. DO NOT EDIT.

package mocks

import (
	certificate "github.com/nais/aivenator/pkg/certificate"
	mock "github.com/stretchr/testify/mock"
)

// Generator is an autogenerated mock type for the Generator type
type Generator struct {
	mock.Mock
}

// MakeCredStores provides a mock function with given fields: accessKey, accessCert, caCert
func (_m *Generator) MakeCredStores(accessKey string, accessCert string, caCert string) (*certificate.CredStoreData, error) {
	ret := _m.Called(accessKey, accessCert, caCert)

	var r0 *certificate.CredStoreData
	if rf, ok := ret.Get(0).(func(string, string, string) *certificate.CredStoreData); ok {
		r0 = rf(accessKey, accessCert, caCert)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*certificate.CredStoreData)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(string, string, string) error); ok {
		r1 = rf(accessKey, accessCert, caCert)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}
