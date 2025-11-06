//go:build ignore

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/johnjallday/ori-agent/internal/pluginloader"
)

func main() {
	fmt.Println("Testing RPC Plugin Loading...")
	fmt.Println("====================================================")

	// Test loading the weather plugin executable
	weatherPath := "plugins/weather/weather"
	fmt.Printf("\n1. Loading weather plugin from: %s\n", weatherPath)

	tool, err := pluginloader.LoadPluginUnified(weatherPath)
	if err != nil {
		log.Fatalf("Failed to load plugin: %v", err)
	}

	// Check if it's an RPC plugin
	isRPC := pluginloader.IsRPCPlugin(tool)
	fmt.Printf("   Is RPC Plugin: %v\n", isRPC)

	// Get plugin definition
	def := tool.Definition()
	fmt.Printf("   Plugin Name: %s\n", def.Name)
	fmt.Printf("   Description: %s\n", def.Description.String())

	// Get version
	if versionedTool, ok := tool.(interface{ Version() string }); ok {
		fmt.Printf("   Version: %s\n", versionedTool.Version())
	}

	// Test calling the plugin
	fmt.Printf("\n2. Testing plugin call...\n")
	result, err := tool.Call(context.Background(), `{"location": "San Francisco"}`)
	if err != nil {
		log.Fatalf("Failed to call plugin: %v", err)
	}
	fmt.Printf("   Result: %s\n", result)

	// Clean up RPC plugin
	fmt.Printf("\n3. Cleaning up RPC plugin...\n")
	pluginloader.CloseRPCPlugin(tool)
	fmt.Printf("   Plugin process terminated\n")

	fmt.Println("\n====================================================")
	fmt.Println("✅ RPC Plugin test completed successfully!")
}
