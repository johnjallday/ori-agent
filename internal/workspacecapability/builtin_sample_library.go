package workspacecapability

import "github.com/johnjallday/ori-agent/internal/workspace"

const (
	SampleLibraryDefinitionVersion = 1
	SampleLibrarySetupAdapterID    = "sample-library"
	SampleLibraryAPIPrefix         = "sample-library"
	SampleLibraryConsolePanelID    = "sample-library"
)

// SampleLibraryDefinition is inert catalog metadata for the separately
// consented Home add-on. It intentionally declares no companion, automation,
// command, URL, script, scanner, route handler, or child-workspace grant.
func SampleLibraryDefinition() Definition {
	return Definition{
		ID: workspace.CapabilitySampleLibrary, Version: SampleLibraryDefinitionVersion,
		Display: Display{
			Name: "Sample Library", Tagline: "Curate approved sample folders from Home.",
			Summary: "Builds a bounded catalog from folders you connect and indexes only when you explicitly ask.",
			Highlights: []string{
				"Installs only in an Assistant Program Home that offers the add-on.",
				"Connecting a folder does not scan or analyze it.",
				"Metadata indexing and content analysis are separate decisions.",
				"A project receives only an exact sample copy you approve, never access to a source root.",
			},
		},
		Requirements: Requirements{LocalFolderAccess: true, MaxInstallsPerWorkspace: 1, AssistantProgramHomeOnly: true},
		Setup:        SetupDescriptor{AdapterID: SampleLibrarySetupAdapterID, DirectoryRequirementKey: "sample-library-root"},
		API:          APIDescriptor{Prefix: SampleLibraryAPIPrefix},
		Station:      StationDescriptor{Title: "Sample Library", ShowFolderDisplayName: false},
		Console:      ConsoleDescriptor{PanelID: SampleLibraryConsolePanelID, Tabs: []string{"catalog", "collections", "settings"}, DefaultTab: "catalog"},
	}
}
