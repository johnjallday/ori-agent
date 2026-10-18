package backuprecovery

type GuideLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}
type GuideStep struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Instructions []string `json:"instructions"`
}
type SetupGuide struct {
	Title         string      `json:"title"`
	Provider      string      `json:"provider"`
	Version       string      `json:"version"`
	Boundary      string      `json:"boundary"`
	Prerequisites []string    `json:"prerequisites"`
	Steps         []GuideStep `json:"steps"`
	Limits        []string    `json:"limits"`
	Sources       []GuideLink `json:"sources"`
}

// ExternalSetupGuide is compiled, model-free guidance. No provider label, path,
// command, credential, remote template or caller text can become an instruction.
// Every call owns its slices; this is not a mutable shared guide registry.
func ExternalSetupGuide() SetupGuide {
	return SetupGuide{
		Title:    "Set up backups outside Ori, then connect metadata",
		Provider: "restic", Version: "0.19.1",
		Boundary: "The external application performs backups and restores. Ori only observes explicitly approved metadata. Completing this guide or choosing folders does not prove a backup exists.",
		Prerequisites: []string{
			"Validated v1: official restic 0.19.1 for Apple Silicon, repository format 2; macOS 26.3.1 with product build 25D771280a.",
			"Selected source folders must be on local internal Apple Fabric APFS storage. Choose one independent physical USB APFS destination, its actual mounted volume root, and one existing repository inside it.",
			"Use a drive with enough space for your selected data, multiple versions and growth. Check source size outside Ori, for example with Finder Get Info. Ori does not scan source contents or predict whether the next backup will fit.",
			"The server operator must separately supply the exact trusted executable. Ori does not download, install, find alternative binaries, or run installation commands. Unsupported versions, builds, storage or containment remain unavailable.",
		},
		Steps: []GuideStep{
			{ID: "prepare-storage", Title: "1. Check the destination yourself", Instructions: []string{"Connect an existing suitable external drive using macOS. Confirm its identity, mounted volume root and available space outside Ori.", "Do not erase, reformat or reuse a drive whose existing contents you have not accounted for. Ori will not mount, unlock, format or eject it. A folder with the same mount spelling is not proof that the intended drive is connected."}},
			{ID: "install-tool", Title: "2. Install the supported application yourself", Instructions: []string{"Follow the official version-pinned installation documentation outside Ori. This feature validates only the official Apple Silicon 0.19.1 release, not a similarly named executable or another provider.", "If the prerequisite is unavailable, keep this workspace in guide/manual-only mode. Do not disable containment or grant broad filesystem access to make a check appear ready."}},
			{ID: "external-repository", Title: "3. Prepare an encrypted, versioned repository outside Ori", Instructions: []string{"Following the official preparation documentation, choose one repository on that independent drive. Review the destination before using the external application's setup tools; Ori does not initialize a repository or select a destination for you.", "Keep the repository password/key-recovery information in a secure place separate from the drive. Losing every usable password/key can make encrypted backups unrecoverable. Never put a password, recovery key or executable recovery command in chat or recovery notes."}},
			{ID: "external-backup", Title: "4. Run and check the first backup outside Ori", Instructions: []string{"In the external application, explicitly select the intended folders and review exclusions. Run the first backup there and inspect its own result and warnings.", "Ori records intended sources separately from historical provider-declared paths. A published snapshot does not prove the latest attempt fully succeeded, every current file is covered, or recovery works."}},
			{ID: "external-schedule", Title: "5. Decide the external backup schedule and retention", Instructions: []string{"Set any automatic backup schedule and version-retention policy outside Ori, using the application's documented tools or your existing scheduler. Ori creates neither backup jobs nor OS daemons.", "Pausing or closing Ori stops Ori's checks, not the external application's backup schedule. Plan what happens when the Mac sleeps, the drive is disconnected or storage becomes full."}},
			{ID: "connect-metadata", Title: "6. Review one metadata connection in Settings", Instructions: []string{"Choose the source folders, the actual external volume root and the existing repository through the user controls. Select one already-created, workspace-scoped Vault secret reference; unlock that Vault outside this console if needed. Ori never discovers or requests the plaintext password in an API, prompt or note.", "Review the exact selections, then explicitly approve a metadata read. Selection/review alone grants nothing. Only a successful bounded read creates a local connection receipt; an empty repository remains No completed backup observed."}},
			{ID: "monitoring-choice", Title: "7. Keep manual-only use or separately review monitoring", Instructions: []string{"A manual check is separate from periodic consent. Review cadence, maximum expected backup age, evidence freshness, disconnect tolerance and capacity warnings before opting into checks.", "Ori cannot promise checks while the server or Mac is off. A disconnected destination is not automatically a failed backup. Unknown capacity, coverage and latest-attempt outcome stay unknown."}},
			{ID: "recovery-practice", Title: "8. Practice a sample recovery outside Ori", Instructions: []string{"Use the Recovery tab's vetted guidance and the external application to restore a sample to a separate location. Do not overwrite your working source as a test.", "Record only a dated user-reported result in Ori. Ori does not restore or inspect recovered contents; one passed sample is not proof that every file is recoverable."}},
		},
		Limits: []string{"External-drive-only backup is not off-site protection. Plan a separate off-site strategy outside this v1 integration.", "No cloud/NAS, other Macs, whole-Mac disaster recovery, automatic mounting/unlocking, backup/restore execution, repository repair, integrity scans, pruning or key rotation.", "A metadata connection is not proof of protection. Backup completeness, latest-attempt success and recoverability are not established by snapshot metadata alone."},
		Sources: []GuideLink{
			{Label: "Official 0.19.1 installation", URL: "https://github.com/restic/restic/blob/v0.19.1/doc/020_installation.rst"},
			{Label: "Official 0.19.1 repository preparation", URL: "https://github.com/restic/restic/blob/v0.19.1/doc/030_preparing_a_new_repo.rst"},
			{Label: "Official 0.19.1 backup documentation", URL: "https://github.com/restic/restic/blob/v0.19.1/doc/040_backup.rst"},
			{Label: "Official 0.19.1 recovery documentation", URL: "https://github.com/restic/restic/blob/v0.19.1/doc/050_restore.rst"},
		},
	}
}
