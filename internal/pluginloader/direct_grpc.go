package pluginloader

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	ori_pluginapi "github.com/oriagent/ori-pluginapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// DirectGRPCPluginClient wraps a direct gRPC client connection and plugin process.
// It implements pluginapi.PluginTool and optional interfaces by delegating to gRPC calls.
type DirectGRPCPluginClient struct {
	cmd    *exec.Cmd
	conn   *grpc.ClientConn
	client ori_pluginapi.ToolServiceClient
}

// LoadPluginDirectGRPC starts a plugin executable and connects via direct gRPC.
func LoadPluginDirectGRPC(path string) (*DirectGRPCPluginClient, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	port, err := pickFreePort()
	if err != nil {
		return nil, fmt.Errorf("failed to pick free port: %w", err)
	}

	cmd := exec.Command(absPath)
	cmd.Env = append(os.Environ(), fmt.Sprintf("ORI_PLUGIN_GRPC_PORT=%d", port))

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start plugin process: %w", err)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := dialPluginGRPC(addr, 8*time.Second)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}

	return &DirectGRPCPluginClient{
		cmd:    cmd,
		conn:   conn,
		client: ori_pluginapi.NewToolServiceClient(conn),
	}, nil
}

// Kill terminates the plugin process and closes the gRPC connection.
func (c *DirectGRPCPluginClient) Kill() {
	if c.conn != nil {
		_ = c.conn.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
}

// Definition implements pluginapi.PluginTool.
func (c *DirectGRPCPluginClient) Definition() ori_pluginapi.Tool {
	resp, err := c.client.GetDefinition(context.Background(), &ori_pluginapi.Empty{})
	if err != nil {
		return ori_pluginapi.Tool{}
	}

	params := map[string]interface{}{}
	if err := json.Unmarshal([]byte(resp.ParametersJson), &params); err != nil {
		params = map[string]interface{}{}
	}

	return ori_pluginapi.Tool{
		Name:        resp.Name,
		Description: resp.Description,
		Parameters:  params,
	}
}

// Call implements pluginapi.PluginTool.
func (c *DirectGRPCPluginClient) Call(ctx context.Context, args string) (string, error) {
	resp, err := c.client.Call(ctx, &ori_pluginapi.CallRequest{ArgsJson: args})
	if err != nil {
		return "", err
	}
	if resp.Error != "" {
		return "", fmt.Errorf("%s", resp.Error)
	}
	return resp.ResultJson, nil
}

// Version implements pluginapi.VersionedTool.
func (c *DirectGRPCPluginClient) Version() string {
	resp, err := c.client.GetVersion(context.Background(), &ori_pluginapi.Empty{})
	if err != nil {
		return "unknown"
	}
	return resp.Version
}

// SetAgentContext implements pluginapi.AgentAwareTool.
func (c *DirectGRPCPluginClient) SetAgentContext(ctx ori_pluginapi.AgentContext) {
	_, _ = c.client.SetAgentContext(context.Background(), &ori_pluginapi.AgentContextRequest{
		Name:         ctx.Name,
		ConfigPath:   ctx.ConfigPath,
		SettingsPath: ctx.SettingsPath,
		AgentDir:     ctx.AgentDir,
	})
}

// GetDefaultSettings implements pluginapi.DefaultSettingsProvider.
func (c *DirectGRPCPluginClient) GetDefaultSettings() (string, error) {
	resp, err := c.client.GetDefaultSettings(context.Background(), &ori_pluginapi.Empty{})
	if err != nil {
		return "", err
	}
	if resp.Error != "" {
		return "", fmt.Errorf("%s", resp.Error)
	}
	return resp.SettingsJson, nil
}

// GetRequiredConfig implements pluginapi.InitializationProvider.
func (c *DirectGRPCPluginClient) GetRequiredConfig() []ori_pluginapi.ConfigVariable {
	resp, err := c.client.GetRequiredConfig(context.Background(), &ori_pluginapi.Empty{})
	if err != nil || resp == nil {
		return []ori_pluginapi.ConfigVariable{}
	}

	configVars := make([]ori_pluginapi.ConfigVariable, len(resp.ConfigVars))
	for i, protoVar := range resp.ConfigVars {
		var defaultValue interface{}
		if protoVar.DefaultValueJson != "" {
			_ = json.Unmarshal([]byte(protoVar.DefaultValueJson), &defaultValue)
		}

		configVars[i] = ori_pluginapi.ConfigVariable{
			Key:          protoVar.Key,
			Name:         protoVar.Name,
			Description:  protoVar.Description,
			Type:         ori_pluginapi.ConfigVariableType(protoVar.Type),
			Required:     protoVar.Required,
			DefaultValue: defaultValue,
			Validation:   protoVar.Validation,
			Options:      protoVar.Options,
			Placeholder:  protoVar.Placeholder,
		}
	}

	return configVars
}

// ValidateConfig implements pluginapi.InitializationProvider.
func (c *DirectGRPCPluginClient) ValidateConfig(config map[string]interface{}) error {
	configJSON, err := json.Marshal(config)
	if err != nil {
		return err
	}

	resp, err := c.client.ValidateConfig(context.Background(), &ori_pluginapi.ValidateConfigRequest{
		ConfigJson: string(configJSON),
	})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

// InitializeWithConfig implements pluginapi.InitializationProvider.
func (c *DirectGRPCPluginClient) InitializeWithConfig(config map[string]interface{}) error {
	configJSON, err := json.Marshal(config)
	if err != nil {
		return err
	}

	resp, err := c.client.InitializeWithConfig(context.Background(), &ori_pluginapi.InitializeConfigRequest{
		ConfigJson: string(configJSON),
	})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

// GetMetadata implements pluginapi.MetadataProvider.
func (c *DirectGRPCPluginClient) GetMetadata() (*ori_pluginapi.PluginMetadata, error) {
	resp, err := c.client.GetMetadata(context.Background(), &ori_pluginapi.Empty{})
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return resp.Metadata, nil
}

// GetTags implements pluginapi.MetadataProvider.
func (c *DirectGRPCPluginClient) GetTags() []string {
	metadata, err := c.GetMetadata()
	if err != nil || metadata == nil {
		return nil
	}
	return metadata.Tags
}

// MinAgentVersion implements pluginapi.PluginCompatibility.
func (c *DirectGRPCPluginClient) MinAgentVersion() string {
	resp, err := c.client.GetCompatibilityInfo(context.Background(), &ori_pluginapi.Empty{})
	if err != nil {
		return ""
	}
	return resp.MinAgentVersion
}

// MaxAgentVersion implements pluginapi.PluginCompatibility.
func (c *DirectGRPCPluginClient) MaxAgentVersion() string {
	resp, err := c.client.GetCompatibilityInfo(context.Background(), &ori_pluginapi.Empty{})
	if err != nil {
		return ""
	}
	return resp.MaxAgentVersion
}

// APIVersion implements pluginapi.PluginCompatibility.
func (c *DirectGRPCPluginClient) APIVersion() string {
	resp, err := c.client.GetCompatibilityInfo(context.Background(), &ori_pluginapi.Empty{})
	if err != nil {
		return ""
	}
	return resp.ApiVersion
}

// HealthCheck implements pluginapi.HealthCheckProvider.
func (c *DirectGRPCPluginClient) HealthCheck() error {
	_, err := c.client.GetVersion(context.Background(), &ori_pluginapi.Empty{})
	return err
}

// GetWebPages implements pluginapi.WebPageProvider.
func (c *DirectGRPCPluginClient) GetWebPages() []string {
	resp, err := c.client.GetWebPages(context.Background(), &ori_pluginapi.Empty{})
	if err != nil || resp == nil {
		return []string{}
	}
	return resp.Pages
}

// ServeWebPage implements pluginapi.WebPageProvider.
func (c *DirectGRPCPluginClient) ServeWebPage(path string, query map[string]string) (string, string, error) {
	resp, err := c.client.ServeWebPage(context.Background(), &ori_pluginapi.WebPageRequest{
		Path:  path,
		Query: query,
	})
	if err != nil {
		return "", "", err
	}
	if resp.Error != "" {
		return "", "", fmt.Errorf("%s", resp.Error)
	}
	return resp.Content, resp.ContentType, nil
}

// AcceptsFiles implements pluginapi.FileAttachmentHandler.
func (c *DirectGRPCPluginClient) AcceptsFiles() []string {
	resp, err := c.client.AcceptsFiles(context.Background(), &ori_pluginapi.Empty{})
	if err != nil || resp == nil || !resp.SupportsFiles {
		return nil
	}
	return resp.AcceptedTypes
}

// SupportsFiles implements pluginapi.FileAttachmentHandler.
func (c *DirectGRPCPluginClient) SupportsFiles() bool {
	resp, err := c.client.AcceptsFiles(context.Background(), &ori_pluginapi.Empty{})
	if err != nil || resp == nil {
		return false
	}
	return resp.SupportsFiles
}

// CallWithFiles implements pluginapi.FileAttachmentHandler.
func (c *DirectGRPCPluginClient) CallWithFiles(ctx context.Context, args string, files []ori_pluginapi.FileAttachment) (string, error) {
	protoFiles := make([]*ori_pluginapi.ProtoFileAttachment, len(files))
	for i, f := range files {
		protoFiles[i] = &ori_pluginapi.ProtoFileAttachment{
			Name:    f.Name,
			Type:    f.Type,
			Size:    f.Size,
			Content: f.Content,
		}
	}

	resp, err := c.client.CallWithFiles(ctx, &ori_pluginapi.CallWithFilesRequest{
		ArgsJson: args,
		Files:    protoFiles,
	})
	if err != nil {
		return "", err
	}
	if resp.Error != "" {
		return "", fmt.Errorf("%s", resp.Error)
	}
	return resp.ResultJson, nil
}

// GetOperations implements pluginapi.OperationsProvider.
func (c *DirectGRPCPluginClient) GetOperations() []ori_pluginapi.OperationInfo {
	resp, err := c.client.GetOperations(context.Background(), &ori_pluginapi.Empty{})
	if err != nil || resp == nil || !resp.SupportsOperations {
		return nil
	}

	operations := make([]ori_pluginapi.OperationInfo, len(resp.Operations))
	for i, op := range resp.Operations {
		operations[i] = ori_pluginapi.OperationInfo{
			Name:               op.Name,
			Parameters:         op.Parameters,
			RequiredParameters: op.RequiredParameters,
		}
	}
	return operations
}

func pickFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()

	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected listener address type %T", l.Addr())
	}
	return addr.Port, nil
}

func dialPluginGRPC(addr string, timeout time.Duration) (*grpc.ClientConn, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error

	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
		conn, err := grpc.DialContext(
			ctx,
			addr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
		)
		cancel()
		if err == nil {
			return conn, nil
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}

	return nil, fmt.Errorf("failed to connect to plugin gRPC at %s: %w", addr, lastErr)
}
