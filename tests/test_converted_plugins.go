package main

import (
	"context"
	"fmt"
	"os"

	"github.com/johnjallday/ori-agent/internal/pluginloader"
)

func main() {
	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║   Testing Converted Plugins (RPC)                         ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// Test cases
	tests := []struct {
		name       string
		pluginPath string
		testArgs   string
	}{
		{
			name:       "Dolphin Reaper - Get Settings",
			pluginPath: "../dolphin-reaper/dolphin-reaper",
			testArgs:   `{"operation": "get_settings"}`,
		},
		{
			name:       "Music Project Manager - Get Settings",
			pluginPath: "../music-project-manager/music-project-manager",
			testArgs:   `{"operation": "get_settings"}`,
		},
	}

	passedTests := 0
	failedTests := 0

	for i, test := range tests {
		fmt.Printf("Test %d: %s\n", i+1, test.name)
		fmt.Println("─────────────────────────────────────────────────────────────")

		// Load plugin
		fmt.Printf("  Loading: %s\n", test.pluginPath)
		tool, err := pluginloader.LoadPluginUnified(test.pluginPath)
		if err != nil {
			fmt.Printf("  ❌ FAILED: %v\n\n", err)
			failedTests++
			continue
		}

		// Check if RPC plugin
		isRPC := pluginloader.IsRPCPlugin(tool)
		fmt.Printf("  Plugin Type: %s\n", getPluginType(isRPC))

		// Get definition
		def := tool.Definition()
		fmt.Printf("  Function: %s\n", def.Name)
		fmt.Printf("  Description: %s\n", def.Description.String())

		// Get version
		if versionedTool, ok := tool.(interface{ Version() string }); ok {
			fmt.Printf("  Version: %s\n", versionedTool.Version())
		}

		// Test call
		fmt.Printf("  Calling with: %s\n", test.testArgs)
		result, err := tool.Call(context.Background(), test.testArgs)

		if err != nil {
			fmt.Printf("  ❌ FAILED: %v\n", err)
			failedTests++
		} else {
			fmt.Printf("  Result: %s\n", result)
			fmt.Printf("  ✅ PASSED\n")
			passedTests++
		}

		// Cleanup
		pluginloader.CloseRPCPlugin(tool)
		fmt.Println()
	}

	// Summary
	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║   Test Summary                                             ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
	fmt.Printf("  Total Tests:  %d\n", len(tests))
	fmt.Printf("  ✅ Passed:    %d\n", passedTests)
	fmt.Printf("  ❌ Failed:    %d\n", failedTests)
	fmt.Println()

	if failedTests > 0 {
		fmt.Println("❌ Some tests failed!")
		os.Exit(1)
	} else {
		fmt.Println("✅ All tests passed!")
		os.Exit(0)
	}
}

func getPluginType(isRPC bool) string {
	if isRPC {
		return "RPC Executable 🚀"
	}
	return "Shared Library (.so)"
}
