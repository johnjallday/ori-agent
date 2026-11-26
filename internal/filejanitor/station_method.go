package filejanitor

import (
	"context"
	"path/filepath"

	"github.com/google/uuid"
)

const (
	MethodFolders = "own_folders"
	MethodKind    = "by_kind"
)

func validFilingMethod(m string) bool { return m == MethodFolders || m == MethodKind }

// StationMethod is a reviewed preference for local destinations. Sending is
// an independent connection, never stored here as a third local method.
type StationMethod struct {
	Folder    string `json:"folder"`
	Current   string `json:"current,omitempty"`
	Suggested string `json:"suggested"`
	Shelves   bool   `json:"shelves"`
	Send      bool   `json:"send"`
	Revision  string `json:"revision"`
	// Compared to the settings held by UpdateSettings on a press. Do not
	// enumerate other workspaces or folder names while holding its lock.
	settingsRevision string
}

func (s *Service) StationMethod(ctx context.Context, owner, id string) (StationMethod, error) {
	var no StationMethod
	if ctx.Err() != nil {
		return no, ctx.Err()
	}
	root, err := s.StoredManagedRoot(owner, id)
	if err != nil {
		return no, err
	}
	settings, err := s.requireConfigured(id)
	if err != nil || settings.RootID != root.Generation {
		return no, ErrReviewUnavailable
	}
	install, ok, err := s.reviewInstall(owner, id)
	if err != nil || !ok {
		return no, ErrReviewUnavailable
	}
	out := StationMethod{Folder: DisplayFileName(filepath.Base(root.Path)), Current: settings.FilingMethod, Suggested: MethodKind}
	if out.Current == "" && !settings.DeferredDestination {
		out.Current = MethodKind
	}
	if settings.ApprovedReference != nil && settings.ApprovedReference.Witness != "" {
		scope, err := s.shelfScope(owner, id)
		if err != nil {
			return no, err
		}
		dirs, _, err := scope.folders(DirRef{ID: scope.witness})
		if err != nil {
			return no, err
		}
		out.Shelves = s.ConditionalMoveAvailable(root.Path, scope.witness) == nil
		if out.Shelves && len(dirs) > 0 {
			out.Suggested = MethodFolders
		}
	}
	connections, err := s.ListConnections(owner, id)
	if err != nil {
		return no, err
	}
	for _, r := range connections.Receivers {
		if r.Refusal == "" {
			out.Send = true
			break
		}
	}
	out.settingsRevision = settings.MethodRevision
	out.Revision, err = setupDigest([]any{"filing-method-v1", id, owner, reviewScopeDigest(root, install), settings.ApprovedReference, settings.MethodRevision, out})
	return out, err
}

func (s *Service) ChooseStationMethod(ctx context.Context, owner, id, revision, method string) (StationMethod, error) {
	var no StationMethod
	if !validFilingMethod(method) || len(revision) != 64 {
		return no, ErrInvalidAction
	}
	root, err := s.StoredManagedRoot(owner, id)
	if err != nil {
		return no, err
	}
	unlock := lockRoot(root.Path)
	defer unlock()
	before, err := s.StationMethod(ctx, owner, id)
	if err != nil {
		return no, err
	}
	if before.Revision != revision || method == MethodFolders && !before.Shelves {
		return no, ErrReviewPlanChanged
	}
	if before.Current == method {
		return before, nil
	}
	_, err = s.store.UpdateSettings(id, func(v *JanitorSettings) error {
		current, err := s.StoredManagedRoot(owner, id)
		if err != nil || current != root || !v.IsSetUp() || v.MethodRevision != before.settingsRevision || ctx.Err() != nil {
			return ErrReviewPlanChanged
		}
		v.FilingMethod = method
		v.MethodRevision = uuid.NewString()
		return nil
	})
	if err != nil {
		return no, err
	}
	return s.StationMethod(ctx, owner, id)
}
