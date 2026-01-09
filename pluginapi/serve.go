package pluginapi

import (
	"fmt"
	"reflect"

	"github.com/hashicorp/go-plugin"
)

// ServePlugin is a helper function that dramatically simplifies plugin main() functions.
// It reads the plugin config from embedded YAML, initializes the BasePlugin, and starts serving.
//
// This function eliminates 30+ lines of boilerplate from every plugin's main() function,
// reducing it to a single line:
//
// Usage:
//
//	//go:embed plugin.yaml
//	var configYAML string
//
//	func main() {
//	    pluginapi.ServePlugin(&myTool{}, configYAML)
//	}
//
// The function automatically:
// - Parses plugin.yaml configuration
// - Creates and initializes BasePlugin with all metadata
// - Injects BasePlugin into your tool struct (via reflection)
// - Configures and starts the gRPC plugin server
//
// Requirements:
// - tool must be a pointer to a struct
// - tool must embed pluginapi.BasePlugin
// - configYAML must be a valid plugin.yaml string
func ServePlugin(tool PluginTool, configYAML string) {
	// Parse plugin config from embedded YAML
	config, err := readPluginConfig(configYAML)
	if err != nil {
		panic(fmt.Sprintf("ServePlugin failed to parse config: %v", err))
	}

	// Get API version from config, default to "v1"
	apiVersion := config.Requirements.ApiVersion
	if apiVersion == "" {
		apiVersion = "v1"
	}

	// Create base plugin with all metadata from config
	base := newBasePlugin(
		config.Name,
		config.Version,
		config.Requirements.MinOriVersion,
		config.Requirements.MaxOriVersion, // May be empty (no max limit)
		apiVersion,
	)

	// Set plugin config for YAML-based features
	base.SetPluginConfig(&config)

	// Set metadata from config
	if metadata, err := config.ToMetadata(); err == nil {
		base.SetMetadata(metadata)
	}

	// Use reflection to inject BasePlugin into the tool struct
	if err := injectBasePlugin(tool, &base); err != nil {
		panic(fmt.Sprintf("ServePlugin failed: %v", err))
	}

	// Start serving the plugin via gRPC
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: Handshake,
		Plugins: map[string]plugin.Plugin{
			"tool": &ToolRPCPlugin{Impl: tool},
		},
		GRPCServer: plugin.DefaultGRPCServer,
	})
}

// injectBasePlugin uses reflection to find and set the embedded BasePlugin field
func injectBasePlugin(tool PluginTool, base *BasePlugin) error {
	toolValue := reflect.ValueOf(tool)

	// Ensure tool is a pointer
	if toolValue.Kind() != reflect.Ptr {
		return fmt.Errorf("tool must be a pointer, got %T", tool)
	}

	// Get the struct value
	structValue := toolValue.Elem()
	if structValue.Kind() != reflect.Struct {
		return fmt.Errorf("tool must be a pointer to struct, got %T", tool)
	}

	// Find the embedded BasePlugin field
	structType := structValue.Type()
	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)

		// Check if this is an embedded BasePlugin field
		if field.Type == reflect.TypeOf(BasePlugin{}) && field.Anonymous {
			fieldValue := structValue.Field(i)

			if !fieldValue.CanSet() {
				return fmt.Errorf("cannot set BasePlugin field in %T (field is unexported)", tool)
			}

			// Set the BasePlugin field by copying pointer's element
			baseValue := reflect.ValueOf(base).Elem()
			fieldValue.Set(baseValue)
			return nil
		}
	}

	return fmt.Errorf("tool %T does not embed pluginapi.BasePlugin", tool)
}
