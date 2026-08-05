package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBoard answers the three `gh` invocations one board read makes —
// repository resolution, the linked-project query, and the item listing — from
// fixtures keyed by subcommand, so a test states only what it cares about.
type fakeBoard struct {
	projects string
	items    string
	failure  error
	calls    [][]string
}

func (f *fakeBoard) run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	if f.failure != nil {
		return nil, f.failure
	}
	joined := strings.Join(args, " ")
	switch {
	case strings.HasPrefix(joined, "repo view"):
		return []byte(`{"name":"ori-agent","owner":{"login":"johnjallday"}}`), nil
	case strings.HasPrefix(joined, "api graphql"):
		if f.projects == "" {
			return []byte(defaultLinkedProject), nil
		}
		return []byte(f.projects), nil
	default:
		if f.items == "" {
			return []byte(`{"items":[],"totalCount":0}`), nil
		}
		return []byte(f.items), nil
	}
}

const defaultLinkedProject = `{"data":{"repository":{"projectsV2":{"nodes":[
	{"number":3,"title":"Ori Dev","url":"https://github.com/users/johnjallday/projects/3",
	 "owner":{"__typename":"User","login":"johnjallday"}}]}}}}`

const readyBoard = `{"items":[
	{"title":"Delete button","status":"Ready",
	 "why":"no rank yet",
	 "content":{"number":297,"title":"Delete button","type":"Issue",
	            "url":"https://github.com/johnjallday/ori-agent/issues/297"}},
	{"title":"Coordinate based map","status":"Ready","size":"M","rank":1,
	 "why":"builds on the map work that just shipped",
	 "content":{"number":292,"title":"Coordinate based map","type":"Issue",
	            "url":"https://github.com/johnjallday/ori-agent/issues/292"}},
	{"title":"Workspace lifecycle (consolidates #290, #289)","status":"Ready","rank":2,
	 "content":{"title":"Workspace lifecycle (consolidates #290, #289)","type":"DraftIssue"}},
	{"title":"Skin Makeover","status":"Backlog",
	 "content":{"number":293,"title":"Skin Makeover","type":"Issue",
	            "url":"https://github.com/johnjallday/ori-agent/issues/293"}}
],"totalCount":4}`

func newBoardApp(t *testing.T, remote *fakeBoard) (*App, *bytes.Buffer, *bytes.Buffer, []string) {
	t.Helper()
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	application := New(Dependencies{
		Stdout:       stdout,
		Stderr:       stderr,
		Getwd:        func() (string, error) { return repo, nil },
		LookupEnv:    func(string) (string, bool) { return "", false },
		GitHubRunner: remote.run,
	})
	base := []string{"--repo-root", repo, "--home", filepath.Join(t.TempDir(), "runtime"), "backlog"}
	return application, stdout, stderr, base
}

func TestBacklogShowsOnlyTheReadyColumnInRankOrder(t *testing.T) {
	t.Parallel()
	application, stdout, stderr, base := newBoardApp(t, &fakeBoard{items: readyBoard})

	if exit := application.Run(context.Background(), base); exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr.String())
	}

	output := stdout.String()
	if strings.Contains(output, "Skin Makeover") {
		t.Fatalf("a Backlog card leaked into the Ready listing: %q", output)
	}
	if !strings.Contains(output, "3 ready") {
		t.Fatalf("listing = %q, want the Ready count", output)
	}
	// The whole point of the command is the order, so it is asserted as an
	// order rather than as three independent presence checks.
	ranked := indexOfAll(output, "#292", "[draft]", "#297")
	if !ascending(ranked) {
		t.Fatalf("listing = %q, want rank 1, then rank 2, then the unranked card last", output)
	}
	for _, want := range []string{"[M]", "builds on the map work that just shipped", "Ori Dev"} {
		if !strings.Contains(output, want) {
			t.Fatalf("listing = %q, want it to contain %q", output, want)
		}
	}
}

// TestBacklogRendersAnUnrankedCardAsADashNotAZero guards a small lie: 0 reads
// like a rank somebody chose, when it means nobody has placed the card yet.
func TestBacklogRendersAnUnrankedCardAsADashNotAZero(t *testing.T) {
	t.Parallel()
	application, stdout, _, base := newBoardApp(t, &fakeBoard{items: readyBoard})

	if exit := application.Run(context.Background(), base); exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	for _, line := range strings.Split(stdout.String(), "\n") {
		if strings.Contains(line, "#297") {
			if !strings.HasPrefix(strings.TrimSpace(line), "-") {
				t.Fatalf("unranked row = %q, want it to lead with a dash", line)
			}
			if strings.Contains(line, "0 ") {
				t.Fatalf("unranked row = %q, want no invented rank", line)
			}
		}
	}
}

func TestBacklogSaysNothingIsGroomedWithoutSoundingBroken(t *testing.T) {
	t.Parallel()
	application, stdout, stderr, base := newBoardApp(t, &fakeBoard{
		items: `{"items":[{"title":"Skin Makeover","status":"Backlog",
			"content":{"number":293,"type":"Issue","url":"u"}}],"totalCount":1}`,
	})

	// A board with nothing groomed is a successful answer, so it exits 0 — a
	// script must not treat "nothing ready today" as a failure.
	if exit := application.Run(context.Background(), base); exit != 0 {
		t.Fatalf("exit = %d, want 0 for an empty Ready column; stderr=%q", exit, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "nothing in Ready") {
		t.Fatalf("listing = %q, want it to say the board was read and nothing is groomed", output)
	}
	// The reader is told where the ungroomed captures are, so an empty column is
	// a signpost rather than a dead end.
	if !strings.Contains(output, "./scripts/issue.sh") {
		t.Fatalf("listing = %q, want it to point at the Issue list", output)
	}
}

func TestBacklogJSONIsItsOwnVersionedEnvelope(t *testing.T) {
	t.Parallel()
	application, stdout, stderr, base := newBoardApp(t, &fakeBoard{items: readyBoard})

	if exit := application.Run(context.Background(), append(base, "--json")); exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr.String())
	}

	var payload backlogPayload
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout = %q is not the envelope: %v", stdout.String(), err)
	}
	if payload.SchemaVersion != backlogSchemaVersion || payload.Repository != "johnjallday/ori-agent" {
		t.Fatalf("payload = %+v, want the schema-versioned envelope", payload)
	}
	if payload.Project.Number != 3 || payload.Project.Title != "Ori Dev" || payload.Project.URL == "" {
		t.Fatalf("project = %+v, want the board it actually read named", payload.Project)
	}
	if payload.ObservedAt == "" || !payload.Complete {
		t.Fatalf("payload = %+v, want an observation time and a complete listing", payload)
	}
	if len(payload.Items) != 3 {
		t.Fatalf("items = %d, want only the Ready column", len(payload.Items))
	}

	// An absent rank is encoded as absent. Zero is a rank somebody could have
	// chosen, so a consumer must be able to tell the two apart.
	last := payload.Items[2]
	if last.Rank != nil {
		t.Fatalf("unranked item carried rank %v", *last.Rank)
	}
	if first := payload.Items[0]; first.Rank == nil || *first.Rank != 1 {
		t.Fatalf("first item = %+v, want rank 1", first)
	}
	// A draft has no Issue behind it, so it carries no number to go looking for.
	draft := payload.Items[1]
	if !draft.IsDraft || draft.Number != nil {
		t.Fatalf("draft = %+v, want is_draft with no Issue number", draft)
	}
	// Human prose belongs to the human surface, not to the machine one.
	if strings.Contains(stdout.String(), "Ori backlog") {
		t.Fatalf("JSON output contained the human header: %s", stdout.String())
	}
}

func TestBacklogJSONEncodesAnEmptyColumnAsAnArray(t *testing.T) {
	t.Parallel()
	application, stdout, _, base := newBoardApp(t, &fakeBoard{})

	if exit := application.Run(context.Background(), append(base, "--json")); exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	// A consumer iterating a JSON array must not have to special-case null for
	// the most ordinary state this command has.
	if !strings.Contains(stdout.String(), `"items": []`) &&
		!strings.Contains(stdout.String(), `"items":[]`) {
		t.Fatalf("empty board = %s, want an empty items array", stdout.String())
	}
}

func TestBacklogOutputIsPlainTextWhenItIsNotATerminal(t *testing.T) {
	t.Parallel()
	application, stdout, _, base := newBoardApp(t, &fakeBoard{items: readyBoard})

	if exit := application.Run(context.Background(), base); exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if strings.ContainsRune(stdout.String(), '\x1b') {
		t.Fatalf("redirected output carried escape sequences: %q", stdout.String())
	}
}

func TestBacklogFailsLoudlyRatherThanShowingAnEmptyBoard(t *testing.T) {
	t.Parallel()
	application, stdout, stderr, base := newBoardApp(t, &fakeBoard{failure: errNoNetwork{}})

	exit := application.Run(context.Background(), base)

	if exit != 1 {
		t.Fatalf("exit = %d, want 1 when GitHub cannot answer", exit)
	}
	// The dangerous failure mode is a reachable-looking empty backlog, because
	// "nothing to do today" is a plausible answer that happens to be false.
	if strings.Contains(stdout.String(), "nothing in Ready") {
		t.Fatalf("a failed read was rendered as an empty column: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Recovery:") {
		t.Fatalf("stderr = %q, want a recovery action", stderr.String())
	}
}

// TestBacklogRefusesWhenSeveralBoardsAreLinked pins the behaviour at the
// repository level: no guess, and both candidates named.
func TestBacklogRefusesWhenSeveralBoardsAreLinked(t *testing.T) {
	t.Parallel()
	application, stdout, stderr, base := newBoardApp(t, &fakeBoard{
		projects: `{"data":{"repository":{"projectsV2":{"nodes":[
			{"number":3,"title":"Ori Dev","url":"a","owner":{"login":"johnjallday"}},
			{"number":4,"title":"Ori Dev","url":"b","owner":{"login":"johnjallday"}}]}}}}`,
	})

	if exit := application.Run(context.Background(), base); exit != 1 {
		t.Fatalf("exit = %d, want 1 when the board is ambiguous", exit)
	}
	if stdout.Len() != 0 {
		t.Fatalf("an ambiguous board still printed a listing: %q", stdout.String())
	}
	for _, want := range []string{"#3", "#4", "unlink"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, want it to contain %q", stderr.String(), want)
		}
	}
}

func indexOfAll(haystack string, needles ...string) []int {
	found := make([]int, 0, len(needles))
	for _, needle := range needles {
		found = append(found, strings.Index(haystack, needle))
	}
	return found
}

func ascending(values []int) bool {
	for index := 1; index < len(values); index++ {
		if values[index-1] < 0 || values[index] <= values[index-1] {
			return false
		}
	}
	return len(values) > 0 && values[0] >= 0
}
