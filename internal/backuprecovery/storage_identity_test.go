package backuprecovery

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func inventedStorageIdentity(external bool) storageIdentity {
	value := storageIdentity{
		volumeID: "11111111-1111-1111-1111-111111111111", partitionID: "22222222-2222-2222-2222-222222222222",
		filesystem: "apfs", transport: "Apple Fabric", location: storageInternal,
		mounted: true, mountDevice: 1, rootFileID: 2,
		physicalStores: []physicalStoreIdentity{{partitionID: "33333333-3333-3333-3333-333333333333", wholeDisk: "disk0"}},
	}
	if external {
		value.volumeID = "44444444-4444-4444-4444-444444444444"
		value.partitionID = "55555555-5555-5555-5555-555555555555"
		value.transport, value.location, value.physical, value.mountDevice = "USB", storageExternal, true, 7
		value.physicalStores = []physicalStoreIdentity{{partitionID: "66666666-6666-6666-6666-666666666666", wholeDisk: "disk8"}}
	}
	return value
}

func TestStorageSelectionRequiresIndependentSupportedEvidence(t *testing.T) {
	tests := []struct {
		name   string
		change func(*storageIdentity, *[]storageIdentity)
		want   error
	}{
		{"measured_internal_source_and_usb_destination", func(*storageIdentity, *[]storageIdentity) {}, nil},
		{"no_sources", func(_ *storageIdentity, s *[]storageIdentity) { *s = nil }, errStorageIdentity},
		{"too_many_sources", func(_ *storageIdentity, s *[]storageIdentity) {
			*s = slices.Repeat(*s, maxSelectedSources+1)
		}, errStorageIdentity},
		{"maximum_sources", func(_ *storageIdentity, s *[]storageIdentity) {
			*s = slices.Repeat(*s, maxSelectedSources)
		}, nil},
		{"same_volume", func(d *storageIdentity, s *[]storageIdentity) { (*s)[0].volumeID = d.volumeID }, errStorageConflict},
		{"same_partition", func(d *storageIdentity, s *[]storageIdentity) { (*s)[0].partitionID = d.partitionID }, errStorageConflict},
		{"shared_physical_store", func(d *storageIdentity, s *[]storageIdentity) {
			(*s)[0].physicalStores[0].partitionID = d.physicalStores[0].partitionID
		}, errStorageConflict},
		{"different_partitions_same_physical_disk", func(d *storageIdentity, s *[]storageIdentity) {
			(*s)[0].physicalStores[0].wholeDisk = d.physicalStores[0].wholeDisk
		}, errStorageConflict},
		{"overlap_in_second_source", func(d *storageIdentity, s *[]storageIdentity) {
			*s = append(*s, *d)
		}, errStorageConflict},
		{"overlap_in_second_backing_store", func(d *storageIdentity, s *[]storageIdentity) {
			d.physicalStores = append(d.physicalStores, (*s)[0].physicalStores[0])
		}, errStorageConflict},
		{"internal_destination", func(d *storageIdentity, _ *[]storageIdentity) {
			*d = inventedStorageIdentity(false)
		}, errStorageIdentity},
		{"external_source_unproven", func(_ *storageIdentity, s *[]storageIdentity) {
			(*s)[0].location, (*s)[0].transport, (*s)[0].physical = storageExternal, "USB", true
		}, errStorageIdentity},
		{"source_mapping_unknown", func(_ *storageIdentity, s *[]storageIdentity) {
			(*s)[0].physicalStores = nil
		}, errStorageIdentity},
		{"source_transport_unproven", func(_ *storageIdentity, s *[]storageIdentity) {
			(*s)[0].transport = "Virtual Interface"
		}, errStorageIdentity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			destination := inventedStorageIdentity(true)
			sources := []storageIdentity{inventedStorageIdentity(false)}
			tt.change(&destination, &sources)
			if err := validateStorageSelection(destination, sources); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v; want %v", err, tt.want)
			}
		})
	}
}

func TestStorageIdentityRejectsIncompleteAndUnsupportedEvidence(t *testing.T) {
	tests := []struct {
		name   string
		change func(*storageIdentity)
	}{
		{"not_mounted", func(v *storageIdentity) { v.mounted = false }},
		{"no_device", func(v *storageIdentity) { v.mountDevice = 0 }},
		{"no_root_inode", func(v *storageIdentity) { v.rootFileID = 0 }},
		{"missing_volume_id", func(v *storageIdentity) { v.volumeID = "" }},
		{"nil_uuid", func(v *storageIdentity) { v.volumeID = "00000000-0000-0000-0000-000000000000" }},
		{"display_name_is_not_id", func(v *storageIdentity) { v.volumeID = "Invented Drive" }},
		{"missing_partition_id", func(v *storageIdentity) { v.partitionID = "" }},
		{"network", func(v *storageIdentity) { v.filesystem = "smbfs" }},
		{"unsupported_filesystem", func(v *storageIdentity) { v.filesystem = "exfat" }},
		{"unknown_location", func(v *storageIdentity) { v.location = storageLocationUnknown }},
		{"unproven_transport", func(v *storageIdentity) { v.transport = "Thunderbolt" }},
		{"virtual_external_device", func(v *storageIdentity) { v.physical = false }},
		{"no_backing_stores", func(v *storageIdentity) { v.physicalStores = nil }},
		{"no_store_uuid", func(v *storageIdentity) { v.physicalStores[0].partitionID = "" }},
		{"duplicate_store", func(v *storageIdentity) { v.physicalStores = append(v.physicalStores, v.physicalStores[0]) }},
		{"no_whole_disk", func(v *storageIdentity) { v.physicalStores[0].wholeDisk = "" }},
		{"partition_not_whole_disk", func(v *storageIdentity) { v.physicalStores[0].wholeDisk = "disk8s1" }},
		{"device_path_not_reference", func(v *storageIdentity) { v.physicalStores[0].wholeDisk = "/dev/disk8" }},
		{"too_many_stores", func(v *storageIdentity) {
			for i := 1; i <= maxPhysicalStores; i++ {
				v.physicalStores = append(v.physicalStores, physicalStoreIdentity{
					partitionID: fmt.Sprintf("aaaaaaaa-aaaa-aaaa-aaaa-%012d", i), wholeDisk: fmt.Sprintf("disk%d", i),
				})
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity := inventedStorageIdentity(true)
			tt.change(&identity)
			if identity.valid() || sameStorageIdentity(identity, identity, false) {
				t.Fatal("invalid evidence accepted")
			}
			if err := validateStorageSelection(identity, []storageIdentity{inventedStorageIdentity(false)}); !errors.Is(err, errStorageIdentity) {
				t.Fatalf("error = %v; want identity unavailable", err)
			}
		})
	}
}

func TestStorageIdentityReconnectIsDistinctFromInFlightStability(t *testing.T) {
	tests := []struct {
		name          string
		change        func(*storageIdentity)
		wantReconnect bool
		wantInFlight  bool
	}{
		{"unchanged", func(*storageIdentity) {}, true, true},
		{"new_mount_device", func(v *storageIdentity) { v.mountDevice++ }, true, false},
		{"new_root_inode", func(v *storageIdentity) { v.rootFileID++ }, true, false},
		{"renumbered_disk", func(v *storageIdentity) { v.physicalStores[0].wholeDisk = "disk99" }, true, false},
		{"wrong_volume", func(v *storageIdentity) { v.volumeID = "77777777-7777-7777-7777-777777777777" }, false, false},
		{"wrong_partition", func(v *storageIdentity) { v.partitionID = "77777777-7777-7777-7777-777777777777" }, false, false},
		{"cloned_volume_other_store", func(v *storageIdentity) {
			v.physicalStores[0].partitionID = "77777777-7777-7777-7777-777777777777"
		}, false, false},
		{"extra_backing_store", func(v *storageIdentity) {
			v.physicalStores = append(v.physicalStores, physicalStoreIdentity{
				partitionID: "77777777-7777-7777-7777-777777777777", wholeDisk: "disk99",
			})
		}, false, false},
		{"disconnected", func(v *storageIdentity) { v.mounted = false }, false, false},
		{"location_changed", func(v *storageIdentity) { v.location = storageInternal; v.transport = "Apple Fabric" }, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expected, observed := inventedStorageIdentity(true), inventedStorageIdentity(true)
			tt.change(&observed)
			if got := sameStorageIdentity(expected, observed, false); got != tt.wantReconnect {
				t.Errorf("reconnect = %v; want %v", got, tt.wantReconnect)
			}
			if got := sameStorageIdentity(expected, observed, true); got != tt.wantInFlight {
				t.Errorf("in-flight = %v; want %v", got, tt.wantInFlight)
			}
		})
	}
}

func TestStorageIdentityBackingStoreOrderIsNotIdentity(t *testing.T) {
	first := inventedStorageIdentity(true)
	first.physicalStores = append(first.physicalStores, physicalStoreIdentity{
		partitionID: "77777777-7777-7777-7777-777777777777", wholeDisk: "disk9",
	})
	second := first
	second.physicalStores = slices.Clone(first.physicalStores)
	slices.Reverse(second.physicalStores)
	if !sameStorageIdentity(first, second, true) || !sameStorageIdentity(first, second, false) {
		t.Fatal("equivalent store ordering rejected")
	}
}

func TestStorageIdentityUUIDNormalization(t *testing.T) {
	valid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	for _, input := range []string{valid, strings.ToUpper(valid)} {
		got, err := normalizeStorageUUID(input)
		if err != nil || got != valid {
			t.Fatalf("UUID normalization failed: %v", err)
		}
	}
	for _, input := range []string{"", " " + valid, valid + "\n", "00000000-0000-0000-0000-000000000000", "Invented Volume", strings.ReplaceAll(valid, "-", "")} {
		if got, err := normalizeStorageUUID(input); got != "" || !errors.Is(err, errStorageIdentity) {
			t.Fatal("malformed UUID accepted")
		}
	}
}

func TestStorageIdentityCannotSerializePrivateEvidence(t *testing.T) {
	for _, value := range []any{inventedStorageIdentity(true), inventedStorageIdentity(false), physicalStoreIdentity{}} {
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != "{}" {
			t.Fatal("private identity evidence entered JSON projection")
		}
	}
	for _, err := range []error{errStorageIdentity, errStorageConflict} {
		if strings.Contains(err.Error(), "disk8") || strings.Contains(err.Error(), "44444444") {
			t.Fatal("private identity in error")
		}
	}
}
