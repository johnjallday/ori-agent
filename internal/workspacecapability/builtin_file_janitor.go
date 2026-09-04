package workspacecapability

import "github.com/johnjallday/ori-agent/internal/workspace"

// FileJanitorDefinitionVersion is the compiled definition version for File
// Janitor. Bump it only when a persisted install needs capability-specific
// migration; the workspace schema itself does not change with it (FR-13).
const FileJanitorDefinitionVersion = 1

// Legacy Downloads Janitor identifiers retained as compatibility aliases for
// this release (FR-131–FR-134). New callers use the canonical spellings above
// them; these keep existing routes, setup progress, and directory requirements
// resolving while callers migrate.
const (
	LegacyAPIPrefixDownloadsJanitor            = "downloads-janitor"
	LegacySetupAdapterDownloadsJanitor         = "downloads_janitor"
	LegacyDirectoryRequirementKeyDownloadsRoot = "downloads-root"
)

// Canonical File Janitor identifiers for new callers.
const (
	FileJanitorAPIPrefix               = "file-janitor"
	FileJanitorSetupAdapterID          = "file_janitor"
	FileJanitorDirectoryRequirementKey = "file-janitor-root"
	FileJanitorConsolePanelID          = "file-janitor"
)

// File Janitor console tabs (FR-105). A configured console shows exactly these.
const (
	FileJanitorTabReview   = "review"
	FileJanitorTabHistory  = "history"
	FileJanitorTabSettings = "settings"
)

// Stable in-process automation identities. Registering by a fixed ID is what
// lets migration and removal reconcile an existing watcher or schedule instead
// of creating a duplicate (FR-130, FR-138).
const (
	FileJanitorWatcherID  = "file-janitor-watcher"
	FileJanitorScheduleID = "file-janitor-daily-catchup"
)

// FileJanitorDefinition returns the compiled File Janitor capability definition
// (PRD FR-1, FR-3). It is a pure value: callers receive an independent copy.
func FileJanitorDefinition() Definition {
	return Definition{
		ID:      workspace.CapabilityFileJanitor,
		Version: FileJanitorDefinitionVersion,
		Display: Display{
			Name:    "File Janitor",
			Tagline: "Review and file one intake folder safely.",
			Summary: "Watches one inbox-style folder, proposes where each new file should go, and files only what you approve.",
			Highlights: []string{
				"Manages exactly one folder you choose.",
				"Reads names, types, sizes, and dates — not file contents.",
				"Never moves or trashes a file without your approval.",
				"Files only into <folder>/Filed/<category>, and never deletes permanently.",
			},
		},
		Requirements: Requirements{
			LocalFolderAccess:       true,
			MaxInstallsPerWorkspace: 1,
		},
		Setup: SetupDescriptor{
			AdapterID:                      FileJanitorSetupAdapterID,
			LegacyAdapterIDs:               []string{LegacySetupAdapterDownloadsJanitor},
			DirectoryRequirementKey:        FileJanitorDirectoryRequirementKey,
			LegacyDirectoryRequirementKeys: []string{LegacyDirectoryRequirementKeyDownloadsRoot},
		},
		API: APIDescriptor{
			Prefix:         FileJanitorAPIPrefix,
			LegacyPrefixes: []string{LegacyAPIPrefixDownloadsJanitor},
		},
		Station: StationDescriptor{
			Title:                 "File Janitor",
			ShowFolderDisplayName: true,
		},
		Console: ConsoleDescriptor{
			PanelID:    FileJanitorConsolePanelID,
			Tabs:       []string{FileJanitorTabReview, FileJanitorTabHistory, FileJanitorTabSettings},
			DefaultTab: FileJanitorTabReview,
		},
		Automation: AutomationDescriptor{
			WatcherID:             FileJanitorWatcherID,
			ScheduleID:            FileJanitorScheduleID,
			DefaultDailyLocalTime: "09:00",
		},
		Companion: &CompanionDescriptor{
			DefaultDisplayName:      "File Curator",
			IncludedByBlueprint:     true,
			OfferedOnInPlaceInstall: true,
			ReadOnly:                true,
		},
	}
}

// BuiltinDefinitions returns every capability this build is allowed to
// activate. This slice IS the allowlist: a persisted install record can only
// select one of these, never add to them (FR-2, FR-14).
func BuiltinDefinitions() []Definition {
	return []Definition{
		FileJanitorDefinition(),
		SampleLibraryDefinition(),
	}
}
