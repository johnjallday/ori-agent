package workspacecontinuity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/google/uuid"
)

const formatMarker = `{"format":"ori.workspace-continuity","version":1}`

var ErrCollision = errors.New("continuity reserved directory collision")

// ObjectSource opens an immutable object produced by canonical domain adapters.
// It must not resolve a path from imported data. Publish always checks its digest.
type ObjectSource func(context.Context, string) (io.ReadCloser, error)

// Fence rechecks the captured SQL dirty sequence and local attachment/reset
// admission. The caller retains its reset permit until Publish returns, and CASes
// local readiness only if this same sequence is still current afterwards.
type Fence func(context.Context) error

var publicationLocks struct {
	sync.Mutex
	entries []*publicationLock
}

type publicationLock struct {
	info os.FileInfo
	refs int
	gate chan struct{}
}

// Serialize publishers by open directory identity, not path spelling. Aliases
// cannot race cleanup against another local publication. This is process-local;
// the installation lease owns process exclusion, not portable lock files.
func lockPublication(ctx context.Context, info os.FileInfo) (func(), error) {
	publicationLocks.Lock()
	var entry *publicationLock
	for _, candidate := range publicationLocks.entries {
		if os.SameFile(candidate.info, info) {
			entry = candidate
			break
		}
	}
	if entry == nil {
		entry = &publicationLock{info: info, gate: make(chan struct{}, 1)}
		publicationLocks.entries = append(publicationLocks.entries, entry)
	}
	entry.refs++
	publicationLocks.Unlock()
	drop := func() {
		publicationLocks.Lock()
		defer publicationLocks.Unlock()
		entry.refs--
		if entry.refs == 0 {
			for index, candidate := range publicationLocks.entries {
				if candidate == entry {
					publicationLocks.entries = append(publicationLocks.entries[:index], publicationLocks.entries[index+1:]...)
					break
				}
			}
		}
	}
	select {
	case entry.gate <- struct{}{}:
		return func() { <-entry.gate; drop() }, nil
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	}
}

// Publish writes through a confined workspace root acquired at call time.
// Operation coordinators should use PublishPinned with their already-open root
// so replacing the source pathname cannot redirect publication.
func Publish(ctx context.Context, workspaceDirectory string, manifest Manifest, source ObjectSource, fence Fence) error {
	if fence == nil || source == nil {
		return ErrInvalid
	}
	root, err := os.OpenRoot(workspaceDirectory)
	if err != nil {
		return ErrUnsafe
	}
	defer func() { _ = root.Close() }()
	return PublishPinned(ctx, root, manifest, source, fence)
}

// PublishPinned writes verified immutable objects/manifest, rechecks canonical
// files and the supplied consistency fence, then publishes the pointer last.
// The caller holds the root and reset/operation lease through acknowledgement;
// this never acknowledges the local dirty sequence itself. A cleanup failure
// returns an error even if a pointer was published, so Ready stays false.
func PublishPinned(ctx context.Context, root *os.Root, manifest Manifest, source ObjectSource, fence Fence) error {
	if root == nil || fence == nil || source == nil {
		return ErrInvalid
	}
	data, pointer, err := EncodeManifest(manifest)
	if err != nil {
		return err
	}
	info, err := root.Stat(".")
	if err != nil {
		return ErrUnsafe
	}
	unlock, err := lockPublication(ctx, info)
	if err != nil {
		return err
	}
	defer unlock()
	if err := fence(ctx); err != nil {
		return err
	}
	if err := verifyFiles(ctx, root, manifest); err != nil {
		return err
	}
	managed, err := prepareManaged(root)
	if err != nil {
		return err
	}
	defer func() { _ = managed.Close() }()
	live := map[string]bool{}
	for _, component := range manifest.Components {
		seenRecords := map[[sha256.Size]byte]bool{}
		for _, ref := range component.Chunks {
			if err := publishObject(ctx, managed, source, ref.Digest, ref.Bytes, MaxChunkBytes); err != nil {
				return err
			}
			payload, err := readFileBounded(managed, "objects/"+ref.Digest, ref.Bytes)
			if err != nil {
				return err
			}
			chunk, err := DecodeChunk(bytes.NewReader(payload), ref, manifest.WorkspaceID, component.Domain)
			if err != nil {
				return err
			}
			for _, record := range chunk.Records {
				key := sha256.Sum256([]byte(chunk.Family + "\x00" + record.ID))
				if seenRecords[key] {
					return ErrInvalid
				}
				seenRecords[key] = true
			}
			live[ref.Digest] = true
		}
		for _, ref := range component.Blobs {
			if err := publishObject(ctx, managed, source, ref.Digest, ref.Bytes, MaxBlobBytes); err != nil {
				return err
			}
			live[ref.Digest] = true
		}
	}
	generationPath := "generations/" + manifest.Generation + ".json"
	if err := writeImmutable(managed, generationPath, data); err != nil {
		return err
	}
	if err := verifyFiles(ctx, root, manifest); err != nil {
		return err
	}
	if err := fence(ctx); err != nil {
		return err
	}
	pointerBytes, err := json.Marshal(pointer)
	if err != nil {
		return ErrInvalid
	}
	if err := atomicWrite(managed, "current.json", pointerBytes); err != nil {
		return err
	}
	return prune(ctx, managed, manifest.Generation, live)
}

func prepareManaged(workspace *os.Root) (*os.Root, error) {
	if err := workspace.Mkdir(".ori", 0o750); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	ori, err := openDirectory(workspace, ".ori")
	if err != nil {
		return nil, ErrUnsafe
	}
	defer func() { _ = ori.Close() }()
	managed, err := openDirectory(ori, "continuity")
	if errors.Is(err, os.ErrNotExist) {
		managed, err = initializeManaged(ori)
	}
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*os.Root, error) { _ = managed.Close(); return nil, err }
	marker, err := readFileBounded(managed, "format.json", MaxPointerBytes)
	if err != nil || string(marker) != formatMarker {
		return fail(ErrCollision)
	}
	for _, name := range []string{"generations", "objects", "staging"} {
		if err := managed.Mkdir(name, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
			return fail(err)
		}
		directory, err := openDirectory(managed, name)
		if err != nil {
			return fail(ErrUnsafe)
		}
		_ = directory.Close()
	}
	if err := verifyManagedLayout(managed); err != nil {
		return fail(err)
	}
	if err := syncDirectory(ori); err != nil {
		return fail(err)
	}
	if err := syncDirectory(workspace); err != nil {
		return fail(err)
	}
	if err := syncDirectory(managed); err != nil {
		return fail(err)
	}
	return managed, nil
}

func publishObject(ctx context.Context, root *os.Root, source ObjectSource, digest string, size, limit int64) error {
	path := "objects/" + digest
	if _, err := root.Lstat(path); err == nil {
		if err := privateFile(root, path); err != nil {
			return err
		}
		return copyVerified(ctx, root, path, digest, size, limit, io.Discard)
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrUnsafe
	}
	reader, err := source(ctx, digest)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	name := "staging/" + uuid.NewString() + ".tmp"
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(name) }()
	// Bounded streaming; cancellation is checked on each read rather than after
	// an arbitrarily large upload. A source must itself honor cancellation if it
	// can block (the production adapters read regular local files).
	_, copyErr := io.Copy(file, io.LimitReader(contextReader{ctx, reader}, size+1))
	syncErr, closeErr := file.Sync(), file.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := copyVerified(ctx, root, name, digest, size, limit, io.Discard); err != nil {
		return err
	}
	if err := root.Link(name, path); err != nil { // exclusive publication, never overwrite
		return err
	}
	return syncSubdirectory(root, "objects")
}

type contextReader struct {
	ctx context.Context
	io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(buffer)
}

func writeImmutable(root *os.Root, path string, data []byte) error {
	if _, err := root.Lstat(path); err == nil {
		if err := privateFile(root, path); err != nil {
			return err
		}
		return copyVerified(context.Background(), root, path, Digest(data), int64(len(data)), MaxManifestBytes, io.Discard)
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrUnsafe
	}
	return stageBytes(root, path, data, false)
}

func atomicWrite(root *os.Root, path string, data []byte) error {
	return stageBytes(root, path, data, true)
}

func stageBytes(root *os.Root, path string, data []byte, replace bool) error {
	name := "staging/" + uuid.NewString() + ".tmp"
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(name) }()
	_, writeErr := file.Write(data)
	syncErr, closeErr := file.Sync(), file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if replace {
		err = root.Rename(name, path)
	} else {
		err = root.Link(name, path)
	}
	if err != nil {
		return err
	}
	if index := strings.LastIndexByte(path, '/'); index >= 0 {
		return syncSubdirectory(root, path[:index])
	}
	return syncDirectory(root)
}

func syncSubdirectory(root *os.Root, name string) error {
	dir, err := openDirectory(root, name)
	if err != nil {
		return ErrUnsafe
	}
	defer func() { _ = dir.Close() }()
	return syncDirectory(dir)
}

func syncDirectory(root *os.Root) error {
	if runtime.GOOS == "windows" {
		return nil // Windows does not expose fsync for directory handles.
	}
	file, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return file.Sync()
}

func prune(ctx context.Context, root *os.Root, generation string, live map[string]bool) error {
	for _, name := range []string{"generations", "objects", "staging"} {
		directory, err := openDirectory(root, name)
		if err != nil {
			return ErrUnsafe
		}
		err = pruneDirectory(ctx, directory, name, generation, live)
		_ = directory.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func pruneDirectory(ctx context.Context, root *os.Root, kind, generation string, live map[string]bool) error {
	file, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, readErr := file.ReadDir(256)
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		for _, entry := range entries {
			name := entry.Name()
			keep, valid := false, false
			switch kind {
			case "generations":
				valid = strings.HasSuffix(name, ".json") && validGeneration(strings.TrimSuffix(name, ".json"))
				keep = name == generation+".json"
			case "objects":
				valid, keep = validDigest(name), live[name]
			case "staging":
				valid = strings.HasSuffix(name, ".tmp") && validGeneration(strings.TrimSuffix(name, ".tmp"))
			}
			if !valid || !entry.Type().IsRegular() {
				return ErrCollision // never delete arbitrary files or traverse links
			}
			if !keep {
				if err := root.Remove(name); err != nil {
					return err
				}
			}
		}
		if readErr == io.EOF {
			break
		}
	}
	return syncDirectory(root)
}
