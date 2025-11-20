package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/johnjallday/ori-agent/internal/pluginloader"
)

func main() {
	// Load the math plugin
	pluginPath := "uploaded_plugins/math"
	tool, err := pluginloader.LoadPluginUnified(pluginPath)
	if err != nil {
		log.Fatalf("Failed to load plugin: %v", err)
	}

	// Get the definition
	def := tool.Definition()

	// Print the definition as JSON
	defJSON, err := json.MarshalIndent(def, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal definition: %v", err)
	}

	fmt.Println("Plugin Definition:")
	fmt.Println(string(defJSON))

	// Check if Parameters is empty
	if len(def.Parameters) == 0 {
		fmt.Println("\n❌ ERROR: Parameters map is EMPTY!")
		os.Exit(1)
	}

	fmt.Println("\n✅ SUCCESS: Parameters map has", len(def.Parameters), "entries")
}
