package drive

import (
	"reflect"
	"testing"
)

func TestGoogleDrivePreset(t *testing.T) {
	p := GoogleDrivePreset()
	if p.ID != GooglePresetID || p.ServerName != GoogleServerName {
		t.Fatalf("unexpected identifiers: id=%q server=%q", p.ID, p.ServerName)
	}
	if p.URL != GoogleMCPURL {
		t.Errorf("URL = %q, want %q", p.URL, GoogleMCPURL)
	}
	if !p.DeveloperPreview {
		t.Error("Drive preset should be flagged DeveloperPreview")
	}
	if len(p.Prerequisites) == 0 {
		t.Error("Drive preset should list prerequisites")
	}
	// Credential-free contract: the preset descriptor must never embed a secret.
	if p.DocsURL == "" {
		t.Error("Drive preset should link its setup docs")
	}
}

func TestReadOnlyToolAllowlist_ExactSet(t *testing.T) {
	want := []string{
		"search_files",
		"list_recent_files",
		"get_file_metadata",
		"read_file_content",
		"download_file_content",
	}
	if !reflect.DeepEqual(ReadOnlyToolAllowlist, want) {
		t.Fatalf("allowlist drift: got %v, want %v", ReadOnlyToolAllowlist, want)
	}
}

func TestIsAllowedTool_FailClosed(t *testing.T) {
	for _, tool := range ReadOnlyToolAllowlist {
		if !IsAllowedTool(tool) {
			t.Errorf("IsAllowedTool(%q) = false, want true", tool)
		}
	}
	// Mutations, permission reads, and unknown/future tools are all denied.
	denied := []string{
		"create_file",
		"copy_file",
		"get_file_permissions",
		"delete_file",
		"share_file",
		"",
		"SEARCH_FILES",            // case-sensitive: not the allowlisted name
		"search_files_and_delete", // superstring must not slip through
		"some_future_write_tool_v2",
	}
	for _, tool := range denied {
		if IsAllowedTool(tool) {
			t.Errorf("IsAllowedTool(%q) = true, want false (fail-closed)", tool)
		}
	}
}

func TestFilterTools_DropsEverythingNotAllowed(t *testing.T) {
	discovered := []string{
		"search_files",
		"create_file", // mutation advertised by the server → dropped
		"get_file_metadata",
		"get_file_permissions", // permission tool → dropped
		"read_file_content",
		"brand_new_tool", // unknown/future → dropped (fail-closed)
		"list_recent_files",
		"download_file_content",
	}
	got := FilterTools(discovered)
	want := []string{
		"search_files",
		"get_file_metadata",
		"read_file_content",
		"list_recent_files",
		"download_file_content",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FilterTools = %v, want %v (order preserved, denied dropped)", got, want)
	}
}

func TestFilterTools_EmptyAndAllDenied(t *testing.T) {
	if got := FilterTools(nil); len(got) != 0 {
		t.Errorf("FilterTools(nil) = %v, want empty", got)
	}
	// A server advertising only write tools yields an empty exposed set — never
	// a fail-open pass-through.
	if got := FilterTools([]string{"create_file", "copy_file"}); len(got) != 0 {
		t.Errorf("FilterTools(all-denied) = %v, want empty", got)
	}
}
