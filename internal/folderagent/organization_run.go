package folderagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/fileparser"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const reviewRunning = "reading"
const maxReviews = 128

type ReviewedDocument struct {
	ConventionPin string               `json:"convention_pin,omitempty"`
	Property      string               `json:"property,omitempty"`
	Placement     *PlacementSuggestion `json:"placement,omitempty"`
	Source        ReviewSource         `json:"source"`
	Status        string               `json:"status"`
	Truncated     bool                 `json:"truncated,omitempty"`
}
type ReviewResult struct {
	FilingOnly bool                 `json:"filing_only,omitempty"` // display-only: the original content grant was narrowed
	Revision   int                  `json:"revision"`
	Decisions  []OrganizingDecision `json:"decisions,omitempty"`
	ID         string               `json:"id"`
	Status     string               `json:"status"`
	Processor  ReviewProcessor      `json:"processor"`
	Created    time.Time            `json:"created"`
	Files      []ReviewedDocument   `json:"files"`
	Read       int                  `json:"read"`
	Skipped    int                  `json:"skipped"`
	Remaining  int                  `json:"remaining"`
	Partial    bool                 `json:"partial"`
	Groups     []DocumentGroup      `json:"groups,omitempty"`
}

func hasRunningReview(r *Record) bool {
	return slices.ContainsFunc(r.OrganizationRuns, func(run OrganizationRun) bool { return run.Result.Status == reviewRunning })
}

type OrganizationRun struct {
	Authority string       `json:"authority"`
	Selection string       `json:"selection"`
	Result    ReviewResult `json:"result"`
}

type reviewExcerpt struct {
	Ref           string `json:"ref"`
	Text          string `json:"text"`
	ExampleFolder string `json:"example_folder,omitempty"`
}

func (s *Service) latestReview(owner, id string) (*Record, string, error) {
	return s.currentReview(owner, id, s.organizationAccess)
}

func (s *Service) latestFilingReview(owner, id string) (*Record, string, error) {
	return s.currentReview(owner, id, s.savedReviewAccess)
}

func reviewAuthority(rec *Record, a DocumentsAccess) string {
	root, _ := rec.Map.Folder("")
	return documentsRef(rec.OwnerUserID, rec.WorkspaceID, a.Root, root.BranchWitness, a.Lead, a.Binding)
}

func (s *Service) currentReview(owner, id string, access func(string, *Record) (DocumentsAccess, error)) (*Record, string, error) {
	rec, err := s.deps.Store.Read(id)
	if err != nil {
		return nil, "", err
	}
	a, err := access(owner, rec)
	if err != nil {
		return nil, "", err
	}
	return rec, reviewAuthority(rec, a), nil
}
func (s *Service) OrganizationResults(owner, id string) ([]ReviewResult, error) {
	rec, auth, err := s.latestReview(owner, id)
	filingOnly := err != nil
	if filingOnly {
		rec, auth, err = s.latestFilingReview(owner, id)
		if err != nil {
			return nil, err
		}
	}
	root, _ := rec.Map.Folder("")
	convention, _, err := s.convention(id, conventionMarker(owner, id, root.BranchWitness))
	if err != nil {
		return nil, err
	}
	out := []ReviewResult{}
	for _, run := range rec.OrganizationRuns {
		if run.Authority == auth {
			v := s.withFilingStanding(owner, id, run.Result)
			v.FilingOnly = filingOnly
			for i := range v.Files {
				v.Files[i].Placement = placementFor(rec, auth, v.Files[i], convention)
			}
			out = append(out, v)
		}
	}
	return out, nil
}

// The initial claim and its consumed digest survive reload. A duplicate may
// observe the receipt but never owns the work. An interrupted claim is not retried.
func (s *Service) ApproveOrganization(ctx context.Context, owner, id, digest string, sel ReviewSelection) (ReviewResult, error) {
	var result ReviewResult
	if len(digest) != 64 {
		return result, ErrWrongState
	}
	rec, auth, err := s.latestReview(owner, id)
	if err != nil {
		return result, err
	}
	for _, r := range rec.OrganizationRuns {
		if r.Result.ID == digest && r.Authority == auth {
			if r.Selection != scopeHash(sel) {
				return ReviewResult{}, ErrWrongState
			}
			return r.Result, nil
		}
	}
	scope, err := s.reviewScope(ctx, owner, rec, sel)
	if err != nil || scope.Approval != digest || scope.authority != auth {
		return result, ErrWrongState
	}
	result = ReviewResult{ID: digest, Processor: scope.Processor, Created: s.deps.Now().UTC(), Status: reviewRunning, Remaining: scope.Remaining, Partial: scope.Partial}
	for _, src := range scope.sources {
		result.Files = append(result.Files, ReviewedDocument{Source: src, Status: "not read"})
	}
	_, err = s.deps.Store.Update(id, func(r *Record) error {
		for _, run := range r.OrganizationRuns {
			if run.Result.ID == digest && run.Authority == auth {
				if run.Selection != scopeHash(sel) {
					return ErrWrongState
				}
				result = run.Result
				return errReviewReplay
			}
			if run.Result.Status == reviewRunning {
				return ErrWrongState
			}
		}
		if len(r.OrganizationRuns) >= maxReviews || rec.Revision != r.Revision {
			return ErrWrongState
		}
		r.OrganizationRuns = append(r.OrganizationRuns, OrganizationRun{Authority: auth, Selection: scopeHash(sel), Result: result})
		return nil
	})
	if errors.Is(err, errReviewReplay) {
		return result, nil
	}
	if err != nil {
		return ReviewResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	verify := func() bool {
		if ctx.Err() != nil {
			return false
		}
		if _, actual, err := s.latestReview(owner, id); err != nil || actual != auth {
			return false
		}
		p, err := s.reviewScope(ctx, owner, rec, sel)
		return err == nil && p.Approval == digest
	}
	excerpts := []reviewExcerpt{}
	result.Status = "reviewed"
	for i, source := range scope.sources {
		if !verify() {
			result.Status = "scope changed"
			break
		}
		item := &result.Files[i]
		if source.Version.Placeholder {
			item.Status = "cloud placeholder"
			continue
		}
		read := s.deps.ReviewRead
		if read == nil {
			read = workspace.ReadVersionedFile
		}
		f, err := read(scope.root, source.Path, fileparser.ReviewFileBytes, scope.rootID, source.Version)
		if err != nil {
			item.Status = "unavailable or changed"
			if errors.Is(err, workspace.ErrSourceTooLarge) {
				item.Status = "too large"
			}
			continue
		}
		e, err := fileparser.ExtractReviewExcerpt(ctx, source.Path, f.Data)
		if err != nil {
			item.Status = "unreadable or protected"
			continue
		}
		if e.Text == "" {
			item.Status = "no readable text"
			if e.Truncated {
				item.Status = "no text in examined pages"
			}
			continue
		}
		item.Status = "read"
		if !source.Example {
			item.Property = invoiceProperty(e.Text)
		}
		item.Truncated = e.Truncated
		result.Read++
		ex := reviewExcerpt{Ref: fmt.Sprintf("s%d", i+1), Text: e.Text}
		if source.Example {
			ex.ExampleFolder, _, _ = strings.Cut(source.Path, "/")
		}
		excerpts = append(excerpts, ex)
	}
	result.Skipped = len(result.Files) - result.Read
	// A group needs two readable loose documents. With one loose source and
	// no convention to apply, save its coverage without transmitting excerpts.
	loose := 0
	for _, ex := range excerpts {
		if ex.ExampleFolder == "" {
			loose++
		}
	}
	if (loose >= 2 || (loose == 1 && scope.Convention.Text != "")) && result.Status == "reviewed" {
		model, chat := s.deps.ReviewModel(id, rec.AgentName)
		raw, _ := json.Marshal(struct {
			Sources    []reviewExcerpt `json:"sources"`
			Convention string          `json:"organizing_convention,omitempty"`
		}{excerpts, scope.Convention.Text})
		if model != scope.Processor || chat == nil || !verify() {
			result.Status = "scope changed"
		} else if len(raw)+len(reviewPrompt) > 16<<10 {
			result.Status = "input limit"
		} else {
			reply, err := chat.Chat(ctx, llm.ChatRequest{Model: model.Model, ProviderConfiguration: model.Pin, MaxTokens: 1200, Isolated: true, SingleAttempt: true, SystemPrompt: reviewPrompt, Messages: []llm.Message{llm.NewUserMessage(string(raw))}})
			if err != nil || reply == nil || (reply.FinishReason != llm.FinishReasonStop && reply.FinishReason != "end_turn") || len(reply.ToolCalls) > 0 || len(reply.Content) > 16<<10 || reply.Usage.CompletionTokens > 1200 {
				result.Status = "model unavailable"
			} else {
				groups, ok := groundedGroups(excerpts, scope.sources, reply.Content)
				if !ok {
					result.Status = "could not verify suggestions"
				} else {
					result.Groups = groups
					bindConventionPlacements(reply.Content, result.Files, scope.Convention)
				}
			}
		}
	}
	if !verify() {
		result.Status = "scope changed"
		result.Groups = nil
	}
	_, err = s.deps.Store.Update(id, func(r *Record) error {
		index := slices.IndexFunc(r.OrganizationRuns, func(run OrganizationRun) bool {
			return run.Result.ID == digest && run.Authority == auth && run.Result.Status == reviewRunning
		})
		if index < 0 {
			return ErrWrongState
		}
		// Location evidence comes from the approved metadata snapshot, not
		// from the model's ability to return a well-formed group.
		if result.Status != "scope changed" && result.Read > 0 {
			scope.coverage.At = s.deps.Now().UTC()
			scope.coverage.Properties = map[string]string{}
			for _, item := range result.Files {
				if item.Status == "read" {
					scope.coverage.Reviewed[item.Source.Ref] = true
					if item.Property != "" {
						scope.coverage.Properties[item.Source.Ref] = item.Property
					}
				}
			}
			before := r.ReviewBaseline
			var known map[string]bool
			if before != nil && s.deps.ReviewActionIdentities != nil {
				if identities, err := s.deps.ReviewActionIdentities(owner, id, scope.root, before.At); err == nil {
					known = identities
				}
			}
			result.Decisions = observationChoices(before, &scope.coverage, digest, known)
			if before != nil && before.Authority == auth {
				for _, f := range scope.coverage.Files {
					if before.Reviewed[f.Ref] {
						scope.coverage.Reviewed[f.Ref] = true
						if before.Properties[f.Ref] != "" {
							scope.coverage.Properties[f.Ref] = before.Properties[f.Ref]
						}
					}
				}
			}
			r.ReviewBaseline = &scope.coverage
		}
		r.OrganizationRuns[index].Result = result
		return nil
	})
	if err != nil {
		return ReviewResult{}, err
	}
	if _, again, err := s.latestReview(owner, id); err != nil || again != auth {
		return ReviewResult{}, ErrWrongState
	}
	return result, nil
}

var errReviewReplay = errors.New("review already admitted")
