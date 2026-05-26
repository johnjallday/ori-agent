package orchestrationhttp

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestHandlerConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  HandlerConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "empty config",
			config:  HandlerConfig{},
			wantErr: true,
			errMsg:  "AgentStore is required",
		},
		{
			name: "missing WorkspaceStore",
			config: HandlerConfig{
				AgentStore: &mockStore{},
			},
			wantErr: true,
			errMsg:  "WorkspaceStore is required",
		},
		{
			name: "missing EventBus",
			config: HandlerConfig{
				AgentStore:     &mockStore{},
				WorkspaceStore: &mockWorkspaceStore{},
			},
			wantErr: true,
			errMsg:  "EventBus is required",
		},
		{
			name: "valid minimal config",
			config: HandlerConfig{
				AgentStore:     &mockStore{},
				WorkspaceStore: &mockWorkspaceStore{},
				EventBus:       workspace.DefaultEventBus(),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil && tt.errMsg != "" {
				if err.Error() != tt.errMsg {
					t.Errorf("Validate() error = %q, want %q", err.Error(), tt.errMsg)
				}
			}
		})
	}
}

func TestNewHandler(t *testing.T) {
	eventBus := workspace.DefaultEventBus()

	t.Run("valid config creates handler", func(t *testing.T) {
		h, err := NewHandler(HandlerConfig{
			AgentStore:     &mockStore{},
			WorkspaceStore: &mockWorkspaceStore{},
			EventBus:       eventBus,
		})
		if err != nil {
			t.Fatalf("NewHandler() error = %v", err)
		}
		if h == nil {
			t.Fatal("NewHandler() returned nil handler")
			return
		}
		// Core sub-handlers should be initialized
		if h.workspaceHandler == nil {
			t.Error("workspaceHandler should be initialized")
		}
		if h.messageHandler == nil {
			t.Error("messageHandler should be initialized")
		}
		if h.capabilitiesHandler == nil {
			t.Error("capabilitiesHandler should be initialized")
		}
	})

	t.Run("invalid config returns error", func(t *testing.T) {
		_, err := NewHandler(HandlerConfig{})
		if err == nil {
			t.Fatal("NewHandler() should return error for invalid config")
		}
	})

	t.Run("optional dependencies initialize sub-handlers", func(t *testing.T) {
		h, err := NewHandler(HandlerConfig{
			AgentStore:          &mockStore{},
			WorkspaceStore:      &mockWorkspaceStore{},
			EventBus:            eventBus,
			NotificationService: workspace.NewNotificationService(eventBus, 100),
		})
		if err != nil {
			t.Fatalf("NewHandler() error = %v", err)
		}
		if h.notificationHandler == nil {
			t.Error("notificationHandler should be initialized when NotificationService is provided")
		}
	})
}

// Mock implementations for testing

// mockStore implements store.Store interface
type mockStore struct{}

func (m *mockStore) ListAgents() []string                                             { return nil }
func (m *mockStore) CreateAgent(name string, cfg *store.CreateAgentConfig) error      { return nil }
func (m *mockStore) DeleteAgent(name string) error                                    { return nil }
func (m *mockStore) GetAgent(name string) (*agent.Agent, bool)                        { return nil, false }
func (m *mockStore) SetAgent(name string, ag *agent.Agent) error                      { return nil }
func (m *mockStore) UpdateAgent(name string, updateFn func(*agent.Agent) error) error { return nil }
func (m *mockStore) Save() error                                                      { return nil }
func (m *mockStore) ClearAgents() error                                               { return nil }

// mockWorkspaceStore implements workspace.Store interface
type mockWorkspaceStore struct{}

func (m *mockWorkspaceStore) Save(ws *workspace.Workspace) error          { return nil }
func (m *mockWorkspaceStore) Get(id string) (*workspace.Workspace, error) { return nil, nil }
func (m *mockWorkspaceStore) List() ([]string, error)                     { return nil, nil }
func (m *mockWorkspaceStore) Delete(id string) error                      { return nil }
func (m *mockWorkspaceStore) ListActive() ([]*workspace.Workspace, error) { return nil, nil }
func (m *mockWorkspaceStore) GetFilesPath(workspaceID string) string      { return "" }
func (m *mockWorkspaceStore) GetOutputsPath(workspaceID string) string    { return "" }
func (m *mockWorkspaceStore) GetWorkspaceAgent(workspaceID, agentName string) (*agent.Agent, bool, error) {
	return nil, false, nil
}
func (m *mockWorkspaceStore) SaveWorkspaceAgent(workspaceID, agentName string, ag *agent.Agent) error {
	return nil
}
func (m *mockWorkspaceStore) Lock(wsID string) func() { return func() {} }
func (m *mockWorkspaceStore) Update(wsID string, fn func(*workspace.Workspace) error) error {
	return workspace.CanonicalUpdate(m, wsID, fn)
}
