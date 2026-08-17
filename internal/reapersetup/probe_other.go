//go:build !darwin

package reapersetup

import "context"

type unsupportedPlatformProbe struct{}

func newPlatformProbe(RunnerRootResolver) platformProber { return unsupportedPlatformProbe{} }

func (unsupportedPlatformProbe) DetectApplication(context.Context) ApplicationObservation {
	return ApplicationObservation{State: ProbeUnsupported}
}

func (unsupportedPlatformProbe) DetectWebRemote(context.Context) WebRemoteObservation {
	return WebRemoteObservation{State: ProbeUnsupported}
}

func (unsupportedPlatformProbe) DetectRunner(context.Context) RunnerObservation {
	return RunnerObservation{State: ProbeUnsupported}
}

func (unsupportedPlatformProbe) CheckTransport(context.Context, WebRemoteObservation) LiveTransportObservation {
	return LiveTransportObservation{State: TransportUnavailable}
}

func (unsupportedPlatformProbe) VerifyProject(context.Context, VerificationTarget) VerificationObservation {
	return VerificationObservation{State: VerificationCheckFailed}
}
