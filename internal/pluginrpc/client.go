package pluginrpc

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/johnjallday/ori-agent/pluginapi"
	"github.com/openai/openai-go/v2"
)

// GRPCClient implements pluginapi.Tool via RPC
type GRPCClient struct {
	client pluginapi.ToolServiceClient
}

func (c *GRPCClient) Definition() openai.FunctionDefinitionParam {
	resp, err := c.client.GetDefinition(context.Background(), &pluginapi.Empty{})
	if err != nil {
		// Return empty definition on error
		return openai.FunctionDefinitionParam{}
	}

	var params openai.FunctionParameters
	if err := json.Unmarshal([]byte(resp.ParametersJson), &params); err != nil {
		// Return empty params on unmarshal error
		params = openai.FunctionParameters{}
	}

	return openai.FunctionDefinitionParam{
		Name:        resp.Name,
		Description: openai.String(resp.Description),
		Parameters:  params,
	}
}

func (c *GRPCClient) Call(ctx context.Context, args string) (string, error) {
	resp, err := c.client.Call(ctx, &pluginapi.CallRequest{ArgsJson: args})
	if err != nil {
		return "", err
	}
	if resp.Error != "" {
		return "", fmt.Errorf("%s", resp.Error)
	}
	return resp.ResultJson, nil
}

// Version returns the plugin version (implements VersionedTool)
func (c *GRPCClient) Version() string {
	resp, err := c.client.GetVersion(context.Background(), &pluginapi.Empty{})
	if err != nil {
		return "unknown"
	}
	return resp.Version
}

// SetAgentContext provides agent information to the plugin (implements AgentAwareTool)
func (c *GRPCClient) SetAgentContext(ctx pluginapi.AgentContext) {
	c.client.SetAgentContext(context.Background(), &pluginapi.AgentContextRequest{
		Name:         ctx.Name,
		ConfigPath:   ctx.ConfigPath,
		SettingsPath: ctx.SettingsPath,
		AgentDir:     ctx.AgentDir,
	})
}
