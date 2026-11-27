package folderagent

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func filingAdviceOnly(lines []personalassistant.FolderPlanLine) bool {
	return len(lines) == 2 && lines[0].Kind == FolderPlanFilingAdvice && lines[1].Kind == personalassistant.FolderPlanFolder
}

// An old advice run widened and released the first-look pin. Its completed
// recommendation can be shown without treating it as a live report. Never
// return the record, the pile, filenames, a plan or actionable permissions.
func (s *Service) releasedAdviceView(ctx context.Context, owner, id string) (View, bool) {
	if s.deps.ReleasedAdviceReceipt == nil {
		return View{}, false
	}
	r, err := s.deps.Store.Read(id)
	if err != nil || r.WorkspaceID != id || r.OwnerUserID != owner || !strings.HasPrefix(r.AssignedLink, GuidedDocumentsLinkPrefix) || !completed(r) || !filingAdviceOnly(r.Setup.Lines) {
		return View{}, false
	}
	for _, line := range r.Setup.Lines {
		if line.State != personalassistant.FolderLineDone {
			return View{}, false
		}
	}
	if !s.deps.ReleasedAdviceReceipt(ctx, owner, id, r.AgentName, r.AssignedLink) {
		return View{}, false
	}
	v := View{WorkspaceID: id, WorkspaceName: r.WorkspaceName, AgentName: r.AgentName, FolderName: r.FolderName, State: StateSetUp,
		Chat: s.chatRef(ctx, id), UpdatedAt: r.UpdatedAt, FilingRecommendationDone: true}
	// The historical report stays inert. Offer a separate review only if
	// the workspace itself still qualifies independently of that receipt.
	v.StationOffer = s.CanOfferGuidedStation(owner, id)
	if s.deps.Workspaces != nil {
		v.Route = s.deps.Workspaces.Route(id)
	}
	if !completeStationMap(r.Map) {
		v.FilingReviewHint = "partial"
	}
	return v, true
}
