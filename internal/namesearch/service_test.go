package namesearch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ---- fakes ------------------------------------------------------------------

type fakeUsers struct {
	id  string
	err error
}

func (u fakeUsers) CurrentUserID(context.Context) (string, error) { return u.id, u.err }

type fakeTree struct {
	nodes []session.Workspace
	err   error
}

func (t fakeTree) GetWorkspaceTree(context.Context) ([]session.Workspace, error) {
	return t.nodes, t.err
}

type fakeNotes struct {
	by   map[string][]session.WorkspaceNoteListItem
	fail map[string]bool // workspace ids whose read fails
	all  bool            // every read fails
}

func (n fakeNotes) ListNotesByWorkspace(_ context.Context, id string) ([]session.WorkspaceNoteListItem, error) {
	if n.all || n.fail[id] {
		return nil, errors.New("notes unavailable")
	}
	return n.by[id], nil
}

type fakeChats struct {
	by map[string][]session.SessionListItem
}

func (c fakeChats) ListSessions(_ context.Context, f *session.SessionFilter, o *session.ListOptions) (*session.ListResult, error) {
	all := c.by[*f.FolderID]
	start := min(o.Offset, len(all))
	end := min(o.Offset+o.Limit, len(all))
	return &session.ListResult{Sessions: all[start:end], Total: len(all), HasMore: end < len(all)}, nil
}

type fakeTickets struct {
	by        map[string][]workspace.Ticket
	truncated bool
}

func (t fakeTickets) Search(q workspace.TicketQuery) (workspace.TicketPage, error) {
	if q.Search != "" {
		// The service must never hand the term to the ticket service: its own
		// search also reads descriptions and numbers.
		return workspace.TicketPage{}, errors.New("the term must not reach the ticket service")
	}
	return workspace.TicketPage{Tickets: t.by[q.WorkspaceID], Truncated: t.truncated}, nil
}

type fakeAgents struct{ by map[string][]string }

func (a fakeAgents) AgentNames(id string) ([]string, error) { return a.by[id], nil }

// fakeFiles finds nothing: the default fixture has no stored files.
type fakeFiles struct{}

func (fakeFiles) WalkFileNames(context.Context, string, FileSection, FileLimits, func(FileEntry)) (int, WalkStop, error) {
	return 0, WalkComplete, nil
}

type panicNotes struct{}

func (panicNotes) ListNotesByWorkspace(context.Context, string) ([]session.WorkspaceNoteListItem, error) {
	panic("the secret term was here")
}

// ---- fixtures ---------------------------------------------------------------

func ws(id, name string, status session.WorkspaceStatus, owner string, kids ...session.Workspace) session.Workspace {
	kind := session.WorkspaceKind("")
	if len(kids) > 0 {
		kind = session.WorkspaceKindGroup
	}
	return session.Workspace{ID: id, Name: name, Status: status, OwnerUserID: owner, Children: kids, Kind: kind,
		UpdatedAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
}

// studio: a group with two workspaces; a trashed workspace with a child; a
// workspace another user owns, with a child; and a plain one.
func studio() fakeTree {
	return fakeTree{nodes: []session.Workspace{
		ws("g1", "Studio", "", "",
			ws("w1", "Album Mix", "", ""),
			ws("w2", "Garden Notes", "", "local")),
		ws("w3", "Archive", session.WorkspaceStatusTrashed, "",
			ws("w4", "Old Album", "", "")),
		ws("w5", "Elsewhere Album", "", "someone-else",
			ws("w6", "Elsewhere Child Album", "", "")),
		ws("w7", "Plain", "", ""),
		ws("w8", "Lost Album", session.WorkspaceStatusMissing, ""),
	}}
}

func note(id, name string) session.WorkspaceNoteListItem {
	return session.WorkspaceNoteListItem{ID: id, Name: name, UpdatedAt: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)}
}

func newService(extra func(*Sources)) *Service {
	src := Sources{
		Tree:  studio(),
		Users: fakeUsers{id: "local"},
		Notes: fakeNotes{by: map[string][]session.WorkspaceNoteListItem{
			"w1": {note("n1", "Album Sketch"), note("n2", "Mixing checklist")},
			"w2": {note("n3", "my ALBUM plan"), note("n4", "")},
			"w4": {note("n5", "Album from the trash")},
			"w5": {note("n6", "Album owned by someone else")},
			"w6": {note("n7", "Album in a foreign child")},
			"w8": {note("n8", "Album in a missing workspace")},
		}},
		Chats: fakeChats{by: map[string][]session.SessionListItem{
			"w1": {{ID: "c1", Title: "Album mastering"}, {ID: "c2", Title: "  "}, {ID: "c3", Title: ""}},
			"w4": {{ID: "c9", Title: "Album in the trash"}},
		}},
		Tickets: fakeTickets{by: map[string][]workspace.Ticket{
			"w1": {
				{ID: "t1", Title: "Master the album"},
				{ID: "t2", Title: "Plan", Description: "an album description that must not match"},
				{ID: "t3", Title: ""},
			},
		}},
		Agents: fakeAgents{by: map[string][]string{"w1": {"Album Scout", "Mixer"}, "w5": {"Album Spy"}}},
		Files:  fakeFiles{},
	}
	if extra != nil {
		extra(&src)
	}
	return New(src)
}

func search(t *testing.T, svc *Service, req Request) map[string]KindResult {
	t.Helper()
	resp, err := svc.Search(context.Background(), req)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	out := map[string]KindResult{}
	order := []string{}
	for _, k := range resp.Kinds {
		out[k.Kind] = k
		order = append(order, k.Kind)
	}
	want := Kinds
	if len(req.Kinds) > 0 {
		want = nil
		for _, k := range Kinds {
			for _, asked := range req.Kinds {
				if asked == k {
					want = append(want, k)
				}
			}
		}
	}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("kinds came back as %v, want %v", order, want)
	}
	return out
}

func labels(r KindResult) []string {
	out := []string{}
	for _, item := range r.Items {
		out = append(out, item.Label)
	}
	return out
}

func eq(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("%s = %q, want %q", what, got, want)
	}
}

// ---- matching ---------------------------------------------------------------

func TestMatchesNamesAnywhereIgnoringCase(t *testing.T) {
	got := search(t, newService(nil), Request{Term: "  AlBuM  "})

	eq(t, "workspaces", labels(got[KindWorkspaces]), []string{"Album Mix"})
	eq(t, "notes", labels(got[KindNotes]), []string{"Album Sketch", "my ALBUM plan"})
	eq(t, "tickets", labels(got[KindTickets]), []string{"Master the album"})
	eq(t, "chats", labels(got[KindChats]), []string{"Album mastering"})
	eq(t, "agents", labels(got[KindAgents]), []string{"Album Scout"})

	// Anywhere in the name, not only at the start.
	mid := search(t, newService(nil), Request{Term: "ster", Kinds: []string{KindTickets, KindChats}})
	eq(t, "tickets mid-word", labels(mid[KindTickets]), []string{"Master the album"})
	eq(t, "chats mid-word", labels(mid[KindChats]), []string{"Album mastering"})
}

func TestItemsCarryTheTreeRowKeyAndWhereTheyLive(t *testing.T) {
	got := search(t, newService(nil), Request{Term: "album"})

	workspaceItem := got[KindWorkspaces].Items[0]
	if workspaceItem.Kind != "workspace" || workspaceItem.RowKey != "w1" || workspaceItem.ID != "w1" {
		t.Errorf("workspace item = %+v", workspaceItem)
	}
	eq(t, "workspace location", workspaceItem.Location, []string{"Studio"})

	n := got[KindNotes].Items[0]
	if n.RowKey != "w1/n/n1" || n.Workspace != (WorkspaceRef{ID: "w1", Name: "Album Mix"}) {
		t.Errorf("note item = %+v", n)
	}
	eq(t, "note location", n.Location, []string{"Studio", "Album Mix", "Notes"})
	if n.Modified == nil {
		t.Error("a note carries its modified time")
	}
	if got[KindTickets].Items[0].RowKey != "w1/t/t1" || got[KindChats].Items[0].RowKey != "w1/c/c1" ||
		got[KindAgents].Items[0].RowKey != "w1/a/Album Scout" {
		t.Errorf("row keys: %+v %+v %+v", got[KindTickets].Items[0], got[KindChats].Items[0], got[KindAgents].Items[0])
	}
	eq(t, "ticket location", got[KindTickets].Items[0].Location, []string{"Studio", "Album Mix", "Backlog"})
}

func TestAGroupIsFoundAsAGroup(t *testing.T) {
	got := search(t, newService(nil), Request{Term: "studio", Kinds: []string{KindWorkspaces}})
	if len(got[KindWorkspaces].Items) != 1 || got[KindWorkspaces].Items[0].Kind != "group" {
		t.Fatalf("items = %+v", got[KindWorkspaces].Items)
	}
}

func TestUntitledItemsMatchTheLabelTheirTreeRowShows(t *testing.T) {
	// The tree labels an untitled chat "Untitled chat", an untitled ticket
	// "Untitled ticket" and an untitled note "Untitled".
	chats := search(t, newService(nil), Request{Term: "untitled", Kinds: []string{KindChats}})
	eq(t, "chats", labels(chats[KindChats]), []string{"Untitled chat", "Untitled chat"})

	tickets := search(t, newService(nil), Request{Term: "untitled ticket", Kinds: []string{KindTickets}})
	eq(t, "tickets", labels(tickets[KindTickets]), []string{"Untitled ticket"})

	notes := search(t, newService(nil), Request{Term: "untitled", Kinds: []string{KindNotes}})
	eq(t, "notes", labels(notes[KindNotes]), []string{"Untitled"})
}

func TestNeverMatchesATicketDescription(t *testing.T) {
	// t2's title is "Plan"; only its description says "album".
	got := search(t, newService(nil), Request{Term: "description", Kinds: []string{KindTickets}})
	if got[KindTickets].Status != StatusOK || len(got[KindTickets].Items) != 0 {
		t.Fatalf("a description matched: %+v", got[KindTickets])
	}
}

func TestNothingFoundIsAnEmptyOKKindNotAnError(t *testing.T) {
	got := search(t, newService(nil), Request{Term: "zzz-no-such-name"})
	for _, k := range Kinds {
		r := got[k]
		if r.Status != StatusOK || r.Count == nil || *r.Count != 0 || len(r.Items) != 0 || r.NextCursor != "" {
			t.Errorf("%s = %+v", k, r)
		}
	}
}

// ---- who may see what ---------------------------------------------------------

func TestExcludesTrashedMissingAndOtherOwnersAndTheirSubtrees(t *testing.T) {
	got := search(t, newService(nil), Request{Term: "album"})

	// The visible workspace names: not "Old Album" (child of a trashed one),
	// "Elsewhere Album" / "Elsewhere Child Album" (someone else's) or "Lost Album".
	eq(t, "workspaces", labels(got[KindWorkspaces]), []string{"Album Mix"})
	// Notes in the trashed, foreign, foreign-child and missing workspaces are not read.
	for _, item := range got[KindNotes].Items {
		if item.Workspace.ID != "w1" && item.Workspace.ID != "w2" {
			t.Errorf("a note from %s leaked: %+v", item.Workspace.ID, item)
		}
	}
	eq(t, "agents", labels(got[KindAgents]), []string{"Album Scout"})
	eq(t, "chats", labels(got[KindChats]), []string{"Album mastering"})
}

func TestAnEmptyOwnerIsTheLocalUserAndAnotherUserSeesOnlyTheirOwn(t *testing.T) {
	asOther := newService(func(s *Sources) { s.Users = fakeUsers{id: "someone-else"} })
	got := search(t, asOther, Request{Term: "album", Kinds: []string{KindWorkspaces, KindNotes}})
	// "someone-else" owns w5 only. w6, its child, has an empty owner = local, so
	// it is not theirs; and the group above it is not theirs either.
	eq(t, "workspaces", labels(got[KindWorkspaces]), []string{"Elsewhere Album"})
	for _, item := range got[KindNotes].Items {
		if item.Workspace.ID != "w5" {
			t.Errorf("another user was shown %+v", item)
		}
	}
}

func TestTheCurrentUserFailingMakesEveryKindUnavailable(t *testing.T) {
	svc := newService(func(s *Sources) { s.Users = fakeUsers{err: errors.New("no user")} })
	for _, k := range search(t, svc, Request{Term: "album"}) {
		if k.Status != StatusUnavailable || k.Count != nil || len(k.Items) != 0 {
			t.Errorf("%s = %+v", k.Kind, k)
		}
	}
}

func TestTheTreeFailingMakesEveryKindUnavailableWithAnOKResponse(t *testing.T) {
	svc := newService(func(s *Sources) { s.Tree = fakeTree{err: errors.New("db down")} })
	got := search(t, svc, Request{Term: "album"})
	for _, k := range Kinds {
		if got[k].Status != StatusUnavailable {
			t.Errorf("%s = %+v", k, got[k])
		}
	}
}

// ---- scope ------------------------------------------------------------------

func TestScopeNarrowsToAWorkspaceOrAGroupsWholeSubtree(t *testing.T) {
	one := search(t, newService(nil), Request{Term: "album", Scope: &Scope{WorkspaceID: "w2"}, Kinds: []string{KindWorkspaces, KindNotes}})
	eq(t, "workspace scope: workspaces", labels(one[KindWorkspaces]), nil)
	eq(t, "workspace scope: notes", labels(one[KindNotes]), []string{"my ALBUM plan"})

	group := search(t, newService(nil), Request{Term: "album", Scope: &Scope{WorkspaceID: "g1"}, Kinds: []string{KindWorkspaces, KindNotes}})
	eq(t, "group scope: workspaces", labels(group[KindWorkspaces]), []string{"Album Mix"})
	eq(t, "group scope: notes", labels(group[KindNotes]), []string{"Album Sketch", "my ALBUM plan"})
}

func TestScopeToAWorkspaceTheUserCannotReadFindsNothing(t *testing.T) {
	for _, id := range []string{"w4", "w5", "w3", "no-such-workspace"} {
		got := search(t, newService(nil), Request{Term: "album", Scope: &Scope{WorkspaceID: id}})
		for _, k := range got {
			if k.Count == nil || *k.Count != 0 {
				t.Errorf("scope %s: %s = %+v", id, k.Kind, k)
			}
		}
	}
}

// ---- paging -----------------------------------------------------------------

func manyNotes(n int) fakeNotes {
	list := []session.WorkspaceNoteListItem{}
	for i := 0; i < n; i++ {
		list = append(list, note(fmt.Sprintf("n%02d", i), fmt.Sprintf("Draft %02d", i)))
	}
	return fakeNotes{by: map[string][]session.WorkspaceNoteListItem{"w1": list}}
}

func TestPagesTwentyFiveAndOffersMore(t *testing.T) {
	svc := newService(func(s *Sources) { s.Notes = manyNotes(26) })
	first := search(t, svc, Request{Term: "draft", Kinds: []string{KindNotes}})[KindNotes]
	if len(first.Items) != PageSize || first.Count == nil || *first.Count != 26 || first.NextCursor != "25" {
		t.Fatalf("first page: %d items, count %v, next %q", len(first.Items), first.Count, first.NextCursor)
	}
	if first.Items[0].Label != "Draft 00" || first.Items[24].Label != "Draft 24" {
		t.Errorf("order: %q ... %q", first.Items[0].Label, first.Items[24].Label)
	}

	second := search(t, svc, Request{Term: "draft", Kinds: []string{KindNotes}, Cursors: map[string]string{KindNotes: first.NextCursor}})[KindNotes]
	eq(t, "second page", labels(second), []string{"Draft 25"})
	if second.NextCursor != "" || *second.Count != 26 {
		t.Errorf("second page: next %q count %d", second.NextCursor, *second.Count)
	}

	// A cursor past the end is an empty page, not an error.
	past := search(t, svc, Request{Term: "draft", Kinds: []string{KindNotes}, Cursors: map[string]string{KindNotes: "999"}})[KindNotes]
	if len(past.Items) != 0 || past.Status != StatusOK {
		t.Errorf("past the end: %+v", past)
	}
}

func TestOneKindsCursorLeavesTheOthersOnTheirFirstPage(t *testing.T) {
	svc := newService(func(s *Sources) { s.Notes = manyNotes(30) })
	got := search(t, svc, Request{Term: "d", Cursors: map[string]string{KindNotes: "25"}})
	if len(got[KindNotes].Items) != 5 {
		t.Errorf("notes: %d items", len(got[KindNotes].Items))
	}
	if got[KindWorkspaces].NextCursor != "" {
		t.Errorf("workspaces moved: %+v", got[KindWorkspaces])
	}
}

func TestOrderIsByNameThenWorkspaceNotByWhateverTheStoreReturned(t *testing.T) {
	svc := newService(func(s *Sources) {
		s.Notes = fakeNotes{by: map[string][]session.WorkspaceNoteListItem{
			"w2": {note("b", "beta")},
			"w1": {note("c", "Alpha"), note("a", "alpha")},
		}}
	})
	got := search(t, svc, Request{Term: "a", Kinds: []string{KindNotes}})[KindNotes]
	ids := []string{}
	for _, it := range got.Items {
		ids = append(ids, it.Workspace.ID+"/"+it.ID)
	}
	// "Alpha" and "alpha" tie case-insensitively and are in one workspace: by id.
	eq(t, "order", ids, []string{"w1/a", "w1/c", "w2/b"})
}

// ---- failure ----------------------------------------------------------------

func TestOneKindUnavailableWhileTheOthersStillAnswer(t *testing.T) {
	svc := newService(func(s *Sources) { s.Notes = fakeNotes{all: true} })
	got := search(t, svc, Request{Term: "album"})
	n := got[KindNotes]
	if n.Status != StatusUnavailable || n.Count != nil || len(n.Items) != 0 {
		t.Fatalf("notes = %+v", n)
	}
	eq(t, "tickets still answer", labels(got[KindTickets]), []string{"Master the album"})
	eq(t, "workspaces still answer", labels(got[KindWorkspaces]), []string{"Album Mix"})
}

func TestOneUnreadableWorkspaceMakesTheKindPartialNotUnavailable(t *testing.T) {
	svc := newService(func(s *Sources) {
		s.Notes = fakeNotes{
			by:   map[string][]session.WorkspaceNoteListItem{"w1": {note("n1", "Album Sketch")}},
			fail: map[string]bool{"w2": true},
		}
	})
	n := search(t, svc, Request{Term: "album", Kinds: []string{KindNotes}})[KindNotes]
	if n.Status != StatusPartial || n.PartialReason != ReasonUnreadable {
		t.Fatalf("notes = %+v", n)
	}
	eq(t, "what was readable is still shown", labels(n), []string{"Album Sketch"})
}

func TestTruncatedTicketsAreReportedPartial(t *testing.T) {
	svc := newService(func(s *Sources) {
		s.Tickets = fakeTickets{by: map[string][]workspace.Ticket{"w1": {{ID: "t1", Title: "Album"}}}, truncated: true}
	})
	got := search(t, svc, Request{Term: "album", Kinds: []string{KindTickets}})[KindTickets]
	if got.Status != StatusPartial || got.PartialReason != ReasonTooMany || len(got.Items) != 1 {
		t.Fatalf("tickets = %+v", got)
	}
}

func TestChatsAreReadPageByPageUpToACap(t *testing.T) {
	list := []session.SessionListItem{}
	for i := 0; i < 250; i++ {
		list = append(list, session.SessionListItem{ID: fmt.Sprintf("c%03d", i), Title: fmt.Sprintf("Chat %03d", i)})
	}
	svc := newService(func(s *Sources) { s.Chats = fakeChats{by: map[string][]session.SessionListItem{"w1": list}} })
	got := search(t, svc, Request{Term: "chat 2", Kinds: []string{KindChats}})[KindChats]
	// "Chat 2xx" are the last 50 of the 250: they sit on the third page.
	if got.Status != StatusOK || *got.Count != 50 {
		t.Fatalf("chats = status %s count %v", got.Status, got.Count)
	}
}

// ---- the term stays private ---------------------------------------------------

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

func TestTheTermNeverReachesTheLogOnSuccessOrFailure(t *testing.T) {
	const term = "zebra-secret-term"
	logs := captureLog(t)

	// Success.
	search(t, newService(nil), Request{Term: term})
	// A store that fails, one that panics with the term in the panic value, a
	// tree that fails, and a user lookup that fails.
	search(t, newService(func(s *Sources) { s.Notes = fakeNotes{all: true} }), Request{Term: term})
	search(t, newService(func(s *Sources) { s.Notes = panicNotes{} }), Request{Term: term})
	search(t, newService(func(s *Sources) { s.Tree = fakeTree{err: errors.New("db down")} }), Request{Term: term})
	search(t, newService(func(s *Sources) { s.Users = fakeUsers{err: errors.New("no user")} }), Request{Term: term})

	if logs.Len() == 0 {
		t.Fatal("expected the failures themselves to be logged (by kind), but nothing was")
	}
	if strings.Contains(strings.ToLower(logs.String()), term) {
		t.Fatalf("the term reached the log:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "kind=notes") {
		t.Errorf("the failing kind should be named:\n%s", logs.String())
	}
}

func TestAPanickingStoreMakesOnlyItsKindUnavailable(t *testing.T) {
	captureLog(t)
	svc := newService(func(s *Sources) { s.Notes = panicNotes{} })
	got := search(t, svc, Request{Term: "album"})
	if got[KindNotes].Status != StatusUnavailable {
		t.Fatalf("notes = %+v", got[KindNotes])
	}
	eq(t, "others", labels(got[KindWorkspaces]), []string{"Album Mix"})
}

func TestTheTermIsNeverPassedToTheTicketService(t *testing.T) {
	// fakeTickets fails any query that carries a Search term.
	got := search(t, newService(nil), Request{Term: "album", Kinds: []string{KindTickets}})[KindTickets]
	if got.Status != StatusOK {
		t.Fatalf("tickets = %+v", got)
	}
}

// ---- validation ---------------------------------------------------------------

func TestNormalizeRejectsWhatItShould(t *testing.T) {
	long := strings.Repeat("a", MaxTermBytes+1)
	manyRunes := strings.Repeat("é", MaxTermRunes+1) // 2 bytes each: under the byte cap, over the rune cap
	bad := map[string]Request{
		"empty":                {},
		"only spaces":          {Term: "   \t"},
		"too many bytes":       {Term: long},
		"too many runes":       {Term: manyRunes},
		"invalid utf-8":        {Term: "ab\xffcd"},
		"unknown kind":         {Term: "x", Kinds: []string{"nope"}},
		"unknown cursor kind":  {Term: "x", Cursors: map[string]string{"nope": "1"}},
		"cursor not digits":    {Term: "x", Cursors: map[string]string{KindNotes: "1a"}},
		"cursor negative":      {Term: "x", Cursors: map[string]string{KindNotes: "-1"}},
		"cursor empty":         {Term: "x", Cursors: map[string]string{KindNotes: ""}},
		"cursor too long":      {Term: "x", Cursors: map[string]string{KindNotes: "1234567"}},
		"scope without id":     {Term: "x", Scope: &Scope{}},
		"scope folder section": {Term: "x", Scope: &Scope{WorkspaceID: "w1", Folder: &FolderScope{Section: "memory"}}},
		"scope folder dotdot":  {Term: "x", Scope: &Scope{WorkspaceID: "w1", Folder: &FolderScope{Section: "files", Path: "a/../b"}}},
		"scope folder nul":     {Term: "x", Scope: &Scope{WorkspaceID: "w1", Folder: &FolderScope{Section: "files", Path: "a\x00b"}}},
		"scope folder slashes": {Term: "x", Scope: &Scope{WorkspaceID: "w1", Folder: &FolderScope{Section: "files", Path: "a//b"}}},
	}
	for name, req := range bad {
		t.Run(name, func(t *testing.T) {
			_, err := req.Normalize()
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
			if req.Term != "" && strings.Contains(err.Error(), req.Term) {
				t.Errorf("the error echoed the term: %q", err)
			}
		})
	}

	good, err := Request{
		Term:    "  Hello  ",
		Kinds:   []string{KindFiles, KindNotes, KindNotes},
		Cursors: map[string]string{KindNotes: "25"},
		Scope:   &Scope{WorkspaceID: " w1 ", Folder: &FolderScope{Section: "files", Path: "/docs/"}},
	}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if good.Term != "Hello" || len(good.Kinds) != 2 || good.Scope.WorkspaceID != "w1" || good.Scope.Folder.Path != "docs" {
		t.Errorf("normalized = %+v / %+v", good, good.Scope)
	}
}

// ---- deadline -----------------------------------------------------------------

type slowNotes struct{ per time.Duration }

func (s slowNotes) ListNotesByWorkspace(ctx context.Context, id string) ([]session.WorkspaceNoteListItem, error) {
	select {
	case <-time.After(s.per):
		return []session.WorkspaceNoteListItem{note("n-"+id, "Album "+id)}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestRunningOutOfTimeKeepsWhatWasFoundAndSaysSo(t *testing.T) {
	svc := newService(func(s *Sources) { s.Notes = slowNotes{per: 60 * time.Millisecond} })
	svc.deadline = 100 * time.Millisecond
	n := search(t, svc, Request{Term: "album", Kinds: []string{KindNotes}})[KindNotes]
	if n.Status != StatusPartial || n.PartialReason != ReasonTimedOut || len(n.Items) == 0 {
		t.Fatalf("notes = %+v", n)
	}
}

// ---- files (task 3.1's contract; the walker lands with the service) -------------

type fileFixture struct {
	svc   *Service
	store *workspace.FileStore
	ids   map[string]string // name -> workspace id
}

func newFileFixture(t *testing.T, names ...string) fileFixture {
	t.Helper()
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fx := fileFixture{store: store, ids: map[string]string{}}
	var nodes []session.Workspace
	for _, name := range names {
		w := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: name})
		if err := store.Save(w); err != nil {
			t.Fatal(err)
		}
		fx.ids[name] = w.ID
		nodes = append(nodes, session.Workspace{ID: w.ID, Name: name, UpdatedAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)})
	}
	fx.svc = New(Sources{Tree: fakeTree{nodes: nodes}, Users: fakeUsers{id: "local"}, Files: WorkspaceBacked{Store: store}})
	return fx
}

func (fx fileFixture) write(t *testing.T, name, section, rel string, mod time.Time) {
	t.Helper()
	root := fx.store.GetFilesPath(fx.ids[name])
	if section == "outputs" {
		root = fx.store.GetOutputsPath(fx.ids[name])
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !mod.IsZero() {
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFilesAreFoundByNameNewestFirstWithTheirLocations(t *testing.T) {
	fx := newFileFixture(t, "Studio One")
	day := func(d int) time.Time { return time.Date(2026, 10, d, 12, 0, 0, 0, time.UTC) }
	fx.write(t, "Studio One", "files", "Report Final.pdf", day(1))
	fx.write(t, "Studio One", "files", "sub/dir/final-notes.txt", day(3))
	fx.write(t, "Studio One", "outputs", "final-output.md", day(2))
	fx.write(t, "Studio One", "files", "unrelated.txt", day(4))

	got := search(t, fx.svc, Request{Term: "FINAL", Kinds: []string{KindFiles}})[KindFiles]
	eq(t, "files newest first", labels(got), []string{"final-notes.txt", "final-output.md", "Report Final.pdf"})

	id := fx.ids["Studio One"]
	nested := got.Items[0]
	if nested.Kind != "file" || nested.RowKey != id+"/f/sub/dir/final-notes.txt" || nested.Path != "sub/dir/final-notes.txt" {
		t.Errorf("nested file = %+v", nested)
	}
	eq(t, "nested location", nested.Location, []string{"Studio One", "Files", "sub", "dir"})
	out := got.Items[1]
	if out.Kind != "output" || out.RowKey != id+"/o/final-output.md" {
		t.Errorf("output = %+v", out)
	}
	eq(t, "output location", out.Location, []string{"Studio One", "Outputs"})
	if nested.Size == nil || *nested.Size != 1 || nested.Modified == nil {
		t.Errorf("size/modified missing: %+v", nested)
	}
}

func TestFilesSkipHiddenPathsAndSymlinks(t *testing.T) {
	fx := newFileFixture(t, "Studio One")
	fx.write(t, "Studio One", "files", ".hidden/final.txt", time.Time{})
	fx.write(t, "Studio One", "files", ".final-dotfile", time.Time{})
	fx.write(t, "Studio One", "files", "visible/final.txt", time.Time{})
	root := fx.store.GetFilesPath(fx.ids["Studio One"])
	if err := os.Symlink(filepath.Join(root, "visible"), filepath.Join(root, "final-link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	got := search(t, fx.svc, Request{Term: "final", Kinds: []string{KindFiles}})[KindFiles]
	eq(t, "files", labels(got), []string{"final.txt"})
	if got.Items[0].Path != "visible/final.txt" {
		t.Errorf("path = %q", got.Items[0].Path)
	}
}

func TestFilesAreOnlyFromWorkspacesTheUserMayRead(t *testing.T) {
	fx := newFileFixture(t, "Mine", "Theirs")
	fx.write(t, "Mine", "files", "plan.txt", time.Time{})
	fx.write(t, "Theirs", "files", "plan.txt", time.Time{})
	// Mark "Theirs" as another user's.
	tree := fx.svc.src.Tree.(fakeTree)
	for i := range tree.nodes {
		if tree.nodes[i].Name == "Theirs" {
			tree.nodes[i].OwnerUserID = "someone-else"
		}
	}
	fx.svc.src.Tree = tree
	got := search(t, fx.svc, Request{Term: "plan", Kinds: []string{KindFiles}})[KindFiles]
	if len(got.Items) != 1 || got.Items[0].Workspace.Name != "Mine" {
		t.Fatalf("files = %+v", got.Items)
	}
}

func TestFilesPerWorkspaceCapReportsPartial(t *testing.T) {
	fx := newFileFixture(t, "Studio One")
	for i := 0; i < 30; i++ {
		fx.write(t, "Studio One", "files", fmt.Sprintf("cap-%02d.txt", i), time.Time{})
	}
	fx.svc.caps.perWorkspace = 10
	got := search(t, fx.svc, Request{Term: "cap-", Kinds: []string{KindFiles}})[KindFiles]
	if got.Status != StatusPartial || got.PartialReason != ReasonTooManyFil {
		t.Fatalf("files = %+v", got)
	}
	if n := len(got.Items); n == 0 || n > 10 {
		t.Errorf("looked at 10 entries, found %d", n)
	}
	if got.Count == nil || *got.Count > 10 {
		t.Errorf("count must be a lower bound, got %v", got.Count)
	}
}

func TestFilesPerRequestCapSpansWorkspaces(t *testing.T) {
	fx := newFileFixture(t, "One", "Two", "Three")
	for _, name := range []string{"One", "Two", "Three"} {
		for i := 0; i < 5; i++ {
			fx.write(t, name, "files", fmt.Sprintf("span-%d.txt", i), time.Time{})
		}
	}
	fx.svc.caps.perRequest = 7
	got := search(t, fx.svc, Request{Term: "span-", Kinds: []string{KindFiles}})[KindFiles]
	if got.Status != StatusPartial || got.PartialReason != ReasonTooManyFil {
		t.Fatalf("files = %+v", got)
	}
	if len(got.Items) > 7 {
		t.Errorf("found %d with a request cap of 7", len(got.Items))
	}
}

func TestFilesDepthCapReportsPartial(t *testing.T) {
	fx := newFileFixture(t, "Studio One")
	fx.write(t, "Studio One", "files", "a/b/c/deep-file.txt", time.Time{})
	fx.write(t, "Studio One", "files", "a/shallow-file.txt", time.Time{})
	fx.svc.caps.depth = 2
	got := search(t, fx.svc, Request{Term: "-file", Kinds: []string{KindFiles}})[KindFiles]
	if got.Status != StatusPartial || got.PartialReason != ReasonTooManyFil {
		t.Fatalf("files = %+v", got)
	}
	eq(t, "files", labels(got), []string{"shallow-file.txt"})
}

func TestFilesInTheScopeFolderOnly(t *testing.T) {
	fx := newFileFixture(t, "Studio One")
	fx.write(t, "Studio One", "files", "docs/plan.txt", time.Time{})
	fx.write(t, "Studio One", "files", "other/plan.txt", time.Time{})
	fx.write(t, "Studio One", "outputs", "docs/plan.txt", time.Time{})
	got := search(t, fx.svc, Request{
		Term: "plan", Kinds: []string{KindFiles},
		Scope: &Scope{WorkspaceID: fx.ids["Studio One"], Folder: &FolderScope{Section: "files", Path: "docs"}},
	})[KindFiles]
	if len(got.Items) != 1 || got.Items[0].Path != "docs/plan.txt" || got.Items[0].Kind != "file" {
		t.Fatalf("files = %+v", got.Items)
	}
}

func TestAMissingFilesFolderIsAnEmptyOKKind(t *testing.T) {
	fx := newFileFixture(t, "Studio One")
	got := search(t, fx.svc, Request{Term: "anything", Kinds: []string{KindFiles}})[KindFiles]
	if got.Status != StatusOK || *got.Count != 0 {
		t.Fatalf("files = %+v", got)
	}
}

func TestACancelledRequestReportsTimedOutNotAnError(t *testing.T) {
	fx := newFileFixture(t, "Studio One")
	fx.write(t, "Studio One", "files", "plan.txt", time.Time{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	resp, err := fx.svc.Search(ctx, Request{Term: "plan", Kinds: []string{KindFiles}})
	if err != nil {
		t.Fatal(err)
	}
	if k := resp.Kinds[0]; k.Status == StatusOK {
		t.Fatalf("a cancelled search reported ok: %+v", k)
	}
}

func TestAgentsOnlyListThoseWithAWorkspaceSnapshot(t *testing.T) {
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Crew"})
	w.AgentInstances = []workspace.AgentInstance{{ID: "i1", Name: "Ghost"}}
	if err := store.Save(w); err != nil {
		t.Fatal(err)
	}
	names, err := WorkspaceBacked{Store: store}.AgentNames(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	// "Ghost" is referenced but has no config.json snapshot, so the tree does
	// not list it and neither does the search.
	for _, n := range names {
		if n == "Ghost" {
			t.Errorf("a referenced agent with no snapshot was listed: %v", names)
		}
	}
	if _, err := (WorkspaceBacked{Store: store}).AgentNames("no-such-workspace"); err == nil {
		t.Error("an unknown workspace must be an error")
	}
}
