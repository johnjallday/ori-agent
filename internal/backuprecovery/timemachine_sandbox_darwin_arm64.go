//go:build darwin && arm64

package backuprecovery

// timeMachineProfileRules are the only additions the spike found the two read
// verbs need: backupd's two XPC services for status, and the Time Machine
// preference domain through cfprefsd for destinations. Without them both verbs
// still exit 0 with fabricated empty answers, so the parser treats those as
// unavailable. No file data, network, write or other executable is granted.
const timeMachineProfileRules = `(allow mach-lookup (global-name "com.apple.backupd.xpc") (global-name "com.apple.backupd.status.xpc")
 (global-name "com.apple.cfprefsd.daemon") (global-name "com.apple.cfprefsd.agent"))
(allow user-preference-read (preference-domain "com.apple.TimeMachine") (preference-domain "com.apple.timemachine"))
(allow ipc-posix-shm-read-data (ipc-posix-name-prefix "apple.cfprefs."))
`

// timeMachineSandboxProfile is the directory-metadata policy for tmutil with
// every selected source's contents denied, plus timeMachineProfileRules. It is
// sent on an owned pipe, never as argv.
func timeMachineSandboxProfile(executable string, sources []string) (string, error) {
	profile, err := directoryMetadataSandboxProfile(executable, sources)
	if err != nil {
		return "", err
	}
	profile += timeMachineProfileRules
	if len(profile) > maxSandboxProfileBytes {
		return "", errSandboxProfile
	}
	return profile, nil
}
