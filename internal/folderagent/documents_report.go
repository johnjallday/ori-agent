package folderagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"regexp"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderlook"
)

var returnID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

func documentsRef(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func (s *Service) returnBinding(ctx context.Context, owner, hq, attempt string) string {
	if owner == "" || !returnID.MatchString(hq) || !returnID.MatchString(attempt) || s.deps.ActiveHQ == nil || s.deps.ActiveHQ(ctx, owner) != hq {
		return ""
	}
	return documentsRef("guided-folder-v1", owner, hq, attempt)
}

// Binding the walk to a path alone would allow a replacement directory at the
// old Documents location to inherit the old map. Require physical identity.
func (s *Service) documentsWitness(rec *Record, root string) string {
	if rec.GuidedReturn == "" || ResolveRootIdentity(root, s.deps.KnownFolderRoot).Policy != folderlook.RootDocuments {
		return ""
	}
	info, err := os.Lstat(root) // #nosec G304 G703 -- server-resolved anchor, never a request path
	if err != nil || !info.IsDir() {
		return ""
	}
	id := directoryWitness(info)
	if id == "" {
		return ""
	}
	return documentsRef("documents-walk-v1", rec.OwnerUserID, rec.WorkspaceID, rec.GuidedReturn, root, id)
}

// documentsReport is the read-only admission check shared by projection and
// resolution. Read the raw sidecar, not the policy-adjusted setup view.
func (s *Service) documentsReport(ctx context.Context, owner, hq, attempt, workspaceID string) (*Record, string, error) {
	binding := s.returnBinding(ctx, owner, hq, attempt)
	if binding == "" || !returnID.MatchString(workspaceID) {
		return nil, "", ErrWrongState
	}
	rec, err := s.deps.Store.Read(workspaceID)
	if err != nil {
		return nil, "", err
	}
	if owner != rec.OwnerUserID {
		return nil, "", ErrNotOwner
	}
	if binding != rec.GuidedReturn || rec.Map == nil || rec.MapWitness == "" || !(rec.State == StateReported || rec.State == StateKept || completed(rec)) {
		return nil, "", ErrWrongState
	}
	root, err := s.deps.Workspaces.AnchorRoot(workspaceID)
	if err != nil || s.documentsWitness(rec, root) != rec.MapWitness {
		return nil, "", ErrWrongState
	}
	return rec, root, nil
}
