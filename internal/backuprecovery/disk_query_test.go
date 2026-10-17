package backuprecovery

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func diskQueryFixture() (nativeMountEvidence, map[string]diskMetadata) {
	mount, volume, stores, parents := diskIdentityFixture(false)
	rows := map[string]diskMetadata{volume.device: volume}
	for name, value := range stores {
		rows[name] = value
	}
	for name, value := range parents {
		rows[name] = value
	}
	return mount, rows
}

func TestScopedDiskQueryVisitsOnlyExplicitReferences(t *testing.T) {
	mount, rows := diskQueryFixture()
	var calls []string
	query := func(ctx context.Context, device string) (diskMetadata, error) {
		calls = append(calls, device)
		row, ok := rows[device]
		if !ok {
			t.Fatal("unscoped query")
		}
		return row, nil
	}
	result, err := readDiskIdentity(t.Context(), mount, query)
	if err != nil || !result.valid() || !reflect.DeepEqual(calls, []string{"disk900000s1", "disk900001s2", "disk900001"}) {
		t.Fatal("explicit query plan failed")
	}
	// Two explicit stores may share their whole disk. Do not query that parent
	// twice or derive additional disks from a partition's textual device number.
	volume := rows[mount.device]
	volume.physicalStores = append(volume.physicalStores, "disk900001s3")
	rows[mount.device] = volume
	store := rows["disk900001s2"]
	store.device = "disk900001s3"
	store.partitionID = "dddddddd-dddd-dddd-dddd-dddddddddddd"
	rows[store.device] = store
	calls = nil
	result, err = readDiskIdentity(t.Context(), mount, query)
	if err != nil || len(result.physicalStores) != 2 || !reflect.DeepEqual(calls, []string{"disk900000s1", "disk900001s2", "disk900001", "disk900001s3"}) {
		t.Fatal("shared parent query was expanded or lost")
	}
}

func TestScopedDiskQueryRejectsUnsafeReferencesBeforeQuery(t *testing.T) {
	cases := map[string]func(*nativeMountEvidence, map[string]diskMetadata){
		"native device":          func(m *nativeMountEvidence, _ map[string]diskMetadata) { m.device = "-all" },
		"native mount":           func(m *nativeMountEvidence, _ map[string]diskMetadata) { m.mountPoint = "/a/../b" },
		"unsupported filesystem": func(m *nativeMountEvidence, _ map[string]diskMetadata) { m.filesystem = "nfs" },
		"snapshot volume": func(m *nativeMountEvidence, r map[string]diskMetadata) {
			v := r[m.device]
			v.snapshot = true
			r[m.device] = v
		},
		"unsupported transport": func(m *nativeMountEvidence, r map[string]diskMetadata) {
			v := r[m.device]
			v.transport = "SATA"
			r[m.device] = v
		},
		"store option": func(m *nativeMountEvidence, r map[string]diskMetadata) {
			v := r[m.device]
			v.physicalStores = []string{"-all"}
			r[m.device] = v
		},
		"store traversal": func(m *nativeMountEvidence, r map[string]diskMetadata) {
			v := r[m.device]
			v.physicalStores = []string{"disk900001s2/../../etc"}
			r[m.device] = v
		},
		"duplicate stores": func(m *nativeMountEvidence, r map[string]diskMetadata) {
			v := r[m.device]
			v.physicalStores = append(v.physicalStores, v.physicalStores[0])
			r[m.device] = v
		},
		"missing stores": func(m *nativeMountEvidence, r map[string]diskMetadata) {
			v := r[m.device]
			v.physicalStores = nil
			r[m.device] = v
		},
		"excess stores": func(m *nativeMountEvidence, r map[string]diskMetadata) {
			v := r[m.device]
			v.physicalStores = make([]string, maxPhysicalStores+1)
			r[m.device] = v
		},
		"parent option": func(_ *nativeMountEvidence, r map[string]diskMetadata) {
			v := r["disk900001s2"]
			v.parentWholeDisk = "-all"
			r[v.device] = v
		},
		"parent path": func(_ *nativeMountEvidence, r map[string]diskMetadata) {
			v := r["disk900001s2"]
			v.parentWholeDisk = "/dev/disk900001"
			r[v.device] = v
		},
		"parent partition": func(_ *nativeMountEvidence, r map[string]diskMetadata) {
			v := r["disk900001s2"]
			v.parentWholeDisk = v.device
			r[v.device] = v
		},
		"wrong store response": func(_ *nativeMountEvidence, r map[string]diskMetadata) {
			v := r["disk900001s2"]
			v.device = "disk900002s2"
			r["disk900001s2"] = v
		},
		"wrong parent response": func(_ *nativeMountEvidence, r map[string]diskMetadata) {
			v := r["disk900001"]
			v.device = "disk900002"
			r["disk900001"] = v
		},
		"virtual parent": func(_ *nativeMountEvidence, r map[string]diskMetadata) {
			v := r["disk900001"]
			v.physicalKind = "Virtual"
			r[v.device] = v
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			mount, rows := diskQueryFixture()
			change(&mount, rows)
			calls := 0
			query := func(_ context.Context, device string) (diskMetadata, error) {
				calls++
				if !diskDeviceReference.MatchString(device) {
					t.Fatal("unsafe device reached query")
				}
				row, ok := rows[device]
				if !ok {
					t.Fatal("query expanded beyond returned evidence")
				}
				return row, nil
			}
			if result, err := readDiskIdentity(t.Context(), mount, query); err != errStorageIdentity || result.valid() {
				t.Fatal("unsafe mapping accepted")
			}
			if calls > 3 {
				t.Fatal("unbounded query plan")
			}
		})
	}
}

func TestScopedDiskQueryStopsOnFailureAndCancellation(t *testing.T) {
	for failAt := 1; failAt <= 3; failAt++ {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			mount, rows := diskQueryFixture()
			calls := 0
			query := func(_ context.Context, device string) (diskMetadata, error) {
				calls++
				if calls == failAt {
					return diskMetadata{}, errors.New("private system diagnostics")
				}
				return rows[device], nil
			}
			if _, err := readDiskIdentity(t.Context(), mount, query); err != errStorageIdentity || calls != failAt {
				t.Fatal("failed scoped query was retried or exposed")
			}
		})
	}
	for cancelAt := 0; cancelAt <= 3; cancelAt++ {
		t.Run("cancel"+string(rune('0'+cancelAt)), func(t *testing.T) {
			mount, rows := diskQueryFixture()
			calls := 0
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if cancelAt == 0 {
				cancel()
			}
			query := func(_ context.Context, device string) (diskMetadata, error) {
				calls++
				if calls == cancelAt {
					cancel()
				}
				return rows[device], nil
			}
			if _, err := readDiskIdentity(ctx, mount, query); err != errStorageIdentity || calls != cancelAt {
				t.Fatal("cancelled scoped query continued")
			}
		})
	}
	mount, _ := diskQueryFixture()
	if _, err := readDiskIdentity(nil, mount, nil); err != errStorageIdentity {
		t.Fatal("missing context admitted")
	}
	if _, err := readDiskIdentity(t.Context(), mount, nil); err != errStorageIdentity {
		t.Fatal("missing query admitted")
	}
}
