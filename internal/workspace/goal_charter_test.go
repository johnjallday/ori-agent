package workspace

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseGoalCharter(t *testing.T) {
	for _, tc := range []struct {
		name      string
		text      string
		isCharter bool
		present   []string
		parts     map[string]string
	}{
		{
			name:  "plain sentence is not a charter",
			text:  "Keep the brand voice consistent.",
			parts: map[string]string{GoalPartOutcome: "Keep the brand voice consistent."},
		},
		{
			name:  "empty goal",
			text:  "   ",
			parts: map[string]string{},
		},
		{
			name:      "all five in canonical order",
			text:      "## Outcome\nA newsletter.\n## Maintain\nTidy ideas.\n## Build\nA template.\n## Watch\nStalled drafts.\n## Boundaries\nNever send.",
			isCharter: true,
			present:   []string{"Outcome", "Maintain", "Build", "Watch", "Boundaries"},
			parts: map[string]string{
				"Outcome": "A newsletter.", "Maintain": "Tidy ideas.", "Build": "A template.",
				"Watch": "Stalled drafts.", "Boundaries": "Never send.",
			},
		},
		{
			name:      "any order and subset, reported in canonical order",
			text:      "## Boundaries\nNever push to main.\n\n## Build\nA release pipeline.",
			isCharter: true,
			present:   []string{"Build", "Boundaries"},
			parts:     map[string]string{"Build": "A release pipeline.", "Boundaries": "Never push to main."},
		},
		{
			name:      "heading forms and case",
			text:      "# outcome\nShip it.\n### WATCH:\nCI.\n**Maintain**\nDocs.\n**Build:**\nA CLI.\n**boundaries**:\nNo deploys.",
			isCharter: true,
			present:   []string{"Outcome", "Maintain", "Build", "Watch", "Boundaries"},
			parts: map[string]string{
				"Outcome": "Ship it.", "Maintain": "Docs.", "Build": "A CLI.", "Watch": "CI.", "Boundaries": "No deploys.",
			},
		},
		{
			name:      "text before the first heading belongs to Outcome",
			text:      "A monthly newsletter.\n\n## Build\nA welcome note.",
			isCharter: true,
			present:   []string{"Outcome", "Build"},
			parts:     map[string]string{"Outcome": "A monthly newsletter.", "Build": "A welcome note."},
		},
		{
			name:      "an unknown heading folds into the preceding part",
			text:      "## Build\nA template.\n## Notes\nUse the house style.\n#### Watch\nNot a heading at four hashes.",
			isCharter: true,
			present:   []string{"Build"},
			parts: map[string]string{
				"Build": "A template.\n## Notes\nUse the house style.\n#### Watch\nNot a heading at four hashes.",
			},
		},
		{
			name:      "a repeated heading appends",
			text:      "## Watch\nCI.\n## Build\nA CLI.\n## Watch\nIssues.",
			isCharter: true,
			present:   []string{"Build", "Watch"},
			parts:     map[string]string{"Watch": "CI.\nIssues.", "Build": "A CLI."},
		},
		{
			name:      "an empty part is not present",
			text:      "## Outcome\nShip it.\n## Watch\n\n## Boundaries\nNo deploys.",
			isCharter: true,
			present:   []string{"Outcome", "Boundaries"},
			parts:     map[string]string{"Outcome": "Ship it.", "Boundaries": "No deploys."},
		},
		{
			name:  "a part name inside a sentence or bold mid-line is not a heading",
			text:  "Outcome: ship it\nThe **Build** is green",
			parts: map[string]string{"Outcome": "Outcome: ship it\nThe **Build** is green"},
		},
		{
			name:  "a heading naming more than the part is not a part",
			text:  "## Outcome and scope\nShip it.",
			parts: map[string]string{"Outcome": "## Outcome and scope\nShip it."},
		},
		{
			name:      "CRLF line endings",
			text:      "## Outcome\r\nShip it.\r\n## Build\r\nA CLI.",
			isCharter: true,
			present:   []string{"Outcome", "Build"},
			parts:     map[string]string{"Outcome": "Ship it.", "Build": "A CLI."},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseGoalCharter(tc.text)
			if got.IsCharter != tc.isCharter {
				t.Fatalf("IsCharter = %v, want %v", got.IsCharter, tc.isCharter)
			}
			if !reflect.DeepEqual(got.Present, tc.present) {
				t.Fatalf("Present = %#v, want %#v", got.Present, tc.present)
			}
			if !reflect.DeepEqual(got.Parts, tc.parts) {
				t.Fatalf("Parts = %#v, want %#v", got.Parts, tc.parts)
			}
		})
	}
}

func TestGoalOutcomeLine(t *testing.T) {
	for _, tc := range []struct {
		name, mission, want string
	}{
		{"empty", "  \n ", ""},
		{"plain goal is flattened whole, as before charters", "Keep the voice\n consistent. Reply within a day.", "Keep the voice consistent. Reply within a day."},
		{"charter shows its first Outcome line", "## Outcome\n\nA monthly newsletter.\nSecond line.\n## Build\nA template.", "A monthly newsletter."},
		{"text before the first heading is the Outcome", "Ship v2.\n## Boundaries\nNever push to main.", "Ship v2."},
		{"an untouched hint is skipped", "## Outcome\n_What this workspace exists to produce or keep true._\nShip v2.", "Ship v2."},
		{"a charter without Outcome shows its first line of text", "## Watch\nFailing CI.\n## Build\nA release pipeline.", "A release pipeline."},
		{"a charter of empty headings has no line", "## Outcome\n## Build\n", ""},
		{"never a heading", "**Outcome**\n  Keep   the archive\ttidy  ", "Keep the archive tidy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := GoalOutcomeLine(tc.mission); got != tc.want {
				t.Fatalf("GoalOutcomeLine = %q, want %q", got, tc.want)
			}
		})
	}

	long := "## Outcome\n" + strings.Repeat("a", WorkspaceGoalPromptLimit+50)
	if got := GoalOutcomeLine(long); len(got) != WorkspaceGoalPromptLimit || !strings.HasSuffix(got, "...") {
		t.Fatalf("a long Outcome line must be bounded, got %d chars", len(got))
	}
}

func TestStripGoalHints(t *testing.T) {
	var filled strings.Builder
	for _, part := range GoalCharterParts {
		filled.WriteString("## " + part.Name + "\n" + GoalHintLine(part) + "\n")
	}
	want := "## Outcome\n## Maintain\n## Build\n## Watch\n## Boundaries\n"
	if got := StripGoalHints(filled.String()); got != want {
		t.Fatalf("untouched fill = %q, want %q", got, want)
	}

	edited := "## Outcome\nShip v2.\n  _What the agent keeps in good order on its own initiative._  \n## Build\n_What the agent should work towards that does not exist yet, and more._\n_emphasis the user wrote_"
	wantEdited := "## Outcome\nShip v2.\n## Build\n_What the agent should work towards that does not exist yet, and more._\n_emphasis the user wrote_"
	if got := StripGoalHints(edited); got != wantEdited {
		t.Fatalf("only exact hint lines go: %q", got)
	}

	if got := StripGoalHints("Keep the brand voice consistent."); got != "Keep the brand voice consistent." {
		t.Fatalf("a plain goal is untouched: %q", got)
	}
}

func TestGoalDigest(t *testing.T) {
	if GoalDigest("  \n") != "" {
		t.Fatal("no goal, no digest")
	}
	a := GoalDigest("help me write a newsletter")
	if len(a) != 16 || a != GoalDigest("  help me write a newsletter\n") {
		t.Fatalf("digest %q must ignore surrounding space", a)
	}
	if a == GoalDigest("help me write a weekly newsletter") || GoalDigest("a\r\nb") != GoalDigest("a\nb") {
		t.Fatal("digest must change with the text and ignore line-ending style")
	}
}

// The toolbox goal brief's summary is a one-line field: a charter fills it
// with its Outcome line, while the keyword inference still reads every part.
func TestProposeGoalBrief_SummarizesACharterByItsOutcome(t *testing.T) {
	brief := ProposeGoalBrief("## Outcome\nA tidy research archive.\n## Build\nSearch the web and summarize the findings.", AutonomyWatch)
	if brief.Summary != "A tidy research archive." {
		t.Fatalf("Summary = %q", brief.Summary)
	}
	plain := ProposeGoalBrief("  search the web and summarize the findings ", AutonomyWatch)
	if plain.Summary != "search the web and summarize the findings" {
		t.Fatalf("plain Summary = %q", plain.Summary)
	}
}

func TestGoalCharterPartsAreTheFiveInCanonicalOrder(t *testing.T) {
	var names []string
	for _, part := range GoalCharterParts {
		if strings.TrimSpace(part.Meaning) == "" {
			t.Fatalf("%s has no meaning", part.Name)
		}
		names = append(names, part.Name)
	}
	if want := []string{"Outcome", "Maintain", "Build", "Watch", "Boundaries"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("parts = %v, want %v", names, want)
	}
	if WorkspaceGoalMaxRunes != 4000 {
		t.Fatalf("WorkspaceGoalMaxRunes = %d", WorkspaceGoalMaxRunes)
	}
}
