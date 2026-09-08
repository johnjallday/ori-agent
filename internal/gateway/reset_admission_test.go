package gateway

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/resetstate"
)

type blockingResetChannel struct {
	idCalls atomic.Int32
	started chan context.Context
	finish  chan struct{}
	done    chan struct{}
	once    sync.Once
}

func (c *blockingResetChannel) ID() string   { c.idCalls.Add(1); return "owned" }
func (c *blockingResetChannel) Type() string { return "fixture" }
func (c *blockingResetChannel) Start(ctx context.Context, _ Handler) error {
	c.started <- ctx
	<-c.finish
	close(c.done)
	return nil
}
func (c *blockingResetChannel) Stop(context.Context) error {
	c.once.Do(func() { close(c.finish) })
	return nil
}
func (c *blockingResetChannel) Send(context.Context, Message) error { return nil }

func TestResetAdmissionGatewayChannelOwnershipSpansLifetime(t *testing.T) {
	service := NewService(logger.New("reset-gateway-test"))
	gate := &resetstate.WorkGate{}
	service.SetAdmissionGate(gate)
	channel := &blockingResetChannel{started: make(chan context.Context, 1), finish: make(chan struct{}), done: make(chan struct{})}
	if err := service.RegisterChannel(context.Background(), channel); err != nil {
		t.Fatal(err)
	}
	var channelCtx context.Context
	select {
	case channelCtx = <-channel.started:
	case <-time.After(5 * time.Second):
		t.Fatal("channel did not start")
	}
	if snapshot := gate.Snapshot(); snapshot.Active != 0 || snapshot.Owners != 1 {
		t.Fatalf("channel lifetime not registered: %+v", snapshot)
	}
	if err := service.Send(t.Context(), Message{ID: uuid.New(), Sender: Sender{Platform: "owned"}}); err != nil {
		t.Fatalf("channel send failed before fence: %v", err)
	}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatalf("idle lifetime owner prevented fence: %v", err)
	}
	select {
	case <-channelCtx.Done():
		t.Fatalf("fence cancelled channel before drain: %v", channelCtx.Err())
	default:
	}
	if err := service.Send(t.Context(), Message{ID: uuid.New(), Sender: Sender{Platform: "owned"}}); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatalf("fenced channel send = %v", err)
	}
	if err := service.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-channel.done:
	case <-time.After(5 * time.Second):
		t.Fatal("channel did not finish after shutdown")
	}
	if snapshot := gate.Snapshot(); snapshot.Owners != 0 || !snapshot.Fenced {
		t.Fatalf("channel owner not drained: %+v", snapshot)
	}
}

type retryResetChannel struct {
	*blockingResetChannel
	mu      sync.Mutex
	stopErr error
}

func (c *retryResetChannel) Stop(ctx context.Context) error {
	c.mu.Lock()
	err := c.stopErr
	c.mu.Unlock()
	if err != nil {
		return err
	}
	return c.blockingResetChannel.Stop(ctx)
}

func TestResetAdmissionGatewayAmbiguousStopRetainsRetryableOwner(t *testing.T) {
	service := NewService(logger.New("reset-gateway-test"))
	gate := &resetstate.WorkGate{}
	service.SetAdmissionGate(gate)
	channel := &retryResetChannel{
		blockingResetChannel: &blockingResetChannel{started: make(chan context.Context, 1), finish: make(chan struct{}), done: make(chan struct{})},
		stopErr:              errors.New("synthetic stop uncertainty"),
	}
	if err := service.RegisterChannel(context.Background(), channel); err != nil {
		t.Fatal(err)
	}
	select {
	case <-channel.started:
	case <-time.After(5 * time.Second):
		t.Fatal("channel did not start")
	}
	if err := service.Shutdown(t.Context()); err == nil {
		t.Fatal("ambiguous channel stop reported success")
	}
	if snapshot := gate.Snapshot(); snapshot.Owners != 1 {
		t.Fatalf("ambiguous channel owner released: %+v", snapshot)
	}
	channel.mu.Lock()
	channel.stopErr = nil
	channel.mu.Unlock()
	if err := service.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-channel.done:
	case <-time.After(5 * time.Second):
		t.Fatal("retried channel stop did not finish")
	}
	if snapshot := gate.Snapshot(); snapshot.Owners != 0 {
		t.Fatalf("retried channel owner remained: %+v", snapshot)
	}
}

func TestResetAdmissionGatewayRefusesBeforeChannelAccess(t *testing.T) {
	service := NewService(logger.New("reset-gateway-test"))
	gate := &resetstate.WorkGate{}
	service.SetAdmissionGate(gate)
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	channel := &blockingResetChannel{started: make(chan context.Context, 1), finish: make(chan struct{}), done: make(chan struct{})}
	if err := service.RegisterChannel(t.Context(), channel); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatalf("fenced register = %v", err)
	}
	if channel.idCalls.Load() != 0 {
		t.Fatal("fenced registration touched channel")
	}
	if err := service.Send(t.Context(), Message{ID: uuid.New(), Sender: Sender{Platform: "owned"}}); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatalf("fenced send = %v", err)
	}
	if err := service.handleMessage(t.Context(), Message{}); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatalf("fenced inbound = %v", err)
	}
}
