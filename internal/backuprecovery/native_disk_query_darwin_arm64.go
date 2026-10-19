//go:build darwin && arm64

package backuprecovery

import "context"

// nativeDiskReadSpec describes ONE exact OS query. This is not a command API:
// neither operation, executable, options nor environment comes from a request.
// directory must be the helper's private Ori-owned scratch, not a selected path.
func nativeDiskReadSpec(device, directory string) (readProcessSpec, error) {
	if !diskDeviceReference.MatchString(device) || !validMetadataPath(directory) {
		return readProcessSpec{}, errStorageIdentity
	}
	return readProcessSpec{executable: systemExecutablePath(systemDiskMetadataReader), arguments: []string{"info", "-plist", "/dev/" + device},
		directory: directory, environment: []string{"HOME=" + directory, "TMPDIR=" + directory, "PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "TZ=UTC", "GOMAXPROCS=2"},
		stdoutLimit: maxDiskMetadataBytes, timeout: scopedQueryLimit, pipelineMember: true}, nil
}

// queryNativeDisk must run ONLY inside the bounded, contained metadata helper.
// It is private and has no registered server/service/HTTP caller. The helper's
// complete deadline also bounds all sequential OS queries and final rechecks.
func queryNativeDisk(ctx context.Context, device, directory string) (diskMetadata, error) {
	if ctx == nil || ctx.Err() != nil {
		return diskMetadata{}, errStorageIdentity
	}
	spec, err := nativeDiskReadSpec(device, directory)
	if err != nil {
		return diskMetadata{}, errStorageIdentity
	}
	checked, err := verifySystemExecutable(systemDiskMetadataReader)
	if err != nil {
		return diskMetadata{}, errStorageIdentity
	}
	defer checked.close()
	out, err := runReadProcess(ctx, spec)
	defer clear(out)
	if err != nil || ctx.Err() != nil || !checked.unchanged() {
		return diskMetadata{}, errStorageIdentity
	}
	return decodeDiskMetadata(out, device)
}
