// Code generated by MockGen. DO NOT EDIT.
// Source: github.com/erigontech/erigon/cl/phase1/network/services (interfaces: VoluntaryExitService)
//
// Generated by this command:
//
//	mockgen -typed=true -destination=./mock_services/voluntary_exit_service_mock.go -package=mock_services . VoluntaryExitService
//

// Package mock_services is a generated GoMock package.
package mock_services

import (
	context "context"
	reflect "reflect"

	services "github.com/erigontech/erigon/cl/phase1/network/services"
	gomock "go.uber.org/mock/gomock"
)

// MockVoluntaryExitService is a mock of VoluntaryExitService interface.
type MockVoluntaryExitService struct {
	ctrl     *gomock.Controller
	recorder *MockVoluntaryExitServiceMockRecorder
	isgomock struct{}
}

// MockVoluntaryExitServiceMockRecorder is the mock recorder for MockVoluntaryExitService.
type MockVoluntaryExitServiceMockRecorder struct {
	mock *MockVoluntaryExitService
}

// NewMockVoluntaryExitService creates a new mock instance.
func NewMockVoluntaryExitService(ctrl *gomock.Controller) *MockVoluntaryExitService {
	mock := &MockVoluntaryExitService{ctrl: ctrl}
	mock.recorder = &MockVoluntaryExitServiceMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockVoluntaryExitService) EXPECT() *MockVoluntaryExitServiceMockRecorder {
	return m.recorder
}

// ProcessMessage mocks base method.
func (m *MockVoluntaryExitService) ProcessMessage(ctx context.Context, subnet *uint64, msg *services.SignedVoluntaryExitForGossip) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ProcessMessage", ctx, subnet, msg)
	ret0, _ := ret[0].(error)
	return ret0
}

// ProcessMessage indicates an expected call of ProcessMessage.
func (mr *MockVoluntaryExitServiceMockRecorder) ProcessMessage(ctx, subnet, msg any) *MockVoluntaryExitServiceProcessMessageCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ProcessMessage", reflect.TypeOf((*MockVoluntaryExitService)(nil).ProcessMessage), ctx, subnet, msg)
	return &MockVoluntaryExitServiceProcessMessageCall{Call: call}
}

// MockVoluntaryExitServiceProcessMessageCall wrap *gomock.Call
type MockVoluntaryExitServiceProcessMessageCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *MockVoluntaryExitServiceProcessMessageCall) Return(arg0 error) *MockVoluntaryExitServiceProcessMessageCall {
	c.Call = c.Call.Return(arg0)
	return c
}

// Do rewrite *gomock.Call.Do
func (c *MockVoluntaryExitServiceProcessMessageCall) Do(f func(context.Context, *uint64, *services.SignedVoluntaryExitForGossip) error) *MockVoluntaryExitServiceProcessMessageCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *MockVoluntaryExitServiceProcessMessageCall) DoAndReturn(f func(context.Context, *uint64, *services.SignedVoluntaryExitForGossip) error) *MockVoluntaryExitServiceProcessMessageCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}
