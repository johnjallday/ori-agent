package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/johnjallday/ori-agent/pluginapi"
)

func main() {
	// Read the embedded plugin.yaml from math plugin source
	yamlContent, err := os.ReadFile("example_plugins/math/plugin.yaml")
	if err != nil {
		log.Fatalf("Failed to read plugin.yaml: %v", err)
	}

	fmt.Println("=== YAML Content ===")
	fmt.Println(string(yamlContent))
	fmt.Println()

	// Parse it
	config := pluginapi.ReadPluginConfig(string(yamlContent))

	fmt.Println("=== Parsed Config ===")
	configJSON, _ := json.MarshalIndent(config, "", "  ")
	fmt.Println(string(configJSON))
	fmt.Println()

	// Check if tool definition exists
	if config.Tool == nil {
		fmt.Println("❌ ERROR: config.Tool is NIL!")
		os.Exit(1)
	}

	fmt.Println("=== Tool Definition (YAML) ===")
	toolJSON, _ := json.MarshalIndent(config.Tool, "", "  ")
	fmt.Println(string(toolJSON))
	fmt.Println()

	// Set tool name from plugin name if not specified
	if config.Tool.Name == "" {
		config.Tool.Name = config.Name
	}

	// Convert to Tool
	tool, err := config.Tool.ToToolDefinition()
	if err != nil {
		fmt.Printf("❌ ERROR converting to Tool: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=== Converted Tool ===")
	toolDefJSON, _ := json.MarshalIndent(tool, "", "  ")
	fmt.Println(string(toolDefJSON))
	fmt.Println()

	if len(tool.Parameters) == 0 {
		fmt.Println("❌ ERROR: Parameters map is EMPTY after conversion!")
		os.Exit(1)
	}

	fmt.Println("✅ SUCCESS: Parameters properly converted!")
}
