package sessionfiles

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// ContinuityUpload retains a session-owned file reference and exact copied
// bytes when available. An external link is historical evidence only: a
// destination must not install OriginalPath as a live session-files link or
// treat the source status as local file-access permission.
type ContinuityUpload struct {
	Version     int                          `json:"version"`
	WorkspaceID string                       `json:"workspace_id"`
	SessionID   string                       `json:"session_id"`
	Entry       FileEntry                    `json:"entry"`
	State       string                       `json:"state"` // copied, linked, missing
	Blob        *workspacecontinuity.BlobRef `json:"blob"`
}

func SnapshotContinuityUpload(value ContinuityUpload, owner string) (workspacecontinuity.Record, error) {
	e := value.Entry
	if value.Version != 1 || value.WorkspaceID != owner || !workspacecontinuity.ValidID(owner) || !workspacecontinuity.ValidID(value.SessionID) || !workspacecontinuity.ValidID(e.ID) ||
		!continuityUploadName(e.Path) || e.Name == "" || len(e.Name) > 1024 || !utf8.ValidString(e.Name) || e.Size < 0 || e.Size > workspacecontinuity.MaxBlobBytes ||
		e.AddedAt.IsZero() || len(e.MimeType) > 1024 || !utf8.ValidString(e.MimeType) || len(e.OriginalPath) > 4096 || !utf8.ValidString(e.OriginalPath) ||
		e.Status != FileStatusOK && e.Status != FileStatusBroken {
		return workspacecontinuity.Record{}, workspacecontinuity.ErrInvalid
	}
	switch value.State {
	case "copied":
		if e.IsLink || e.OriginalPath != "" || value.Blob == nil || value.Blob.Bytes != e.Size || !continuityBlobDigest(value.Blob.Digest) {
			return workspacecontinuity.Record{}, workspacecontinuity.ErrInvalid
		}
	case "linked":
		if !e.IsLink || value.Blob != nil {
			return workspacecontinuity.Record{}, workspacecontinuity.ErrInvalid
		}
	case "missing":
		if e.IsLink || value.Blob != nil {
			return workspacecontinuity.Record{}, workspacecontinuity.ErrInvalid
		}
	default:
		return workspacecontinuity.Record{}, workspacecontinuity.ErrInvalid
	}
	return workspacecontinuity.EncodeRecord(e.ID, value)
}

func DecodeContinuityUpload(record workspacecontinuity.Record, owner string) (ContinuityUpload, error) {
	var value ContinuityUpload
	if err := workspacecontinuity.DecodeRecord(record, &value); err != nil {
		return value, err
	}
	if value.Version != 1 {
		return value, workspacecontinuity.ErrVersion
	}
	encoded, err := SnapshotContinuityUpload(value, owner)
	if err != nil {
		return value, err
	}
	if record.ID != encoded.ID || !bytes.Equal(record.Data, encoded.Data) {
		return value, workspacecontinuity.ErrInvalid
	}
	return value, nil
}

func continuityBlobDigest(digest string) bool {
	if len(digest) != 64 {
		return false
	}
	for _, ch := range digest {
		if ch < '0' || ch > '9' && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func continuityUploadName(name string) bool {
	return workspacecontinuity.ValidID(name) && !strings.HasSuffix(name, ".") && !strings.HasSuffix(name, " ") && filepath.Base(name) == name
}

// CollectContinuityUploads pages exact SQL workspace membership, then reads
// the session-files owner from its installation-local base root. It does not
// trust Store's cache, mutate link status, follow external links, or invent
// an empty manifest for a broken session directory. The caller supplies the
// shared SQL read view and must fence session-files mutations/publication.
func CollectContinuityUploads(ctx context.Context, query workspacecontinuity.Queryer, basePath, owner string, spool *workspacecontinuity.Spool) error {
	if query == nil || spool == nil || !workspacecontinuity.ValidID(owner) {
		return workspacecontinuity.ErrInvalid
	}
	var hasOwner int
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces WHERE id=? AND owner_user_id='local' AND deleted_at IS NULL`, owner).Scan(&hasOwner); err != nil {
		return err
	}
	if hasOwner != 1 {
		return workspacecontinuity.ErrConflict
	}
	const pageSize = 256
	after, count := "", 0
	for {
		rows, err := query.QueryContext(ctx, `SELECT id FROM sessions WHERE workspace_id=? AND id>? ORDER BY id LIMIT ?`, owner, after, pageSize)
		if err != nil {
			return err
		}
		page := make([]string, 0, pageSize)
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return err
			}
			page = append(page, id)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, sessionID := range page {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !workspacecontinuity.ValidID(sessionID) {
				return workspacecontinuity.ErrInvalid
			}
			n, err := collectContinuitySessionFiles(ctx, basePath, owner, sessionID, spool)
			if err != nil {
				return err
			}
			if n > workspacecontinuity.MaxReferences*workspacecontinuity.MaxRecords-count {
				return workspacecontinuity.ErrLimit
			}
			count += n
			after = sessionID
		}
		if len(page) < pageSize {
			break
		}
	}
	if count == 0 {
		return spool.SetAvailability(ctx, "uploads", workspacecontinuity.Empty, "")
	}
	return nil
}

func collectContinuitySessionFiles(ctx context.Context, basePath, owner, sessionID string, spool *workspacecontinuity.Spool) (int, error) {
	manifestPath := sessionID + "/manifest.json"
	data, err := workspacecontinuity.ReadCanonicalFile(ctx, basePath, manifestPath, workspacecontinuity.MaxManifestBytes)
	if errors.Is(err, workspacecontinuity.ErrIncomplete) {
		entries, listingErr := workspacecontinuity.ListCanonicalDirectory(ctx, basePath, sessionID, workspacecontinuity.MaxFiles)
		if errors.Is(listingErr, workspacecontinuity.ErrIncomplete) {
			return 0, nil
		}
		if listingErr != nil {
			return 0, listingErr
		}
		_ = entries
		return 0, workspacecontinuity.ErrIncomplete
	}
	if err != nil {
		return 0, err
	}
	var manifest Manifest
	if err := workspacecontinuity.DecodeRequiredDocument(data, &manifest, workspacecontinuity.MaxManifestBytes); err != nil {
		return 0, err
	}
	if manifest.SessionID != sessionID || manifest.MaxFiles <= 0 || manifest.MaxFiles > workspacecontinuity.MaxFiles || len(manifest.Files) > manifest.MaxFiles || manifest.UpdatedAt.IsZero() {
		return 0, workspacecontinuity.ErrInvalid
	}
	declared := make(map[string]bool, len(manifest.Files))
	seenIDs := make(map[string]bool, len(manifest.Files))
	for _, entry := range manifest.Files {
		if !continuityUploadName(entry.Path) || declared[entry.Path] || seenIDs[entry.ID] {
			return 0, workspacecontinuity.ErrInvalid
		}
		declared[entry.Path], seenIDs[entry.ID] = true, true
	}
	if err := checkContinuitySessionFileCoverage(ctx, basePath, sessionID, declared); err != nil {
		return 0, err
	}
	for _, entry := range manifest.Files {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		value := ContinuityUpload{Version: 1, WorkspaceID: owner, SessionID: sessionID, Entry: entry}
		if entry.IsLink {
			value.State = "linked"
		} else {
			location := sessionID + "/files/" + entry.Path
			file, err := workspacecontinuity.OpenCanonicalFile(basePath, location)
			switch {
			case errors.Is(err, os.ErrNotExist):
				value.State = "missing"
			case err != nil:
				return 0, err
			default:
				before, statErr := file.Stat()
				if statErr != nil || !before.Mode().IsRegular() || before.Size() != entry.Size {
					_ = file.Close()
					return 0, workspacecontinuity.ErrChanged
				}
				blob, copyErr := spool.AddBlobReader(ctx, "uploads", file, entry.Size)
				after, statErr := file.Stat()
				closeErr := file.Close()
				if err := errors.Join(copyErr, statErr, closeErr); err != nil {
					return 0, err
				}
				if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
					return 0, workspacecontinuity.ErrChanged
				}
				current, err := workspacecontinuity.OpenCanonicalFile(basePath, location)
				if err != nil {
					return 0, workspacecontinuity.ErrChanged
				}
				currentInfo, statErr := current.Stat()
				closeErr = current.Close()
				if statErr != nil || closeErr != nil || !os.SameFile(before, currentInfo) {
					return 0, workspacecontinuity.ErrChanged
				}
				value.State, value.Blob = "copied", &blob
			}
		}
		record, err := SnapshotContinuityUpload(value, owner)
		if err != nil {
			return 0, err
		}
		if err := spool.AddRecord(ctx, "uploads", "uploads", record); err != nil {
			return 0, err
		}
	}
	if err := checkContinuitySessionFileCoverage(ctx, basePath, sessionID, declared); err != nil {
		return 0, err
	}
	return len(manifest.Files), nil
}

func checkContinuitySessionFileCoverage(ctx context.Context, basePath, sessionID string, declared map[string]bool) error {
	entries, err := workspacecontinuity.ListCanonicalDirectory(ctx, basePath, sessionID+"/files", workspacecontinuity.MaxFiles)
	if errors.Is(err, workspacecontinuity.ErrIncomplete) && len(declared) == 0 {
		return nil
	}
	if errors.Is(err, workspacecontinuity.ErrIncomplete) {
		return nil
	} // declared owned bytes are individually recorded as missing
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !declared[entry.Name()] {
			return workspacecontinuity.ErrIncomplete
		}
		// Symlinks are allowed only for individually declared external links.
		// The owner checks copied bytes with a confined regular-file open.
	}
	return ctx.Err()
}
