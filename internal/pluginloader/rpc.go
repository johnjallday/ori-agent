package pluginloader

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"github.com/johnjallday/ori-agent/pluginapi"
	"github.com/openai/openai-go/v2"
)

// RPCPluginClient wraps a plugin client connection and implements pluginapi.Tool
type RPCPluginClient struct {
	client *plugin.Client
	tool   pluginapi.Tool
}

// LoadPluginRPC loads a plugin executable via go-plugin RPC
// path should be the path to the plugin executable
func LoadPluginRPC(path string) (*RPCPluginClient, error) {
	// Ensure path is absolute
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Create a logger that discards all output to suppress debug messages
	silentLogger := hclog.New(&hclog.LoggerOptions{
		Output: io.Discard,
		Level:  hclog.Off,
	})

	// Create the client configuration
	clientConfig := &plugin.ClientConfig{
		HandshakeConfig: pluginapi.Handshake,
		Plugins:         pluginapi.PluginMap,
		Cmd:             exec.Command(absPath),
		AllowedProtocols: []plugin.Protocol{
			plugin.ProtocolGRPC,
		},
		Logger: silentLogger,
	}

	// Create the client
	client := plugin.NewClient(clientConfig)

	// Connect via RPC
	rpcClient, err := client.Client()
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("failed to create RPC client: %w", err)
	}

	// Request the plugin
	raw, err := rpcClient.Dispense("tool")
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("failed to dispense plugin: %w", err)
	}

	// Cast to Tool interface
	tool, ok := raw.(pluginapi.Tool)
	if !ok {
		client.Kill()
		return nil, fmt.Errorf("plugin does not implement Tool interface")
	}

	return &RPCPluginClient{
		client: client,
		tool:   tool,
	}, nil
}

// Definition implements pluginapi.Tool
func (r *RPCPluginClient) Definition() openai.FunctionDefinitionParam {
	return r.tool.Definition()
}

// Call implements pluginapi.Tool
func (r *RPCPluginClient) Call(ctx context.Context, args string) (string, error) {
	return r.tool.Call(ctx, args)
}

// Version returns the plugin version if it implements VersionedTool
func (r *RPCPluginClient) Version() string {
	if versionedTool, ok := r.tool.(pluginapi.VersionedTool); ok {
		return versionedTool.Version()
	}
	return ""
}

// MinAgentVersion returns the minimum agent version if the plugin implements PluginCompatibility
func (r *RPCPluginClient) MinAgentVersion() string {
	if metadata, ok := r.tool.(pluginapi.PluginCompatibility); ok {
		return metadata.MinAgentVersion()
	}
	return ""
}

// MaxAgentVersion returns the maximum agent version if the plugin implements PluginCompatibility
func (r *RPCPluginClient) MaxAgentVersion() string {
	if metadata, ok := r.tool.(pluginapi.PluginCompatibility); ok {
		return metadata.MaxAgentVersion()
	}
	return ""
}

// APIVersion returns the plugin API version if the plugin implements PluginCompatibility
func (r *RPCPluginClient) APIVersion() string {
	if metadata, ok := r.tool.(pluginapi.PluginCompatibility); ok {
		return metadata.APIVersion()
	}
	return ""
}

// HealthCheck runs a health check if the plugin implements HealthCheckProvider
func (r *RPCPluginClient) HealthCheck() error {
	if healthProvider, ok := r.tool.(pluginapi.HealthCheckProvider); ok {
		return healthProvider.HealthCheck()
	}
	return nil // No custom health check provided
}

// SetAgentContext sets agent context if the plugin implements AgentAwareTool
func (r *RPCPluginClient) SetAgentContext(ctx pluginapi.AgentContext) {
	if agentAware, ok := r.tool.(pluginapi.AgentAwareTool); ok {
		agentAware.SetAgentContext(ctx)
	}
}

// GetDefaultSettings returns default settings if the plugin implements DefaultSettingsProvider
func (r *RPCPluginClient) GetDefaultSettings() (string, error) {
	if settingsProvider, ok := r.tool.(pluginapi.DefaultSettingsProvider); ok {
		return settingsProvider.GetDefaultSettings()
	}
	return "", fmt.Errorf("plugin does not support default settings")
}

// GetRequiredConfig returns required config if the plugin implements InitializationProvider
func (r *RPCPluginClient) GetRequiredConfig() []pluginapi.ConfigVariable {
	if initProvider, ok := r.tool.(pluginapi.InitializationProvider); ok {
		return initProvider.GetRequiredConfig()
	}
	return []pluginapi.ConfigVariable{}
}

// ValidateConfig validates config if the plugin implements InitializationProvider
func (r *RPCPluginClient) ValidateConfig(config map[string]interface{}) error {
	if initProvider, ok := r.tool.(pluginapi.InitializationProvider); ok {
		return initProvider.ValidateConfig(config)
	}
	return fmt.Errorf("plugin does not support initialization")
}

// InitializeWithConfig initializes the plugin if it implements InitializationProvider
func (r *RPCPluginClient) InitializeWithConfig(config map[string]interface{}) error {
	if initProvider, ok := r.tool.(pluginapi.InitializationProvider); ok {
		return initProvider.InitializeWithConfig(config)
	}
	return fmt.Errorf("plugin does not support initialization")
}

// GetMetadata returns plugin metadata if the plugin implements MetadataProvider
func (r *RPCPluginClient) GetMetadata() (*pluginapi.PluginMetadata, error) {
	if metadataProvider, ok := r.tool.(pluginapi.MetadataProvider); ok {
		return metadataProvider.GetMetadata()
	}
	return nil, nil // Plugin doesn't provide metadata
}

// GetWebPages returns available web pages if the plugin implements WebPageProvider
func (r *RPCPluginClient) GetWebPages() []string {
	if webProvider, ok := r.tool.(pluginapi.WebPageProvider); ok {
		return webProvider.GetWebPages()
	}
	return []string{} // Plugin doesn't provide web pages
}

// ServeWebPage serves a web page if the plugin implements WebPageProvider
func (r *RPCPluginClient) ServeWebPage(path string, query map[string]string) (string, string, error) {
	if webProvider, ok := r.tool.(pluginapi.WebPageProvider); ok {
		return webProvider.ServeWebPage(path, query)
	}
	return "", "", nil // Plugin doesn't provide web pages
}

// Kill terminates the plugin process
func (r *RPCPluginClient) Kill() {
	if r.client != nil {
		r.client.Kill()
	}
}
