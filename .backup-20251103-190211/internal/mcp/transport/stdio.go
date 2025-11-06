package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

// StdioTransport manages communication with an MCP server via stdio
type StdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	scanner *bufio.Scanner
	mu      sync.Mutex
	closed  bool
}

// NewStdioTransport creates a new stdio transport for the given command
func NewStdioTransport(ctx context.Context, command string, args []string, env []string) (*StdioTransport, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		stderr.Close()
		return nil, fmt.Errorf("failed to start command: %w", err)
	}

	t := &StdioTransport{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		stderr:  stderr,
		scanner: bufio.NewScanner(stdout),
	}

	// Start stderr reader in background
	go t.readStderr()

	return t, nil
}

// Send sends a message to the MCP server
func (t *StdioTransport) Send(message interface{}) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return fmt.Errorf("transport is closed")
	}

	data, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	// MCP uses newline-delimited JSON
	_, err = t.stdin.Write(append(data, '\n'))
	if err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}

	return nil
}

// Receive reads the next message from the MCP server
func (t *StdioTransport) Receive() (json.RawMessage, error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, fmt.Errorf("transport is closed")
	}
	t.mu.Unlock()

	if !t.scanner.Scan() {
		if err := t.scanner.Err(); err != nil {
			return nil, fmt.Errorf("scanner error: %w", err)
		}
		return nil, io.EOF
	}

	line := t.scanner.Bytes()
	if len(line) == 0 {
		return nil, fmt.Errorf("empty line received")
	}

	// Return raw JSON message
	return json.RawMessage(line), nil
}

// Close closes the transport and terminates the server process
func (t *StdioTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}
	t.closed = true

	// Close stdin to signal server to shut down
	if t.stdin != nil {
		t.stdin.Close()
	}

	// Wait for process to exit (with timeout handled by context)
	if t.cmd != nil && t.cmd.Process != nil {
		t.cmd.Wait()
	}

	// Close remaining pipes
	if t.stdout != nil {
		t.stdout.Close()
	}
	if t.stderr != nil {
		t.stderr.Close()
	}

	return nil
}

// readStderr continuously reads stderr output from the server
func (t *StdioTransport) readStderr() {
	scanner := bufio.NewScanner(t.stderr)
	for scanner.Scan() {
		// TODO: Forward stderr to logging system
		line := scanner.Text()
		if len(line) > 0 {
			// For now, we'll just ignore stderr
			// In future, integrate with ori-agent logging
			_ = line
		}
	}
}

// IsAlive checks if the transport is still active
func (t *StdioTransport) IsAlive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return false
	}

	if t.cmd == nil || t.cmd.Process == nil {
		return false
	}

	// Check if process is still running
	// Process.Signal(0) is a way to check without sending a real signal
	return t.cmd.ProcessState == nil || !t.cmd.ProcessState.Exited()
}
