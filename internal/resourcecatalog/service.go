package resourcecatalog

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

var (
	// ErrNotFound is an unknown, foreign or non-resource workspace. It never
	// says which.
	ErrNotFound = errors.New("resource workspace not found")
	// ErrDetached is a Studio without a valid link to its Home: its saved
	// catalog stays readable but nothing can be inspected or shared.
	ErrDetached = errors.New("this resource workspace is not attached to its Home")
	// ErrProviderUnavailable means the Studio's project plugin cannot serve its
	// catalog now (missing, disabled, without the declaration, or newer).
	ErrProviderUnavailable = errors.New("the resource catalog's plugin is unavailable")
	// ErrScopeReviewRequired means the sources to read are not all approved on
	// this computer, or the installation or a source changed since approval.
	ErrScopeReviewRequired = errors.New("review the sources before reading them")
	// ErrReviewStale is an expired, used or no longer matching review.
	ErrReviewStale = errors.New("the review changed or expired; review again")
	// ErrInspectionActive means another inspection of this Studio is running.
	ErrInspectionActive = errors.New("an inspection of this catalog is already running")
	// ErrInvalidRequest is a request the service refuses as malformed.
	ErrInvalidRequest = errors.New("the catalog request is invalid")
	// ErrConflict is a write made against an older revision.
	ErrConflict = errors.New("the catalog changed since it was read")
)

// ProviderStatus is what the Studio's project plugin offers right now.
type ProviderStatus struct {
	Available   bool
	Reason      string
	Catalog     CatalogRef
	Declaration *Declaration
}

// Provider reasons. The words a person reads are built from them.
const (
	ReasonProviderMissing  = "provider_missing"
	ReasonProviderDisabled = "provider_disabled"
	ReasonCatalogMissing   = "catalog_missing"
	ReasonCatalogNewer     = "catalog_incompatible"
	ReasonServiceMissing   = "service_unavailable"
)

// Provider bridges to the Studio's project plugin. It is given only the
// Studio workspace (to find its exact provider) and an operation input; it
// calls the plugin's declared machine-level operations with the host's
// timeout, byte and schema checks, and never sees a grant, path or catalog.
type Provider interface {
	Status(studio *workspace.Workspace) ProviderStatus
	Sources(ctx context.Context, studio *workspace.Workspace) ([]byte, error)
	Inventory(ctx context.Context, studio *workspace.Workspace, input []byte) ([]byte, error)
}

// Deps are the service's collaborators.
type Deps struct {
	Workspaces workspace.Store
	Folders    workspace.FolderResolver
	// DataDir is Ori's data directory. Grants and operations live under it,
	// never in a workspace folder.
	DataDir  string
	Provider Provider
	// HomeProviderAvailable reports whether a Home's own package is installed,
	// enabled and still the one it is pinned to. Lookups need it; nil means
	// lookups are unavailable.
	HomeProviderAvailable func(home *workspace.Workspace) bool
	Now                   func() time.Time
	NewID                 func() string
}

// Service owns every Studio catalog: reviews, inspection, publication,
// withdrawal and the projections read from them.
type Service struct {
	deps    Deps
	mu      sync.Mutex
	reviews map[string]pendingReview
	running map[string]*runningOperation
}

// NewService returns a Service. A nil Now or NewID uses the clock and UUIDs.
func NewService(deps Deps) *Service {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.NewID == nil {
		deps.NewID = uuid.NewString
	}
	return &Service{deps: deps, reviews: map[string]pendingReview{}, running: map[string]*runningOperation{}}
}

func (s *Service) now() time.Time { return s.deps.Now().UTC() }

// studioRef is one resolved Studio.
type studioRef struct {
	workspace *workspace.Workspace
	link      *workspace.AssistantProjectLink
	paths     paths
	// attached is true while the Studio's resource link is valid and its Home
	// still lists it. Inspection, sharing and lookups need it.
	attached bool
}

// resolveStudio finds the owner's resource workspace. A workspace that is no
// longer linked but still holds a catalog is returned as detached so its
// history stays readable to its owner.
func (s *Service) resolveStudio(owner, studioID string) (studioRef, error) {
	if s == nil || s.deps.Workspaces == nil || s.deps.Folders == nil || strings.TrimSpace(s.deps.DataDir) == "" {
		return studioRef{}, ErrNotFound
	}
	studioID = strings.TrimSpace(studioID)
	if !workspaceIDPattern.MatchString(studioID) {
		return studioRef{}, ErrNotFound
	}
	ws, err := s.deps.Workspaces.Get(studioID)
	if err != nil || ws == nil || ws.Status == workspace.StatusTrashed || ws.Status == workspace.StatusMissing ||
		normalizedOwner(ws.OwnerUserID) != normalizedOwner(owner) {
		return studioRef{}, ErrNotFound
	}
	folder, err := s.deps.Folders.GetFolderPath(ws.ID)
	if err != nil {
		return studioRef{}, ErrNotFound
	}
	p, err := studioPaths(folder, s.deps.DataDir, ws.ID)
	if err != nil {
		return studioRef{}, ErrNotFound
	}
	ref := studioRef{workspace: ws, paths: p}
	link := ws.GetAssistantProjectLink()
	if workspace.AssistantProjectLinkIsResource(link) {
		ref.link = link
		ref.attached = s.linkActive(ws, link)
		return ref, nil
	}
	// A detached former Studio is readable only while it still has a catalog.
	if observations, readErr := readObservations(p); readErr == nil && observations.Revision > 0 {
		return ref, nil
	}
	return studioRef{}, ErrNotFound
}

// linkActive checks the Studio's link from the Home's side: the exact Home
// still lists it under the same key, and the Studio sits under that Home.
func (s *Service) linkActive(ws *workspace.Workspace, link *workspace.AssistantProjectLink) bool {
	if link == nil || link.ID != workspace.AssistantProjectLinkID(link.StationWorkspaceID, ws.ID) || link.StateRevision < 1 {
		return false
	}
	linked, err := workspace.NewAssistantProgramStore(s.deps.Workspaces).LinkedProjects(link.StationWorkspaceID)
	if err != nil {
		return false
	}
	for _, project := range linked {
		if project.ID == ws.ID {
			return true
		}
	}
	return false
}

// provider returns the Studio's provider status, refusing a catalog whose id
// differs from the one its files were written under.
func (s *Service) provider(ref studioRef, observations Observations) ProviderStatus {
	if s.deps.Provider == nil || ref.workspace == nil {
		return ProviderStatus{Reason: ReasonProviderMissing}
	}
	status := s.deps.Provider.Status(ref.workspace)
	if !status.Available {
		return status
	}
	if status.Declaration == nil || status.Catalog.Version != SupportedCatalogVersion {
		return ProviderStatus{Reason: ReasonCatalogNewer, Catalog: status.Catalog}
	}
	if observations.Catalog.ID != "" && (observations.Catalog.ID != status.Catalog.ID || !strings.EqualFold(observations.Catalog.PluginID, status.Catalog.PluginID)) {
		return ProviderStatus{Reason: ReasonCatalogNewer, Catalog: status.Catalog}
	}
	return status
}

func normalizedOwner(owner string) string {
	return (workspace.AssistantProgramKey{OwnerUserID: owner}).Normalize().OwnerUserID
}
