package backuprecovery

import (
	"errors"
	"regexp"
	"strings"
)

// These private evidence types describe the candidate macOS identity policy.
// They do not discover devices, resolve paths, authorize access, or grant a
// caller permission. Only a future compiled verifier may construct them from
// scoped OS evidence, never from an HTTP request or portable workspace state.
// Persistent volume/store identities are separate from transient mount identity.
type storageIdentity struct {
	volumeID       string
	partitionID    string
	filesystem     string
	transport      string
	location       storageLocation
	physical       bool
	mounted        bool
	mountDevice    uint64
	rootFileID     uint64
	physicalStores []physicalStoreIdentity
}

type storageLocation uint8

const (
	storageLocationUnknown storageLocation = iota
	storageInternal
	storageExternal
)

type physicalStoreIdentity struct {
	partitionID string
	// wholeDisk is a current OS device reference, not a persistent identifier.
	// Compare it between live source/destination evidence, never across restart.
	wholeDisk string
}

const (
	maxPhysicalStores  = 4
	maxSelectedSources = 16
)

var (
	errStorageIdentity = errors.New("backup storage identity is unavailable")
	errStorageConflict = errors.New("backup destination is not independent of its sources")
	storageUUID        = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	wholeDiskReference = regexp.MustCompile(`^disk[0-9]{1,6}$`)
)

func validStorageUUID(id string) bool {
	// The OS collector normalizes UUID case explicitly. The evaluator does not
	// silently normalize untrusted/persisted evidence or accept nil identities.
	return storageUUID.MatchString(id) && id != "00000000-0000-0000-0000-000000000000"
}

func (identity storageIdentity) valid() bool {
	if !identity.mounted || identity.mountDevice == 0 || identity.rootFileID == 0 ||
		identity.filesystem != "apfs" || !validStorageUUID(identity.volumeID) ||
		!validStorageUUID(identity.partitionID) || len(identity.physicalStores) == 0 ||
		len(identity.physicalStores) > maxPhysicalStores {
		return false
	}
	switch identity.location {
	case storageExternal:
		if !identity.physical || identity.transport != "USB" {
			return false
		}
	case storageInternal:
		// On the tested Apple Silicon host, the internal APFS store mapping is
		// explicit, but diskutil's whole-disk VirtualOrPhysical is "Unknown".
		// This narrow internal Apple Fabric case does not make an unknown or
		// virtual external disk acceptable. Other source transports are unproven.
		if identity.transport != "Apple Fabric" {
			return false
		}
	default:
		return false
	}
	seen := make(map[string]bool, len(identity.physicalStores))
	for _, store := range identity.physicalStores {
		if !validStorageUUID(store.partitionID) || !wholeDiskReference.MatchString(store.wholeDisk) || seen[store.partitionID] {
			return false
		}
		seen[store.partitionID] = true
	}
	return true
}

// validateStorageSelection rejects source/destination overlap using both stable
// physical-store UUIDs and contemporaneous whole-disk references. Different APFS
// volumes/partitions can still share one physical failure domain. Source content
// is never needed. Path/mount/repository verification remains a separate gate.
func validateStorageSelection(destination storageIdentity, sources []storageIdentity) error {
	if !destination.valid() || destination.location != storageExternal || len(sources) == 0 || len(sources) > maxSelectedSources {
		return errStorageIdentity
	}
	for _, source := range sources {
		if !source.valid() {
			return errStorageIdentity
		}
		if source.volumeID == destination.volumeID || source.partitionID == destination.partitionID {
			return errStorageConflict
		}
		for _, sourceStore := range source.physicalStores {
			for _, destinationStore := range destination.physicalStores {
				if sourceStore.partitionID == destinationStore.partitionID || sourceStore.wholeDisk == destinationStore.wholeDisk {
					return errStorageConflict
				}
			}
		}
		// V1's measured source support is internal APFS on Apple Silicon, not
		// arbitrary external, network, image, pooled, or virtual source storage.
		if source.location != storageInternal {
			return errStorageIdentity
		}
	}
	return nil
}

// sameStorageIdentity compares stable identity across a reviewed reconnect. For
// one in-flight read, the mount device, root inode and device mapping must also
// remain unchanged. Ordering is not identity; duplicate stores are invalid.
// This is not proof against hardware deliberately cloning all persistent IDs.
func sameStorageIdentity(expected, observed storageIdentity, withinRead bool) bool {
	if !expected.valid() || !observed.valid() || expected.volumeID != observed.volumeID ||
		expected.partitionID != observed.partitionID || expected.filesystem != observed.filesystem ||
		expected.transport != observed.transport || expected.location != observed.location ||
		expected.physical != observed.physical || len(expected.physicalStores) != len(observed.physicalStores) {
		return false
	}
	if withinRead && (expected.mountDevice != observed.mountDevice || expected.rootFileID != observed.rootFileID) {
		return false
	}
	for _, wanted := range expected.physicalStores {
		matched := false
		for _, actual := range observed.physicalStores {
			if wanted.partitionID == actual.partitionID && (!withinRead || wanted.wholeDisk == actual.wholeDisk) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// normalizeStorageUUID is only for the OS collector's documented UUID fields.
// It rejects malformed values; it is not a path, permission, or trust resolver.
func normalizeStorageUUID(value string) (string, error) {
	value = strings.ToLower(value)
	if !validStorageUUID(value) {
		return "", errStorageIdentity
	}
	return value, nil
}
