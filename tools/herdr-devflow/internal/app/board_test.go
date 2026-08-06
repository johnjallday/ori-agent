package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Shared fixtures for the two board-reading commands. They read the same board
// through the same machinery and differ only in which column they take, so the
// setup lives once and each test file states only what its own column claims.

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

// mixedBoard holds both columns at once, which is the shape that actually
// catches a leak: every column assertion in either file is made against a board
// where the other column is also populated.
//
// Ready: #292 (rank 1), a draft (rank 2), #297 (unranked).
// Backlog: #290 (rank 1), #293 (unranked).
const mixedBoard = `{"items":[
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
	            "url":"https://github.com/johnjallday/ori-agent/issues/293"}},
	{"title":"Imported Workspace does not recognize HQ","status":"Backlog","size":"S","rank":1,
	 "why":"blocks importing at all",
	 "content":{"number":290,"title":"Imported Workspace does not recognize HQ","type":"Issue",
	            "url":"https://github.com/johnjallday/ori-agent/issues/290"}}
],"totalCount":5}`

// newBoardApp builds an App wired to a fake GitHub, returning the argument base
// for one subcommand. The subcommand is a parameter because the two commands
// share every other part of this setup.
func newBoardApp(
	t *testing.T, remote *fakeBoard, subcommand string,
) (*App, *bytes.Buffer, *bytes.Buffer, []string) {
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
	base := []string{
		"--repo-root", repo,
		"--home", filepath.Join(t.TempDir(), "runtime"),
		subcommand,
	}
	return application, stdout, stderr, base
}

// renderedCards returns the card labels one listing actually rendered, read
// from the label column of each row rather than by searching the whole output.
//
// A substring search cannot do this job: the draft card's title is "Workspace
// lifecycle (consolidates #290, #289)", so looking for "#290" anywhere in the
// text finds it inside another card's title and reports a leak that is not
// there. Only the label column says which card a row is.
func renderedCards(output string) map[string]bool {
	cards := make(map[string]bool)
	for line := range strings.SplitSeq(output, "\n") {
		// Continuation lines (the dimmed justification) are indented; header
		// and footer lines have no rank column. Only card rows start flush
		// left with a rank followed by a label.
		if line == "" || strings.HasPrefix(line, " ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if label := fields[1]; strings.HasPrefix(label, "#") || label == "[draft]" {
			cards[label] = true
		}
	}
	return cards
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
