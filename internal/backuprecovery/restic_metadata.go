// Package backuprecovery holds the Backup & Recovery metadata contract and
// private read-boundary primitives. No provider service, activation, HTTP API,
// template, or monitoring runtime is wired yet.
package backuprecovery

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxConfigBytes       = 16 << 10
	maxSnapshotBytes     = 1 << 20
	maxSnapshotCount     = 256
	maxDeclaredSources   = 16
	maxDeclaredExcludes  = 64
	maxSourcePathBytes   = 4096
	maxExcludeBytes      = 1024
	maxMetadataDepth     = 8
	maxMetadataTokens    = 32768
	supportedRepoVersion = 2
)

// Never return a provider diagnostic or JSON decoder error: either may contain
// credentials, private paths, hostnames, or instruction-like external text.
var (
	errMetadataRead      = errors.New("backup metadata read unavailable")
	errMetadataInvalid   = errors.New("backup metadata is invalid")
	errMetadataLimit     = errors.New("backup metadata exceeds supported limits")
	errRepositoryVersion = errors.New("backup repository version is unsupported")
)

// These private types deliberately have no exported/JSON fields. They are
// domain-only evidence, not API, advisor, Today, or Daily Brief payloads.
type resticConfigMetadata struct {
	id      string
	version int
}

type resticSnapshotMetadata struct {
	id             string
	startedAt      time.Time // snapshot.time is the start, NOT completion
	completedAt    *time.Time
	paths          []string // provider-declared scope, not verified file coverage
	exclusions     []string
	filesProcessed *uint64
	bytesProcessed *uint64
	// No successful/healthy/verified flag: even an incomplete backup can
	// publish a snapshot with a backup_end and file/byte counts.
}

func decodeResticConfig(stdout, stderr []byte, exitCode int) (resticConfigMetadata, error) {
	value, err := decodeMetadata(stdout, stderr, exitCode, maxConfigBytes)
	if err != nil {
		return resticConfigMetadata{}, err
	}
	object, ok := value.(map[string]any)
	if !ok || !metadataID(object["id"]) {
		return resticConfigMetadata{}, errMetadataInvalid
	}
	version, err := metadataCount(object["version"])
	if err != nil || version == nil {
		return resticConfigMetadata{}, errMetadataInvalid
	}
	if *version != supportedRepoVersion {
		return resticConfigMetadata{}, errRepositoryVersion
	}
	return resticConfigMetadata{id: object["id"].(string), version: supportedRepoVersion}, nil
}

func decodeResticSnapshots(stdout, stderr []byte, exitCode int) ([]resticSnapshotMetadata, error) {
	value, err := decodeMetadata(stdout, stderr, exitCode, maxSnapshotBytes)
	if err != nil {
		return nil, err
	}
	rows, ok := value.([]any)
	if !ok {
		return nil, errMetadataInvalid
	}
	if len(rows) > maxSnapshotCount {
		return nil, errMetadataLimit
	}
	out := make([]resticSnapshotMetadata, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for _, value := range rows {
		row, ok := value.(map[string]any)
		if !ok {
			return nil, errMetadataInvalid
		}
		snapshot, err := decodeSnapshot(row)
		if err != nil {
			return nil, err // never return a successful partial list
		}
		if seen[snapshot.id] {
			return nil, errMetadataInvalid
		}
		seen[snapshot.id] = true
		out = append(out, snapshot)
	}
	return out, nil
}

func decodeSnapshot(row map[string]any) (resticSnapshotMetadata, error) {
	var result resticSnapshotMetadata
	if !metadataID(row["id"]) {
		return result, errMetadataInvalid
	}
	result.id = row["id"].(string)
	started, err := metadataTime(row["time"])
	if err != nil || started == nil {
		return result, errMetadataInvalid
	}
	result.startedAt = *started
	result.paths, err = metadataStrings(row["paths"], maxDeclaredSources, maxSourcePathBytes)
	if err != nil {
		return result, err
	}
	if len(result.paths) == 0 {
		return result, errMetadataInvalid
	}
	seen := make(map[string]bool, len(result.paths))
	for _, source := range result.paths {
		// Syntax only; this performs no filesystem/volume identity check. The
		// eventual local verifier must never treat this as access authority.
		if source == "/" || !path.IsAbs(source) || path.Clean(source) != source || seen[source] {
			return result, errMetadataInvalid
		}
		seen[source] = true
	}
	result.exclusions, err = metadataStrings(row["excludes"], maxDeclaredExcludes, maxExcludeBytes)
	if err != nil {
		return result, err
	}
	if row["summary"] == nil {
		return result, nil // absent legacy summary is unknown, not time-as-completion
	}
	summary, ok := row["summary"].(map[string]any)
	if !ok {
		return result, errMetadataInvalid
	}
	result.completedAt, err = metadataTime(summary["backup_end"])
	if err != nil {
		return result, err
	}
	backupStart, err := metadataTime(summary["backup_start"])
	if err != nil {
		return result, err
	}
	// restic permits overriding snapshot.time; use the actual summary start
	// when supplied, not the snapshot label, to check completion ordering.
	if backupStart != nil && result.completedAt != nil && result.completedAt.Before(*backupStart) {
		return result, errMetadataInvalid
	}
	result.filesProcessed, err = metadataCount(summary["total_files_processed"])
	if err != nil {
		return result, err
	}
	result.bytesProcessed, err = metadataCount(summary["total_bytes_processed"])
	return result, err
}

// decodeMetadata rejects any stderr, including an exit-zero partial enumeration.
// The process runner must separately bound collection, deadline and cancellation;
// this decoder does not launch a process or provide those guarantees itself.
func decodeMetadata(stdout, stderr []byte, exitCode, maxBytes int) (any, error) {
	if exitCode != 0 || len(stderr) != 0 {
		return nil, errMetadataRead
	}
	if len(stdout) > maxBytes {
		return nil, errMetadataLimit
	}
	if !utf8.Valid(stdout) {
		return nil, errMetadataInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout))
	decoder.UseNumber()
	tokens := 0
	value, err := metadataValue(decoder, 0, &tokens)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errMetadataInvalid
	}
	return value, nil
}

// Exact-key maps avoid encoding/json's case-insensitive struct-field aliases.
// Unknown additive provider fields are discarded by the typed projection, but
// are still scanned for duplicates, invalid strings, depth and token limits.
func metadataValue(decoder *json.Decoder, depth int, tokens *int) (any, error) {
	if depth > maxMetadataDepth || *tokens >= maxMetadataTokens {
		return nil, errMetadataLimit
	}
	*tokens++
	token, err := decoder.Token()
	if err != nil {
		return nil, errMetadataInvalid
	}
	if text, ok := token.(string); ok && strings.ContainsRune(text, utf8.RuneError) {
		// encoding/json replaces lone escaped UTF-16 surrogates with RuneError.
		// Conservatively reject rather than silently change an identity/path.
		return nil, errMetadataInvalid
	}
	switch token {
	case json.Delim('{'):
		object := make(map[string]any)
		for decoder.More() {
			key, err := metadataValue(decoder, depth+1, tokens)
			name, ok := key.(string)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, errMetadataInvalid
			}
			if _, exists := object[name]; exists {
				return nil, errMetadataInvalid
			}
			value, err := metadataValue(decoder, depth+1, tokens)
			if err != nil {
				return nil, err
			}
			object[name] = value
		}
		if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
			return nil, errMetadataInvalid
		}
		return object, nil
	case json.Delim('['):
		array := make([]any, 0)
		for decoder.More() {
			value, err := metadataValue(decoder, depth+1, tokens)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		if end, err := decoder.Token(); err != nil || end != json.Delim(']') {
			return nil, errMetadataInvalid
		}
		return array, nil
	case json.Delim('}'), json.Delim(']'):
		return nil, errMetadataInvalid
	default:
		return token, nil
	}
}

func metadataID(value any) bool {
	text, ok := value.(string)
	if !ok || len(text) != 64 {
		return false
	}
	for _, char := range text {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

func metadataTime(value any) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	text, ok := value.(string)
	if !ok {
		return nil, errMetadataInvalid
	}
	at, err := time.Parse(time.RFC3339Nano, text)
	if err != nil || at.IsZero() {
		return nil, errMetadataInvalid
	}
	at = at.UTC()
	return &at, nil
}

func metadataCount(value any) (*uint64, error) {
	if value == nil {
		return nil, nil
	}
	number, ok := value.(json.Number)
	if !ok {
		return nil, errMetadataInvalid
	}
	count, err := strconv.ParseUint(string(number), 10, 64)
	if err != nil {
		return nil, errMetadataInvalid
	}
	return &count, nil
}

func metadataStrings(value any, maxCount, maxBytes int) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	values, ok := value.([]any)
	if !ok {
		return nil, errMetadataInvalid
	}
	if len(values) > maxCount {
		return nil, errMetadataLimit
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok || text == "" || strings.ContainsFunc(text, unicode.IsControl) {
			return nil, errMetadataInvalid
		}
		if len(text) > maxBytes {
			return nil, errMetadataLimit
		}
		out = append(out, text)
	}
	return out, nil
}
