package folderagent

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
	"github.com/johnjallday/ori-agent/internal/sensitive"
)

const maxDocumentsDecisions = 64

type DocumentsNotebook func(context.Context, string, string, func(secondbrain.Source, string) error) error

type DocumentsDecision struct {
	RequestID  string `json:"request_id,omitempty"`
	Payload    string `json:"payload,omitempty"`
	Operation  string `json:"operation,omitempty"`
	BodyHash   string `json:"body_hash,omitempty"`
	Proposal   string `json:"proposal"`
	Generation string `json:"generation,omitempty"`
	Directory  string `json:"directory,omitempty"`
	Name       string `json:"name,omitempty"`
	Rel        string `json:"rel,omitempty"`
	State      string `json:"state"`
}

type DocumentsDraftInput struct {
	Generation string `json:"generation,omitempty"`
	HQ         string `json:"hq"`
	Attempt    string `json:"attempt"`
	Report     string `json:"report"`
	Candidate  string `json:"candidate"`
	Name       string `json:"name"`
	Answer     string `json:"answer"`
	Review     string `json:"review,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
}

type DocumentsDraftView struct {
	Name       string `json:"name"`
	Body       string `json:"body,omitempty"`
	Rel        string `json:"rel"`
	Notebook   string `json:"notebook"`
	Review     string `json:"review,omitempty"`
	SetupPath  string `json:"setup_path,omitempty"`
	Generation string `json:"generation,omitempty"`
	Saved      bool   `json:"saved,omitempty"`
}

func documentsConflict(message string) error {
	return &Refusal{Code: "documents_changed", Status: http.StatusConflict, Message: message}
}

func reviewedBody(name, answer, location string) (string, string, error) {
	rel, ok := secondbrain.ProjectNoteRel(name)
	if !ok || !projectLabel.MatchString(name) || sensitive.ContainsSecretLikeText(name) {
		return "", "", documentsConflict("Choose a plain project name without paths or private information.")
	}
	if utf8.RuneCountInString(answer) > personalassistant.SecondBrainProjectAnswerMax || !utf8.ValidString(answer) {
		return "", "", documentsConflict("Keep the answer under 500 characters.")
	}
	line, ok := secondbrain.ProjectAnswerLine(answer)
	if !ok || strings.ContainsAny(line, "<>[]`\\") || strings.Contains(line, "://") || strings.Contains(line, "](") {
		return "", "", documentsConflict("Use a short answer in plain text, without links or credentials.")
	}
	return fmt.Sprintf("# %s\n\n%s\n\nSource: %s\n", name, line, location), rel, nil
}

// resolveDraft never trusts the display name as a source path. Resolve the
// candidate against the full saved report, not its current rank: publication
// itself can move a previously vacant name down the suggestion list.
func (s *Service) resolveDraft(ctx context.Context, owner, id string, in DocumentsDraftInput) (*Record, string, string, string, error) {
	rec, root, err := s.documentsReport(ctx, owner, in.HQ, in.Attempt, id)
	if err != nil {
		return nil, "", "", "", err
	}
	report := documentsRef(rec.MapWitness, holderReportKey(rec))
	if report != in.Report {
		return nil, "", "", "", ErrWrongState
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, "", "", "", ErrWrongState
	}
	if s.deps.DocumentsScope == nil {
		return nil, "", "", "", ErrUnavailable
	}
	scope, err := s.deps.DocumentsScope(ctx, owner)
	if err != nil {
		return nil, "", "", "", err
	}
	for _, f := range rec.Map.Folders {
		if !safeMember(f) || documentsRef(report, f.Path, f.Witness) != in.Candidate {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(f.Path))
		for _, guard := range append([]string{scope.Notebook}, scope.Protected...) {
			if guard == "" {
				continue
			}
			if real, err := filepath.EvalSymlinks(guard); err == nil {
				guard = real
			}
			if filejanitor.RootsOverlap(path, guard) {
				return nil, "", "", "", ErrWrongState
			}
		}
		if !explicitMember(root, rec.Map, f) {
			return nil, "", "", "", ErrWrongState
		}
		return rec, root, "Documents/" + f.Path, documentsRef(rec.MapWitness, f.Path, f.Witness), nil
	}
	return nil, "", "", "", ErrWrongState
}

func (s *Service) DocumentsDraft(ctx context.Context, owner, id string, in DocumentsDraftInput, save bool) (DocumentsDraftView, error) {
	var out DocumentsDraftView
	if len(in.Candidate) != 64 || len(in.Report) != 64 || (save && (!secondbrain.ValidRequestID(in.RequestID) || len(in.Review) != 64)) {
		return out, ErrWrongState
	}
	if s.deps.DocumentsNotebook == nil {
		return out, ErrUnavailable
	}
	err := s.deps.DocumentsNotebook(ctx, owner, in.HQ, func(source secondbrain.Source, generation string) error {
		rec, root, location, proposal, err := s.resolveDraft(ctx, owner, id, in)
		if err != nil {
			return err
		}
		if in.Generation != "" && generation != in.Generation {
			return documentsConflict("The connected notebook changed. Review the destination again.")
		}
		if !source.CreatedByOri() || !source.FillIn.Allowed() {
			return ErrWrongState
		}
		if !disjointNotebook(root, source.Path) {
			return documentsConflict("Choose Notes outside Documents before saving a project.")
		}
		name := strings.TrimSpace(in.Name)
		body, rel, err := reviewedBody(name, in.Answer, location)
		if err != nil {
			return err
		}
		writer, err := secondbrain.NewWriter(&source)
		if err != nil {
			return err
		}
		directory, vacant, err := writer.ReviewProject(name)
		if err != nil {
			return documentsConflict("The reviewed notebook is no longer available. Open Notes to check it.")
		}
		digest := documentsRef(body)
		payload := documentsRef(owner, id, in.HQ, in.Report, in.Candidate, proposal, generation, directory, rel, digest)
		out = DocumentsDraftView{Name: name, Body: body, Rel: rel, Notebook: source.Name, Review: payload}
		if save && in.Review != payload {
			return documentsConflict("The draft or notebook has changed. Review the destination again.")
		}
		var decision *DocumentsDecision
		for i := range rec.DocumentsDecisions {
			d := &rec.DocumentsDecisions[i]
			if save && d.RequestID == in.RequestID {
				if d.Payload != payload {
					return documentsConflict("That confirmation belongs to a different draft.")
				}
				decision = d
				break
			}
			if d.Proposal == proposal && (d.State == "saved" || d.State == "pending") {
				return documentsConflict("This project already has a save decision. Check its earlier result.")
			}
		}
		if decision == nil && !vacant {
			return documentsConflict("That project name is taken. Review another name or cancel.")
		}
		if !save {
			return nil
		}
		if decision == nil {
			d := DocumentsDecision{RequestID: in.RequestID, Operation: strings.TrimPrefix(newToken(), "fa-"), Payload: payload, BodyHash: digest, Generation: generation, Directory: directory, Proposal: proposal, Name: name, Rel: rel, State: "pending"}
			_, err = s.deps.Store.Update(id, func(current *Record) error {
				if current.Revision != rec.Revision || len(current.DocumentsDecisions) >= maxDocumentsDecisions {
					return ErrWrongState
				}
				current.DocumentsDecisions = append(current.DocumentsDecisions, d)
				return nil
			})
			if err != nil {
				return err
			}
			decision = &d
			if _, _, _, _, err = s.resolveDraft(ctx, owner, id, in); err != nil {
				return err
			}
			if err := writer.PublishDocumentsProject(name, directory, d.Operation, []byte(body)); err != nil {
				return documentsConflict("Could not publish the reviewed note. Nothing was overwritten; check the destination.")
			}
		}
		if _, err := writer.ProvenProject(name, directory, decision.Operation, digest); err != nil {
			return documentsConflict("The saved note is missing or changed. Ori will not replace it.")
		}
		if decision.State != "saved" {
			_, err = s.deps.Store.Update(id, func(current *Record) error {
				for i, d := range current.DocumentsDecisions {
					if d == *decision {
						current.DocumentsDecisions[i].State = "saved"
						return nil
					}
				}
				return ErrWrongState
			})
			if err != nil {
				return documentsConflict("Could not record the result. Retry the same confirmation to check the file.")
			}
		}
		out.Saved = true
		out.Body = ""
		return nil
	})
	if err != nil {
		return DocumentsDraftView{}, err
	}
	return out, nil
}

func disjointNotebook(root, notebook string) bool {
	src, e1 := filepath.EvalSymlinks(root)
	dst, e2 := filepath.EvalSymlinks(notebook)
	return e1 == nil && e2 == nil && !filejanitor.RootsOverlap(src, dst)
}

func (s *Service) OpenDocumentsDraft(ctx context.Context, owner, id, hq, attempt, request string) (DocumentsDraftView, error) {
	return s.readDocumentsResult(ctx, owner, id, hq, attempt, request, false)
}

// RecoverDocumentsResult only completes the receipt of a proven publication;
// it never publishes, opens the note for display, or needs the draft body.
func (s *Service) RecoverDocumentsResult(ctx context.Context, owner, id, hq, attempt, request string) (DocumentsDraftView, error) {
	return s.readDocumentsResult(ctx, owner, id, hq, attempt, request, true)
}
func (s *Service) readDocumentsResult(ctx context.Context, owner, id, hq, attempt, request string, recoverReceipt bool) (DocumentsDraftView, error) {
	var out DocumentsDraftView
	if s.deps.DocumentsNotebook == nil {
		return out, ErrUnavailable
	}
	err := s.deps.DocumentsNotebook(ctx, owner, hq, func(source secondbrain.Source, generation string) error {
		rec, root, err := s.documentsReport(ctx, owner, hq, attempt, id)
		if err != nil {
			return err
		}
		if !disjointNotebook(root, source.Path) {
			return ErrWrongState
		}
		for _, d := range rec.DocumentsDecisions {
			eligible := d.State == "saved" || (recoverReceipt && d.State == "pending")
			if d.RequestID != request || !eligible || d.Generation != generation {
				continue
			}
			w, err := secondbrain.NewWriter(&source)
			if err != nil {
				return err
			}
			body, err := w.ProvenProject(d.Name, d.Directory, d.Operation, d.BodyHash)
			if err != nil {
				return documentsConflict("The note is missing or has been edited. Nothing was changed.")
			}
			if recoverReceipt {
				body = ""
				if d.State == "pending" {
					_, err = s.deps.Store.Update(id, func(r *Record) error {
						for i, stored := range r.DocumentsDecisions {
							if stored == d {
								r.DocumentsDecisions[i].State = "saved"
								return nil
							}
						}
						return ErrWrongState
					})
					if err != nil {
						return err
					}
				}
			}
			out = DocumentsDraftView{Name: d.Name, Rel: d.Rel, Notebook: source.Name, Body: body, Saved: true}
			return nil
		}
		return documentsConflict("This note is not in the connected notebook.")
	})
	if err != nil {
		return DocumentsDraftView{}, err
	}
	return out, nil
}
