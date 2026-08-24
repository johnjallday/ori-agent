package workspacesurface

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeProcessBehavior struct {
	startDelay time.Duration
	callDelay  time.Duration
	crash      bool
	startError error
}

type fakeProcessStats struct {
	mu        sync.Mutex
	created   int
	started   int
	stopped   int
	calls     int
	active    int
	maxActive int
	canceled  int
	behaviors []fakeProcessBehavior
}

type fakeServiceProcess struct {
	mu       sync.Mutex
	stats    *fakeProcessStats
	behavior fakeProcessBehavior
	healthy  bool
}

func (s *fakeProcessStats) factory(_ ServiceSpec) ServiceProcess {
	s.mu.Lock()
	index := s.created
	s.created++
	behavior := fakeProcessBehavior{}
	if index < len(s.behaviors) {
		behavior = s.behaviors[index]
	}
	s.mu.Unlock()
	return &fakeServiceProcess{stats: s, behavior: behavior}
}

func (p *fakeServiceProcess) Start(ctx context.Context) error {
	if p.behavior.startDelay > 0 {
		timer := time.NewTimer(p.behavior.startDelay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if p.behavior.startError != nil {
		return p.behavior.startError
	}
	p.stats.mu.Lock()
	p.stats.started++
	p.stats.mu.Unlock()
	p.setHealthy(true)
	return nil
}

func (p *fakeServiceProcess) Stop(context.Context) error {
	p.stats.mu.Lock()
	p.stats.stopped++
	p.stats.mu.Unlock()
	p.setHealthy(false)
	return nil
}

func (p *fakeServiceProcess) Call(ctx context.Context, operation string, _ map[string]any) (json.RawMessage, error) {
	p.stats.mu.Lock()
	p.stats.calls++
	p.stats.active++
	if p.stats.active > p.stats.maxActive {
		p.stats.maxActive = p.stats.active
	}
	p.stats.mu.Unlock()
	defer func() {
		p.stats.mu.Lock()
		p.stats.active--
		p.stats.mu.Unlock()
	}()
	if p.behavior.callDelay > 0 {
		timer := time.NewTimer(p.behavior.callDelay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			p.stats.mu.Lock()
			p.stats.canceled++
			p.stats.mu.Unlock()
			return nil, ctx.Err()
		}
	}
	if p.behavior.crash {
		p.setHealthy(false)
		return nil, errors.New("process exited at /private/plugin:2307")
	}
	return json.RawMessage(`{"operation":"` + operation + `"}`), nil
}

func (p *fakeServiceProcess) Healthy() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.healthy
}

func (p *fakeServiceProcess) setHealthy(healthy bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.healthy = healthy
}

func testServiceSpec() ServiceSpec {
	return ServiceSpec{
		PluginID: "demo-plugin", PluginGeneration: 1, ServiceID: "demo-service",
		Command: "/managed/demo-service", MaxConcurrency: 2,
		StartupTimeout: 100 * time.Millisecond, ShutdownTimeout: 100 * time.Millisecond,
	}
}

func TestServiceManagerLazilyStartsAndBoundsConcurrentCalls(t *testing.T) {
	stats := &fakeProcessStats{behaviors: []fakeProcessBehavior{{callDelay: 50 * time.Millisecond}}}
	manager := NewServiceManager(stats.factory)
	if stats.created != 0 {
		t.Fatal("constructor started an unneeded service")
	}

	const calls = 8
	var wg sync.WaitGroup
	errs := make(chan error, calls)
	for index := 0; index < calls; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := manager.Call(context.Background(), testServiceSpec(), ServiceCall{Operation: "status.read", Timeout: time.Second})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Call() error = %v", err)
		}
	}
	stats.mu.Lock()
	defer stats.mu.Unlock()
	if stats.created != 1 || stats.started != 1 || stats.calls != calls || stats.maxActive != 2 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestServiceManagerAppliesCallAndStartupTimeouts(t *testing.T) {
	callStats := &fakeProcessStats{behaviors: []fakeProcessBehavior{{callDelay: time.Second}}}
	callManager := NewServiceManager(callStats.factory)
	_, err := callManager.Call(context.Background(), testServiceSpec(), ServiceCall{
		Operation: "status.read", Timeout: 20 * time.Millisecond,
	})
	if !errors.Is(err, ErrServiceTimeout) {
		t.Fatalf("call timeout error = %v", err)
	}

	startStats := &fakeProcessStats{behaviors: []fakeProcessBehavior{{startDelay: time.Second}}}
	startManager := NewServiceManager(startStats.factory)
	spec := testServiceSpec()
	spec.StartupTimeout = 20 * time.Millisecond
	if err := startManager.Probe(context.Background(), spec); !errors.Is(err, ErrServiceTimeout) {
		t.Fatalf("startup timeout error = %v", err)
	}
	startStats.mu.Lock()
	defer startStats.mu.Unlock()
	if startStats.stopped != 1 {
		t.Fatalf("timed-out startup left process unstopped: %+v", startStats)
	}
}

func TestServiceManagerPropagatesCallerCancellation(t *testing.T) {
	stats := &fakeProcessStats{behaviors: []fakeProcessBehavior{{callDelay: time.Second}}}
	manager := NewServiceManager(stats.factory)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := manager.Call(ctx, testServiceSpec(), ServiceCall{Operation: "status.read", Timeout: time.Second})
		result <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled call error = %v", err)
	}
	stats.mu.Lock()
	defer stats.mu.Unlock()
	if stats.canceled != 1 {
		t.Fatalf("service observed %d cancellations", stats.canceled)
	}
}

func TestServiceManagerPerformsOnlyOneBoundedCrashRestart(t *testing.T) {
	t.Run("one crash then recovery", func(t *testing.T) {
		stats := &fakeProcessStats{behaviors: []fakeProcessBehavior{{crash: true}, {}}}
		manager := NewServiceManager(stats.factory)
		output, err := manager.Call(context.Background(), testServiceSpec(), ServiceCall{Operation: "status.read", Timeout: time.Second})
		if err != nil || !json.Valid(output) {
			t.Fatalf("recovered Call() = %s, %v", output, err)
		}
		stats.mu.Lock()
		defer stats.mu.Unlock()
		if stats.created != 2 || stats.calls != 2 || stats.stopped != 1 {
			t.Fatalf("restart stats = %+v", stats)
		}
	})

	t.Run("second crash stays unavailable", func(t *testing.T) {
		stats := &fakeProcessStats{behaviors: []fakeProcessBehavior{{crash: true}, {crash: true}, {}}}
		manager := NewServiceManager(stats.factory)
		_, err := manager.Call(context.Background(), testServiceSpec(), ServiceCall{Operation: "status.read", Timeout: time.Second})
		if !errors.Is(err, ErrServiceUnavailable) {
			t.Fatalf("double crash error = %v", err)
		}
		stats.mu.Lock()
		defer stats.mu.Unlock()
		if stats.created != 2 || stats.calls != 2 {
			t.Fatalf("manager entered a restart loop: %+v", stats)
		}
	})
}

func TestServiceManagerStopCancelsCallsBeforeProcessStop(t *testing.T) {
	stats := &fakeProcessStats{behaviors: []fakeProcessBehavior{{callDelay: time.Second}}}
	manager := NewServiceManager(stats.factory)
	callDone := make(chan error, 1)
	go func() {
		_, err := manager.Call(context.Background(), testServiceSpec(), ServiceCall{Operation: "status.read", Timeout: time.Second})
		callDone <- err
	}()
	time.Sleep(20 * time.Millisecond)
	if err := manager.StopPlugin("demo-plugin", 1); err != nil {
		t.Fatalf("StopPlugin() error = %v", err)
	}
	if err := <-callDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("stopped call error = %v", err)
	}
	stats.mu.Lock()
	defer stats.mu.Unlock()
	if stats.stopped != 1 || stats.canceled != 1 || stats.active != 0 {
		t.Fatalf("stop stats = %+v", stats)
	}
}
