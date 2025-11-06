package pluginrpc

import (
	"context"
	"encoding/json"

	"github.com/johnjallday/ori-agent/pluginapi"
)

// GRPCServer implements the gRPC server for plugins
type GRPCServer struct {
	pluginapi.UnimplementedToolServiceServer
	Impl pluginapi.Tool
}

func (s *GRPCServer) GetDefinition(ctx context.Context, _ *pluginapi.Empty) (*pluginapi.FunctionDefinition, error) {
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

	return &pluginapi.FunctionDefinition{
		Name:           def.Name,
		Description:    desc,
		ParametersJson: string(paramsJSON),
	}, nil
}

func (s *GRPCServer) Call(ctx context.Context, req *pluginapi.CallRequest) (*pluginapi.CallResponse, error) {
	result, err := s.Impl.Call(ctx, req.ArgsJson)
	if err != nil {
		return &pluginapi.CallResponse{Error: err.Error()}, nil
	}
	return &pluginapi.CallResponse{ResultJson: result}, nil
}

func (s *GRPCServer) GetVersion(ctx context.Context, _ *pluginapi.Empty) (*pluginapi.VersionResponse, error) {
	if versionedTool, ok := s.Impl.(pluginapi.VersionedTool); ok {
		return &pluginapi.VersionResponse{Version: versionedTool.Version()}, nil
	}
	return &pluginapi.VersionResponse{Version: "unknown"}, nil
}

func (s *GRPCServer) SetAgentContext(ctx context.Context, req *pluginapi.AgentContextRequest) (*pluginapi.Empty, error) {
	if agentAware, ok := s.Impl.(pluginapi.AgentAwareTool); ok {
		agentAware.SetAgentContext(pluginapi.AgentContext{
			Name:         req.Name,
			ConfigPath:   req.ConfigPath,
			SettingsPath: req.SettingsPath,
			AgentDir:     req.AgentDir,
		})
	}
	return &pluginapi.Empty{}, nil
}
