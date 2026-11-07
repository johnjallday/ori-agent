package projectlibrary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	BrowserMaxEntries    = 500
	BrowserMaxListBytes  = 128 << 10
	BrowserMaxTextBytes  = 512 << 10
	BrowserMaxImageBytes = 2 << 20
	BrowserTimeout       = 3 * time.Second
)

var (
	ErrBrowserDenied      = errors.New("project folder access is unavailable. Discovery metadata is not a file-read grant")
	ErrBrowserUnsupported = errors.New("this project folder or file is not supported for preview")
	ErrBrowserChanged     = errors.New("the project folder, file or permission changed. Refresh Files")
	ErrBrowserMissing     = errors.New("the project folder or file is missing")
	ErrBrowserLarge       = errors.New("this file exceeds the preview limit")
)

type BrowserEntry struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Kind     string    `json:"kind"`
	Size     int64     `json:"size,omitempty"`
	Modified time.Time `json:"modified"`
	Version  string    `json:"version,omitempty"` // Comparison witness, never a credential.
}

type BrowserListing struct {
	Witness   string         `json:"-"`
	Path      string         `json:"path"`
	Entries   []BrowserEntry `json:"entries"`
	Truncated bool           `json:"truncated"`
	Reason    string         `json:"reason,omitempty"`
}

type BrowserPreview struct {
	MediaType string
	Bytes     []byte
}

// browserSource is process-local authority, never serialized into a response.
// The independently granted child reference authorizes the read. An observation
// contributes only a saved identity witness; a discovery grant cannot do so.
type browserSource struct {
	Root      Root
	Locator   workspace.ProjectEntryLocator
	Reference workspace.DirectoryReference
	Link      ExactLink
}

func browserRelative(value string, file bool) bool {
	if value == "" {
		return !file
	}
	if len(value) > 1024 || !utf8.ValidString(value) || strings.ContainsAny(value, "\\\x00") ||
		strings.HasPrefix(value, "/") || len(strings.Split(value, "/")) > 32 {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
		for _, r := range part {
			if unicode.IsControl(r) {
				return false
			}
		}
	}
	return true
}

func (s *Store) browserSource(scope Scope, entryID string) (browserSource, error) {
	fail := browserSource{}
	if !validText(entryID, 160) || entryID == "" {
		return fail, ErrBrowserDenied
	}
	doc, state, err := s.readSnapshot(scope)
	if err != nil {
		return fail, ErrBrowserDenied
	}
	entry := sessionEntry(doc, entryID)
	if entry == nil || entry.Link == nil {
		return fail, ErrBrowserDenied
	}
	child, ref, locator, link, err := s.exactLinkedChild(scope, state, entry.Link.WorkspaceID)
	if err != nil || link != *entry.Link || strings.TrimSpace(ref.Purpose) != "" ||
		!workspace.AssistantProjectLinkMirrorsAgree(s.workspaces, child) {
		return fail, ErrBrowserDenied
	}
	// SQLite stores directory references too. Portable locator metadata may be
	// folder-only, but a different/missing primary reference is not an overlay.
	primaryRef, err := child.GetDirectoryReference(ref.ID)
	if err != nil || !reflect.DeepEqual(primaryRef, ref) {
		return fail, ErrBrowserDenied
	}
	if primaryLocator, err := workspace.GetProjectEntryLocator(child.SharedData); err != nil ||
		(primaryLocator != nil && *primaryLocator != *locator) {
		return fail, ErrBrowserDenied
	}
	identity := ""
	for _, observed := range entry.Observations {
		for _, root := range doc.Roots {
			if root.ID != observed.RootID || filepath.Join(root.Path, observed.RelativeFolder) != ref.Path {
				continue
			}
			if identity != "" && identity != observed.FileIdentity {
				return fail, ErrBrowserChanged
			}
			identity = observed.FileIdentity
		}
	}
	// Legacy link-only references have no durable inode witness. Never silently
	// enroll the currently found path, nor require/revive discovery consent.
	if identity == "" {
		return fail, ErrBrowserUnsupported
	}
	return browserSource{Root: Root{Path: ref.Path, FileIdentity: identity}, Locator: *locator, Reference: *ref, Link: link}, nil
}

func (s *Store) browserStillAuthorized(ctx context.Context, scope Scope, entryID string, before browserSource) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	after, err := s.browserSource(scope, entryID)
	if err != nil || !reflect.DeepEqual(before, after) {
		return ErrBrowserChanged
	}
	if err := browserCheckRoot(after); err != nil {
		return err
	}
	return ctx.Err()
}

func browserVersion(source browserSource, relative, identity string, size int64, modified time.Time) string {
	data, _ := json.Marshal(struct {
		Source         browserSource
		Path, Identity string
		Size           int64
		Modified       time.Time
	}{source, relative, identity, size, modified})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// BrowserList reads just one directory. Source resolution is owner-scoped and
// repeated after the bounded read; no discovery method or recursive API runs.
func (s *Store) BrowserList(ctx context.Context, scope Scope, entryID, relative string) (BrowserListing, error) {
	if !browserRelative(relative, false) {
		return BrowserListing{}, ErrBrowserDenied
	}
	ctx, cancel := context.WithTimeout(ctx, BrowserTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return BrowserListing{}, err
	}
	source, err := s.browserSource(scope, entryID)
	if err != nil {
		return BrowserListing{}, err
	}
	listing, err := browserReadDirectory(ctx, source, relative)
	if err != nil {
		return BrowserListing{}, err
	}
	if err := s.browserStillAuthorized(ctx, scope, entryID, source); err != nil {
		return BrowserListing{}, err
	}
	if err := browserCheckLocation(source, relative, listing.Witness, false); err != nil {
		return BrowserListing{}, err
	}
	// Bound actual JSON expansion in linear time, including escaped paths and
	// the worst-case envelope, not repeated serialization of shrinking pages.
	header, _ := json.Marshal(BrowserListing{Path: relative, Entries: []BrowserEntry{}, Truncated: true, Reason: "unsupported_names"})
	budget := BrowserMaxListBytes - len(header)
	for i, row := range listing.Entries {
		if err := ctx.Err(); err != nil {
			return BrowserListing{}, err
		}
		encoded, err := json.Marshal(row)
		if err != nil {
			return BrowserListing{}, ErrBrowserUnsupported
		}
		budget -= len(encoded) + 1
		if budget < 0 {
			listing.Entries = listing.Entries[:i]
			listing.Truncated, listing.Reason = true, "response_limit"
			break
		}
	}
	return listing, nil
}

func (s *Store) BrowserPreview(ctx context.Context, scope Scope, entryID, relative, version string) (BrowserPreview, error) {
	if !browserRelative(relative, true) || !validDigest(version) {
		return BrowserPreview{}, ErrBrowserDenied
	}
	ctx, cancel := context.WithTimeout(ctx, BrowserTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return BrowserPreview{}, err
	}
	source, err := s.browserSource(scope, entryID)
	if err != nil {
		return BrowserPreview{}, err
	}
	preview, err := browserReadPreview(ctx, source, relative, version)
	if err != nil {
		return BrowserPreview{}, err
	}
	if err := s.browserStillAuthorized(ctx, scope, entryID, source); err != nil {
		return BrowserPreview{}, err
	}
	if err := browserCheckLocation(source, relative, version, true); err != nil {
		return BrowserPreview{}, err
	}
	return preview, nil
}

func browserPreviewType(relative string) (string, int) {
	switch strings.ToLower(path.Ext(relative)) {
	case ".png":
		return "image/png", BrowserMaxImageBytes
	case ".jpg", ".jpeg":
		return "image/jpeg", BrowserMaxImageBytes
	case ".txt", ".md", ".markdown", ".html", ".htm", ".css", ".js", ".mjs", ".ts", ".tsx", ".jsx", ".json", ".yaml", ".yml", ".toml", ".xml", ".csv", ".go", ".py", ".rs", ".c", ".h", ".cpp", ".sh", ".sql", ".log":
		return "text/plain; charset=utf-8", BrowserMaxTextBytes
	default:
		return "", 0
	}
}
