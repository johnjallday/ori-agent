// Package workspacecontinuity handles private offline checkpoints, not live
// domain state or execution permission. Only a separate reviewed coordinator may
// attach or restore a checkpoint; parsing one never authorizes work.
package workspacecontinuity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	Version                = 1
	Directory              = ".ori/continuity"
	MaxPointerBytes        = 4 << 10
	MaxManifestBytes       = 1 << 20
	MaxChunkBytes          = 8 << 20
	MaxRecordBytes         = 4 << 20
	MaxRecords             = 500
	MaxReferences          = 4096
	MaxChildren            = 256
	MaxFiles               = 4096
	MaxBlobBytes     int64 = 256 << 20
	MaxTotalBytes    int64 = 16 << 30
)

var (
	ErrInvalid    = errors.New("invalid continuity data")
	ErrVersion    = errors.New("unsupported continuity version")
	ErrLimit      = errors.New("continuity limit exceeded")
	ErrDigest     = errors.New("continuity content digest mismatch")
	ErrIncomplete = errors.New("incomplete continuity checkpoint")
	ErrLegacy     = errors.New("no continuity checkpoint")
	ErrChanged    = errors.New("continuity source changed")
	ErrUnsafe     = errors.New("unsafe continuity file")
)

// DomainNames returns a new slice: callers cannot mutate the wire contract.
func DomainNames() []string {
	return []string{"workspace", "agents", "notes", "assistant", "followups", "brief_config", "brief_history", "sessions", "uploads", "tool_history", "knowledge", "setup"}
}

func knownDomain(name string) bool {
	for _, domain := range DomainNames() {
		if domain == name {
			return true
		}
	}
	return false
}

type Availability string

const (
	Present     Availability = "present"
	Empty       Availability = "empty"
	Unavailable Availability = "unavailable"
	Unsupported Availability = "unsupported"
)

type Pointer struct {
	Version    int    `json:"version"`
	Generation string `json:"generation"`
	Digest     string `json:"digest"`
}

type Manifest struct {
	Version           int           `json:"version"`
	Generation        string        `json:"generation"`
	WorkspaceID       string        `json:"workspace_id"`
	CheckpointAt      time.Time     `json:"checkpoint_at"`
	SourceRevision    uint64        `json:"source_revision"`
	SourceFingerprint string        `json:"source_fingerprint"`
	Children          []string      `json:"children"`
	Files             []Fingerprint `json:"files"`
	Components        []Component   `json:"components"`
}

// Fingerprint refers only to a canonical file in the same workspace, excluding
// children and the managed checkpoint subtree. It is not a destination path.
type Fingerprint struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
	Bytes  int64  `json:"bytes"`
}

type Component struct {
	Domain       string           `json:"domain"`
	Version      int              `json:"version"`
	Availability Availability     `json:"availability"`
	Reason       string           `json:"reason,omitempty"`
	Counts       map[string]int64 `json:"counts"`
	Chunks       []ChunkRef       `json:"chunks"`
	Blobs        []BlobRef        `json:"blobs"`
}

type ChunkRef struct {
	Digest string `json:"digest"`
	Bytes  int64  `json:"bytes"`
	Family string `json:"family"`
	Count  int64  `json:"count"`
}

type BlobRef struct {
	Digest string `json:"digest"`
	Bytes  int64  `json:"bytes"`
}

func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, ch := range value {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func validGeneration(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

// ValidID never turns an opaque identity into a filename. Reject separators and
// controls now so later domain adapters cannot accidentally do so unsafely.
func ValidID(value string) bool {
	if value == "" || len(value) > 200 || value == "." || value == ".." || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, ch := range value {
		if unicode.IsControl(ch) || ch == '/' || ch == '\\' || ch == ':' {
			return false
		}
	}
	return true
}

func validLabel(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, ch := range value {
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '_' {
			return false
		}
	}
	return true
}

func safeRelativePath(value string) bool {
	if len(value) > 1024 || !utf8.ValidString(value) || !fs.ValidPath(value) || value == "." || !filepath.IsLocal(value) || strings.ContainsAny(value, "\\:") {
		return false
	}
	for _, ch := range value {
		if unicode.IsControl(ch) {
			return false
		}
	}
	for _, part := range strings.Split(value, "/") {
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
	}
	return true
}

// ValidCanonicalPath reports whether a workspace-relative file name can be
// declared in a manifest (portable across the filesystems Ori supports).
func ValidCanonicalPath(value string) bool { return canonicalPath(value) }

func canonicalPath(value string) bool {
	folded := strings.ToLower(value)
	return safeRelativePath(value) && folded != Directory && !strings.HasPrefix(folded, Directory+"/") && folded != "sub_workspaces" && !strings.HasPrefix(folded, "sub_workspaces/") && folded != "sub-workspaces" && !strings.HasPrefix(folded, "sub-workspaces/") && !strings.HasPrefix(folded, ".ori/"+initializationPrefix)
}

// FilesDigest is stable across enumeration order, but covers names, sizes and
// digests. It fingerprints files, not authorship or authority.
func FilesDigest(files []Fingerprint) string {
	ordered := slices.Clone(files)
	slices.SortFunc(ordered, func(a, b Fingerprint) int { return strings.Compare(a.Path, b.Path) })
	data, _ := json.Marshal(ordered) // strings/int64 cannot fail JSON encoding
	return Digest(data)
}

func (p Pointer) Validate() error {
	if p.Version != Version {
		return ErrVersion
	}
	if !validGeneration(p.Generation) || !validDigest(p.Digest) {
		return ErrInvalid
	}
	return nil
}

func (m Manifest) Validate() error {
	if m.Version != Version {
		return ErrVersion
	}
	if !validGeneration(m.Generation) || !ValidID(m.WorkspaceID) || m.CheckpointAt.IsZero() || !validDigest(m.SourceFingerprint) {
		return ErrInvalid
	}
	_, offset := m.CheckpointAt.Zone()
	if offset != 0 {
		return ErrInvalid
	}
	if len(m.Children) > MaxChildren || len(m.Files) > MaxFiles || len(m.Components) > len(DomainNames()) {
		return ErrLimit
	}
	if len(m.Components) < len(DomainNames()) {
		return ErrIncomplete
	}
	seen := map[string]bool{m.WorkspaceID: true}
	for _, id := range m.Children {
		if !ValidID(id) || seen[id] {
			return ErrInvalid
		}
		seen[id] = true
	}
	total := int64(0)
	seen = map[string]bool{}
	hasWorkspace := false
	for _, file := range m.Files {
		key := strings.ToLower(file.Path)
		if !canonicalPath(file.Path) || !validDigest(file.Digest) || seen[key] {
			return ErrInvalid
		}
		if file.Bytes < 0 || file.Bytes > MaxBlobBytes || file.Bytes > MaxTotalBytes-total {
			return ErrLimit
		}
		total += file.Bytes
		seen[key] = true
		hasWorkspace = hasWorkspace || file.Path == "workspace.json"
	}
	if !hasWorkspace {
		return ErrIncomplete
	}
	if m.SourceFingerprint != FilesDigest(m.Files) {
		return ErrDigest
	}
	seen = map[string]bool{}
	references := 0
	for _, component := range m.Components {
		if !knownDomain(component.Domain) || seen[component.Domain] {
			return ErrInvalid
		}
		seen[component.Domain] = true
		if err := component.validate(&total, &references); err != nil {
			return err
		}
	}
	return nil
}

func (c Component) validate(total *int64, references *int) error {
	if c.Version != Version {
		return ErrVersion
	}
	if len(c.Counts) > 32 {
		return ErrLimit
	}
	switch c.Availability {
	case Present:
		if len(c.Chunks) == 0 || c.Reason != "" {
			return ErrIncomplete
		}
	case Empty, Unavailable, Unsupported:
		if len(c.Chunks) != 0 || len(c.Blobs) != 0 || len(c.Counts) != 0 {
			return ErrInvalid
		}
		if c.Availability == Empty && c.Reason != "" || c.Availability != Empty && !validLabel(c.Reason) {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	counts := map[string]int64{}
	seen := map[string]bool{}
	for _, ref := range c.Chunks {
		if !validDigest(ref.Digest) || !validLabel(ref.Family) || seen[ref.Digest] {
			return ErrInvalid
		}
		if ref.Bytes <= 0 || ref.Bytes > MaxChunkBytes || ref.Count <= 0 || ref.Count > MaxRecords {
			return ErrLimit
		}
		if err := addReference(ref.Bytes, total, references); err != nil {
			return err
		}
		counts[ref.Family] += ref.Count // bounded by MaxReferences * MaxRecords
		seen[ref.Digest] = true
	}
	if len(counts) != len(c.Counts) {
		return ErrInvalid
	}
	for family, count := range c.Counts {
		if !validLabel(family) || count <= 0 || counts[family] != count {
			return ErrInvalid
		}
	}
	for _, blob := range c.Blobs {
		if (c.Domain != "uploads" && c.Domain != "agents") || !validDigest(blob.Digest) || seen[blob.Digest] {
			return ErrInvalid
		}
		if blob.Bytes < 0 || blob.Bytes > MaxBlobBytes {
			return ErrLimit
		}
		if err := addReference(blob.Bytes, total, references); err != nil {
			return err
		}
		seen[blob.Digest] = true
	}
	return nil
}

func addReference(size int64, total *int64, references *int) error {
	if *references >= MaxReferences || size > MaxTotalBytes-*total {
		return ErrLimit
	}
	*total += size
	*references++
	return nil
}
