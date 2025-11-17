package menubar

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

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
	cancelFunc  context.CancelFunc
	errorMsg    string
	statusChan  chan ServerStatus
	subscribers []func(ServerStatus)
	subMu       sync.RWMutex
}

// NewController creates a new server controller
func NewController(port int) *Controller {
	return &Controller{
		status:     StatusStopped,
		port:       port,
		statusChan: make(chan ServerStatus, 10),
	}
}

// StartServer starts the ori-agent HTTP server
func (c *Controller) StartServer(ctx context.Context) error {
	c.statusMu.Lock()

	// Check if already running or starting
	if c.status == StatusRunning || c.status == StatusStarting {
		c.statusMu.Unlock()
		return fmt.Errorf("server is already %s", c.status.String())
	}

	// Check if port is available
	if !c.isPortAvailable() {
		c.status = StatusError
		c.errorMsg = fmt.Sprintf("Port %d is already in use", c.port)
		c.statusMu.Unlock()
		c.notifyStatusChange(StatusError)
		return fmt.Errorf("port %d is already in use", c.port)
	}

	c.status = StatusStarting
	c.errorMsg = ""
	c.statusMu.Unlock()
	c.notifyStatusChange(StatusStarting)

	// Create context for server lifecycle
	serverCtx, cancel := context.WithCancel(ctx)
	c.cancelFunc = cancel

	// Start server in goroutine
	go c.runServer(serverCtx)

	// Wait for server to be running (with timeout)
	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, 10*time.Second)
	defer timeoutCancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutCtx.Done():
			c.statusMu.Lock()
			c.status = StatusError
			c.errorMsg = "Server failed to start within timeout"
			c.statusMu.Unlock()
			c.notifyStatusChange(StatusError)
			return fmt.Errorf("server failed to start within timeout")
		case <-ticker.C:
			c.statusMu.RLock()
			status := c.status
			c.statusMu.RUnlock()

			if status == StatusRunning {
				return nil
			} else if status == StatusError {
				c.statusMu.RLock()
				errMsg := c.errorMsg
				c.statusMu.RUnlock()
				return fmt.Errorf("server failed to start: %s", errMsg)
			}
		}
	}
}

// StopServer gracefully stops the ori-agent HTTP server
func (c *Controller) StopServer(ctx context.Context) error {
	c.statusMu.Lock()

	if c.status == StatusStopped || c.status == StatusStopping {
		c.statusMu.Unlock()
		return fmt.Errorf("server is already %s", c.status.String())
	}

	c.status = StatusStopping
	c.statusMu.Unlock()
	c.notifyStatusChange(StatusStopping)

	// Cancel the server context
	if c.cancelFunc != nil {
		c.cancelFunc()
	}

	// Wait for shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Shutdown the HTTP server gracefully
	if c.httpServer != nil {
		if err := c.httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("Error during HTTP server shutdown: %v", err)
		}
	}

	// Shutdown the server's background services
	if c.server != nil {
		c.server.Shutdown()
	}

	c.statusMu.Lock()
	c.status = StatusStopped
	c.httpServer = nil
	c.server = nil
	c.cancelFunc = nil
	c.statusMu.Unlock()
	c.notifyStatusChange(StatusStopped)

	log.Println("Server stopped successfully")
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
	return c.port
}

// SetPort updates the port the server should run on
// Note: Server must be stopped before changing the port
func (c *Controller) SetPort(port int) error {
	c.statusMu.Lock()
	defer c.statusMu.Unlock()

	// Don't allow port changes while server is running
	if c.status != StatusStopped {
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
	listener.Close()
	return true
}

// runServer runs the HTTP server in a goroutine
func (c *Controller) runServer(ctx context.Context) {
	log.Printf("Starting ori-agent server on port %d...", c.port)

	// Create server instance
	srv, err := server.New()
	if err != nil {
		c.statusMu.Lock()
		c.status = StatusError
		c.errorMsg = fmt.Sprintf("Failed to create server: %v", err)
		c.statusMu.Unlock()
		c.notifyStatusChange(StatusError)
		log.Printf("Failed to create server: %v", err)
		return
	}

	c.server = srv

	// Create HTTP server with wrapper for graceful shutdown
	addr := fmt.Sprintf(":%d", c.port)
	httpServer := srv.HTTPServer(addr)

	c.statusMu.Lock()
	c.httpServer = &server.HTTPServerWrapper{Server: httpServer}
	c.statusMu.Unlock()

	// Update status to running
	c.statusMu.Lock()
	c.status = StatusRunning
	c.statusMu.Unlock()
	c.notifyStatusChange(StatusRunning)

	log.Printf("✅ Server running on http://localhost:%d", c.port)

	// Start HTTP server (blocks until shutdown)
	if err := httpServer.ListenAndServe(); err != nil && err.Error() != "http: Server closed" {
		c.statusMu.Lock()
		c.status = StatusError
		c.errorMsg = fmt.Sprintf("Server error: %v", err)
		c.statusMu.Unlock()
		c.notifyStatusChange(StatusError)
		log.Printf("Server error: %v", err)
		return
	}

	// Server stopped normally
	log.Println("Server shut down cleanly")
}

// notifyStatusChange notifies all subscribers of a status change
func (c *Controller) notifyStatusChange(status ServerStatus) {
	c.subMu.RLock()
	subscribers := make([]func(ServerStatus), len(c.subscribers))
	copy(subscribers, c.subscribers)
	c.subMu.RUnlock()

	// Notify all subscribers
	for _, callback := range subscribers {
		go callback(status)
	}

	// Also send to status channel (non-blocking)
	select {
	case c.statusChan <- status:
	default:
	}
}
