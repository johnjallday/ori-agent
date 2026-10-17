//go:build darwin || linux

package backuprecovery

import (
	"path/filepath"
	"strconv"
	"strings"
)

// metadataBrokerSandboxProfile extends metadata-only traversal with just the
// encrypted namespaces. The root/data/index/locks namespace is never readable.
// This profile still needs scoped OS-query IPC and production helper binding.
func metadataBrokerSandboxProfile(executable string, sources []string, repository string) (string, error) {
	if !validMetadataPath(repository) || repository == executable || strings.HasPrefix(executable, repository+"/") {
		return "", errSandboxProfile
	}
	for _, source := range sources {
		if repository == source || strings.HasPrefix(repository, source+"/") || strings.HasPrefix(source, repository+"/") {
			return "", errSandboxProfile
		}
	}
	profile, err := directoryMetadataSandboxProfile(executable, sources)
	if err != nil {
		return "", err
	}
	config, keys, snapshots := strconv.Quote(filepath.Join(repository, "config")), strconv.Quote(filepath.Join(repository, "keys")), strconv.Quote(filepath.Join(repository, "snapshots"))
	// Explicit deny protects against overlap with a loader-dependency allow.
	profile += "(deny file-read-data (require-all (subpath " + strconv.Quote(repository) + ") (require-not (literal " + config + ")) (require-not (subpath " + keys + ")) (require-not (subpath " + snapshots + "))))\n"
	profile += "(allow file-read-data (literal " + config + ") (subpath " + keys + ") (subpath " + snapshots + "))\n"
	if len(profile) > maxSandboxProfileBytes {
		return "", errSandboxProfile
	}
	return profile, nil
}

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

// directoryMetadataSandboxProfile is the directory-metadata-only policy, not
// the completed diskutil/broker/provider policy. It is pure formatting, not
// approval, executable verification or evidence of available containment.
// Send the private profile through an owned pipe, never command-line text.
func directoryMetadataSandboxProfile(executable string, sources []string) (string, error) {
	if !validMetadataPath(executable) || len(sources) == 0 || len(sources) > maxSelectedSources {
		return "", errSandboxProfile
	}
	var profile strings.Builder
	profile.WriteString(`(version 1)
(deny default)
(deny network*)
(allow process-fork)
(allow sysctl-read)
(allow file-read-metadata)
(allow file-read-data (literal "/"))
(allow file-read* (subpath "/System/Library") (subpath "/usr/lib")
 (subpath "/private/var/db/timezone") (literal "/private/etc/localtime")
 (literal "/private/etc/passwd") (literal "/dev/null")
 (literal "/dev/urandom") (literal "/dev/random") (subpath "/dev/fd"))
`)
	quoted := strconv.Quote(executable)
	profile.WriteString("(allow process-exec (literal " + quoted + "))\n(allow file-read* (literal " + quoted + "))\n")
	seen := make(map[string]bool, len(sources))
	for _, source := range sources {
		if !validMetadataPath(source) || seen[source] || executable == source || strings.HasPrefix(executable, source+"/") {
			return "", errSandboxProfile
		}
		seen[source] = true
		// Explicit denial matters even if the selected folder overlaps a system
		// dependency. Failure to load a dependency is unavailability, not license
		// to relax source-content protection.
		profile.WriteString("(deny file-read-data (subpath " + strconv.Quote(source) + "))\n")
	}
	if profile.Len() > maxSandboxProfileBytes {
		return "", errSandboxProfile
	}
	return profile.String(), nil
}
