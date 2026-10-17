package backuprecovery

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

const (
	maxEncryptedConfigBytes        = 64 << 10
	maxEncryptedKeyBytes           = 64 << 10
	maxEncryptedSnapshotBytes      = 1 << 20
	maxEncryptedMetadataBytes      = 16 << 20
	maxMetadataKeyCount            = 16
	maxBrokerOutputBytes           = maxEncryptedMetadataBytes + 32<<10
	metadataBundleMagic            = "ORI-BACKUP-METADATA-1\n"
	metadataConfig            byte = 0
	metadataKey               byte = 1
	metadataSnapshot          byte = 2
	metadataEnd               byte = 255
)

var errMetadataBundle = errors.New("backup encrypted metadata is unavailable")

// Encrypted metadata is private to the broker/provider pipeline. It contains no
// source file data, repository data/index/locks, or decrypted provider output.
// Identifiers/data must not enter logs, assistant context or public JSON.
type metadataEntry struct {
	kind byte
	id   string
	data []byte
}

type metadataBundle struct{ entries []metadataEntry }

func (bundle metadataBundle) valid() bool {
	if len(bundle.entries) < 2 || len(bundle.entries) > 1+maxMetadataKeyCount+maxSnapshotCount {
		return false
	}
	keys, snapshots, total := 0, 0, 0
	var previous metadataEntry
	for index, entry := range bundle.entries {
		limit := 0
		switch entry.kind {
		case metadataConfig:
			if index != 0 || entry.id != "" {
				return false
			}
			limit = maxEncryptedConfigBytes
		case metadataKey, metadataSnapshot:
			if index == 0 || !metadataID(entry.id) || entry.kind < previous.kind ||
				(entry.kind == previous.kind && entry.id <= previous.id) {
				return false
			}
			if entry.kind == metadataKey {
				keys++
				limit = maxEncryptedKeyBytes
			} else {
				snapshots++
				limit = maxEncryptedSnapshotBytes
			}
		default:
			return false
		}
		if len(entry.data) == 0 || len(entry.data) > limit || total > maxEncryptedMetadataBytes-len(entry.data) {
			return false
		}
		total += len(entry.data)
		previous = entry
	}
	return keys >= 1 && keys <= maxMetadataKeyCount && snapshots <= maxSnapshotCount
}

// encodeMetadataBundle uses a bounded length-delimited protocol instead of JSON
// base64 expansion. An explicit end marker and exact EOF reject partial output.
func encodeMetadataBundle(bundle metadataBundle) ([]byte, error) {
	if !bundle.valid() {
		return nil, errMetadataBundle
	}
	output := append([]byte(nil), metadataBundleMagic...)
	for _, entry := range bundle.entries {
		output = append(output, entry.kind)
		var id [64]byte
		copy(id[:], entry.id)
		output = append(output, id[:]...)
		size := len(entry.data)
		if size < 1 || size > maxEncryptedSnapshotBytes {
			return nil, errMetadataBundle
		}
		output = binary.BigEndian.AppendUint32(output, uint32(size))
		output = append(output, entry.data...)
	}
	return append(output, metadataEnd), nil
}

func decodeMetadataBundle(data []byte) (metadataBundle, error) {
	if len(data) > maxBrokerOutputBytes || !bytes.HasPrefix(data, []byte(metadataBundleMagic)) {
		return metadataBundle{}, errMetadataBundle
	}
	reader := bytes.NewReader(data[len(metadataBundleMagic):])
	var bundle metadataBundle
	total := 0
	for {
		kind, err := reader.ReadByte()
		if err != nil {
			return metadataBundle{}, errMetadataBundle
		}
		if kind == metadataEnd {
			if reader.Len() != 0 || !bundle.valid() {
				return metadataBundle{}, errMetadataBundle
			}
			return bundle, nil
		}
		if kind > metadataSnapshot || len(bundle.entries) >= 1+maxMetadataKeyCount+maxSnapshotCount {
			return metadataBundle{}, errMetadataBundle
		}
		var header [68]byte
		if _, err := io.ReadFull(reader, header[:]); err != nil {
			return metadataBundle{}, errMetadataBundle
		}
		id := string(header[:64])
		if kind == metadataConfig {
			if !bytes.Equal(header[:64], make([]byte, 64)) {
				return metadataBundle{}, errMetadataBundle
			}
			id = ""
		}
		wireSize := binary.BigEndian.Uint32(header[64:])
		if wireSize == 0 || wireSize > maxEncryptedSnapshotBytes {
			return metadataBundle{}, errMetadataBundle
		}
		size := int(wireSize) // at most 1 MiB, including on 32-bit test builds
		limit := maxEncryptedSnapshotBytes
		if kind != metadataSnapshot {
			limit = maxEncryptedKeyBytes
		}
		if size > limit || size > reader.Len() || size > maxEncryptedMetadataBytes-total {
			return metadataBundle{}, errMetadataBundle
		}
		content := make([]byte, size)
		if _, err := io.ReadFull(reader, content); err != nil {
			return metadataBundle{}, errMetadataBundle
		}
		total += len(content)
		bundle.entries = append(bundle.entries, metadataEntry{kind: kind, id: id, data: content})
	}
}
