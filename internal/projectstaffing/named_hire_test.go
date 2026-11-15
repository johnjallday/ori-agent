package projectstaffing

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func namedHire(t *testing.T, s *Service, names map[string]string, create SharedCreate) ([]Fill, error) {
	t.Helper()
	var fills []Fill
	err := s.WithHome("home", func(tx *Tx) error {
		var err error
		fills, err = tx.HireSharedNamed(t.Context(), songTeam, folderGrant, true, names, create)
		return err
	})
	return fills, err
}

func TestHireSharedNamed_ExactNameKeepsRoleAndRecoversOneCreate(t *testing.T) {
	_, s, roster := hiredFixture(t)
	calls := 0
	names := map[string]string{songTeam.Roles[0].ID: "Muse"}
	underlying := stoppingCreator(roster, &calls)
	create := func(ctx context.Context, role workspace.AssistantProgramRoleSpec, name string, marker *agent.AssistantSetupProvenance) (bool, error) {
		if !reflect.DeepEqual(role, songTeam.Roles[0]) {
			t.Fatal("name edit changed the role passed to creation")
		}
		return underlying(ctx, role, name, marker)
	}
	if _, err := namedHire(t, s, names, create); err == nil {
		t.Fatal("expected simulated stop after create")
	}
	if fills, err := namedHire(t, s, names, creator(roster, &calls)); err != nil || len(fills) != 1 || fills[0].Mode != ModeBind || fills[0].Name != "Muse" {
		t.Fatalf("recovery: %+v %v", fills, err)
	}
	if calls != 1 || len(roster) != 1 || roleOf(t, s).AgentName != "Muse" {
		t.Fatal("retry duplicated/renamed the shared assistant")
	}
}

func TestHireSharedNamed_CollisionNeverSuffixesOrAdopts(t *testing.T) {
	_, s, roster := hiredFixture(t)
	roster["Muse"] = &agent.Agent{}
	calls := 0
	fills, err := namedHire(t, s, map[string]string{songTeam.Roles[0].ID: "Muse"}, creator(roster, &calls))
	if !errors.Is(err, ErrReservationHeld) || len(fills) != 0 || calls != 0 || len(roster) != 1 {
		t.Fatalf("collision: %+v %v calls=%d", fills, err, calls)
	}
	role := roleOf(t, s)
	if role.AgentName != "" || role.PendingName != "" {
		t.Fatalf("adopted/reserved collision: %+v", role)
	}
}

func TestHireSharedNamed_UnknownRoleWritesNothing(t *testing.T) {
	_, s, roster := hiredFixture(t)
	calls := 0
	if _, err := namedHire(t, s, map[string]string{"forged-role": "Muse"}, creator(roster, &calls)); !errors.Is(err, ErrNotShared) {
		t.Fatalf("unknown role: %v", err)
	}
	consent, _ := s.Consents().Read("home")
	if consent != nil || calls != 0 || len(roster) != 0 {
		t.Fatalf("unknown role mutated authority: %+v", consent)
	}
}
