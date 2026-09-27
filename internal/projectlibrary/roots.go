package projectlibrary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// FolderPicker is implemented only by a trusted server-side native chooser.
// HTTP request JSON may never supply a path or call pathselection.IssueFor.
type FolderPicker interface {
	Available() bool
	Choose(ctx context.Context, prompt string) (path string, chosen bool, err error)
}

// PortfolioRootResolver is installed by the host from its canonical folder
// offer service. Only a resolved offer belonging to the exact owner/Home can
// supply a root; the browser provides an opaque offer ID, never a path.
type PortfolioRootResolver interface {
	PortfolioRoot(ctx context.Context, userID, offerID, homeID string) (path, identity string, err error)
}

type RootReview struct {
	Token        string    `json:"token"`
	RootPath     string    `json:"root_path"` // Show only in the authenticated user's review UI.
	Scope        string    `json:"scope"`
	IncludesScan bool      `json:"includes_scan"`
	ExpiresAt    time.Time `json:"expires_at"`
	Revision     int64     `json:"revision"`
}

// Roots owns native selections and review bindings. The scope and guards are
// host-supplied; review→commit selection references are intentionally ephemeral.
// A restart expires uncommitted reviews, not durable connected roots.
type Roots struct {
	library    *Store
	picker     FolderPicker
	selections *pathselection.Store
	guards     filejanitor.RootGuards
	mu         sync.Mutex
	pending    map[string]string // review token -> native picker reference (never a path)
}

func NewRoots(library *Store, picker FolderPicker, selections *pathselection.Store, guards filejanitor.RootGuards) *Roots {
	if guards.HomeDir == "" && guards.DataDir == "" && guards.WorkspaceRoot == "" && len(guards.ExtraForbidden) == 0 {
		guards = filejanitor.DefaultRootGuards()
	}
	return &Roots{library: library, picker: picker, selections: selections, guards: guards,
		pending: make(map[string]string)}
}

func selectionScope(scope Scope) string {
	// Length-prefixed encoding prevents identities containing separators from
	// colliding. Scope is never returned to the browser as authority.
	parts := []string{scope.OwnerUserID, scope.HomeID, scope.ProviderID, scope.ProgramID}
	var b strings.Builder
	for _, part := range parts {
		b.WriteString(strconv.Itoa(len(part)))
		b.WriteByte(':')
		b.WriteString(part)
	}
	return b.String()
}

// pickedRoot checks for broad roots, volume roots, Ori's own storage and
// symlinks in *every* selected ancestor. Resolving a symlink and silently
// granting its target would bypass the user's precise native selection.
func (r *Roots) pickedRoot(path string) (string, string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || !validText(path, 4096) {
		return "", "", ErrUnavailable
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || canonical != path {
		return "", "", ErrUnavailable
	}
	checked, err := filejanitor.ValidateRoot(path, r.guards)
	if err != nil || checked != path {
		return "", "", ErrUnavailable
	}
	info, err := os.Lstat(path) // #nosec G703 G304 -- server-held native selection or a persisted reviewed root, never request JSON.
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", "", ErrUnavailable
	}
	id, err := directoryIdentity(info)
	return path, id, err
}

func (r *Roots) writable(scope Scope) (Document, error) {
	if r == nil || r.library == nil || r.library.workspaces == nil {
		return Document{}, ErrUnavailable
	}
	doc, err := r.library.Read(scope)
	if err != nil {
		return Document{}, err
	}
	home, err := r.library.workspaces.Get(scope.HomeID)
	if err != nil {
		return Document{}, ErrUnavailable
	}
	state, err := r.library.home(scope, home)
	if err != nil || !state.PluginAvailable || !r.library.providerWritable(scope, home) {
		return Document{}, ErrUnavailable
	}
	return doc, nil
}

// Pick is the only origin of a root-selection token. A selection is neither
// durable permission nor a root; cancellation causes no library mutation.
func (r *Roots) Pick(ctx context.Context, scope Scope) (string, error) {
	if _, err := r.writable(scope); err != nil {
		return "", err
	}
	if r.picker == nil || !r.picker.Available() || r.selections == nil {
		return "", ErrUnavailable
	}
	path, chosen, err := r.picker.Choose(ctx, "Choose one project folder to catalog")
	if err != nil || !chosen {
		return "", ErrUnavailable
	}
	gate := rootAccessGate(scope)
	gate.RLock()
	defer gate.RUnlock()
	if _, err := r.writable(scope); err != nil {
		return "", err // Home could have been removed while chooser was open.
	}
	path, _, err = r.pickedRoot(path)
	if err != nil {
		return "", err
	}
	return r.selections.IssueFor(path, selectionScope(scope))
}

// DirectoryIdentity binds an earlier server-side folder selection to its
// volume/directory, without persisting the native path in an offer record.
// Unsupported platforms fail closed; an offer can always use a fresh picker.
func DirectoryIdentity(info os.FileInfo) (string, error) { return directoryIdentity(info) }

// PickerAvailable reports host capability without opening a native dialog.
func (r *Roots) PickerAvailable() bool { return r != nil && r.picker != nil && r.picker.Available() }

// PickFromPortfolio reuses a recent server-held selection after the Home was
// independently created. It still issues only a short-lived picker reference;
// connect and scan each require their own review and confirmation.
func (r *Roots) PickFromPortfolio(ctx context.Context, scope Scope, offerID string, resolver PortfolioRootResolver) (string, error) {
	if r == nil || r.selections == nil || resolver == nil || offerID == "" || len(offerID) > 160 {
		return "", ErrUnavailable
	}
	if _, err := r.writable(scope); err != nil {
		return "", err
	}
	path, originIdentity, err := resolver.PortfolioRoot(ctx, scope.OwnerUserID, offerID, scope.HomeID)
	if err != nil || originIdentity == "" {
		return "", ErrUnavailable // A lost selection requires a fresh native picker.
	}
	gate := rootAccessGate(scope)
	gate.RLock()
	defer gate.RUnlock()
	if _, err := r.writable(scope); err != nil {
		return "", err
	}
	path, identity, err := r.pickedRoot(path)
	if err != nil || identity != originIdentity {
		return "", ErrUnavailable // The folder changed after offer verification.
	}
	return r.selections.IssueFor(path, selectionScope(scope))
}

func rootDigest(scope Scope, action, path, identity string, revision int64) string {
	input := []string{selectionScope(scope), action, path, identity, stringInt(revision)}
	h := sha256.New()
	for _, piece := range input {
		_, _ = h.Write([]byte(piece))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func stringInt(value int64) string { return strconv.FormatInt(value, 10) }

func (r *Roots) currentProviderRevision(scope Scope) (int64, error) {
	home, err := r.library.workspaces.Get(scope.HomeID)
	if err != nil {
		return 0, ErrUnavailable
	}
	state, err := r.library.home(scope, home)
	if err != nil || !state.PluginAvailable || !r.library.providerWritable(scope, home) {
		return 0, ErrUnavailable
	}
	return state.StateRevision, nil
}

func (r *Roots) Review(scope Scope, pickToken string, expected int64) (RootReview, error) {
	gate := rootAccessGate(scope)
	gate.RLock()
	defer gate.RUnlock()
	if _, err := r.writable(scope); err != nil {
		return RootReview{}, err
	}
	if r.selections == nil || pickToken == "" {
		return RootReview{}, ErrUnavailable
	}
	path, err := r.selections.ResolveFor(pickToken, selectionScope(scope))
	if err != nil {
		return RootReview{}, ErrUnavailable
	}
	path, identity, err := r.pickedRoot(path)
	if err != nil {
		return RootReview{}, err
	}
	providerRevision, err := r.currentProviderRevision(scope)
	if err != nil {
		return RootReview{}, err
	}
	at := r.library.now().UTC()
	review := ReviewReceipt{Token: newID(), Action: "connect_root", ProviderRevision: providerRevision,
		Digest:   rootDigest(scope, "connect_root", path, identity, expected),
		Revision: expected + 1, ExpiresAt: at.Add(10 * time.Minute)}
	_, _, err = r.library.mutateWithPolicy(scope, expected, operation{key: review.Token, action: "review_root", digest: review.Digest},
		func(state *workspace.AssistantProgramState) bool {
			return state.PluginAvailable && state.StateRevision == providerRevision
		}, func(doc *Document) (string, error) {
			if len(doc.Reviews) >= maxReviews {
				return "", ErrLimit
			}
			doc.Reviews = append(doc.Reviews, review)
			return review.Token, nil
		})
	if err != nil {
		return RootReview{}, err
	}
	r.mu.Lock()
	r.pending[review.Token] = pickToken
	r.mu.Unlock()
	return RootReview{Token: review.Token, RootPath: path, Scope: "folder names and project markers only",
		IncludesScan: false, ExpiresAt: review.ExpiresAt, Revision: review.Revision}, nil
}

func findReview(doc Document, token, action string) (ReviewReceipt, bool) {
	for _, review := range doc.Reviews {
		if review.Token == token && review.Action == action {
			return review, true
		}
	}
	return ReviewReceipt{}, false
}

// Commit connects a durable metadata-only root, never a Home DirectoryReference
// or an Assistant Project Link. The caller must have performed a user review.
func (r *Roots) Commit(scope Scope, token, key string) (Root, bool, error) {
	gate := rootAccessGate(scope)
	gate.RLock()
	defer gate.RUnlock()
	doc, err := r.writable(scope)
	if err != nil {
		return Root{}, false, err
	}
	review, ok := findReview(doc, token, "connect_root")
	if !ok || key == "" || !validText(key, 160) {
		return Root{}, false, ErrConflict
	}
	for _, receipt := range doc.Operations {
		if receipt.Key == key {
			if receipt.Action != "connect_root" || receipt.Digest != review.Digest {
				return Root{}, false, ErrConflict
			}
			for _, root := range doc.Roots {
				if root.ID == receipt.ConsequenceID {
					return root, true, nil
				}
			}
			return Root{}, false, ErrCorrupt
		}
	}
	if review.ConsumedAt != nil || !review.ExpiresAt.After(r.library.now().UTC()) || review.Revision != doc.Revision {
		return Root{}, false, ErrConflict
	}
	providerRevision, err := r.currentProviderRevision(scope)
	if err != nil || providerRevision != review.ProviderRevision {
		return Root{}, false, ErrConflict
	}
	r.mu.Lock()
	pickToken := r.pending[token]
	r.mu.Unlock()
	if pickToken == "" || r.selections == nil {
		return Root{}, false, ErrUnavailable // re-pick after restart; no arbitrary-path fallback
	}
	path, err := r.selections.ResolveFor(pickToken, selectionScope(scope))
	if err != nil {
		return Root{}, false, ErrUnavailable
	}
	path, identity, err := r.pickedRoot(path)
	if err != nil {
		return Root{}, false, err
	}
	if review.ConsumedAt != nil || !review.ExpiresAt.After(r.library.now().UTC()) ||
		review.Revision != doc.Revision || review.Digest != rootDigest(scope, "connect_root", path, identity, review.Revision-1) {
		return Root{}, false, ErrConflict
	}
	root := Root{ID: newID(), Path: path, FileIdentity: identity, Revision: 1, ApprovedAt: r.library.now().UTC()}
	inactiveRoots := map[string]bool{}
	op, replay, err := r.library.mutateWithPolicy(scope, doc.Revision, operation{key: key, action: "connect_root", digest: review.Digest},
		func(state *workspace.AssistantProgramState) bool {
			for _, id := range state.ProjectLibraryInactiveRoots {
				inactiveRoots[id] = true
			}
			return state.PluginAvailable && state.StateRevision == review.ProviderRevision
		}, func(current *Document) (string, error) {
			if len(current.Roots) >= maxRoots {
				return "", ErrLimit
			}
			for _, prior := range current.Roots {
				if prior.RevokedAt != nil {
					continue
				}
				if !inactiveRoots[prior.ID] && (prior.Path == path || prior.FileIdentity == identity) {
					return "", ErrConflict
				}
			}
			for i := range current.Reviews {
				item := &current.Reviews[i]
				if item.Token == token && item.Action == "connect_root" && item.Digest == review.Digest &&
					item.ConsumedAt == nil && item.Revision == current.Revision && item.ExpiresAt.After(r.library.now().UTC()) {
					// Check again immediately before the Home envelope mutation.
					if _, liveID, checkErr := r.pickedRoot(path); checkErr != nil || liveID != identity {
						return "", ErrUnavailable
					}
					now := r.library.now().UTC()
					item.ConsumedAt = &now
					current.Roots = append(current.Roots, root)
					return root.ID, nil
				}
			}
			return "", ErrConflict
		})
	if err != nil {
		return Root{}, false, err
	}
	if replay {
		for _, prior := range doc.Roots {
			if prior.ID == op.ConsequenceID {
				return prior, true, nil
			}
		}
		return Root{}, false, ErrConflict
	}
	r.mu.Lock()
	delete(r.pending, token)
	r.mu.Unlock()
	return root, false, nil
}

type RevokeReview struct {
	Token      string    `json:"token"`
	RootID     string    `json:"root_id"`
	EntryCount int       `json:"entry_count"`
	ExpiresAt  time.Time `json:"expires_at"`
	Revision   int64     `json:"revision"`
}

// ReviewRevoke shows the impact of disconnecting a metadata-only source. It
// does not delete entries, user fields or independently connected projects.
func (r *Roots) ReviewRevoke(scope Scope, rootID string, expected int64) (RevokeReview, error) {
	if r == nil || r.library == nil {
		return RevokeReview{}, ErrUnavailable
	}
	doc, err := r.library.Read(scope)
	if err != nil {
		return RevokeReview{}, err
	}
	var root *Root
	for i := range doc.Roots {
		if doc.Roots[i].ID == rootID && doc.Roots[i].RevokedAt == nil {
			root = &doc.Roots[i]
			break
		}
	}
	if root == nil || doc.Revision != expected {
		return RevokeReview{}, ErrConflict
	}
	count := 0
	for _, entry := range doc.Entries {
		for _, observed := range entry.Observations {
			if observed.RootID == rootID {
				count++
				break
			}
		}
	}
	at := r.library.now().UTC()
	review := ReviewReceipt{Token: newID(), Action: "revoke_root",
		Digest:   rootDigest(scope, "revoke_root", rootID, root.FileIdentity, root.Revision),
		Revision: expected + 1, ExpiresAt: at.Add(10 * time.Minute)}
	_, _, err = r.library.mutate(scope, expected, operation{key: review.Token, action: "review_revoke_root", digest: review.Digest},
		func(current *Document) (string, error) {
			if len(current.Reviews) >= maxReviews {
				return "", ErrLimit
			}
			current.Reviews = append(current.Reviews, review)
			return review.Token, nil
		})
	if err != nil {
		return RevokeReview{}, err
	}
	return RevokeReview{Token: review.Token, RootID: rootID, EntryCount: count,
		ExpiresAt: review.ExpiresAt, Revision: review.Revision}, nil
}

func (r *Roots) CommitRevoke(scope Scope, rootID, token, key string) (Root, bool, error) {
	if r == nil || r.library == nil || key == "" || !validText(key, 160) {
		return Root{}, false, ErrConflict
	}
	gate := rootAccessGate(scope)
	gate.Lock()
	defer gate.Unlock()
	doc, err := r.library.Read(scope)
	if err != nil {
		return Root{}, false, err
	}
	review, ok := findReview(doc, token, "revoke_root")
	if !ok {
		return Root{}, false, ErrConflict
	}
	var root *Root
	for i := range doc.Roots {
		if doc.Roots[i].ID == rootID {
			root = &doc.Roots[i]
			break
		}
	}
	if root == nil {
		return Root{}, false, ErrConflict
	}
	for _, receipt := range doc.Operations {
		if receipt.Key != key {
			continue
		}
		if receipt.Action == "revoke_root" && receipt.Digest == review.Digest && receipt.ConsequenceID == rootID {
			return *root, true, nil
		}
		return Root{}, false, ErrConflict
	}
	if root.RevokedAt != nil || review.ConsumedAt != nil || review.Revision != doc.Revision ||
		!review.ExpiresAt.After(r.library.now().UTC()) ||
		review.Digest != rootDigest(scope, "revoke_root", rootID, root.FileIdentity, root.Revision) {
		return Root{}, false, ErrConflict
	}
	var result Root
	_, replay, err := r.library.mutate(scope, doc.Revision,
		operation{key: key, action: "revoke_root", digest: review.Digest}, func(current *Document) (string, error) {
			for i := range current.Reviews {
				item := &current.Reviews[i]
				if item.Token == token && item.Action == "revoke_root" && item.Digest == review.Digest &&
					item.Revision == current.Revision && item.ConsumedAt == nil && item.ExpiresAt.After(r.library.now().UTC()) {
					for j := range current.Roots {
						candidate := &current.Roots[j]
						if candidate.ID == rootID && candidate.RevokedAt == nil &&
							rootDigest(scope, "revoke_root", rootID, candidate.FileIdentity, candidate.Revision) == review.Digest {
							now := r.library.now().UTC()
							candidate.RevokedAt = &now
							candidate.Revision++
							item.ConsumedAt = &now
							result = *candidate
							return rootID, nil
						}
					}
				}
			}
			return "", ErrConflict
		})
	if err != nil {
		return Root{}, false, err
	}
	if replay {
		return *root, true, nil
	}
	return result, false, nil
}

// Root access, revocation and Home removal share one process-wide gate per
// exact canonical Home key. Lock order: gate -> Store -> no-follow filesystem
// operation; removal acknowledgement waits for in-flight directory readers.
func rootAccessGate(scope Scope) *sync.RWMutex {
	key := workspace.AssistantProgramKey{OwnerUserID: scope.OwnerUserID,
		PluginID: scope.ProviderID, ProgramID: scope.ProgramID}.Normalize()
	return workspace.AssistantHomeRootAccessGate(key.OwnerUserID, scope.HomeID, key.PluginID, key.ProgramID)
}

// VerifyConnectedRoot returns only the historical record to trusted domain
// code; actual child operations must use the descriptor-anchored walk.
func (r *Roots) VerifyConnectedRoot(scope Scope, id string) (Root, error) {
	gate := rootAccessGate(scope)
	gate.RLock()
	defer gate.RUnlock()
	return r.verifyConnectedRootNoGate(scope, id)
}

func (r *Roots) verifyConnectedRootNoGate(scope Scope, id string) (Root, error) {
	if r == nil || r.library == nil {
		return Root{}, ErrUnavailable
	}
	doc, state, err := r.library.readSnapshot(scope)
	if err != nil || !state.PluginAvailable {
		return Root{}, ErrUnavailable
	}
	home, err := r.library.workspaces.Get(scope.HomeID)
	if err != nil || !r.library.providerWritable(scope, home) {
		return Root{}, ErrUnavailable
	}
	for _, inactive := range state.ProjectLibraryInactiveRoots {
		if inactive == id {
			return Root{}, ErrUnavailable
		}
	}
	for _, root := range doc.Roots {
		if root.ID == id && root.RevokedAt == nil {
			_, liveID, err := r.pickedRoot(root.Path)
			if err == nil && liveID == root.FileIdentity {
				return root, nil
			}
			return Root{}, ErrUnavailable
		}
	}
	return Root{}, ErrUnavailable
}
