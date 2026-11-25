package workspace

// InboxReviewDestination is checked both when listing choices and inside the
// workspace write transaction. Unowned legacy workspaces belong to local.
func InboxReviewDestination(ws *Workspace, owner string) bool {
	return ws != nil && owner != "" && (ws.OwnerUserID == owner || ws.OwnerUserID == "" && owner == "local") && ws.Status == StatusActive && ws.Kind != "group" && IsCanonicalWorkspaceSlug(ws.FolderSlug)
}

func (s *TicketService) CreateInboxReviewTicket(owner, workspaceID, key, title string) (*Ticket, error) {
	if owner == "" {
		return nil, invalidTicketField("owner", "required")
	}
	ticket, _, err := s.CreateWithKey(TicketCreateInput{WorkspaceID: workspaceID, State: TicketStateBacklog, Title: title, Source: TicketSourceInboxReview, SourceID: key, Actor: TicketActorUser, ActorID: owner, inboxOwner: owner})
	return ticket, err
}
