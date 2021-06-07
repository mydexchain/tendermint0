// Code generated by mockery v1.1.1. DO NOT EDIT.

package mocks

import (
	mock "github.com/stretchr/testify/mock"
	abcicli "github.com/mydexchain/tendermint0/abci/client"
)

// ClientCreator is an autogenerated mock type for the ClientCreator type
type ClientCreator struct {
	mock.Mock
}

// NewABCIClient provides a mock function with given fields:
func (_m *ClientCreator) NewABCIClient() (abcicli.Client, error) {
	ret := _m.Called()

	var r0 abcicli.Client
	if rf, ok := ret.Get(0).(func() abcicli.Client); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(abcicli.Client)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func() error); ok {
		r1 = rf()
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}
