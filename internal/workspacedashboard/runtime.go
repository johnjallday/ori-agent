package workspacedashboard

import (
	"context"
	"fmt"

	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

// operationIDs is the v1 read-only operation vocabulary a dashboard may call.
// It is a fixed list rather than an allowlist over a generic query path, so the
// complete set of data a dashboard can read is reviewable in one place and
// adding to it is a visible decision.
func operationIDs() []string {
	return nil
}

// operations declares the trusted policy for each id in operationIDs. The two
// must stay in agreement: workspacesurface.ValidateRegistration rejects a
// binding whose operation set does not match its descriptor.
func operations() map[string]workspacesurface.Operation {
	return map[string]workspacesurface.Operation{}
}

// Runtime serves a dashboard's read-only data operations. It implements
// workspacesurface.Runtime like any other, so dashboard operations dispatch
// through the existing broker with no parallel plumbing and no exemption from
// input validation, output bounds, or timeouts.
//
// Every operation is scoped to the workspace in the trusted WorkspaceContext,
// which the browser cannot construct or override.
type Runtime struct{}

// NewRuntime creates the read-only dashboard runtime.
func NewRuntime() *Runtime {
	return &Runtime{}
}

// Status reports the dashboard's health. A dashboard that resolved at all has
// its files in place, so there is nothing further to check: unlike a plugin
// service there is no process to be up or down.
func (r *Runtime) Status(context.Context, workspacesurface.WorkspaceContext) (workspacesurface.StationStatus, error) {
	return workspacesurface.StationStatus{
		State: workspacesurface.StationReady, Value: "Ready",
		Description: "This workspace's dashboard is ready.",
	}, nil
}

// Invoke dispatches one declared read-only operation.
//
// An unknown operation id is an error with no fallthrough to another operation
// and no partial data: a dashboard must never be able to reach data by guessing
// at a name.
func (r *Runtime) Invoke(_ context.Context, invocation workspacesurface.Invocation) (workspacesurface.Result, error) {
	return workspacesurface.Result{}, fmt.Errorf("workspace dashboard operation %q is not available", invocation.Operation)
}
