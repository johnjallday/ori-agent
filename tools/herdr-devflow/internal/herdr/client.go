// Package herdr adapts the supported Herdr CLI and newline-delimited local
// socket API. It never scrapes terminal tables, titles, or pane output.
package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/config"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
)

const DefaultBinary = "herdr"

type Command struct {
	Path string
	Args []string
	Dir  string
	Env  []string
}

type CommandResult struct {
	Stdout []byte
	Stderr []byte
}

type Runner interface {
	Run(context.Context, Command) (CommandResult, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, command.Path, command.Args...)
	cmd.Dir = command.Dir
	if command.Env != nil {
		cmd.Env = command.Env
	}
	stdout, stderr := new(strings.Builder), new(strings.Builder)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	return CommandResult{Stdout: []byte(stdout.String()), Stderr: []byte(stderr.String())}, err
}

// Client is safe to construct in tests with a fake Runner. Socket operations
// are only attempted when SocketPath is explicitly supplied by Herdr.
type Client struct {
	Binary     string
	SocketPath string
	Runner     Runner
	sequence   atomic.Uint64
}

func New(binary, socketPath string, runner Runner) *Client {
	if binary == "" {
		binary = DefaultBinary
	}
	if runner == nil {
		runner = ExecRunner{}
	}
	return &Client{Binary: binary, SocketPath: socketPath, Runner: runner}
}

// Version reads only the documented `herdr X.Y.Z` version output, not a human
// status table.
func (c *Client) Version(ctx context.Context) (config.Version, error) {
	result, err := c.Runner.Run(ctx, Command{Path: c.Binary, Args: []string{"--version"}})
	if err != nil {
		return config.Version{}, c.commandError("version", result, err)
	}
	raw := strings.TrimSpace(string(result.Stdout))
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "herdr"))
	version, parseErr := config.ParseVersion(raw)
	if parseErr != nil {
		return config.Version{}, &model.StageError{
			Stage:    "version",
			Code:     model.ErrHerdrIncompatible,
			Message:  "Herdr returned an unsupported version format",
			Recovery: "herdr --version",
			Cause:    parseErr,
		}
	}
	return version, nil
}

// CLIJSON executes a supported CLI wrapper whose response is JSON. It accepts
// both response envelopes and raw JSON schema documents.
func (c *Client) CLIJSON(ctx context.Context, args ...string) (json.RawMessage, error) {
	result, runErr := c.Runner.Run(ctx, Command{Path: c.Binary, Args: args})
	if runErr != nil {
		// Herdr emits structured API errors on stderr/stdout for command
		// failures. Prefer that stable code when it is available.
		if response, responseErr := decodeJSONResponse(result.Stdout); responseErr == nil {
			return response, nil
		} else {
			var stageErr *model.StageError
			if errors.As(responseErr, &stageErr) && stageErr.Stage == "Herdr API" {
				return nil, responseErr
			}
		}
		return nil, c.commandError(strings.Join(args, " "), result, runErr)
	}
	response, responseErr := decodeJSONResponse(result.Stdout)
	if responseErr != nil {
		return nil, responseErr
	}
	return response, nil
}

type Schema struct {
	Protocol      int
	SchemaVersion int
	Methods       map[string]struct{}
	Raw           json.RawMessage
}

func (s Schema) Supports(method string) bool {
	_, ok := s.Methods[method]
	return ok
}

func (c *Client) Schema(ctx context.Context) (Schema, error) {
	raw, err := c.CLIJSON(ctx, "api", "schema", "--json")
	if err != nil {
		return Schema{}, err
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return Schema{}, &model.StageError{
			Stage:    "schema",
			Code:     model.ErrSchemaUnsupported,
			Message:  "Herdr returned an invalid JSON API schema",
			Recovery: "herdr api schema --json",
			Cause:    err,
		}
	}
	schema := Schema{Methods: make(map[string]struct{}), Raw: raw}
	if protocol, ok := document["protocol"].(float64); ok {
		schema.Protocol = int(protocol)
	}
	if version, ok := document["schema_version"].(float64); ok {
		schema.SchemaVersion = int(version)
	}
	collectMethods(document, schema.Methods)
	return schema, nil
}

func collectMethods(value any, methods map[string]struct{}) {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if key == "const" {
				if method, ok := nested.(string); ok && (method == "ping" || strings.Contains(method, ".")) {
					methods[method] = struct{}{}
				}
			}
			collectMethods(nested, methods)
		}
	case []any:
		for _, nested := range typed {
			collectMethods(nested, methods)
		}
	}
}

func (c *Client) Snapshot(ctx context.Context) (json.RawMessage, error) {
	return c.CLIJSON(ctx, "api", "snapshot")
}

func (c *Client) ServerStatus(ctx context.Context) (json.RawMessage, error) {
	return c.CLIJSON(ctx, "status", "server", "--json")
}

func (c *Client) Ping(ctx context.Context) (json.RawMessage, error) {
	return c.CallSocket(ctx, "ping", map[string]any{})
}

// IntegrationStatus intentionally treats the current CLI output as opaque.
// Herdr 0.7.5 has no JSON integration-status command, so this preserves the
// user's current report without inferring state from terminal-like text.
func (c *Client) IntegrationStatus(ctx context.Context) (string, error) {
	result, err := c.Runner.Run(ctx, Command{Path: c.Binary, Args: []string{"integration", "status"}})
	if err != nil {
		return "", c.commandError("integration status", result, err)
	}
	value := redactOpaqueStatus(result.Stdout)
	if value == "" {
		return "", &model.StageError{Stage: "integration status", Code: model.ErrHerdrUnavailable, Message: "Herdr returned no integration status", Recovery: "herdr integration status"}
	}
	return value, nil
}

type PluginInfo struct {
	PluginID   string   `json:"plugin_id"`
	PluginRoot string   `json:"plugin_root"`
	Enabled    bool     `json:"enabled"`
	Warnings   []string `json:"warnings"`
}

type WorkspaceInfo struct {
	WorkspaceID string `json:"workspace_id"`
	Cwd         string `json:"cwd"`
	Label       string `json:"label"`
}

type TabInfo struct {
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
}

type PaneInfo struct {
	PaneID           string               `json:"pane_id"`
	TerminalID       string               `json:"terminal_id"`
	WorkspaceID      string               `json:"workspace_id"`
	TabID            string               `json:"tab_id"`
	Cwd              string               `json:"cwd"`
	ForegroundCwd    string               `json:"foreground_cwd"`
	Agent            string               `json:"agent"`
	Name             string               `json:"name"`
	AgentStatus      model.AgentStatus    `json:"agent_status"`
	InteractiveReady bool                 `json:"interactive_ready"`
	LaunchPending    bool                 `json:"launch_pending"`
	AgentSession     *model.NativeSession `json:"agent_session"`
}

type AgentInfo = PaneInfo

type WorktreeInfo struct {
	Path       string `json:"path"`
	Branch     string `json:"branch"`
	Repository string `json:"repository"`
}

type WorktreeOpenResult struct {
	Type        string        `json:"type"`
	AlreadyOpen bool          `json:"already_open"`
	Workspace   WorkspaceInfo `json:"workspace"`
	Tab         TabInfo       `json:"tab"`
	RootPane    PaneInfo      `json:"root_pane"`
	Worktree    WorktreeInfo  `json:"worktree"`
}

type PaneProcessInfo struct {
	PaneID                   string              `json:"pane_id"`
	ShellPID                 *int64              `json:"shell_pid"`
	ForegroundProcessGroupID *int64              `json:"foreground_process_group_id"`
	ForegroundProcesses      []ForegroundProcess `json:"foreground_processes"`
}

type ForegroundProcess struct {
	PID  int64  `json:"pid"`
	Name string `json:"name"`
	Cwd  string `json:"cwd"`
}

func (c *Client) PluginList(ctx context.Context, pluginID string) ([]PluginInfo, error) {
	args := []string{"plugin", "list", "--json"}
	if pluginID != "" {
		args = []string{"plugin", "list", "--plugin", pluginID, "--json"}
	}
	raw, err := c.CLIJSON(ctx, args...)
	if err != nil {
		return nil, err
	}
	var response struct {
		Plugins []PluginInfo `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, &model.StageError{
			Stage:    "plugin list",
			Code:     model.ErrPluginUnavailable,
			Message:  "Herdr returned an invalid plugin list",
			Recovery: "herdr plugin list --json",
			Cause:    err,
		}
	}
	return response.Plugins, nil
}

func (c *Client) LinkPlugin(ctx context.Context, path string) (PluginInfo, error) {
	var raw json.RawMessage
	var err error
	if c.SocketPath != "" {
		raw, err = c.CallSocket(ctx, "plugin.link", map[string]any{"path": path, "enabled": true})
	} else {
		raw, err = c.CLIJSON(ctx, "plugin", "link", "--enabled", path)
	}
	if err != nil {
		return PluginInfo{}, err
	}
	var response struct {
		Plugin PluginInfo `json:"plugin"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return PluginInfo{}, &model.StageError{
			Stage:    "plugin link",
			Code:     model.ErrPluginUnavailable,
			Message:  "Herdr returned an invalid plugin link response",
			Recovery: "herdr plugin link --enabled <installed-plugin-dir>",
			Cause:    err,
		}
	}
	return response.Plugin, nil
}

func (c *Client) EnablePlugin(ctx context.Context, pluginID string) (PluginInfo, error) {
	var raw json.RawMessage
	var err error
	if c.SocketPath != "" {
		raw, err = c.CallSocket(ctx, "plugin.enable", map[string]any{"plugin_id": pluginID})
	} else {
		raw, err = c.CLIJSON(ctx, "plugin", "enable", pluginID)
	}
	if err != nil {
		return PluginInfo{}, err
	}
	var response struct {
		Plugin PluginInfo `json:"plugin"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return PluginInfo{}, &model.StageError{
			Stage:    "plugin enable",
			Code:     model.ErrPluginUnavailable,
			Message:  "Herdr returned an invalid plugin enable response",
			Recovery: "herdr plugin enable ori.devflow",
			Cause:    err,
		}
	}
	return response.Plugin, nil
}

// WorktreeOpen opens an existing checkout only. This adapter intentionally has
// no worktree-create or worktree-remove method because Git remains Ori's
// authority for worktree lifecycle.
func (c *Client) WorktreeOpen(ctx context.Context, path string) (json.RawMessage, error) {
	return c.CLIJSON(ctx, "worktree", "open", "--path", path, "--no-focus", "--json")
}

func (c *Client) OpenExistingWorktree(ctx context.Context, path string) (WorktreeOpenResult, error) {
	raw, err := c.WorktreeOpen(ctx, path)
	if err != nil {
		return WorktreeOpenResult{}, err
	}
	var result WorktreeOpenResult
	if err := decodeResult("worktree open", raw, &result); err != nil {
		return WorktreeOpenResult{}, err
	}
	if result.Type != "worktree_opened" || result.Workspace.WorkspaceID == "" || result.RootPane.PaneID == "" || result.Tab.TabID == "" {
		return WorktreeOpenResult{}, &model.StageError{Stage: "worktree open", Code: model.ErrHerdrUnavailable, Message: "Herdr did not return an existing workspace, tab, and root pane", Recovery: "wt herd doctor"}
	}
	return result, nil
}

func (c *Client) WorkspaceClose(ctx context.Context, workspaceID string) (json.RawMessage, error) {
	return c.CLIJSON(ctx, "workspace", "close", workspaceID)
}

func (c *Client) PaneSplit(ctx context.Context, paneID, direction, cwd string) (json.RawMessage, error) {
	return c.CLIJSON(ctx, "pane", "split", paneID, "--direction", direction, "--cwd", cwd, "--no-focus")
}

func (c *Client) PaneProcessInfo(ctx context.Context, paneID string) (PaneProcessInfo, error) {
	raw, err := c.CLIJSON(ctx, "pane", "process-info", "--pane", paneID)
	if err != nil {
		return PaneProcessInfo{}, err
	}
	var response struct {
		ProcessInfo PaneProcessInfo `json:"process_info"`
	}
	if err := decodeResult("pane process info", raw, &response); err != nil {
		return PaneProcessInfo{}, err
	}
	if response.ProcessInfo.PaneID == "" {
		return PaneProcessInfo{}, &model.StageError{Stage: "pane process info", Code: model.ErrHerdrUnavailable, Message: "Herdr returned no pane process information", Recovery: "wt herd retry"}
	}
	return response.ProcessInfo, nil
}

func (c *Client) AgentList(ctx context.Context) (json.RawMessage, error) {
	return c.CLIJSON(ctx, "agent", "list")
}

func (c *Client) AgentGet(ctx context.Context, target string) (json.RawMessage, error) {
	return c.CLIJSON(ctx, "agent", "get", target)
}

func (c *Client) AgentGetInfo(ctx context.Context, target string) (AgentInfo, error) {
	raw, err := c.AgentGet(ctx, target)
	if err != nil {
		return AgentInfo{}, err
	}
	var response struct {
		Agent AgentInfo `json:"agent"`
	}
	if err := decodeResult("agent get", raw, &response); err != nil {
		return AgentInfo{}, err
	}
	if response.Agent.PaneID == "" {
		return AgentInfo{}, &model.StageError{Stage: "agent get", Code: model.ErrAgentMissing, Message: "Herdr returned no agent identity", Recovery: "wt herd status"}
	}
	return response.Agent, nil
}

func (c *Client) AgentListInfo(ctx context.Context) ([]AgentInfo, error) {
	raw, err := c.AgentList(ctx)
	if err != nil {
		return nil, err
	}
	var response struct {
		Agents []AgentInfo `json:"agents"`
	}
	if err := decodeResult("agent list", raw, &response); err != nil {
		return nil, err
	}
	return response.Agents, nil
}

func (c *Client) AgentStart(ctx context.Context, name, kind, paneID string, timeout time.Duration) (json.RawMessage, error) {
	return c.CLIJSON(ctx, "agent", "start", name, "--kind", kind, "--pane", paneID, "--timeout", fmt.Sprintf("%d", timeout.Milliseconds()))
}

func (c *Client) AgentStartInfo(ctx context.Context, name, kind, paneID string, timeout time.Duration) (AgentInfo, error) {
	raw, err := c.AgentStart(ctx, name, kind, paneID, timeout)
	if err != nil {
		return AgentInfo{}, err
	}
	var response struct {
		Agent AgentInfo `json:"agent"`
	}
	if err := decodeResult("agent start", raw, &response); err != nil {
		return AgentInfo{}, err
	}
	return response.Agent, nil
}

func (c *Client) AgentPrompt(ctx context.Context, target, text string, timeout time.Duration) (json.RawMessage, error) {
	return c.CLIJSON(ctx, "agent", "prompt", target, text, "--wait", "--timeout", fmt.Sprintf("%d", timeout.Milliseconds()))
}

func (c *Client) AgentPromptInfo(ctx context.Context, target, text string, timeout time.Duration) (AgentInfo, error) {
	raw, err := c.AgentPrompt(ctx, target, text, timeout)
	if err != nil {
		return AgentInfo{}, err
	}
	var response struct {
		Agent AgentInfo `json:"agent"`
	}
	if err := decodeResult("agent prompt", raw, &response); err != nil {
		return AgentInfo{}, err
	}
	return response.Agent, nil
}

func (c *Client) AgentRename(ctx context.Context, target, name string) (json.RawMessage, error) {
	return c.CLIJSON(ctx, "agent", "rename", target, name)
}

func (c *Client) AgentFocus(ctx context.Context, target string) (json.RawMessage, error) {
	return c.CLIJSON(ctx, "agent", "focus", target)
}

func (c *Client) AgentRead(ctx context.Context, target string, lines int) (json.RawMessage, error) {
	return c.CLIJSON(ctx, "agent", "read", target, "--source", "recent-unwrapped", "--lines", fmt.Sprintf("%d", lines))
}

func (c *Client) ReportWorkspaceMetadata(ctx context.Context, workspaceID, source string, tokens map[string]string) (json.RawMessage, error) {
	if c.SocketPath != "" {
		return c.CallSocket(ctx, "workspace.report_metadata", map[string]any{
			"workspace_id": workspaceID,
			"source":       source,
			"tokens":       tokens,
		})
	}
	args := []string{"workspace", "report-metadata", workspaceID, "--source", source}
	for _, key := range sortedTokenKeys(tokens) {
		value := tokens[key]
		args = append(args, "--token", key+"="+value)
	}
	return c.CLIJSON(ctx, args...)
}

func (c *Client) ReportPaneMetadata(ctx context.Context, paneID, source string, tokens map[string]string) (json.RawMessage, error) {
	if c.SocketPath != "" {
		return c.CallSocket(ctx, "pane.report_metadata", map[string]any{
			"pane_id": paneID,
			"source":  source,
			"tokens":  tokens,
		})
	}
	args := []string{"pane", "report-metadata", paneID, "--source", source}
	for _, key := range sortedTokenKeys(tokens) {
		value := tokens[key]
		args = append(args, "--token", key+"="+value)
	}
	return c.CLIJSON(ctx, args...)
}

func sortedTokenKeys(tokens map[string]string) []string {
	keys := make([]string, 0, len(tokens))
	for key := range tokens {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (c *Client) SetAgentView(ctx context.Context, params map[string]any) (json.RawMessage, error) {
	return c.CallSocket(ctx, "agent.view.set", params)
}

func (c *Client) ClearAgentView(ctx context.Context, source string) (json.RawMessage, error) {
	return c.CallSocket(ctx, "agent.view.clear", map[string]any{"source": source})
}

// CallSocket sends a single documented request through Herdr's local JSONL
// socket. Event subscribers use Subscribe instead so their connection remains
// open after the acknowledgement.
func (c *Client) CallSocket(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c.SocketPath == "" {
		return nil, &model.StageError{
			Stage:    method,
			Code:     model.ErrHerdrUnavailable,
			Message:  "no Herdr socket path is available for this operation",
			Recovery: "run this command inside Herdr or set HERDR_SOCKET_PATH",
		}
	}
	if runtime.GOOS == "windows" {
		return nil, &model.StageError{
			Stage:    method,
			Code:     model.ErrSchedulerUnsupported,
			Message:  "raw Windows named-pipe socket calls are not available in this build",
			Recovery: "use the Herdr CLI wrapper for this operation",
		}
	}

	dialer := net.Dialer{}
	connection, err := dialer.DialContext(ctx, "unix", c.SocketPath)
	if err != nil {
		return nil, socketError(method, err)
	}
	defer connection.Close()

	id := fmt.Sprintf("ori-devflow-%d", c.sequence.Add(1))
	request := map[string]any{"id": id, "method": method, "params": params}
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode %s request: %w", method, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	if _, err := connection.Write(append(encoded, '\n')); err != nil {
		return nil, socketError(method, err)
	}
	scanner := bufio.NewScanner(connection)
	scanner.Buffer(make([]byte, 1024), 8*1024*1024)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, socketError(method, err)
		}
		return nil, &model.StageError{Stage: method, Code: model.ErrHerdrUnavailable, Message: "Herdr socket closed before replying", Recovery: "herdr status server"}
	}
	return decodeJSONResponse(scanner.Bytes())
}

type EventStream struct {
	Connection net.Conn
	Scanner    *bufio.Scanner
}

// Subscribe leaves the socket open after Herdr acknowledges the event stream.
func (c *Client) Subscribe(ctx context.Context, subscriptions []map[string]any) (*EventStream, error) {
	if c.SocketPath == "" || runtime.GOOS == "windows" {
		return nil, &model.StageError{Stage: "events.subscribe", Code: model.ErrHerdrUnavailable, Message: "a Unix Herdr socket is required for event subscriptions", Recovery: "run status --watch inside Herdr"}
	}
	dialer := net.Dialer{}
	connection, err := dialer.DialContext(ctx, "unix", c.SocketPath)
	if err != nil {
		return nil, socketError("events.subscribe", err)
	}
	id := fmt.Sprintf("ori-devflow-%d", c.sequence.Add(1))
	request, err := json.Marshal(map[string]any{"id": id, "method": "events.subscribe", "params": map[string]any{"subscriptions": subscriptions}})
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	if _, err := connection.Write(append(request, '\n')); err != nil {
		_ = connection.Close()
		return nil, socketError("events.subscribe", err)
	}
	scanner := bufio.NewScanner(connection)
	scanner.Buffer(make([]byte, 1024), 8*1024*1024)
	if !scanner.Scan() {
		_ = connection.Close()
		if err := scanner.Err(); err != nil {
			return nil, socketError("events.subscribe", err)
		}
		return nil, &model.StageError{Stage: "events.subscribe", Code: model.ErrHerdrUnavailable, Message: "Herdr socket closed before acknowledging subscription", Recovery: "herdr status server"}
	}
	if _, err := decodeJSONResponse(scanner.Bytes()); err != nil {
		_ = connection.Close()
		return nil, err
	}
	return &EventStream{Connection: connection, Scanner: scanner}, nil
}

func (s *EventStream) Next() (json.RawMessage, error) {
	if s == nil || s.Scanner == nil {
		return nil, io.EOF
	}
	if !s.Scanner.Scan() {
		if err := s.Scanner.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	copyOfLine := append([]byte(nil), s.Scanner.Bytes()...)
	return json.RawMessage(copyOfLine), nil
}

func (s *EventStream) Close() error {
	if s == nil || s.Connection == nil {
		return nil
	}
	return s.Connection.Close()
}

func decodeResult(stage string, raw json.RawMessage, destination any) error {
	if err := json.Unmarshal(raw, destination); err != nil {
		return &model.StageError{Stage: stage, Code: model.ErrHerdrUnavailable, Message: "Herdr returned an unexpected structured response", Recovery: "wt herd doctor", Cause: err}
	}
	return nil
}

func decodeJSONResponse(raw []byte) (json.RawMessage, error) {
	trimmed := bytesTrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, &model.StageError{Stage: "Herdr response", Code: model.ErrHerdrUnavailable, Message: "Herdr returned no JSON response", Recovery: "herdr status server"}
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(trimmed, &envelope); err != nil {
		return nil, &model.StageError{Stage: "Herdr response", Code: model.ErrHerdrUnavailable, Message: "Herdr did not return structured JSON", Recovery: "herdr status server", Cause: err}
	}
	if envelope.Error != nil {
		return nil, apiError(envelope.Error.Code, envelope.Error.Message)
	}
	if len(envelope.Result) > 0 {
		return envelope.Result, nil
	}
	return json.RawMessage(append([]byte(nil), trimmed...)), nil
}

func apiError(code, message string) error {
	stageCode := model.ErrHerdrUnavailable
	recovery := "herdr status server"
	switch code {
	case "permission_denied":
		stageCode = model.ErrHerdrPermission
		recovery = "check Herdr socket permissions, then run wt herd doctor"
	case "not_found", "agent_not_found", "agent_pane_not_found":
		stageCode = model.ErrAgentMissing
		recovery = "wt herd status && wt herd rebind <role> --target <live-target>"
	case "plugin_not_found", "plugin_disabled", "plugin_invalid":
		stageCode = model.ErrPluginUnavailable
		recovery = "wt herd setup"
	}
	return &model.StageError{Stage: "Herdr API", Code: stageCode, Message: message, Recovery: recovery}
}

func (c *Client) commandError(stage string, result CommandResult, err error) error {
	if errors.Is(err, exec.ErrNotFound) {
		return &model.StageError{Stage: stage, Code: model.ErrHerdrMissing, Message: "Herdr executable was not found", Recovery: "install Herdr, then run wt herd setup", Cause: err}
	}
	if errors.Is(err, os.ErrPermission) {
		return &model.StageError{Stage: stage, Code: model.ErrHerdrPermission, Message: "Herdr executable or socket permission was denied", Recovery: "check executable and socket permissions, then run wt herd doctor", Cause: err}
	}
	return &model.StageError{Stage: stage, Code: model.ErrHerdrUnavailable, Message: "Herdr command failed before returning a structured response", Recovery: "herdr status server && wt herd doctor", Cause: err}
}

func socketError(stage string, err error) error {
	code := model.ErrHerdrUnavailable
	recovery := "herdr status server && wt herd doctor"
	if errors.Is(err, os.ErrPermission) {
		code = model.ErrHerdrPermission
		recovery = "check Herdr socket permissions, then run wt herd doctor"
	}
	return &model.StageError{Stage: stage, Code: code, Message: "could not reach the Herdr local socket", Recovery: recovery, Cause: err}
}

func bytesTrimSpace(value []byte) []byte {
	return []byte(strings.TrimSpace(string(value)))
}

func redactOpaqueStatus(value []byte) string {
	const maxRunes = 480
	var builder strings.Builder
	for _, runeValue := range string(value) {
		switch {
		case runeValue == '\n' || runeValue == '\r' || runeValue == '\t':
			builder.WriteRune(' ')
		case runeValue >= 32 && runeValue != 127:
			builder.WriteRune(runeValue)
		}
		if builder.Len() >= maxRunes {
			builder.WriteString("…")
			break
		}
	}
	return strings.Join(strings.Fields(builder.String()), " ")
}
