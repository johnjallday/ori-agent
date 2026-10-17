//go:build darwin && arm64

package backuprecovery

import (
	"context"
	"os"
	"slices"
	"syscall"

	"golang.org/x/sys/unix"
)

// Selection is private reviewed-input data, not access authority. In particular
// the broker receives no credential reference, password, command or program.
type metadataSelection struct {
	Sources          []localSource `json:"sources"`
	VolumePath       string        `json:"volume_path"`
	RepositoryPath   string        `json:"repository_path"`
	Destination      *localStorage `json:"destination,omitempty"`
	RepositoryFileID uint64        `json:"repository_file_id,omitempty"`
}

type selectedDirectoryEvidence struct {
	storage storageIdentity
	fileID  uint64
}
type selectedMetadata struct {
	destination selectedDirectoryEvidence
	sources     []selectedDirectoryEvidence
	capacity    *localCapacity
	bundle      metadataBundle
}

func (selection metadataSelection) valid() bool {
	if !validMetadataPath(selection.VolumePath) || !validMetadataPath(selection.RepositoryPath) || !pathsOverlap(selection.VolumePath, selection.RepositoryPath) ||
		(len(selection.RepositoryPath) < len(selection.VolumePath)) || len(selection.Sources) == 0 || len(selection.Sources) > maxSelectedSources ||
		(selection.Destination != nil && !selection.Destination.valid()) {
		return false
	}
	seen := map[string]bool{}
	for _, source := range selection.Sources {
		if !validMetadataPath(source.Path) || seen[source.Path] || pathsOverlap(source.Path, selection.VolumePath) || pathsOverlap(source.Path, selection.RepositoryPath) || (source.Storage != nil && !source.Storage.valid()) {
			return false
		}
		seen[source.Path] = true
	}
	return true
}

func storageMatchesLocal(expected *localStorage, observed storageIdentity) bool {
	if expected == nil {
		return observed.valid()
	}
	if !expected.valid() || !observed.valid() || expected.VolumeID != observed.volumeID || expected.PartitionID != observed.partitionID || len(expected.StoreIDs) != len(observed.physicalStores) {
		return false
	}
	for _, store := range observed.physicalStores {
		if !slices.Contains(expected.StoreIDs, store.partitionID) {
			return false
		}
	}
	return true
}

func durableStorage(observed storageIdentity) *localStorage {
	result := &localStorage{VolumeID: observed.volumeID, PartitionID: observed.partitionID}
	for _, store := range observed.physicalStores {
		result.StoreIDs = append(result.StoreIDs, store.partitionID)
	}
	slices.Sort(result.StoreIDs)
	return result
}

// collectSelectedMetadata must run in a bounded, sandboxed group-leading broker.
// Its only data reads are the encrypted metadata namespaces. OS queries are
// scoped to native-observed mounts; repeated source mounts share query results.
// Held descriptors and fresh OS mappings are checked again after collection.
func collectSelectedMetadata(ctx context.Context, selection metadataSelection, scratch string) (selectedMetadata, error) {
	bad := func() (selectedMetadata, error) { return selectedMetadata{}, errStorageIdentity }
	if ctx == nil || ctx.Err() != nil || !selection.valid() || syscall.Getpgrp() != os.Getpid() {
		return bad()
	}
	var held []*nativeDirectory
	defer func() {
		for _, directory := range held {
			directory.close()
		}
	}()
	open := func(path string) (*nativeDirectory, error) {
		directory, err := readNativeDirectory(path)
		if err == nil {
			held = append(held, directory)
		}
		return directory, err
	}
	volume, err := open(selection.VolumePath)
	if err != nil {
		var info unix.Stat_t
		if unix.Lstat(selection.VolumePath, &info) == unix.ENOENT {
			return selectedMetadata{}, errDestinationAbsent
		}
		return bad()
	}
	if volume.mount.mountPoint != selection.VolumePath || volume.identity.inode != volume.mount.rootFileID {
		return selectedMetadata{}, errDestinationAbsent
	}
	repository, err := open(selection.RepositoryPath)
	if err != nil {
		return bad()
	}
	if repository.mount != volume.mount || (selection.RepositoryFileID != 0 && repository.identity.inode != selection.RepositoryFileID) {
		return selectedMetadata{}, errIdentityMismatch
	}
	sources := make([]*nativeDirectory, 0, len(selection.Sources))
	for _, source := range selection.Sources {
		directory, err := open(source.Path)
		if err != nil {
			return bad()
		}
		if source.DirectoryFileID != 0 && directory.identity.inode != source.DirectoryFileID {
			return selectedMetadata{}, errIdentityMismatch
		}
		sources = append(sources, directory)
	}
	// Fresh caches per phase: no cached physical mapping can substitute for the
	// post-collection check. Within a phase, query only explicit native devices.
	observe := func() (selectedMetadata, error) {
		cache := map[nativeMountEvidence]storageIdentity{}
		read := func(directory *nativeDirectory) (storageIdentity, error) {
			if cached, ok := cache[directory.mount]; ok {
				return cached, nil
			}
			identity, err := readDiskIdentity(ctx, directory.mount, func(ctx context.Context, device string) (diskMetadata, error) {
				return queryNativeDisk(ctx, device, scratch)
			})
			if err == nil {
				cache[directory.mount] = identity
			}
			return identity, err
		}
		destination, err := read(repository)
		if err != nil {
			return bad()
		}
		if !storageMatchesLocal(selection.Destination, destination) {
			return selectedMetadata{}, errIdentityMismatch
		}
		result := selectedMetadata{destination: selectedDirectoryEvidence{storage: destination, fileID: repository.identity.inode}}
		identities := make([]storageIdentity, 0, len(sources))
		for i, source := range sources {
			identity, err := read(source)
			if err != nil {
				return bad()
			}
			if !storageMatchesLocal(selection.Sources[i].Storage, identity) {
				return selectedMetadata{}, errIdentityMismatch
			}
			identities = append(identities, identity)
			result.sources = append(result.sources, selectedDirectoryEvidence{storage: identity, fileID: source.identity.inode})
		}
		if err := validateStorageSelection(destination, identities); err != nil {
			return selectedMetadata{}, err
		}
		return result, nil
	}
	before, err := observe()
	if err != nil {
		return selectedMetadata{}, err
	}
	anchor, err := newMetadataAnchor(selection.RepositoryPath, repository.identity)
	if err != nil {
		return bad()
	}
	defer anchor.close()
	bundle, err := anchor.collect(ctx)
	if err != nil {
		return selectedMetadata{}, errMetadataBundle
	}
	for _, directory := range held {
		if !directory.unchanged() {
			return bad()
		}
	}
	after, err := observe()
	if err != nil {
		return selectedMetadata{}, err
	}
	if !before.sameSelection(after) {
		return selectedMetadata{}, errIdentityMismatch
	}
	for _, directory := range held {
		if !directory.unchanged() {
			return bad()
		}
	}
	if repository.capacity != nil {
		before.capacity = &localCapacity{TotalBytes: repository.capacity.total, AvailableBytes: repository.capacity.available}
	}
	before.bundle = bundle
	return before, nil
}

func (evidence selectedMetadata) sameSelection(other selectedMetadata) bool {
	if evidence.destination.fileID == 0 || evidence.destination.fileID != other.destination.fileID || !sameStorageIdentity(evidence.destination.storage, other.destination.storage, true) || len(evidence.sources) != len(other.sources) {
		return false
	}
	for i, source := range evidence.sources {
		if source.fileID == 0 || source.fileID != other.sources[i].fileID || !sameStorageIdentity(source.storage, other.sources[i].storage, true) {
			return false
		}
	}
	return true
}
