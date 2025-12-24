package pluginloader

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/pluginapi"
)

// mockPluginTool is a minimal implementation for testing
type mockPluginTool struct {
	name string
}

func (m *mockPluginTool) Definition() pluginapi.Tool {
	return pluginapi.Tool{
		Name:        m.name,
		Description: "Test plugin",
	}
}

func (m *mockPluginTool) Call(ctx context.Context, args string) (string, error) {
	return "mock result", nil
}

// mockVersionedTool adds version support
type mockVersionedTool struct {
	mockPluginTool
	version string
}

func (m *mockVersionedTool) Version() string {
	return m.version
}

// mockMetadataProvider adds metadata support
type mockMetadataProvider struct {
	mockPluginTool
	metadata *pluginapi.PluginMetadata
	tags     []string
}

func (m *mockMetadataProvider) GetMetadata() (*pluginapi.PluginMetadata, error) {
	return m.metadata, nil
}

func (m *mockMetadataProvider) GetTags() []string {
	return m.tags
}

// mockFileHandler adds file attachment support
type mockFileHandler struct {
	mockPluginTool
	acceptedTypes []string
}

func (m *mockFileHandler) AcceptsFiles() []string {
	return m.acceptedTypes
}

func (m *mockFileHandler) CallWithFiles(ctx context.Context, args string, files []pluginapi.FileAttachment) (string, error) {
	return "result", nil
}

// mockAgentAwareTool adds agent context support
type mockAgentAwareTool struct {
	mockPluginTool
	context *pluginapi.AgentContext
}

func (m *mockAgentAwareTool) SetAgentContext(ctx pluginapi.AgentContext) {
	m.context = &ctx
}

func TestIsRPCPlugin(t *testing.T) {
	// Regular mock is not an RPC plugin
	regularTool := &mockPluginTool{name: "test"}
	if IsRPCPlugin(regularTool) {
		t.Error("IsRPCPlugin() should return false for non-RPC plugins")
	}

	// RPCPluginClient is an RPC plugin
	rpcClient := &RPCPluginClient{tool: regularTool}
	if !IsRPCPlugin(rpcClient) {
		t.Error("IsRPCPlugin() should return true for RPCPluginClient")
	}
}

func TestGetPluginVersion(t *testing.T) {
	tests := []struct {
		name     string
		tool     pluginapi.PluginTool
		expected string
	}{
		{
			name:     "non-versioned plugin",
			tool:     &mockPluginTool{name: "test"},
			expected: "",
		},
		{
			name:     "versioned plugin",
			tool:     &mockVersionedTool{mockPluginTool: mockPluginTool{name: "test"}, version: "1.2.3"},
			expected: "1.2.3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version := GetPluginVersion(tt.tool)
			if version != tt.expected {
				t.Errorf("GetPluginVersion() = %q, want %q", version, tt.expected)
			}
		})
	}
}

func TestGetPluginMetadata(t *testing.T) {
	t.Run("non-metadata provider", func(t *testing.T) {
		tool := &mockPluginTool{name: "test"}
		metadata, err := GetPluginMetadata(tool)
		if err != nil {
			t.Errorf("GetPluginMetadata() error = %v", err)
		}
		if metadata != nil {
			t.Error("GetPluginMetadata() should return nil for non-metadata provider")
		}
	})

	t.Run("metadata provider", func(t *testing.T) {
		expectedMeta := &pluginapi.PluginMetadata{
			Name:        "test-plugin",
			Version:     "1.0.0",
			License:     "MIT",
			Repository:  "https://github.com/test/plugin",
			Description: "A test plugin",
		}
		tool := &mockMetadataProvider{
			mockPluginTool: mockPluginTool{name: "test"},
			metadata:       expectedMeta,
		}
		metadata, err := GetPluginMetadata(tool)
		if err != nil {
			t.Errorf("GetPluginMetadata() error = %v", err)
		}
		if metadata == nil {
			t.Fatal("GetPluginMetadata() returned nil for metadata provider")
		}
		if metadata.Name != expectedMeta.Name {
			t.Errorf("GetPluginMetadata().Name = %q, want %q", metadata.Name, expectedMeta.Name)
		}
	})
}

func TestGetPluginFileSupport(t *testing.T) {
	t.Run("non-file handler", func(t *testing.T) {
		tool := &mockPluginTool{name: "test"}
		supports, types := GetPluginFileSupport(tool)
		if supports {
			t.Error("GetPluginFileSupport() should return false for non-file handler")
		}
		if types != nil {
			t.Error("GetPluginFileSupport() should return nil types for non-file handler")
		}
	})

	t.Run("file handler", func(t *testing.T) {
		expectedTypes := []string{"image/*", "application/pdf"}
		tool := &mockFileHandler{
			mockPluginTool: mockPluginTool{name: "test"},
			acceptedTypes:  expectedTypes,
		}
		supports, types := GetPluginFileSupport(tool)
		if !supports {
			t.Error("GetPluginFileSupport() should return true for file handler")
		}
		if len(types) != len(expectedTypes) {
			t.Errorf("GetPluginFileSupport() types = %v, want %v", types, expectedTypes)
		}
	})
}

func TestPluginSupportsFiles(t *testing.T) {
	t.Run("non-file handler", func(t *testing.T) {
		tool := &mockPluginTool{name: "test"}
		if PluginSupportsFiles(tool) {
			t.Error("PluginSupportsFiles() should return false for non-file handler")
		}
	})

	t.Run("file handler", func(t *testing.T) {
		tool := &mockFileHandler{mockPluginTool: mockPluginTool{name: "test"}}
		if !PluginSupportsFiles(tool) {
			t.Error("PluginSupportsFiles() should return true for file handler")
		}
	})
}

func TestSetAgentContext(t *testing.T) {
	t.Run("non-agent-aware tool", func(t *testing.T) {
		tool := &mockPluginTool{name: "test"}
		// Should not panic
		SetAgentContext(tool, "agent1", "agents/agent1/config.json", "/home/user")
	})

	t.Run("agent-aware tool", func(t *testing.T) {
		tool := &mockAgentAwareTool{mockPluginTool: mockPluginTool{name: "test"}}
		SetAgentContext(tool, "agent1", "agents/agent1/config.json", "/home/user")

		if tool.context == nil {
			t.Fatal("SetAgentContext() should set context on agent-aware tool")
		}
		if tool.context.Name != "agent1" {
			t.Errorf("context.Name = %q, want %q", tool.context.Name, "agent1")
		}
		if tool.context.AgentDir != "agents/agent1" {
			t.Errorf("context.AgentDir = %q, want %q", tool.context.AgentDir, "agents/agent1")
		}
		if tool.context.CurrentLocation != "/home/user" {
			t.Errorf("context.CurrentLocation = %q, want %q", tool.context.CurrentLocation, "/home/user")
		}
	})
}

func TestLoadPluginUnified_FileNotFound(t *testing.T) {
	_, err := LoadPluginUnified("/nonexistent/path/to/plugin")
	if err == nil {
		t.Error("LoadPluginUnified() should return error for non-existent file")
	}
}

func TestMapConfigTypeToFrontendType(t *testing.T) {
	tests := []struct {
		input    pluginapi.ConfigVariableType
		expected string
	}{
		{pluginapi.ConfigTypeString, "string"},
		{pluginapi.ConfigTypeInt, "int"},
		{pluginapi.ConfigTypeFloat, "float"},
		{pluginapi.ConfigTypeBool, "bool"},
		{pluginapi.ConfigTypeFilePath, "filepath"},
		{pluginapi.ConfigTypeDirPath, "filepath"},
		{pluginapi.ConfigTypePassword, "password"},
		{pluginapi.ConfigTypeURL, "string"},
		{pluginapi.ConfigTypeEmail, "string"},
		{pluginapi.ConfigVariableType("unknown"), "string"}, // Unknown type
	}

	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			result := mapConfigTypeToFrontendType(tt.input)
			if result != tt.expected {
				t.Errorf("mapConfigTypeToFrontendType(%v) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestCloseRPCPlugin(t *testing.T) {
	t.Run("non-RPC plugin", func(t *testing.T) {
		tool := &mockPluginTool{name: "test"}
		// Should not panic
		CloseRPCPlugin(tool)
	})
}

func TestPluginDiscoveryPaths(t *testing.T) {
	// Test that common plugin directories exist or can be created
	paths := []string{
		"plugins",
		"uploaded_plugins",
	}

	// Get the project root (go up from internal/pluginloader)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}

	// Find project root by looking for go.mod
	projectRoot := cwd
	for {
		if _, err := os.Stat(filepath.Join(projectRoot, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(projectRoot)
		if parent == projectRoot {
			t.Skip("Could not find project root")
			return
		}
		projectRoot = parent
	}

	for _, path := range paths {
		fullPath := filepath.Join(projectRoot, path)
		info, err := os.Stat(fullPath)
		if os.IsNotExist(err) {
			t.Logf("Plugin directory %q does not exist (may be expected)", path)
			continue
		}
		if err != nil {
			t.Errorf("Error checking path %q: %v", path, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("Path %q exists but is not a directory", path)
		}
	}
}
