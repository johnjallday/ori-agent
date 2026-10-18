//go:build darwin && arm64

package backuprecovery

// scopedOSQuerySandboxProfile permits the fixed protected diskutil info reader
// to ask the OS storage services for metadata. It grants no device-node data
// reads, mounts, writes, network or arbitrary executable. Service APIs themselves
// are broader than info: only the compiled trusted command may use this policy.
func scopedOSQuerySandboxProfile(executable string, sources []string) (string, error) {
	profile, err := directoryMetadataSandboxProfile(executable, sources)
	if err != nil {
		return "", err
	}
	profile += scopedOSQueryRules
	if len(profile) > maxSandboxProfileBytes {
		return "", errSandboxProfile
	}
	return profile, nil
}

const scopedOSQueryRules = `(allow process-exec (literal "/usr/sbin/diskutil"))
(allow file-read* (literal "/usr/sbin/diskutil"))
(allow mach-lookup (global-name "com.apple.diskmanagementd") (global-name "com.apple.DiskArbitration.diskarbitrationd"))
`
