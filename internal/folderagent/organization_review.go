package folderagent

import (
	"context"
	"os"

	"github.com/johnjallday/ori-agent/internal/diridentity"
	"github.com/johnjallday/ori-agent/internal/folderlook"
)

// DocumentsAccess is live host authority, never a persisted grant. Binding
// identifies the exact workspace instance, anchor reference and read grant.
type DocumentsAccess struct{ Root, Lead, Binding string }

// organizationAccess is independent of the HQ return contract. Old reports
// with a physical branch witness are eligible; path-only evidence is not.
func (s *Service) organizationAccess(owner string, rec *Record) (DocumentsAccess, error) {
	if owner == "" || owner != rec.OwnerUserID {
		return DocumentsAccess{}, ErrNotOwner
	}
	if rec.Map == nil || (rec.State != StateReported && rec.State != StateKept && !completed(rec)) || s.deps.DocumentsAccess == nil {
		return DocumentsAccess{}, ErrWrongState
	}
	a, err := s.deps.DocumentsAccess(owner, rec.WorkspaceID)
	if err != nil {
		return DocumentsAccess{}, err
	}
	if a.Binding == "" || a.Lead == "" || a.Lead != rec.AgentName || ResolveRootIdentity(a.Root, s.deps.KnownFolderRoot).Policy != folderlook.RootDocuments {
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
