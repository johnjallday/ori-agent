// Package vaultfixture builds disposable notes folders for tests and demos: a
// music Obsidian vault, a software Obsidian vault, and a research folder kept
// in Logseq's plain-Markdown layout, plus Obsidian's own vault list naming
// them.
//
// Everything is written by hand and is deterministic for a given clock: the
// dates inside the notes and every file's modification time are fixed offsets
// from the now passed in, so "due in four days" and "untouched since June" stay
// true whenever the fixture is built. Values that look like credentials are
// assembled at run time from harmless parts and are not secrets.
package vaultfixture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Flavours.
const (
	Music    = "music"
	Software = "software"
	Research = "research"
)

// Flavours lists every flavour in build order.
var Flavours = []string{Music, Software, Research}

// Kinds, as the second-brain record names them.
const (
	KindObsidian = "obsidian"
	KindMarkdown = "markdown"
)

// Vault is one built notes folder and what a correct reader finds in it.
type Vault struct {
	Flavour string `json:"flavour"`
	Kind    string `json:"kind"`
	// Name is the folder's base name; Path its absolute, canonical path.
	Name string `json:"name"`
	Path string `json:"path"`
	// LastOpened is the "ts" the vault list gives it.
	LastOpened time.Time `json:"last_opened"`

	// What a reader that follows the second-brain rules finds.
	Notes         int    `json:"notes"`
	OpenItems     int    `json:"open_items"`
	NotesWithOpen int    `json:"notes_with_open"`
	Dated         int    `json:"dated"`
	Done          int    `json:"done"`
	DailyNotes    int    `json:"daily_notes"`
	Secrets       int    `json:"secrets"`
	Decision      string `json:"decision"`
	Stale         string `json:"stale"`
	TopPerson     string `json:"top_person"`
	TopMentions   int    `json:"top_mentions"`
}

// CredentialShapedValue returns text shaped like an access token. It is
// assembled from parts so no token-shaped literal is ever committed.
func CredentialShapedValue() string {
	return "gh" + "p_" + strings.Repeat("Vv7", 8)
}

// note is one file: its path below the vault, its age, and its text.
type note struct {
	rel  string
	age  time.Duration
	text string
}

const day = 24 * time.Hour

// Build writes one flavour's folder under dir, which must exist, and returns
// its canonical path and expected facts.
func Build(dir, flavour string, now time.Time) (Vault, error) {
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return Vault{}, fmt.Errorf("vaultfixture: resolve %s: %w", dir, err)
	}
	var vault Vault
	var notes []note
	switch flavour {
	case Music:
		vault, notes = music(now)
	case Software:
		vault, notes = software(now)
	case Research:
		vault, notes = research(now)
	default:
		return Vault{}, fmt.Errorf("vaultfixture: unknown flavour %q", flavour)
	}
	vault.Flavour = flavour
	vault.Path = filepath.Join(canonical, vault.Name)
	for _, n := range notes {
		if err := write(vault.Path, n.rel, n.text, now.Add(-n.age)); err != nil {
			return Vault{}, err
		}
	}
	return vault, nil
}

// BuildAll writes every flavour under dir. Their LastOpened times run from the
// first flavour (most recent) to the last.
func BuildAll(dir string, now time.Time) ([]Vault, error) {
	vaults := make([]Vault, 0, len(Flavours))
	for index, flavour := range Flavours {
		vault, err := Build(dir, flavour, now)
		if err != nil {
			return nil, err
		}
		vault.LastOpened = now.Add(-time.Duration(index+1) * time.Hour).Truncate(time.Millisecond)
		vaults = append(vaults, vault)
	}
	return vaults, nil
}

// WriteVaultList writes Obsidian's vault list at path in Obsidian's own shape,
// naming each Obsidian vault given. A plain Markdown folder is not an
// Obsidian vault and is left out, as Obsidian would.
func WriteVaultList(path string, vaults ...Vault) error {
	type entry struct {
		Path string `json:"path"`
		TS   int64  `json:"ts"`
		Open bool   `json:"open,omitempty"`
	}
	list := map[string]map[string]entry{"vaults": {}}
	for index, vault := range vaults {
		if vault.Kind != KindObsidian {
			continue
		}
		sum := sha256.Sum256([]byte(vault.Path))
		list["vaults"][hex.EncodeToString(sum[:8])] = entry{Path: vault.Path, TS: vault.LastOpened.UnixMilli(), Open: index == 0}
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func write(dir, rel, content string, modTime time.Time) error {
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		return fmt.Errorf("vaultfixture: write %s: %w", rel, err)
	}
	return os.Chtimes(full, modTime, modTime)
}

func iso(t time.Time) string { return t.Format("2006-01-02") }

// spoken is "Oct 25", the way people write a date in a sentence.
func spoken(t time.Time) string { return t.Format("Jan 2") }

func music(now time.Time) (Vault, []note) {
	vault := Vault{
		Kind: KindObsidian, Name: "Second Brain",
		Notes: 8, OpenItems: 12, NotesWithOpen: 6, Dated: 4, Done: 3, DailyNotes: 2,
		Decision: "Projects/Distribution Decision.md", Stale: "Projects/Studio Website.md",
		TopPerson: "Marco", TopMentions: 3,
	}
	return vault, []note{
		{rel: ".obsidian/app.json", text: "{\n  \"alwaysUpdateLinks\": true\n}\n"},
		{rel: ".obsidian/workspace.json", text: "{}\n"},
		{rel: ".trash/Old idea.md", text: "- [ ] this deleted note is never read\n"},
		{rel: "Attachments/cover.png", text: "not really a png\n"},
		{rel: "Projects/Album Release.md", age: day, text: "---\ntags: [project, music]\nstatus: active\n---\n" +
			"# Album Release\n\nRelease checklist for the new record.\n\n" +
			"- [x] Finish mixing tracks 1-6\n" +
			"- [ ] Master track 7 📅 " + iso(now.Add(3*day)) + "\n" +
			"- [ ] Send the artwork brief to [[Sarah Kim]] by " + spoken(now.Add(6*day)) + "\n" +
			"- [ ] Confirm the release date with [[Marco]]\n" +
			"- [ ] Upload stems for the remix contest\n"},
		{rel: "Projects/Distribution Decision.md", age: 2 * day, text: "---\nstatus: open\n---\n" +
			"# Distribution Decision\n\n" +
			"Decide by " + spoken(now.Add(4*day)) + " between DistroKid and TuneCore.\n" +
			"Deferred on " + iso(now.Add(-14*day)) + " and again on " + iso(now.Add(-7*day)) + ".\n\n" +
			"- [ ] Compare DistroKid and TuneCore fees 📅 " + iso(now.Add(4*day)) + "\n" +
			"- [ ] Ask [[Marco]] which one the label prefers\n"},
		{rel: "Projects/Studio Website.md", age: 120 * day, text: "---\nstatus: stalled\n---\n" +
			"# Studio Website\n\nLast touched in the summer.\n\n" +
			"- [ ] Fix the footer links\n- [ ] Add the tour dates page\n- [ ] Replace the bio photo\n"},
		{rel: "People/Sarah Kim.md", age: 30 * day, text: "---\ntags: [person]\n---\n# Sarah Kim\n\nDesigner for the album artwork.\n"},
		{rel: "People/Marco.md", age: 40 * day, text: "# Marco\n\nLabel manager.\n"},
		{rel: "Daily/" + iso(now.Add(-day)) + ".md", age: day, text: "- [ ] Call [[Marco]] about the release date 📅 " + iso(now.Add(2*day)) + "\n" +
			"- [x] Bounce track 6\n\nTalked to [[Sarah Kim]] about the cover.\n"},
		{rel: "Daily/" + iso(now) + ".md", age: 2 * time.Hour, text: "- [ ] Book the mastering session\n\nIdeas for the video.\n"},
		{rel: "Reading List.md", age: 10 * day, text: "# Reading List\n\n- [ ] Read Making Music by Dennis DeSantis\n- [x] Read All You Need to Know About the Music Business\n"},
	}
}

func software(now time.Time) (Vault, []note) {
	vault := Vault{
		Kind: KindObsidian, Name: "Engineering Notes",
		Notes: 7, OpenItems: 9, NotesWithOpen: 5, Dated: 3, Done: 3, DailyNotes: 1, Secrets: 1,
		Decision: "Projects/API Migration.md", Stale: "Ideas/Rewrite the CLI.md",
		TopPerson: "Priya Shah", TopMentions: 2,
	}
	return vault, []note{
		{rel: ".obsidian/app.json", text: "{}\n"},
		{rel: ".git/HEAD", text: "ref: refs/heads/main\n"},
		{rel: "Projects/API Migration.md", age: 3 * time.Hour, text: "# API Migration\n\n" +
			"Need to decide: Postgres or SQLite for the cache.\n\n" +
			"- [x] Inventory the endpoints\n" +
			"- [ ] Move auth to the new gateway 📅 " + iso(now.Add(5*day)) + "\n" +
			"- [ ] Write the rollback plan with [[Priya Shah]]\n" +
			"- [ ] Rotate the deploy token " + CredentialShapedValue() + "\n" +
			"```sh\n- [ ] not a task, a shell comment\n```\n"},
		{rel: "Projects/Mobile App.md", age: 2 * day, text: "---\ntags: [project]\n---\n# Mobile App\n\n" +
			"1. [ ] Fix the login crash on Android due: " + iso(now.Add(2*day)) + "\n" +
			"2. [ ] Ship the dark mode toggle\n3. [x] Upgrade the build tools\n"},
		{rel: "Ideas/Rewrite the CLI.md", age: 90 * day, text: "# Rewrite the CLI\n\n" +
			"- TODO Prototype the argument parser\n- LATER Benchmark against the old binary\n"},
		{rel: "People/Priya Shah.md", age: 20 * day, text: "# Priya Shah\n\nPlatform lead.\n"},
		{rel: "People/Tom.md", age: 25 * day, text: "# Tom\n\nOn call this month.\n"},
		{rel: "Daily/" + iso(now) + ".md", age: time.Hour, text: "Sync with [[Priya Shah]] and [[Tom]].\n\n" +
			"- [ ] Review the incident report by " + spoken(now.Add(day)) + "\n- [x] Merge the hotfix\n"},
		{rel: "Inbox.md", age: 4 * day, text: "# Inbox\n\n* [ ] Try the new profiler\n* [ ] Answer the hiring email\n"},
	}
}

// research is a Logseq graph: plain Markdown with no .obsidian, pages/ and
// journals/YYYY_MM_DD.md, TODO/LATER/DOING/DONE markers and key:: properties.
func research(now time.Time) (Vault, []note) {
	journal := func(t time.Time) string { return "journals/" + t.Format("2006_01_02") + ".md" }
	vault := Vault{
		Kind: KindMarkdown, Name: "Thesis Notes",
		Notes: 6, OpenItems: 8, NotesWithOpen: 4, Dated: 1, Done: 2, DailyNotes: 2,
		Decision: "pages/Chapter 3.md", Stale: "pages/Grant Application.md",
		TopPerson: "Dr. Lee", TopMentions: 2,
	}
	return vault, []note{
		{rel: "logseq/config.edn", text: "{:preferred-format :markdown}\n"},
		{rel: "pages/Literature Review.md", age: day, text: "title:: Literature Review\ntags:: research\n\n" +
			"- TODO Summarise the Smith 2024 paper\n" +
			"- LATER Find the replication dataset\n" +
			"- DONE Read Jones 2023\n" +
			"- TODO Email [[Dr. Lee]] about the chapter 3 draft by " + spoken(now.Add(9*day)) + "\n"},
		{rel: "pages/Chapter 3.md", age: 2 * day, text: "status:: drafting\n\n" +
			"- Decide whether to drop the second experiment\n" +
			"- DOING Rewrite the methods section\n" +
			"- TODO Redo figure 4\n"},
		{rel: "pages/Dr. Lee.md", age: 50 * day, text: "tags:: person\n\n- Advisor, office hours on Thursdays\n"},
		{rel: "pages/Grant Application.md", age: 75 * day, text: "- TODO Draft the budget\n- TODO Get two letters of support\n- DONE Pick the funder\n"},
		{rel: journal(now.Add(-day)), age: day, text: "- TODO Back up the lab data\n- Met [[Dr. Lee]] about the outline\n"},
		{rel: journal(now), age: 3 * time.Hour, text: "- Reading day, nothing due\n"},
	}
}
