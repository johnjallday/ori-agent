package backuprecovery

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func inventedMetadataBundle() metadataBundle {
	return metadataBundle{entries: []metadataEntry{
		{kind: metadataConfig, data: []byte("invented encrypted config")},
		{kind: metadataKey, id: strings.Repeat("1", 64), data: []byte("invented encrypted key")},
		{kind: metadataSnapshot, id: strings.Repeat("2", 64), data: []byte("invented encrypted snapshot")},
	}}
}

func TestMetadataBundleRoundTripAndPrivateProjection(t *testing.T) {
	bundle := inventedMetadataBundle()
	encoded, err := encodeMetadataBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeMetadataBundle(encoded)
	if err != nil || len(decoded.entries) != len(bundle.entries) {
		t.Fatal("bundle round trip failed")
	}
	for i, entry := range bundle.entries {
		got := decoded.entries[i]
		if entry.kind != got.kind || entry.id != got.id || !bytes.Equal(entry.data, got.data) {
			t.Fatal("bundle changed")
		}
	}
	for _, value := range []any{bundle, bundle.entries[0]} {
		public, err := json.Marshal(value)
		if err != nil || string(public) != "{}" {
			t.Fatal("private metadata entered public JSON")
		}
	}
}

func TestMetadataBundleRejectsPartialAndAmbiguousTransport(t *testing.T) {
	encoded, err := encodeMetadataBundle(inventedMetadataBundle())
	if err != nil {
		t.Fatal(err)
	}
	for length := range len(encoded) {
		if _, err := decodeMetadataBundle(encoded[:length]); err != errMetadataBundle {
			t.Fatalf("accepted truncated frame at length %d", length)
		}
	}
	for _, data := range [][]byte{
		append(bytes.Clone(encoded), 0), []byte("unknown version"),
		append([]byte(metadataBundleMagic), metadataEnd),
		append([]byte(metadataBundleMagic), 42), bytes.Repeat([]byte{'x'}, maxBrokerOutputBytes+1),
	} {
		if _, err := decodeMetadataBundle(data); err != errMetadataBundle {
			t.Fatal("accepted malformed frame")
		}
	}
}

func TestMetadataBundleRejectsInvalidEntries(t *testing.T) {
	for name, change := range map[string]func(*metadataBundle){
		"no_entries":         func(b *metadataBundle) { b.entries = nil },
		"no_key":             func(b *metadataBundle) { b.entries = append(b.entries[:1], b.entries[2]) },
		"empty_data":         func(b *metadataBundle) { b.entries[0].data = nil },
		"config_id":          func(b *metadataBundle) { b.entries[0].id = strings.Repeat("a", 64) },
		"bad_id":             func(b *metadataBundle) { b.entries[1].id = "../outside" },
		"duplicate_key":      func(b *metadataBundle) { b.entries = append(b.entries[:2], b.entries[1]) },
		"unknown_kind":       func(b *metadataBundle) { b.entries[2].kind = 42 },
		"out_of_order":       func(b *metadataBundle) { b.entries[1], b.entries[2] = b.entries[2], b.entries[1] },
		"oversized_config":   func(b *metadataBundle) { b.entries[0].data = make([]byte, maxEncryptedConfigBytes+1) },
		"oversized_key":      func(b *metadataBundle) { b.entries[1].data = make([]byte, maxEncryptedKeyBytes+1) },
		"oversized_snapshot": func(b *metadataBundle) { b.entries[2].data = make([]byte, maxEncryptedSnapshotBytes+1) },
		"too_many_keys": func(b *metadataBundle) {
			b.entries = b.entries[:1]
			for i := 0; i <= maxMetadataKeyCount; i++ {
				b.entries = append(b.entries, metadataEntry{kind: metadataKey, id: fmt.Sprintf("%064x", i), data: []byte("key")})
			}
		},
		"too_many_snapshots": func(b *metadataBundle) {
			b.entries = b.entries[:2]
			for i := 0; i <= maxSnapshotCount; i++ {
				b.entries = append(b.entries, metadataEntry{kind: metadataSnapshot, id: fmt.Sprintf("%064x", i), data: []byte("snapshot")})
			}
		},
		"total_bytes": func(b *metadataBundle) {
			b.entries = b.entries[:2]
			for i := 0; i < 16; i++ {
				b.entries = append(b.entries, metadataEntry{kind: metadataSnapshot, id: fmt.Sprintf("%064x", i), data: make([]byte, maxEncryptedSnapshotBytes)})
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			bundle := inventedMetadataBundle()
			change(&bundle)
			if bundle.valid() {
				t.Fatal("invalid bundle accepted")
			}
			if _, err := encodeMetadataBundle(bundle); err != errMetadataBundle {
				t.Fatal("invalid bundle encoded")
			}
		})
	}
}
