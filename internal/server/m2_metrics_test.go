package server

import (
"testing"
"time"
)

func TestBindM2MetricsStoresCallbacks(t *testing.T) {
// These should be callable after binding without panic
previewCreatedCalled := false
previewConsumedCalled := false
deployTransitionCalled := false
deployLeadTimeCalled := false

BindM2Metrics(
func(teamBucket string) {
previewCreatedCalled = true
},
func(outcome, teamBucket string) {
previewConsumedCalled = true
},
func(stateFrom, stateTo, teamBucket string) {
deployTransitionCalled = true
},
func(outcome, teamBucket string, duration time.Duration) {
deployLeadTimeCalled = true
},
)

// Verify recorders are bound
recordM2PreviewCreated("00")
if !previewCreatedCalled {
t.Error("preview created recorder not called")
}

recordM2PreviewConsumed("success", "00")
if !previewConsumedCalled {
t.Error("preview consumed recorder not called")
}

recordM2DeployRunTransition("queued", "preparing", "00")
if !deployTransitionCalled {
t.Error("deploy transition recorder not called")
}

recordM2DeployRunLeadTime("success", "00", 5*time.Second)
if !deployLeadTimeCalled {
t.Error("deploy lead time recorder not called")
}
}

func TestBindM2MetricsWithNilHandlersDefaultsToNoop(t *testing.T) {
// Binding with nil values should not panic
BindM2Metrics(nil, nil, nil, nil)

// Calling should not panic
recordM2PreviewCreated("00")
recordM2PreviewConsumed("success", "00")
recordM2DeployRunTransition("queued", "preparing", "00")
recordM2DeployRunLeadTime("success", "00", 5*time.Second)
}
