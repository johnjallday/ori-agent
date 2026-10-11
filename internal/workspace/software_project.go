package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SoftwareProjectKey is the shared-data key of a workspace's software-project
// binding.
const SoftwareProjectKey = "software_project"

const maxSoftwareProjectBytes = 4096

// ErrInvalidSoftwareProject reports a binding that is malformed. A malformed
// binding is never ignored: the workspace's code folder is then unavailable
// rather than quietly resolved some other way.
var ErrInvalidSoftwareProject = errors.New("invalid software project binding")

// ErrSoftwareProjectUnavailable reports a binding whose folder is not there
// any more, or is no longer the folder the user confirmed.
var ErrSoftwareProjectUnavailable = errors.New("software project folder is unavailable")

// SoftwareProjectBinding records the one outside folder a software-project
// workspace was set up for: the user's own checkout, linked where it is.
//
// It names a linked directory reference rather than holding a path, so there is
// exactly one place the path lives and the read tools, the policy resolver and
// the inspector cannot disagree about which folder is meant. ProjectPath is not
// used for this: it is confined to the workspace's own folder, on purpose.
type SoftwareProjectBinding struct {
	// DirectoryID is the workspace's linked directory reference for the folder.
	DirectoryID string `json:"directory_id"`
	// DirectoryIdentity is the device and inode witness of the directory the
	// user confirmed. A directory deleted and made again at the same path has a
	// different one, and is a different folder.
	DirectoryIdentity string `json:"directory_identity"`
	// Recipe and RecipeVersion say which host setup made the binding.
	Recipe        string    `json:"recipe"`
	RecipeVersion int       `json:"recipe_version"`
	BoundAt       time.Time `json:"bound_at"`
	// RepositoryKey and Remote describe the repository the checkout belongs to,
	// when an explicit inspection read them. They are hints for explaining that
	// two workspaces share a repository. They are never compared to decide that
	// two folders are the same checkout.
	RepositoryKey string `json:"repository_key,omitempty"`
	Remote        string `json:"remote,omitempty"`
}

// Validate checks the binding's shape. It does not touch the filesystem.
func (b SoftwareProjectBinding) Validate() error {
	for _, text := range []string{b.DirectoryID, b.DirectoryIdentity, b.Recipe} {
		if strings.TrimSpace(text) == "" || len(text) > 200 || strings.ContainsAny(text, "/\\\x00\r\n") {
			return fmt.Errorf("%w: identity fields", ErrInvalidSoftwareProject)
		}
	}
	if b.RecipeVersion < 1 || b.BoundAt.IsZero() {
		return fmt.Errorf("%w: recipe version or time", ErrInvalidSoftwareProject)
	}
	if len(b.RepositoryKey) > 64 || strings.ContainsAny(b.RepositoryKey, "/\\\x00\r\n") {
		return fmt.Errorf("%w: repository key", ErrInvalidSoftwareProject)
	}
	// A remote is a host and a path with no user, password or query; it is a
	// label, not something to connect to.
	if len(b.Remote) > 300 || strings.ContainsAny(b.Remote, "@?#\x00\r\n ") {
		return fmt.Errorf("%w: remote", ErrInvalidSoftwareProject)
	}
	return nil
}

// GetSoftwareProjectBinding strictly reads a workspace's binding. It returns
// nil with no error when the workspace has none, and an error when one is
// present but malformed.
func GetSoftwareProjectBinding(sharedData map[string]any) (*SoftwareProjectBinding, error) {
	raw, ok := sharedData[SoftwareProjectKey]
	if !ok || raw == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil || len(encoded) > maxSoftwareProjectBytes {
		return nil, fmt.Errorf("%w: malformed", ErrInvalidSoftwareProject)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var binding SoftwareProjectBinding
	if err := decoder.Decode(&binding); err != nil {
		return nil, fmt.Errorf("%w: malformed", ErrInvalidSoftwareProject)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: trailing data", ErrInvalidSoftwareProject)
	}
	if err := binding.Validate(); err != nil {
		return nil, err
	}
	return &binding, nil
}

// SetSoftwareProjectBinding stores a validated binding in shared data.
func SetSoftwareProjectBinding(sharedData map[string]any, binding SoftwareProjectBinding) error {
	if sharedData == nil {
		return fmt.Errorf("%w: no shared data", ErrInvalidSoftwareProject)
	}
	if err := binding.Validate(); err != nil {
		return err
	}
	sharedData[SoftwareProjectKey] = binding
	return nil
}

// SoftwareProjectFolder resolves a workspace's software-project folder.
//
// bound is false when the workspace has no binding: the caller then uses
// whatever it used before this binding existed. When bound is true the answer
// is final: either the confirmed folder, or an error. A binding that cannot be
// honoured (malformed, its directory reference gone or repurposed, the folder
// missing, or a different directory now at that path) never falls back to some
// other folder, because the other folder is not the one the user confirmed.
//
// identity returns a directory's witness; it is injected so this package does
// not grow a dependency on how a host derives one.
func SoftwareProjectFolder(ws *Workspace, identity func(os.FileInfo) (string, error)) (folder string, bound bool, err error) {
	if ws == nil {
		return "", false, nil
	}
	binding, err := GetSoftwareProjectBinding(ws.SharedData)
	if err != nil {
		return "", true, err
	}
	if binding == nil {
		return "", false, nil
	}
	ref, refErr := ws.GetDirectoryReference(binding.DirectoryID)
	if refErr != nil || ref == nil || ref.Purpose != "" || !filepath.IsAbs(ref.Path) {
		return "", true, fmt.Errorf("%w: its linked folder is no longer part of the workspace", ErrSoftwareProjectUnavailable)
	}
	path := filepath.Clean(ref.Path)
	info, statErr := os.Lstat(path) // #nosec G304 G703 -- the path is the workspace's own saved linked directory, set from a server-held selection
	if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", true, fmt.Errorf("%w: the folder is missing", ErrSoftwareProjectUnavailable)
	}
	if identity == nil {
		return "", true, fmt.Errorf("%w: the folder cannot be verified", ErrSoftwareProjectUnavailable)
	}
	witness, witnessErr := identity(info)
	if witnessErr != nil || witness != binding.DirectoryIdentity {
		return "", true, fmt.Errorf("%w: a different folder is now at that location", ErrSoftwareProjectUnavailable)
	}
	return path, true, nil
}
