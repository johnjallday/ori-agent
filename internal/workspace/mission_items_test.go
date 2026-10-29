package workspace

import (
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/filecuratorguidance"
)

// backup-first FR9: an unmet protection fact is a backup precondition with the
// PRD's title and first step and the fact's own wording; covered, unavailable
// or an unnamed folder is none.
func TestBackupPrecondition_UnmetStatesOnly(t *testing.T) {
	asOf := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	fact := func(state string) filecuratorguidance.ProtectionFact {
		return filecuratorguidance.ProtectionFact{WorkspaceID: "ws-1", RootGeneration: "root-1", State: state, AsOf: asOf}
	}
	for _, state := range []string{"not_covered", "unknown", "configured"} {
		p, ok := BackupPrecondition("Downloads", fact(state))
		if !ok || p.Kind != "backup_protection" || p.Action != "check_protection" ||
			p.SuggestedTitle != "Back up Downloads before the first filing batch" ||
			p.SuggestedFirstStep != "Open Check protection and approve one metadata read." {
			t.Fatalf("%s: %+v %v", state, p, ok)
		}
		// The fact's own prompt wording, without its heading.
		want := strings.TrimSpace(strings.SplitN(strings.TrimSpace(fact(state).Prompt()), "\n", 2)[1])
		if p.Statement != want || !strings.HasPrefix(p.Statement, "As of 2026-10-29T09:00:00Z (stored metadata only, not a new check):") {
			t.Fatalf("%s statement = %q", state, p.Statement)
		}
	}
	for name, input := range map[string]struct {
		folder string
		fact   filecuratorguidance.ProtectionFact
	}{
		"covered":       {"Downloads", fact("covered")},
		"unavailable":   {"Downloads", fact("unavailable")},
		"no root":       {"Downloads", filecuratorguidance.ProtectionFact{State: "unknown", AsOf: asOf}},
		"unnamed":       {"  ", fact("unknown")},
		"unknown state": {"Downloads", fact("")},
	} {
		if p, ok := BackupPrecondition(input.folder, input.fact); ok {
			t.Fatalf("%s: %+v", name, p)
		}
	}
}

// FR10: a suggestion matches a precondition after title normalization, and
// tagging adds the precondition, its action and who wrote it to the context.
func TestMissionPrecondition_MatchAndTag(t *testing.T) {
	p, _ := BackupPrecondition("Downloads", filecuratorguidance.ProtectionFact{RootGeneration: "r", State: "unknown"})
	if got, ok := MatchMissionPrecondition("  back up downloads   before the first filing batch.", []MissionPrecondition{p}); !ok || got.Kind != p.Kind {
		t.Fatalf("normalized title did not match: %+v %v", got, ok)
	}
	if _, ok := MatchMissionPrecondition("Back up Documents before the first filing batch", []MissionPrecondition{p}); ok {
		t.Fatal("another folder's title matched")
	}
	if _, ok := MatchMissionPrecondition("", []MissionPrecondition{p}); ok {
		t.Fatal("an empty title matched")
	}
	context := TagMissionPrecondition(MissionSuggestionContext(MissionTaskSuggestion{Why: "why"}, "run-1"), p, MissionSuggestionSourceModel)
	if context["precondition"] != "backup_protection" || context["action"] != "check_protection" || context["source"] != "model" ||
		context["why"] != "why" || context["run_id"] != "run-1" {
		t.Fatalf("context = %+v", context)
	}
	if host := TagMissionPrecondition(nil, p, MissionSuggestionSourceHost); host["source"] != "host" {
		t.Fatalf("host context = %+v", host)
	}
}
