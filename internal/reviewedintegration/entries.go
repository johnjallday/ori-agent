package reviewedintegration

// This file is inert host-owned allowlist data. Each entry is a reviewed floor:
// Ori installs the latest stable release at or above MinimumVersion and falls
// back to FallbackCommit when that release cannot be resolved. A fallback commit
// is recorded only after verifying the published asset against that commit's
// manifest. See docs/reaper-integration.md for the v0.8.0 verification evidence.
var builtInEntries = mustRegistry([]Entry{
	{
		Key: "ori_reaper", PluginID: "reaper-plugin", MinimumVersion: "0.8.0",
		DisplayName:  "REAPER",
		InstallTitle: "Install Ori REAPER Plugin",
		InstallDescription: "Ori's REAPER integration is a local integration for Ori, not an audio plug-in, VST, " +
			"effect, or instrument. It will not appear in REAPER's FX browser.",
		SourceRepository: "https://github.com/johnjallday/reaper-plugin",
		FallbackCommit:   "3e3234bfae3465f909fe2aa5189f685a41c7a2ed", SourceFormat: reviewedClaudeFormat,
		PublisherLabel: "Ori", SourceLabel: "johnjallday/reaper-plugin",
		ExpectedBlueprintID: "reaper-song", MinimumBlueprintVersion: 9,
		ExpectedProgramID: "music-producer-assistant", ExpectedProgramSchema: 1,
		RequiredHostFeatures: []string{
			"independent_program_homes_v1",
			"specialist_setup_journey_v1",
			"setup_quests_v2",
			"template_group_requirements_v1",
			"blueprint_inputs_v1",
		},
		ExpectedProtocol: 1, SupportedPlatforms: []string{"darwin/arm64"},
		ReleaseReady: true,
	},
})

// builtInHomeProviders are reviewed plugins that contribute only an independent
// Assistant Program Home. Same floor rule as above; a content-only release has
// no platform asset, so its fallback commit is verified against the published
// tag alone. See docs/architecture/independent-program-homes.md for the v0.1.0
// evidence.
var builtInHomeProviders = mustHomeProviders([]HomeProvider{
	{
		Key: "music_project_management", PluginID: "music-project-management", MinimumVersion: "0.1.0",
		DisplayName:      "Music Project Management",
		SourceRepository: "https://github.com/johnjallday/music-project-management",
		FallbackCommit:   "5f748d2de4457ac9dd02ea1ec31e34e1493744cf", SourceFormat: reviewedClaudeFormat,
		PublisherLabel: "Ori", SourceLabel: "johnjallday/music-project-management",
		ProgramID: "music-producer-assistant", HomeSchemaVersion: 1,
		RequiredHostFeatures: []string{"independent_program_homes_v1"},
		ExpectedProtocol:     1,
		ReleaseReady:         true,
	},
}, builtInEntries)
