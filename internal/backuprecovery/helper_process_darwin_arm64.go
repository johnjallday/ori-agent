//go:build darwin && arm64

package backuprecovery

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"time"
)

const maxHelperInputBytes = maxSettingsBytes + maxBrokerOutputBytes + 4096 + 32
const maxBrokerEvidenceBytes = 12 << 10

type storeEvidenceWire struct {
	ID    string `json:"id"`
	Whole string `json:"whole"`
}
type storageEvidenceWire struct {
	Volume    string              `json:"volume"`
	Partition string              `json:"partition"`
	Device    uint64              `json:"device"`
	Root      uint64              `json:"root"`
	Internal  bool                `json:"internal"`
	Physical  bool                `json:"physical"`
	Stores    []storeEvidenceWire `json:"stores"`
}
type directoryEvidenceWire struct {
	Storage storageEvidenceWire `json:"storage"`
	FileID  uint64              `json:"file_id"`
}
type brokerEvidenceWire struct {
	Destination directoryEvidenceWire   `json:"destination"`
	Sources     []directoryEvidenceWire `json:"sources"`
	Capacity    *localCapacity          `json:"capacity,omitempty"`
}
type brokerHelperRequest struct {
	Selection metadataSelection `json:"selection"`
	Scratch   string            `json:"scratch"`
}
type readerHelperRequest struct {
	Executable string   `json:"executable"`
	Scratch    string   `json:"scratch"`
	Sources    []string `json:"sources"`
	Repository string   `json:"repository"`
}
type readerHelperResponse struct {
	RepositoryID string          `json:"repository_id"`
	Snapshots    []localSnapshot `json:"snapshots"`
}

func directoryToWire(evidence selectedDirectoryEvidence) directoryEvidenceWire {
	s := evidence.storage
	wire := directoryEvidenceWire{FileID: evidence.fileID, Storage: storageEvidenceWire{Volume: s.volumeID, Partition: s.partitionID, Device: s.mountDevice, Root: s.rootFileID, Internal: s.location == storageInternal, Physical: s.physical}}
	for _, store := range s.physicalStores {
		wire.Storage.Stores = append(wire.Storage.Stores, storeEvidenceWire{ID: store.partitionID, Whole: store.wholeDisk})
	}
	return wire
}
func (wire directoryEvidenceWire) evidence() selectedDirectoryEvidence {
	location, transport := storageExternal, "USB"
	if wire.Storage.Internal {
		location, transport = storageInternal, "Apple Fabric"
	}
	s := storageIdentity{volumeID: wire.Storage.Volume, partitionID: wire.Storage.Partition, mountDevice: wire.Storage.Device, rootFileID: wire.Storage.Root, filesystem: "apfs", mounted: true, location: location, transport: transport, physical: wire.Storage.Physical}
	for _, store := range wire.Storage.Stores {
		s.physicalStores = append(s.physicalStores, physicalStoreIdentity{partitionID: store.ID, wholeDisk: store.Whole})
	}
	return selectedDirectoryEvidence{storage: s, fileID: wire.FileID}
}

func encodeBrokerResult(result selectedMetadata) ([]byte, error) {
	wire := brokerEvidenceWire{Destination: directoryToWire(result.destination), Capacity: result.capacity}
	for _, source := range result.sources {
		wire.Sources = append(wire.Sources, directoryToWire(source))
	}
	header, err := json.Marshal(wire)
	headerSize := len(header)
	if err != nil || headerSize < 1 || headerSize > maxBrokerEvidenceBytes {
		return nil, errMetadataBundle
	}
	bundle, err := encodeMetadataBundle(result.bundle)
	if err != nil {
		return nil, err
	}
	output := binary.BigEndian.AppendUint32(nil, uint32(headerSize))
	output = append(output, header...)
	output = append(output, bundle...)
	if len(output) > maxBrokerOutputBytes {
		return nil, errMetadataBundle
	}
	return output, nil
}
func decodeBrokerResult(raw []byte, sourceCount int) (selectedMetadata, error) {
	bad := func() (selectedMetadata, error) { return selectedMetadata{}, errMetadataBundle }
	if len(raw) < 4 || len(raw) > maxBrokerOutputBytes {
		return bad()
	}
	size := binary.BigEndian.Uint32(raw[:4])
	if size > maxBrokerEvidenceBytes || int(size) > len(raw)-4 {
		return bad()
	}
	wire, err := decodeLocalJSON[brokerEvidenceWire](raw[4:4+int(size)], maxBrokerEvidenceBytes)
	if err != nil || len(wire.Sources) != sourceCount || sourceCount < 1 || sourceCount > maxSelectedSources {
		return bad()
	}
	result := selectedMetadata{destination: wire.Destination.evidence(), capacity: wire.Capacity}
	if result.destination.fileID == 0 {
		return bad()
	}
	identities := make([]storageIdentity, 0, sourceCount)
	for _, source := range wire.Sources {
		value := source.evidence()
		if value.fileID == 0 {
			return bad()
		}
		result.sources = append(result.sources, value)
		identities = append(identities, value.storage)
	}
	if validateStorageSelection(result.destination.storage, identities) != nil {
		return bad()
	}
	if result.capacity != nil && (result.capacity.TotalBytes == 0 || result.capacity.AvailableBytes > result.capacity.TotalBytes) {
		return bad()
	}
	result.bundle, err = decodeMetadataBundle(raw[4+int(size):])
	if err != nil {
		return bad()
	}
	return result, nil
}

// Private length framing has no password JSON field. The final password bytes
// exist only in an owned pipe/buffer and are cleared before this child exits.
func readerHelperInput(request readerHelperRequest, bundle metadataBundle) ([]byte, error) {
	header, err := json.Marshal(request)
	headerSize := len(header)
	if err != nil || headerSize < 1 || headerSize > maxSettingsBytes {
		return nil, errReadProcess
	}
	encoded, err := encodeMetadataBundle(bundle)
	bundleSize := len(encoded)
	if err != nil {
		return nil, err
	}
	if bundleSize < 1 || bundleSize > maxBrokerOutputBytes {
		return nil, errMetadataBundle
	}
	output := binary.BigEndian.AppendUint32(nil, uint32(headerSize))
	output = append(output, header...)
	output = binary.BigEndian.AppendUint32(output, uint32(bundleSize))
	return append(output, encoded...), nil
}
func decodeReaderHelperInput(raw []byte) (readerHelperRequest, metadataBundle, []byte, error) {
	bad := func() (readerHelperRequest, metadataBundle, []byte, error) {
		return readerHelperRequest{}, metadataBundle{}, nil, errReadProcess
	}
	if len(raw) < 8 || len(raw) > maxHelperInputBytes {
		return bad()
	}
	headerSize := binary.BigEndian.Uint32(raw[:4])
	if headerSize > maxSettingsBytes || int(headerSize) > len(raw)-8 {
		return bad()
	}
	position := 4 + int(headerSize)
	request, err := decodeLocalJSON[readerHelperRequest](raw[4:position], maxSettingsBytes)
	if err != nil {
		return bad()
	}
	size := binary.BigEndian.Uint32(raw[position : position+4])
	position += 4
	if size > maxBrokerOutputBytes || int(size) > len(raw)-position {
		return bad()
	}
	bundle, err := decodeMetadataBundle(raw[position : position+int(size)])
	if err != nil {
		return bad()
	}
	password := raw[position+int(size):]
	if len(password) < 1 || len(password) > 4096 {
		return bad()
	}
	return request, bundle, password, nil
}

func runCompiledBackupHelper(mode string) int {
	ctx, cancel := context.WithTimeout(context.Background(), completeReadLimit)
	defer cancel()
	if mode == "broker-v1" {
		input, err := io.ReadAll(io.LimitReader(os.Stdin, maxSettingsBytes+1))
		if err != nil {
			return 1
		}
		defer clear(input)
		request, err := decodeLocalJSON[brokerHelperRequest](input, maxSettingsBytes)
		if err != nil || !validMetadataPath(request.Scratch) {
			return 1
		}
		brokerCtx, stop := context.WithTimeout(ctx, 75*time.Second)
		defer stop()
		result, err := collectSelectedMetadata(brokerCtx, request.Selection, request.Scratch)
		if err != nil {
			return helperFailureCode(err)
		}
		encoded, err := encodeBrokerResult(result)
		if err != nil {
			return 1
		}
		if _, err := os.Stdout.Write(encoded); err != nil {
			return 1
		}
		return 0
	}
	input, err := io.ReadAll(io.LimitReader(os.Stdin, maxHelperInputBytes+1))
	if err != nil {
		return 1
	}
	defer clear(input)
	request, bundle, password, err := decodeReaderHelperInput(input)
	if err != nil {
		return 1
	}
	result, err := readResticReplica(ctx, request.Executable, request.Scratch, bundle, request.Sources, request.Repository, password)
	if err != nil {
		return helperFailureCode(err)
	}
	response := readerHelperResponse{RepositoryID: result.config.id, Snapshots: make([]localSnapshot, 0, len(result.snapshots))}
	for _, snapshot := range result.snapshots {
		response.Snapshots = append(response.Snapshots, localSnapshot{ID: snapshot.id, StartedAt: snapshot.startedAt, CompletedAt: snapshot.completedAt, DeclaredPaths: snapshot.paths, Exclusions: snapshot.exclusions, FilesProcessed: snapshot.filesProcessed, BytesProcessed: snapshot.bytesProcessed})
	}
	encoded, err := json.Marshal(response)
	if err != nil || len(encoded) > maxPrivateStateBytes {
		return 1
	}
	defer clear(encoded)
	if _, err := io.Copy(os.Stdout, bytes.NewReader(encoded)); err != nil {
		return 1
	}
	return 0
}
