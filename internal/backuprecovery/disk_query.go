package backuprecovery

import (
	"context"
	"slices"
)

// scopedDiskQuery is private helper composition. A real implementation must
// select only /usr/sbin/diskutil info -plist /dev/<validated device>, under the
// bounded helper's containment. A response or label cannot select an operation.
// The seam is for scoped planning tests, not a provider configuration callback.
type scopedDiskQuery func(context.Context, string) (diskMetadata, error)

// readDiskIdentity queries only the descriptor-observed volume, its explicitly
// referenced APFS partitions and their explicitly returned parent whole disks.
// It never enumerates mounts/devices or guesses a parent by trimming strings.
func readDiskIdentity(ctx context.Context, mount nativeMountEvidence, query scopedDiskQuery) (storageIdentity, error) {
	bad := func() (storageIdentity, error) { return storageIdentity{}, errStorageIdentity }
	if ctx == nil || ctx.Err() != nil || query == nil || !mount.local || mount.filesystem != "apfs" || mount.deviceID == 0 || mount.rootFileID == 0 ||
		!validNativeMountPoint(mount.mountPoint) || !diskPartitionReference.MatchString(mount.device) {
		return bad()
	}
	volume, err := query(ctx, mount.device)
	if err != nil || ctx.Err() != nil || volume.device != mount.device || volume.mountPoint != mount.mountPoint || volume.filesystem != "apfs" || (volume.mountedKnown && !volume.mounted) || volume.whole || volume.snapshot ||
		!validStorageUUID(volume.volumeID) || !validStorageUUID(volume.partitionID) || len(volume.physicalStores) == 0 || len(volume.physicalStores) > maxPhysicalStores ||
		(volume.internal && volume.transport != "Apple Fabric") || (!volume.internal && volume.transport != "USB") {
		return bad()
	}
	references := slices.Clone(volume.physicalStores)
	slices.Sort(references)
	for i, reference := range references {
		if !diskPartitionReference.MatchString(reference) || (i > 0 && reference == references[i-1]) {
			return bad()
		}
	}
	stores, parents := make(map[string]diskMetadata, len(references)), make(map[string]diskMetadata, len(references))
	for _, reference := range references {
		if ctx.Err() != nil {
			return bad()
		}
		store, err := query(ctx, reference)
		if err != nil || ctx.Err() != nil || store.device != reference || store.whole || store.snapshot || !validStorageUUID(store.partitionID) ||
			store.internal != volume.internal || store.transport != volume.transport || !wholeDiskReference.MatchString(store.parentWholeDisk) {
			return bad()
		}
		stores[reference] = store
		if _, ok := parents[store.parentWholeDisk]; ok {
			continue
		}
		parent, err := query(ctx, store.parentWholeDisk)
		if err != nil || ctx.Err() != nil || parent.device != store.parentWholeDisk || !parent.whole || parent.snapshot || parent.internal != volume.internal || parent.transport != volume.transport ||
			(parent.physicalKind != "Physical" && (!volume.internal || parent.physicalKind != "Unknown")) {
			return bad()
		}
		parents[parent.device] = parent
	}
	return composeDiskIdentity(mount, volume, stores, parents)
}
