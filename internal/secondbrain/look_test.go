package secondbrain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/testutil/vaultfixture"
)

func musicSource(t *testing.T) (Source, vaultfixture.Vault) {
	t.Helper()
	vault, err := vaultfixture.Build(canonicalTemp(t), vaultfixture.Music, fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	return Source{ID: SourceID(vault.Path), Kind: KindObsidian, Name: vault.Name, Path: vault.Path, ConnectedAt: fixtureNow, RequestID: "connect-1"}, vault
}

func fixedLooker() Looker { return Looker{Now: func() time.Time { return fixtureNow }} }

func TestLooker_RulesOnlyLookFinishesWithOneHeadlineAndASuggestedSet(t *testing.T) {
	source, vault := musicSource(t)
	run := fixedLooker().Run(context.Background(), source, nil, "look-1", "connect-1")
	look := run.Look
	if look.State != LookFinished || look.HeadlineBy != HeadlineByRules {
		t.Fatalf("look = %+v", look)
	}
	if want := "12 open items across 6 notes, 4 with dates."; look.Headline != want {
		t.Fatalf("headline = %q, want %q", look.Headline, want)
	}
	if len(look.Items) != vault.OpenItems || look.Counts.OpenItems != vault.OpenItems {
		t.Fatalf("items = %d, counts %+v", len(look.Items), look.Counts)
	}
	var suggested []LookItem
	for _, item := range look.Items {
		if item.Suggested {
			suggested = append(suggested, item)
		}
	}
	if len(suggested) == 0 || len(suggested) > MaxSuggested {
		t.Fatalf("suggested = %d", len(suggested))
	}
	record := Record{Sources: []Source{source}}
	record.Sources[0].LastLook = &look
	if err := record.Validate(); err != nil {
		t.Fatalf("the look does not fit the record: %v", err)
	}
}

func TestLooker_KeepsBoundedSignalsAndNoLineText(t *testing.T) {
	source, vault := musicSource(t)
	look := fixedLooker().Run(context.Background(), source, nil, "look-1", "connect-1").Look
	if len(look.People) != 2 || look.People[0].Name != vault.TopPerson || look.People[0].Mentions != vault.TopMentions ||
		look.People[0].Note != "People/Marco.md" || !strings.HasPrefix(look.People[0].Link, "obsidian://") {
		t.Fatalf("people = %+v", look.People)
	}
	if len(look.Stale) != 1 || look.Stale[0].Note != vault.Stale || look.Stale[0].Title != "Studio Website" || look.Stale[0].Open != 3 || look.Stale[0].Modified.IsZero() {
		t.Fatalf("stale = %+v", look.Stale)
	}
	if len(look.Decisions) != 1 || look.Decisions[0].Note != vault.Decision || look.Decisions[0].Title != "Distribution Decision" || look.Decisions[0].Open != 2 {
		t.Fatalf("decisions = %+v", look.Decisions)
	}
	encoded, err := json.Marshal(struct {
		P []LookPerson
		S []LookStaleNote
		D []LookDecision
	}{look.People, look.Stale, look.Decisions})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"Decide by", "Label manager", "Last touched", "Designer for"} {
		if strings.Contains(string(encoded), line) {
			t.Fatalf("the signals kept a note's words %q: %s", line, encoded)
		}
	}
}

func TestLookSignals_CapsFoldsAndSkipsWhatCannotBeKept(t *testing.T) {
	var signals Signals
	for i := range MaxLookSignals + 5 {
		name := fmt.Sprintf("n%d", i)
		signals.People = append(signals.People, Person{Name: name, Note: "People/" + name + ".md", Mentions: 1})
		signals.Stale = append(signals.Stale, StaleNote{Note: name + ".md", Title: name, Open: 1, Modified: fixtureNow})
		signals.Decisions = append(signals.Decisions, DecisionNote{Note: name + ".md", Title: name, Text: "the decision line", Open: 1})
	}
	people, stale, decisions := lookSignals(Source{Kind: KindMarkdown, Name: "Brain"}, signals)
	if len(people) != MaxLookSignals || len(stale) != MaxLookSignals || len(decisions) != MaxLookSignals || people[0].Link != "" {
		t.Fatalf("kept %d/%d/%d, first person %+v", len(people), len(stale), len(decisions), people[0])
	}

	// Notes whose links run long hit the byte budget before the count cap.
	var heavy Signals
	for i := range MaxLookSignals {
		name := fmt.Sprintf("%02d %s.md", i, strings.Repeat("語", 90))
		heavy.Stale = append(heavy.Stale, StaleNote{Note: name, Title: strings.Repeat("語", MaxSignalTitleRunes), Open: 1, Modified: fixtureNow})
		heavy.Decisions = append(heavy.Decisions, DecisionNote{Note: name, Title: strings.Repeat("語", MaxSignalTitleRunes), Open: 1})
		heavy.People = append(heavy.People, Person{Name: name, Note: name, Mentions: 1})
	}
	heavyPeople, heavyStale, heavyDecisions := lookSignals(Source{Kind: KindObsidian, Name: "Brain"}, heavy)
	encoded, err := json.Marshal([]any{heavyPeople, heavyStale, heavyDecisions})
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > maxStoredSignalBytes+64 || len(heavyPeople)+len(heavyStale)+len(heavyDecisions) == 3*MaxLookSignals {
		t.Fatalf("kept %d bytes in %d/%d/%d signals, want the budget to stop them", len(encoded), len(heavyPeople), len(heavyStale), len(heavyDecisions))
	}

	long := strings.Repeat("é", MaxSignalTitleRunes+40)
	_, stale, decisions = lookSignals(Source{Kind: KindMarkdown, Name: "Brain"}, Signals{
		Stale:     []StaleNote{{Note: `odd\name.md`, Title: "skipped", Modified: fixtureNow}, {Note: "kept.md", Title: "  two\n lines ", Modified: fixtureNow}},
		Decisions: []DecisionNote{{Note: "a.md", Title: long}, {Note: strings.Repeat("x", maxSignalNoteBytes+1), Title: "skipped"}},
	})
	if len(stale) != 1 || stale[0].Title != "two lines" {
		t.Fatalf("stale = %+v", stale)
	}
	if len(decisions) != 1 || utf8.RuneCountInString(decisions[0].Title) > MaxSignalTitleRunes {
		t.Fatalf("decisions = %+v", decisions)
	}
}

func TestSuggest_DatedThenDecisionsThenActiveCappedAtTen(t *testing.T) {
	loops := []OpenLoop{
		{Task: Task{Text: "active"}, InActive: true},
		{Task: Task{Text: "old"}},
		{Task: Task{Text: "decision"}, InDecision: true},
		{Task: Task{Text: "later", Due: "2026-11-01"}},
		{Task: Task{Text: "soon", Due: "2026-10-22"}},
	}
	chosen := Suggest(loops)
	if len(chosen) != 4 || chosen[1] {
		t.Fatalf("chosen = %v, want all but the old undated item", chosen)
	}
	var many []OpenLoop
	for range 15 {
		many = append(many, OpenLoop{Task: Task{Text: "x", Due: "2026-10-22"}})
	}
	if got := len(Suggest(many)); got != MaxSuggested {
		t.Fatalf("cap = %d", got)
	}
	if got := len(Suggest([]OpenLoop{{Task: Task{Text: "old"}}})); got != 1 {
		t.Fatalf("fallback = %d, want the old item to stand in", got)
	}
}

func TestRulesHeadline(t *testing.T) {
	cases := map[string]LookCounts{
		"No Markdown notes found in Brain.":                         {},
		"No open items in 3 notes.":                                 {Notes: 3},
		"1 open item across 1 note, 1 with a date.":                 {Notes: 2, OpenItems: 1, NotesWithOpen: 1, Dated: 1},
		"5 open items across 2 notes, 0 with dates. (partial look)": {Notes: 4, OpenItems: 5, NotesWithOpen: 2, Partial: true},
	}
	for want, counts := range cases {
		if got := RulesHeadline("Brain", counts); got != want {
			t.Errorf("RulesHeadline(%+v) = %q, want %q", counts, got, want)
		}
	}
}

func TestLooker_AMissingFolderFailsWithOneSentence(t *testing.T) {
	source, vault := musicSource(t)
	if err := os.Rename(vault.Path, vault.Path+" moved"); err != nil {
		t.Fatal(err)
	}
	look := fixedLooker().Run(context.Background(), source, nil, "look-1", "").Look
	if look.State != LookFailed || !strings.HasPrefix(look.Failure, "Can't find Second Brain.") || look.FinishedAt == nil {
		t.Fatalf("look = %+v", look)
	}
}

func TestLooker_ComparesWithThePreviousLook(t *testing.T) {
	source, vault := musicSource(t)
	first := fixedLooker().Run(context.Background(), source, nil, "look-1", "").Look
	note := filepath.Join(vault.Path, "Reading List.md")
	data, err := os.ReadFile(note) // #nosec G304 -- the test's own fixture
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), "- [ ] Read Making Music", "- [x] Read Making Music", 1) + "- [ ] Read Song Machine\n"
	if err := os.WriteFile(note, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	second := fixedLooker().Run(context.Background(), source, &first, "look-2", "")
	if second.Look.NewSince != 1 || second.Look.DoneSince != 1 || len(second.Gone) != 1 {
		t.Fatalf("new %d done %d gone %v", second.Look.NewSince, second.Look.DoneSince, second.Gone)
	}
	if second.Gone[0] != ItemID(source.ID, "Reading List.md", "Read Making Music by Dennis DeSantis") {
		t.Fatalf("gone = %v", second.Gone)
	}
}

func newFollowUps(t *testing.T) *followup.Service {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return followup.NewService(followup.NewSQLiteStore(db))
}

func TestCompleteGone_ClosesOnlyOpenFollowUpsAndSaysDoneInYourNotes(t *testing.T) {
	ctx := context.Background()
	source, _ := musicSource(t)
	service := newFollowUps(t)
	look := fixedLooker().Run(ctx, source, nil, "look-1", "").Look
	in := Acceptance{UserID: "local", WorkspaceID: "hq-local", Source: source, Items: look.Items[:3], Location: time.UTC}
	if _, _, err := Accept(ctx, service, in); err != nil {
		t.Fatal(err)
	}
	dismissed, err := service.FindBySource(ctx, "local", FollowUpSourceType, look.Items[2].ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Dismiss(ctx, "local", dismissed.ID); err != nil {
		t.Fatal(err)
	}

	gone := []string{look.Items[0].ID, look.Items[2].ID, ItemID(source.ID, "Gone.md", "never accepted")}
	completed, err := CompleteGone(ctx, service, "local", gone)
	if err != nil || completed != 1 {
		t.Fatalf("completed = %d, %v; want only the open one", completed, err)
	}
	done, _ := service.FindBySource(ctx, "local", FollowUpSourceType, look.Items[0].ID)
	if done.Status != followup.StatusCompleted || !strings.Contains(done.Detail, DoneInNotes) || !strings.Contains(done.Detail, "Open in Obsidian: obsidian://") {
		t.Fatalf("done follow-up = %+v", done)
	}
	if still, _ := service.FindBySource(ctx, "local", FollowUpSourceType, look.Items[2].ID); still.Status != followup.StatusDismissed {
		t.Fatalf("a dismissed follow-up was changed: %+v", still)
	}
	if open, _ := service.FindBySource(ctx, "local", FollowUpSourceType, look.Items[1].ID); open.Status != followup.StatusActive {
		t.Fatalf("an item still open in the notes was closed: %+v", open)
	}
	if again, err := CompleteGone(ctx, service, "local", gone); err != nil || again != 0 {
		t.Fatalf("a second pass = %d, %v", again, err)
	}
}

func TestAccept_CreatesFollowUpsOnceAcrossTwoAcceptancesAndALaterLook(t *testing.T) {
	ctx := context.Background()
	source, _ := musicSource(t)
	service := newFollowUps(t)
	look := fixedLooker().Run(ctx, source, nil, "look-1", "").Look
	var suggested []LookItem
	for _, item := range look.Items {
		if item.Suggested {
			suggested = append(suggested, item)
		}
	}
	in := Acceptance{UserID: "local", WorkspaceID: "hq-local", Source: source, Items: suggested, Location: time.UTC}
	created, skipped, err := Accept(ctx, service, in)
	if err != nil || created != len(suggested) || skipped != 0 {
		t.Fatalf("first accept = %d created %d skipped %v", created, skipped, err)
	}
	created, skipped, err = Accept(ctx, service, in)
	if err != nil || created != 0 || skipped != len(suggested) {
		t.Fatalf("second accept = %d created %d skipped %v", created, skipped, err)
	}
	later := fixedLooker().Run(ctx, source, &look, "look-2", "").Look
	in.Items = later.Items
	created, skipped, err = Accept(ctx, service, in)
	if err != nil || created != len(later.Items)-len(suggested) || skipped != len(suggested) {
		t.Fatalf("accept after a later look = %d created %d skipped %v", created, skipped, err)
	}
	all, err := service.List(ctx, followup.Filter{UserID: "local"})
	if err != nil || len(all) != len(later.Items) {
		t.Fatalf("follow-ups = %d, want %d, err %v", len(all), len(later.Items), err)
	}
	for _, f := range all {
		if f.Source.Type != FollowUpSourceType || f.WorkspaceID != "hq-local" || f.Detail == "" {
			t.Fatalf("follow-up = %+v", f)
		}
		if f.Title == "Master track 7" && (f.DueAt == nil || f.DueAt.Format("2006-01-02") != fixtureNow.Add(3*24*time.Hour).Format("2006-01-02")) {
			t.Fatalf("due = %v", f.DueAt)
		}
		if strings.HasPrefix(f.Title, "Compare DistroKid") && f.Category != followup.CategoryNeedsDecision {
			t.Fatalf("decision item category = %s", f.Category)
		}
	}
}
