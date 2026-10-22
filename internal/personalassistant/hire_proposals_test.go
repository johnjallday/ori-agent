package personalassistant

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/types"
)

func newHireProposalsForTest(t *testing.T) *SQLiteHireProposals {
	t.Helper()
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(t.TempDir(), "proposals.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewSQLiteHireProposals(db)
}

func sampleProposal(id, conversationID string) HireProposal {
	return HireProposal{
		ID: id, UserID: "local", ConversationID: conversationID, MessageID: "msg-" + id,
		Role: string(types.RoleResearcher), Name: "Ruth", Brief: "Compare the three CRMs under evaluation and recommend one by Friday.",
		WorkspaceName: "Compare the Three CRMs", Blueprint: "research-project", BlueprintLabel: "Research Project", OpenRoles: 2,
		State: HireProposalOffered,
	}
}

func TestHireProposals_CreateGetAndListByConversation(t *testing.T) {
	proposals := newHireProposalsForTest(t)
	created, err := proposals.Create(t.Context(), sampleProposal("p-1", "conv-1"))
	if err != nil {
		t.Fatal(err)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("timestamps %+v", created)
	}
	if _, err := proposals.Create(t.Context(), sampleProposal("p-2", "conv-1")); err != nil {
		t.Fatal(err)
	}
	other := sampleProposal("p-3", "conv-2")
	if _, err := proposals.Create(t.Context(), other); err != nil {
		t.Fatal(err)
	}

	got, err := proposals.Get(t.Context(), "p-1")
	if err != nil || got.Role != "researcher" || got.State != HireProposalOffered || got.OpenRoles != 2 || got.MessageID != "msg-p-1" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	mine, err := proposals.ByConversation(t.Context(), "local", "conv-1")
	if err != nil || len(mine) != 2 || mine[0].ID != "p-1" || mine[1].ID != "p-2" {
		t.Fatalf("ByConversation = %+v, %v", mine, err)
	}
	if foreign, _ := proposals.ByConversation(t.Context(), "someone-else", "conv-1"); len(foreign) != 0 {
		t.Fatalf("another user sees %d proposals", len(foreign))
	}
	if _, err := proposals.Get(t.Context(), "missing"); !errors.Is(err, ErrHireProposalNotFound) {
		t.Fatalf("missing proposal: %v", err)
	}
}

func TestHireProposals_UpdateChangesStateAndOutcomeNotIdentity(t *testing.T) {
	proposals := newHireProposalsForTest(t)
	if _, err := proposals.Create(t.Context(), sampleProposal("p-1", "conv-1")); err != nil {
		t.Fatal(err)
	}
	updated, err := proposals.Update(t.Context(), "p-1", func(p *HireProposal) error {
		p.ID, p.UserID, p.ConversationID = "other", "intruder", "conv-9"
		p.State, p.HireID, p.AgentName, p.WorkspaceID = HireProposalHired, "hire-1", "Ruth", "ws-1"
		p.Role, p.Name, p.Brief = string(types.RoleSynthesizer), "Wren", "Write the comparison up."
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != "p-1" || updated.UserID != "local" || updated.ConversationID != "conv-1" {
		t.Fatalf("identity changed: %+v", updated)
	}
	if updated.State != HireProposalHired || updated.HireID != "hire-1" || updated.Role != "synthesizer" || updated.Name != "Wren" || updated.Brief != "Write the comparison up." {
		t.Fatalf("outcome not recorded: %+v", updated)
	}
	if _, err := proposals.Update(t.Context(), "p-1", func(p *HireProposal) error { p.State = "looking"; return nil }); !errors.Is(err, ErrValidation) {
		t.Fatalf("an unknown state was stored: %v", err)
	}
	if _, err := proposals.Update(t.Context(), "missing", func(*HireProposal) error { return nil }); !errors.Is(err, ErrHireProposalNotFound) {
		t.Fatalf("missing proposal: %v", err)
	}
}

func TestHireProposals_RefusesUnboundedPathLikeOrSecretText(t *testing.T) {
	proposals := newHireProposalsForTest(t)
	for name, mutate := range map[string]func(*HireProposal){
		"long brief":     func(p *HireProposal) { p.Brief = strings.Repeat("x", WorkspaceGoalMaxRunes+1) },
		"empty brief":    func(p *HireProposal) { p.Brief = " " },
		"secret brief":   func(p *HireProposal) { p.Brief = "api_key=sk-1234567890abcdef1234567890abcdef" },
		"path name":      func(p *HireProposal) { p.Name = "/Users/someone/Agents" },
		"control role":   func(p *HireProposal) { p.Role = "researcher\n" },
		"long workspace": func(p *HireProposal) { p.WorkspaceName = strings.Repeat("w", HireWorkspaceNameMaxRunes+1) },
		"open roles":     func(p *HireProposal) { p.OpenRoles = -1 },
		"bad state":      func(p *HireProposal) { p.State = "maybe" },
	} {
		proposal := sampleProposal("p-"+name, "conv-1")
		mutate(&proposal)
		if _, err := proposals.Create(t.Context(), proposal); !errors.Is(err, ErrValidation) {
			t.Fatalf("%s was stored: %v", name, err)
		}
	}
}
