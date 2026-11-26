package folderagent

import (
	"context"
	"os"
	"slices"
	"strings"

	"github.com/johnjallday/ori-agent/internal/diridentity"
	"github.com/johnjallday/ori-agent/internal/folderlook"
)

// DocumentsAccess is live host authority, never a persisted grant. Binding
// identifies the exact workspace instance, anchor reference and read grant.
type DocumentsAccess struct{ Root, Lead, Binding string }

// organizationAccess is independent of the HQ return contract. Old reports
// with a physical branch witness are eligible; path-only evidence is not.
func (s *Service) organizationAccess(owner string, rec *Record) (DocumentsAccess, error) {
	return s.checkOrganizationAccess(owner, rec, s.deps.DocumentsAccess)
}

// savedReviewAccess never substitutes metadata permission for a content read.
// The alternate proof can only recover a previously completed review under
// precisely the original authority. Filing still checks its own current scan,
// sources, destination, confirmation and journal at each operation.
func (s *Service) savedReviewAccess(owner string, rec *Record) (DocumentsAccess, error) {
	a, err := s.organizationAccess(owner, rec)
	if err == nil || !strings.HasPrefix(rec.AssignedLink, GuidedDocumentsLinkPrefix) || len(rec.OrganizationRuns) == 0 || s.deps.SavedFilingAccess == nil {
		return a, err
	}
	a, err = s.checkOrganizationAccess(owner, rec, func(owner, id string) (DocumentsAccess, error) {
		return s.deps.SavedFilingAccess(owner, id, rec.AgentName, rec.AssignedLink)
	})
	if err != nil {
		return DocumentsAccess{}, err
	}
	auth := reviewAuthority(rec, a)
	if !slices.ContainsFunc(rec.OrganizationRuns, func(run OrganizationRun) bool { return run.Authority == auth && run.Result.Status == "reviewed" }) {
		return DocumentsAccess{}, ErrWrongState
	}
	return a, nil
}

func (s *Service) checkOrganizationAccess(owner string, rec *Record, resolve func(string, string) (DocumentsAccess, error)) (DocumentsAccess, error) {
	if owner == "" || owner != rec.OwnerUserID {
		return DocumentsAccess{}, ErrNotOwner
	}
	if rec.Map == nil || (rec.State != StateReported && rec.State != StateKept && !completed(rec)) || resolve == nil {
		return DocumentsAccess{}, ErrWrongState
	}
	a, err := resolve(owner, rec.WorkspaceID)
	if err != nil {
		return DocumentsAccess{}, err
	}
	if a.Binding == "" || a.Lead == "" || a.Lead != rec.AgentName || ResolveRootIdentity(a.Root, s.deps.KnownFolderRoot).Policy != folderlook.RootDocuments {
		return DocumentsAccess{}, ErrWrongState
	}
	if rec.AssignedLink != "" && !s.assignedRootCurrent(context.Background(), rec, a.Root) {
		return DocumentsAccess{}, ErrWrongState
	}
	walked, ok := rec.Map.Folder("")
	info, err := os.Lstat(a.Root) // #nosec G304 G703 -- current owned workspace anchor
	if err != nil || !ok || walked.BranchWitness == "" || diridentity.Of(info) != walked.BranchWitness {
		return DocumentsAccess{}, ErrWrongState
	}
	return a, nil
}

// OrganizationPreview lists loose candidates only. It cannot call a parser or
// a provider. Loading the chat report doesn't even list the folder.
func (s *Service) OrganizationPreview(owner, id string) (OrganizationPreview, error) {
	return s.PreviewOrganization(context.Background(), owner, id, ReviewSelection{})
}
