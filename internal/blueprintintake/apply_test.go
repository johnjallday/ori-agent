package blueprintintake

import (
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

type proposalFolder struct{ root string }

func (f proposalFolder) GetFolderPath(string) (string, error) { return f.root, nil }

type recordingTicketCreator struct {
	calls  []workspace.TicketCreateInput
	failID string
}

func (c *recordingTicketCreator) CreateIdempotent(input workspace.TicketCreateInput) (*workspace.Ticket, bool, error) {
	c.calls = append(c.calls, input)
	if input.SourceID == c.failID {
		return nil, false, errors.New("ticket refused")
	}
	return &workspace.Ticket{ID: "ticket-" + input.SourceID}, true, nil
}

func pendingProposal(t *testing.T, store *ProposalStore) Proposal {
	t.Helper()
	proposal, err := finalizeProposal("ws", "materials", []ProposalItem{
		{Kind: "ticket", Key: "one", Title: "One", Source: ProposalSource{SourceID: "s", Quote: "q"}},
		{Kind: "ticket", Key: "two", Title: "Two", Source: ProposalSource{SourceID: "s", Quote: "q"}},
	}, ProposalNotice{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(proposal); err != nil {
		t.Fatal(err)
	}
	return proposal
}

func TestApplyRequiresExactReviewedHashBeforeCreatingAnything(t *testing.T) {
	store := NewProposalStore(proposalFolder{root: t.TempDir()})
	proposal := pendingProposal(t, store)
	for name, hash := range map[string]string{"missing": "", "stale": "not-the-hash"} {
		t.Run(name, func(t *testing.T) {
			creator := &recordingTicketCreator{}
			service := NewApplyService(store, creator)
			_, err := service.Apply("ws", "materials", ApplyRequest{ProposalHash: hash, Items: []ApplyChoice{{Key: "one", Selected: true}}})
			if !errors.Is(err, ErrProposalStale) {
				t.Fatalf("error = %v", err)
			}
			if len(creator.calls) != 0 {
				t.Fatalf("direct ticket path called %d times before hash approval", len(creator.calls))
			}
		})
	}
	if proposal.Hash == "" {
		t.Fatal("fixture has no reviewed hash")
	}
}

func TestApplyReportsEachItemAndWritesTicketProvenance(t *testing.T) {
	store := NewProposalStore(proposalFolder{root: t.TempDir()})
	proposal := pendingProposal(t, store)
	creator := &recordingTicketCreator{failID: "materials:two"}
	service := NewApplyService(store, creator)
	result, err := service.Apply("ws", "materials", ApplyRequest{ProposalHash: proposal.Hash, Items: []ApplyChoice{{Key: "one", Selected: true}, {Key: "two", Selected: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 2 || result.Results[0].Status != "created" || result.Results[1].Status != "failed" {
		t.Fatalf("results = %+v", result.Results)
	}
	if len(creator.calls) != 2 || creator.calls[0].Source != workspace.TicketSourceBlueprintIntake || creator.calls[0].SourceID != "materials:one" {
		t.Fatalf("ticket provenance = %+v", creator.calls)
	}
	stored, err := store.Get("ws", "materials")
	if err != nil || stored.Status != "applied" {
		t.Fatalf("stored proposal = %+v, %v", stored, err)
	}
}
