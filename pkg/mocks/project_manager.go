// Code generated by mockery v2.7.4. DO NOT EDIT.

package mocks

import mock "github.com/stretchr/testify/mock"

// ProjectManager is an autogenerated mock type for the ProjectManager type
type ProjectManager struct {
	mock.Mock
}

// GetCA provides a mock function with given fields: projectName
func (_m *ProjectManager) GetCA(projectName string) (string, error) {
	ret := _m.Called(projectName)

	var r0 string
	if rf, ok := ret.Get(0).(func(string) string); ok {
		r0 = rf(projectName)
	} else {
		r0 = ret.Get(0).(string)
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(string) error); ok {
		r1 = rf(projectName)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}
