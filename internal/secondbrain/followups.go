package secondbrain

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/followup"
)

const (
	// FollowUpSourceType is the SourceRef type of a follow-up made from an
	// open item in the user's notes (FR28).
	FollowUpSourceType = "note_item"
	// MaxSuggested caps the items the primary action adds (FR24).
	MaxSuggested = 10
)

// Suggest chooses the items the primary action adds (FR24): dated items
// first, soonest due first; then items in notes that mark an open decision;
// then items in notes edited in the last week; at most MaxSuggested. When
// none of those apply, the most recently edited notes' items stand in, so a
// look with open items always has something to add. It returns the chosen
// indices of loops.
func Suggest(loops []OpenLoop) map[int]bool {
	order := make([]int, 0, len(loops))
	for index := range loops {
		order = append(order, index)
	}
	rank := func(loop OpenLoop) int {
		switch {
		case loop.Due != "":
			return 0
		case loop.InDecision:
			return 1
		case loop.InActive:
			return 2
		default:
			return 3
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := loops[order[i]], loops[order[j]]
		if rank(a) != rank(b) {
			return rank(a) < rank(b)
		}
		if a.Due != b.Due {
			return a.Due < b.Due
		}
		return false // keep the newest-note-first order of loops
	})
	chosen := make(map[int]bool, MaxSuggested)
	for _, index := range order {
		if len(chosen) == MaxSuggested || rank(loops[index]) == 3 {
			break
		}
		chosen[index] = true
	}
	if len(chosen) == 0 {
		for index := range loops {
			if len(chosen) == MaxSuggested {
				break
			}
			chosen[index] = true
		}
	}
	return chosen
}

// FollowUps is the part of the follow-up service an acceptance uses.
type FollowUps interface {
	Capture(ctx context.Context, in followup.CaptureInput) (*followup.FollowUp, error)
	FindBySource(ctx context.Context, userID, sourceType, sourceID string) (*followup.FollowUp, error)
}

// ProjectTickets offers an open line in a project's notes as that project's
// ticket, when an active workspace's primary directory is exactly that project's
// folder (PRD 760 FR-28).
type ProjectTickets interface {
	// WorkspaceForFolder finds the active workspace whose primary directory is
	// exactly this folder.
	WorkspaceForFolder(path string) (workspaceID, workspaceName string, ok bool)
	// CreateNoteTicket makes a Backlog ticket with the item's id as its key. A
	// later attempt finds the same ticket and reports created false.
	CreateNoteTicket(ctx context.Context, workspaceID string, ticket NoteTicket) (created bool, err error)
	// HasNoteTicket says whether this workspace already holds the line's ticket,
	// whatever has happened to it since (renamed, moved, closed).
	HasNoteTicket(workspaceID, itemID string) bool
}

// NoteTicket is a ticket made from an open line.
type NoteTicket struct {
	ItemID string
	Title  string
	Detail string
	DueAt  *time.Time
}

// ProjectFolderOf returns the folder of a note directly or deeper inside
// Projects/<name>/, relative to the root. A note path that climbs out, or one not
// inside a project, has none.
func ProjectFolderOf(note string) (string, bool) {
	parts := strings.Split(note, "/")
	if len(parts) < 3 || parts[0] != FolderProjects {
		return "", false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", false
		}
	}
	if !validProjectName(parts[1]) {
		return "", false
	}
	return FolderProjects + "/" + parts[1], true
}

// ItemDestination is the workspace an item would become a ticket in, if any.
func ItemDestination(tickets ProjectTickets, root string, item LookItem) (workspaceID, workspaceName string, ok bool) {
	if tickets == nil || item.Seeded {
		return "", "", false
	}
	folder, inProject := ProjectFolderOf(item.Note)
	if !inProject {
		return "", "", false
	}
	return tickets.WorkspaceForFolder(filepath.Join(root, filepath.FromSlash(folder)))
}

// Acceptance turns chosen open items into HQ follow-ups, or into a project's
// tickets when Tickets is set.
type Acceptance struct {
	UserID      string
	WorkspaceID string
	Source      Source
	Items       []LookItem
	Tickets     ProjectTickets
	// Location places a due date; it defaults to the server's own zone.
	Location *time.Location
}

// Accept creates a follow-up for each item that does not have one yet (FR28):
// SourceRef{note_item, item id}, the line as the title, the note's relative
// path as the detail, the item's date as the due date. An item accepted
// before, by this look or an earlier one, in whatever state the user left it,
// is skipped and counted, never captured again.
func Accept(ctx context.Context, followUps FollowUps, in Acceptance) (created, skipped int, err error) {
	result, err := AcceptRouted(ctx, followUps, in)
	return result.FollowUps, result.Skipped, err
}

// AcceptResult says where accepted items went.
type AcceptResult struct {
	FollowUps int
	Skipped   int
	// Tickets counts the tickets made, by workspace name.
	Tickets map[string]int
}

// TicketCount is every ticket made.
func (r AcceptResult) TicketCount() int {
	total := 0
	for _, count := range r.Tickets {
		total += count
	}
	return total
}

// AcceptRouted is Accept with project routing (PRD 760 FR-28, FR-29). An item
// already held as a follow-up stays one and is skipped, never moved. Otherwise a
// line in a project with a workspace becomes that workspace's Backlog ticket,
// keyed by the item's id so a later look makes no duplicate. Everything else
// becomes a follow-up, as before.
func AcceptRouted(ctx context.Context, followUps FollowUps, in Acceptance) (AcceptResult, error) {
	var result AcceptResult
	if followUps == nil {
		return result, errors.New("secondbrain: follow-ups unavailable")
	}
	location := in.Location
	if location == nil {
		location = time.Local
	}
	for _, item := range in.Items {
		if item.Seeded {
			// Seeded from a follow-up the user already has; its own source is
			// not this note, so the dedup below could not see it.
			result.Skipped++
			continue
		}
		_, err := followUps.FindBySource(ctx, in.UserID, FollowUpSourceType, item.ID)
		switch {
		case err == nil:
			result.Skipped++
			continue
		case !errors.Is(err, followup.ErrNotFound):
			return result, err
		}
		capture := captureInput(in, item, location)
		if workspaceID, name, routed := ItemDestination(in.Tickets, in.Source.Path, item); routed {
			created, err := in.Tickets.CreateNoteTicket(ctx, workspaceID, NoteTicket{
				ItemID: item.ID, Title: item.Text, Detail: item.Note, DueAt: capture.DueAt,
			})
			if err != nil {
				return result, err
			}
			if created {
				if result.Tickets == nil {
					result.Tickets = map[string]int{}
				}
				result.Tickets[name]++
			} else {
				result.Skipped++
			}
			continue
		}
		if _, err := followUps.Capture(ctx, capture); err != nil {
			return result, err
		}
		result.FollowUps++
	}
	return result, nil
}

func captureInput(in Acceptance, item LookItem, location *time.Location) followup.CaptureInput {
	capture := followup.CaptureInput{
		UserID: in.UserID, WorkspaceID: in.WorkspaceID,
		Category: followup.CategoryIOwe, Direction: followup.DirectionOutbound,
		Title: item.Text, Detail: itemDetail(item),
		Source:     followup.SourceRef{Type: FollowUpSourceType, ID: item.ID},
		Provenance: followup.ProvenanceExplicit,
	}
	if item.InDecision {
		capture.Category, capture.Direction = followup.CategoryNeedsDecision, followup.DirectionNone
	}
	if due, err := time.ParseInLocation("2006-01-02", item.Due, location); err == nil {
		// Due at the end of that day, so an item due today is not yet late.
		end := due.AddDate(0, 0, 1).Add(-time.Second)
		capture.DueAt = &end
	}
	return capture
}

// detailLinkPrefix starts the line of a follow-up's detail that carries the
// Obsidian link. itemDetail writes it and SplitDetail reads it, so the stored
// format has one owner.
const detailLinkPrefix = "Open in Obsidian: "

// itemDetail is a follow-up's detail: the note it came from and, for an
// Obsidian vault, the link that opens it (FR28).
func itemDetail(item LookItem) string {
	if item.Link == "" {
		return item.Note
	}
	return item.Note + "\n" + detailLinkPrefix + item.Link
}

// SplitDetail reads a note follow-up's stored detail back into the note's path
// and, when the detail carries one, the link that opens it. Lines added later,
// such as DoneInNotes, are ignored. The link is returned as stored; callers
// decide which schemes they render.
func SplitDetail(detail string) (note, link string) {
	lines := strings.Split(strings.ReplaceAll(detail, "\r\n", "\n"), "\n")
	note = strings.TrimSpace(lines[0])
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, detailLinkPrefix); ok && link == "" {
			link = strings.TrimSpace(rest)
		}
	}
	return note, link
}

// DoneInNotes is what a follow-up says once its line is done in the notes.
const DoneInNotes = "Done in your notes."

// FollowUpCloser is the part of the follow-up service that closes a
// follow-up whose line was finished in the notes.
type FollowUpCloser interface {
	FindBySource(ctx context.Context, userID, sourceType, sourceID string) (*followup.FollowUp, error)
	Edit(ctx context.Context, userID, id, title, detail string, dueAt *time.Time) (*followup.FollowUp, error)
	Complete(ctx context.Context, userID, id string) (*followup.FollowUp, error)
}

// CompleteGone completes each open follow-up made from an item a later look
// no longer finds open: ticked, marked DONE, or removed (FR29). The note is
// never edited to match; a follow-up the user already closed, snoozed into
// the past or dismissed is left as it is unless it is still open.
func CompleteGone(ctx context.Context, closer FollowUpCloser, userID string, gone []string) (int, error) {
	if closer == nil {
		return 0, nil
	}
	completed := 0
	for _, id := range gone {
		closed, err := closeFollowUp(ctx, closer, userID, id, DoneInNotes)
		if err != nil {
			return completed, err
		}
		if closed {
			completed++
		}
	}
	return completed, nil
}

// DoneInView is what a follow-up says once its item is marked done in the Notes
// panel. The note itself is not edited.
const DoneInView = "Done from your notes view."

// CompleteDone completes the open follow-up made from an item the user marked
// done, if there is one. It reports whether one was completed.
func CompleteDone(ctx context.Context, closer FollowUpCloser, userID, itemID string) (bool, error) {
	if closer == nil {
		return false, nil
	}
	return closeFollowUp(ctx, closer, userID, itemID, DoneInView)
}

// closeFollowUp records why in the follow-up's detail and completes it. A
// missing follow-up, or one the user already closed, is left alone.
func closeFollowUp(ctx context.Context, closer FollowUpCloser, userID, itemID, why string) (bool, error) {
	existing, err := closer.FindBySource(ctx, userID, FollowUpSourceType, itemID)
	if errors.Is(err, followup.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !existing.IsOpen() {
		return false, nil
	}
	detail := strings.TrimSpace(existing.Detail)
	if !strings.Contains(detail, why) {
		detail = strings.TrimSpace(detail + "\n" + why)
	}
	if _, err := closer.Edit(ctx, userID, existing.ID, existing.Title, detail, existing.DueAt); err != nil {
		return false, err
	}
	if _, err := closer.Complete(ctx, userID, existing.ID); err != nil {
		return false, err
	}
	return true, nil
}
