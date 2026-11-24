// Package namesearch finds Ori's own items by name for Home's Files view: the
// workspaces and groups a user may read, and the notes, tickets, chats, agents
// and stored files inside them.
//
// It matches NAMES only, case-insensitively, anywhere in the name. It never
// looks at note text, ticket descriptions, chat messages, memory entries or file
// contents, and it never writes: it does not import notes from disk, reconcile
// files, or cache anything. Every request recomputes from the stores, so there
// is no copy to go stale; a cursor is just an offset into the sorted matches.
//
// The term is private. It is matched in Go and never reaches SQL, a store, a
// log line or an error message, so a failure here can be logged safely by kind
// and error type alone.
package namesearch

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The kinds of result, in the order the response lists them.
const (
	KindWorkspaces = "workspaces"
	KindNotes      = "notes"
	KindTickets    = "tickets"
	KindChats      = "chats"
	KindAgents     = "agents"
	KindFiles      = "files"
)

// Kinds is every kind, in response order.
var Kinds = []string{KindWorkspaces, KindNotes, KindTickets, KindChats, KindAgents, KindFiles}

// Limits. The term limits are the filename index's own, so one term fits both
// searches.
const (
	PageSize        = 25
	MaxTermBytes    = 512
	MaxTermRunes    = 128
	MaxCursorDigits = 6
	maxIDBytes      = 256
	maxPathBytes    = 1024

	// Deadline bounds one whole request. A kind that has not finished by then
	// reports what it has as partial, or unavailable when it has nothing.
	Deadline = 2 * time.Second

	chatPageSize = 100
	maxChatPages = 20
)

// Status of one kind in a response.
type Status string

const (
	StatusOK          Status = "ok"
	StatusPartial     Status = "partial"
	StatusUnavailable Status = "unavailable"
)

// Reasons a kind is partial.
const (
	ReasonTooMany    = "too_many_items"
	ReasonTooManyFil = "too_many_files"
	ReasonTimedOut   = "timed_out"
	ReasonUnreadable = "unreadable"
)

// ErrInvalid marks a request the caller got wrong. Its message is fixed text:
// it never carries the term, a cursor or any other part of the request.
var ErrInvalid = errors.New("invalid name search request")

// Scope narrows a search to one workspace or group (and everything inside it).
// Folder narrows the Files kind further, to one folder of that workspace.
type Scope struct {
	WorkspaceID string       `json:"workspace_id"`
	Folder      *FolderScope `json:"folder,omitempty"`
}

// FolderScope is a folder inside a workspace's Files or Outputs section.
type FolderScope struct {
	Section string `json:"section"`
	Path    string `json:"path"`
}

// Request is one search.
type Request struct {
	Term    string            `json:"term"`
	Scope   *Scope            `json:"scope,omitempty"`
	Kinds   []string          `json:"kinds,omitempty"`
	Cursors map[string]string `json:"cursors,omitempty"`
}

// WorkspaceRef names the workspace an item lives in.
type WorkspaceRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Item is one result. RowKey is the id of the row the tree shows for it, so the
// page can open the same tab and scroll to the same row.
type Item struct {
	Kind      string       `json:"kind"`
	ID        string       `json:"id"`
	Label     string       `json:"label"`
	RowKey    string       `json:"row_key"`
	Workspace WorkspaceRef `json:"workspace"`
	Location  []string     `json:"location"`
	Modified  *time.Time   `json:"modified,omitempty"`
	Path      string       `json:"path,omitempty"`
	Size      *int64       `json:"size,omitempty"`
}

// KindResult is one kind's page of results. Count is null when the kind is
// unavailable, and a lower bound when it is partial.
type KindResult struct {
	Kind          string `json:"kind"`
	Status        Status `json:"status"`
	Count         *int   `json:"count"`
	Items         []Item `json:"items"`
	NextCursor    string `json:"next_cursor"`
	PartialReason string `json:"partial_reason,omitempty"`
}

// Response is the answer to one search: every requested kind, in Kinds order.
type Response struct {
	Kinds []KindResult `json:"kinds"`
}

// Sources are the stores the search reads. Each is a narrow read-only view so a
// test can stand in for it, and none of them is asked to write.
type Sources struct {
	Tree    TreeSource
	Notes   NoteSource
	Chats   ChatSource
	Tickets TicketSource
	Agents  AgentSource
	Files   FileSource
	Users   userprofile.UserProvider
}

// TreeSource reads the workspace and group tree.
type TreeSource interface {
	GetWorkspaceTree(ctx context.Context) ([]session.Workspace, error)
}

// NoteSource lists a workspace's notes by name, without their text.
type NoteSource interface {
	ListNotesByWorkspace(ctx context.Context, workspaceID string) ([]session.WorkspaceNoteListItem, error)
}

// ChatSource lists a workspace's chats.
type ChatSource interface {
	ListSessions(ctx context.Context, filter *session.SessionFilter, opts *session.ListOptions) (*session.ListResult, error)
}

// TicketSource lists a workspace's tickets.
type TicketSource interface {
	Search(query workspace.TicketQuery) (workspace.TicketPage, error)
}

// AgentSource lists the names of the agents a workspace shows in its Agents
// section.
type AgentSource interface {
	AgentNames(workspaceID string) ([]string, error)
}

// FileSection is the Files or Outputs folder of a workspace.
type FileSection string

const (
	SectionFiles   FileSection = "files"
	SectionOutputs FileSection = "outputs"
)

// FileEntry is one stored file.
type FileEntry struct {
	RelativePath string
	Name         string
	Size         int64
	ModTime      time.Time
}

// FileLimits bound one walk of one workspace section.
type FileLimits struct {
	MaxEntries int
	MaxDepth   int
}

// WalkStop says why a walk ended early; the zero value is "it finished".
type WalkStop string

const (
	WalkComplete   WalkStop = ""
	WalkEntryCap   WalkStop = "entries"
	WalkDepthCap   WalkStop = "depth"
	WalkCancelled  WalkStop = "cancelled"
	WalkUnreadable WalkStop = "unreadable"
)

// FileSource walks a workspace's stored files read-only. It returns how many
// entries it looked at and why it stopped if it did not finish.
type FileSource interface {
	WalkFileNames(ctx context.Context, workspaceID string, section FileSection, limits FileLimits, visit func(FileEntry)) (visited int, stop WalkStop, err error)
}

// fileCaps bound the work the Files kind can cause in one request, so the search
// never walks every workspace's whole tree without a stated limit. A walk that
// reaches any of them reports the kind as partial.
type fileCaps struct {
	perRequest   int           // entries looked at across all workspaces
	perWorkspace int           // entries looked at in one section of one workspace
	depth        int           // deepest folder level entered
	budget       time.Duration // wall-clock for the whole Files kind
}

var defaultFileCaps = fileCaps{
	perRequest:   20000,
	perWorkspace: 5000,
	depth:        12,
	budget:       800 * time.Millisecond,
}

// Service answers searches.
type Service struct {
	src      Sources
	deadline time.Duration
	caps     fileCaps
}

// New returns a Service over the given sources. A nil source makes its kind
// unavailable rather than panicking.
func New(src Sources) *Service {
	return &Service{src: src, deadline: Deadline, caps: defaultFileCaps}
}

// Normalize validates a request and returns it with the term trimmed. The
// error is always ErrInvalid with fixed text.
func (r Request) Normalize() (Request, error) {
	out := Request{Term: strings.TrimSpace(r.Term)}
	if out.Term == "" || !utf8.ValidString(out.Term) || len(out.Term) > MaxTermBytes ||
		utf8.RuneCountInString(out.Term) > MaxTermRunes {
		return Request{}, ErrInvalid
	}
	if r.Scope != nil {
		scope := Scope{WorkspaceID: strings.TrimSpace(r.Scope.WorkspaceID)}
		if scope.WorkspaceID == "" || len(scope.WorkspaceID) > maxIDBytes {
			return Request{}, ErrInvalid
		}
		if f := r.Scope.Folder; f != nil {
			section := strings.TrimSpace(f.Section)
			if section != string(SectionFiles) && section != string(SectionOutputs) {
				return Request{}, ErrInvalid
			}
			clean, ok := cleanFolderPath(f.Path)
			if !ok {
				return Request{}, ErrInvalid
			}
			scope.Folder = &FolderScope{Section: section, Path: clean}
		}
		out.Scope = &scope
	}
	if len(r.Kinds) > 0 {
		seen := map[string]bool{}
		for _, kind := range r.Kinds {
			if !knownKind(kind) {
				return Request{}, ErrInvalid
			}
			if !seen[kind] {
				seen[kind] = true
				out.Kinds = append(out.Kinds, kind)
			}
		}
	}
	if len(r.Cursors) > 0 {
		out.Cursors = map[string]string{}
		for kind, cursor := range r.Cursors {
			if !knownKind(kind) || len(cursor) == 0 || len(cursor) > MaxCursorDigits {
				return Request{}, ErrInvalid
			}
			for _, digit := range cursor {
				if digit < '0' || digit > '9' {
					return Request{}, ErrInvalid
				}
			}
			out.Cursors[kind] = cursor
		}
	}
	return out, nil
}

func knownKind(kind string) bool {
	for _, k := range Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// cleanFolderPath accepts a slash-separated path inside a workspace folder: no
// leading slash, no empty or dot segments, no "..", no NUL or backslash.
func cleanFolderPath(raw string) (string, bool) {
	path := strings.Trim(strings.TrimSpace(raw), "/")
	if path == "" {
		return "", true // the section's root
	}
	if len(path) > maxPathBytes || !utf8.ValidString(path) || strings.ContainsAny(path, "\x00\\") {
		return "", false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", false
		}
	}
	return path, true
}

// node is one workspace or group the user may read.
type node struct {
	id          string
	name        string // the label the tree shows
	kind        string // "workspace" or "group"
	updated     time.Time
	ancestors   []string // names of the groups above, outermost first
	ancestorIDs []string
}

func (n node) ref() WorkspaceRef { return WorkspaceRef{ID: n.id, Name: n.name} }

// location is where an item sits: the groups above its workspace, the
// workspace, then whatever the caller adds (a section, folders).
func (n node) location(more ...string) []string {
	out := make([]string, 0, len(n.ancestors)+1+len(more))
	out = append(out, n.ancestors...)
	out = append(out, n.name)
	return append(out, more...)
}

// Search runs one search. A request that fails validation returns ErrInvalid.
// Everything else is a response: a kind that could not be read says so in its
// own status and the other kinds still answer.
func (s *Service) Search(ctx context.Context, request Request) (Response, error) {
	req, err := request.Normalize()
	if err != nil {
		return Response{}, err
	}
	kinds := req.Kinds
	if len(kinds) == 0 {
		kinds = Kinds
	}
	// The kinds answer in the response's order whatever order they were asked in.
	asked := make([]string, 0, len(kinds))
	for _, kind := range Kinds {
		for _, want := range kinds {
			if want == kind {
				asked = append(asked, kind)
				break
			}
		}
	}

	ctx, cancel := context.WithTimeout(ctx, s.deadline)
	defer cancel()

	nodes, nodesErr := s.readable(ctx, req.Scope)
	results := make([]KindResult, len(asked))
	if nodesErr != nil {
		s.logFailure("workspaces", nodesErr)
		for i, kind := range asked {
			results[i] = unavailable(kind)
		}
		return Response{Kinds: results}, nil
	}

	needle := strings.ToLower(req.Term)
	var wg sync.WaitGroup
	for i, kind := range asked {
		wg.Add(1)
		go func(i int, kind string) {
			defer wg.Done()
			defer func() {
				if recovered := recover(); recovered != nil {
					// Type only: the panic value could hold anything.
					s.logFailure(kind, fmt.Errorf("panic: %T", recovered))
					results[i] = unavailable(kind)
				}
			}()
			c := s.collect(ctx, kind, nodes, needle, req.Scope)
			results[i] = page(kind, c, offsetFor(req.Cursors, kind))
		}(i, kind)
	}
	wg.Wait()
	return Response{Kinds: results}, nil
}

func offsetFor(cursors map[string]string, kind string) int {
	n, err := strconv.Atoi(cursors[kind])
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// readable lists the workspaces and groups the signed-in user may read, in tree
// order, narrowed to the scope. Trashed and missing workspaces drop their whole
// subtree, as do workspaces another user owns.
func (s *Service) readable(ctx context.Context, scope *Scope) ([]node, error) {
	if s.src.Tree == nil || s.src.Users == nil {
		return nil, errors.New("workspace tree unavailable")
	}
	user, err := s.src.Users.CurrentUserID(ctx)
	if err != nil {
		return nil, err
	}
	tree, err := s.src.Tree.GetWorkspaceTree(ctx)
	if err != nil {
		return nil, err
	}
	var out []node
	var walk func(list []session.Workspace, names, ids []string)
	walk = func(list []session.Workspace, names, ids []string) {
		for _, w := range list {
			if w.Status == session.WorkspaceStatusTrashed || w.Status == session.WorkspaceStatusMissing {
				continue
			}
			if !ownedBy(w.OwnerUserID, user) {
				continue
			}
			label := workspaceLabel(w)
			kind := "workspace"
			if w.Kind == session.WorkspaceKindGroup {
				kind = "group"
			}
			out = append(out, node{
				id:          w.ID,
				name:        label,
				kind:        kind,
				updated:     w.UpdatedAt,
				ancestors:   append([]string(nil), names...),
				ancestorIDs: append([]string(nil), ids...),
			})
			if len(w.Children) > 0 {
				walk(w.Children, append(append([]string(nil), names...), label), append(append([]string(nil), ids...), w.ID))
			}
		}
	}
	walk(tree, nil, nil)

	if scope == nil {
		return out, nil
	}
	inScope := make([]node, 0, len(out))
	for _, n := range out {
		if n.id == scope.WorkspaceID {
			inScope = append(inScope, n)
			continue
		}
		for _, ancestor := range n.ancestorIDs {
			if ancestor == scope.WorkspaceID {
				inScope = append(inScope, n)
				break
			}
		}
	}
	return inScope, nil
}

// ownedBy reports whether a record belongs to the user. An empty owner is the
// local user, as for every record that predates multi-user ownership.
func ownedBy(owner, user string) bool {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		owner = userprofile.LocalUserID
	}
	user = strings.TrimSpace(user)
	if user == "" {
		user = userprofile.LocalUserID
	}
	return owner == user
}

// workspaceLabel is the name the tree shows for a workspace or group row.
func workspaceLabel(w session.Workspace) string {
	if name := strings.TrimSpace(w.Name); name != "" {
		return name
	}
	if w.Kind == session.WorkspaceKindGroup {
		return "Group"
	}
	return "Untitled workspace"
}

// collected is everything one kind found before paging.
type collected struct {
	items       []Item
	partial     string
	unavailable bool
	sortNewest  bool // Files: newest first; everything else by name
}

func unavailable(kind string) KindResult {
	return KindResult{Kind: kind, Status: StatusUnavailable, Items: []Item{}}
}

func (s *Service) collect(ctx context.Context, kind string, nodes []node, needle string, scope *Scope) collected {
	switch kind {
	case KindWorkspaces:
		return s.workspaces(nodes, needle)
	case KindNotes:
		return s.notes(ctx, nodes, needle)
	case KindTickets:
		return s.tickets(ctx, nodes, needle)
	case KindChats:
		return s.chats(ctx, nodes, needle)
	case KindAgents:
		return s.agents(ctx, nodes, needle)
	case KindFiles:
		return s.files(ctx, nodes, needle, scope)
	}
	return collected{unavailable: true}
}

func matches(label, needle string) bool {
	return strings.Contains(strings.ToLower(label), needle)
}

func stamp(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// gather runs read once per workspace and tallies the outcome honestly: a read
// that failed leaves the kind partial when others succeeded and unavailable when
// none did, and running out of time keeps what was found and says so.
func (s *Service) gather(ctx context.Context, kind string, nodes []node, read func(ctx context.Context, n node) (items []Item, truncated bool, err error)) collected {
	var c collected
	var ok, failed int
	for _, n := range nodes {
		if ctx.Err() != nil {
			c.partial = ReasonTimedOut
			break
		}
		items, truncated, err := read(ctx, n)
		if err != nil {
			if ctx.Err() != nil {
				c.partial = ReasonTimedOut
				break
			}
			failed++
			s.logFailure(kind, err)
			continue
		}
		ok++
		c.items = append(c.items, items...)
		if truncated && c.partial == "" {
			c.partial = ReasonTooMany
		}
	}
	switch {
	case failed > 0 && ok == 0:
		return collected{unavailable: true}
	case c.partial == ReasonTimedOut && ok == 0 && len(nodes) > 0:
		return collected{unavailable: true}
	case failed > 0 && c.partial == "":
		c.partial = ReasonUnreadable
	}
	return c
}

func (s *Service) workspaces(nodes []node, needle string) collected {
	var c collected
	for _, n := range nodes {
		if !matches(n.name, needle) {
			continue
		}
		c.items = append(c.items, Item{
			Kind:      n.kind,
			ID:        n.id,
			Label:     n.name,
			RowKey:    n.id,
			Workspace: n.ref(),
			Location:  append([]string(nil), n.ancestors...),
			Modified:  stamp(n.updated),
		})
	}
	return c
}

func (s *Service) notes(ctx context.Context, nodes []node, needle string) collected {
	if s.src.Notes == nil {
		return collected{unavailable: true}
	}
	return s.gather(ctx, KindNotes, nodes, func(ctx context.Context, n node) ([]Item, bool, error) {
		list, err := s.src.Notes.ListNotesByWorkspace(ctx, n.id)
		if err != nil {
			return nil, false, err
		}
		var items []Item
		for _, note := range list {
			label := strings.TrimSpace(note.Name)
			if label == "" {
				label = "Untitled"
			}
			if !matches(label, needle) {
				continue
			}
			items = append(items, Item{
				Kind:      "note",
				ID:        note.ID,
				Label:     label,
				RowKey:    n.id + "/n/" + note.ID,
				Workspace: n.ref(),
				Location:  n.location("Notes"),
				Modified:  stamp(note.UpdatedAt),
			})
		}
		return items, false, nil
	})
}

func (s *Service) tickets(ctx context.Context, nodes []node, needle string) collected {
	if s.src.Tickets == nil {
		return collected{unavailable: true}
	}
	return s.gather(ctx, KindTickets, nodes, func(ctx context.Context, n node) ([]Item, bool, error) {
		// Search is left empty on purpose: the ticket service's own search also
		// reads descriptions and numbers, and this search is by title alone. The
		// defaults give the same rows the tree shows (top-level, recent window).
		page, err := s.src.Tickets.Search(workspace.TicketQuery{WorkspaceID: n.id, Limit: workspace.TicketMaxLimit})
		if err != nil {
			return nil, false, err
		}
		var items []Item
		for _, ticket := range page.Tickets {
			label := strings.TrimSpace(ticket.Title)
			if label == "" {
				label = "Untitled ticket"
			}
			if !matches(label, needle) {
				continue
			}
			items = append(items, Item{
				Kind:      "ticket",
				ID:        ticket.ID,
				Label:     label,
				RowKey:    n.id + "/t/" + ticket.ID,
				Workspace: n.ref(),
				Location:  n.location("Backlog"),
				Modified:  stamp(ticket.UpdatedAt),
			})
		}
		return items, page.Truncated, nil
	})
}

func (s *Service) chats(ctx context.Context, nodes []node, needle string) collected {
	if s.src.Chats == nil {
		return collected{unavailable: true}
	}
	return s.gather(ctx, KindChats, nodes, func(ctx context.Context, n node) ([]Item, bool, error) {
		var items []Item
		folder := n.id
		for pageNo := 0; pageNo < maxChatPages; pageNo++ {
			if ctx.Err() != nil {
				return items, false, ctx.Err()
			}
			result, err := s.src.Chats.ListSessions(ctx,
				&session.SessionFilter{FolderID: &folder},
				&session.ListOptions{Limit: chatPageSize, Offset: pageNo * chatPageSize, Sort: session.SortByUpdatedDesc})
			if err != nil {
				return nil, false, err
			}
			if result == nil {
				break
			}
			for _, chat := range result.Sessions {
				label := strings.TrimSpace(chat.Title)
				if label == "" {
					label = "Untitled chat"
				}
				if !matches(label, needle) {
					continue
				}
				items = append(items, Item{
					Kind:      "chat",
					ID:        chat.ID,
					Label:     label,
					RowKey:    n.id + "/c/" + chat.ID,
					Workspace: n.ref(),
					Location:  n.location("Chats"),
					Modified:  stamp(chat.UpdatedAt),
				})
			}
			if !result.HasMore || len(result.Sessions) == 0 {
				return items, false, nil
			}
		}
		// Ran out of pages with more left: what was found is real, but not all.
		return items, true, nil
	})
}

func (s *Service) agents(ctx context.Context, nodes []node, needle string) collected {
	if s.src.Agents == nil {
		return collected{unavailable: true}
	}
	return s.gather(ctx, KindAgents, nodes, func(_ context.Context, n node) ([]Item, bool, error) {
		names, err := s.src.Agents.AgentNames(n.id)
		if err != nil {
			return nil, false, err
		}
		var items []Item
		for _, name := range names {
			name = strings.TrimSpace(name)
			if name == "" || !matches(name, needle) {
				continue
			}
			items = append(items, Item{
				Kind:      "agent",
				ID:        name,
				Label:     name,
				RowKey:    n.id + "/a/" + name,
				Workspace: n.ref(),
				Location:  n.location("Agents"),
			})
		}
		return items, false, nil
	})
}

// files walks each in-scope workspace's Files and Outputs folders under the
// request's caps. Linked folders are not here: their filenames live in the
// local filename index, which the browser asks itself.
func (s *Service) files(ctx context.Context, nodes []node, needle string, scope *Scope) collected {
	if s.src.Files == nil {
		return collected{unavailable: true}
	}
	ctx, cancel := context.WithTimeout(ctx, s.caps.budget)
	defer cancel()

	// The scope's own workspace first, then the most recently changed.
	ordered := append([]node(nil), nodes...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if scope != nil {
			a, b := ordered[i].id == scope.WorkspaceID, ordered[j].id == scope.WorkspaceID
			if a != b {
				return a
			}
		}
		return ordered[i].updated.After(ordered[j].updated)
	})

	c := collected{sortNewest: true}
	remaining := s.caps.perRequest
	var ok, failed int
walk:
	for _, n := range ordered {
		for _, section := range []FileSection{SectionFiles, SectionOutputs} {
			if scope != nil && scope.Folder != nil && (n.id != scope.WorkspaceID || string(section) != scope.Folder.Section) {
				continue
			}
			if remaining <= 0 {
				c.partial = ReasonTooManyFil
				break walk
			}
			if ctx.Err() != nil {
				c.partial = ReasonTimedOut
				break walk
			}
			limit := s.caps.perWorkspace
			if remaining < limit {
				limit = remaining
			}
			visited, stop, err := s.src.Files.WalkFileNames(ctx, n.id, section,
				FileLimits{MaxEntries: limit, MaxDepth: s.caps.depth},
				func(entry FileEntry) {
					rel := entry.RelativePath
					if scope != nil && scope.Folder != nil && scope.Folder.Path != "" &&
						!strings.HasPrefix(rel, scope.Folder.Path+"/") {
						return
					}
					if !matches(entry.Name, needle) {
						return
					}
					c.items = append(c.items, fileItem(n, section, entry))
				})
			remaining -= visited
			if err != nil {
				failed++
				s.logFailure(KindFiles, err)
				continue
			}
			ok++
			switch stop {
			case WalkComplete:
			case WalkEntryCap, WalkDepthCap:
				if c.partial == "" {
					c.partial = ReasonTooManyFil
				}
			case WalkCancelled:
				c.partial = ReasonTimedOut
				break walk
			case WalkUnreadable:
				if c.partial == "" {
					c.partial = ReasonUnreadable
				}
			}
		}
	}
	switch {
	case failed > 0 && ok == 0:
		return collected{unavailable: true}
	case failed > 0 && c.partial == "":
		c.partial = ReasonUnreadable
	}
	return c
}

func fileItem(n node, section FileSection, entry FileEntry) Item {
	prefix, label := "f", "Files"
	kind := "file"
	if section == SectionOutputs {
		prefix, label, kind = "o", "Outputs", "output"
	}
	where := []string{label}
	if dir := parentDirs(entry.RelativePath); len(dir) > 0 {
		where = append(where, dir...)
	}
	size := entry.Size
	return Item{
		Kind:      kind,
		ID:        entry.RelativePath,
		Label:     entry.Name,
		RowKey:    n.id + "/" + prefix + "/" + entry.RelativePath,
		Workspace: n.ref(),
		Location:  n.location(where...),
		Modified:  stamp(entry.ModTime),
		Path:      entry.RelativePath,
		Size:      &size,
	}
}

func parentDirs(rel string) []string {
	cut := strings.LastIndex(rel, "/")
	if cut < 0 {
		return nil
	}
	return strings.Split(rel[:cut], "/")
}

// page sorts one kind's matches and cuts the page the cursor asks for.
func page(kind string, c collected, offset int) KindResult {
	if c.unavailable {
		return unavailable(kind)
	}
	items := c.items
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if c.sortNewest {
			at, bt := modTime(a), modTime(b)
			if !at.Equal(bt) {
				return at.After(bt)
			}
		}
		if x, y := strings.ToLower(a.Label), strings.ToLower(b.Label); x != y {
			return x < y
		}
		if x, y := strings.ToLower(a.Workspace.Name), strings.ToLower(b.Workspace.Name); x != y {
			return x < y
		}
		return a.ID < b.ID
	})
	total := len(items)
	if offset > total {
		offset = total
	}
	end := offset + PageSize
	if end > total {
		end = total
	}
	out := KindResult{
		Kind:          kind,
		Status:        StatusOK,
		Count:         &total,
		Items:         append([]Item{}, items[offset:end]...),
		PartialReason: c.partial,
	}
	if c.partial != "" {
		out.Status = StatusPartial
	}
	if end < total {
		out.NextCursor = strconv.Itoa(end)
	}
	return out
}

func modTime(i Item) time.Time {
	if i.Modified == nil {
		return time.Time{}
	}
	return *i.Modified
}

// logFailure records that a kind could not be read. It names the kind and the
// error's type only: nothing about the request, and no error text, which could
// in principle echo a path.
func (s *Service) logFailure(kind string, err error) {
	logger.Warn("name search: kind unavailable", logger.Fields{
		"kind":       kind,
		"error_type": fmt.Sprintf("%T", err),
	})
}
