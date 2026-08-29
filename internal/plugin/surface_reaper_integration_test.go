package plugin

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalReaperPluginProjectEntryContribution(t *testing.T) {
	root := os.Getenv("ORI_REAPER_PLUGIN_PATH")
	if root == "" {
		t.Skip("ORI_REAPER_PLUGIN_PATH is not set")
	}
	oriFixture, err := os.ReadFile(filepath.Join("testdata", "workspace-surface-v1", "valid-project-entry-contribution.json"))
	if err != nil {
		t.Fatal(err)
	}
	pluginFixture, err := os.ReadFile(filepath.Join(root, "protocol", "workspace-surface-v1", "valid-project-entry-contribution.json")) // #nosec G304 -- explicit coordinated local-plugin test root
	if err != nil || !bytes.Equal(oriFixture, pluginFixture) {
		t.Fatalf("project-entry protocol fixtures differ: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, OriManifestDir, OriManifestFile)) // #nosec G304 -- explicit coordinated local-plugin test root
	if err != nil {
		t.Fatal(err)
	}
	contribution, err := ParseSurfaceContribution(data)
	if err != nil {
		t.Fatalf("parse local REAPER contribution: %v", err)
	}
	if len(contribution.Capabilities) != 1 {
		t.Fatalf("capabilities = %+v", contribution.Capabilities)
	}
	var project *ContributedSurface
	for index := range contribution.Capabilities[0].Surfaces {
		if contribution.Capabilities[0].Surfaces[index].ID == "project-tidy" {
			project = &contribution.Capabilities[0].Surfaces[index]
		}
	}
	if project == nil || project.Placement != "project_entry" || project.DefaultTaskTemplate != "survey" || len(project.TaskTemplates) != 2 {
		t.Fatalf("project tidy surface = %+v", project)
	}
	for _, template := range project.TaskTemplates {
		if len(template.RequiredCapabilities) != 1 || template.RequiredCapabilities[0] != "reaper_live_control" || !template.AutoStart ||
			strings.Contains(template.Instructions, "file_fallback_for") || !strings.Contains(strings.ToLower(template.Instructions), "never edit the .rpp file") {
			t.Fatalf("task template authority = %+v", template)
		}
		if template.ID == "apply" && !strings.Contains(template.Instructions, "workspace_notes") {
			t.Fatalf("apply task does not require the host note tool")
		}
	}
}
