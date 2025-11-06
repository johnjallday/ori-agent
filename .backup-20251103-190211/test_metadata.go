package main

import (
	"fmt"
	"github.com/johnjallday/ori-agent/internal/pluginloader"
)

func main() {
	tool, err := pluginloader.LoadPluginUnified("uploaded_plugins/music-project-manager")
	if err != nil {
		fmt.Printf("Error loading plugin: %v\n", err)
		return
	}
	defer pluginloader.CloseRPCPlugin(tool)
	
	meta, err := pluginloader.GetPluginMetadata(tool)
	if err != nil {
		fmt.Printf("Error getting metadata: %v\n", err)
		return
	}
	
	if meta == nil {
		fmt.Println("No metadata returned")
		return
	}
	
	fmt.Printf("Metadata:\n")
	fmt.Printf("  License: %s\n", meta.License)
	fmt.Printf("  Repository: %s\n", meta.Repository)
	fmt.Printf("  Maintainers: %d\n", len(meta.Maintainers))
	for i, m := range meta.Maintainers {
		fmt.Printf("    [%d] %s <%s> (%s)\n", i, m.Name, m.Email, m.Role)
	}
}
