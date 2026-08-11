package workspace

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Server-side Ticket filtering, search, sorting, and the recent/archive
// default (tasks/prd-workspace-ticket-management.md FR-59, FR-65 through
// FR-68, FR-143).
//
// All of this lives on the server on purpose. A browser that has to pull every
// descendant Ticket in order to filter or sort them does not scale past a
// small workspace tree, and it makes a rolled-up parent pay for work it never
// displays (FR-68). The client sends a query; the server returns only the
// matching page.

// TicketDueFilter narrows by due-date condition (FR-65).
type TicketDueFilter string

const (
	// TicketDueAny is the default: no due-date constraint.
	TicketDueAny TicketDueFilter = ""
	// TicketDueOverdue matches a due date strictly before today. Terminal
	// Tickets are never overdue — closed work cannot be late.
	TicketDueOverdue TicketDueFilter = "overdue"
	// TicketDueToday matches a due date falling on today.
	TicketDueToday TicketDueFilter = "today"
	// TicketDueWeek matches a due date from today through six days ahead.
	TicketDueWeek TicketDueFilter = "week"
	// TicketDueNone matches Tickets with no due date at all.
	TicketDueNone TicketDueFilter = "none"
)

func (f TicketDueFilter) Valid() bool {
	switch f {
	case TicketDueAny, TicketDueOverdue, TicketDueToday, TicketDueWeek, TicketDueNone:
		return true
	}
	return false
}

// ParseTicketDueFilter validates an inbound due-date filter value.
func ParseTicketDueFilter(value string) (TicketDueFilter, error) {
	filter := TicketDueFilter(strings.ToLower(strings.TrimSpace(value)))
	if !filter.Valid() {
		return "", invalidTicketField("due", "due must be one of overdue, today, week, none (got %q)", value)
	}
	return filter, nil
}

// TicketArchiveFilter selects the recent/archive window (FR-143).
type TicketArchiveFilter string

const (
	// TicketArchiveRecent is the default view: every active Ticket plus
	// terminal Tickets closed within the last TicketRecentWindowDays.
	TicketArchiveRecent TicketArchiveFilter = ""
	// TicketArchiveOnly returns only terminal Tickets past the window.
	TicketArchiveOnly TicketArchiveFilter = "archived"
	// TicketArchiveAll ignores the window entirely.
	TicketArchiveAll TicketArchiveFilter = "all"
)

func (f TicketArchiveFilter) Valid() bool {
	switch f {
	case TicketArchiveRecent, TicketArchiveOnly, TicketArchiveAll:
		return true
	}
	return false
}

// ParseTicketArchiveFilter validates an inbound archive filter value.
func ParseTicketArchiveFilter(value string) (TicketArchiveFilter, error) {
	filter := TicketArchiveFilter(strings.ToLower(strings.TrimSpace(value)))
	if !filter.Valid() {
		return "", invalidTicketField("archive", "archive must be one of archived, all (got %q)", value)
	}
	return filter, nil
}

// TicketRecentWindowDays is how long a Done or Cancelled Ticket stays in
// default views before moving behind the archive filter (FR-143).
//
// The boundary is inclusive: a Ticket closed exactly TicketRecentWindowDays
// ago is still recent, because the requirement is that it remains visible
// "for 14 calendar days" — day 14 is inside that promise, day 15 is not.
const TicketRecentWindowDays = 14

// TicketSortField names a deterministic ordering (FR-68).
type TicketSortField string

const (
	// TicketSortRank is the default: canonical state order, then the user's
	// manual rank within the state. This is the only ordering that reflects
	// deliberate arrangement, so it stays the default everywhere.
	TicketSortRank TicketSortField = ""
	// TicketSortPriority orders by priority (1 is most urgent).
	TicketSortPriority TicketSortField = "priority"
	// TicketSortDueDate orders by due date, with undated Tickets last
	// regardless of direction — "no due date" is absent information, not an
	// extreme value, so it never leads the list.
	TicketSortDueDate TicketSortField = "due_date"
	// TicketSortCreated orders by creation time.
	TicketSortCreated TicketSortField = "created"
	// TicketSortUpdated orders by last modification time.
	TicketSortUpdated TicketSortField = "updated"
	// TicketSortNumber orders by the workspace-local Ticket number.
	TicketSortNumber TicketSortField = "number"
)

func (f TicketSortField) Valid() bool {
	switch f {
	case TicketSortRank, TicketSortPriority, TicketSortDueDate,
		TicketSortCreated, TicketSortUpdated, TicketSortNumber:
		return true
	}
	return false
}

// ParseTicketSortField validates an inbound sort value.
func ParseTicketSortField(value string) (TicketSortField, error) {
	field := TicketSortField(strings.ToLower(strings.TrimSpace(value)))
	if !field.Valid() {
		return "", invalidTicketField("sort",
			"sort must be one of priority, due_date, created, updated, number (got %q)", value)
	}
	return field, nil
}

// TicketSearchMaxLength bounds the search term. A search is a filter, not a
// document query; an unbounded term is a way to make the server scan a huge
// string against every record for no useful result.
const TicketSearchMaxLength = 200

// TicketDefaultLimit and TicketMaxLimit bound how many Tickets one read can
// return (FR-68). The default is generous enough for an ordinary workspace to
// come back in a single call, and the cap keeps a large rolled-up tree from
// serializing thousands of records into the browser.
const (
	TicketDefaultLimit = 500
	TicketMaxLimit     = 2000
)

// NormalizeTicketSearch trims and bounds a search term.
func NormalizeTicketSearch(value string) (string, error) {
	term := strings.TrimSpace(value)
	if utf8.RuneCountInString(term) > TicketSearchMaxLength {
		return "", invalidTicketField("search", "search must be %d characters or fewer", TicketSearchMaxLength)
	}
	return term, nil
}

// ticketFilter is the compiled, normalized form of a TicketQuery's predicates.
// Compiling once per read keeps the per-Ticket test cheap: lowercasing the
// search term and building the tag/priority sets inside the scan loop would
// repeat that work for every record in the tree.
type ticketFilter struct {
	states     map[TicketState]struct{}
	tags       []string // normalized; a Ticket must carry ALL of them
	priorities map[int]struct{}
	assignees  map[string]struct{}
	sources    map[string]struct{}
	owners     map[string]struct{}
	due        TicketDueFilter
	archive    TicketArchiveFilter
	search     string // lowercased
	now        time.Time
	includeSub bool
	// unassignedOnly is set when the caller explicitly asked for Tickets with
	// no assignee, which cannot be expressed as a name match.
	unassignedOnly bool
}

// TicketAssigneeUnassigned is the sentinel an assignee filter uses to select
// Tickets with nobody assigned.
const TicketAssigneeUnassigned = "unassigned"

func compileTicketFilter(query TicketQuery, now time.Time) (*ticketFilter, error) {
	filter := &ticketFilter{
		due:        query.Due,
		archive:    query.Archive,
		now:        now,
		includeSub: query.IncludeSubtickets,
	}

	if len(query.States) > 0 {
		filter.states = make(map[TicketState]struct{}, len(query.States))
		for _, state := range query.States {
			if !state.Valid() {
				return nil, invalidTicketField("state", "unknown ticket state %q", string(state))
			}
			filter.states[state] = struct{}{}
		}
	}

	if len(query.Tags) > 0 {
		tags, err := ValidateWorkspaceTags(query.Tags)
		if err != nil {
			return nil, &TicketValidationError{Field: "tag", Message: err.Error()}
		}
		filter.tags = tags
	}

	if len(query.Priorities) > 0 {
		filter.priorities = make(map[int]struct{}, len(query.Priorities))
		for _, priority := range query.Priorities {
			if priority < TicketPriorityMin || priority > TicketPriorityMax {
				return nil, invalidTicketField("priority",
					"priority must be between %d and %d", TicketPriorityMin, TicketPriorityMax)
			}
			filter.priorities[priority] = struct{}{}
		}
	}

	if len(query.Assignees) > 0 {
		filter.assignees = make(map[string]struct{}, len(query.Assignees))
		for _, assignee := range query.Assignees {
			trimmed := strings.ToLower(strings.TrimSpace(assignee))
			if trimmed == "" {
				continue
			}
			if trimmed == TicketAssigneeUnassigned {
				filter.unassignedOnly = true
				continue
			}
			filter.assignees[trimmed] = struct{}{}
		}
	}

	if len(query.Sources) > 0 {
		filter.sources = make(map[string]struct{}, len(query.Sources))
		for _, source := range query.Sources {
			normalized, err := NormalizeTicketSource(source)
			if err != nil {
				return nil, &TicketValidationError{Field: "source", Message: err.Error()}
			}
			filter.sources[normalized] = struct{}{}
		}
	}

	if len(query.OwnerIDs) > 0 {
		filter.owners = make(map[string]struct{}, len(query.OwnerIDs))
		for _, owner := range query.OwnerIDs {
			if trimmed := strings.TrimSpace(owner); trimmed != "" {
				filter.owners[trimmed] = struct{}{}
			}
		}
	}

	if !filter.due.Valid() {
		return nil, invalidTicketField("due", "unknown due filter %q", string(filter.due))
	}
	if !filter.archive.Valid() {
		return nil, invalidTicketField("archive", "unknown archive filter %q", string(filter.archive))
	}

	search, err := NormalizeTicketSearch(query.Search)
	if err != nil {
		return nil, err
	}
	filter.search = strings.ToLower(search)

	return filter, nil
}

// matches reports whether one record satisfies every active predicate. All
// predicates are ANDed; within a single multi-valued predicate (states,
// priorities, assignees, sources, owners) membership is ORed, which is what
// makes a chip-style filter bar behave the way users expect.
func (f *ticketFilter) matches(task *Task, ownerID string) bool {
	if task.ParentTaskID != "" && !f.includeSub {
		return false
	}

	state := task.CanonicalState()
	if f.states != nil {
		if _, ok := f.states[state]; !ok {
			return false
		}
	}
	if !f.matchesArchiveWindow(task, state) {
		return false
	}
	if f.owners != nil {
		if _, ok := f.owners[ownerID]; !ok {
			return false
		}
	}
	if f.priorities != nil {
		if _, ok := f.priorities[task.Priority]; !ok {
			return false
		}
	}
	if f.sources != nil {
		if _, ok := f.sources[strings.ToLower(task.SourceType)]; !ok {
			return false
		}
	}
	if !f.matchesAssignee(task) {
		return false
	}
	if !f.matchesTags(task) {
		return false
	}
	if !f.matchesDue(task, state) {
		return false
	}
	return f.matchesSearch(task)
}

// matchesArchiveWindow applies the 14-day recent/archive default (FR-143).
// Active Tickets are always recent — only Done and Cancelled ever age out —
// and nothing here deletes or hides history, it only changes which view a
// closed Ticket appears in by default.
func (f *ticketFilter) matchesArchiveWindow(task *Task, state TicketState) bool {
	if f.archive == TicketArchiveAll {
		return true
	}
	if !state.Terminal() {
		// An open Ticket is never archived, so an archive-only read excludes it.
		return f.archive != TicketArchiveOnly
	}
	archived := ticketIsArchived(task, f.now)
	if f.archive == TicketArchiveOnly {
		return archived
	}
	return !archived
}

// ticketIsArchived reports whether a terminal Ticket has aged past the recent
// window. The comparison is in whole calendar days in UTC so the boundary is
// stable regardless of the hour a Ticket happened to close.
func ticketIsArchived(task *Task, now time.Time) bool {
	closed := ticketClosedAt(task)
	if closed.IsZero() {
		// A terminal Ticket with no recorded close time is treated as recent
		// rather than silently vanishing from the default view.
		return false
	}
	closedDay := closed.UTC().Truncate(24 * time.Hour)
	today := now.UTC().Truncate(24 * time.Hour)
	elapsed := int(today.Sub(closedDay) / (24 * time.Hour))
	return elapsed > TicketRecentWindowDays
}

// ticketClosedAt returns when the Ticket entered its current terminal state,
// preferring the audited history entry over the CompletedAt stamp because
// history is append-only and cannot be overwritten by a later edit.
func ticketClosedAt(task *Task) time.Time {
	for i := len(task.StateHistory) - 1; i >= 0; i-- {
		if task.StateHistory[i].To.Terminal() {
			return task.StateHistory[i].Timestamp
		}
	}
	if task.CompletedAt != nil {
		return *task.CompletedAt
	}
	return task.TicketUpdatedAt()
}

func (f *ticketFilter) matchesAssignee(task *Task) bool {
	if f.assignees == nil && !f.unassignedOnly {
		return true
	}
	assignee := strings.ToLower(strings.TrimSpace(task.To))
	unassigned := assignee == "" || assignee == TicketAssigneeUnassigned
	if unassigned {
		return f.unassignedOnly
	}
	if f.assignees == nil {
		return false
	}
	_, ok := f.assignees[assignee]
	return ok
}

// matchesTags requires every requested tag to be present. Narrowing by two
// tags should mean "both", not "either" — the OR reading makes each extra tag
// widen the result, which is the opposite of what a filter is for.
func (f *ticketFilter) matchesTags(task *Task) bool {
	if len(f.tags) == 0 {
		return true
	}
	for _, wanted := range f.tags {
		found := false
		for _, tag := range task.Tags {
			if strings.EqualFold(tag, wanted) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (f *ticketFilter) matchesDue(task *Task, state TicketState) bool {
	if f.due == TicketDueAny {
		return true
	}
	if task.DueDate == nil {
		return f.due == TicketDueNone
	}
	if f.due == TicketDueNone {
		return false
	}

	due := task.DueDate.UTC().Truncate(24 * time.Hour)
	today := f.now.UTC().Truncate(24 * time.Hour)
	switch f.due {
	case TicketDueOverdue:
		// Closed work cannot be late: a Done Ticket with a past due date is
		// history, not an outstanding problem.
		return !state.Terminal() && due.Before(today)
	case TicketDueToday:
		return due.Equal(today)
	case TicketDueWeek:
		return !due.Before(today) && due.Before(today.AddDate(0, 0, 7))
	}
	return true
}

// matchesSearch does a case-insensitive substring match over title and
// description. Deliberately not a tokenizer or ranker: this is a "find the
// ticket I am thinking of" filter, and a substring match is predictable in a
// way that partial-token scoring is not.
func (f *ticketFilter) matchesSearch(task *Task) bool {
	if f.search == "" {
		return true
	}
	if strings.Contains(strings.ToLower(task.Description), f.search) {
		return true
	}
	if strings.Contains(strings.ToLower(task.Details), f.search) {
		return true
	}
	// A user searching "#12" is looking for that Ticket by number.
	return strings.Contains(strings.ToLower(FormatTicketNumber(task.TicketNumber)), f.search)
}

// sortTicketsBy applies a deterministic ordering. Every comparison ends in a
// stable ID tiebreak so two Tickets that tie on the sort key never swap places
// between identical requests — an unstable list is unusable for reordering and
// makes tests flaky for no reason.
func sortTicketsBy(tickets []Ticket, field TicketSortField, descending bool) {
	stateOrder := make(map[TicketState]int, len(AllTicketStates))
	for i, state := range AllTicketStates {
		stateOrder[state] = i
	}

	// compare returns the ascending ordering of the sort KEY only: negative if
	// a sorts first, positive if b does, zero on a tie. Direction is applied
	// by the caller, and the stable ID tiebreak is applied after that — so a
	// descending sort is genuinely deterministic rather than merely reversed.
	compare := func(a, b Ticket) int {
		switch field {
		case TicketSortPriority:
			return cmpInt(a.Priority, b.Priority)
		case TicketSortDueDate:
			return cmpTimePtr(a.DueDate, b.DueDate)
		case TicketSortCreated:
			return cmpTime(a.CreatedAt, b.CreatedAt)
		case TicketSortUpdated:
			return cmpTime(a.UpdatedAt, b.UpdatedAt)
		case TicketSortNumber:
			return cmpInt64(a.Number, b.Number)
		default: // TicketSortRank
			if order := cmpInt(stateOrder[a.State], stateOrder[b.State]); order != 0 {
				return order
			}
			if rank := cmpInt64(a.StateRank, b.StateRank); rank != 0 {
				return rank
			}
			return cmpTime(a.CreatedAt, b.CreatedAt)
		}
	}

	sort.SliceStable(tickets, func(i, j int) bool {
		a, b := tickets[i], tickets[j]

		// Tickets with no due date sort last in BOTH directions. "No due date"
		// is absent information, not an extreme value, so flipping the sort
		// must not float them to the top — which is exactly what inverting a
		// plain less-than comparison would do.
		if field == TicketSortDueDate && (a.DueDate == nil) != (b.DueDate == nil) {
			return b.DueDate == nil
		}

		result := compare(a, b)
		if descending {
			result = -result
		}
		if result != 0 {
			return result < 0
		}
		return a.ID < b.ID
	})
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpTime(a, b time.Time) int {
	switch {
	case a.Before(b):
		return -1
	case a.After(b):
		return 1
	}
	return 0
}

// cmpTimePtr compares optional times. Nil placement is handled by the caller
// so it can stay direction-independent; here nil simply ties.
func cmpTimePtr(a, b *time.Time) int {
	if a == nil || b == nil {
		return 0
	}
	return cmpTime(*a, *b)
}

// normalizeTicketLimit clamps a requested page size into the supported range.
func normalizeTicketLimit(limit int) (int, error) {
	if limit < 0 {
		return 0, invalidTicketField("limit", "limit cannot be negative")
	}
	if limit == 0 {
		return TicketDefaultLimit, nil
	}
	if limit > TicketMaxLimit {
		return 0, invalidTicketField("limit", "limit must be %d or fewer", TicketMaxLimit)
	}
	return limit, nil
}

// TicketPage is a bounded collection read. Total reports how many Tickets
// matched before the limit was applied, so a client can tell the difference
// between "that is everything" and "there is more" (FR-133).
type TicketPage struct {
	Tickets   []Ticket `json:"tickets"`
	Total     int      `json:"total"`
	Truncated bool     `json:"truncated"`
	// PartialOwners lists descendant workspaces that could not be read during
	// a roll-up. The result is still returned — one unreadable child must not
	// break a parent's list — but the caller has to be able to say so rather
	// than present an incomplete list as complete (FR-133).
	PartialOwners []string `json:"partial_owners,omitempty"`
}

func (p TicketPage) String() string {
	return fmt.Sprintf("TicketPage{tickets:%d total:%d truncated:%t}", len(p.Tickets), p.Total, p.Truncated)
}
