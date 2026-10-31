package secondbrain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readTestFile(t *testing.T, parts ...string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(parts...)) // #nosec G304 -- the test's own temp tree
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCreate_MakesTheLayoutWithAReadmeInEachFolder(t *testing.T) {
	parent := canonicalTemp(t)
	creation, err := Creator{}.Create(parent, DefaultFolderName, Seeds{})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, DefaultFolderName)
	if creation.Path != root || creation.FollowUps != 0 || creation.People != 0 {
		t.Fatalf("creation = %+v", creation)
	}
	for _, entry := range createdLayout {
		readme := readTestFile(t, root, entry.folder, "README.md")
		if !strings.Contains(readme, "Ori") {
			t.Errorf("%s/README.md does not say whether Ori writes there: %q", entry.folder, readme)
		}
	}
	rootReadmeText := readTestFile(t, root, "README.md")
	if !strings.HasPrefix(rootReadmeText, OwnedFoldersSentence+"\n") {
		t.Fatalf("the README does not open with the ownership sentence: %q", rootReadmeText)
	}
	if strings.Contains(rootReadmeText, "What Ori knows about you") {
		t.Fatal("an empty seed still wrote the knowledge section")
	}
	var app map[string]any
	if err := json.Unmarshal([]byte(readTestFile(t, root, ".obsidian", "app.json")), &app); err != nil {
		t.Fatalf("app.json: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".obsidian"))
	if err != nil || len(entries) != 1 {
		t.Fatalf(".obsidian holds %v, %v; want app.json alone", entries, err)
	}
	if !IsObsidianVault(root) {
		t.Fatal("the folder is not recognised as a vault")
	}
	if !HasNotes(root) {
		t.Fatal("the folder is not recognised as holding notes")
	}
}

func TestCreate_TargetMustBeMissingOrEmpty(t *testing.T) {
	t.Run("an empty folder is used", func(t *testing.T) {
		parent := canonicalTemp(t)
		if err := os.Mkdir(filepath.Join(parent, "Notes"), 0o750); err != nil {
			t.Fatal(err)
		}
		if _, err := (Creator{}).Create(parent, "Notes", Seeds{}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a folder holding only .DS_Store is used", func(t *testing.T) {
		parent := canonicalTemp(t)
		writeNote(t, parent, "Notes/.DS_Store", "finder")
		if _, err := (Creator{}).Create(parent, "Notes", Seeds{}); err != nil {
			t.Fatal(err)
		}
	})
	for name, arrange := range map[string]func(t *testing.T, parent string){
		"a folder with a note":   func(t *testing.T, parent string) { writeNote(t, parent, "Notes/mine.md", "mine") },
		"a folder with a hidden": func(t *testing.T, parent string) { writeNote(t, parent, "Notes/.git/HEAD", "x") },
		"a file":                 func(t *testing.T, parent string) { writeNote(t, parent, "Notes", "a file") },
		"a link to an empty folder": func(t *testing.T, parent string) {
			if err := os.Mkdir(filepath.Join(parent, "elsewhere"), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(parent, "elsewhere"), filepath.Join(parent, "Notes")); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			parent := canonicalTemp(t)
			arrange(t, parent)
			before := snapshot(t, parent)
			if _, err := (Creator{}).Create(parent, "Notes", Seeds{}); !errors.Is(err, ErrTargetNotEmpty) {
				t.Fatalf("err = %v, want ErrTargetNotEmpty", err)
			}
			if after := snapshot(t, parent); after != before {
				t.Fatalf("a refusal changed the tree.\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}

func TestCreate_RefusesANameThatIsNotOneFolderName(t *testing.T) {
	parent := canonicalTemp(t)
	before := snapshot(t, parent)
	for _, name := range []string{"", " ", ".", "..", "../up", "a/b", `a\b`, "/abs", ".hidden", "trailing.", " padded ", "a\nb", "a:b", strings.Repeat("x", 256)} {
		if _, err := (Creator{}).Create(parent, name, Seeds{}); !errors.Is(err, ErrInvalidFolderName) {
			t.Errorf("name %q: err = %v", name, err)
		}
	}
	if after := snapshot(t, parent); after != before {
		t.Fatalf("a refusal changed the tree.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestCreate_RollsBackOnlyWhatItMade(t *testing.T) {
	seeds := Seeds{
		FollowUps: []SeedFollowUp{{Title: "one"}, {Title: "two"}},
		People:    []SeedPerson{{Name: "Sam"}},
	}
	failing := func(at string) Creator {
		return Creator{beforeWrite: func(rel string) error {
			if rel == at {
				return errors.New("disk full")
			}
			return nil
		}}
	}
	for _, at := range []string{".obsidian", "Daily/README.md", "README.md", "Inbox/two.md", "People/Sam.md"} {
		t.Run("the target it made is removed, failing at "+at, func(t *testing.T) {
			parent := canonicalTemp(t)
			writeNote(t, parent, "Beside.md", "not part of it")
			// The parent's own line goes: adding and removing a child moves its mtime.
			before := withoutPath(snapshot(t, parent), ".")
			if _, err := failing(at).Create(parent, "Notes", seeds); err == nil {
				t.Fatal("the failure was not reported")
			}
			if after := withoutPath(snapshot(t, parent), "."); after != before {
				t.Fatalf("the rollback left something.\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
	t.Run("an empty folder the user made stays, empty", func(t *testing.T) {
		parent := canonicalTemp(t)
		if err := os.Mkdir(filepath.Join(parent, "Notes"), 0o750); err != nil {
			t.Fatal(err)
		}
		if _, err := failing("Inbox/two.md").Create(parent, "Notes", seeds); err == nil {
			t.Fatal("the failure was not reported")
		}
		entries, err := os.ReadDir(filepath.Join(parent, "Notes"))
		if err != nil || len(entries) != 0 {
			t.Fatalf("Notes holds %v, %v; want it empty", entries, err)
		}
	})
}

func TestCreate_SeedsFollowUpsThatReadBackAsOpenItems(t *testing.T) {
	parent := canonicalTemp(t)
	creation, err := Creator{}.Create(parent, "Notes", Seeds{FollowUps: []SeedFollowUp{
		{Title: "Call the distributor", Due: "2026-11-05", Source: "Email from Dana"},
		{Title: "Renew the lease"},
		{Title: "Renew  the lease!"},
		{Title: "Bad date", Due: "soon"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if creation.FollowUps != 4 {
		t.Fatalf("follow-ups = %d", creation.FollowUps)
	}
	root := filepath.Join(parent, "Notes")
	note := readTestFile(t, root, "Inbox", "call-the-distributor.md")
	if mark := ReadMark([]byte(note)); mark.Kind != NoteKindSeed || !mark.OwnedByOri {
		t.Fatalf("mark = %+v", mark)
	}
	for _, want := range []string{"# Call the distributor", "- [ ] Call the distributor 📅 2026-11-05", "From: Email from Dana"} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q:\n%s", want, note)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "Inbox", "renew-the-lease-2.md")); err != nil {
		t.Fatalf("a repeated slug was not numbered: %v", err)
	}

	reading, err := Read(t.Context(), root, ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	signals := Extract(reading, time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC))
	if len(signals.OpenLoops) != 4 {
		t.Fatalf("read back %d open items: %+v", len(signals.OpenLoops), signals.OpenLoops)
	}
	var found *OpenLoop
	for index := range signals.OpenLoops {
		if signals.OpenLoops[index].Note == "Inbox/call-the-distributor.md" {
			found = &signals.OpenLoops[index]
		}
	}
	if found == nil || found.Text != "Call the distributor" || found.Due != "2026-11-05" {
		t.Fatalf("round trip = %+v", found)
	}
}

func TestCreate_SeedsPeopleAndWhatOriKnows(t *testing.T) {
	parent := canonicalTemp(t)
	creation, err := Creator{}.Create(parent, "Notes", Seeds{
		Facts:            []string{"You run a small label.", "You work\nmornings."},
		WorkingAgreement: "Ask before sending anything.",
		People:           []SeedPerson{{Name: "Dana Lee", How: "Your distributor"}, {Name: "../../Evil: Name", How: ""}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if creation.People != 2 {
		t.Fatalf("people = %d", creation.People)
	}
	root := filepath.Join(parent, "Notes")
	readme := readTestFile(t, root, "README.md")
	for _, want := range []string{"## What Ori knows about you", "- You run a small label.", "- You work mornings.", "How you work with Ori: Ask before sending anything."} {
		if !strings.Contains(readme, want) {
			t.Errorf("README lacks %q:\n%s", want, readme)
		}
	}
	dana := readTestFile(t, root, "People", "Dana Lee.md")
	if !strings.Contains(dana, "# Dana Lee") || !strings.Contains(dana, "Your distributor") {
		t.Fatalf("person note = %q", dana)
	}
	entries, err := os.ReadDir(filepath.Join(root, "People"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 { // README, Dana Lee, the cleaned name
		t.Fatalf("People holds %d entries", len(entries))
	}
	if _, err := os.Lstat(filepath.Join(parent, "Evil")); err == nil {
		t.Fatal("a person's name climbed out of the folder")
	}
}

func TestCreate_EverySeededLinePassesTheSecretFilter(t *testing.T) {
	const secret = "sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789"
	parent := canonicalTemp(t)
	creation, err := Creator{}.Create(parent, "Notes", Seeds{
		FollowUps: []SeedFollowUp{
			{Title: "Use key " + secret},
			{Title: "Clean title", Source: "token " + secret},
		},
		Facts:            []string{"Your key is " + secret, "You keep a clean fact."},
		WorkingAgreement: "password: " + secret,
		People:           []SeedPerson{{Name: secret}, {Name: "Sam", How: "key " + secret}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if creation.FollowUps != 1 || creation.People != 1 {
		t.Fatalf("creation = %+v", creation)
	}
	root := filepath.Join(parent, "Notes")
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if strings.Contains(readTestFile(t, path), secret) || strings.Contains(entry.Name(), secret) {
			t.Errorf("the secret reached %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readTestFile(t, root, "README.md"), "You keep a clean fact.") {
		t.Fatal("a clean fact was dropped with the secret one")
	}
}

func TestCreate_SeedingIsBounded(t *testing.T) {
	seeds := Seeds{}
	for index := range MaxSeedFollowUps + 25 {
		seeds.FollowUps = append(seeds.FollowUps, SeedFollowUp{Title: fmt.Sprintf("Task %d", index)})
	}
	for index := range MaxSeedPeople + 25 {
		seeds.People = append(seeds.People, SeedPerson{Name: fmt.Sprintf("Person %d", index)})
	}
	creation, err := Creator{}.Create(canonicalTemp(t), "Notes", seeds)
	if err != nil {
		t.Fatal(err)
	}
	if creation.FollowUps != MaxSeedFollowUps || creation.People != MaxSeedPeople {
		t.Fatalf("creation = %+v", creation)
	}
}

func TestSetupHeadline_ReportsWhatWasSeededAndIsHonestWhenNothingWas(t *testing.T) {
	for counts, want := range map[SetupCounts]string{
		{FollowUps: 3, People: 2}: "Your second brain is set up: 3 follow-ups in Inbox, 2 people.",
		{FollowUps: 1, People: 1}: "Your second brain is set up: 1 follow-up in Inbox, 1 person.",
		{FollowUps: 4}:            "Your second brain is set up: 4 follow-ups in Inbox.",
		{People: 5}:               "Your second brain is set up: 5 people.",
		{}:                        "Your second brain is ready. Ask me to note something and it lands in Inbox.",
	} {
		if got := SetupHeadline(counts); got != want {
			t.Errorf("SetupHeadline(%+v) = %q, want %q", counts, got, want)
		}
	}
}

func TestRunSetup_ReportsTheSeedAndNeverSuggestsSeededItems(t *testing.T) {
	ctx := context.Background()
	parent := canonicalTemp(t)
	creation, err := Creator{}.Create(parent, "Second Brain", Seeds{
		FollowUps: []SeedFollowUp{{Title: "Call the distributor", Due: "2026-11-05"}, {Title: "Renew the lease"}},
		People:    []SeedPerson{{Name: "Dana Lee", How: "Dana Lee is your distributor."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// A note the user wrote after setup is not seeded.
	writeNote(t, creation.Path, "Inbox/mine.md", "- [ ] buy strings\n")
	source := createdSource(creation.Path)
	setup := SetupCounts{FollowUps: creation.FollowUps, People: creation.People}

	run := fixedLooker().RunSetup(ctx, source, "look-1", "create-1", setup)
	look := run.Look
	if look.State != LookFinished || look.HeadlineBy != HeadlineByRules ||
		look.Headline != "Your second brain is set up: 2 follow-ups in Inbox, 1 person." {
		t.Fatalf("look = %+v", look)
	}
	var seeded, suggested []LookItem
	for _, item := range look.Items {
		if item.Seeded {
			seeded = append(seeded, item)
		}
		if item.Suggested {
			suggested = append(suggested, item)
		}
	}
	if len(look.Items) != 3 || len(seeded) != 2 {
		t.Fatalf("items = %+v", look.Items)
	}
	if len(suggested) != 1 || suggested[0].Text != "buy strings" {
		t.Fatalf("suggested = %+v; only the user's own note may be suggested", suggested)
	}
	record := Record{Sources: []Source{source}}
	record.Sources[0].LastLook = &look
	if err := record.Validate(); err != nil {
		t.Fatalf("the look does not fit the record: %v", err)
	}

	// The same notes in a connected folder are the user's: nothing is seeded.
	connected := source
	connected.Origin = OriginConnected
	for _, item := range fixedLooker().Run(ctx, connected, nil, "look-2", "").Look.Items {
		if item.Seeded {
			t.Fatalf("a connected folder's item was marked seeded: %+v", item)
		}
	}

	// Accepting every item adds only the user's own.
	service := newFollowUps(t)
	created, skipped, err := Accept(ctx, service, Acceptance{UserID: "local", WorkspaceID: "hq", Source: source, Items: look.Items, Location: time.UTC})
	if err != nil || created != 1 || skipped != 2 {
		t.Fatalf("accept = %d created %d skipped, %v", created, skipped, err)
	}
}

func TestCreation_UndoRemovesOnlyWhatItMade(t *testing.T) {
	parent := canonicalTemp(t)
	writeNote(t, parent, "Beside.md", "keep")
	before := withoutPath(snapshot(t, parent), ".")
	creation, err := Creator{}.Create(parent, "Notes", Seeds{FollowUps: []SeedFollowUp{{Title: "One"}}})
	if err != nil {
		t.Fatal(err)
	}
	creation.Undo()
	if after := withoutPath(snapshot(t, parent), "."); after != before {
		t.Fatalf("undo left something.\nbefore:\n%s\nafter:\n%s", before, after)
	}

	// A note the user added in the meantime keeps its folder.
	creation, err = Creator{}.Create(parent, "Notes", Seeds{})
	if err != nil {
		t.Fatal(err)
	}
	writeNote(t, parent, "Notes/Inbox/mine.md", "mine")
	creation.Undo()
	if got := readTestFile(t, parent, "Notes", "Inbox", "mine.md"); got != "mine" {
		t.Fatalf("the user's note = %q", got)
	}
}

func TestPersonFromSentence_NamesThePersonAnEntryIsAbout(t *testing.T) {
	for sentence, want := range map[string]string{
		"Dana Lee is my distributor.":                "Dana Lee",
		"My distributor is Dana Lee.":                "Dana Lee",
		"Sam's birthday is in March":                 "Sam",
		"Your manager, Priya Raman, prefers email.":  "Priya Raman",
		"Talk to Alex Jordan Smith Jr about mixing.": "Alex Jordan Smith",
		"The label is run by Mo.":                    "Mo",
	} {
		person, ok := PersonFromSentence(sentence)
		if !ok || person.Name != want || person.How != sentence {
			t.Errorf("PersonFromSentence(%q) = %+v, %v; want name %q", sentence, person, ok, want)
		}
	}
	for _, sentence := range []string{"", "my distributor prefers email", "I like short replies.", "The  \n"} {
		if person, ok := PersonFromSentence(sentence); ok {
			t.Errorf("PersonFromSentence(%q) = %+v; want nobody", sentence, person)
		}
	}
}

func TestCreate_ASeededFolderCanStillBeWrittenByTheWriter(t *testing.T) {
	parent := canonicalTemp(t)
	creation, err := Creator{}.Create(parent, "Notes", Seeds{FollowUps: []SeedFollowUp{{Title: "One"}}})
	if err != nil {
		t.Fatal(err)
	}
	writer := newWriterFor(t, creation.Path)
	if _, err := writer.Write("Inbox/README.md", NoteKindCapture, []byte("x")); !errors.Is(err, ErrUserModified) {
		t.Fatalf("Ori overwrote the folder's README: err = %v", err)
	}
	if _, err := writer.Write("Inbox/one.md", NoteKindCapture, []byte("replaced\n")); err != nil {
		t.Fatalf("an intact seeded note: %v", err)
	}
}
