package github

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

var testRepository = Repository{Owner: "johnjallday", Name: "ori-agent"}

// projectClient answers every `gh` invocation from a fixture keyed by the
// subcommand, so one test can supply both the linked-project query and the
// item listing without caring which order they are called in.
func projectClient(t *testing.T, answers map[string]string, failure error) (*Client, *[][]string) {
	t.Helper()
	calls := &[][]string{}
	client := New(Options{Run: func(_ context.Context, args ...string) ([]byte, error) {
		*calls = append(*calls, args)
		if failure != nil {
			return nil, failure
		}
		for prefix, answer := range answers {
			if strings.HasPrefix(strings.Join(args, " "), prefix) {
				return []byte(answer), nil
			}
		}
		t.Fatalf("unexpected gh invocation: %v", args)
		return nil, nil
	}})
	return client, calls
}

func linkedProjects(nodes string) string {
	return `{"data":{"repository":{"projectsV2":{"nodes":[` + nodes + `]}}}}`
}

const oneLinkedProject = `{"number":3,"title":"Ori Dev",
	"url":"https://github.com/users/johnjallday/projects/3",
	"owner":{"__typename":"User","login":"johnjallday"}}`

func TestResolveLinkedProjectUsesTheOnlyLinkedBoard(t *testing.T) {
	t.Parallel()
	client, calls := projectClient(t, map[string]string{
		"api graphql": linkedProjects(oneLinkedProject),
	}, nil)

	project, err := client.ResolveLinkedProject(context.Background(), testRepository)
	if err != nil {
		t.Fatalf("ResolveLinkedProject: %v", err)
	}
	if project.Number != 3 || project.Title != "Ori Dev" || project.Owner != "johnjallday" {
		t.Fatalf("project = %+v, want the linked board", project)
	}
	// The repository is passed as GraphQL variables rather than interpolated
	// into the query text, so a repository name can never be read as syntax.
	joined := strings.Join((*calls)[0], " ")
	if !strings.Contains(joined, "owner=johnjallday") || !strings.Contains(joined, "name=ori-agent") {
		t.Fatalf("query args = %v, want the repository passed as variables", (*calls)[0])
	}
}

// TestResolveLinkedProjectRefusesToGuessBetweenBoards is the whole reason this
// resolution exists: reading the wrong backlog silently is worse than not
// reading one at all, so several linked boards must fail rather than pick.
func TestResolveLinkedProjectRefusesToGuessBetweenBoards(t *testing.T) {
	t.Parallel()
	second := `{"number":4,"title":"Ori Dev",
		"url":"https://github.com/users/johnjallday/projects/4",
		"owner":{"__typename":"User","login":"johnjallday"}}`
	client, _ := projectClient(t, map[string]string{
		"api graphql": linkedProjects(oneLinkedProject + "," + second),
	}, nil)

	_, err := client.ResolveLinkedProject(context.Background(), testRepository)
	if err == nil {
		t.Fatal("two linked boards were resolved to one without complaint")
	}
	var remoteErr *Error
	if !errors.As(err, &remoteErr) || remoteErr.Kind != ErrorProjectAmbiguous {
		t.Fatalf("err = %v, want an ambiguity classification", err)
	}
	// Identical titles are exactly the case a title-matching heuristic would get
	// wrong, so both candidates have to be named for the reader to choose.
	for _, want := range []string{"#3", "#4"} {
		if !strings.Contains(remoteErr.Detail, want) {
			t.Fatalf("detail = %q, want it to name %s", remoteErr.Detail, want)
		}
	}
	if !strings.Contains(remoteErr.Recovery(), "unlink") {
		t.Fatalf("recovery = %q, want it to say how to disambiguate", remoteErr.Recovery())
	}
}

func TestResolveLinkedProjectSaysWhenNoBoardIsLinked(t *testing.T) {
	t.Parallel()
	client, _ := projectClient(t, map[string]string{"api graphql": linkedProjects("")}, nil)

	_, err := client.ResolveLinkedProject(context.Background(), testRepository)
	var remoteErr *Error
	if !errors.As(err, &remoteErr) || remoteErr.Kind != ErrorProjectMissing {
		t.Fatalf("err = %v, want a missing-board classification", err)
	}
	if !strings.Contains(remoteErr.Detail, "johnjallday/ori-agent") {
		t.Fatalf("detail = %q, want the repository named", remoteErr.Detail)
	}
	if !strings.Contains(remoteErr.Recovery(), "gh project link") {
		t.Fatalf("recovery = %q, want it to say how to link one", remoteErr.Recovery())
	}
}

// TestProjectScopeFailureIsNotReportedAsAuthentication pins an ordering that is
// easy to break: gh phrases the scope refusal as "your authentication token is
// missing required scopes", which the authentication pattern also matches.
// Classified that way the advice becomes `gh auth login`, which succeeds and
// changes nothing.
func TestProjectScopeFailureIsNotReportedAsAuthentication(t *testing.T) {
	t.Parallel()
	scopeFailure := &exec.ExitError{Stderr: []byte(
		"error: your authentication token is missing required scopes " +
			"[read:project read:org read:discussion]")}
	client, _ := projectClient(t, nil, scopeFailure)

	_, err := client.ResolveLinkedProject(context.Background(), testRepository)
	var remoteErr *Error
	if !errors.As(err, &remoteErr) {
		t.Fatalf("err = %v, want a classified failure", err)
	}
	if remoteErr.Kind != ErrorProjectScope {
		t.Fatalf("kind = %q, want %q — a valid token missing a scope is not a login problem",
			remoteErr.Kind, ErrorProjectScope)
	}

	recovery := remoteErr.Recovery()
	if !strings.Contains(recovery, "https://github.com/settings/tokens") {
		t.Fatalf("recovery = %q, want the token page named", recovery)
	}
	// gh's own suggestion cannot work when the credential is GITHUB_TOKEN, so
	// the recovery has to say so rather than repeating it.
	if !strings.Contains(recovery, "GITHUB_TOKEN") {
		t.Fatalf("recovery = %q, want it to warn that gh auth refresh will not work here", recovery)
	}
	if !strings.Contains(recovery, "read:project") {
		t.Fatalf("recovery = %q, want the missing scopes named", recovery)
	}
}

const boardItems = `{"items":[
	{"title":"Coordinate based map","status":"Ready","size":"M","rank":1,
	 "why":"builds on the map work",
	 "content":{"number":292,"title":"Coordinate based map","type":"Issue",
	            "url":"https://github.com/johnjallday/ori-agent/issues/292"}},
	{"title":"Workspace lifecycle (consolidates #290, #289)","status":"Ready",
	 "content":{"title":"Workspace lifecycle (consolidates #290, #289)","type":"DraftIssue"}},
	{"title":"Skin Makeover","status":"Backlog",
	 "content":{"number":293,"title":"Skin Makeover","type":"Issue",
	            "url":"https://github.com/johnjallday/ori-agent/issues/293"}},
	{"title":"Delete button","status":"Ready",
	 "content":{"number":297,"title":"Delete button","type":"Issue",
	            "url":"https://github.com/johnjallday/ori-agent/issues/297"}}
],"totalCount":4}`

func TestListProjectItemsDetectsDraftsByTheAbsentIssueNumber(t *testing.T) {
	t.Parallel()
	client, _ := projectClient(t, map[string]string{"project item-list": boardItems}, nil)

	board, err := client.ListProjectItems(context.Background(), testRepository,
		Project{Number: 3, Owner: "johnjallday", Title: "Ori Dev"})
	if err != nil {
		t.Fatalf("ListProjectItems: %v", err)
	}
	if len(board.Items) != 4 {
		t.Fatalf("items = %d, want every card including the draft", len(board.Items))
	}

	var drafts int
	for _, item := range board.Items {
		if item.IsDraft {
			drafts++
			if item.Number != 0 {
				t.Fatalf("draft carried Issue number %d", item.Number)
			}
		}
	}
	if drafts != 1 {
		t.Fatalf("drafts = %d, want exactly the one card with no content number", drafts)
	}
	if !board.Complete || board.Truncated {
		t.Fatalf("board complete=%v truncated=%v, want a whole listing", board.Complete, board.Truncated)
	}
}

// TestReadyOrdersByRankAndSinksTheUnranked pins the ordering the morning
// decision depends on. An unranked card is one the grooming agent has not
// placed, and it must never sit above one somebody deliberately ranked.
func TestReadyOrdersByRankAndSinksTheUnranked(t *testing.T) {
	t.Parallel()
	client, _ := projectClient(t, map[string]string{"project item-list": boardItems}, nil)

	board, err := client.ListProjectItems(context.Background(), testRepository,
		Project{Number: 3, Owner: "johnjallday"})
	if err != nil {
		t.Fatalf("ListProjectItems: %v", err)
	}

	ready := board.Ready()
	// Backlog is excluded; the three Ready cards remain.
	if len(ready) != 3 {
		t.Fatalf("ready = %d cards, want only the Ready column", len(ready))
	}
	if ready[0].Rank != 1 || !ready[0].HasRank || ready[0].Number != 292 {
		t.Fatalf("first = %+v, want the ranked card", ready[0])
	}
	for _, item := range ready[1:] {
		if item.HasRank {
			t.Fatalf("a ranked card sorted below an unranked one: %+v", ready)
		}
	}
	// Unranked cards fall back to Issue number, so the order is stable rather
	// than whatever the remote happened to return.
	if ready[1].Number != 0 || !ready[1].IsDraft {
		t.Fatalf("second = %+v, want the draft (number 0) before #297", ready[1])
	}
	if ready[2].Number != 297 {
		t.Fatalf("third = %+v, want #297 last", ready[2])
	}
}

func TestListProjectItemsReportsTruncationRatherThanImplyingCompleteness(t *testing.T) {
	t.Parallel()
	client, _ := projectClient(t, map[string]string{
		"project item-list": `{"items":[{"title":"one","status":"Ready",
			"content":{"number":1,"type":"Issue","url":"u"}}],"totalCount":57}`,
	}, nil)

	board, err := client.ListProjectItems(context.Background(), testRepository,
		Project{Number: 3, Owner: "johnjallday"})
	if err != nil {
		t.Fatalf("ListProjectItems: %v", err)
	}
	if !board.Truncated || board.Complete {
		t.Fatalf("board truncated=%v complete=%v, want a capped listing reported honestly",
			board.Truncated, board.Complete)
	}
}

func TestListProjectItemsNamesTheProjectsOwnerNotTheRepositorys(t *testing.T) {
	t.Parallel()
	client, calls := projectClient(t, map[string]string{"project item-list": boardItems}, nil)

	// A user-owned board can be linked to an organization's repository, so the
	// owner on the query has to be the project's.
	_, err := client.ListProjectItems(context.Background(),
		Repository{Owner: "some-org", Name: "ori-agent"},
		Project{Number: 3, Owner: "johnjallday"})
	if err != nil {
		t.Fatalf("ListProjectItems: %v", err)
	}
	joined := strings.Join((*calls)[0], " ")
	if !strings.Contains(joined, "--owner johnjallday") {
		t.Fatalf("args = %q, want the project's owner", joined)
	}
}
