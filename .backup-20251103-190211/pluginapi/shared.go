package pluginapi

import "github.com/hashicorp/go-plugin"

// Handshake is a shared configuration that's used to verify that both
// the host and plugin are talking the same protocol.
var Handshake = plugin.HandshakeConfig{
	// This isn't required when using VersionedPlugins
	ProtocolVersion: 1,

	// MagicCookieKey and value are used as a basic verification
	// that a plugin is intended to be launched. This is not a
	// security feature, just a UX feature. If the magic cookie
	// doesn't match, we show a friendly message to the user.
	MagicCookieKey:   "ORI_PLUGIN",
	MagicCookieValue: "ori-agent-v1",
}

// PluginMap is the map of plugin name to implementation.
// This is used by both the host and plugin.
var PluginMap = map[string]plugin.Plugin{
	"tool": &ToolRPCPlugin{},
}
