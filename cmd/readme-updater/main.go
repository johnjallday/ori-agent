package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/johnjallday/ori-agent/internal/readmeupdater"
)

func main() {
	var (
		dryRun     bool
		verbose    bool
		readmePath string
	)

	flag.BoolVar(&dryRun, "dry-run", false, "Show what would be updated without making changes")
	flag.BoolVar(&verbose, "verbose", false, "Show detailed output")
	flag.StringVar(&readmePath, "file", "README.md", "Path to README.md file")
	flag.Parse()

	// Convert to absolute path
	absPath, err := filepath.Abs(readmePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving path: %v\n", err)
		os.Exit(1)
	}

	// Create updater
	updater := readmeupdater.New(absPath, dryRun, verbose)

	// Run updates
	if err := updater.Update(); err != nil {
		fmt.Fprintf(os.Stderr, "Error updating README: %v\n", err)
		os.Exit(1)
	}

	if dryRun {
		fmt.Println("\n✅ Dry run completed. No changes were made.")
		fmt.Println("Run without --dry-run to apply changes.")
	} else {
		fmt.Println("\n✅ README.md updated successfully!")
	}
}
