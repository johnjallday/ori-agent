package pluginapi

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/go-plugin"
	"github.com/openai/openai-go/v2"
	"google.golang.org/grpc"
)

// ToolRPCPlugin is the implementation of plugin.Plugin so we can serve/consume this
type ToolRPCPlugin struct {
	plugin.Plugin
	// Impl is the concrete implementation (only set for plugin-side)
	Impl Tool
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
	Impl Tool
}

func (s *grpcServer) GetDefinition(ctx context.Context, _ *Empty) (*FunctionDefinition, error) {
	def := s.Impl.Definition()

	// Convert OpenAI definition to protobuf message
	paramsJSON, err := json.Marshal(def.Parameters)
	if err != nil {
		return nil, err
	}

	// Extract description from param.Opt[string]
	desc := ""
	if def.Description.Valid() {
		desc = def.Description.Value
	}

	return &FunctionDefinition{
		Name:           def.Name,
		Description:    desc,
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

// grpcClient is a local wrapper for the client implementation
type grpcClient struct {
	client ToolServiceClient
}

func (c *grpcClient) Definition() openai.FunctionDefinitionParam {
	resp, err := c.client.GetDefinition(context.Background(), &Empty{})
	if err != nil {
		return openai.FunctionDefinitionParam{}
	}

	var params openai.FunctionParameters
	if err := json.Unmarshal([]byte(resp.ParametersJson), &params); err != nil {
		params = openai.FunctionParameters{}
	}

	return openai.FunctionDefinitionParam{
		Name:        resp.Name,
		Description: openai.String(resp.Description),
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
	c.client.SetAgentContext(context.Background(), &AgentContextRequest{
		Name:         ctx.Name,
		ConfigPath:   ctx.ConfigPath,
		SettingsPath: ctx.SettingsPath,
		AgentDir:     ctx.AgentDir,
	})
}

// Compile-time interface checks
var (
	_ Tool                    = (*grpcClient)(nil)
	_ VersionedTool           = (*grpcClient)(nil)
	_ DefaultSettingsProvider = (*grpcClient)(nil)
	_ AgentAwareTool          = (*grpcClient)(nil)
)
