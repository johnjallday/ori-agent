package pluginapi

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
)

// ToolRPCPlugin is the implementation of plugin.Plugin so we can serve/consume this
type ToolRPCPlugin struct {
	plugin.Plugin
	// Impl is the concrete implementation (only set for plugin-side)
	Impl PluginTool
}

// GRPCServer registers this plugin for serving over gRPC
func (p *ToolRPCPlugin) GRPCServer(broker *plugin.GRPCBroker, s *grpc.Server) error {
	// The actual server implementation is in internal/pluginrpc package
	// This will be imported by plugins that use this
	RegisterToolServiceServer(s, &grpcServer{Impl: p.Impl})
	return nil
}

// GRPCClient returns the client implementation
func (p *ToolRPCPlugin) GRPCClient(ctx context.Context, broker *plugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) {
	return &grpcClient{client: NewToolServiceClient(c)}, nil
}

// grpcServer is a local wrapper for the server implementation
type grpcServer struct {
	UnimplementedToolServiceServer
	Impl PluginTool
}

func (s *grpcServer) GetDefinition(ctx context.Context, _ *Empty) (*ToolDefinition, error) {
	def := s.Impl.Definition()

	// Convert generic Tool definition to protobuf message
	paramsJSON, err := json.Marshal(def.Parameters)
	if err != nil {
		return nil, err
	}

	return &ToolDefinition{
		Name:           def.Name,
		Description:    def.Description,
		ParametersJson: string(paramsJSON),
	}, nil
}

func (s *grpcServer) Call(ctx context.Context, req *CallRequest) (*CallResponse, error) {
	result, err := s.Impl.Call(ctx, req.ArgsJson)
	if err != nil {
		return &CallResponse{Error: err.Error()}, nil
	}
	return &CallResponse{ResultJson: result}, nil
}

func (s *grpcServer) GetVersion(ctx context.Context, _ *Empty) (*VersionResponse, error) {
	if versionedTool, ok := s.Impl.(VersionedTool); ok {
		return &VersionResponse{Version: versionedTool.Version()}, nil
	}
	return &VersionResponse{Version: "unknown"}, nil
}

func (s *grpcServer) SetAgentContext(ctx context.Context, req *AgentContextRequest) (*Empty, error) {
	if agentAware, ok := s.Impl.(AgentAwareTool); ok {
		agentAware.SetAgentContext(AgentContext{
			Name:         req.Name,
			ConfigPath:   req.ConfigPath,
			SettingsPath: req.SettingsPath,
			AgentDir:     req.AgentDir,
		})
	}
	return &Empty{}, nil
}

func (s *grpcServer) GetDefaultSettings(ctx context.Context, _ *Empty) (*SettingsResponse, error) {
	// Check if plugin implements DefaultSettingsProvider
	if settingsProvider, ok := s.Impl.(DefaultSettingsProvider); ok {
		settings, err := settingsProvider.GetDefaultSettings()
		if err != nil {
			return &SettingsResponse{Error: err.Error()}, nil
		}
		return &SettingsResponse{SettingsJson: settings}, nil
	}
	// Plugin doesn't implement settings, return empty
	return &SettingsResponse{}, nil
}

func (s *grpcServer) GetRequiredConfig(ctx context.Context, _ *Empty) (*ConfigVariablesResponse, error) {
	if initProvider, ok := s.Impl.(InitializationProvider); ok {
		configVars := initProvider.GetRequiredConfig()

		// Convert ConfigVariable to protobuf message
		protoVars := make([]*ProtoConfigVariable, len(configVars))
		for i, cv := range configVars {
			defaultValJSON, _ := json.Marshal(cv.DefaultValue)
			protoVars[i] = &ProtoConfigVariable{
				Key:              cv.Key,
				Name:             cv.Name,
				Description:      cv.Description,
				Type:             string(cv.Type),
				Required:         cv.Required,
				DefaultValueJson: string(defaultValJSON),
				Validation:       cv.Validation,
				Options:          cv.Options,
				Placeholder:      cv.Placeholder,
			}
		}

		return &ConfigVariablesResponse{ConfigVars: protoVars}, nil
	}
	// Plugin doesn't implement InitializationProvider
	return &ConfigVariablesResponse{}, nil
}

func (s *grpcServer) ValidateConfig(ctx context.Context, req *ValidateConfigRequest) (*ConfigResponse, error) {
	if initProvider, ok := s.Impl.(InitializationProvider); ok {
		var config map[string]interface{}
		if err := json.Unmarshal([]byte(req.ConfigJson), &config); err != nil {
			return &ConfigResponse{Success: false, Error: err.Error()}, nil
		}

		if err := initProvider.ValidateConfig(config); err != nil {
			return &ConfigResponse{Success: false, Error: err.Error()}, nil
		}

		return &ConfigResponse{Success: true}, nil
	}
	return &ConfigResponse{Success: false, Error: "plugin does not implement InitializationProvider"}, nil
}

func (s *grpcServer) InitializeWithConfig(ctx context.Context, req *InitializeConfigRequest) (*ConfigResponse, error) {
	if initProvider, ok := s.Impl.(InitializationProvider); ok {
		var config map[string]interface{}
		if err := json.Unmarshal([]byte(req.ConfigJson), &config); err != nil {
			return &ConfigResponse{Success: false, Error: err.Error()}, nil
		}

		if err := initProvider.InitializeWithConfig(config); err != nil {
			return &ConfigResponse{Success: false, Error: err.Error()}, nil
		}

		return &ConfigResponse{Success: true}, nil
	}
	return &ConfigResponse{Success: false, Error: "plugin does not implement InitializationProvider"}, nil
}

func (s *grpcServer) GetMetadata(ctx context.Context, _ *Empty) (*MetadataResponse, error) {
	// Check if plugin implements MetadataProvider
	if metadataProvider, ok := s.Impl.(MetadataProvider); ok {
		metadata, err := metadataProvider.GetMetadata()
		if err != nil {
			return &MetadataResponse{Error: err.Error()}, nil
		}

		if metadata != nil {
			metadata.Tags = metadataProvider.GetTags()
		}

		// metadata is already a *PluginMetadata from the proto
		return &MetadataResponse{Metadata: metadata}, nil
	}
	// Plugin doesn't implement MetadataProvider, return empty
	return &MetadataResponse{}, nil
}

func (s *grpcServer) GetCompatibilityInfo(ctx context.Context, _ *Empty) (*CompatibilityInfoResponse, error) {
	// Check if plugin implements PluginCompatibility
	if compatTool, ok := s.Impl.(PluginCompatibility); ok {
		return &CompatibilityInfoResponse{
			MinAgentVersion: compatTool.MinAgentVersion(),
			MaxAgentVersion: compatTool.MaxAgentVersion(),
			ApiVersion:      compatTool.APIVersion(),
		}, nil
	}
	// Plugin doesn't implement PluginCompatibility, return empty
	return &CompatibilityInfoResponse{}, nil
}

// grpcClient is a local wrapper for the client implementation
type grpcClient struct {
	client ToolServiceClient
}

func (c *grpcClient) Definition() Tool {
	resp, err := c.client.GetDefinition(context.Background(), &Empty{})
	if err != nil {
		return Tool{}
	}

	var params map[string]interface{}
	if err := json.Unmarshal([]byte(resp.ParametersJson), &params); err != nil {
		params = map[string]interface{}{}
	}

	return Tool{
		Name:        resp.Name,
		Description: resp.Description,
		Parameters:  params,
	}
}

func (c *grpcClient) Call(ctx context.Context, args string) (string, error) {
	resp, err := c.client.Call(ctx, &CallRequest{ArgsJson: args})
	if err != nil {
		return "", err
	}
	if err := resp.Error; err != "" {
		return "", fmt.Errorf("%s", err)
	}
	return resp.ResultJson, nil
}

func (c *grpcClient) Version() string {
	resp, err := c.client.GetVersion(context.Background(), &Empty{})
	if err != nil {
		return "unknown"
	}
	return resp.Version
}

func (c *grpcClient) GetDefaultSettings() (string, error) {
	resp, err := c.client.GetDefaultSettings(context.Background(), &Empty{})
	if err != nil {
		return "", err
	}
	if resp.Error != "" {
		return "", fmt.Errorf("%s", resp.Error)
	}
	return resp.SettingsJson, nil
}

func (c *grpcClient) SetAgentContext(ctx AgentContext) {
	_, _ = c.client.SetAgentContext(context.Background(), &AgentContextRequest{
		Name:         ctx.Name,
		ConfigPath:   ctx.ConfigPath,
		SettingsPath: ctx.SettingsPath,
		AgentDir:     ctx.AgentDir,
	})
}

func (c *grpcClient) GetRequiredConfig() []ConfigVariable {
	resp, err := c.client.GetRequiredConfig(context.Background(), &Empty{})
	if err != nil || resp == nil {
		return []ConfigVariable{}
	}

	// Convert protobuf ProtoConfigVariable to pluginapi.ConfigVariable
	configVars := make([]ConfigVariable, len(resp.ConfigVars))
	for i, protoVar := range resp.ConfigVars {
		var defaultValue interface{}
		if protoVar.DefaultValueJson != "" {
			_ = json.Unmarshal([]byte(protoVar.DefaultValueJson), &defaultValue) // Use zero value on error
		}

		configVars[i] = ConfigVariable{
			Key:          protoVar.Key,
			Name:         protoVar.Name,
			Description:  protoVar.Description,
			Type:         ConfigVariableType(protoVar.Type),
			Required:     protoVar.Required,
			DefaultValue: defaultValue,
			Validation:   protoVar.Validation,
			Options:      protoVar.Options,
			Placeholder:  protoVar.Placeholder,
		}
	}

	return configVars
}

func (c *grpcClient) ValidateConfig(config map[string]interface{}) error {
	configJSON, err := json.Marshal(config)
	if err != nil {
		return err
	}

	resp, err := c.client.ValidateConfig(context.Background(), &ValidateConfigRequest{
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

func (c *grpcClient) InitializeWithConfig(config map[string]interface{}) error {
	configJSON, err := json.Marshal(config)
	if err != nil {
		return err
	}

	resp, err := c.client.InitializeWithConfig(context.Background(), &InitializeConfigRequest{
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

func (c *grpcClient) GetMetadata() (*PluginMetadata, error) {
	resp, err := c.client.GetMetadata(context.Background(), &Empty{})
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("%s", resp.Error)
	}

	// Return the proto-generated metadata directly
	return resp.Metadata, nil
}

func (c *grpcClient) GetTags() []string {
	metadata, err := c.GetMetadata()
	if err != nil || metadata == nil {
		return nil
	}
	return metadata.Tags
}

func (c *grpcClient) MinAgentVersion() string {
	resp, err := c.client.GetCompatibilityInfo(context.Background(), &Empty{})
	if err != nil {
		return ""
	}
	return resp.MinAgentVersion
}

func (c *grpcClient) MaxAgentVersion() string {
	resp, err := c.client.GetCompatibilityInfo(context.Background(), &Empty{})
	if err != nil {
		return ""
	}
	return resp.MaxAgentVersion
}

func (c *grpcClient) APIVersion() string {
	resp, err := c.client.GetCompatibilityInfo(context.Background(), &Empty{})
	if err != nil {
		return ""
	}
	return resp.ApiVersion
}

func (s *grpcServer) GetWebPages(ctx context.Context, _ *Empty) (*WebPagesResponse, error) {
	if webProvider, ok := s.Impl.(WebPageProvider); ok {
		pages := webProvider.GetWebPages()
		return &WebPagesResponse{Pages: pages}, nil
	}
	return &WebPagesResponse{}, nil
}

func (s *grpcServer) ServeWebPage(ctx context.Context, req *WebPageRequest) (*WebPageResponse, error) {
	if webProvider, ok := s.Impl.(WebPageProvider); ok {
		content, contentType, err := webProvider.ServeWebPage(req.Path, req.Query)
		if err != nil {
			return &WebPageResponse{Error: err.Error()}, nil
		}
		return &WebPageResponse{
			Content:     content,
			ContentType: contentType,
		}, nil
	}
	return &WebPageResponse{Error: "plugin does not implement WebPageProvider"}, nil
}

func (c *grpcClient) GetWebPages() []string {
	resp, err := c.client.GetWebPages(context.Background(), &Empty{})
	if err != nil || resp == nil {
		return []string{}
	}
	return resp.Pages
}

func (c *grpcClient) ServeWebPage(path string, query map[string]string) (string, string, error) {
	resp, err := c.client.ServeWebPage(context.Background(), &WebPageRequest{
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

// =============================================================================
// Operations Provider Support - Server Side
// =============================================================================

func (s *grpcServer) GetOperations(ctx context.Context, _ *Empty) (*OperationsResponse, error) {
	// Check if plugin implements OperationsProvider
	if opsProvider, ok := s.Impl.(OperationsProvider); ok {
		operations := opsProvider.GetOperations()
		if operations == nil {
			return &OperationsResponse{SupportsOperations: false}, nil
		}

		// Convert OperationInfo to proto
		protoOps := make([]*ProtoOperationInfo, len(operations))
		for i, op := range operations {
			protoOps[i] = &ProtoOperationInfo{
				Name:               op.Name,
				Parameters:         op.Parameters,
				RequiredParameters: op.RequiredParameters,
			}
		}

		return &OperationsResponse{
			Operations:         protoOps,
			SupportsOperations: true,
		}, nil
	}
	// Plugin doesn't implement OperationsProvider
	return &OperationsResponse{SupportsOperations: false}, nil
}

// =============================================================================
// File Attachment Support - Server Side
// =============================================================================

func (s *grpcServer) AcceptsFiles(ctx context.Context, _ *Empty) (*AcceptsFilesResponse, error) {
	// Check if plugin implements FileAttachmentHandler
	if fileHandler, ok := s.Impl.(FileAttachmentHandler); ok {
		acceptedTypes := fileHandler.AcceptsFiles()
		return &AcceptsFilesResponse{
			AcceptedTypes: acceptedTypes,
			SupportsFiles: true,
		}, nil
	}
	// Plugin doesn't implement FileAttachmentHandler
	return &AcceptsFilesResponse{
		AcceptedTypes: nil,
		SupportsFiles: false,
	}, nil
}

func (s *grpcServer) CallWithFiles(ctx context.Context, req *CallWithFilesRequest) (*CallResponse, error) {
	// Check if plugin implements FileAttachmentHandler
	if fileHandler, ok := s.Impl.(FileAttachmentHandler); ok {
		// Convert proto ProtoFileAttachment to pluginapi FileAttachment
		files := make([]FileAttachment, len(req.Files))
		for i, pf := range req.Files {
			files[i] = FileAttachment{
				Name:    pf.Name,
				Type:    pf.Type,
				Size:    pf.Size,
				Content: pf.Content,
			}
		}

		result, err := fileHandler.CallWithFiles(ctx, req.ArgsJson, files)
		if err != nil {
			return &CallResponse{Error: err.Error()}, nil
		}
		return &CallResponse{ResultJson: result}, nil
	}

	// Fallback to regular Call if plugin doesn't support files
	result, err := s.Impl.Call(ctx, req.ArgsJson)
	if err != nil {
		return &CallResponse{Error: err.Error()}, nil
	}
	return &CallResponse{ResultJson: result}, nil
}

// =============================================================================
// File Attachment Support - Client Side
// =============================================================================

// AcceptsFiles returns the list of file types this plugin accepts.
// Returns nil and false if the plugin doesn't implement FileAttachmentHandler.
func (c *grpcClient) AcceptsFiles() []string {
	resp, err := c.client.AcceptsFiles(context.Background(), &Empty{})
	if err != nil || resp == nil || !resp.SupportsFiles {
		return nil
	}
	return resp.AcceptedTypes
}

// SupportsFiles returns true if the plugin implements FileAttachmentHandler.
func (c *grpcClient) SupportsFiles() bool {
	resp, err := c.client.AcceptsFiles(context.Background(), &Empty{})
	if err != nil || resp == nil {
		return false
	}
	return resp.SupportsFiles
}

// CallWithFiles executes the tool with arguments and file attachments.
// If the plugin doesn't support files, it falls back to regular Call.
func (c *grpcClient) CallWithFiles(ctx context.Context, args string, files []FileAttachment) (string, error) {
	// Convert pluginapi FileAttachment to proto ProtoFileAttachment
	protoFiles := make([]*ProtoFileAttachment, len(files))
	for i, f := range files {
		protoFiles[i] = &ProtoFileAttachment{
			Name:    f.Name,
			Type:    f.Type,
			Size:    f.Size,
			Content: f.Content,
		}
	}

	resp, err := c.client.CallWithFiles(ctx, &CallWithFilesRequest{
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

// =============================================================================
// Operations Provider Support - Client Side
// =============================================================================

// GetOperations returns operation-specific parameter information.
// Returns nil if the plugin doesn't implement OperationsProvider.
func (c *grpcClient) GetOperations() []OperationInfo {
	resp, err := c.client.GetOperations(context.Background(), &Empty{})
	if err != nil || resp == nil || !resp.SupportsOperations {
		return nil
	}

	// Convert proto to OperationInfo
	operations := make([]OperationInfo, len(resp.Operations))
	for i, op := range resp.Operations {
		operations[i] = OperationInfo{
			Name:               op.Name,
			Parameters:         op.Parameters,
			RequiredParameters: op.RequiredParameters,
		}
	}

	return operations
}

// Compile-time interface checks
var (
	_ PluginTool              = (*grpcClient)(nil)
	_ VersionedTool           = (*grpcClient)(nil)
	_ PluginCompatibility     = (*grpcClient)(nil)
	_ MetadataProvider        = (*grpcClient)(nil)
	_ DefaultSettingsProvider = (*grpcClient)(nil)
	_ AgentAwareTool          = (*grpcClient)(nil)
	_ InitializationProvider  = (*grpcClient)(nil)
	_ WebPageProvider         = (*grpcClient)(nil)
	_ FileAttachmentHandler   = (*grpcClient)(nil)
	_ OperationsProvider      = (*grpcClient)(nil)
)
