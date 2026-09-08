package menubar

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/server"
)

// ServerStatus represents the current state of the server
type ServerStatus int

const (
	StatusStopped ServerStatus = iota
	StatusStarting
	StatusRunning
	StatusStopping
	StatusError
)

// String returns a human-readable status string
func (s ServerStatus) String() string {
	switch s {
	case StatusStopped:
		return "Stopped"
	case StatusStarting:
		return "Starting"
	case StatusRunning:
		return "Running"
	case StatusStopping:
		return "Stopping"
	case StatusError:
		return "Error"
	default:
		return "Unknown"
	}
}

// Controller manages the lifecycle of the ori-agent HTTP server
type Controller struct {
	status      ServerStatus
	statusMu    sync.RWMutex
	port        int
	server      *server.Server
	httpServer  *server.HTTPServerWrapper // Wrapper for graceful shutdown
	errorMsg    string
	statusChan  chan ServerStatus
	subscribers []func(ServerStatus)
	subMu       sync.RWMutex
	resetLease  *resetstate.Lease
	starting    bool // Construction outlives a StartServer caller timeout.
	stopping    bool
	generation  uint64 // Invalidates waiters/serve errors from an older lifecycle.

	// Runtime construction is separated from host lifecycle so isolated tests
	// can exercise real stop/start without native credentials or provider discovery.
	runtimeFactory func(addr string) (*server.Server, *http.Server, error)
}

// NewController creates a new server controller
func NewController(port int) *Controller {
	return NewControllerWithResetLease(port, nil)
}

// NewControllerWithResetLease keeps process ownership/admission outside the
// replaceable server. A nil lease is an alternate host with reset unavailable.
func NewControllerWithResetLease(port int, lease *resetstate.Lease) *Controller {
	return &Controller{
		status: StatusStopped, port: port, statusChan: make(chan ServerStatus, 10),
		resetLease: lease,
		runtimeFactory: func(addr string) (*server.Server, *http.Server, error) {
			return newServerRuntime(addr, lease)
		},
	}
}

func (c *Controller) enterRuntime() (func(), error) {
	if c.resetLease == nil {
		return func() {}, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return c.resetLease.EnterRuntime(cwd, os.Getenv("ORI_DATA_DIR"))
}

// StartServerWithPreflight holds host admission before a menu action can inspect
// or request takeover of a port. The callback is compiled host code, never HTTP
// input. StartServer rechecks lifecycle state before transferring construction.
func (c *Controller) StartServerWithPreflight(ctx context.Context, preflight func(int) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	release, err := c.enterRuntime()
	if err != nil {
		return err
	}
	defer release()
	if !c.CanStart() {
		return fmt.Errorf("server cannot start; finish its lifecycle or fully quit Ori for reset recovery")
	}
	if err := preflight(c.GetPort()); err != nil {
		return err
	}
	return c.StartServer(ctx)
}

// StartServer starts the ori-agent HTTP server
func (c *Controller) StartServer(ctx context.Context) error {
	c.statusMu.Lock()

	// A timed-out caller is not proof construction stopped. Nor may a failed
	// HTTP shutdown discard a still-owned runtime and start another over it.
	if !c.canStartLocked() {
		c.statusMu.Unlock()
		return fmt.Errorf("server still owns a running, starting or stopping runtime; stop it or fully quit before starting again")
	}
	if err := ctx.Err(); err != nil {
		c.statusMu.Unlock()
		return err
	}
	release, err := c.enterRuntime()
	if err != nil {
		c.status = StatusError
		c.errorMsg = err.Error()
		c.statusMu.Unlock()
		c.notifyStatusChange(StatusError)
		return err
	}

	// Ownership/fencing must be checked before even inspecting a port.
	if !c.isPortAvailable() {
		release()
		c.status = StatusError
		c.errorMsg = fmt.Sprintf("Port %d is already in use", c.port)
		err := fmt.Errorf("port %d is already in use", c.port)
		c.statusMu.Unlock()
		c.notifyStatusChange(StatusError)
		return err
	}

	c.status = StatusStarting
	c.starting = true
	c.generation++
	generation := c.generation
	c.errorMsg = ""
	c.statusMu.Unlock()
	c.notifyStatusChange(StatusStarting)

	// Start server in goroutine. Shutdown is driven by httpServer.Shutdown
	// and server.Shutdown in StopServer, not by context cancellation.
	go c.runServer(release, generation) // The constructor, not the waiting caller, owns release.

	return c.waitForRunning(ctx, generation)
}

func (c *Controller) waitForRunning(ctx context.Context, generation uint64) error {
	// Waiting belongs to this start generation, not whichever runtime happens
	// to be attached when the caller's deadline or next poll arrives.
	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, 10*time.Second)
	defer timeoutCancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutCtx.Done():
			c.statusMu.Lock()
			if c.generation != generation {
				c.statusMu.Unlock()
				return fmt.Errorf("server start was superseded by a later lifecycle")
			}
			if c.status == StatusRunning {
				c.statusMu.Unlock()
				return nil
			}
			c.status = StatusError
			c.errorMsg = "Server start wait ended; initialization may still be running. Wait or fully quit Ori."
			c.statusMu.Unlock()
			c.notifyStatusChange(StatusError)
			return timeoutCtx.Err()
		case <-ticker.C:
			c.statusMu.RLock()
			if c.generation != generation {
				c.statusMu.RUnlock()
				return fmt.Errorf("server start was superseded by a later lifecycle")
			}
			status, errMsg := c.status, c.errorMsg
			c.statusMu.RUnlock()

			switch status {
			case StatusRunning:
				return nil
			case StatusError:
				return fmt.Errorf("server failed to start: %s", errMsg)
			}
		}
	}
}

// StopServer gracefully stops the ori-agent HTTP server
func (c *Controller) StopServer(ctx context.Context) error {
	c.statusMu.Lock()

	if c.starting || c.stopping || c.status == StatusStopped {
		c.statusMu.Unlock()
		return fmt.Errorf("server is stopped or its lifecycle is still in progress; wait or fully quit Ori")
	}

	c.status = StatusStopping
	c.stopping = true
	c.generation++
	httpServer, srv := c.httpServer, c.server
	c.statusMu.Unlock()
	c.notifyStatusChange(StatusStopping)

	// Wait for shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Shutdown the HTTP server gracefully
	if httpServer != nil {
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			c.statusMu.Lock()
			c.status = StatusError
			c.stopping = false
			c.errorMsg = "HTTP shutdown is incomplete; retry Stop or fully quit Ori before restarting."
			c.statusMu.Unlock()
			c.notifyStatusChange(StatusError)
			return fmt.Errorf("HTTP shutdown incomplete: %w", err)
		}
	}

	// Shutdown the server's background services only after requests have joined.
	if srv != nil {
		srv.Shutdown()
	}

	c.statusMu.Lock()
	c.status = StatusStopped
	c.stopping = false
	c.httpServer = nil
	c.server = nil
	c.statusMu.Unlock()
	c.notifyStatusChange(StatusStopped)

	logger.Info("Server stopped successfully", nil)
	return nil
}

// GetStatus returns the current server status
func (c *Controller) GetStatus() ServerStatus {
	c.statusMu.RLock()
	defer c.statusMu.RUnlock()
	return c.status
}

// GetErrorMessage returns the last error message
func (c *Controller) GetErrorMessage() string {
	c.statusMu.RLock()
	defer c.statusMu.RUnlock()
	return c.errorMsg
}

// GetPort returns the port the server is configured to run on
func (c *Controller) GetPort() int {
	c.statusMu.RLock()
	defer c.statusMu.RUnlock()
	return c.port
}

func (c *Controller) canStartLocked() bool {
	return !c.starting && !c.stopping && c.server == nil && c.httpServer == nil &&
		c.status != StatusRunning && c.status != StatusStarting && c.status != StatusStopping
}

// CanStart/CanStop are UI hints; the mutating methods recheck under their lock.
func (c *Controller) CanStart() bool {
	c.statusMu.RLock()
	defer c.statusMu.RUnlock()
	return c.canStartLocked() && !c.resetLease.WorkGate().Snapshot().Fenced
}

func (c *Controller) CanStop() bool {
	c.statusMu.RLock()
	defer c.statusMu.RUnlock()
	return !c.starting && !c.stopping && (c.server != nil || c.httpServer != nil)
}

// SetPort updates the port the server should run on
// Note: Server must be stopped before changing the port
func (c *Controller) SetPort(port int) error {
	release, err := c.resetLease.WorkGate().Enter()
	if err != nil {
		return err
	}
	defer release()
	c.statusMu.Lock()
	defer c.statusMu.Unlock()

	// Don't allow port changes while server is running
	if c.status != StatusStopped || !c.canStartLocked() {
		return fmt.Errorf("cannot change port while server is %s", c.status.String())
	}

	c.port = port
	return nil
}

// WatchStatus registers a callback to be notified of status changes
func (c *Controller) WatchStatus(callback func(ServerStatus)) {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	c.subscribers = append(c.subscribers, callback)
}

// isPortAvailable checks if the configured port is available
func (c *Controller) isPortAvailable() bool {
	addr := fmt.Sprintf(":%d", c.port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

func newServerRuntime(addr string, lease *resetstate.Lease) (*server.Server, *http.Server, error) {
	srv, err := server.NewWithResetLease(lease)
	if err != nil {
		return nil, nil, err
	}
	return srv, srv.HTTPServer(addr), nil
}

// runServer runs the HTTP server in a goroutine
func (c *Controller) runServer(release func(), generation uint64) {
	defer release()
	port := c.GetPort()
	logger.Debug("Starting ori-agent server", logger.Fields{"port": port})

	// Create server instance and HTTP lifecycle together. The production
	// factory retains the normal builder and BaseContext startup behavior.
	addr := fmt.Sprintf(":%d", port)
	srv, httpServer, err := c.runtimeFactory(addr)
	if err != nil {
		if c.resetLease != nil {
			c.resetLease.MarkUncertain()
		}
		c.statusMu.Lock()
		c.status = StatusError
		c.starting = false
		c.errorMsg = fmt.Sprintf("Failed to create server: %v. Fully quit Ori before retrying.", err)
		c.statusMu.Unlock()
		c.notifyStatusChange(StatusError)
		logger.Error("Failed to create server", logger.Fields{"error": err})
		return
	}

	c.statusMu.Lock()
	c.server = srv
	c.httpServer = &server.HTTPServerWrapper{Server: httpServer}
	c.starting = false
	c.status = StatusRunning
	c.statusMu.Unlock()
	release() // Idempotent; no permit is held for the listening server's lifetime.
	c.notifyStatusChange(StatusRunning)

	logger.Info("Server running", logger.Fields{"port": port})

	// Start HTTP server (blocks until shutdown)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		c.statusMu.Lock()
		if c.generation != generation {
			c.statusMu.Unlock()
			return
		}
		c.status = StatusError
		c.errorMsg = fmt.Sprintf("Server error: %v", err)
		c.statusMu.Unlock()
		c.notifyStatusChange(StatusError)
		logger.Error("Server error", logger.Fields{"error": err})
		return
	}

	// Server stopped normally
	logger.Info("Server shut down cleanly", nil)
}

// notifyStatusChange notifies all subscribers of a status change
func (c *Controller) notifyStatusChange(status ServerStatus) {
	c.subMu.RLock()
	subscribers := make([]func(ServerStatus), len(c.subscribers))
	copy(subscribers, c.subscribers)
	c.subMu.RUnlock()

	// Register callbacks before launch. Subscribers are outside the controller
	// and may touch shell/runtime state, so a detached notification must remain
	// visible to host reset admission through callback completion.
	for _, callback := range subscribers {
		finishCallback, err := c.resetLease.WorkGate().Enter()
		if err != nil {
			continue
		}
		go func(fn func(ServerStatus)) {
			defer finishCallback()
			fn(status)
		}(callback)
	}

	// Also send to status channel (non-blocking)
	select {
	case c.statusChan <- status:
	default:
	}
}
