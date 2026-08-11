package workspace

import (
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// TicketService is the single canonical entry point for Ticket creation,
// reads, updates, transitions, ordering, deletion, Note linking, and Run
// association (FR-85). New code never mutates Ticket lifecycle fields
// directly on a Task — it goes through here, so validation, ownership,
// numbering, ranking, versioning, history, and events cannot be bypassed by
// one caller and enforced by another.
//
// Two invariants shape every method below:
//
//   - Ownership is authority. Every mutation names the workspace that owns
//     the Ticket. A parent workspace may READ descendant Tickets via roll-up,
//     but it can never mutate one under its own identity (FR-9, FR-12).
//   - Writes are atomic. All persistence goes through store.Update, whose
//     mutation function only commits when it returns nil. A validation
//     failure part-way through therefore leaves the workspace untouched
//     (FR-94), and rank/number allocation cannot interleave with a concurrent
//     create on the same workspace.
type TicketService struct {
	store    Store
	eventBus *EventBus
	// notes resolves Note identity for linking and display. Optional; see
	// SetNoteLookup in ticket_notes.go for why it is an interface here.
	notes TicketNoteLookup
	// now is injectable so the 14-day recent/archive boundary (FR-143) can be
	// tested at exact day boundaries without sleeping or faking system time.
	now func() time.Time
}

// NewTicketService constructs a TicketService over the given store. The event
// bus is optional; wire it with SetEventBus.
func NewTicketService(store Store) *TicketService {
	return &TicketService{store: store, now: time.Now}
}

// SetEventBus wires canonical Ticket lifecycle event publication (FR-98).
// Optional; nil (the default) means no events are published.
func (s *TicketService) SetEventBus(bus *EventBus) {
	if s == nil {
		return
	}
	s.eventBus = bus
}

// SetClock overrides the service clock. Tests use it to pin the archive
// boundary; production never calls it.
func (s *TicketService) SetClock(now func() time.Time) {
	if s == nil || now == nil {
		return
	}
	s.now = now
}

func (s *TicketService) clock() time.Time {
	if s == nil || s.now == nil {
		return time.Now()
	}
	return s.now()
}

// --- Inputs ---------------------------------------------------------------

// TicketCreateInput describes a canonical Ticket creation request.
//
// State is REQUIRED and must be Backlog or Ready: FR-19 makes the choice
// between "Add to Backlog" and "Create Ready" explicit, so there is
// deliberately no default here. Quick-capture surfaces that want to default
// to Backlog pass it explicitly and label the result, rather than relying on
// a silent server-side default that would make the two paths
// indistinguishable.
type TicketCreateInput struct {
	WorkspaceID   string
	State         TicketState
	Title         string
	Description   string
	Tags          []string
	Priority      int
	DueDate       *time.Time
	ReferenceURL  string
	Source        string
	SourceID      string
	LinkedNoteIDs []string
	// Actor records who captured the work for the initial history entry.
	Actor   string
	ActorID string
}

// TicketUpdateInput describes a supported-field edit. Pointer fields
// distinguish "not sent" from "cleared" — nil leaves the field untouched.
//
// There is deliberately no field for state, ownership, number, provenance,
// stable ID, version, or any runtime/execution value. State changes go
// through Transition (FR-88); the rest are immutable or owned by the
// execution engine.
type TicketUpdateInput struct {
	Title         *string
	Description   *string
	Tags          *[]string
	Priority      *int
	DueDate       **time.Time
	ReferenceURL  *string
	LinkedNoteIDs *[]string
	// IfVersion enables optimistic concurrency (FR-93). When greater than
	// zero the update is refused with ErrTicketVersionConflict unless it
	// matches the stored version. Canonical HTTP routes always send it;
	// compatibility adapters may omit it, which is why zero is permitted here
	// rather than rejected outright.
	IfVersion int64
}

// TicketTransitionInput describes an explicit state change (FR-88).
type TicketTransitionInput struct {
	To        TicketState
	Actor     string
	ActorID   string
	Reason    string
	RunID     string
	IfVersion int64
}

// TicketQuery selects Tickets for a collection read. WorkspaceID is the
// requesting workspace and always the default scope; IncludeDescendants opts
// into the read-only roll-up (FR-89).
//
// Every predicate is applied on the server (see ticket_query.go). Filtering
// and sorting in the browser would force a rolled-up parent to download its
// entire descendant tree just to display a handful of rows (FR-68).
type TicketQuery struct {
	WorkspaceID        string
	IncludeDescendants bool
	// States filters to the given canonical states. Empty means every state,
	// subject to the recent/archive window below.
	States []TicketState
	// IncludeSubtickets includes child Tickets in the result. Collection
	// reads default to top-level Tickets only, matching the existing Backlog
	// list behavior; hierarchy is presented inside Ticket detail (FR-142).
	IncludeSubtickets bool

	// Filters (FR-59, FR-65). Multi-valued fields match any listed value,
	// except Tags, which requires all of them.
	Tags       []string
	Priorities []int
	Assignees  []string
	Sources    []string
	// OwnerIDs narrows a roll-up to specific owning workspaces. It cannot
	// widen scope: an owner outside the requested subtree simply matches
	// nothing.
	OwnerIDs []string
	Due      TicketDueFilter
	Archive  TicketArchiveFilter

	// Search matches title, description, and Ticket number (FR-67).
	Search string

	// Sort selects the ordering; the zero value is state-then-rank (FR-68).
	Sort           TicketSortField
	SortDescending bool

	// Limit bounds the returned page. Zero means TicketDefaultLimit.
	Limit int
}

// --- Reads ----------------------------------------------------------------

// WorkspaceName returns workspaceID's display name, or "" when it cannot be
// read. Used to attach owning-workspace identity to responses without a
// second full fetch.
func (s *TicketService) WorkspaceName(workspaceID string) string {
	ws, err := s.store.Get(workspaceID)
	if err != nil {
		return ""
	}
	return ws.Name
}

// Get returns one Ticket from its owning workspace. A ticketID that does not
// exist in workspaceID is reported as ErrTicketNotFound whether it is unknown
// or simply owned by someone else — a caller must never be able to use this
// route to discover another workspace's records (FR-9).
func (s *TicketService) Get(workspaceID, ticketID string) (*Ticket, error) {
	ws, err := s.store.Get(workspaceID)
	if err != nil {
		return nil, err
	}
	task, err := ws.GetTask(ticketID)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrTicketNotFound, ticketID)
	}
	ticket := NewTicket(task, ws.ID, ws.Name)
	ticket.Archived = ticket.State.Terminal() && ticketIsArchived(task, s.clock())
	attachTicketHierarchy(ws, task, &ticket)
	return &ticket, nil
}

// attachTicketHierarchy populates parent and subticket references for a
// detail read (FR-142). Hierarchy is a detail-only concern: the Board renders
// independent cards, so collection reads never carry these.
func attachTicketHierarchy(ws *Workspace, task *Task, ticket *Ticket) {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	if task.ParentTaskID != "" {
		for i := range ws.Tasks {
			if ws.Tasks[i].ID == task.ParentTaskID {
				parent := NewTicketSummary(&ws.Tasks[i], ws.ID, ws.Name)
				ticket.Parent = &parent
				break
			}
		}
	}

	for i := range ws.Tasks {
		if ws.Tasks[i].ParentTaskID != task.ID {
			continue
		}
		ticket.Subtickets = append(ticket.Subtickets, NewTicketSummary(&ws.Tasks[i], ws.ID, ws.Name))
	}
	// Preserve the author's declared subtask order, falling back to the stable
	// ID so two unordered subtickets never swap between reads.
	sort.SliceStable(ticket.Subtickets, func(i, j int) bool {
		a, b := ticket.Subtickets[i], ticket.Subtickets[j]
		if a.SubticketIndex != b.SubticketIndex {
			return a.SubticketIndex < b.SubticketIndex
		}
		return a.ID < b.ID
	})
}

// Promote is the explicit Backlog → Ready commitment transition (FR-22).
// It is a named wrapper over Transition rather than separate logic, so the
// legality check, rank assignment, history entry, and idempotence are exactly
// the same ones every other state change goes through.
//
// Promoting a Ticket that has already left Backlog returns the current record
// unchanged rather than erroring, so a repeated click or a race between two
// promoters is safe (FR-23).
func (s *TicketService) Promote(workspaceID, ticketID string, ifVersion int64) (*Ticket, error) {
	current, err := s.Get(workspaceID, ticketID)
	if err != nil {
		return nil, err
	}
	if current.State != TicketStateBacklog {
		return current, nil
	}
	return s.Transition(workspaceID, ticketID, TicketTransitionInput{
		To:        TicketStateReady,
		Actor:     TicketActorUser,
		IfVersion: ifVersion,
	})
}

// List returns the requesting workspace's Tickets, optionally rolling up
// every descendant workspace's Tickets too (FR-10, FR-89).
//
// Roll-up never copies a record or rewrites its WorkspaceID: each returned
// Ticket carries its real owner's ID and name so the UI can badge it and
// address mutations to the right route (FR-11, FR-90). An unreadable
// descendant is skipped rather than failing the whole roll-up — a partial
// result the caller can surface beats a parent workspace whose list breaks
// because one child is mid-write.
func (s *TicketService) List(query TicketQuery) ([]Ticket, error) {
	page, err := s.Search(query)
	if err != nil {
		return nil, err
	}
	return page.Tickets, nil
}

// Search is the full collection read: filters, search, sort, and bounded
// paging (FR-59, FR-65 through FR-68). List wraps it for callers that only
// want the records.
func (s *TicketService) Search(query TicketQuery) (TicketPage, error) {
	limit, err := normalizeTicketLimit(query.Limit)
	if err != nil {
		return TicketPage{}, err
	}
	if !query.Sort.Valid() {
		return TicketPage{}, invalidTicketField("sort", "unknown sort field %q", string(query.Sort))
	}
	filter, err := compileTicketFilter(query, s.clock())
	if err != nil {
		return TicketPage{}, err
	}

	ws, err := s.store.Get(query.WorkspaceID)
	if err != nil {
		return TicketPage{}, err
	}

	page := TicketPage{Tickets: localTickets(ws, filter)}
	if query.IncludeDescendants {
		descendantIDs, err := s.descendantWorkspaceIDs(query.WorkspaceID)
		if err != nil {
			return TicketPage{}, err
		}
		for _, id := range descendantIDs {
			child, err := s.store.Get(id)
			if err != nil {
				// One unreadable descendant must not break a parent's list.
				// Report it instead of silently returning a short result that
				// looks complete (FR-133).
				page.PartialOwners = append(page.PartialOwners, id)
				continue
			}
			page.Tickets = append(page.Tickets, localTickets(child, filter)...)
		}
	}

	sortTicketsBy(page.Tickets, query.Sort, query.SortDescending)

	page.Total = len(page.Tickets)
	if len(page.Tickets) > limit {
		page.Tickets = page.Tickets[:limit]
		page.Truncated = true
	}
	return page, nil
}

// localTickets projects one workspace's own matching Tickets. It holds the
// workspace read lock for the duration of the scan.
func localTickets(ws *Workspace, filter *ticketFilter) []Ticket {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	out := make([]Ticket, 0, len(ws.Tasks))
	for i := range ws.Tasks {
		task := &ws.Tasks[i]
		if !filter.matches(task, ws.ID) {
			continue
		}
		ticket := NewTicket(task, ws.ID, ws.Name)
		ticket.Archived = ticket.State.Terminal() && ticketIsArchived(task, filter.now)
		out = append(out, ticket)
	}
	return out
}

// descendantWorkspaceIDs returns every workspace transitively parented under
// rootID via a BFS over Workspace.ParentID. This is a read-only traversal:
// descendant ownership is never altered by appearing in a roll-up (FR-10).
func (s *TicketService) descendantWorkspaceIDs(rootID string) ([]string, error) {
	allIDs, err := s.store.List()
	if err != nil {
		return nil, err
	}
	childrenOf := make(map[string][]string, len(allIDs))
	for _, id := range allIDs {
		ws, err := s.store.Get(id)
		if err != nil {
			continue
		}
		if ws.ParentID != "" {
			childrenOf[ws.ParentID] = append(childrenOf[ws.ParentID], ws.ID)
		}
	}

	var out []string
	seen := map[string]bool{rootID: true}
	queue := append([]string(nil), childrenOf[rootID]...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		// Guard against a cyclic ParentID chain: a corrupted or hand-edited
		// workspace.json could otherwise spin this loop forever.
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
		queue = append(queue, childrenOf[id]...)
	}
	return out, nil
}

// --- Writes ---------------------------------------------------------------

// Create captures a new Ticket in the explicitly chosen state (FR-19).
//
// Number, rank, version, provenance, and the initial history entry are all
// assigned inside one store.Update so a concurrent create on the same
// workspace cannot hand out a duplicate number or rank (FR-140).
func (s *TicketService) Create(input TicketCreateInput) (*Ticket, error) {
	workspaceID := strings.TrimSpace(input.WorkspaceID)
	if workspaceID == "" {
		return nil, invalidTicketField("studio_id", "studio_id is required")
	}

	state := input.State
	if !state.Valid() {
		return nil, invalidTicketField("state", "creating a ticket requires an explicit state of backlog or ready")
	}
	// Creation is a capture decision, not a lifecycle shortcut: work cannot
	// be born already started, reviewed, done, or cancelled (FR-19, FR-36).
	if state != TicketStateBacklog && state != TicketStateReady {
		return nil, invalidTicketField("state", "a new ticket must be created in backlog or ready, not %s", state.Label())
	}

	task, err := buildTicketRecord(workspaceID, state, input)
	if err != nil {
		return nil, err
	}

	var created Task
	err = s.store.Update(workspaceID, func(ws *Workspace) error {
		task.TicketNumber = allocateTicketNumber(ws)
		task.StateRank = nextTicketRank(ws, state)
		if err := ws.AddTask(task); err != nil {
			return err
		}
		got, err := ws.GetTask(task.ID)
		if err != nil {
			return err
		}
		created = *got
		return nil
	})
	if err != nil {
		return nil, err
	}

	ticket := NewTicket(&created, created.WorkspaceID, s.WorkspaceName(created.WorkspaceID))
	s.publishTicket(EventTicketCreated, ticket, nil)
	// Legacy consumers (Details panel, drawer, Quest Board) still listen for
	// the old capture events; keep them fed during the compatibility window.
	s.publishLegacyTaskEvent(legacyCreateEventFor(state), created)
	return &ticket, nil
}

// buildTicketRecord validates a create request and constructs the record to
// persist. Everything that can be rejected is rejected here, before the store
// transaction opens, so an invalid request never takes a workspace lock.
func buildTicketRecord(workspaceID string, state TicketState, input TicketCreateInput) (Task, error) {
	title, err := NormalizeTicketTitle(input.Title)
	if err != nil {
		return Task{}, err
	}
	description, err := NormalizeTicketDescription(input.Description)
	if err != nil {
		return Task{}, err
	}
	tags, err := ValidateWorkspaceTags(input.Tags)
	if err != nil {
		return Task{}, &TicketValidationError{Field: "tags", Message: err.Error()}
	}
	priority, err := NormalizeTicketPriority(input.Priority)
	if err != nil {
		return Task{}, err
	}
	referenceURL, err := NormalizeReferenceURL(input.ReferenceURL)
	if err != nil {
		return Task{}, &TicketValidationError{Field: "reference_url", Message: err.Error()}
	}
	source, err := NormalizeTicketSource(input.Source)
	if err != nil {
		return Task{}, &TicketValidationError{Field: "source", Message: err.Error()}
	}

	now := time.Now().UTC()
	actor := strings.TrimSpace(input.Actor)
	if actor == "" {
		actor = TicketActorUser
	}

	task := Task{
		ID:            uuid.New().String(),
		WorkspaceID:   workspaceID,
		Description:   title,
		Details:       description,
		Tags:          tags,
		Priority:      priority,
		DueDate:       NormalizeTicketDueDate(input.DueDate),
		ReferenceURL:  referenceURL,
		SourceType:    source,
		SourceID:      strings.TrimSpace(input.SourceID),
		LinkedNoteIDs: NormalizeLinkedNoteIDs(input.LinkedNoteIDs),
		TicketState:   state,
		Status:        legacyStatusForTicketState(state, ""),
		TicketVersion: 1,
		CreatedAt:     now,
		UpdatedAt:     now,
		StateHistory: []TicketStateChange{{
			To:        state,
			Actor:     actor,
			ActorID:   strings.TrimSpace(input.ActorID),
			Timestamp: now,
		}},
	}

	if state == TicketStateReady {
		// Newly Ready work stays quiescent: no assignee, no schedule, no Run,
		// and background coordinator/scheduler sweeps skip it until an
		// explicit execution intent arrives (FR-24, FR-25).
		task.AwaitingExecutionIntent = true
	}
	if state == TicketStateBacklog {
		// Backlog safety invariants apply to every capture path, not just the
		// legacy Backlog service (FR-20).
		if err := ValidateBacklogTaskInvariants(&task); err != nil {
			return Task{}, &TicketValidationError{Field: "state", Message: err.Error()}
		}
	}
	return task, nil
}

// legacyCreateEventFor picks the legacy event name matching a canonical
// creation, so existing subscribers see the same shape they always did.
func legacyCreateEventFor(state TicketState) EventType {
	if state == TicketStateBacklog {
		return EventTaskBacklogCaptured
	}
	return EventTaskCreated
}

// allocateTicketNumber reserves the next workspace-local Ticket number
// (FR-140). It only ever moves forward, so deleting a Ticket does not free
// its number for reuse.
//
// The sequence is seeded from the highest number already present, which makes
// the allocator correct on a workspace whose records were numbered by
// migration (or by an older build) without requiring migration to have run
// first.
func allocateTicketNumber(ws *Workspace) int64 {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	highest := ws.TicketSequence
	for i := range ws.Tasks {
		if n := ws.Tasks[i].TicketNumber; n > highest {
			highest = n
		}
	}
	ws.TicketSequence = highest + 1
	return ws.TicketSequence
}

// nextTicketRank returns the rank for a Ticket entering `state`: one past the
// highest rank currently in that state, so it lands last (FR-15). Rank spaces
// are per workspace and per state, which is why the scan filters on both.
//
// It acquires the workspace read lock, so it must NOT be called from inside
// Workspace.MutateTask — that already holds the write lock and sync.RWMutex
// is not reentrant, so the nested acquire would deadlock. Transition paths
// call nextTicketRankLocked instead.
func nextTicketRank(ws *Workspace, state TicketState) int64 {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	return nextTicketRankLocked(ws, state)
}

// nextTicketRankLocked is nextTicketRank for callers that already hold the
// workspace lock (anything running inside MutateTask).
func nextTicketRankLocked(ws *Workspace, state TicketState) int64 {
	var highest int64
	for i := range ws.Tasks {
		task := &ws.Tasks[i]
		if task.CanonicalState() != state {
			continue
		}
		if task.StateRank > highest {
			highest = task.StateRank
		}
	}
	return highest + 1
}

// Update applies a supported-field edit to an existing Ticket (FR-92, FR-93).
// workspaceID must be the Ticket's real owner — a parent roll-up ID is
// rejected here, because the Ticket simply will not be found in the parent's
// record (FR-12).
func (s *TicketService) Update(workspaceID, ticketID string, input TicketUpdateInput) (*Ticket, error) {
	var updated Task
	err := s.store.Update(workspaceID, func(ws *Workspace) error {
		return mutateTicket(ws, ticketID, func(task *Task) error {
			if err := checkTicketVersion(task, input.IfVersion); err != nil {
				return err
			}
			if err := applyTicketUpdate(task, input); err != nil {
				return err
			}
			// A Backlog Ticket must still satisfy its safety invariants after
			// an edit, not just at capture time (FR-20).
			if task.CanonicalState() == TicketStateBacklog {
				if err := ValidateBacklogTaskInvariants(task); err != nil {
					return &TicketValidationError{Field: "state", Message: err.Error()}
				}
			}
			touchTicket(task)
			updated = *task
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	ticket := NewTicket(&updated, updated.WorkspaceID, s.WorkspaceName(updated.WorkspaceID))
	s.publishTicket(EventTicketUpdated, ticket, nil)
	return &ticket, nil
}

// applyTicketUpdate writes the supplied fields onto the record. It validates
// every field before touching the first one is not possible without a scratch
// copy, so instead each assignment happens only after its own validation
// passes and any failure aborts the enclosing store.Update — which discards
// the whole mutation rather than committing a partial edit (FR-94).
func applyTicketUpdate(task *Task, input TicketUpdateInput) error {
	if input.Title != nil {
		title, err := NormalizeTicketTitle(*input.Title)
		if err != nil {
			return err
		}
		task.Description = title
	}
	if input.Description != nil {
		description, err := NormalizeTicketDescription(*input.Description)
		if err != nil {
			return err
		}
		task.Details = description
	}
	if input.Tags != nil {
		tags, err := ValidateWorkspaceTags(*input.Tags)
		if err != nil {
			return &TicketValidationError{Field: "tags", Message: err.Error()}
		}
		task.Tags = tags
	}
	if input.Priority != nil {
		priority, err := NormalizeTicketPriority(*input.Priority)
		if err != nil {
			return err
		}
		task.Priority = priority
	}
	if input.DueDate != nil {
		task.DueDate = NormalizeTicketDueDate(*input.DueDate)
	}
	if input.ReferenceURL != nil {
		referenceURL, err := NormalizeReferenceURL(*input.ReferenceURL)
		if err != nil {
			return &TicketValidationError{Field: "reference_url", Message: err.Error()}
		}
		task.ReferenceURL = referenceURL
	}
	if input.LinkedNoteIDs != nil {
		// Membership validation against the workspace's Note store lands in
		// Group 5, which owns the Note relationship. Shape normalization is
		// safe to do here and keeps stored data clean in the meantime.
		task.LinkedNoteIDs = NormalizeLinkedNoteIDs(*input.LinkedNoteIDs)
	}
	return nil
}

// Transition performs an explicit state change (FR-88). It validates
// ownership, version, and the legal workflow, assigns a deterministic rank in
// the destination state (FR-15), and appends an audit entry (FR-40) — all
// inside one transaction, so a refused transition changes nothing.
func (s *TicketService) Transition(workspaceID, ticketID string, input TicketTransitionInput) (*Ticket, error) {
	if !input.To.Valid() {
		return nil, invalidTicketField("state", "unknown ticket state %q", string(input.To))
	}
	reason, err := NormalizeTicketReason(input.Reason)
	if err != nil {
		return nil, err
	}

	var (
		updated  Task
		previous TicketState
		noop     bool
	)
	err = s.store.Update(workspaceID, func(ws *Workspace) error {
		return mutateTicket(ws, ticketID, func(task *Task) error {
			if err := checkTicketVersion(task, input.IfVersion); err != nil {
				return err
			}
			previous = task.CanonicalState()
			// Requesting the state a Ticket is already in is idempotent
			// rather than an error, so a repeated client call or a race
			// between two promoters returns the current record instead of
			// failing the second caller (FR-23).
			if previous == input.To {
				noop = true
				updated = *task
				return nil
			}

			change := TicketStateChange{
				Actor:     strings.TrimSpace(input.Actor),
				ActorID:   strings.TrimSpace(input.ActorID),
				Reason:    reason,
				RunID:     strings.TrimSpace(input.RunID),
				Timestamp: s.clock().UTC(),
			}
			if err := task.TransitionTicket(input.To, change); err != nil {
				return err
			}
			// Locked variant: MutateTask already holds ws.mu.
			task.StateRank = nextTicketRankLocked(ws, input.To)
			applyTransitionSideEffects(task, input.To)

			if input.To == TicketStateBacklog {
				if err := ValidateBacklogTaskInvariants(task); err != nil {
					return &TicketValidationError{Field: "state", Message: err.Error()}
				}
			}
			touchTicket(task)
			updated = *task
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	ticket := NewTicket(&updated, updated.WorkspaceID, s.WorkspaceName(updated.WorkspaceID))
	if noop {
		return &ticket, nil
	}
	s.publishTicket(EventTicketStateChanged, ticket, map[string]any{
		"previous_state": string(previous),
		"next_state":     string(input.To),
		"actor":          ticketActorOrDefault(input.Actor),
		"actor_id":       strings.TrimSpace(input.ActorID),
		"reason":         reason,
		"run_id":         strings.TrimSpace(input.RunID),
	})
	if previous == TicketStateBacklog && input.To == TicketStateReady {
		s.publishLegacyTaskEvent(EventTaskBacklogPromoted, updated)
	}
	return &ticket, nil
}

// applyTransitionSideEffects keeps the record's non-lifecycle fields honest
// after a state change.
func applyTransitionSideEffects(task *Task, next TicketState) {
	switch next {
	case TicketStateReady:
		// Work arriving at Ready — freshly promoted, reopened, or stopped —
		// is quiescent until someone explicitly acts on it again (FR-24).
		//
		// This is also why reopening does NOT restore a schedule that
		// cancellation disabled: the Ticket comes back as work someone has to
		// choose to start, not as work that silently resumes running on a
		// timer the user thought they had stopped (FR-39).
		task.AwaitingExecutionIntent = true
		// BacklogRank belongs to the Backlog rank space only; leaving a stale
		// value behind would let it leak into a later Backlog ordering.
		task.BacklogRank = 0

	case TicketStateBacklog:
		// Returning to Backlog must satisfy the Backlog invariants, which
		// forbid a waiting-for-execution marker, a schedule, or a pending run.
		task.AwaitingExecutionIntent = false
		disableTicketSchedule(task)

	case TicketStateCancelled:
		// Cancelling stops future execution (FR-38). Leaving a schedule armed
		// on cancelled work is how a Ticket a user closed runs again next
		// morning. The schedule CONFIG is retained so the user can see what
		// was disabled and re-enable it deliberately.
		disableTicketSchedule(task)
	}
}

// disableTicketSchedule disarms future scheduled execution without discarding
// the schedule's configuration, so the decision is visible and reversible.
func disableTicketSchedule(task *Task) {
	task.ScheduleEnabled = false
	task.NextRun = nil
}

func ticketActorOrDefault(actor string) string {
	if trimmed := strings.TrimSpace(actor); trimmed != "" {
		return trimmed
	}
	return TicketActorSystem
}

// Reorder atomically assigns sequential ranks within exactly one owning
// workspace and one state (FR-91). Any unknown, duplicate, foreign-owner, or
// wrong-state ID fails the whole request without a partial rank change,
// because store.Update discards the mutation unless the function returns nil.
func (s *TicketService) Reorder(workspaceID string, state TicketState, orderedIDs []string) ([]Ticket, error) {
	if !state.Valid() {
		return nil, invalidTicketField("state", "unknown ticket state %q", string(state))
	}
	if len(orderedIDs) == 0 {
		return nil, invalidTicketField("ordered_ids", "ordered_ids must contain at least one ticket id")
	}

	err := s.store.Update(workspaceID, func(ws *Workspace) error {
		seen := make(map[string]bool, len(orderedIDs))
		for i, id := range orderedIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				return invalidTicketField("ordered_ids", "ordered_ids contains an empty ticket id")
			}
			if seen[id] {
				return invalidTicketField("ordered_ids", "duplicate ticket id in reorder request: %s", id)
			}
			seen[id] = true

			rank := int64(i + 1)
			if err := mutateTicket(ws, id, func(task *Task) error {
				if current := task.CanonicalState(); current != state {
					return invalidTicketField("ordered_ids",
						"ticket %s is in %s, not %s; reordering is scoped to one state",
						id, current.Label(), state.Label())
				}
				task.StateRank = rank
				touchTicket(task)
				return nil
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	tickets, err := s.List(TicketQuery{WorkspaceID: workspaceID, States: []TicketState{state}})
	if err != nil {
		return nil, err
	}
	s.publishRaw(EventTicketReordered, workspaceID, "", map[string]any{
		"studio_id":   workspaceID,
		"state":       string(state),
		"ordered_ids": orderedIDs,
	})
	return tickets, nil
}

// Delete removes a Ticket through the existing task-deletion safeguards
// (Workspace.DeleteTask), which also clean up layout and index entries.
//
// Linked Notes are deliberately untouched: deleting a Ticket unlinks it from
// its Notes but never deletes a Note (FR-18, FR-72).
func (s *TicketService) Delete(workspaceID, ticketID string, ifVersion int64) error {
	err := s.store.Update(workspaceID, func(ws *Workspace) error {
		task, err := ws.GetTask(ticketID)
		if err != nil {
			return fmt.Errorf("%w: %s", ErrTicketNotFound, ticketID)
		}
		if err := checkTicketVersion(task, ifVersion); err != nil {
			return err
		}
		return ws.DeleteTask(ticketID)
	})
	if err != nil {
		return err
	}

	s.publishRaw(EventTicketDeleted, workspaceID, ticketID, map[string]any{
		"ticket_id": ticketID,
		"studio_id": workspaceID,
	})
	s.publishRaw(EventTaskDeleted, workspaceID, ticketID, nil)
	return nil
}

// --- Shared helpers -------------------------------------------------------

// mutateTicket resolves ticketID inside ws and applies fn, translating a
// missing record into ErrTicketNotFound so every caller reports an unknown
// and a foreign ID identically.
func mutateTicket(ws *Workspace, ticketID string, fn func(*Task) error) error {
	err := ws.MutateTask(ticketID, fn)
	if err != nil && strings.Contains(err.Error(), "not found in workspace") {
		return fmt.Errorf("%w: %s", ErrTicketNotFound, ticketID)
	}
	return err
}

// checkTicketVersion enforces optimistic concurrency (FR-93). A zero
// ifVersion means the caller did not supply a token and the check is skipped;
// canonical HTTP routes always supply one.
func checkTicketVersion(task *Task, ifVersion int64) error {
	if ifVersion <= 0 {
		return nil
	}
	if task.TicketVersion != ifVersion {
		return fmt.Errorf("%w (expected version %d, found %d)",
			ErrTicketVersionConflict, ifVersion, task.TicketVersion)
	}
	return nil
}

// touchTicket bumps the concurrency token and last-modified stamp. Every
// canonical mutation calls it, which is what makes a stale IfVersion detect a
// concurrent edit rather than silently overwriting it.
func touchTicket(task *Task) {
	task.TicketVersion++
	task.UpdatedAt = time.Now().UTC()
}

// IsTicketNotFound reports whether err is (or wraps) ErrTicketNotFound.
func IsTicketNotFound(err error) bool { return errors.Is(err, ErrTicketNotFound) }

// IsTicketVersionConflict reports whether err is (or wraps)
// ErrTicketVersionConflict.
func IsTicketVersionConflict(err error) bool { return errors.Is(err, ErrTicketVersionConflict) }

// IsTicketValidationError reports whether err is (or wraps) a field-level
// validation failure, and returns it for field-aware error rendering.
func IsTicketValidationError(err error) (*TicketValidationError, bool) {
	var validationErr *TicketValidationError
	if errors.As(err, &validationErr) {
		return validationErr, true
	}
	return nil, false
}

// IsIllegalTicketTransition reports whether err is (or wraps) a refused
// transition, and returns it so the caller can name the legal destinations.
func IsIllegalTicketTransition(err error) (*IllegalTicketTransitionError, bool) {
	var transitionErr *IllegalTicketTransitionError
	if errors.As(err, &transitionErr) {
		return transitionErr, true
	}
	return nil, false
}

// --- Events ---------------------------------------------------------------

// publishTicket emits a canonical Ticket event carrying the identity every
// consumer needs: stable ID, owning workspace, display number, state, and
// version (FR-99).
func (s *TicketService) publishTicket(eventType EventType, ticket Ticket, extra map[string]any) {
	if s.eventBus == nil {
		return
	}
	data := map[string]any{
		"ticket_id":      ticket.ID,
		"studio_id":      ticket.OwningWorkspaceID,
		"ticket_number":  ticket.Number,
		"display_number": ticket.DisplayNumber,
		"title":          ticket.Title,
		"state":          string(ticket.State),
		"version":        ticket.Version,
		"timestamp":      s.clock().UTC().Format(time.RFC3339),
	}
	maps.Copy(data, extra)
	s.eventBus.Publish(NewTaskEvent(eventType, ticket.OwningWorkspaceID, ticket.ID, "", data))
}

func (s *TicketService) publishRaw(eventType EventType, workspaceID, ticketID string, data map[string]any) {
	if s.eventBus == nil {
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	if _, ok := data["timestamp"]; !ok {
		data["timestamp"] = s.clock().UTC().Format(time.RFC3339)
	}
	s.eventBus.Publish(NewTaskEvent(eventType, workspaceID, ticketID, "", data))
}

// publishLegacyTaskEvent keeps pre-Ticket subscribers working during the
// compatibility window (FR-98). It intentionally mirrors the payload shape
// BacklogService published, not the canonical one.
func (s *TicketService) publishLegacyTaskEvent(eventType EventType, task Task) {
	if s.eventBus == nil {
		return
	}
	s.eventBus.Publish(NewTaskEvent(eventType, task.WorkspaceID, task.ID, "", map[string]any{
		"description": task.Description,
		"status":      task.Status,
	}))
}
