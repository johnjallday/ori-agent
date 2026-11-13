package reviewedintegration

// This file is inert host-owned allowlist data. Each entry is a reviewed floor:
// Ori installs the latest stable release at or above MinimumVersion and falls
// back to FallbackCommit when that release cannot be resolved. A fallback commit
// is recorded only after verifying the published asset against that commit's
// manifest. See docs/reaper-integration.md for the v0.12.0 verification evidence.
var builtInEntries = mustRegistry([]Entry{
	{
		Key: "ori_reaper", PluginID: "reaper-plugin", MinimumVersion: "0.12.0",
		DisplayName:  "REAPER",
		InstallTitle: "Install Ori REAPER Plugin",
		InstallDescription: "Ori's REAPER integration is a local integration for Ori, not an audio plug-in, VST, " +
			"effect, or instrument. It will not appear in REAPER's FX browser.",
		SourceRepository: "https://github.com/johnjallday/reaper-plugin",
		FallbackCommit:   "ea85c6692e49ddaaf83ae3f9e17ede17fe6385e3", SourceFormat: reviewedClaudeFormat,
		PublisherLabel: "Ori", SourceLabel: "johnjallday/reaper-plugin",
		ExpectedBlueprintID: "reaper-song", MinimumBlueprintVersion: 10,
		ExpectedProgramID: "music-producer-assistant", ExpectedProgramSchema: 1,
		RequiredHostFeatures: []string{
			"independent_program_homes_v1",
			"specialist_setup_journey_v1",
			"setup_quests_v2",
			"template_group_requirements_v1",
			"blueprint_inputs_v1",
			"home_profile_v1",
			"resource_workspaces_v1",
			"project_facts_v1",
		},
		ExpectedProtocol: 1, SupportedPlatforms: []string{"darwin/arm64"},
		ReleaseReady: true,
	},
})

// builtInHomeProviders are reviewed plugins that contribute only an independent
// Assistant Program Home. Same floor rule as above; a content-only release has
// no platform asset, so its fallback commit is verified against the published
// tag alone. See docs/architecture/independent-program-homes.md for the v0.1.0,
// v0.3.0 and v0.5.0 evidence.
var builtInHomeProviders = mustHomeProviders([]HomeProvider{
	{
		Key: "music_project_management", PluginID: "music-project-management", MinimumVersion: "0.5.0",
		DisplayName:      "Music Project Management",
		SourceRepository: "https://github.com/johnjallday/music-project-management",
		// v0.5.0: the Home's project_card words (Song, Songs and assistants,
		// tracks/BPM/length) and the Key, Reference tracks and Mix notes card
		// fields; requires 0.4.0's three host features plus project_card_words_v1.
		FallbackCommit: "c420507da24c1fc007d794f3e8974231dd2c5d4a", SourceFormat: reviewedClaudeFormat,
		PublisherLabel: "Ori", SourceLabel: "johnjallday/music-project-management",
		ProgramID: "music-producer-assistant", HomeSchemaVersion: 1,
		RequiredHostFeatures: []string{
			"independent_program_homes_v1",
			"home_profile_v1",
			"resource_workspaces_v1",
			"project_card_words_v1",
		},
		ExpectedProtocol: 1,
		ReleaseReady:     true,
	},
}, builtInEntries)
