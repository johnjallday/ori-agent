package workspacesurface

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/mcp"
)

var (
	ErrServiceUnavailable = errors.New("workspace surface service is unavailable")
	ErrServiceTimeout     = errors.New("workspace surface service timed out")
	ErrServiceStopping    = errors.New("workspace surface service is stopping")
)

type ServiceSpec struct {
	PluginID         string
	PluginGeneration uint64
	ServiceID        string
	Command          string
	Args             []string
	Env              map[string]string
	MaxConcurrency   int
	StartupTimeout   time.Duration
	ShutdownTimeout  time.Duration
}

func (s ServiceSpec) key() string {
	return s.PluginID + "\x00" + s.ServiceID
}

func (s ServiceSpec) normalized() ServiceSpec {
	copy := s
	if copy.MaxConcurrency < 1 {
		copy.MaxConcurrency = 8
	}
	if copy.MaxConcurrency > 32 {
		copy.MaxConcurrency = 32
	}
	if copy.StartupTimeout <= 0 || copy.StartupTimeout > 10*time.Second {
		copy.StartupTimeout = 10 * time.Second
	}
	if copy.ShutdownTimeout <= 0 || copy.ShutdownTimeout > 5*time.Second {
		copy.ShutdownTimeout = 5 * time.Second
	}
	copy.Args = append([]string(nil), s.Args...)
	copy.Env = cloneStringMap(s.Env)
	return copy
}

func (s ServiceSpec) validate() error {
	if !idPattern.MatchString(s.PluginID) || !idPattern.MatchString(s.ServiceID) || s.PluginGeneration == 0 || strings.TrimSpace(s.Command) == "" {
		return ErrServiceUnavailable
	}
	return nil
}

type ServiceCall struct {
	Operation string
	Arguments map[string]any
	Timeout   time.Duration
}

type ServiceProcess interface {
	Start(context.Context) error
	Stop(context.Context) error
	Call(context.Context, string, map[string]any) (json.RawMessage, error)
	Healthy() bool
}

type ServiceProcessFactory func(ServiceSpec) ServiceProcess

type ServiceManager struct {
	mu        sync.Mutex
	instances map[string]*serviceInstance
	factory   ServiceProcessFactory
}

type serviceInstance struct {
	spec ServiceSpec

	mu          sync.Mutex
	process     ServiceProcess
	started     bool
	starting    bool
	startDone   chan struct{}
	stopping    bool
	restartUsed bool
	rootCtx     context.Context
	cancel      context.CancelFunc
	semaphore   chan struct{}
	calls       sync.WaitGroup
}

func NewServiceManager(factory ServiceProcessFactory) *ServiceManager {
	if factory == nil {
		factory = newMCPServiceProcess
	}
	return &ServiceManager{instances: make(map[string]*serviceInstance), factory: factory}
}

// Call lazily starts the service, bounds concurrency and call duration, and
// performs at most one reconstruction/retry after an unhealthy transport
// failure. Domain/tool errors from a healthy process are not retried.
func (m *ServiceManager) Call(ctx context.Context, spec ServiceSpec, call ServiceCall) (json.RawMessage, error) {
	if m == nil || m.factory == nil || spec.validate() != nil || !idPattern.MatchString(call.Operation) {
		return nil, ErrServiceUnavailable
	}
	instance, err := m.instance(spec.normalized())
	if err != nil {
		return nil, err
	}
	select {
	case instance.semaphore <- struct{}{}:
		defer func() { <-instance.semaphore }()
	case <-ctx.Done():
		return nil, classifyServiceContext(ctx.Err())
	case <-instance.rootCtx.Done():
		return nil, ErrServiceStopping
	}

	instance.calls.Add(1)
	defer instance.calls.Done()
	return m.callInstance(ctx, instance, call)
}

// Probe lazily starts a service without exposing its MCP tools to an agent.
func (m *ServiceManager) Probe(ctx context.Context, spec ServiceSpec) error {
	if m == nil || spec.validate() != nil {
		return ErrServiceUnavailable
	}
	instance, err := m.instance(spec.normalized())
	if err != nil {
		return err
	}
	return m.ensureStarted(ctx, instance)
}

func (m *ServiceManager) instance(spec ServiceSpec) (*serviceInstance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := spec.key()
	if existing := m.instances[key]; existing != nil {
		if existing.spec.PluginGeneration != spec.PluginGeneration {
			return nil, ErrServiceStopping
		}
		return existing, nil
	}
	rootCtx, cancel := context.WithCancel(context.Background())
	instance := &serviceInstance{
		spec: spec, rootCtx: rootCtx, cancel: cancel,
		semaphore: make(chan struct{}, spec.MaxConcurrency),
	}
	m.instances[key] = instance
	return instance, nil
}

func (m *ServiceManager) callInstance(ctx context.Context, instance *serviceInstance, call ServiceCall) (json.RawMessage, error) {
	if err := m.ensureStarted(ctx, instance); err != nil {
		return nil, err
	}
	output, err := invokeProcess(ctx, instance, call)
	if err == nil {
		instance.mu.Lock()
		instance.restartUsed = false
		instance.mu.Unlock()
		return output, nil
	}
	if errors.Is(err, ErrServiceTimeout) || errors.Is(err, context.Canceled) || processHealthy(instance) {
		return nil, err
	}

	instance.mu.Lock()
	if instance.restartUsed || instance.stopping {
		instance.mu.Unlock()
		return nil, ErrServiceUnavailable
	}
	instance.restartUsed = true
	old := instance.process
	instance.process = nil
	instance.started = false
	instance.mu.Unlock()
	if old != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), instance.spec.ShutdownTimeout)
		_ = old.Stop(stopCtx)
		cancel()
	}
	if err := m.ensureStarted(ctx, instance); err != nil {
		return nil, err
	}
	output, err = invokeProcess(ctx, instance, call)
	if err != nil {
		return nil, err
	}
	instance.mu.Lock()
	instance.restartUsed = false
	instance.mu.Unlock()
	return output, nil
}

func (m *ServiceManager) ensureStarted(ctx context.Context, instance *serviceInstance) error {
	instance.mu.Lock()
	if instance.stopping {
		instance.mu.Unlock()
		return ErrServiceStopping
	}
	if instance.started && instance.process != nil && instance.process.Healthy() {
		instance.mu.Unlock()
		return nil
	}
	if instance.starting {
		done := instance.startDone
		instance.mu.Unlock()
		select {
		case <-done:
			return m.ensureStarted(ctx, instance)
		case <-ctx.Done():
			return classifyServiceContext(ctx.Err())
		case <-instance.rootCtx.Done():
			return ErrServiceStopping
		}
	}
	process := m.factory(instance.spec)
	instance.process = process
	instance.starting = true
	instance.startDone = make(chan struct{})
	done := instance.startDone
	startupTimeout := instance.spec.StartupTimeout
	instance.mu.Unlock()

	startCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	stopCancel := context.AfterFunc(instance.rootCtx, cancel)
	err := process.Start(startCtx)
	stopCancel()
	cancel()

	instance.mu.Lock()
	instance.starting = false
	close(done)
	failed := err != nil || !process.Healthy() || instance.stopping
	if failed {
		instance.process = nil
		instance.started = false
	} else {
		instance.started = true
	}
	instance.mu.Unlock()
	if !failed {
		return nil
	}
	stopCtx, stop := context.WithTimeout(context.Background(), instance.spec.ShutdownTimeout)
	_ = process.Stop(stopCtx)
	stop()
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(startCtx.Err(), context.DeadlineExceeded) {
		return ErrServiceTimeout
	}
	return ErrServiceUnavailable
}

func invokeProcess(ctx context.Context, instance *serviceInstance, call ServiceCall) (json.RawMessage, error) {
	timeout := call.Timeout
	if timeout <= 0 || timeout > 60*time.Second {
		timeout = 15 * time.Second
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	stopCancel := context.AfterFunc(instance.rootCtx, cancel)
	defer func() {
		stopCancel()
		cancel()
	}()

	instance.mu.Lock()
	process := instance.process
	stopping := instance.stopping
	instance.mu.Unlock()
	if stopping || process == nil {
		return nil, ErrServiceStopping
	}
	output, err := process.Call(callCtx, call.Operation, cloneAnyMap(call.Arguments))
	if err != nil {
		if errors.Is(callCtx.Err(), context.DeadlineExceeded) {
			return nil, ErrServiceTimeout
		}
		if errors.Is(callCtx.Err(), context.Canceled) {
			return nil, context.Canceled
		}
		return nil, ErrServiceUnavailable
	}
	return output, nil
}

func processHealthy(instance *serviceInstance) bool {
	instance.mu.Lock()
	defer instance.mu.Unlock()
	return instance.process != nil && instance.process.Healthy()
}

// StopPlugin invalidates/cancels calls first, waits within the stop budget,
// stops every matching process, and removes the instances before component
// files may change.
func (m *ServiceManager) StopPlugin(pluginID string, generation uint64) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	var targets []*serviceInstance
	for key, instance := range m.instances {
		if instance.spec.PluginID == pluginID && (generation == 0 || instance.spec.PluginGeneration == generation) {
			targets = append(targets, instance)
			delete(m.instances, key)
		}
	}
	m.mu.Unlock()

	var joined error
	for _, instance := range targets {
		if err := stopInstance(instance); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func (m *ServiceManager) Shutdown() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	instances := make([]*serviceInstance, 0, len(m.instances))
	for key, instance := range m.instances {
		instances = append(instances, instance)
		delete(m.instances, key)
	}
	m.mu.Unlock()
	var joined error
	for _, instance := range instances {
		if err := stopInstance(instance); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func stopInstance(instance *serviceInstance) error {
	instance.mu.Lock()
	instance.stopping = true
	instance.cancel()
	process := instance.process
	timeout := instance.spec.ShutdownTimeout
	instance.mu.Unlock()

	wait := make(chan struct{})
	go func() {
		instance.calls.Wait()
		close(wait)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-wait:
	case <-timer.C:
	}
	if process == nil {
		return nil
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := process.Stop(stopCtx); err != nil && !errors.Is(err, context.Canceled) {
		return ErrServiceUnavailable
	}
	return nil
}

func classifyServiceContext(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrServiceTimeout
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	return ErrServiceUnavailable
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	copy := make(map[string]string, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}

func cloneAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return map[string]any{}
	}
	copy := make(map[string]any, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}

type mcpServiceProcess struct {
	mu     sync.Mutex
	server *mcp.Server
	failed bool
}

func newMCPServiceProcess(spec ServiceSpec) ServiceProcess {
	return &mcpServiceProcess{server: mcp.NewServer(mcp.ServerConfig{
		Name:    spec.PluginID + ":surface:" + spec.ServiceID,
		Command: spec.Command, Args: append([]string(nil), spec.Args...), Env: cloneStringMap(spec.Env),
		Transport: mcp.TransportStdio, Enabled: true,
	})}
}

func (p *mcpServiceProcess) Start(ctx context.Context) error {
	result := make(chan error, 1)
	go func() { result <- p.server.Start() }()
	select {
	case err := <-result:
		if err == nil {
			p.setFailed(false)
		}
		return err
	case <-ctx.Done():
		_ = p.server.Stop()
		return ctx.Err()
	}
}

func (p *mcpServiceProcess) Stop(ctx context.Context) error {
	result := make(chan error, 1)
	go func() { result <- p.server.Stop() }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *mcpServiceProcess) Call(ctx context.Context, operation string, arguments map[string]any) (json.RawMessage, error) {
	result, err := p.server.CallTool(ctx, operation, arguments)
	if err != nil {
		p.setFailed(true)
		return nil, err
	}
	if result == nil || result.IsError {
		return nil, ErrServiceUnavailable
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil || string(data) == "null" {
		return nil, fmt.Errorf("%w: invalid structured output", ErrServiceUnavailable)
	}
	return data, nil
}

func (p *mcpServiceProcess) Healthy() bool {
	if p == nil || p.server == nil {
		return false
	}
	p.mu.Lock()
	failed := p.failed
	p.mu.Unlock()
	return !failed && p.server.GetStatus() == mcp.StatusRunning
}

func (p *mcpServiceProcess) setFailed(failed bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failed = failed
}
