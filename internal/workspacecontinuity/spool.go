package workspacecontinuity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"

	"github.com/google/uuid"
)

// Spool bounds collection to one chunk in memory and immutable private objects
// on disk. It is single-writer, used while domain adapters share one SQL view.
// It is not a live store and never takes paths from imported records. Close it
// after publication/failure while still holding the caller's reset permit.
type Spool struct {
	directory  string
	root       *os.Root
	workspace  string
	objects    map[string]int64
	components map[string]*Component
	seen       map[[sha256.Size]byte]bool
	pending    Chunk
	estimate   int
	total      int64
	references int
	sealed     bool
	failed     error
}

// NewSpool creates a disposable child of the caller's trusted installation temp
// directory. It never creates a directory under an unreviewed workspace.
func NewSpool(parent, workspaceID string) (*Spool, error) {
	if !ValidID(workspaceID) {
		return nil, ErrInvalid
	}
	directory, err := os.MkdirTemp(parent, ".ori-continuity-spool-")
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		_ = os.Remove(directory)
		return nil, err
	}
	return &Spool{directory: directory, root: root, workspace: workspaceID,
		objects: map[string]int64{}, components: map[string]*Component{}, seen: map[[sha256.Size]byte]bool{}}, nil
}

func (s *Spool) check(ctx context.Context) error {
	if s == nil || s.root == nil || s.sealed {
		return ErrInvalid
	}
	if s.failed != nil {
		return s.failed
	}
	if err := ctx.Err(); err != nil {
		s.failed = err
		return err
	}
	return nil
}

func (s *Spool) component(domain string) *Component {
	value := s.components[domain]
	if value == nil {
		value = &Component{Domain: domain, Version: Version, Availability: Empty,
			Counts: map[string]int64{}, Chunks: []ChunkRef{}, Blobs: []BlobRef{}}
		s.components[domain] = value
	}
	return value
}

func (s *Spool) put(ctx context.Context, data []byte) (BlobRef, error) {
	ref := BlobRef{Digest: Digest(data), Bytes: int64(len(data))}
	if err := ctx.Err(); err != nil {
		return ref, err
	}
	if _, exists := s.objects[ref.Digest]; exists {
		return ref, nil
	}
	if ref.Bytes > MaxTotalBytes-s.total {
		return ref, ErrLimit
	}
	file, err := s.root.OpenFile(ref.Digest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ref, err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr, ctx.Err()); err != nil {
		_ = s.root.Remove(ref.Digest)
		return ref, err
	}
	// This is disposable collection, not publication; Publish syncs verified
	// destination objects before the pointer becomes visible.
	s.objects[ref.Digest] = ref.Bytes
	s.total += ref.Bytes
	return ref, nil
}

func (s *Spool) flush(ctx context.Context) error {
	if len(s.pending.Records) == 0 {
		return nil
	}
	if s.references >= MaxReferences {
		return ErrLimit
	}
	data, ref, err := EncodeChunk(s.pending)
	if err != nil {
		return err
	}
	if _, err := s.put(ctx, data); err != nil {
		return err
	}
	component := s.component(s.pending.Domain)
	component.Availability = Present
	component.Chunks = append(component.Chunks, ref)
	component.Counts[s.pending.Family] += ref.Count
	s.references++
	s.pending = Chunk{}
	s.estimate = 0
	return nil
}

// AddRecord retains a copy, not a page buffer a caller may reuse. Switching
// family/domain flushes the prior chunk so interleaved adapters cannot multiply
// memory by the number of open families. Any error poisons this collection.
func (s *Spool) AddRecord(ctx context.Context, domain, family string, record Record) (err error) {
	if err = s.check(ctx); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			s.failed = err
		}
	}()
	if !knownDomain(domain) || !validLabel(family) || !ValidID(record.ID) {
		return ErrInvalid
	}
	if len(record.Data) > MaxRecordBytes {
		return ErrLimit
	}
	// Account for JSON's HTML escaping before sizing the chunk. RawMessage is
	// otherwise allowed to expand at final marshal time (for example '<').
	encoded, err := json.Marshal(record.Data)
	if err != nil {
		return ErrInvalid
	}
	if len(encoded) > MaxRecordBytes {
		return ErrLimit
	}
	record.Data = encoded
	component := s.component(domain)
	if component.Availability == Unavailable || component.Availability == Unsupported {
		return ErrConflict
	}
	key := sha256.Sum256([]byte(domain + "\x00" + family + "\x00" + record.ID))
	if s.seen[key] {
		return ErrConflict
	}
	if len(s.seen) >= MaxReferences*MaxRecords {
		return ErrLimit
	}
	increment := len(record.Data) + len(record.ID)*6 + 64
	if len(s.pending.Records) > 0 && (s.pending.Domain != domain || s.pending.Family != family ||
		len(s.pending.Records) == MaxRecords || s.estimate+increment > MaxChunkBytes) {
		if err := s.flush(ctx); err != nil {
			return err
		}
	}
	if len(s.pending.Records) == 0 {
		s.pending = Chunk{Version: Version, WorkspaceID: s.workspace, Domain: domain, Family: family}
		s.estimate = 512
	}
	if s.estimate+increment > MaxChunkBytes {
		return ErrLimit
	}
	s.seen[key] = true
	s.pending.Records = append(s.pending.Records, Record{ID: record.ID, Data: record.Data})
	s.estimate += increment
	return nil
}

// AddBlob handles already bounded small appearance/upload bytes.
func (s *Spool) AddBlob(ctx context.Context, domain string, data []byte) (BlobRef, error) {
	return s.AddBlobReader(ctx, domain, bytes.NewReader(data), int64(len(data)))
}

// AddBlobReader streams owned bytes without a blob-sized allocation. No external
// link is opened here; the domain owner validates its source and retains the
// read handle. Duplicate content shares an object and per-domain reference.
func (s *Spool) AddBlobReader(ctx context.Context, domain string, reader io.Reader, size int64) (ref BlobRef, err error) {
	if err = s.check(ctx); err != nil {
		return ref, err
	}
	defer func() {
		if err != nil {
			s.failed = err
		}
	}()
	if (domain != "uploads" && domain != "agents") || reader == nil {
		return ref, ErrInvalid
	}
	if size < 0 || size > MaxBlobBytes || size > MaxTotalBytes-s.total {
		return ref, ErrLimit
	}
	component := s.component(domain)
	if component.Availability == Unsupported || component.Availability == Unavailable {
		return ref, ErrConflict
	}
	temporary := ".blob-" + uuid.NewString()
	file, err := s.root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ref, err
	}
	defer func() { _ = s.root.Remove(temporary) }()
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(contextReader{ctx, reader}, size+1))
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr, ctx.Err()); err != nil {
		return ref, err
	}
	if written != size {
		return ref, ErrChanged
	}
	ref = BlobRef{Digest: hex.EncodeToString(hash.Sum(nil)), Bytes: size}
	for _, existing := range component.Blobs {
		if existing == ref {
			return ref, nil
		}
	}
	if s.references >= MaxReferences {
		return ref, ErrLimit
	}
	if _, exists := s.objects[ref.Digest]; !exists {
		if err := s.root.Link(temporary, ref.Digest); err != nil {
			return ref, err
		}
		s.objects[ref.Digest] = size
		s.total += size
	}
	component.Blobs = append(component.Blobs, ref)
	s.references++
	return ref, nil
}

// SetAvailability declares domains explicitly; omission is unsupported, not a
// fabricated empty component. It cannot silently discard already collected data.
func (s *Spool) SetAvailability(ctx context.Context, domain string, availability Availability, reason string) (err error) {
	if err = s.check(ctx); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			s.failed = err
		}
	}()
	candidate := Component{Domain: domain, Version: Version, Availability: availability, Reason: reason,
		Counts: map[string]int64{}, Chunks: []ChunkRef{}, Blobs: []BlobRef{}}
	total, refs := int64(0), 0
	if !knownDomain(domain) || availability == Present {
		return ErrInvalid
	}
	if err := candidate.validate(&total, &refs); err != nil {
		return err
	}
	if existing := s.components[domain]; existing != nil && (len(existing.Chunks) != 0 || len(existing.Blobs) != 0) ||
		s.pending.Domain == domain && len(s.pending.Records) != 0 {
		return ErrConflict
	}
	s.components[domain] = &candidate
	return nil
}

// Seal returns deterministic metadata and an immutable verified object reader.
// It does not mark source readiness. Missing domain adapters remain explicit.
func (s *Spool) Seal(ctx context.Context) ([]Component, ObjectSource, error) {
	if err := s.check(ctx); err != nil {
		return nil, nil, err
	}
	if err := s.flush(ctx); err != nil {
		s.failed = err
		return nil, nil, err
	}
	components := make([]Component, 0, len(DomainNames()))
	total, refs := int64(0), 0
	for _, name := range DomainNames() {
		component := s.components[name]
		if component == nil {
			component = &Component{Domain: name, Version: Version, Availability: Unsupported, Reason: "adapter_unavailable",
				Counts: map[string]int64{}, Chunks: []ChunkRef{}, Blobs: []BlobRef{}}
		}
		if err := component.validate(&total, &refs); err != nil {
			s.failed = err
			return nil, nil, err
		}
		components = append(components, *component)
	}
	s.sealed = true
	return components, func(ctx context.Context, digest string) (io.ReadCloser, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !validDigest(digest) || s.root == nil {
			return nil, ErrInvalid
		}
		if _, exists := s.objects[digest]; !exists {
			return nil, ErrIncomplete
		}
		return openRegular(s.root, digest)
	}, nil
}

func (s *Spool) Close() error {
	if s == nil || s.root == nil {
		return nil
	}
	// Delete only this owner's known objects, never recursive unknown contents.
	var errs []error
	names := make([]string, 0, len(s.objects))
	for name := range s.objects {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := s.root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	errs = append(errs, s.root.Close())
	s.root = nil
	if err := os.Remove(s.directory); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
