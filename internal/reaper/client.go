// Package reaper provides the trusted server-side client for REAPER's loopback
// Web Remote. It reuses reapersetup's platform probes for port discovery; no
// browser request, workspace metadata, or caller can provide an endpoint.
package reaper

import (
	"context"
	"time"

	"github.com/johnjallday/ori-agent/internal/reapersetup"
)

// State is the current connection observation. No endpoint or port is present
// in this public shape.
type State struct {
	Applies   bool      `json:"applies"`
	Connected bool      `json:"connected"`
	Reason    string    `json:"reason,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

// Client resolves the current loopback listener through the existing setup
// probes. CheckTransport performs the bounded /_/TRANSPORT request.
type Client struct {
	web       reapersetup.WebRemoteProbe
	transport reapersetup.LiveTransportProbe
	now       func() time.Time
}

func NewClient(probes reapersetup.ProbeSet) *Client {
	return &Client{web: probes.WebRemote, transport: probes.Transport, now: time.Now}
}

// Connected treats an unreachable REAPER as normal live state, not a request
// failure.
func (c *Client) Connected(ctx context.Context) State {
	now := time.Now
	if c != nil && c.now != nil {
		now = c.now
	}
	state := State{CheckedAt: now().UTC()}
	if c == nil || c.web == nil || c.transport == nil {
		state.Reason = "unavailable"
		return state
	}
	web := c.web.DetectWebRemote(ctx)
	switch web.State {
	case reapersetup.ProbeReady:
		// Continue. The platform probe owns the stale-ini fallback.
	case reapersetup.ProbeMissing:
		state.Reason = "web_remote_off"
		return state
	case reapersetup.ProbeUnsupported:
		state.Reason = "unsupported"
		return state
	default:
		state.Reason = "web_remote_unavailable"
		return state
	}
	live := c.transport.CheckTransport(ctx, web)
	if live.State != reapersetup.TransportAvailable {
		state.Reason = "reaper_unreachable"
		return state
	}
	state.Connected = true
	return state
}
