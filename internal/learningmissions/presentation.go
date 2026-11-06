package learningmissions

import "context"

// VisibleOutcome rechecks source access even on idempotent replies. Stored
// receipts still recover identity, but cannot restore revoked text or links.
// Projection is read-only and never enrolls, reconciles or invokes a model.
func (s *Service) VisibleOutcome(ctx context.Context, owner string, out Outcome) Outcome {
	if out.Instance == nil {
		out.ResultText = ""
		if out.Operation.InstanceID != "" {
			out.Operation.TicketID, out.Operation.AttemptID, out.Operation.ReviewID = "", "", ""
			out.Review = nil
		}
		return out
	}
	visible := s.visibleInstance(ctx, owner, *out.Instance)
	out.Instance = &visible
	if visible.Progress.Blocker == "source_unavailable" || visible.Progress.Blocker == "definition_unavailable" {
		out.ResultText = ""
		out.Operation.TicketID, out.Operation.AttemptID, out.Operation.ReviewID = "", "", ""
		out.Review = nil
	}
	return out
}

func (s *Service) visibleInstance(ctx context.Context, owner string, i Instance) Instance {
	blocker := ""
	if _, err := Lookup(i.Definition.Structure, i.Definition.Version); err != nil {
		blocker = "definition_unavailable"
	} else if s.validateSource(ctx, owner, i.Definition) != nil {
		blocker = "source_unavailable"
	}
	if blocker != "" {
		i.TicketHref = ""
		i.Definition = Definition{Structure: i.Definition.Structure, Version: i.Definition.Version,
			Goal: "Learning evidence unavailable", Guidance: i.Definition.Guidance}
		i.Progress = Progress{Blocker: blocker, AcceptedAt: i.Progress.AcceptedAt}
		return i
	}
	if links, ok := s.source.(interface {
		TicketLink(context.Context, string, Instance) string
	}); ok {
		i.TicketHref = links.TicketLink(ctx, owner, i)
	}
	if i.State == Offered && i.Definition.ExpiresAt <= s.now().UnixMilli() {
		i.Progress.Blocker = "offer_expired"
	} else if i.State == Offered || (i.State == Active && i.Progress.TicketID == "") {
		if _, err := s.source.Candidate(ctx, owner, i.Definition.WorkspaceID); err != nil {
			i.Progress.Blocker = "execution_unavailable"
		}
	} else if i.State == Active && i.Progress.RunID != "" {
		if source, ok := s.source.(ResultSource); ok {
			result, err := source.Result(ctx, owner, i)
			if err != nil || VerifyResult(owner, i, result) != nil {
				i.Progress.Blocker = "result_unavailable"
			}
		}
	}
	return i
}
