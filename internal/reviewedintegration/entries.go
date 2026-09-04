package reviewedintegration

// This file is inert host-owned allowlist data. The candidate commit is pinned
// while release readiness stays false until a human publishes the exact asset.
var builtInEntries = mustRegistry([]Entry{
	{
		Key: "ori_reaper", PluginID: "reaper-plugin", ExpectedVersion: "0.5.0",
		SourceRepository: "https://github.com/johnjallday/reaper-plugin",
		SourceCommit:     "a5f4149f1aaf64611e90ff9484e37f7854c828b9", SourceFormat: reviewedClaudeFormat,
		PublisherLabel: "Ori", SourceLabel: "johnjallday/reaper-plugin",
		ExpectedBlueprintID: "reaper-song", ExpectedBlueprintVersion: 4,
		ExpectedProgramID: "music-producer-assistant", ExpectedProgramSchema: 2,
		RequiredHostFeatures: []string{
			"assistant_program_v1",
			"specialist_setup_journey_v1",
		},
		ExpectedProtocol: 1, SupportedPlatforms: []string{"darwin/arm64"},
		ReleaseReady: false,
	},
})
