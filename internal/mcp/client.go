package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/johnjallday/ori-agent/internal/mcp/transport"
)

// Client implements a JSON-RPC 2.0 client for MCP
type Client struct {
	transport    *transport.StdioTransport
	requestID    atomic.Int64
	pending      map[interface{}]chan *Response
	pendingMu    sync.RWMutex
	initialized  bool
	capabilities ServerCapabilities
}

// NewClient creates a new MCP client
func NewClient(t *transport.StdioTransport) *Client {
	c := &Client{
		transport: t,
		pending:   make(map[interface{}]chan *Response),
	}

	// Start receiving responses
	go c.receiveLoop()

	return c
}

// Initialize sends the initialize request to the MCP server
func (c *Client) Initialize(ctx context.Context, clientInfo Implementation) (*InitializeResult, error) {
	params := InitializeParams{
		ProtocolVersion: "2024-11-05",
		Capabilities: ClientCapabilities{
			Experimental: make(map[string]interface{}),
		},
		ClientInfo: clientInfo,
	}

	result, err := c.Call(ctx, MethodInitialize, params)
	if err != nil {
		return nil, fmt.Errorf("initialize failed: %w", err)
	}

	var initResult InitializeResult
	if err := json.Unmarshal(result, &initResult); err != nil {
		return nil, fmt.Errorf("failed to unmarshal initialize result: %w", err)
	}

	c.initialized = true
	c.capabilities = initResult.Capabilities

	// Send initialized notification
	if err := c.Notify(MethodInitialized, nil); err != nil {
		return nil, fmt.Errorf("failed to send initialized notification: %w", err)
	}

	return &initResult, nil
}

// Call sends a request and waits for a response
func (c *Client) Call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	reqID := c.requestID.Add(1)

	var paramsJSON json.RawMessage
	if params != nil {
		var err error
		paramsJSON, err = json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal params: %w", err)
		}
	}

	req := Request{
		JSONRPC: "2.0",
		ID:      reqID,
		Method:  method,
		Params:  paramsJSON,
	}

	respChan := make(chan *Response, 1)

	c.pendingMu.Lock()
	c.pending[reqID] = respChan
	c.pendingMu.Unlock()

	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, reqID)
		c.pendingMu.Unlock()
		close(respChan)
	}()

	if err := c.transport.Send(req); err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	select {
	case resp := <-respChan:
		if resp.Error != nil {
			return nil, fmt.Errorf("RPC error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Notify sends a notification (no response expected)
func (c *Client) Notify(method string, params interface{}) error {
	var paramsJSON json.RawMessage
	if params != nil {
		var err error
		paramsJSON, err = json.Marshal(params)
		if err != nil {
			return fmt.Errorf("failed to marshal params: %w", err)
		}
	}

	notif := Notification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  paramsJSON,
	}

	if err := c.transport.Send(notif); err != nil {
		return fmt.Errorf("failed to send notification: %w", err)
	}

	return nil
}

// receiveLoop continuously receives messages from the transport
func (c *Client) receiveLoop() {
	for {
		msg, err := c.transport.Receive()
		if err != nil {
			// Transport closed or error
			c.closeAllPending()
			return
		}

		// Try to parse as response
		var resp Response
		if err := json.Unmarshal(msg, &resp); err != nil {
			// Invalid message, skip
			fmt.Fprintf(os.Stderr, "[MCP] Failed to unmarshal response: %v\n", err)
			continue
		}

		// Check if this is a response to a pending request
		if resp.ID != nil {
			// Convert float64 to int64 if needed (JSON unmarshals numbers as float64)
			var normalizedID interface{} = resp.ID
			if f, ok := resp.ID.(float64); ok {
				normalizedID = int64(f)
			}

			c.pendingMu.RLock()
			ch, ok := c.pending[normalizedID]
			c.pendingMu.RUnlock()

			if ok {
				select {
				case ch <- &resp:
				default:
					// Channel full, skip
					fmt.Fprintf(os.Stderr, "[MCP] Pending channel full for ID %v\n", normalizedID)
				}
			} else {
				fmt.Fprintf(os.Stderr, "[MCP] No pending request for ID %v (type: %T, all pending: %v)\n", normalizedID, normalizedID, c.getPendingIDs())
			}
		}
	}
}

// getPendingIDs returns all pending request IDs (for debugging)
func (c *Client) getPendingIDs() []interface{} {
	c.pendingMu.RLock()
	defer c.pendingMu.RUnlock()

	ids := make([]interface{}, 0, len(c.pending))
	for id := range c.pending {
		ids = append(ids, id)
	}
	return ids
}

// closeAllPending closes all pending request channels
func (c *Client) closeAllPending() {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()

	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
}

// Close closes the client and its transport
func (c *Client) Close() error {
	return c.transport.Close()
}

// IsAlive checks if the client connection is still active
func (c *Client) IsAlive() bool {
	return c.transport.IsAlive()
}

// ListTools lists all available tools from the MCP server
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	if !c.initialized {
		return nil, fmt.Errorf("client not initialized")
	}

	result, err := c.Call(ctx, MethodToolsList, ToolsListParams{})
	if err != nil {
		return nil, err
	}

	var toolsResult ToolsListResult
	if err := json.Unmarshal(result, &toolsResult); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tools list: %w", err)
	}

	return toolsResult.Tools, nil
}

// CallTool calls a tool on the MCP server
func (c *Client) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*ToolCallResult, error) {
	if !c.initialized {
		return nil, fmt.Errorf("client not initialized")
	}

	params := ToolCallParams{
		Name:      name,
		Arguments: arguments,
	}

	result, err := c.Call(ctx, MethodToolsCall, params)
	if err != nil {
		return nil, err
	}

	var toolResult ToolCallResult
	if err := json.Unmarshal(result, &toolResult); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tool result: %w", err)
	}

	return &toolResult, nil
}

// Ping sends a ping request to check server health
func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := c.Call(ctx, MethodPing, nil)
	return err
}
