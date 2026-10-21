package secondbrain

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/testutil/softwarefixture"
)

var extractDay = time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)

func extract(rel, content string) NoteFacts {
	return ExtractNote(Note{RelPath: rel, ModTime: extractDay, Content: []byte(content)})
}

func taskTexts(tasks []Task) []string {
	out := make([]string, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, task.Text)
	}
	return out
}

func TestExtractNote_OpenAndDoneMarkers(t *testing.T) {
	cases := []struct {
		line       string
		open, done bool
		text       string
	}{
		{line: "- [ ] dash item", open: true, text: "dash item"},
		{line: "* [ ] star item", open: true, text: "star item"},
		{line: "+ [ ] plus item", open: true, text: "plus item"},
		{line: "1. [ ] numbered item", open: true, text: "numbered item"},
		{line: "12) [ ] paren item", open: true, text: "paren item"},
		{line: "\t  - [ ] nested item", open: true, text: "nested item"},
		{line: "- [ ]    spaced    out   item  ", open: true, text: "spaced out item"},
		{line: "- TODO logseq todo", open: true, text: "logseq todo"},
		{line: "- DOING logseq doing", open: true, text: "logseq doing"},
		{line: "- LATER logseq later", open: true, text: "logseq later"},
		{line: "- NOW logseq now", open: true, text: "logseq now"},
		{line: "- [x] done lower", done: true},
		{line: "- [X] done upper", done: true},
		{line: "* [x] done star", done: true},
		{line: "3. [x] done numbered", done: true},
		{line: "- DONE logseq done", done: true},
		{line: "- [ ]"},
		{line: "-[ ] no space"},
		{line: "- [-] cancelled"},
		{line: "- todo lowercase is prose"},
		{line: "TODO without a bullet"},
		{line: "[ ] no bullet"},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			facts := extract("Note.md", tc.line+"\n")
			if gotOpen := len(facts.Open) == 1; gotOpen != tc.open {
				t.Fatalf("open = %v (%v), want %v", gotOpen, taskTexts(facts.Open), tc.open)
			}
			if tc.open && facts.Open[0].Text != tc.text {
				t.Fatalf("text = %q, want %q", facts.Open[0].Text, tc.text)
			}
			if gotDone := facts.Done == 1; gotDone != tc.done {
				t.Fatalf("done = %d, want %v", facts.Done, tc.done)
			}
		})
	}
}

func TestExtractNote_RecordsTheLineNumberBelowFrontmatter(t *testing.T) {
	facts := extract("Note.md", "---\ntitle: Plan\n---\n\nIntro\n- [ ] first\n\n- [ ] second\n")
	if len(facts.Open) != 2 || facts.Open[0].Line != 6 || facts.Open[1].Line != 8 {
		t.Fatalf("open = %+v, want lines 6 and 8", facts.Open)
	}
}

func TestExtractNote_Dates(t *testing.T) {
	december := time.Date(2026, 12, 20, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		line    string
		modTime time.Time
		due     string
		text    string
	}{
		{line: "- [ ] ship the master 📅 2026-10-25", due: "2026-10-25", text: "ship the master"},
		{line: "- [ ] ship ⏫ 📅 2026-10-25 🔁 every week", due: "2026-10-25", text: "ship"},
		{line: "- [ ] pay the invoice due: 2026-11-01", due: "2026-11-01", text: "pay the invoice due: 2026-11-01"},
		{line: "- [ ] call back [due:: 2026-11-02]", due: "2026-11-02", text: "call back"},
		{line: "- [ ] review the 2026-10-30 draft", due: "2026-10-30", text: "review the 2026-10-30 draft"},
		{line: "- [ ] water plants ⏳ 2026-10-28", due: "2026-10-28", text: "water plants"},
		{line: "- [ ] started ✅ 2026-10-01 ➕ 2026-09-01", due: "", text: "started"},
		{line: "- [ ] send the mix by Oct 25", due: "2026-10-25", text: "send the mix by Oct 25"},
		{line: "- [ ] send the mix by October 25th", due: "2026-10-25", text: "send the mix by October 25th"},
		{line: "- [ ] renew by Sept. 3", due: "2026-09-03"},
		{line: "- [ ] renew by Jan 5", modTime: december, due: "2027-01-05"},
		{line: "- [ ] renew by Feb 30", due: ""},
		{line: "- [ ] renew 2026-13-45", due: ""},
		{line: "- [ ] go by bus", due: ""},
		{line: "- TODO book the studio 📅 2026-11-03", due: "2026-11-03", text: "book the studio"},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			modTime := tc.modTime
			if modTime.IsZero() {
				modTime = extractDay
			}
			facts := ExtractNote(Note{RelPath: "Note.md", ModTime: modTime, Content: []byte(tc.line)})
			if len(facts.Open) != 1 {
				t.Fatalf("open = %v, want one", taskTexts(facts.Open))
			}
			if facts.Open[0].Due != tc.due {
				t.Fatalf("due = %q, want %q", facts.Open[0].Due, tc.due)
			}
			if tc.text != "" && facts.Open[0].Text != tc.text {
				t.Fatalf("text = %q, want %q", facts.Open[0].Text, tc.text)
			}
		})
	}
}

func TestExtractNote_CapsTheTextAt200Runes(t *testing.T) {
	facts := extract("Note.md", "- [ ] "+strings.Repeat("é", 250))
	if got := len([]rune(facts.Open[0].Text)); got != MaxTaskTextRunes {
		t.Fatalf("text has %d runes, want %d", got, MaxTaskTextRunes)
	}
}

func TestExtractNote_Wikilinks(t *testing.T) {
	facts := extract("Note.md", "Met [[Sarah Kim]] and [[Sarah Kim|Sarah]] about [[Projects/Album#Mix|the album]].\n"+
		"![[cover.png]] [[ ]] [[Bo]] [[bo]] [[Plan^block]]\n")
	want := []string{"Sarah Kim", "Projects/Album", "Bo", "Plan"}
	if !reflect.DeepEqual(facts.Links, want) {
		t.Fatalf("links = %q, want %q", facts.Links, want)
	}
}

func TestExtractNote_TitleTagsStatus(t *testing.T) {
	cases := []struct {
		name, rel, content string
		title, status      string
		tags               []string
	}{
		{
			name: "frontmatter list", rel: "a.md",
			content: "---\ntitle: Distributor\ntags: [music, \"#label\"]\nstatus: deciding\n---\n# Heading\n",
			title:   "Distributor", status: "deciding", tags: []string{"music", "label"},
		},
		{
			name: "frontmatter block list and string", rel: "a.md",
			content: "---\ntags:\n  - person\n  - friend\nstatus:\n  - active\n---\n# Sarah\n",
			title:   "Sarah", status: "active", tags: []string{"person", "friend"},
		},
		{
			name: "comma tags", rel: "a.md", content: "---\ntags: \"work, #urgent\"\n---\n",
			title: "a", tags: []string{"work", "urgent"},
		},
		{
			name: "legacy tag key", rel: "a.md", content: "---\ntag: person\n---\n",
			title: "a", tags: []string{"person"},
		},
		{name: "heading", rel: "Folder/b.md", content: "Intro\n## Not this\n# Big Plan #\n", title: "Big Plan"},
		{name: "file name", rel: "Folder/Some Note.markdown", content: "no heading\n", title: "Some Note"},
		{
			name: "bad yaml is body", rel: "c.md", content: "---\ntitle: [unclosed\n---\n# Real\n",
			title: "Real",
		},
		{name: "unclosed frontmatter is body", rel: "d.md", content: "---\ntitle: X\n# Real\n", title: "Real"},
		{
			name: "logseq properties", rel: "pages/e.md",
			content: "title:: Sarah Kim\ntags:: person, [[friend]]\nstatus:: active\n\n- met at the show\n",
			title:   "Sarah Kim", status: "active", tags: []string{"person", "friend"},
		},
		{name: "byte order mark", rel: "f.md", content: "\xef\xbb\xbf# Marked\n", title: "Marked"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			facts := extract(tc.rel, tc.content)
			if facts.Title != tc.title || facts.Status != tc.status || !reflect.DeepEqual(facts.Tags, tc.tags) {
				t.Fatalf("title=%q status=%q tags=%q, want %q %q %q", facts.Title, facts.Status, facts.Tags, tc.title, tc.status, tc.tags)
			}
		})
	}
}

func TestExtractNote_IgnoresFencedCode(t *testing.T) {
	facts := extract("Note.md", "```md\n- [ ] not a task\n- [x] not done\n```\n~~~\n- TODO nope\n~~~\n- [ ] real\n")
	if got := taskTexts(facts.Open); !reflect.DeepEqual(got, []string{"real"}) || facts.Done != 0 {
		t.Fatalf("open=%v done=%d, want [real] and 0", got, facts.Done)
	}
}

func TestExtractNote_DropsSecretLikeLines(t *testing.T) {
	secret := softwarefixture.CredentialShapedValue()
	facts := extract("Note.md", "- [ ] rotate the token "+secret+" [[Vault]]\n- TODO paste "+secret+"\n- [ ] keep this\n# "+secret+"\n")
	if got := taskTexts(facts.Open); !reflect.DeepEqual(got, []string{"keep this"}) {
		t.Fatalf("open = %v, want only the clean item", got)
	}
	if facts.DroppedSecrets != 2 {
		t.Fatalf("DroppedSecrets = %d, want 2", facts.DroppedSecrets)
	}
	if len(facts.Links) != 0 {
		t.Fatalf("links = %v, want none from a dropped line", facts.Links)
	}
	if strings.Contains(facts.Title, secret) || facts.Title != "Note" {
		t.Fatalf("title = %q, want the file name", facts.Title)
	}
}

func TestExtractNote_Decisions(t *testing.T) {
	cases := []struct {
		name, content string
		decision      bool
		text          string
	}{
		{name: "decide line", content: "# Label\nDecide on the distributor by Oct 25\n", decision: true, text: "Decide on the distributor by Oct 25"},
		{name: "need to decide bullet", content: "- Need to decide which mixer\n", decision: true, text: "Need to decide which mixer"},
		{name: "decision heading", content: "## Decision: venue\n", decision: true, text: "Decision: venue"},
		{name: "open decide task", content: "- [ ] Decide on artwork\n", decision: true, text: "Decide on artwork"},
		{name: "done decide task", content: "- [x] Decide on artwork\n"},
		{name: "decided is past", content: "Decided to go with Tunecore.\n"},
		{name: "decisions log", content: "Decisions log\n"},
		{name: "status deciding", content: "---\nstatus: deciding\n---\n# Venue\n", decision: true, text: "Venue"},
		{name: "status undecided", content: "---\nstatus: undecided\n---\n# Venue\n", decision: true, text: "Venue"},
		{name: "status decided wins", content: "---\nstatus: decided\n---\nDecide on venue\n"},
		{name: "title decision with open item", content: "# Distributor decision\n- [ ] compare fees\n", decision: true, text: "Distributor decision"},
		{name: "title decision without open item", content: "# Distributor decision\nWent with Tunecore.\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			facts := extract("Note.md", tc.content)
			if facts.Decision != tc.decision || facts.DecisionText != tc.text {
				t.Fatalf("decision=%v text=%q, want %v %q", facts.Decision, facts.DecisionText, tc.decision, tc.text)
			}
		})
	}
}

func TestIsDailyNote(t *testing.T) {
	cases := map[string]bool{
		"2026-10-20.md":                 true,
		"Daily/2026-10-20.md":           true,
		"Daily/2026-10-20.markdown":     true,
		"journals/2026_10_20.md":        true,
		"Logseq/journals/2026_10_20.md": true,
		"2026_10_20.md":                 false,
		"pages/2026_10_20.md":           false,
		"2026-02-30.md":                 false,
		"notes/2026-10-20 meeting.md":   false,
		"Inbox.md":                      false,
	}
	for rel, want := range cases {
		if got := IsDailyNote(rel); got != want {
			t.Errorf("IsDailyNote(%q) = %v, want %v", rel, got, want)
		}
	}
}

func factsAt(rel, content string, modTime time.Time) NoteFacts {
	return ExtractNote(Note{RelPath: rel, ModTime: modTime, Content: []byte(content)})
}

func TestDerive_StaleActiveAndCounts(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	notes := []NoteFacts{
		factsAt("Projects/Website.md", "- [ ] fix footer\n- [ ] update bio\n- [ ] add shows\n", now.Add(-StaleAfter)),
		factsAt("Projects/Almost.md", "- [ ] one\n", now.Add(-StaleAfter+time.Hour)),
		factsAt("Archive/Old.md", "- [x] done long ago\n", now.Add(-200*24*time.Hour)),
		factsAt("Inbox.md", "- [ ] call distributor 📅 2026-10-25\n- [x] book flights\n", now.Add(-2*time.Hour)),
		factsAt("2026-10-20.md", "- [ ] buy strings\n", now.Add(-26*time.Hour)),
		factsAt("Week.md", "notes only\n", now.Add(-ActiveWithin)),
		factsAt("Last Week.md", "notes only\n", now.Add(-ActiveWithin-time.Minute)),
	}
	signals := Derive(notes, now)

	if signals.Notes != 7 || signals.NotesWithOpen != 4 || len(signals.OpenLoops) != 6 || signals.Dated != 1 || signals.Done != 2 || signals.DailyNotes != 1 {
		t.Fatalf("counts = notes %d withOpen %d loops %d dated %d done %d daily %d",
			signals.Notes, signals.NotesWithOpen, len(signals.OpenLoops), signals.Dated, signals.Done, signals.DailyNotes)
	}
	if len(signals.Stale) != 1 || signals.Stale[0].Note != "Projects/Website.md" || signals.Stale[0].Open != 3 {
		t.Fatalf("stale = %+v, want only Website with 3 open items", signals.Stale)
	}
	var active []string
	for _, note := range signals.Active {
		active = append(active, note.Note)
	}
	if want := []string{"Inbox.md", "2026-10-20.md", "Week.md"}; !reflect.DeepEqual(active, want) {
		t.Fatalf("active = %v, want %v", active, want)
	}
	first := signals.OpenLoops[0]
	if first.Note != "Inbox.md" || first.Text != "call distributor" || first.Due != "2026-10-25" || !first.InActive || first.AgeDays != 0 {
		t.Fatalf("first loop = %+v", first)
	}
	last := signals.OpenLoops[len(signals.OpenLoops)-1]
	if last.Note != "Projects/Website.md" || last.AgeDays != 60 || last.InActive {
		t.Fatalf("last loop = %+v", last)
	}
}

func TestDerive_DecisionsCarryTheirOpenItems(t *testing.T) {
	now := extractDay
	signals := Derive([]NoteFacts{
		factsAt("Label/Distributor.md", "# Distributor\nDecide on the distributor by Oct 25\n- [ ] compare fees\n", now),
		factsAt("Inbox.md", "- [ ] other\n", now),
	}, now)
	if len(signals.Decisions) != 1 || signals.Decisions[0].Note != "Label/Distributor.md" || signals.Decisions[0].Open != 1 {
		t.Fatalf("decisions = %+v", signals.Decisions)
	}
	for _, loop := range signals.OpenLoops {
		if loop.InDecision != (loop.Note == "Label/Distributor.md") {
			t.Fatalf("loop %+v InDecision is wrong", loop)
		}
	}
}

func TestDerive_PeopleResolveToPersonNotesRankedByMentions(t *testing.T) {
	now := extractDay
	signals := Derive([]NoteFacts{
		factsAt("People/Sarah Kim.md", "# Sarah Kim\nSee [[Sarah Kim]] and [[Bo]].\n", now),
		factsAt("Contacts/Bo.md", "# Bo\n", now),
		factsAt("Friends/Ana.md", "---\ntags: [person]\n---\n# Ana\n", now),
		factsAt("Projects/Album.md", "With [[Sarah Kim|Sarah]], [[Bo]], [[Ana|A]] and [[Inbox]]. Again [[Sarah Kim]].\n", now),
		factsAt("Inbox.md", "Call [[People/Sarah Kim]] about [[Album]] and [[Nobody]].\n", now),
		factsAt("Projects/Ana.md", "# A project also called Ana\n", now),
	}, now)
	got := make([]string, 0, len(signals.People))
	for _, person := range signals.People {
		got = append(got, person.Name+":"+person.Note+":"+string(rune('0'+person.Mentions)))
	}
	want := []string{"Bo:Contacts/Bo.md:2", "Sarah Kim:People/Sarah Kim.md:2", "Ana:Friends/Ana.md:1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("people = %v, want %v", got, want)
	}
}

func TestExtract_ReadsEveryNoteInAReading(t *testing.T) {
	reading := Reading{Notes: []Note{
		{RelPath: "a.md", ModTime: extractDay, Content: []byte("- [ ] one\n")},
		{RelPath: "b.md", ModTime: extractDay, Content: []byte("- [ ] two\n- [ ] three\n")},
	}}
	signals := Extract(reading, extractDay)
	if signals.Notes != 2 || len(signals.OpenLoops) != 3 || signals.NotesWithOpen != 2 {
		t.Fatalf("signals = %+v", signals)
	}
}
