package reviewedintegration

// This file is inert host-owned allowlist data. Release sources are pinned only
// after verifying the published asset against that commit's manifest.
// See docs/reaper-integration.md for the v0.6.1 verification evidence.
var builtInEntries = mustRegistry([]Entry{
	{
		Key: "ori_reaper", PluginID: "reaper-plugin", ExpectedVersion: "0.6.1",
		DisplayName:  "REAPER",
		InstallTitle: "Install Ori REAPER Plugin",
		InstallDescription: "Ori's REAPER integration is a local integration for Ori, not an audio plug-in, VST, " +
			"effect, or instrument. It will not appear in REAPER's FX browser.",
		SourceRepository: "https://github.com/johnjallday/reaper-plugin",
		SourceCommit:     "e11ca2942279af02a9a035039b18b146ff9fc89d", SourceFormat: reviewedClaudeFormat,
		PublisherLabel: "Ori", SourceLabel: "johnjallday/reaper-plugin",
		ExpectedBlueprintID: "reaper-song", ExpectedBlueprintVersion: 7,
		ExpectedProgramID: "music-producer-assistant", ExpectedProgramSchema: 2,
		RequiredHostFeatures: []string{
			"assistant_program_v1",
			"specialist_setup_journey_v1",
			"setup_quests_v2",
			"template_group_requirements_v1",
		},
		ExpectedProtocol: 1, SupportedPlatforms: []string{"darwin/arm64"},
		ReleaseReady: true,
	},
})
