package sessionfiles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// ContinuityInstall is one reviewed upload for a restored session. Open is set
// only for copied bytes; it must return exactly the reviewed object.
type ContinuityInstall struct {
	Upload ContinuityUpload
	Open   func(context.Context) (io.ReadCloser, error)
}

// InstallContinuityUploads adds reviewed uploads to a restored session through
// the normal store, preserving each file's ID, name and date. Copied bytes are
// written privately and verified against the reviewed digest. A linked or
// missing source file becomes an unavailable entry: the source machine's path
// is never installed as a live link or read. Retrying after a crash accepts
// only byte-identical files already present; anything else is a conflict and
// is never overwritten. It returns the number of newly installed entries.
func (s *Store) InstallContinuityUploads(ctx context.Context, sessionID string, items []ContinuityInstall) (int, error) {
	if s == nil || !workspacecontinuity.ValidID(sessionID) || len(items) > workspacecontinuity.MaxFiles {
		return 0, workspacecontinuity.ErrInvalid
	}
	if len(items) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	manifest, ok := s.manifests[sessionID]
	if !ok {
		manifestPath := s.getManifestPath(sessionID)
		if _, err := os.Lstat(manifestPath); errors.Is(err, os.ErrNotExist) {
			manifest = NewManifest(sessionID)
		} else {
			loaded, loadErr := LoadManifest(manifestPath)
			if loadErr != nil {
				return 0, workspacecontinuity.ErrConflict // never replace an unreadable manifest
			}
			manifest = loaded
		}
	}
	if manifest.SessionID != sessionID {
		return 0, workspacecontinuity.ErrConflict
	}
	if err := os.MkdirAll(s.getFilesPath(sessionID), 0700); err != nil {
		return 0, err
	}
	byID := make(map[string]FileEntry, len(manifest.Files))
	byPath := make(map[string]string, len(manifest.Files))
	for _, entry := range manifest.Files {
		byID[entry.ID], byPath[entry.Path] = entry, entry.ID
	}
	installed := 0
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return installed, err
		}
		value := item.Upload
		if value.SessionID != sessionID {
			return installed, workspacecontinuity.ErrInvalid
		}
		entry := value.Entry
		switch value.State {
		case "linked":
			entry.OriginalPath, entry.Status = "", FileStatusBroken
		case "missing":
			entry.Status = FileStatusBroken
		case "copied":
			entry.Status = FileStatusOK
		default:
			return installed, workspacecontinuity.ErrInvalid
		}
		if existing, found := byID[entry.ID]; found {
			if existing.Path != entry.Path || existing.Size != entry.Size || existing.IsLink != entry.IsLink {
				return installed, workspacecontinuity.ErrConflict
			}
			continue // exact prior install; keep any local edits to the entry
		}
		if other, taken := byPath[entry.Path]; taken && other != entry.ID {
			return installed, workspacecontinuity.ErrConflict
		}
		if value.State == "copied" {
			if item.Open == nil || value.Blob == nil {
				return installed, workspacecontinuity.ErrInvalid
			}
			if err := s.writeContinuityUpload(ctx, sessionID, entry, *value.Blob, item.Open); err != nil {
				return installed, err
			}
		}
		manifest.Files = append(manifest.Files, entry)
		byID[entry.ID], byPath[entry.Path] = entry, entry.ID
		installed++
	}
	if len(manifest.Files) > manifest.MaxFiles {
		manifest.MaxFiles = len(manifest.Files) // an imported history is never truncated to fit
	}
	s.manifests[sessionID] = manifest
	if err := SaveManifest(manifest, s.getManifestPath(sessionID)); err != nil {
		return installed, err
	}
	return installed, nil
}

func (s *Store) writeContinuityUpload(ctx context.Context, sessionID string, entry FileEntry, blob workspacecontinuity.BlobRef,
	open func(context.Context) (io.ReadCloser, error)) error {
	if !continuityUploadName(entry.Path) {
		return workspacecontinuity.ErrInvalid
	}
	target := filepath.Join(s.getFilesPath(sessionID), entry.Path)
	if info, err := os.Lstat(target); err == nil {
		if !info.Mode().IsRegular() || info.Size() != blob.Bytes {
			return workspacecontinuity.ErrConflict
		}
		digest, err := fileDigest(target)
		if err != nil || digest != blob.Digest {
			return workspacecontinuity.ErrConflict
		}
		return nil // byte-identical file from an interrupted earlier attempt
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	reader, err := open(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	temporary := filepath.Join(s.getFilesPath(sessionID), ".import-"+uuid.NewString())
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temporary) }()
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(reader, blob.Bytes+1))
	syncErr, closeErr := file.Sync(), file.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		return err
	}
	if written != blob.Bytes || hex.EncodeToString(hash.Sum(nil)) != blob.Digest {
		return workspacecontinuity.ErrDigest
	}
	// Exclusive publication: a file created meanwhile is never replaced.
	if err := os.Link(temporary, target); err != nil {
		return workspacecontinuity.ErrConflict
	}
	return nil
}

func fileDigest(path string) (string, error) {
	file, err := os.Open(path) // #nosec G304 -- path is the store's own session file, validated above
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
