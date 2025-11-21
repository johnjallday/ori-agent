package readmeupdater

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Updater handles README.md updates
type Updater struct {
	readmePath string
	dryRun     bool
	verbose    bool
	projectDir string
	sections   map[string]SectionUpdater
}

// SectionUpdater defines the interface for updating specific sections
type SectionUpdater interface {
	Name() string
	Generate(projectDir string) (string, error)
}

// New creates a new README updater
func New(readmePath string, dryRun, verbose bool) *Updater {
	projectDir := filepath.Dir(readmePath)

	u := &Updater{
		readmePath: readmePath,
		dryRun:     dryRun,
		verbose:    verbose,
		projectDir: projectDir,
		sections:   make(map[string]SectionUpdater),
	}

	// Register all section updaters
	u.registerUpdaters()

	return u
}

// registerUpdaters registers all available section updaters
func (u *Updater) registerUpdaters() {
	// TODO: Implement updater types
	// u.sections["VERSION"] = &VersionUpdater{}
	// u.sections["GO_VERSION"] = &GoVersionUpdater{}
	// u.sections["PLUGINS"] = &PluginsUpdater{}
}

// Update processes the README and updates all AUTO sections
func (u *Updater) Update() error {
	// Read the current README
	content, err := os.ReadFile(u.readmePath)
	if err != nil {
		return fmt.Errorf("failed to read README: %w", err)
	}

	originalContent := string(content)
	updatedContent := originalContent

	// Process each registered section
	for sectionName, updater := range u.sections {
		startMarker := fmt.Sprintf("<!-- AUTO:%s -->", sectionName)
		endMarker := fmt.Sprintf("<!-- AUTO:%s_END -->", sectionName)

		if !strings.Contains(updatedContent, startMarker) {
			if u.verbose {
				fmt.Printf("⚠️  Section %s not found in README (no markers)\n", sectionName)
			}
			continue
		}

		// Generate new content
		newContent, err := updater.Generate(u.projectDir)
		if err != nil {
			fmt.Printf("⚠️  Failed to generate %s: %v\n", sectionName, err)
			continue
		}

		// Replace content between markers
		updatedContent, err = u.replaceBetweenMarkers(updatedContent, startMarker, endMarker, newContent)
		if err != nil {
			fmt.Printf("⚠️  Failed to update %s: %v\n", sectionName, err)
			continue
		}

		if u.verbose {
			fmt.Printf("✓ Updated section: %s\n", sectionName)
		}
	}

	// Check if anything changed
	if originalContent == updatedContent {
		fmt.Println("ℹ️  No changes needed - README is already up to date!")
		return nil
	}

	// Show diff if verbose or dry-run
	if u.verbose || u.dryRun {
		u.showChanges(originalContent, updatedContent)
	}

	// Write back if not dry-run
	if !u.dryRun {
		if err := os.WriteFile(u.readmePath, []byte(updatedContent), 0644); err != nil {
			return fmt.Errorf("failed to write README: %w", err)
		}
	}

	return nil
}

// replaceBetweenMarkers replaces content between start and end markers
func (u *Updater) replaceBetweenMarkers(content, startMarker, endMarker, newContent string) (string, error) {
	startIdx := strings.Index(content, startMarker)
	if startIdx == -1 {
		return content, fmt.Errorf("start marker not found: %s", startMarker)
	}

	endIdx := strings.Index(content, endMarker)
	if endIdx == -1 {
		return content, fmt.Errorf("end marker not found: %s", endMarker)
	}

	if endIdx <= startIdx {
		return content, fmt.Errorf("end marker appears before start marker")
	}

	// Build the replacement
	before := content[:startIdx+len(startMarker)]
	after := content[endIdx:]

	return before + "\n" + newContent + "\n" + after, nil
}

// showChanges displays a simple diff of what changed
func (u *Updater) showChanges(original, updated string) {
	fmt.Println("\n📝 Changes:")
	fmt.Println(strings.Repeat("─", 60))

	originalLines := strings.Split(original, "\n")
	updatedLines := strings.Split(updated, "\n")

	scanner1 := bufio.NewScanner(strings.NewReader(original))
	scanner2 := bufio.NewScanner(strings.NewReader(updated))

	lineNum := 0
	for scanner1.Scan() && scanner2.Scan() {
		lineNum++
		line1 := scanner1.Text()
		line2 := scanner2.Text()

		if line1 != line2 {
			fmt.Printf("  Line %d:\n", lineNum)
			fmt.Printf("    - %s\n", line1)
			fmt.Printf("    + %s\n", line2)
		}
	}

	// Handle different lengths
	if len(originalLines) != len(updatedLines) {
		fmt.Printf("  (File length changed: %d → %d lines)\n", len(originalLines), len(updatedLines))
	}

	fmt.Println(strings.Repeat("─", 60))
}
