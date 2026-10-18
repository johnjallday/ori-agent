//go:build darwin && arm64

package backuprecovery

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

type darwinMetadataProvider struct {
	helper     *ownBackupHelper
	executable string
	root       localRoot
}

func newPlatformProvider(ctx context.Context, executable, dataRoot string) (localMetadataProvider, error) {
	if ctx == nil || ctx.Err() != nil || !validMetadataPath(executable) {
		return nil, errTrustedExecutable
	}
	root, err := newLocalRoot(dataRoot)
	if err != nil {
		return nil, errLocalState
	}
	// Product/RSR build is not kern.osversion (the tested host's base kernel is
	// 25D2128). Query just the fixed protected OS metadata tool, never disk inventory.
	versionTool, err := verifySystemExecutable(systemProductMetadata)
	if err != nil {
		return nil, errProviderUnavailable
	}
	defer versionTool.close()
	build, err := runReadProcess(ctx, readProcessSpec{executable: versionTool.path, arguments: []string{"-buildVersion"}, directory: "/", environment: []string{"HOME=" + root.path, "TMPDIR=" + root.path, "PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"}, stdoutLimit: 128, timeout: childDrainLimit})
	if err != nil || !bytes.Equal(build, []byte("25D771280a\n")) || !versionTool.unchanged() {
		return nil, errProviderUnavailable
	}
	helper, err := verifyOwnBackupHelper(ctx)
	if err != nil {
		return nil, err
	}
	return &darwinMetadataProvider{helper: helper, executable: executable, root: root}, nil
}
func (provider *darwinMetadataProvider) close() {
	if provider != nil {
		provider.helper.close()
	}
}

func (provider *darwinMetadataProvider) broker(ctx context.Context, selection metadataSelection, scratch string) (selectedMetadata, error) {
	sources := make([]string, 0, len(selection.Sources))
	for _, source := range selection.Sources {
		sources = append(sources, source.Path)
	}
	profile, err := metadataBrokerSandboxProfile(provider.helper.executable.path, sources, selection.RepositoryPath)
	if err != nil {
		return selectedMetadata{}, err
	}
	profile += scopedOSQueryRules
	if len(profile) > maxSandboxProfileBytes {
		return selectedMetadata{}, errSandboxProfile
	}
	request, err := json.Marshal(brokerHelperRequest{Selection: selection, Scratch: scratch})
	if err != nil || len(request) > maxSettingsBytes {
		return selectedMetadata{}, errReadProcess
	}
	out, err := provider.helper.run(ctx, "broker-v1", profile, scratch, request, nil)
	if err != nil {
		return selectedMetadata{}, err
	}
	defer clear(out)
	return decodeBrokerResult(out, len(selection.Sources))
}

func (provider *darwinMetadataProvider) read(parent context.Context, configuration localConfiguration, usePassword backupPasswordUse) (result providerReadResult, err error) {
	if parent == nil || parent.Err() != nil || provider == nil || provider.helper == nil || usePassword == nil || !configuration.valid(false) {
		return providerReadResult{}, errMetadataRead
	}
	ctx, cancel := context.WithTimeout(parent, completeReadLimit)
	defer cancel()
	if !provider.root.check() {
		return providerReadResult{}, errLocalState
	}
	// Only the installation-owned app root is touched in the host. Selected
	// paths are inspected exclusively in the separately cancellable broker.
	root, err := os.OpenRoot(provider.root.path)
	if err != nil {
		return providerReadResult{}, errLocalState
	}
	defer func() { _ = root.Close() }()
	name := ".backup-read-" + uuid.NewString()
	if root.Mkdir(name, 0700) != nil {
		return providerReadResult{}, errLocalState
	}
	owned, err := root.Lstat(name)
	if err != nil {
		return providerReadResult{}, errLocalState
	}
	defer func() {
		current, checkErr := root.Lstat(name)
		if checkErr != nil || !os.SameFile(owned, current) || current.Mode()&os.ModeSymlink != 0 || root.RemoveAll(name) != nil {
			result = providerReadResult{}
			err = errLocalState
		}
	}()
	scratch := filepath.Join(provider.root.path, name)
	selection := metadataSelection{Sources: configuration.Sources, VolumePath: configuration.VolumePath, RepositoryPath: configuration.RepositoryPath, Destination: configuration.Destination, RepositoryFileID: configuration.RepositoryFileID}
	before, err := provider.broker(ctx, selection, scratch)
	if err != nil {
		return providerReadResult{}, err
	}
	observedAt := time.Now().UTC()
	sources := make([]string, 0, len(configuration.Sources))
	for _, source := range configuration.Sources {
		sources = append(sources, source.Path)
	}
	request, err := readerHelperInput(readerHelperRequest{Executable: provider.executable, Scratch: scratch, Sources: sources, Repository: configuration.RepositoryPath}, before.bundle)
	if err != nil {
		return providerReadResult{}, err
	}
	out, err := provider.helper.run(ctx, "reader-v1", "", scratch, request, usePassword)
	if err != nil {
		return providerReadResult{}, err
	}
	defer clear(out)
	metadata, err := decodeLocalJSON[readerHelperResponse](out, maxPrivateStateBytes)
	if err != nil || !metadataID(metadata.RepositoryID) || len(metadata.Snapshots) > maxSnapshotCount {
		return providerReadResult{}, errMetadataInvalid
	}
	if configuration.RepositoryID != "" && configuration.RepositoryID != metadata.RepositoryID {
		return providerReadResult{}, errIdentityMismatch
	}
	after, err := provider.broker(ctx, selection, scratch)
	if err != nil {
		return providerReadResult{}, err
	}
	if !before.sameSelection(after) || len(before.bundle.entries) == 0 || len(after.bundle.entries) == 0 || !bytes.Equal(before.bundle.entries[0].data, after.bundle.entries[0].data) {
		return providerReadResult{}, errIdentityMismatch
	}
	if ctx.Err() != nil || !provider.root.check() {
		return providerReadResult{}, errReadProcessCancelled
	}
	// Validation evidence is separate from reviewed input. No receipt, settings,
	// monitoring intent or stored observation is created by this return value.
	configuration.RepositoryID = metadata.RepositoryID
	configuration.RepositoryFileID = before.destination.fileID
	configuration.Destination = durableStorage(before.destination.storage)
	configuration.Sources = append([]localSource(nil), configuration.Sources...)
	for i := range configuration.Sources {
		configuration.Sources[i].DirectoryFileID = before.sources[i].fileID
		configuration.Sources[i].Storage = durableStorage(before.sources[i].storage)
	}
	if !configuration.valid(true) {
		return providerReadResult{}, errMetadataInvalid
	}
	return providerReadResult{configuration: configuration, observedAt: observedAt, snapshots: metadata.Snapshots, capacity: before.capacity}, nil
}
