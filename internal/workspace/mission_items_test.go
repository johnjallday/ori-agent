package workspace

import (
	"encoding/json"
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
			p.SuggestedFirstStep != "Press Back up and approve one metadata read." {
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

const assessmentTestItem = `{"kind": "task", "title": "Draft the issue template", "why": "w", "evidence": "e", "priority": "high", "effort": "small"}`

// goal-charter FR18: a suggested charter is kept only when it reads as a
// charter with an Outcome, bounded at the goal cap on whole lines.
func TestParseMissionRunOutput_ReadsASuggestedCharter(t *testing.T) {
	parse := func(t *testing.T, value string) MissionRunOutput {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"assessment": "a", "items": []any{}, "suggested_charter": value})
		out, err := ParseMissionRunOutput("ws-1", "run-1", string(body))
		if err != nil || out.ParseStatus() != MissionParseOK {
			t.Fatalf("parse: %v %s", err, out.ParseStatus())
		}
		return out
	}

	valid := "## Outcome\r\nA monthly newsletter.\r\n## Maintain\r\n_What the agent keeps in good order on its own initiative._\r\n## Boundaries\r\nNever send."
	if got := parse(t, "  "+valid+"\n").SuggestedCharter; got != "## Outcome\nA monthly newsletter.\n## Maintain\n## Boundaries\nNever send." {
		t.Fatalf("valid charter = %q", got)
	}
	for name, value := range map[string]string{
		"plain text":         "A monthly newsletter that goes out on Tuesdays.",
		"no Outcome":         "## Build\nA template.\n## Watch\nStalled drafts.",
		"empty Outcome only": "## Outcome\n\n## Build\nA template.",
	} {
		if got := parse(t, value).SuggestedCharter; got != "" {
			t.Errorf("%s kept: %q", name, got)
		}
	}
	// A non-string value is ignored and does not fail the run.
	if out, err := ParseMissionRunOutput("ws-1", "run-1", `{"items": [], "suggested_charter": {"Outcome": "x"}}`); err != nil || out.SuggestedCharter != "" {
		t.Fatalf("object charter: %+v %v", out, err)
	}

	// Over the cap: trimmed to whole lines, and a heading left without its
	// text at the cut goes too.
	head := "## Outcome\n" + strings.Repeat("é", 100) + "\n## Build\n"
	filler := strings.Repeat("b", WorkspaceGoalMaxRunes-len([]rune(head))-5)
	long := head + filler + "\n## Watch\n" + strings.Repeat("w", 50)
	got := parse(t, long).SuggestedCharter
	if want := head + filler; got != want {
		t.Fatalf("over-cap charter: %d runes, ends %q", len([]rune(got)), got[len(got)-20:])
	}
	if len([]rune(got)) > WorkspaceGoalMaxRunes {
		t.Fatalf("over the cap: %d", len([]rune(got)))
	}
}

// goal-charter FR11/FR24: the assessment is read when present, its paragraph
// breaks kept and its length bounded; an older-shaped answer still parses ok.
func TestParseMissionRunOutput_ReadsTheAssessment(t *testing.T) {
	out, err := ParseMissionRunOutput("ws-1", "run-1", `{"assessment": "Outcome: nothing written yet.  \r\n\r\n\n\nBuild: the template\ncomes first.\n", "items": [`+assessmentTestItem+`]}`)
	if err != nil || out.ParseStatus() != MissionParseOK {
		t.Fatalf("parse: %v %s", err, out.ParseStatus())
	}
	if want := "Outcome: nothing written yet.\n\nBuild: the template\ncomes first."; out.Assessment != want || len(out.Tasks) != 1 {
		t.Fatalf("assessment = %q (want %q), tasks %d", out.Assessment, want, len(out.Tasks))
	}

	empty, err := ParseMissionRunOutput("ws-1", "run-1", `{"assessment": "Everything is in order.", "items": []}`)
	if err != nil || empty.ParseStatus() != MissionParseOK || empty.Assessment != "Everything is in order." || len(empty.Tasks)+len(empty.Findings) != 0 {
		t.Fatalf("assessment with no items: %+v %v", empty, err)
	}

	old, err := ParseMissionRunOutput("ws-1", "run-1", `{"items": [`+assessmentTestItem+`]}`)
	if err != nil || old.ParseStatus() != MissionParseOK || old.Assessment != "" || len(old.Tasks) != 1 {
		t.Fatalf("old shape: %+v %v", old, err)
	}

	long, _ := ParseMissionRunOutput("ws-1", "run-1", `{"assessment": "`+strings.Repeat("é", MissionAssessmentMax+40)+`", "items": []}`)
	if got := []rune(long.Assessment); len(got) != MissionAssessmentMax || string(got[len(got)-1]) != "é" {
		t.Fatalf("bounded on a rune boundary: %d runes", len(got))
	}

	// A model that structures the assessment by part, or writes something
	// unusable there, does not fail the run.
	structured, err := ParseMissionRunOutput("ws-1", "run-1", `{"assessment": {"build": "A template first.", "outcome": "Nothing yet.", "Notes": "Quiet week."}, "items": []}`)
	if err != nil || structured.Assessment != "Outcome: Nothing yet.\n\nBuild: A template first.\n\nNotes: Quiet week." {
		t.Fatalf("structured assessment = %q %v", structured.Assessment, err)
	}
	odd, err := ParseMissionRunOutput("ws-1", "run-1", `{"assessment": 42, "items": [`+assessmentTestItem+`]}`)
	if err != nil || odd.Assessment != "" || odd.ParseStatus() != MissionParseOK || len(odd.Tasks) != 1 {
		t.Fatalf("unusable assessment: %+v %v", odd, err)
	}
}
