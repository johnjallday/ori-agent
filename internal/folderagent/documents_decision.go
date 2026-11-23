package folderagent

import (
	"context"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

type DocumentsDisposition struct {
	State   string `json:"state"`
	Name    string `json:"name,omitempty"`
	Request string `json:"request_id,omitempty"`
}

func (s *Service) DismissDocuments(ctx context.Context, owner, id string, in DocumentsDraftInput) error {
	rec, _, _, proposal, err := s.resolveDraft(ctx, owner, id, in)
	if err != nil {
		return err
	}
	for _, d := range rec.DocumentsDecisions {
		if d.Proposal == proposal {
			if d.State == "dismissed" {
				return nil
			}
			return ErrWrongState
		}
	}
	_, err = s.deps.Store.Update(id, func(current *Record) error {
		if current.Revision != rec.Revision || len(current.DocumentsDecisions) >= maxDocumentsDecisions {
			return ErrWrongState
		}
		current.DocumentsDecisions = append(current.DocumentsDecisions, DocumentsDecision{Proposal: proposal, State: "dismissed"})
		return nil
	})
	return err
}

// A return reads a durable decision, never claims success from a filename.
// Nothing here repairs a receipt or returns note text. The explicit Open
// reads and proves the exact file again, under the same connection lock.
func (s *Service) documentsDisposition(ctx context.Context, owner, hq string, rec *Record) *DocumentsDisposition {
	members := map[string]bool{}
	for _, f := range rec.Map.Folders {
		if safeMember(f) {
			members[documentsRef(rec.MapWitness, f.Path, f.Witness)] = true
		}
	}
	for i := len(rec.DocumentsDecisions) - 1; i >= 0; i-- {
		d := rec.DocumentsDecisions[i]
		if !members[d.Proposal] {
			continue
		}
		if d.State == "dismissed" {
			return &DocumentsDisposition{State: "dismissed"}
		}
		if d.State != "pending" && d.State != "saved" {
			continue
		}
		result := &DocumentsDisposition{State: "unverified", Name: d.Name}
		if s.deps.DocumentsNotebook != nil {
			err := s.deps.DocumentsNotebook(ctx, owner, hq, func(source secondbrain.Source, generation string) error {
				if generation != d.Generation {
					return ErrWrongState
				}
				w, err := secondbrain.NewWriter(&source)
				if err != nil {
					return err
				}
				_, err = w.ProvenProject(d.Name, d.Directory, d.Operation, d.BodyHash)
				return err
			})
			if err == nil {
				result.State = d.State
				result.Request = d.RequestID
			}
		}
		return result
	}
	return nil
}
