package learningmissions

import "context"

type OriginalView struct {
	Result     ResultRef `json:"result"`
	ResultText string    `json:"result_text"`
	Input      string    `json:"input"`
	Decision   string    `json:"decision"`
	TicketHref string    `json:"ticket_href,omitempty"`
}

// OriginalView reads only the exact local receipt selected by this contract.
// It creates no nonce, approval, operation, practice credit or model request.
func (s *Service) OriginalView(ctx context.Context, owner, id string) (OriginalView, error) {
	if s == nil || s.store == nil {
		return OriginalView{}, ErrUnavailable
	}
	i, err := s.store.Instance(ctx, owner, id)
	if err != nil || i.Definition.Structure == DelegateInspect || s.validateSource(ctx, owner, i.Definition) != nil {
		return OriginalView{}, ErrUnavailable
	}
	prior, err := s.original(ctx, owner, i.Definition)
	if err != nil {
		return OriginalView{}, ErrUnavailable
	}
	out := OriginalView{Result: prior.Result.Ref, ResultText: prior.Result.Text, Input: prior.Result.InputText, Decision: prior.Review.Decision}
	if prior.Observation != nil {
		if links, ok := s.source.(ObservedSource); ok {
			out.TicketHref = links.ObservedTicketLink(ctx, owner, *prior.Observation.Observation)
		}
	} else if links, ok := s.source.(interface {
		TicketLink(context.Context, string, Instance) string
	}); ok {
		out.TicketHref = links.TicketLink(ctx, owner, prior.Instance)
	}
	return out, nil
}
