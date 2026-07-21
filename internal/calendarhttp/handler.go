// Package calendarhttp exposes the Calendar Ops guided connector setup as a
// workspace-scoped HTTP API: setup state, connector-preset add, guided mapping
// suggestions, connection validation, and persistence of the resolved binding,
// mappings, read-only tool allowlist, calendar/timezone/context settings, and
// agent access grant.
//
// This package owns only the first-run *setup* surface (group 3). The safe
// runtime CalendarMCPGateway, agenda reads, and mutation confirmation boundary
// are a separate concern added later; this handler never exposes write
// operations to agents and persists a read-only AllowedTools allowlist.
package calendarhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/johnjallday/ori-agent/internal/calendar"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// CalendarOpsTemplateID is the built-in template id whose workspaces this setup
// flow applies to. It matches the shipped starter template folder name and the
// provenance recorded at workspace creation.
const CalendarOpsTemplateID = "calendar-ops"

// calendarOpsAgentNames are the roster agents the calendar binding is granted
// to (read-only). They match the shipped calendar-ops template roster (frozen
// like every built-in roster). Any workspace agent NOT in this set is denied
// the calendar binding (FR27: only Scheduler and Meeting Prep receive it).
var calendarOpsAgentNames = []string{"Scheduler", "Meeting Prep"}

// FolderStore is the workspace folder store subset the setup flow needs: it
// reads and persists the workspace's MCP bindings, agent instances, agent MCP
// access, and template provenance.
//
// GetFolderWorkspace (not the plain Get most other callers use) is required
// deliberately: TemplateProvenance -- which loadCalendarOpsWorkspace's
// IsFromTemplate check depends on -- has no SQLite column, so a SyncStore's
// primary-backed Get always returns it nil. GetFolderWorkspace reads the
// canonical workspace.json directly, matching the pattern already used by
// other folder-store-only fields (see internal/workspace/http_handlers.go's
// identically-shaped interface).
type FolderStore interface {
	GetFolderWorkspace(id string) (*agentworkspace.Workspace, error)
	Save(ws *agentworkspace.Workspace) error
}

// WorkspaceLister lists workspaces so context-workspace candidates can be
// filtered to the current user's own, active, non-group workspaces.
type WorkspaceLister interface {
	ListWorkspaces(ctx context.Context) ([]session.Workspace, error)
}

// connectorStatus is the resolved runtime status of a bound MCP connector,
// normalized away from mcp.ServerStatus so the state machine input is a small,
// testable set of booleans.
type connectorStatus struct {
	Present      bool             `json:"present"`
	Status       mcp.ServerStatus `json:"status,omitempty"`
	Connected    bool             `json:"connected"`
	AuthRequired bool             `json:"auth_required"`
	Degraded     bool             `json:"degraded"`
	Transport    string           `json:"transport,omitempty"`
	URL          string           `json:"url,omitempty"`
}

// Handler serves the Calendar Ops setup API.
type Handler struct {
	folders  FolderStore
	lister   WorkspaceLister
	registry *mcp.Registry
	config   *mcp.ConfigManager
	provider userprofile.UserProvider

	// connectorStatusFn resolves a bound server's runtime status. Injectable so
	// setup-state tests can drive every transition without a live registry.
	connectorStatusFn func(serverName string) connectorStatus
	// toolCallerFor builds a calendar.ToolCaller for a server. Injectable so the
	// validate flow can be tested with a fake connector.
	toolCallerFor func(serverName string) calendar.ToolCaller

	// cache is the gateway's short-TTL read cache (FR34). Always non-nil.
	cache *readCache
	// confirmations is the mutation confirmation store (FR31). Always non-nil.
	confirmations *confirmationStore
}

// NewHandler constructs a Calendar Ops setup handler. registry and config may be
// nil in tests that exercise only the store-backed paths (setup state, save);
// the connector-status and tool-caller seams then fall back to safe defaults.
func NewHandler(folders FolderStore, lister WorkspaceLister, registry *mcp.Registry, config *mcp.ConfigManager, provider userprofile.UserProvider) *Handler {
	if provider == nil {
		provider = userprofile.LocalUserProvider{}
	}
	h := &Handler{
		folders:  folders,
		lister:   lister,
		registry: registry,
		config:   config,
		provider: provider,
	}
	h.connectorStatusFn = h.registryConnectorStatus
	h.toolCallerFor = h.registryToolCaller
	h.cache = newReadCache(readCacheTTL)
	h.confirmations = newConfirmationStore(confirmationTTL)
	return h
}

// WithConnectorStatusFn overrides connector-status resolution (tests).
func (h *Handler) WithConnectorStatusFn(fn func(serverName string) connectorStatus) *Handler {
	if fn != nil {
		h.connectorStatusFn = fn
	}
	return h
}

// WithToolCallerFactory overrides the tool-caller factory (tests).
func (h *Handler) WithToolCallerFactory(fn func(serverName string) calendar.ToolCaller) *Handler {
	if fn != nil {
		h.toolCallerFor = fn
	}
	return h
}

func (h *Handler) currentUserID(ctx context.Context) (string, error) {
	if h == nil || h.provider == nil {
		return userprofile.LocalUserID, nil
	}
	userID, err := h.provider.CurrentUserID(ctx)
	if err != nil {
		return "", err
	}
	if userID == "" {
		return userprofile.LocalUserID, nil
	}
	return userID, nil
}

// registryConnectorStatus resolves a bound server's status from the live MCP
// registry/config. A nil registry (tests) reports "not present".
func (h *Handler) registryConnectorStatus(serverName string) connectorStatus {
	serverName = strings.TrimSpace(serverName)
	if serverName == "" || h.registry == nil {
		return connectorStatus{}
	}
	out := connectorStatus{Present: true}
	if h.config != nil {
		if cfg, err := h.config.GetServer(serverName); err == nil && cfg != nil {
			out.Transport = cfg.Transport
			out.URL = cfg.URL
		} else if err != nil {
			out.Present = false
			return out
		}
	}
	status, err := h.registry.GetServerStatus(serverName)
	if err != nil {
		out.Present = false
		return out
	}
	out.Status = status
	switch status {
	case mcp.StatusRunning:
		out.Connected = true
	case mcp.StatusAuthRequired:
		out.AuthRequired = true
	case mcp.StatusError:
		out.Degraded = true
	case mcp.StatusStopped, mcp.StatusStarting, mcp.StatusRestarting:
		// transitional / not yet usable; treated as needing (re)connection
	}
	return out
}

// registryToolCaller builds a calendar.ToolCaller that invokes tools on a
// server through the MCP registry and decodes the structured result into a
// generic JSON tree. A nil registry yields a caller that always errors.
func (h *Handler) registryToolCaller(serverName string) calendar.ToolCaller {
	return func(ctx context.Context, toolName string, args map[string]any) (any, error) {
		if h.registry == nil {
			return nil, fmt.Errorf("mcp registry is unavailable")
		}
		res, err := h.registry.CallTool(ctx, serverName, toolName, args)
		if err != nil {
			return nil, err
		}
		return decodeToolResult(res)
	}
}

// decodeToolResult turns an MCP tool-call result into a generic JSON tree
// (map/slice/scalars). It prefers the structured content and falls back to
// parsing text content as JSON. A non-JSON text response is an error, never
// reinterpreted — Calendar Ops only accepts deterministically mapped structured
// results (FR14).
func decodeToolResult(res *mcp.ToolCallResult) (any, error) {
	if res == nil {
		return nil, fmt.Errorf("connector returned no result")
	}
	if res.IsError {
		return nil, fmt.Errorf("connector reported a tool error")
	}
	if res.StructuredContent != nil {
		b, err := json.Marshal(res.StructuredContent)
		if err != nil {
			return nil, fmt.Errorf("connector structured content was not encodable: %w", err)
		}
		var out any
		if err := json.Unmarshal(b, &out); err != nil {
			return nil, fmt.Errorf("connector structured content was not valid JSON: %w", err)
		}
		return out, nil
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	text := strings.TrimSpace(sb.String())
	if text == "" {
		return nil, fmt.Errorf("connector returned no structured content")
	}
	var out any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return nil, fmt.Errorf("connector response was not structured JSON")
	}
	return out, nil
}
