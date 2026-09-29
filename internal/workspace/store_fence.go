package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// WorkspaceFenceLockFile is the per-folder advisory lock every workspace.json
// writer holds across a write. It lives beside workspace.json so a rename of
// the folder carries the lock with it, and it is never user content.
const WorkspaceFenceLockFile = ".workspace.lock"

// ErrStaleWorkspaceVersion reports a write whose base version no longer matches
// the stored record: another writer (in this process or another one) persisted
// a newer version after this caller read the workspace. An Assistant Home or a
// split project child refuses such a write on every mirror so that an
// acknowledged change is never silently overwritten; the caller must reload the
// workspace and review its action again. Ordinary workspaces keep the older
// permissive behavior and only log the possible lost write.
var ErrStaleWorkspaceVersion = errors.New("workspace changed since it was read; reload and retry")

// VersionedSaver is implemented by a primary store that can persist a workspace
// only while the stored version still equals the version the writer read. The
// check and the write are one conditional statement on the store, never a
// read followed by an unconditional update.
type VersionedSaver interface {
	SaveExpecting(ws *Workspace, expectedVersion int64) error
}

// WorkspaceFenceProtected reports whether ws is an Assistant Home or a split
// project child: the records whose folder and primary mirrors must never
// diverge silently and whose stale writes are refused rather than logged.
func WorkspaceFenceProtected(ws *Workspace) bool {
	return ws != nil && (ws.AssistantProgramState != nil || ws.AssistantProjectLink != nil)
}

// folderFence holds the cross-process lock on one workspace folder for the
// duration of a write, together with the exact record found on disk before the
// write. SyncStore keeps the fence across the folder write, the primary write
// and any rollback so a writer in another process cannot interleave between
// the two mirrors.
type folderFence struct {
	store      *FileStore
	id         string
	folder     string // absolute folder path; empty when the workspace has no folder yet
	rel        string // idToPath value recorded when the fence was taken
	configPath string
	before     *Workspace // parsed on-disk record; nil when none (or unreadable) existed
	beforeRaw  []byte     // exact bytes found on disk; nil when no file existed
	release    func()
	// onWrite runs with the exact bytes about to replace workspace.json, before
	// the atomic rename. SyncStore uses it to complete and persist the journal.
	onWrite func(data []byte) error
}

// openFence resolves the workspace's current folder, takes its advisory lock
// and reads the record beneath it. A workspace with no folder mapping yet gets
// an unlocked fence: nothing on disk can be overwritten, and Save's own
// folder-exists check still refuses a colliding folder.
func (s *FileStore) openFence(id string) (*folderFence, error) {
	if s == nil {
		return nil, fmt.Errorf("workspace store is unavailable")
	}
	if strings.TrimSpace(id) == "" {
		return &folderFence{store: s, id: id}, nil
	}
	for attempt := 0; attempt < 16; attempt++ {
		rel, folder, ok := s.folderFor(id)
		if !ok {
			return &folderFence{store: s, id: id}, nil
		}
		// #nosec G301 -- workspace folders are user-visible directories that already use 0755 elsewhere in this store.
		if err := os.MkdirAll(folder, 0o755); err != nil {
			return nil, fmt.Errorf("prepare workspace folder for fence: %w", err)
		}
		release, err := lockWorkspaceFolder(folder)
		if err != nil {
			return nil, err
		}
		if _, current, stillOK := s.folderFor(id); !stillOK || current != folder {
			// The mapping moved while we waited for the lock; lock the new folder.
			release()
			continue
		}
		fence := &folderFence{
			store: s, id: id, folder: folder, rel: rel,
			configPath: filepath.Join(folder, WorkspaceConfigFile), release: release,
		}
		raw, err := os.ReadFile(fence.configPath) // #nosec G304 -- the fixed workspace.json inside the store-resolved folder, not a request path.
		if err != nil {
			if os.IsNotExist(err) {
				return fence, nil
			}
			release()
			return nil, fmt.Errorf("read workspace record under fence: %w", err)
		}
		fence.beforeRaw = raw
		if before, parseErr := FromJSON(raw); parseErr == nil {
			fence.before = before
		} else {
			logger.Warn("workspace record unreadable under fence", logger.Fields{
				"workspace_id": id, "error": parseErr.Error(),
			})
		}
		return fence, nil
	}
	return nil, fmt.Errorf("workspace %s: folder mapping kept changing while acquiring its fence", id)
}

func (s *FileStore) folderFor(id string) (rel, folder string, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rel, ok = s.idToPath[id]
	if !ok {
		return "", "", false
	}
	return rel, s.resolveFolder(rel), true
}

// Before returns a private copy of the record that was on disk when the fence
// was taken, or nil when there was none.
func (f *folderFence) Before() *Workspace {
	if f == nil || f.before == nil {
		return nil
	}
	clone, err := cloneWorkspaceForRebind(f.before)
	if err != nil {
		return nil
	}
	return clone
}

// refuseStale enforces the folder side of the fence for one write. An
// unreadable protected record is never overwritten. On a single-store
// FileStore (no SyncStore mirror) the folder version is the sequence: a
// protected record whose on-disk version differs from the writer's base is
// refused and an ordinary stale write is logged. On a mirrored FileStore the
// primary's version is the sequence (SyncStore checks it), legacy folder-only
// writers may lawfully leave the folder version out of step, so no version is
// compared here; instead a protected record with a pending interrupted-write
// journal refuses direct writes until that split is reconciled. Callers hold
// the fence.
func (f *folderFence) refuseStale(ws *Workspace) error {
	if f == nil || f.folder == "" || ws == nil {
		return nil
	}
	protected := WorkspaceFenceProtected(ws) || WorkspaceFenceProtected(f.before)
	if f.before == nil {
		if f.beforeRaw != nil && protected {
			return fmt.Errorf("workspace %s: refusing to overwrite an unreadable workspace.json for a Home or project link", ws.ID)
		}
		return nil
	}
	if f.store != nil && f.store.mirrored.Load() {
		if protected && f.onWrite == nil {
			pending, err := f.pendingJournal()
			if err != nil {
				return err
			}
			if pending != nil {
				return fmt.Errorf("%w: workspace %s has an interrupted mirrored write %s pending; reconcile it before writing the folder directly",
					ErrWorkspaceMirrorsDiverged, ws.ID, pending.OperationID)
			}
		}
		return nil
	}
	if f.before.Version == ws.Version {
		return nil
	}
	if protected {
		return fmt.Errorf("%w: workspace %s is at version %d on disk, this write was based on version %d",
			ErrStaleWorkspaceVersion, ws.ID, f.before.Version, ws.Version)
	}
	logger.Warn("possible lost write: saving workspace over a different on-disk version",
		logger.Fields{
			"workspace_id":     ws.ID,
			"incoming_version": ws.Version,
			"disk_version":     f.before.Version,
		})
	return nil
}

// protectedEnvelopesAgree reports whether two mirrors of one workspace carry
// the same Assistant Home state and project link, using the comparators the
// Home library and the staffing guard already rely on. Serialization
// whitespace is ignored; a Home or link present on one side only disagrees.
func protectedEnvelopesAgree(primary, folder *Workspace) bool {
	if primary == nil || folder == nil {
		return primary == nil && folder == nil
	}
	primaryState, folderState := primary.GetAssistantProgramState(), folder.GetAssistantProgramState()
	switch {
	case primaryState == nil && folderState == nil:
	case primaryState == nil || folderState == nil:
		return false
	default:
		if !AssistantProgramLibraryInputsMatch(primaryState, folderState) ||
			(len(primaryState.ProjectLibrary) == 0) != (len(folderState.ProjectLibrary) == 0) {
			return false
		}
		if len(primaryState.ProjectLibrary) != 0 {
			var primaryJSON, folderJSON bytes.Buffer
			if json.Compact(&primaryJSON, primaryState.ProjectLibrary) != nil ||
				json.Compact(&folderJSON, folderState.ProjectLibrary) != nil ||
				!bytes.Equal(primaryJSON.Bytes(), folderJSON.Bytes()) {
				return false
			}
		}
	}
	primaryLink, folderLink := primary.GetAssistantProjectLink(), folder.GetAssistantProjectLink()
	switch {
	case primaryLink == nil && folderLink == nil:
		return true
	case primaryLink == nil || folderLink == nil:
		return false
	}
	first, firstErr := json.Marshal(primaryLink)
	second, secondErr := json.Marshal(folderLink)
	return firstErr == nil && secondErr == nil && bytes.Equal(first, second)
}

// restore puts the exact pre-write bytes back (or removes a folder the write
// created) so a failed primary write leaves the folder mirror at its previous
// version rather than one bump ahead. Callers hold the fence.
func (f *folderFence) restore() error {
	if f == nil || f.store == nil {
		return nil
	}
	if f.folder == "" || f.beforeRaw == nil {
		return f.store.Delete(f.id)
	}
	f.store.mu.Lock()
	defer f.store.mu.Unlock()
	if err := atomicWriteFile(f.configPath, f.beforeRaw); err != nil {
		return err
	}
	f.store.idToPath[f.id] = f.rel
	if f.before != nil {
		f.store.cacheMeta(f.before)
	}
	return nil
}

// RestoreMirrorRecord writes ws to its folder exactly as given: no version
// bump, no stale check. It is the primitive an explicitly reviewed
// reconciliation uses to put a diverged folder mirror back beside its primary
// (including the primary's version), and what disposable drills use to undo
// an injected split. Ordinary writers must use Save; there is no HTTP route
// to this method and it never chooses a mirror on its own.
func (s *FileStore) RestoreMirrorRecord(ws *Workspace) error {
	if s == nil || ws == nil || strings.TrimSpace(ws.ID) == "" {
		return fmt.Errorf("workspace is required")
	}
	fence, err := s.openFence(ws.ID)
	if err != nil {
		return err
	}
	defer fence.close()
	if fence.folder == "" {
		return fmt.Errorf("workspace %s has no folder to restore", ws.ID)
	}
	data, err := ws.ToJSON()
	if err != nil {
		return fmt.Errorf("failed to serialize workspace: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := atomicWriteFile(fence.configPath, data); err != nil {
		return fmt.Errorf("failed to restore workspace file: %w", err)
	}
	restored, err := FromJSON(data)
	if err != nil {
		return fmt.Errorf("failed to reload restored workspace: %w", err)
	}
	s.cacheMeta(restored)
	// The caller has explicitly reconciled the mirrors; an interrupted-write
	// journal no longer describes the folder and must not keep it fenced.
	return fence.clearJournal()
}

func (f *folderFence) close() {
	if f == nil || f.release == nil {
		return
	}
	f.release()
	f.release = nil
}
