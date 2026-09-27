package projectlibrary

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

var errReplay = errors.New("project library operation already recorded")

// Store uses the canonical Home envelope, not a second index or source-folder
// sidecar. The absence of ProjectLibrary is the legacy-authority marker.
type Store struct {
	workspaces       workspace.Store
	now              func() time.Time
	providerEvidence func(Scope, *workspace.Workspace) bool
}

func NewStore(workspaces workspace.Store) *Store {
	return &Store{workspaces: workspaces, now: time.Now}
}

// WithProviderEvidence binds a host-owned installed-provider check at service
// construction. Read-only history remains available if evidence disappears;
// reviews and mutations do not. Call this before publishing the Store.
func (s *Store) WithProviderEvidence(check func(Scope, *workspace.Workspace) bool) *Store {
	s.providerEvidence = check
	return s
}

func (s *Store) providerWritable(scope Scope, home *workspace.Workspace) bool {
	if s == nil || home == nil {
		return false
	}
	state := home.GetAssistantProgramState()
	return state != nil && state.PluginAvailable &&
		(s.providerEvidence == nil || s.providerEvidence(scope, home))
}

func (s *Store) home(scope Scope, home *workspace.Workspace) (*workspace.AssistantProgramState, error) {
	if !scope.valid() || home == nil || home.ID != scope.HomeID ||
		home.OwnerUserID != scope.OwnerUserID || home.Status == workspace.StatusTrashed ||
		home.Status == workspace.StatusMissing || home.GetAssistantProjectLink() != nil {
		return nil, ErrUnavailable
	}
	state := home.GetAssistantProgramState()
	if state == nil || state.Key.Normalize() != (workspace.AssistantProgramKey{
		OwnerUserID: scope.OwnerUserID, PluginID: scope.ProviderID, ProgramID: scope.ProgramID,
	}).Normalize() {
		return nil, ErrUnavailable
	}
	// SyncStore writes the folder mirror before SQLite. A crash between those
	// saves cannot be made transactionally atomic by the two stores; fail closed
	// instead of silently overwriting the newer side (including its receipt).
	if mirror, ok := s.workspaces.(workspace.MirrorWorkspaceProvider); ok {
		folder, mirrored, err := mirror.GetMirrorWorkspace(scope.HomeID)
		if !mirrored {
			return state, nil
		}
		if err != nil || folder == nil {
			return nil, ErrMirrorDiverged
		}
		folderState := folder.GetAssistantProgramState()
		if folderState == nil || !workspace.AssistantProgramLibraryInputsMatch(state, folderState) ||
			(len(folderState.ProjectLibrary) == 0) != (len(state.ProjectLibrary) == 0) {
			return nil, ErrMirrorDiverged
		}
		if len(state.ProjectLibrary) != 0 {
			var primaryJSON, folderJSON bytes.Buffer
			if json.Compact(&primaryJSON, state.ProjectLibrary) != nil ||
				json.Compact(&folderJSON, folderState.ProjectLibrary) != nil ||
				!bytes.Equal(primaryJSON.Bytes(), folderJSON.Bytes()) {
				return nil, ErrMirrorDiverged
			}
		}
	}
	return state, nil
}

// Read never creates, migrates or scans. The caller must have separately
// established the current user's right to read this exact Home.
func (s *Store) Read(scope Scope) (Document, error) {
	doc, _, err := s.readSnapshot(scope)
	return doc, err
}

// readSnapshot returns metadata and its Home authority from the same read.
// Query consumers must not combine an older document with newer link/member
// state fetched independently after a concurrent Home mutation.
func (s *Store) readSnapshot(scope Scope) (Document, *workspace.AssistantProgramState, error) {
	if s == nil || s.workspaces == nil || !scope.valid() {
		return Document{}, nil, ErrUnavailable
	}
	home, err := s.workspaces.Get(scope.HomeID)
	if err != nil {
		return Document{}, nil, ErrUnavailable
	}
	state, err := s.home(scope, home)
	if err != nil {
		return Document{}, nil, err
	}
	if len(state.ProjectLibrary) == 0 {
		return Document{}, nil, ErrNotInitialized
	}
	doc, err := decodeDocument(state.ProjectLibrary, scope)
	if err != nil || !inactiveRootsValid(state, doc) {
		return Document{}, nil, ErrCorrupt
	}
	return doc, state, nil
}

func inactiveRootsValid(state *workspace.AssistantProgramState, doc Document) bool {
	if state == nil || len(state.ProjectLibraryInactiveRoots) > len(doc.Roots) {
		return false
	}
	known := make(map[string]bool, len(doc.Roots))
	for _, root := range doc.Roots {
		known[root.ID] = true
	}
	seen := map[string]bool{}
	for _, id := range state.ProjectLibraryInactiveRoots {
		if !known[id] || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

// operation is only usable inside this package; request handlers cannot
// publish a library mutation without the separate review/policy service.
type operation struct {
	key    string
	action string
	digest string
}

// mutate is a single Home Update for an already initialized document.
// Only CommitInitialize can cross the legacy-to-library authority boundary.
// A matching operation replay precedes revision CAS.
// fn must have no external side effects (cross-store actions require a
// separate reconciliation protocol). The return value is only valid on success.
func (s *Store) mutate(scope Scope, expected int64, op operation, fn func(*Document) (string, error)) (OperationReceipt, bool, error) {
	return s.mutateWithPolicy(scope, expected, op, nil, fn)
}

func (s *Store) mutateWithPolicy(scope Scope, expected int64, op operation, policy func(*workspace.AssistantProgramState) bool, fn func(*Document) (string, error)) (OperationReceipt, bool, error) {
	if policy == nil {
		return s.mutateWithHomePolicy(scope, expected, op, nil, fn)
	}
	return s.mutateWithHomePolicy(scope, expected, op, func(state *workspace.AssistantProgramState, _ *workspace.Workspace) bool {
		return policy(state)
	}, fn)
}

// mutateWithHomePolicy is for finalizing a consequence whose policy must
// observe the same Home snapshot being atomically updated. In particular, a
// scan that began under an installed provider may only publish candidates if
// that exact provider still matches at the final persistence boundary.
func (s *Store) mutateWithHomePolicy(scope Scope, expected int64, op operation, policy func(*workspace.AssistantProgramState, *workspace.Workspace) bool, fn func(*Document) (string, error)) (OperationReceipt, bool, error) {
	if s == nil || s.workspaces == nil || !scope.valid() || expected < 0 || fn == nil ||
		!validText(op.key, 160) || op.key == "" || !validText(op.action, 80) || op.action == "" ||
		!validText(op.digest, 160) || op.digest == "" || expected == 0 {
		return OperationReceipt{}, false, ErrConflict
	}
	var result OperationReceipt
	var replay bool
	err := s.workspaces.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state, err := s.home(scope, home)
		if err != nil {
			return err
		}
		// Revoking an existing grant is a reductive owner action. It must
		// remain possible when the installed provider has disappeared.
		if ((op.action != "review_revoke_root" && op.action != "revoke_root" && op.action != "scan_finish") &&
			!s.providerWritable(scope, home)) || (policy != nil && !policy(state, home)) {
			return ErrUnavailable
		}
		if len(state.ProjectLibrary) == 0 {
			return ErrNotInitialized
		}
		doc, err := decodeDocument(state.ProjectLibrary, scope)
		if err != nil || !inactiveRootsValid(state, doc) {
			return ErrCorrupt
		}
		for _, prior := range doc.Operations {
			if prior.Key != op.key {
				continue
			}
			if prior.Action != op.action || prior.Digest != op.digest {
				return ErrConflict
			}
			result, replay = prior, true
			return errReplay
		}
		if doc.Revision != expected {
			return ErrConflict
		}
		if len(doc.Operations) == maxOperations {
			return ErrLimit
		}
		consequenceID, err := fn(&doc)
		if err != nil {
			return err
		}
		if consequenceID == "" || !validText(consequenceID, 160) {
			return ErrCorrupt
		}
		doc.Revision++
		result = OperationReceipt{
			Key: op.key, Action: op.action, Digest: op.digest, ConsequenceID: consequenceID,
			Revision: doc.Revision, RecordedAt: s.now().UTC(),
		}
		doc.Operations = append(doc.Operations, result)
		if !doc.valid(scope) {
			return ErrCorrupt
		}
		encoded, err := json.Marshal(doc)
		if err != nil || len(encoded) > maxDocumentBytes {
			return ErrLimit
		}
		state.ProjectLibrary = encoded
		home.SetAssistantProgramState(state)
		return nil
	})
	if errors.Is(err, errReplay) && replay {
		return result, true, nil
	}
	if err != nil {
		// Preserve the actionable error from the callback; never turn failed
		// saves into success or claim an operation was recorded.
		if errors.Is(err, ErrUnavailable) || errors.Is(err, ErrNotInitialized) || errors.Is(err, ErrCorrupt) ||
			errors.Is(err, ErrConflict) || errors.Is(err, ErrLimit) || errors.Is(err, ErrMirrorDiverged) {
			return OperationReceipt{}, false, err
		}
		return OperationReceipt{}, false, fmt.Errorf("persist project library: %w", err)
	}
	return result, replay, nil
}

// newID is for durable records only; callers must not use an observed file
// name, mutable pathname or client-provided label as a catalog identifier.
func newID() string { return uuid.NewString() }
