package assistantsetup

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
)

// WorkspaceSource must return canonical folder-backed active workspaces. The
// resolver does not fall back to names, recent activity, or browser state.
type WorkspaceSource interface {
	ListActive() ([]*workspace.Workspace, error)
}

type CandidateResolver interface {
	Resolve(ctx context.Context, ownerUserID string) ([]Target, error)
	ResolveOne(ctx context.Context, ownerUserID, workspaceID string) (*Target, error)
}

type CanonicalCandidateResolver struct {
	workspaces WorkspaceSource
}

func NewCanonicalCandidateResolver(workspaces WorkspaceSource) *CanonicalCandidateResolver {
	return &CanonicalCandidateResolver{workspaces: workspaces}
}

func (r *CanonicalCandidateResolver) Resolve(_ context.Context, ownerUserID string) ([]Target, error) {
	ownerUserID = strings.TrimSpace(ownerUserID)
	if r == nil || r.workspaces == nil || ownerUserID == "" {
		return nil, ErrUnavailable
	}
	items, err := r.workspaces.ListActive()
	if err != nil {
		return nil, ErrUnavailable
	}
	result := make([]Target, 0)
	for _, candidate := range items {
		if !eligibleWorkspaceOwner(candidate, ownerUserID) || !isOrdinaryActiveWorkspace(candidate) ||
			!candidate.HasInstalledCapability(workspace.CapabilityFileJanitor) {
			continue
		}
		supported, reason := supportedFileJanitorWorkspace(candidate)
		result = append(result, Target{
			WorkspaceID: candidate.ID,
			Name:        candidate.Name,
			Route:       fileJanitorRoute(candidate),
			Supported:   supported,
			Reason:      reason,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].WorkspaceID < result[j].WorkspaceID })
	return result, nil
}

func (r *CanonicalCandidateResolver) ResolveOne(ctx context.Context, ownerUserID, workspaceID string) (*Target, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, ErrNotFound
	}
	items, err := r.Resolve(ctx, ownerUserID)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].WorkspaceID == workspaceID {
			candidate := items[i]
			return &candidate, nil
		}
	}
	return nil, ErrNotFound
}

func eligibleWorkspaceOwner(candidate *workspace.Workspace, ownerUserID string) bool {
	if candidate == nil || strings.TrimSpace(candidate.ID) == "" {
		return false
	}
	owner := strings.TrimSpace(candidate.OwnerUserID)
	return owner == ownerUserID || (owner == "" && ownerUserID == userprofile.LocalUserID)
}

func isOrdinaryActiveWorkspace(candidate *workspace.Workspace) bool {
	if candidate == nil || strings.EqualFold(strings.TrimSpace(candidate.Kind), "group") ||
		strings.TrimSpace(candidate.Designation) != "" {
		return false
	}
	return candidate.Status == "" || candidate.Status == workspace.StatusActive
}

func supportedFileJanitorWorkspace(candidate *workspace.Workspace) (bool, string) {
	if candidate == nil {
		return false, "workspace_unavailable"
	}
	provenance := candidate.GetTemplateProvenance()
	if provenance == nil {
		return false, "manual_setup_required"
	}
	if provenance.TemplateID != BlueprintID && provenance.TemplateID != workspacecapability.LegacyDownloadsTemplateID {
		return false, "custom_workspace_requires_review"
	}
	if provenance.SetupWizard == nil || len(provenance.SetupWizard.Steps) == 0 {
		// The retired Downloads Janitor did not originally ship the modern
		// wizard. Its capability migration remains a supported legacy identity;
		// later reconciliation asks File Janitor for the actual partial state.
		if provenance.TemplateID == workspacecapability.LegacyDownloadsTemplateID {
			return true, ""
		}
		return false, "setup_wizard_unavailable"
	}
	return true, ""
}

func fileJanitorRoute(candidate *workspace.Workspace) string {
	if candidate == nil || strings.TrimSpace(candidate.FolderSlug) == "" {
		return ""
	}
	return fmt.Sprintf("/workspaces/%s?panel=file-janitor", candidate.FolderSlug)
}

var _ CandidateResolver = (*CanonicalCandidateResolver)(nil)
