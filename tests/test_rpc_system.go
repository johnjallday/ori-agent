package main

import (
	"context"
	"fmt"
	"os"

	"github.com/johnjallday/ori-agent/internal/pluginloader"
)

func main() {
	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║   Dolphin Agent RPC Plugin System Test                    ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// Test cases
	tests := []struct {
		name        string
		pluginPath  string
		testArgs    string
		expectError bool
	}{
		{
			name:       "Weather Plugin",
			pluginPath: "plugins/weather/weather",
			testArgs:   `{"location": "Tokyo"}`,
		},
		{
			name:       "Math Plugin - Addition",
			pluginPath: "plugins/math/math",
			testArgs:   `{"operation": "add", "a": 15, "b": 27}`,
		},
		{
			name:       "Math Plugin - Division",
			pluginPath: "plugins/math/math",
			testArgs:   `{"operation": "divide", "a": 100, "b": 4}`,
		},
		{
			name:        "Math Plugin - Division by Zero",
			pluginPath:  "plugins/math/math",
			testArgs:    `{"operation": "divide", "a": 10, "b": 0}`,
			expectError: true,
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

		if test.expectError {
			if err != nil {
				fmt.Printf("  ✅ PASSED: Got expected error: %v\n", err)
				passedTests++
			} else {
				fmt.Printf("  ❌ FAILED: Expected error but got result: %s\n", result)
				failedTests++
			}
		} else {
			if err != nil {
				fmt.Printf("  ❌ FAILED: %v\n", err)
				failedTests++
			} else {
				fmt.Printf("  Result: %s\n", result)
				fmt.Printf("  ✅ PASSED\n")
				passedTests++
			}
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
