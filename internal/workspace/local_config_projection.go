package workspace

import (
	"bytes"
	"encoding/json"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

type localAgentConfig struct {
	Version            int    `json:"version"`
	APIKey             string `json:"api_key"`
	AllowWebSearch     *bool  `json:"allow_web_search"`
	AllowNativeMCP     *bool  `json:"allow_native_mcp"`
	FallbackAllowCloud *bool  `json:"fallback_allow_cloud"`
}

func clearAgentLocalConfig(ag *agent.Agent) {
	ag.Settings.APIKey = ""
	// Nil means allowed for web search, so use an explicit false projection.
	ag.Settings.AllowWebSearch = new(bool)
	ag.Settings.AllowNativeMCPTools = new(bool)
	ag.Settings.FallbackAllowCloud = new(bool)
}

func splitAgentLocalConfig(input *agent.Agent) (*agent.Agent, []byte, error) {
	if input == nil {
		return nil, nil, ErrLocalConfigInvalid
	}
	data, err := json.Marshal(input)
	if err != nil || len(data) > workspacecontinuity.MaxRecordBytes {
		return nil, nil, ErrLocalConfigInvalid
	}
	var portable agent.Agent
	if err := workspacecontinuity.DecodeDocument(data, &portable, workspacecontinuity.MaxRecordBytes); err != nil {
		return nil, nil, err
	}
	private := localAgentConfig{Version: 1, APIKey: portable.Settings.APIKey, AllowWebSearch: portable.Settings.AllowWebSearch,
		AllowNativeMCP: portable.Settings.AllowNativeMCPTools, FallbackAllowCloud: portable.Settings.FallbackAllowCloud}
	// #nosec G117 -- This private DTO is transient plaintext for AES-GCM staging in the local owner, never a file/HTTP/checkpoint payload.
	data, err = json.Marshal(private)
	if err != nil || len(data) > maxLocalConfigBytes {
		return nil, nil, ErrLocalConfigInvalid
	}
	clearAgentLocalConfig(&portable)
	portable.WorkspaceLocalConfigID = ""
	return &portable, data, nil
}

func applyAgentLocalConfig(ag *agent.Agent, data []byte) error {
	var private localAgentConfig
	if err := workspacecontinuity.DecodeRequiredDocument(data, &private, maxLocalConfigBytes); err != nil || private.Version != 1 {
		return ErrLocalConfigInvalid
	}
	ag.Settings.APIKey = private.APIKey
	ag.Settings.AllowWebSearch = private.AllowWebSearch
	ag.Settings.AllowNativeMCPTools = private.AllowNativeMCP
	ag.Settings.FallbackAllowCloud = private.FallbackAllowCloud
	return nil
}

type localMCPConfig struct {
	ServerName        string                `json:"server_name"`
	RuntimeKind       BindingRuntimeKind    `json:"runtime_kind"`
	Config            map[string]any        `json:"config"`
	Scope             map[string]any        `json:"scope"`
	AllowedTools      []string              `json:"allowed_tools"`
	DefaultSideEffect SideEffect            `json:"default_side_effect"`
	ToolOverrides     map[string]SideEffect `json:"tool_overrides"`
}

type localSkillConfig struct {
	SkillName         string                `json:"skill_name"`
	Config            map[string]any        `json:"config"`
	Trusted           bool                  `json:"trusted"`
	DefaultSideEffect SideEffect            `json:"default_side_effect"`
	ToolOverrides     map[string]SideEffect `json:"tool_overrides"`
}

type localBindingsConfig struct {
	Version           int                         `json:"version"`
	MCP               map[string]localMCPConfig   `json:"mcp"`
	Skills            map[string]localSkillConfig `json:"skills"`
	AllowNativeMCPCLI bool                        `json:"allow_native_mcp_cli"`
	Runtime           *WorkspaceRuntimeState      `json:"runtime"`
}

// splitWorkspaceLocalConfig preserves every non-private field, without FromJSON's
// task/instance migrations. Opaque MCP/skill config and scope maps are entirely
// local; guessing secret field names cannot make an arbitrary connector safe.
func splitWorkspaceLocalConfig(input *Workspace) (*Workspace, []byte, error) {
	if input == nil {
		return nil, nil, ErrLocalConfigInvalid
	}
	data, err := input.ToJSON()
	if err != nil {
		return nil, nil, ErrLocalConfigInvalid
	}
	if len(data) > maxNativeWorkspaceBytes {
		return nil, nil, workspacecontinuity.ErrLimit
	}
	var portable Workspace
	// This is a clone of a canonical Go value, not an import decoder. Native
	// history does not acquire the checkpoint's token/record limits on save.
	// Preserve authored numbers while moving only the private fields; a JSON
	// round trip through float64 would change large IDs in otherwise inert data.
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&portable); err != nil {
		return nil, nil, ErrLocalConfigInvalid
	}
	private := localBindingsConfig{Version: 1, MCP: map[string]localMCPConfig{}, Skills: map[string]localSkillConfig{},
		AllowNativeMCPCLI: portable.AllowNativeMCPCLI, Runtime: CloneWorkspaceRuntimeState(portable.RuntimeState)}
	for i := range portable.MCPBindings {
		binding := &portable.MCPBindings[i]
		if !workspacecontinuity.ValidID(binding.ID) {
			return nil, nil, ErrLocalConfigInvalid
		}
		if _, exists := private.MCP[binding.ID]; exists {
			return nil, nil, ErrLocalConfigInvalid
		}
		private.MCP[binding.ID] = localMCPConfig{ServerName: binding.ServerName, RuntimeKind: binding.RuntimeKind, Config: binding.Config, Scope: binding.Scope,
			AllowedTools: binding.AllowedTools, DefaultSideEffect: binding.DefaultSideEffect, ToolOverrides: binding.ToolOverrides}
		binding.Config, binding.Scope = nil, nil
		binding.AllowedTools = []string{}
		binding.DefaultSideEffect, binding.ToolOverrides = "", nil
	}
	for i := range portable.SkillBindings {
		binding := &portable.SkillBindings[i]
		if !workspacecontinuity.ValidID(binding.ID) {
			return nil, nil, ErrLocalConfigInvalid
		}
		if _, exists := private.Skills[binding.ID]; exists {
			return nil, nil, ErrLocalConfigInvalid
		}
		private.Skills[binding.ID] = localSkillConfig{SkillName: binding.SkillName, Config: binding.Config, Trusted: binding.Trusted,
			DefaultSideEffect: binding.DefaultSideEffect, ToolOverrides: binding.ToolOverrides}
		binding.Config, binding.Trusted = nil, false
		binding.DefaultSideEffect, binding.ToolOverrides = "", nil
	}
	portable.AllowNativeMCPCLI = false
	portable.WorkspaceLocalConfigID = ""
	if portable.RuntimeState != nil {
		// Keep operating-mode intent, not grants or locally verified readiness.
		portable.RuntimeState = &WorkspaceRuntimeState{SelectedModeID: portable.RuntimeState.SelectedModeID}
	}
	data, err = json.Marshal(private)
	if err != nil || len(data) > maxLocalConfigBytes {
		return nil, nil, ErrLocalConfigInvalid
	}
	return &portable, data, nil
}

func decodeBindingsLocalConfig(data []byte) (localBindingsConfig, error) {
	// Decode nested private DTOs independently so omission of allowed_tools
	// cannot turn an explicit deny into the legacy nil/all-tools grant.
	var envelope struct {
		Version           int                        `json:"version"`
		MCP               map[string]json.RawMessage `json:"mcp"`
		Skills            map[string]json.RawMessage `json:"skills"`
		AllowNativeMCPCLI bool                       `json:"allow_native_mcp_cli"`
		Runtime           *WorkspaceRuntimeState     `json:"runtime"`
	}
	if err := workspacecontinuity.DecodeRequiredDocument(data, &envelope, maxLocalConfigBytes); err != nil || envelope.Version != 1 {
		return localBindingsConfig{}, ErrLocalConfigInvalid
	}
	private := localBindingsConfig{Version: 1, MCP: map[string]localMCPConfig{}, Skills: map[string]localSkillConfig{},
		AllowNativeMCPCLI: envelope.AllowNativeMCPCLI, Runtime: envelope.Runtime}
	for id, raw := range envelope.MCP {
		var value localMCPConfig
		if err := workspacecontinuity.DecodeRequiredDocument(raw, &value, maxLocalConfigBytes); err != nil {
			return localBindingsConfig{}, ErrLocalConfigInvalid
		}
		private.MCP[id] = value
	}
	for id, raw := range envelope.Skills {
		var value localSkillConfig
		if err := workspacecontinuity.DecodeRequiredDocument(raw, &value, maxLocalConfigBytes); err != nil {
			return localBindingsConfig{}, ErrLocalConfigInvalid
		}
		private.Skills[id] = value
	}
	return private, nil
}

func applyBindingsLocalConfig(ws *Workspace, data []byte) error {
	private, err := decodeBindingsLocalConfig(data)
	if err != nil {
		return err
	}
	for i := range ws.MCPBindings {
		binding := &ws.MCPBindings[i]
		if value, exists := private.MCP[binding.ID]; exists {
			if value.ServerName != binding.ServerName || value.RuntimeKind != binding.RuntimeKind {
				return ErrLocalConfigInvalid
			}
			binding.Config, binding.Scope = value.Config, value.Scope
			binding.AllowedTools = value.AllowedTools
			binding.DefaultSideEffect, binding.ToolOverrides = value.DefaultSideEffect, value.ToolOverrides
		}
	}
	for i := range ws.SkillBindings {
		binding := &ws.SkillBindings[i]
		if value, exists := private.Skills[binding.ID]; exists {
			if value.SkillName != binding.SkillName {
				return ErrLocalConfigInvalid
			}
			binding.Config, binding.Trusted = value.Config, value.Trusted
			binding.DefaultSideEffect, binding.ToolOverrides = value.DefaultSideEffect, value.ToolOverrides
		}
	}
	ws.AllowNativeMCPCLI = private.AllowNativeMCPCLI
	ws.RuntimeState = CloneWorkspaceRuntimeState(private.Runtime)
	return nil
}
