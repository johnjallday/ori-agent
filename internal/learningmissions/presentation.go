package learningmissions

import (
	"context"
	"errors"
)

// InstanceAvailability never constitutes result verification or execution consent.
// Production status adapters must use metadata only, not canonical content reads.
type InstanceAvailability struct {
	ResultReady bool
	TicketHref  string
}

type InstanceStatusSource interface {
	InstanceStatus(context.Context, string, Instance) (InstanceAvailability, error)
}

// VisibleOutcome rechecks source access even on idempotent replies. Stored
// receipts still recover identity, but cannot restore revoked text or links.
// Projection is read-only and never enrolls, reconciles or invokes a model.
func (s *Service) VisibleOutcome(ctx context.Context, owner string, out Outcome) Outcome {
	out.Operation.Milestone, out.Operation.MilestoneID, out.Operation.CandidateFingerprint = nil, "", ""
	if out.Operation.ObservationID != "" {
		return s.visibleObservedOutcome(ctx, owner, out)
	}
	out.ObservedInput = ""
	if out.Operation.Kind == "observe" {
		out.Operation.Observation = nil // host Start tokens are not owner-facing projections
		out.Operation.TicketID, out.Operation.AttemptID, out.Operation.ReviewID = "", "", ""
		out.Review = nil
	}
	if out.Instance == nil {
		out.ResultText, out.OriginalText, out.OriginalInput = "", "", ""
		if out.Operation.InstanceID != "" {
			out.Operation.TicketID, out.Operation.AttemptID, out.Operation.ReviewID = "", "", ""
			out.Review = nil
		}
		return out
	}
	visible := s.visibleInstance(ctx, owner, *out.Instance)
	out.Instance = &visible
	if visible.Progress.Blocker == "source_unavailable" || visible.Progress.Blocker == "definition_unavailable" {
		out.ResultText, out.OriginalText, out.OriginalInput = "", "", ""
		out.Operation.TicketID, out.Operation.AttemptID, out.Operation.ReviewID = "", "", ""
		out.Review = nil
	}
	return out
}

func (s *Service) visibleInstance(ctx context.Context, owner string, i Instance) Instance {
	blocker := ""
	metadata, hasMetadata := s.source.(InstanceStatusSource)
	var availability InstanceAvailability
	if errors.Is(i.Definition.Validate(), ErrUnsupported) {
		blocker = "definition_unavailable"
	} else if hasMetadata {
		incarnation, err := s.store.SourceIncarnation(ctx, owner, i.Definition.WorkspaceID)
		if err == nil && incarnation == i.Definition.Incarnation {
			availability, err = metadata.InstanceStatus(ctx, owner, i)
		} else {
			err = ErrUnavailable
		}
		if err != nil {
			blocker = "source_unavailable"
		}
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
	if hasMetadata {
		i.TicketHref = availability.TicketHref
	} else if links, ok := s.source.(interface {
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
		if !hasMetadata || !availability.ResultReady {
			i.Progress.Blocker = "result_unavailable"
		}
	}
	if i.State == Offered && i.Definition.ReplacesID != "" && i.Progress.Blocker == "" {
		active, err := s.store.Instance(ctx, owner, i.Definition.ReplacesID)
		if err != nil || active.Revision != i.Definition.ReplacesRevision || (active.State != Active && active.State != Blocked) {
			i.Progress.Blocker = "replacement_changed"
		}
	}
	if i.State == Offered && i.Definition.Personalized && i.Progress.Blocker == "" {
		if s.designer == nil {
			i.Progress.Blocker = "model_changed"
		} else if model, err := s.designer.Configuration(ctx); err != nil || model.Fingerprint != i.Definition.ModelFingerprint {
			i.Progress.Blocker = "model_changed"
		}
	}
	return i
}
