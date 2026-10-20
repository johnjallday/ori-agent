package resourcecatalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

// Catalog files live in the Studio's own folder, so they travel with it and
// its continuity checkpoints carry them. Grants and operations live in Ori's
// data directory: they belong to this computer only.
const (
	// CatalogDir is the catalog's directory inside a workspace folder.
	CatalogDir = ".ori/resource-catalog"
	// LockFile and ScratchPrefix are the catalog's own lock and writer
	// scratch; neither is user content.
	LockFile      = ".lock"
	ScratchPrefix = ".resource-catalog-"

	observationsFile = "observations.json"
	annotationsFile  = "annotations.json"
	grantsFile       = "grants.json"
	operationsFile   = "operations.json"
	dataDirName      = "resource_catalog"

	maxObservationsBytes = 64 << 20
	maxAnnotationsBytes  = 16 << 20
	maxGrantsBytes       = 256 << 10
	maxOperationsBytes   = 1 << 20
)

var (
	// ErrCorrupt is a catalog, grants or operations file the host cannot
	// read whole. Nothing is guessed from it and nothing overwrites it.
	ErrCorrupt = errors.New("resource catalog file is unreadable")

	workspaceIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

	processLocks sync.Map // path -> *sync.Mutex
)

// readDocument decodes a file the host wrote: closed shape, no trailing data,
// bounded size. A missing file is reported with found=false.
func readDocument(path string, limit int64, target any) (bool, error) {
	info, err := os.Lstat(path) // #nosec G304 -- path is composed from a resolved workspace folder or the data dir plus fixed names
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return false, fmt.Errorf("%w: %s is not a bounded regular file", ErrCorrupt, filepath.Base(path))
	}
	data, err := os.ReadFile(path) // #nosec G304 G703 -- same composed path, checked above
	if err != nil {
		return false, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return false, fmt.Errorf("%w: %s: %v", ErrCorrupt, filepath.Base(path), err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("%w: %s has trailing data", ErrCorrupt, filepath.Base(path))
	}
	return true, nil
}

// writeDocument replaces path atomically: a synced 0600 temp file renamed
// over it, then the directory synced, so a crash leaves the old or the new
// document and never half of one.
func writeDocument(path string, document any) error {
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil { // #nosec G301 -- private catalog directory
		return err
	}
	tmp, err := os.CreateTemp(dir, ScratchPrefix+"*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil { // #nosec G703 -- both paths are inside the same composed directory
		cleanup()
		return err
	}
	if handle, err := os.Open(dir); err == nil { // #nosec G304 -- the directory just written
		_ = handle.Sync()
		_ = handle.Close()
	}
	return nil
}

// withLock serializes writers of one directory: a process mutex for this
// process and an advisory file lock for any other Ori process on the same
// folder.
func withLock(dir string, fn func() error) error {
	value, _ := processLocks.LoadOrStore(dir, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	if err := os.MkdirAll(dir, 0o750); err != nil { // #nosec G301 -- private catalog directory
		return err
	}
	unlock, err := lockFile(filepath.Join(dir, LockFile))
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

// paths holds one Studio's file locations.
type paths struct {
	catalogDir string
	dataDir    string
}

func (p paths) observations() string { return filepath.Join(p.catalogDir, observationsFile) }
func (p paths) annotations() string  { return filepath.Join(p.catalogDir, annotationsFile) }
func (p paths) grants() string       { return filepath.Join(p.dataDir, grantsFile) }
func (p paths) operations() string   { return filepath.Join(p.dataDir, operationsFile) }

func studioPaths(workspaceFolder, dataDir, studioID string) (paths, error) {
	if !workspaceIDPattern.MatchString(studioID) || !filepath.IsAbs(workspaceFolder) || !filepath.IsAbs(dataDir) {
		return paths{}, errors.New("resource catalog paths are unavailable")
	}
	return paths{
		catalogDir: filepath.Join(workspaceFolder, filepath.FromSlash(CatalogDir)),
		dataDir:    filepath.Join(dataDir, dataDirName, studioID),
	}, nil
}

func readObservations(p paths) (Observations, error) {
	var document Observations
	found, err := readDocument(p.observations(), maxObservationsBytes, &document)
	if err != nil {
		return Observations{}, err
	}
	if !found {
		return Observations{SchemaVersion: DocumentSchemaVersion, Installations: []InstallationRecord{}, Sources: []SourceRecord{}, Entries: []EntryRecord{}}, nil
	}
	if document.SchemaVersion != DocumentSchemaVersion {
		return Observations{}, fmt.Errorf("%w: observations schema %d", ErrCorrupt, document.SchemaVersion)
	}
	return document, nil
}

func readAnnotations(p paths) (Annotations, error) {
	var document Annotations
	found, err := readDocument(p.annotations(), maxAnnotationsBytes, &document)
	if err != nil {
		return Annotations{}, err
	}
	if !found {
		return Annotations{SchemaVersion: DocumentSchemaVersion, Annotations: map[string]Annotation{}}, nil
	}
	if document.SchemaVersion != DocumentSchemaVersion {
		return Annotations{}, fmt.Errorf("%w: annotations schema %d", ErrCorrupt, document.SchemaVersion)
	}
	if document.Annotations == nil {
		document.Annotations = map[string]Annotation{}
	}
	return document, nil
}

// readGrants returns this computer's grants, or found=false when it has none.
func readGrants(p paths) (Grants, bool, error) {
	var document Grants
	found, err := readDocument(p.grants(), maxGrantsBytes, &document)
	if err != nil || !found {
		return Grants{}, false, err
	}
	if document.SchemaVersion != DocumentSchemaVersion {
		return Grants{}, false, fmt.Errorf("%w: grants schema %d", ErrCorrupt, document.SchemaVersion)
	}
	return document, true, nil
}

type operationsDocument struct {
	SchemaVersion int         `json:"schema_version"`
	Operations    []Operation `json:"operations"`
}

func readOperations(p paths) ([]Operation, error) {
	var document operationsDocument
	found, err := readDocument(p.operations(), maxOperationsBytes, &document)
	if err != nil || !found {
		return nil, err
	}
	if document.SchemaVersion != DocumentSchemaVersion {
		return nil, fmt.Errorf("%w: operations schema %d", ErrCorrupt, document.SchemaVersion)
	}
	return document.Operations, nil
}

func writeOperations(p paths, operations []Operation) error {
	if len(operations) > maxOperationsKept {
		operations = operations[len(operations)-maxOperationsKept:]
	}
	return writeDocument(p.operations(), operationsDocument{SchemaVersion: DocumentSchemaVersion, Operations: operations})
}
