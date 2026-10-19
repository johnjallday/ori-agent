package backuprecovery

import (
	"testing"
)

func diskIdentityFixture(internal bool) (nativeMountEvidence, diskMetadata, map[string]diskMetadata, map[string]diskMetadata) {
	transport, kind := "USB", "Physical"
	if internal {
		transport, kind = "Apple Fabric", "Unknown"
	}
	mount := nativeMountEvidence{device: "disk900000s1", mountPoint: "/Invented Mount", filesystem: "apfs", deviceID: 100, rootFileID: 2, local: true}
	volume := diskMetadata{device: mount.device, volumeID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", partitionID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", mountPoint: mount.mountPoint, filesystem: "apfs", transport: transport, internal: internal, mounted: true, mountedKnown: true, physicalStores: []string{"disk900001s2"}}
	store := diskMetadata{device: "disk900001s2", partitionID: "cccccccc-cccc-cccc-cccc-cccccccccccc", transport: transport, internal: internal, parentWholeDisk: "disk900001"}
	parent := diskMetadata{device: "disk900001", transport: transport, internal: internal, whole: true, physicalKind: kind}
	return mount, volume, map[string]diskMetadata{store.device: store}, map[string]diskMetadata{parent.device: parent}
}

func TestDiskIdentityJoinsOnlyExplicitNativeMapping(t *testing.T) {
	for _, internal := range []bool{false, true} {
		mount, volume, stores, parents := diskIdentityFixture(internal)
		result, err := composeDiskIdentity(mount, volume, stores, parents)
		if err != nil || !result.valid() || result.physical == internal || (result.location == storageInternal) != internal || result.mountDevice != 100 || result.rootFileID != 2 || len(result.physicalStores) != 1 || result.physicalStores[0].partitionID != "cccccccc-cccc-cccc-cccc-cccccccccccc" {
			t.Fatal("explicit mapping rejected or lost", err)
		}
	}
}

func TestDiskIdentityUsesNativeMountWhenOptionalFlagIsUnreported(t *testing.T) {
	mount, volume, stores, parents := diskIdentityFixture(false)
	volume.mountedKnown = false
	volume.mounted = false
	if _, err := composeDiskIdentity(mount, volume, stores, parents); err != nil {
		t.Fatal("independent native mount evidence rejected solely for an unreported optional flag")
	}
	volume.mountedKnown = true
	if _, err := composeDiskIdentity(mount, volume, stores, parents); err != errStorageIdentity {
		t.Fatal("explicit contradictory unmounted report accepted")
	}
	volume.mountedKnown = false
	mount.deviceID = 0
	if _, err := composeDiskIdentity(mount, volume, stores, parents); err != errStorageIdentity {
		t.Fatal("absent report became guessed mount authority")
	}
}

func TestDiskIdentityRejectsPartialContradictoryOrUnscopedEvidence(t *testing.T) {
	type change func(*nativeMountEvidence, *diskMetadata, map[string]diskMetadata, map[string]diskMetadata)
	cases := map[string]change{
		"not local":               func(m *nativeMountEvidence, _ *diskMetadata, _, _ map[string]diskMetadata) { m.local = false },
		"wrong native filesystem": func(m *nativeMountEvidence, _ *diskMetadata, _, _ map[string]diskMetadata) { m.filesystem = "nfs" },
		"missing native device":   func(m *nativeMountEvidence, _ *diskMetadata, _, _ map[string]diskMetadata) { m.deviceID = 0 },
		"missing root inode":      func(m *nativeMountEvidence, _ *diskMetadata, _, _ map[string]diskMetadata) { m.rootFileID = 0 },
		"path traversal": func(m *nativeMountEvidence, _ *diskMetadata, _, _ map[string]diskMetadata) {
			m.mountPoint = "/Invented/../Mount"
		},
		"path control": func(m *nativeMountEvidence, _ *diskMetadata, _, _ map[string]diskMetadata) {
			m.mountPoint = "/Invented\nMount"
		},
		"wrong native device name": func(m *nativeMountEvidence, _ *diskMetadata, _, _ map[string]diskMetadata) { m.device = "disk900000s2" },
		"native snapshot device": func(m *nativeMountEvidence, v *diskMetadata, _, _ map[string]diskMetadata) {
			m.device = "disk900000s1s1"
			v.device = m.device
		},
		"mount mismatch": func(_ *nativeMountEvidence, v *diskMetadata, _, _ map[string]diskMetadata) {
			v.mountPoint = "/Another Mount"
		},
		"not mounted":          func(_ *nativeMountEvidence, v *diskMetadata, _, _ map[string]diskMetadata) { v.mounted = false },
		"volume is whole disk": func(_ *nativeMountEvidence, v *diskMetadata, _, _ map[string]diskMetadata) { v.whole = true },
		"volume is snapshot":   func(_ *nativeMountEvidence, v *diskMetadata, _, _ map[string]diskMetadata) { v.snapshot = true },
		"wrong filesystem":     func(_ *nativeMountEvidence, v *diskMetadata, _, _ map[string]diskMetadata) { v.filesystem = "exfat" },
		"unsupported transport": func(_ *nativeMountEvidence, v *diskMetadata, _, _ map[string]diskMetadata) {
			v.transport = "Thunderbolt"
		},
		"missing volume ID":    func(_ *nativeMountEvidence, v *diskMetadata, _, _ map[string]diskMetadata) { v.volumeID = "" },
		"missing partition ID": func(_ *nativeMountEvidence, v *diskMetadata, _, _ map[string]diskMetadata) { v.partitionID = "" },
		"missing mapping":      func(_ *nativeMountEvidence, v *diskMetadata, _, _ map[string]diskMetadata) { v.physicalStores = nil },
		"duplicate store reference": func(_ *nativeMountEvidence, v *diskMetadata, _, _ map[string]diskMetadata) {
			v.physicalStores = append(v.physicalStores, v.physicalStores[0])
		},
		"unqueried store": func(_ *nativeMountEvidence, _ *diskMetadata, s, _ map[string]diskMetadata) { delete(s, "disk900001s2") },
		"extra store": func(_ *nativeMountEvidence, _ *diskMetadata, s, _ map[string]diskMetadata) {
			s["disk900002s2"] = s["disk900001s2"]
		},
		"wrong store response": func(_ *nativeMountEvidence, _ *diskMetadata, s, _ map[string]diskMetadata) {
			v := s["disk900001s2"]
			v.device = "disk900002s2"
			s["disk900001s2"] = v
		},
		"store is whole": func(_ *nativeMountEvidence, _ *diskMetadata, s, _ map[string]diskMetadata) {
			v := s["disk900001s2"]
			v.whole = true
			s["disk900001s2"] = v
		},
		"store location mismatch": func(_ *nativeMountEvidence, _ *diskMetadata, s, _ map[string]diskMetadata) {
			v := s["disk900001s2"]
			v.internal = true
			s["disk900001s2"] = v
		},
		"store transport mismatch": func(_ *nativeMountEvidence, _ *diskMetadata, s, _ map[string]diskMetadata) {
			v := s["disk900001s2"]
			v.transport = "SATA"
			s["disk900001s2"] = v
		},
		"missing store identity": func(_ *nativeMountEvidence, _ *diskMetadata, s, _ map[string]diskMetadata) {
			v := s["disk900001s2"]
			v.partitionID = ""
			s["disk900001s2"] = v
		},
		"unqueried parent": func(_ *nativeMountEvidence, _ *diskMetadata, _, p map[string]diskMetadata) { delete(p, "disk900001") },
		"extra parent": func(_ *nativeMountEvidence, _ *diskMetadata, _, p map[string]diskMetadata) {
			p["disk900002"] = p["disk900001"]
		},
		"wrong parent response": func(_ *nativeMountEvidence, _ *diskMetadata, _, p map[string]diskMetadata) {
			v := p["disk900001"]
			v.device = "disk900002"
			p["disk900001"] = v
		},
		"parent is partition": func(_ *nativeMountEvidence, _ *diskMetadata, _, p map[string]diskMetadata) {
			v := p["disk900001"]
			v.whole = false
			p["disk900001"] = v
		},
		"parent location mismatch": func(_ *nativeMountEvidence, _ *diskMetadata, _, p map[string]diskMetadata) {
			v := p["disk900001"]
			v.internal = true
			p["disk900001"] = v
		},
		"parent transport mismatch": func(_ *nativeMountEvidence, _ *diskMetadata, _, p map[string]diskMetadata) {
			v := p["disk900001"]
			v.transport = "SATA"
			p["disk900001"] = v
		},
		"virtual destination": func(_ *nativeMountEvidence, _ *diskMetadata, _, p map[string]diskMetadata) {
			v := p["disk900001"]
			v.physicalKind = "Virtual"
			p["disk900001"] = v
		},
		"unknown destination": func(_ *nativeMountEvidence, _ *diskMetadata, _, p map[string]diskMetadata) {
			v := p["disk900001"]
			v.physicalKind = "Unknown"
			p["disk900001"] = v
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m, v, s, p := diskIdentityFixture(false)
			mutate(&m, &v, s, p)
			if _, err := composeDiskIdentity(m, v, s, p); err != errStorageIdentity {
				t.Fatal("unsafe mapping accepted", err)
			}
		})
	}
	m, v, s, p := diskIdentityFixture(true)
	parent := p["disk900001"]
	parent.physicalKind = "Virtual"
	p[parent.device] = parent
	if _, err := composeDiskIdentity(m, v, s, p); err != errStorageIdentity {
		t.Fatal("Apple Fabric exception accepted a virtual source")
	}
}
