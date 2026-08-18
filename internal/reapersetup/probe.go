package reapersetup

import (
	"context"
	"time"
)

// ProbeState is the bounded internal vocabulary shared by platform probes.
// Probe results are never exposed directly: the runtime adapter maps them to
// stable, redacted reason and action codes.
type ProbeState string

const (
	ProbeReady       ProbeState = "ready"
	ProbeMissing     ProbeState = "missing"
	ProbeInvalid     ProbeState = "invalid"
	ProbeUnknown     ProbeState = "unknown"
	ProbeUnsupported ProbeState = "unsupported"
)

// ApplicationObservation reports only installation state. Detecting an app
// must never launch it or infer that it is currently running.
type ApplicationObservation struct {
	State ProbeState
}

// WebRemoteObservation is trusted process-local data. Ports are deliberately
// not part of runtimecapability's public status model and must never be logged.
// Port remains the preferred/legacy candidate; Ports carries every bounded,
// trusted configured interface so live probing can tolerate a stale sibling.
type WebRemoteObservation struct {
	State ProbeState
	Port  int
	Ports []int
}

// RunnerObservation contains the canonical exchange root and registered action
// identifier needed by trusted verification. These values remain adapter
// internals and are never copied into status, analytics, or task errors.
type RunnerObservation struct {
	State     ProbeState
	Root      string
	CommandID string
}

const (
	TransportAvailable   = "available"
	TransportOffline     = "offline"
	TransportUnavailable = "unavailable"
	TransportMalformed   = "malformed"
	TransportCheckFailed = "check_failed"
)

type LiveTransportObservation struct {
	State string
	Port  int
}

const (
	VerificationSucceeded        = "succeeded"
	VerificationWrongProject     = "wrong_project"
	VerificationProjectMissing   = "project_missing"
	VerificationTimedOut         = "timeout"
	VerificationRunnerFailed     = "runner_failed"
	VerificationPermissionDenied = "permission_denied"
	VerificationMalformed        = "malformed"
	VerificationCheckFailed      = "check_failed"
)

// VerificationTarget is assembled exclusively from trusted workspace,
// platform, and runner state. No browser or blueprint field can provide one.
type VerificationTarget struct {
	ExpectedProject string
	WebRemote       WebRemoteObservation
	Runner          RunnerObservation
	Timeout         time.Duration
}

type VerificationObservation struct {
	State string
}

// The deliberately small platform-neutral probe interfaces keep macOS paths,
// REAPER configuration details, HTTP endpoints, and the runner protocol out of
// the generalized runtime contract.
type ApplicationProbe interface {
	DetectApplication(context.Context) ApplicationObservation
}

type WebRemoteProbe interface {
	DetectWebRemote(context.Context) WebRemoteObservation
}

type RunnerProbe interface {
	DetectRunner(context.Context) RunnerObservation
}

type LiveTransportProbe interface {
	CheckTransport(context.Context, WebRemoteObservation) LiveTransportObservation
}

type ProjectVerifier interface {
	VerifyProject(context.Context, VerificationTarget) VerificationObservation
}

type platformProber interface {
	ApplicationProbe
	WebRemoteProbe
	RunnerProbe
	LiveTransportProbe
	ProjectVerifier
}

// ProbeSet is injected into RuntimeAdapter so every state and timeout is
// deterministic in tests. Production receives one compiled platform probe.
type ProbeSet struct {
	Application ApplicationProbe
	WebRemote   WebRemoteProbe
	Runner      RunnerProbe
	Transport   LiveTransportProbe
	Verifier    ProjectVerifier
}

func NewPlatformProbeSet(roots RunnerRootResolver) ProbeSet {
	probe := newPlatformProbe(roots)
	return ProbeSet{
		Application: probe,
		WebRemote:   probe,
		Runner:      probe,
		Transport:   probe,
		Verifier:    probe,
	}
}
