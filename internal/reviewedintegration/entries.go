package reviewedintegration

// This file is inert host-owned allowlist data. Release sources are pinned only
// after verifying the published asset against that commit's manifest.
// See docs/reaper-integration.md for the v0.5.0 verification evidence.
var builtInEntries = mustRegistry([]Entry{
	{
		Key: "ori_reaper", PluginID: "reaper-plugin", ExpectedVersion: "0.5.0",
		SourceRepository: "https://github.com/johnjallday/reaper-plugin",
		SourceCommit:     "1f494db5a39d8c13f6149943b28e6a506d19631a", SourceFormat: reviewedClaudeFormat,
		PublisherLabel: "Ori", SourceLabel: "johnjallday/reaper-plugin",
		ExpectedBlueprintID: "reaper-song", ExpectedBlueprintVersion: 4,
		ExpectedProgramID: "music-producer-assistant", ExpectedProgramSchema: 2,
		RequiredHostFeatures: []string{
			"assistant_program_v1",
			"specialist_setup_journey_v1",
		},
		ExpectedProtocol: 1, SupportedPlatforms: []string{"darwin/arm64"},
		ReleaseReady: true,
	},
})
