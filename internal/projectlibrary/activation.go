package projectlibrary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"slices"
	"time"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ActivationCreator is the canonical projectconnection path. Implementations
// must re-preview at commit, serialize folder attaches and verify the durable
// child/link. This interface does not expose a general workspace creator.
type ActivationCreator interface {
	Preview(context.Context, projectconnection.Scope, projectconnection.Request) (projectconnection.Preview, error)
	Commit(context.Context, projectconnection.Scope, projectconnection.Request, string, string) (projectconnection.CommitResult, error)
	HomePreparation(projectconnection.Scope) (projectconnection.HomePreparation, error)
	Observe(projectconnection.Scope, string, string, projecttemplates.ProjectConnectionMode) bool
	ObservedResult(projectconnection.Scope, string, string) (projectconnection.CommitResult, bool)
}

type ActivationCreatorFactory func(projectconnection.SelectionResolver) ActivationCreator

type ActivationService struct {
	inspector *ActivationInspector
	creator   ActivationCreatorFactory
}

func NewActivationService(inspector *ActivationInspector, creator ActivationCreatorFactory) *ActivationService {
	return &ActivationService{inspector: inspector, creator: creator}
}

type ActivationReview struct {
	Token             string    `json:"token"`
	EntryID           string    `json:"entry_id"`
	WorkspaceName     string    `json:"workspace_name"`
	ProjectFile       string    `json:"project_file"`
	BlueprintID       string    `json:"blueprint_id"`
	ProjectRoleLabels []string  `json:"project_role_labels"`
	Statement         string    `json:"statement"`
	ExpiresAt         time.Time `json:"expires_at"`
}

type ActivationResult struct {
	EntryID     string `json:"entry_id"`
	WorkspaceID string `json:"workspace_id"`
	LinkID      string `json:"link_id"`
	Replay      bool   `json:"replay"`
}

func activationDigest(scope Scope, runID string, binding ActivationBinding) (string, error) {
	encoded, err := json.Marshal(struct {
		Scope   Scope             `json:"scope"`
		RunID   string            `json:"run_id"`
		Binding ActivationBinding `json:"binding"`
	}{scope, runID, binding})
	if err != nil {
		return "", ErrCorrupt
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func activationRequest(token string, binding ActivationBinding) projectconnection.Request {
	return projectconnection.Request{
		ModeID: projecttemplates.ProjectConnectionExistingProject, SelectionToken: token,
		EntryName: binding.ProjectFile, WorkspaceName: binding.WorkspaceName, GroupComposition: "grouped",
	}
}

// providerFor always derives the template from the live installed manager and
// the current pinned Home, never from an ID or manifest supplied by a client.
func (a *ActivationInspector) providerFor(scope Scope, blueprintID string) (projecttemplates.Template, plugin.InstalledPlugin, error) {
	home, err := a.library.workspaces.Get(scope.HomeID)
	if err != nil || !a.library.providerWritable(scope, home) {
		return projecttemplates.Template{}, plugin.InstalledPlugin{}, ErrUnavailable
	}
	state, err := a.library.home(scope, home)
	if err != nil {
		return projecttemplates.Template{}, plugin.InstalledPlugin{}, err
	}
	owner := state.HomeProvider
	if owner == nil && state.GroupTemplate != nil {
		owner = state.GroupTemplate.ProgramHomeOwner
	}
	installed, err := a.installed.List()
	if err != nil || owner == nil || !plugin.IndependentHomeProviderEvidenceAvailable(installed, owner) {
		return projecttemplates.Template{}, plugin.InstalledPlugin{}, ErrUnavailable
	}
	blueprint, _, ambiguous := compatibleProjectBlueprint(installed, owner, scope)
	if ambiguous || blueprint == nil || blueprint.QualifiedID != blueprintID {
		return projecttemplates.Template{}, plugin.InstalledPlugin{}, ErrUnavailable
	}
	for _, candidate := range installed {
		if candidate.Name == blueprint.Template.PluginOwner.PluginID && candidate.Version == blueprint.Template.PluginOwner.PluginVersion &&
			candidate.Enabled && candidate.EvidenceGeneration() > 0 && candidate.ComponentFingerprint != "" {
			return blueprint.Template, candidate, nil
		}
	}
	return projecttemplates.Template{}, plugin.InstalledPlugin{}, ErrUnavailable
}

// activationResolver is constructed only inside a reviewed creator call while
// holding the exact Home's root read gate. It refuses all other tokens. No
// pathselection.Issue or browser-originated folder pathname is involved.
type activationResolver struct {
	inspector *ActivationInspector
	scope     Scope
	token     string
	binding   ActivationBinding
	ctx       context.Context
}

func (r activationResolver) Resolve(token string) (string, error) {
	if token == "" || token != r.token || r.inspector == nil || r.ctx.Err() != nil {
		return "", projectconnection.ErrUnavailable
	}
	a := r.inspector
	root, err := a.roots.verifyConnectedRootNoGate(r.scope, r.binding.RootID)
	if err != nil || root.Revision != r.binding.RootRevision {
		return "", projectconnection.ErrUnavailable
	}
	doc, err := a.library.Read(r.scope)
	if err != nil {
		return "", projectconnection.ErrUnavailable
	}
	entry := sessionEntry(doc, r.binding.EntryID)
	if entry == nil || entry.Revision != r.binding.EntryRevision || entry.Link != nil {
		return "", projectconnection.ErrChanged
	}
	found := false
	for _, observed := range entry.Observations {
		if observed.RootID == root.ID && observed.RelativeFolder == r.binding.RelativeFolder &&
			observed.FileIdentity == r.binding.FolderIdentity &&
			(observed.Availability == "available" || observed.Availability == "ambiguous") &&
			slices.Contains(observed.Alternates, r.binding.ProjectFile) {
			found = true
			break
		}
	}
	if !found {
		return "", projectconnection.ErrChanged
	}
	rows, partial, err := readPinnedDirectory(r.ctx, root, r.binding.RelativeFolder, r.binding.FolderIdentity, 5000)
	if err != nil || partial {
		return "", projectconnection.ErrUnavailable
	}
	for _, row := range rows {
		if row.Name == r.binding.ProjectFile && !row.IsDir && !row.IsLink && !row.Unreadable &&
			row.Identity == r.binding.ProjectFileIdentity {
			return filepath.Join(root.Path, r.binding.RelativeFolder), nil
		}
	}
	return "", projectconnection.ErrChanged
}

// Review previews the *same* creator later used to commit one song. Its only
// write is a short-lived Home review; no selection grant, child, link, role or
// source file is created. An ambiguous folder requires an explicit file name.
func (s *ActivationService) Review(ctx context.Context, scope Scope, entryID string, expected int64, selectedFile, workspaceName string) (ActivationReview, error) {
	if s == nil || s.inspector == nil || s.creator == nil || expected < 1 || entryID == "" || !validText(entryID, 160) ||
		!validText(selectedFile, 255) || !validText(workspaceName, 128) || workspaceName == "" {
		return ActivationReview{}, ErrUnavailable
	}
	a := s.inspector
	eligible, err := a.Eligibility(ctx, scope, entryID)
	if err != nil || (eligible.State != "review_available" && eligible.State != "file_choice_required") || eligible.RootID == "" {
		return ActivationReview{}, ErrUnavailable
	}
	if selectedFile == "" && len(eligible.ProjectFiles) == 1 {
		selectedFile = eligible.ProjectFiles[0]
	}
	if !slices.Contains(eligible.ProjectFiles, selectedFile) || filepath.Base(selectedFile) != selectedFile {
		return ActivationReview{}, ErrConflict
	}
	gate := rootAccessGate(scope)
	gate.RLock()
	defer gate.RUnlock()
	doc, err := a.library.Read(scope)
	if err != nil || doc.Revision != expected {
		return ActivationReview{}, ErrConflict
	}
	entry := sessionEntry(doc, entryID)
	if entry == nil || entry.Link != nil {
		return ActivationReview{}, ErrConflict
	}
	root, err := a.roots.verifyConnectedRootNoGate(scope, eligible.RootID)
	if err != nil {
		return ActivationReview{}, ErrUnavailable
	}
	var observed *Observation
	for i := range entry.Observations {
		if entry.Observations[i].RootID == root.ID && entry.Observations[i].RelativeFolder == eligible.RelativeFolder &&
			slices.Contains(entry.Observations[i].Alternates, selectedFile) &&
			(entry.Observations[i].Availability == "available" || entry.Observations[i].Availability == "ambiguous") {
			observed = &entry.Observations[i]
			break
		}
	}
	if observed == nil {
		return ActivationReview{}, ErrConflict
	}
	// The selected *file*, not only its containing folder and basename, must
	// remain the same object throughout review, creator preview and commit. A
	// same-named regular-file replacement cannot inherit an earlier review.
	rows, partial, err := readPinnedDirectory(ctx, root, observed.RelativeFolder, observed.FileIdentity, 5000)
	if err != nil || partial {
		return ActivationReview{}, ErrUnavailable
	}
	fileIdentity := ""
	for _, row := range rows {
		if row.Name == selectedFile && !row.IsDir && !row.IsLink && !row.Unreadable {
			fileIdentity = row.Identity
			break
		}
	}
	if fileIdentity == "" {
		return ActivationReview{}, ErrConflict
	}
	template, provider, err := a.providerFor(scope, eligible.BlueprintID)
	if err != nil {
		return ActivationReview{}, err
	}
	token, runID := newID(), newID()
	binding := ActivationBinding{EntryID: entryID, EntryRevision: entry.Revision,
		RootID: root.ID, RootRevision: root.Revision, RelativeFolder: observed.RelativeFolder,
		FolderIdentity: observed.FileIdentity, ProjectFile: selectedFile, ProjectFileIdentity: fileIdentity, WorkspaceName: workspaceName,
		BlueprintID: eligible.BlueprintID, ProviderFingerprint: provider.ComponentFingerprint,
		ProviderGeneration: provider.EvidenceGeneration(), ProviderInstalledAt: provider.InstalledAt}
	resolver := activationResolver{inspector: a, scope: scope, token: token, binding: binding, ctx: ctx}
	creator := s.creator(resolver)
	if creator == nil {
		return ActivationReview{}, ErrUnavailable
	}
	creatorScope := projectconnection.Scope{OwnerUserID: scope.OwnerUserID, RunID: runID, Template: template}
	prepared, err := creator.HomePreparation(creatorScope)
	if err != nil || !prepared.Exists || prepared.HomeID != scope.HomeID {
		return ActivationReview{}, ErrUnavailable
	}
	preview, err := creator.Preview(ctx, creatorScope, activationRequest(token, binding))
	if err != nil || preview.Projection.HomeWillBeCreated || preview.Projection.EntryName != selectedFile {
		return ActivationReview{}, ErrUnavailable
	}
	binding.CreatorInputDigest, binding.CreatorOwnerDigest = preview.InputDigest, preview.OwnerDigest
	if !binding.valid() {
		return ActivationReview{}, ErrConflict
	}
	digest, err := activationDigest(scope, runID, binding)
	if err != nil {
		return ActivationReview{}, err
	}
	at := a.library.now().UTC()
	review := ReviewReceipt{Token: token, Action: "activate_project", TargetID: runID, Digest: digest,
		Activation: &binding, Revision: doc.Revision + 1, EntryRevision: entry.Revision,
		ProviderRevision: root.Revision, ExpiresAt: at.Add(10 * time.Minute)}
	_, _, err = a.library.mutate(scope, doc.Revision, operation{key: token, action: "review_activation", digest: digest},
		func(current *Document) (string, error) {
			if len(current.Reviews) >= maxReviews {
				return "", ErrLimit
			}
			current.Reviews = append(current.Reviews, review)
			return token, nil
		})
	if err != nil {
		return ActivationReview{}, err
	}
	return ActivationReview{Token: token, EntryID: entryID, WorkspaceName: workspaceName,
		ProjectFile: selectedFile, BlueprintID: eligible.BlueprintID, ProjectRoleLabels: eligible.ProjectRoleLabels,
		Statement: "Creates one exact linked project in this Home through reviewed existing-file setup. Starts File-only: source files are not changed, no app is launched and no live access is granted. Project-role staffing and live access require separate reviews.",
		ExpiresAt: review.ExpiresAt}, nil
}

// Commit is deliberately strict. A canonical child may be created before the
// library's independent Home write succeeds; ObservedResult/Observe detect
// that exact run on retry so no second project is ever created for it.
func (s *ActivationService) Commit(ctx context.Context, scope Scope, entryID, token, key string) (ActivationResult, error) {
	if s == nil || s.inspector == nil || s.creator == nil || entryID == "" || token == "" || key == "" ||
		!validText(entryID, 160) || !validText(token, 160) || !validText(key, 160) {
		return ActivationResult{}, ErrConflict
	}
	a := s.inspector
	gate := rootAccessGate(scope)
	gate.RLock()
	defer gate.RUnlock()
	doc, err := a.library.Read(scope)
	if err != nil {
		return ActivationResult{}, err
	}
	review, ok := findReview(doc, token, "activate_project")
	if !ok || review.Activation == nil || review.Activation.EntryID != entryID || !review.Activation.valid() {
		return ActivationResult{}, ErrConflict
	}
	binding := *review.Activation
	digest, err := activationDigest(scope, review.TargetID, binding)
	if err != nil || digest != review.Digest {
		return ActivationResult{}, ErrConflict
	}
	for _, op := range doc.Operations {
		if op.Key != key {
			continue
		}
		if op.Action != "activate_project" || op.Digest != digest {
			return ActivationResult{}, ErrConflict
		}
		entry := sessionEntry(doc, entryID)
		if entry == nil || entry.Link == nil || entry.Link.WorkspaceID != op.ConsequenceID ||
			!a.reciprocalLink(scope, *entry.Link) {
			return ActivationResult{}, ErrUnavailable
		}
		return ActivationResult{EntryID: entryID, WorkspaceID: entry.Link.WorkspaceID, LinkID: entry.Link.LinkID, Replay: true}, nil
	}
	if review.ConsumedAt != nil || !review.ExpiresAt.After(a.library.now().UTC()) || review.Revision != doc.Revision {
		return ActivationResult{}, ErrConflict
	}
	template, provider, err := a.providerFor(scope, binding.BlueprintID)
	if err != nil || provider.EvidenceGeneration() != binding.ProviderGeneration ||
		provider.ComponentFingerprint != binding.ProviderFingerprint || !provider.InstalledAt.Equal(binding.ProviderInstalledAt) {
		return ActivationResult{}, ErrUnavailable
	}
	resolver := activationResolver{inspector: a, scope: scope, token: token, binding: binding, ctx: ctx}
	creator := s.creator(resolver)
	if creator == nil {
		return ActivationResult{}, ErrUnavailable
	}
	creatorScope := projectconnection.Scope{OwnerUserID: scope.OwnerUserID, RunID: review.TargetID, Template: template}
	prepared, err := creator.HomePreparation(creatorScope)
	if err != nil || !prepared.Exists || prepared.HomeID != scope.HomeID {
		return ActivationResult{}, ErrUnavailable
	}
	request := activationRequest(token, binding)
	// A prior creator success changes folder ownership, so its fresh Preview
	// would correctly refuse a *new* child. Observe this exact deterministic
	// run before deciding to re-preview; the source resolver, provider, Home,
	// reciprocal link and document review are still rechecked below.
	result, found := creator.ObservedResult(creatorScope, scope.HomeID, "")
	if !found {
		preview, previewErr := creator.Preview(ctx, creatorScope, request)
		if previewErr != nil || preview.InputDigest != binding.CreatorInputDigest || preview.OwnerDigest != binding.CreatorOwnerDigest ||
			preview.Projection.HomeWillBeCreated || preview.Projection.EntryName != binding.ProjectFile {
			return ActivationResult{}, ErrConflict
		}
		result, err = creator.Commit(ctx, creatorScope, request, binding.CreatorInputDigest, binding.CreatorOwnerDigest)
		if err != nil {
			return ActivationResult{}, err
		}
	}
	if result.HomeWorkspaceID != scope.HomeID || result.ProjectWorkspaceID == "" ||
		!creator.Observe(creatorScope, scope.HomeID, result.ProjectWorkspaceID, projecttemplates.ProjectConnectionExistingProject) {
		return ActivationResult{}, ErrUnavailable
	}
	child, err := a.owners.Get(result.ProjectWorkspaceID)
	if err != nil || child == nil || child.OwnerUserID != scope.OwnerUserID {
		return ActivationResult{}, ErrUnavailable
	}
	link := child.GetAssistantProjectLink()
	if link == nil || link.StationWorkspaceID != scope.HomeID || link.ID == "" ||
		!a.reciprocalLink(scope, ExactLink{WorkspaceID: child.ID, LinkID: link.ID, Revision: link.StateRevision}) {
		return ActivationResult{}, ErrUnavailable
	}
	if _, err := resolver.Resolve(token); err != nil {
		return ActivationResult{}, ErrUnavailable
	}
	var accepted ExactLink
	_, _, err = a.library.mutateWithHomePolicy(scope, doc.Revision, operation{key: key, action: "activate_project", digest: digest},
		func(state *workspace.AssistantProgramState, _ *workspace.Workspace) bool {
			owner := state.HomeProvider
			if owner == nil && state.GroupTemplate != nil {
				owner = state.GroupTemplate.ProgramHomeOwner
			}
			installed, listErr := a.installed.List()
			if listErr != nil || owner == nil || !plugin.IndependentHomeProviderEvidenceAvailable(installed, owner) {
				return false
			}
			selected, _, ambiguous := compatibleProjectBlueprint(installed, owner, scope)
			if ambiguous || selected == nil || selected.QualifiedID != binding.BlueprintID {
				return false
			}
			for _, candidate := range installed {
				if candidate.Name == selected.Template.PluginOwner.PluginID && candidate.Version == selected.Template.PluginOwner.PluginVersion &&
					candidate.Enabled && candidate.ComponentFingerprint == binding.ProviderFingerprint &&
					candidate.EvidenceGeneration() == binding.ProviderGeneration && candidate.InstalledAt.Equal(binding.ProviderInstalledAt) {
					return true
				}
			}
			return false
		}, func(current *Document) (string, error) {
			entry := sessionEntry(*current, entryID)
			if entry == nil || entry.Link != nil || entry.Revision != binding.EntryRevision {
				return "", ErrConflict
			}
			for i := range current.Reviews {
				r := &current.Reviews[i]
				if r.Token == token && r.Action == "activate_project" && r.ConsumedAt == nil &&
					r.Digest == digest && r.Revision == current.Revision && r.ExpiresAt.After(a.library.now().UTC()) {
					now := a.library.now().UTC()
					r.ConsumedAt = &now
					accepted = ExactLink{WorkspaceID: child.ID, LinkID: link.ID, Revision: link.StateRevision}
					entry.Link = &accepted
					entry.Revision++
					return child.ID, nil
				}
			}
			return "", ErrConflict
		})
	if err != nil {
		return ActivationResult{}, err // Child remains recoverable; never create another run silently.
	}
	return ActivationResult{EntryID: entryID, WorkspaceID: child.ID, LinkID: accepted.LinkID}, nil
}

func (a *ActivationInspector) reciprocalLink(scope Scope, exact ExactLink) bool {
	home, err := a.library.workspaces.Get(scope.HomeID)
	if err != nil || home == nil || home.OwnerUserID != scope.OwnerUserID {
		return false
	}
	state := home.GetAssistantProgramState()
	if state == nil || !slices.Contains(state.LinkedProjectIDs, exact.WorkspaceID) {
		return false
	}
	child, err := a.owners.Get(exact.WorkspaceID)
	if err != nil || child == nil || child.OwnerUserID != scope.OwnerUserID {
		return false
	}
	link := child.GetAssistantProjectLink()
	return link != nil && link.ID == exact.LinkID && link.StateRevision == exact.Revision &&
		link.StationWorkspaceID == scope.HomeID && link.Key.Normalize() == state.Key.Normalize()
}
