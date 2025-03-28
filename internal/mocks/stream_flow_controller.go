// Code generated by MockGen. DO NOT EDIT.
// Source: github.com/lucas-clemente/quic-go/internal/flowcontrol (interfaces: StreamFlowController)

// Package mocks is a generated GoMock package.
package mocks

import (
	reflect "reflect"

	protocol "github.com/For-ACGN/quic-go/internal/protocol"
	gomock "github.com/golang/mock/gomock"
)

// MockStreamFlowController is a mock of StreamFlowController interface
type MockStreamFlowController struct {
	ctrl     *gomock.Controller
	recorder *MockStreamFlowControllerMockRecorder
}

// MockStreamFlowControllerMockRecorder is the mock recorder for MockStreamFlowController
type MockStreamFlowControllerMockRecorder struct {
	mock *MockStreamFlowController
}

// NewMockStreamFlowController creates a new mock instance
func NewMockStreamFlowController(ctrl *gomock.Controller) *MockStreamFlowController {
	mock := &MockStreamFlowController{ctrl: ctrl}
	mock.recorder = &MockStreamFlowControllerMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use
func (m *MockStreamFlowController) EXPECT() *MockStreamFlowControllerMockRecorder {
	return m.recorder
}

// Abandon mocks base method
func (m *MockStreamFlowController) Abandon() {
	m.ctrl.T.Helper()
	m.ctrl.Call(m, "Abandon")
}

// Abandon indicates an expected call of Abandon
func (mr *MockStreamFlowControllerMockRecorder) Abandon() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Abandon", reflect.TypeOf((*MockStreamFlowController)(nil).Abandon))
}

// AddBytesRead mocks base method
func (m *MockStreamFlowController) AddBytesRead(arg0 protocol.ByteCount) {
	m.ctrl.T.Helper()
	m.ctrl.Call(m, "AddBytesRead", arg0)
}

// AddBytesRead indicates an expected call of AddBytesRead
func (mr *MockStreamFlowControllerMockRecorder) AddBytesRead(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "AddBytesRead", reflect.TypeOf((*MockStreamFlowController)(nil).AddBytesRead), arg0)
}

// AddBytesSent mocks base method
func (m *MockStreamFlowController) AddBytesSent(arg0 protocol.ByteCount) {
	m.ctrl.T.Helper()
	m.ctrl.Call(m, "AddBytesSent", arg0)
}

// AddBytesSent indicates an expected call of AddBytesSent
func (mr *MockStreamFlowControllerMockRecorder) AddBytesSent(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "AddBytesSent", reflect.TypeOf((*MockStreamFlowController)(nil).AddBytesSent), arg0)
}

// GetWindowUpdate mocks base method
func (m *MockStreamFlowController) GetWindowUpdate() protocol.ByteCount {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetWindowUpdate")
	ret0, _ := ret[0].(protocol.ByteCount)
	return ret0
}

// GetWindowUpdate indicates an expected call of GetWindowUpdate
func (mr *MockStreamFlowControllerMockRecorder) GetWindowUpdate() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetWindowUpdate", reflect.TypeOf((*MockStreamFlowController)(nil).GetWindowUpdate))
}

// IsNewlyBlocked mocks base method
func (m *MockStreamFlowController) IsNewlyBlocked() (bool, protocol.ByteCount) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "IsNewlyBlocked")
	ret0, _ := ret[0].(bool)
	ret1, _ := ret[1].(protocol.ByteCount)
	return ret0, ret1
}

// IsNewlyBlocked indicates an expected call of IsNewlyBlocked
func (mr *MockStreamFlowControllerMockRecorder) IsNewlyBlocked() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "IsNewlyBlocked", reflect.TypeOf((*MockStreamFlowController)(nil).IsNewlyBlocked))
}

// SendWindowSize mocks base method
func (m *MockStreamFlowController) SendWindowSize() protocol.ByteCount {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "SendWindowSize")
	ret0, _ := ret[0].(protocol.ByteCount)
	return ret0
}

// SendWindowSize indicates an expected call of SendWindowSize
func (mr *MockStreamFlowControllerMockRecorder) SendWindowSize() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "SendWindowSize", reflect.TypeOf((*MockStreamFlowController)(nil).SendWindowSize))
}

// UpdateHighestReceived mocks base method
func (m *MockStreamFlowController) UpdateHighestReceived(arg0 protocol.ByteCount, arg1 bool) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "UpdateHighestReceived", arg0, arg1)
	ret0, _ := ret[0].(error)
	return ret0
}

// UpdateHighestReceived indicates an expected call of UpdateHighestReceived
func (mr *MockStreamFlowControllerMockRecorder) UpdateHighestReceived(arg0, arg1 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "UpdateHighestReceived", reflect.TypeOf((*MockStreamFlowController)(nil).UpdateHighestReceived), arg0, arg1)
}

// UpdateSendWindow mocks base method
func (m *MockStreamFlowController) UpdateSendWindow(arg0 protocol.ByteCount) {
	m.ctrl.T.Helper()
	m.ctrl.Call(m, "UpdateSendWindow", arg0)
}

// UpdateSendWindow indicates an expected call of UpdateSendWindow
func (mr *MockStreamFlowControllerMockRecorder) UpdateSendWindow(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "UpdateSendWindow", reflect.TypeOf((*MockStreamFlowController)(nil).UpdateSendWindow), arg0)
}
