package backuprecovery

import (
	"path"
	"unicode"
	"unicode/utf8"
)

// nativeMountEvidence must come from a descriptor-bound native statfs and a
// separately verified mount-root descriptor. This private value cannot be
// supplied by an API. Constructing it in a test proves no actual OS collection.
type nativeMountEvidence struct {
	device, mountPoint, filesystem string
	deviceID, rootFileID           uint64
	local                          bool
}

// composeDiskIdentity joins precisely the volume's referenced physical stores
// and their parent disks. No display name, generic free-space field, missing
// boolean, guessed parent or unqueried extra device can establish independence.
// The collector still must anchor the selected path and recheck live evidence.
func composeDiskIdentity(mount nativeMountEvidence, volume diskMetadata, stores, parents map[string]diskMetadata) (storageIdentity, error) {
	bad := func() (storageIdentity, error) { return storageIdentity{}, errStorageIdentity }
	if !mount.local || mount.filesystem != "apfs" || mount.deviceID == 0 || mount.rootFileID == 0 ||
		!validNativeMountPoint(mount.mountPoint) || !diskPartitionReference.MatchString(mount.device) ||
		volume.device != mount.device || volume.mountPoint != mount.mountPoint || volume.filesystem != "apfs" ||
		(volume.mountedKnown && !volume.mounted) || volume.whole || volume.snapshot || !validStorageUUID(volume.volumeID) || !validStorageUUID(volume.partitionID) ||
		len(volume.physicalStores) == 0 || len(volume.physicalStores) > maxPhysicalStores || len(stores) != len(volume.physicalStores) || len(parents) == 0 || len(parents) > maxPhysicalStores {
		return bad()
	}
	out := storageIdentity{volumeID: volume.volumeID, partitionID: volume.partitionID, filesystem: "apfs", transport: volume.transport,
		physical: true, mounted: true, mountDevice: mount.deviceID, rootFileID: mount.rootFileID, location: storageExternal}
	if volume.internal {
		out.location = storageInternal
	}
	if volume.internal && volume.transport != "Apple Fabric" || !volume.internal && volume.transport != "USB" {
		return bad()
	}
	seenStores, seenParents := map[string]bool{}, map[string]bool{}
	for _, reference := range volume.physicalStores {
		store, ok := stores[reference]
		if !ok || !diskPartitionReference.MatchString(reference) || store.device != reference || seenStores[reference] || store.whole || store.snapshot ||
			!validStorageUUID(store.partitionID) || !wholeDiskReference.MatchString(store.parentWholeDisk) ||
			store.internal != volume.internal || store.transport != volume.transport {
			return bad()
		}
		seenStores[reference] = true
		parent, ok := parents[store.parentWholeDisk]
		if !ok || parent.device != store.parentWholeDisk || !parent.whole || parent.snapshot || parent.internal != volume.internal || parent.transport != volume.transport {
			return bad()
		}
		switch parent.physicalKind {
		case "Physical":
		case "Unknown":
			if !volume.internal {
				return bad()
			}
			out.physical = false
		default:
			return bad()
		}
		seenParents[parent.device] = true
		out.physicalStores = append(out.physicalStores, physicalStoreIdentity{partitionID: store.partitionID, wholeDisk: parent.device})
	}
	if len(seenParents) != len(parents) || !out.valid() {
		return bad()
	}
	return out, nil
}

func validNativeMountPoint(value string) bool {
	if !path.IsAbs(value) || path.Clean(value) != value || len(value) > maxSourcePathBytes || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == unicode.ReplacementChar {
			return false
		}
	}
	return true
}
