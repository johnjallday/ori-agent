package reaper

import (
	"context"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/reapersetup"
)

type clientProbe struct {
	web       reapersetup.WebRemoteObservation
	transport reapersetup.LiveTransportObservation
}

func (p clientProbe) DetectWebRemote(context.Context) reapersetup.WebRemoteObservation {
	return p.web
}

func (p clientProbe) CheckTransport(context.Context, reapersetup.WebRemoteObservation) reapersetup.LiveTransportObservation {
	return p.transport
}

func TestClientConnectedTreatsUnreachableAsState(t *testing.T) {
	probe := clientProbe{
		web:       reapersetup.WebRemoteObservation{State: reapersetup.ProbeReady, Port: 2307},
		transport: reapersetup.LiveTransportObservation{State: reapersetup.TransportOffline},
	}
	client := NewClient(reapersetup.ProbeSet{WebRemote: probe, Transport: probe})
	checkedAt := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	client.now = func() time.Time { return checkedAt }

	got := client.Connected(context.Background())
	if got.Connected || got.Reason != "reaper_unreachable" || !got.CheckedAt.Equal(checkedAt) {
		t.Fatalf("connected state = %+v", got)
	}
}

func TestClientConnectedUsesAvailableTransport(t *testing.T) {
	probe := clientProbe{
		web:       reapersetup.WebRemoteObservation{State: reapersetup.ProbeReady, Port: 2307},
		transport: reapersetup.LiveTransportObservation{State: reapersetup.TransportAvailable, Port: 2308},
	}
	client := NewClient(reapersetup.ProbeSet{WebRemote: probe, Transport: probe})
	got := client.Connected(context.Background())
	if !got.Connected || got.Reason != "" {
		t.Fatalf("connected state = %+v", got)
	}
}
