package settingsreset

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/vault"
)

// Read-only host observation for the cancellation decision.
//
// Legacy receipts record only THAT the scope changed, never which of the many
// checks failed, and their protected paths are an unlabelled sorted set. The
// host therefore re-derives, on its own, what the receipt cannot say:
//
//   - which protected path is the workspace root, from settings.json — never
//     from the receipt, never from a client value;
//   - whether that path's reviewed digest still holds, hashing ONLY paths that
//     are already in the receipt's protected set (no request path is ever
//     hashed);
//   - whether every other part of the reviewed scope still holds, by running the
//     real recovery scope check against the receipt as the first launch saw it,
//     with the retained digests replaced by their current values so the digest
//     check cannot hide a different failure.
//
// Nothing here writes, opens an application store, or touches a native
// credential store: the scope check is given a throwaway in-memory secret store.

type retainedRole string

const (
	roleWorkspace retainedRole = "workspace"
	roleVault     retainedRole = "vault"
	roleOther     retainedRole = "other"
)

type retainedState string

const (
	retainedMatches    retainedState = "matches"
	retainedDiffers    retainedState = "differs"
	retainedUnreadable retainedState = "unreadable"
)

type retainedFact struct {
	Path  string
	Role  retainedRole
	State retainedState
}

// hostFacts is everything the pure decision needs from the filesystem.
type hostFacts struct {
	// Unverifiable is set when an independent derivation could not complete.
	Unverifiable bool
	// OtherScopeIntact: every reviewed check except the retained digests holds.
	OtherScopeIntact bool
	Retained         []retainedFact
	// Workspace describes the changed workspace folder, for wording only.
	Workspace WorkspaceCondition
}

// finderMetadata are names the desktop adds to a folder on its own. A folder
// holding only these looks empty to the person who emptied it.
var finderMetadata = []string{".DS_Store", ".localized"}

func classifyWorkspaceFolder(path string) WorkspaceCondition {
	info, err := os.Lstat(path)
	switch {
	case os.IsNotExist(err):
		return WorkspaceMissing
	case err != nil:
		return WorkspaceUnknown
	case !info.IsDir():
		return WorkspaceChanged
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return WorkspaceUnknown
	}
	for _, entry := range entries {
		if !slices.Contains(finderMetadata, entry.Name()) {
			return WorkspaceChanged
		}
	}
	return WorkspaceEmpty
}

func observeCancelHost(ctx context.Context, lease *resetstate.Lease, j *journal, options RecoveryOptions) hostFacts {
	unverifiable := hostFacts{Unverifiable: true}
	if lease == nil || j == nil {
		return unverifiable
	}
	root, err := resolvePath(options.DataDir)
	if err != nil || root != lease.Path() || j.Plan.Installation != root {
		return unverifiable
	}
	workspaceRoots, vaultRoot, err := independentRetainedRoots(root)
	if err != nil {
		return unverifiable
	}

	staged, err := reconstructStagedReceipt(j)
	if err != nil {
		return unverifiable
	}
	facts := hostFacts{}
	for i, recorded := range j.Plan.Evidence.ProtectedDigests {
		fact := retainedFact{Path: recorded.Path, Role: roleOther}
		switch {
		case recorded.Path == vaultRoot:
			fact.Role = roleVault
		case slices.Contains(workspaceRoots, recorded.Path):
			fact.Role = roleWorkspace
		}
		resolved, resolveErr := resolvePath(recorded.Path)
		if resolveErr != nil || resolved != recorded.Path {
			fact.State = retainedUnreadable
			facts.Retained = append(facts.Retained, fact)
			continue
		}
		digest, digestErr := j.Plan.Evidence.digestRetained(ctx, resolved)
		switch {
		case ctx.Err() != nil:
			return unverifiable
		case digestErr != nil:
			fact.State = retainedUnreadable
		case digest == recorded.Digest:
			fact.State = retainedMatches
		default:
			fact.State = retainedDiffers
			if fact.Role == roleWorkspace && facts.Workspace == WorkspaceUnknown {
				facts.Workspace = classifyWorkspaceFolder(resolved)
			}
		}
		// Replace the reviewed digest by the current one so the shared scope check
		// below judges everything BUT the retained digests.
		if fact.State != retainedUnreadable {
			staged.Plan.Evidence.ProtectedDigests[i].Digest = digest
		}
		facts.Retained = append(facts.Retained, fact)
	}
	if slices.ContainsFunc(facts.Retained, func(f retainedFact) bool { return f.State == retainedUnreadable }) {
		// The decision refuses on this; the shared check would only repeat it.
		return facts
	}
	if ctx.Err() != nil {
		return unverifiable
	}
	_, _, _, scopeErr := validateRecoveryScope(ctx, lease, staged, RecoveryOptions{
		DataDir:     options.DataDir,
		SecretStore: vault.NewMemorySecretStore(), // never the real credential store
	})
	facts.OtherScopeIntact = scopeErr == nil
	return facts
}

// reconstructStagedReceipt returns an independent copy of the receipt as the
// first recovery launch read it: awaiting_restart at revision 2, no blocker.
// Only a receipt that already proved it came from that state may be passed in.
func reconstructStagedReceipt(j *journal) (*journal, error) {
	data, err := encodeJournal(j)
	if err != nil {
		return nil, err
	}
	copied, err := decodeJournal(data)
	if err != nil {
		return nil, err
	}
	copied.Operation.State = StateAwaitingRestart
	copied.Operation.Revision = 2
	copied.Operation.Blockers = []Blocker{}
	return copied, nil
}

// independentRetainedRoots re-derives, from settings alone and without opening
// any application store, which protected paths are the workspace: the root the
// running server would choose (internal/server resolveWorkspaceRoot), the
// configured root and the default root. More than one is accepted because the
// receipt's protected set is a union and a path is only ever attributed when it
// equals one of them. The vault root is derived the same way.
func independentRetainedRoots(root string) (workspaceRoots []string, vaultRoot string, err error) {
	settings := config.NewManager(filepath.Join(root, "settings.json"))
	if err := settings.Load(); err != nil {
		return nil, "", err
	}
	serverChoice := settings.GetWorkspaceRoot()
	switch {
	case serverChoice != "":
	case settings.IsWorkspaceRootConfirmed():
		serverChoice = config.ResolveWorkspaceRoot("")
	case strings.TrimSpace(os.Getenv("WORKSPACE_DIR")) != "":
		serverChoice = config.ResolveWorkspaceRoot("")
	default:
		serverChoice = filepath.Join(root, "workspace-staging")
	}
	if vaultRoot, err = resolvePath(config.ResolveVaultRoot(settings.GetVaultRoot())); err != nil {
		return nil, "", err
	}
	for _, candidate := range []string{serverChoice, settings.GetWorkspaceRoot(), config.ResolveWorkspaceRoot("")} {
		if candidate == "" {
			continue
		}
		resolved, err := resolvePath(candidate)
		if err != nil {
			return nil, "", err
		}
		// One folder cannot be both. A path that is the vault is never the
		// workspace, which makes a change to it refuse.
		if resolved != vaultRoot && !slices.Contains(workspaceRoots, resolved) {
			workspaceRoots = append(workspaceRoots, resolved)
		}
	}
	return workspaceRoots, vaultRoot, nil
}
