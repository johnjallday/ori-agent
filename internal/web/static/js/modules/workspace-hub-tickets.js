/**
 * Workspace Hub Tickets
 *
 * The shared browser client for the canonical Ticket API
 * (tasks/prd-workspace-ticket-management.md). Every first-party surface —
 * List, Board, detail, Notes, Home, Action Center, command/search — talks to
 * tickets through this one module, so ownership, numbering, state vocabulary,
 * and error handling cannot drift apart between them.
 *
 * Three rules this module enforces on callers:
 *
 *  - Ownership is the route. Mutations address `owningWorkspaceId`, never the
 *    workspace whose list the ticket happened to appear in (FR-12, FR-90).
 *  - State moves only through `transition()`. There is deliberately no way to
 *    put `state` in an update payload (FR-88).
 *  - No mock data. Every render here is backed by a real server response.
 *
 * @module workspace-hub-tickets
 */
(function () {
  'use strict';

  /**
   * Canonical Ticket states in board order, with the presentation metadata
   * that must stay identical across List, Board, and detail (FR-6).
   *
   * `tone` names a semantic class rather than a color so state is never
   * communicated by color alone (FR-131); the stylesheet pairs each with a
   * distinct label and shape.
   */
  const TICKET_STATES = [
    { id: 'backlog', label: 'Backlog', tone: 'neutral', activeColumn: true },
    { id: 'ready', label: 'Ready', tone: 'info', activeColumn: true },
    { id: 'in_progress', label: 'In Progress', tone: 'active', activeColumn: true },
    { id: 'review', label: 'Review', tone: 'attention', activeColumn: true },
    { id: 'done', label: 'Done', tone: 'success', activeColumn: true },
    { id: 'cancelled', label: 'Cancelled', tone: 'muted', activeColumn: false }
  ];

  const STATE_BY_ID = new Map(TICKET_STATES.map(state => [state.id, state]));

  /** The two states a new Ticket may be created in (FR-19). */
  const CAPTURE_STATES = ['backlog', 'ready'];

  function stateMeta(stateId) {
    return (
      STATE_BY_ID.get(stateId) || { id: stateId, label: stateId || 'Unknown', tone: 'neutral' }
    );
  }

  function stateLabel(stateId) {
    return stateMeta(stateId).label;
  }

  /**
   * Renders the workspace-local ticket number. Returns '' for an unnumbered
   * record rather than '#0', so a partially-migrated ticket shows nothing
   * instead of a wrong number (FR-140).
   */
  function formatTicketNumber(number) {
    const value = Number(number);
    if (!Number.isFinite(value) || value <= 0) return '';
    return `#${value}`;
  }

  /**
   * Renders a ticket number with owning-workspace context, which is what
   * makes a rolled-up list unambiguous (FR-141).
   */
  function formatQualifiedNumber(ticket) {
    const base = formatTicketNumber(ticket && ticket.number);
    const owner = ticket && ticket.owningWorkspaceName;
    if (!base) return '';
    return owner ? `${owner} ${base}` : base;
  }

  /**
   * Normalizes a server Ticket into the shape the UI renders.
   *
   * The server already returns canonical fields; this converts snake_case to
   * camelCase and supplies safe defaults so a missing optional field can
   * never throw mid-render. It deliberately does NOT invent a state: an
   * unrecognized value passes through so the UI shows something wrong rather
   * than silently mislabeling a ticket as Backlog (FR-7).
   */
  function normalizeTicket(raw) {
    if (!raw || typeof raw !== 'object') return null;
    return {
      id: raw.id || '',
      number: Number(raw.number) || 0,
      displayNumber: raw.display_number || formatTicketNumber(raw.number),
      qualifiedNumber: raw.qualified_number || '',
      owningWorkspaceId: raw.owning_workspace_id || '',
      owningWorkspaceName: raw.owning_workspace_name || '',
      title: raw.title || '',
      description: raw.description || '',
      state: raw.state || '',
      stateLabel: raw.state_label || stateLabel(raw.state),
      stateRank: Number(raw.state_rank) || 0,
      tags: Array.isArray(raw.tags) ? raw.tags.slice() : [],
      priority: Number(raw.priority) || 0,
      dueDate: raw.due_date || null,
      referenceUrl: raw.reference_url || '',
      source: raw.source || '',
      sourceId: raw.source_id || '',
      linkedNoteIds: Array.isArray(raw.linked_note_ids) ? raw.linked_note_ids.slice() : [],
      assignee: raw.assignee || '',
      parentTicketId: raw.parent_ticket_id || '',
      dependsOnTicketIds: Array.isArray(raw.depends_on_ticket_ids)
        ? raw.depends_on_ticket_ids.slice()
        : [],
      scheduleEnabled: Boolean(raw.schedule_enabled),
      awaitingExecutionIntent: Boolean(raw.awaiting_execution_intent),
      currentRunId: raw.current_run_id || '',
      needsAttention: Boolean(raw.needs_attention),
      stateHistory: Array.isArray(raw.state_history) ? raw.state_history.slice() : [],
      legalTransitions: Array.isArray(raw.legal_transitions) ? raw.legal_transitions.slice() : [],
      // Hierarchy is detail-only; a list response leaves these empty (FR-142).
      parent: raw.parent ? normalizeTicketSummary(raw.parent) : null,
      subtickets: Array.isArray(raw.subtickets) ? raw.subtickets.map(normalizeTicketSummary) : [],
      archived: Boolean(raw.archived),
      startedAt: raw.started_at || null,
      completedAt: raw.completed_at || null,
      version: Number(raw.version) || 0,
      createdAt: raw.created_at || '',
      updatedAt: raw.updated_at || ''
    };
  }

  /** The compact form used for parent/subticket references. */
  function normalizeTicketSummary(raw) {
    if (!raw || typeof raw !== 'object') return null;
    return {
      id: raw.id || '',
      number: Number(raw.number) || 0,
      displayNumber: raw.display_number || formatTicketNumber(raw.number),
      title: raw.title || '',
      state: raw.state || '',
      stateLabel: raw.state_label || stateLabel(raw.state),
      owningWorkspaceId: raw.owning_workspace_id || '',
      owningWorkspaceName: raw.owning_workspace_name || ''
    };
  }

  function normalizeTicketList(rawList) {
    if (!Array.isArray(rawList)) return [];
    return rawList.map(normalizeTicket).filter(Boolean);
  }

  /**
   * An error carrying the canonical API envelope, so callers can react to the
   * specific failure — refresh on a 409 version conflict, highlight a field
   * on a 400, offer legal destinations on a refused transition (FR-94).
   */
  class TicketApiError extends Error {
    constructor(message, { status = 0, code = '', details = null } = {}) {
      super(message);
      this.name = 'TicketApiError';
      this.status = status;
      this.code = code;
      this.details = details;
    }

    /** True when another editor changed the ticket first (FR-93). */
    get isVersionConflict() {
      return this.status === 409 && Boolean(this.details && this.details.current);
    }

    /** True when the requested state move is not legal from the current one. */
    get isIllegalTransition() {
      return this.status === 409 && Boolean(this.details && this.details.legal_transitions);
    }

    /** The field a validation error points at, or '' when unspecified. */
    get field() {
      return (this.details && this.details.field) || '';
    }

    /** The server's current copy of the record, when it sent one. */
    get currentTicket() {
      return this.details && this.details.current ? normalizeTicket(this.details.current) : null;
    }
  }

  function ticketsBasePath(studioId) {
    return `/api/workspaces/${encodeURIComponent(studioId)}/tickets`;
  }

  async function request(method, url, body) {
    const fetchImpl = window.fetch;
    const options = { method, headers: { Accept: 'application/json' } };
    if (body !== undefined) {
      options.headers['Content-Type'] = 'application/json';
      options.body = JSON.stringify(body);
    }

    const response = await fetchImpl(url, options);
    let payload = null;
    try {
      payload = await response.json();
    } catch {
      payload = null;
    }

    if (!response.ok) {
      const message =
        (payload && (payload.message || payload.error)) || `Request failed (${response.status})`;
      throw new TicketApiError(message, {
        status: response.status,
        code: (payload && payload.code) || '',
        details: (payload && payload.details) || null
      });
    }
    return payload;
  }

  const api = {
    /**
     * Lists a workspace's tickets. `includeDescendants` opts into the
     * read-only roll-up; each returned ticket keeps its real owner (FR-89).
     */
    async list(studioId, options = {}) {
      const {
        states = [],
        tags = [],
        priorities = [],
        assignees = [],
        sources = [],
        owners = [],
        due = '',
        archive = '',
        search = '',
        sort = '',
        descending = false,
        limit = 0,
        includeDescendants = false,
        includeSubtickets = false
      } = options;

      // Every predicate goes to the server. Filtering in the browser would
      // mean downloading a whole descendant tree to display a few rows.
      const params = new URLSearchParams();
      const appendAll = (name, values) =>
        (values || []).filter(Boolean).forEach(value => params.append(name, String(value)));

      appendAll('state', states);
      appendAll('tag', tags);
      appendAll('priority', priorities);
      appendAll('assignee', assignees);
      appendAll('source', sources);
      appendAll('owner', owners);
      if (due) params.set('due', due);
      if (archive) params.set('archive', archive);
      if (search) params.set('search', search);
      if (sort) params.set('sort', sort);
      if (descending) params.set('desc', 'true');
      if (limit) params.set('limit', String(limit));
      if (includeDescendants) params.set('include_descendants', 'true');
      if (includeSubtickets) params.set('include_subtickets', 'true');

      const query = params.toString();
      const url = query ? `${ticketsBasePath(studioId)}?${query}` : ticketsBasePath(studioId);
      const payload = await request('GET', url);
      return {
        tickets: normalizeTicketList(payload && payload.tickets),
        count: (payload && payload.count) || 0,
        total: (payload && payload.total) || 0,
        truncated: Boolean(payload && payload.truncated),
        partialOwners: (payload && payload.partial_owners) || [],
        includeDescendants: Boolean(payload && payload.include_descendants)
      };
    },

    /**
     * Creates a ticket. `state` is required and must be 'backlog' or 'ready'
     * — the caller makes the capture choice explicit, the server never
     * guesses one (FR-19).
     */
    async create(studioId, input) {
      if (!CAPTURE_STATES.includes(input && input.state)) {
        throw new TicketApiError('Choose whether to add this to Backlog or create it Ready', {
          status: 400,
          code: 'validation_error',
          details: { field: 'state' }
        });
      }
      const payload = await request('POST', ticketsBasePath(studioId), {
        state: input.state,
        title: input.title,
        description: input.description || '',
        tags: input.tags || [],
        priority: input.priority || 0,
        due_date: input.dueDate || null,
        reference_url: input.referenceUrl || '',
        source: input.source || '',
        source_id: input.sourceId || '',
        linked_note_ids: input.linkedNoteIds || []
      });
      return normalizeTicket(payload);
    },

    async get(studioId, ticketId) {
      const payload = await request(
        'GET',
        `${ticketsBasePath(studioId)}/${encodeURIComponent(ticketId)}`
      );
      return normalizeTicket(payload);
    },

    /**
     * Edits ticket content. State is intentionally not accepted here — use
     * transition() (FR-88). `version` enables optimistic concurrency and
     * should always be the version the user was looking at (FR-93).
     */
    async update(studioId, ticketId, changes) {
      const body = {};
      if ('title' in changes) body.title = changes.title;
      if ('description' in changes) body.description = changes.description;
      if ('tags' in changes) body.tags = changes.tags;
      if ('priority' in changes) body.priority = changes.priority;
      if ('dueDate' in changes) body.due_date = changes.dueDate;
      if ('referenceUrl' in changes) body.reference_url = changes.referenceUrl;
      if ('linkedNoteIds' in changes) body.linked_note_ids = changes.linkedNoteIds;
      if ('version' in changes) body.version = changes.version;

      const payload = await request(
        'PATCH',
        `${ticketsBasePath(studioId)}/${encodeURIComponent(ticketId)}`,
        body
      );
      return normalizeTicket(payload);
    },

    /** The only way to change ticket state (FR-88). */
    async transition(studioId, ticketId, to, { reason = '', runId = '', version } = {}) {
      const body = { to, reason, run_id: runId };
      if (typeof version === 'number' && version > 0) body.version = version;
      const payload = await request(
        'POST',
        `${ticketsBasePath(studioId)}/${encodeURIComponent(ticketId)}/transition`,
        body
      );
      return normalizeTicket(payload);
    },

    /** Reorders within one owner and one state; all-or-nothing (FR-91). */
    async reorder(studioId, state, orderedIds) {
      const payload = await request('POST', `${ticketsBasePath(studioId)}/reorder`, {
        state,
        ordered_ids: orderedIds
      });
      return normalizeTicketList(payload && payload.tickets);
    },

    /**
     * Every execution attempt for one ticket, newest first (FR-27, FR-33).
     *
     * Runs are attempt-level records: a retry adds one, it never rewrites the
     * one before it. This is what lets detail show the whole story rather
     * than only the latest outcome.
     */
    async runs(studioId, ticketId) {
      const url =
        `/api/workspaces/${encodeURIComponent(studioId)}/runs` +
        `?ticket_id=${encodeURIComponent(ticketId)}`;
      const payload = await request('GET', url);
      const runs = (payload && (payload.runs || (payload.data && payload.data.runs))) || [];
      return runs
        .map(run => ({
          id: run.id || '',
          status: run.status || '',
          startedAt: run.started_at || run.created_at || '',
          finishedAt: run.finished_at || run.completed_at || '',
          error: run.error || ''
        }))
        .sort((a, b) => String(b.startedAt).localeCompare(String(a.startedAt)));
    },

    /**
     * The Notes this ticket references (FR-69).
     *
     * A ticket REFERENCES notes; it does not contain them. Only identity comes
     * back — never note bodies — so note content cannot leak into ticket
     * surfaces by accident (FR-79).
     */
    async linkedNotes(studioId, ticketId) {
      const payload = await request(
        'GET',
        `${ticketsBasePath(studioId)}/${encodeURIComponent(ticketId)}/notes`
      );
      return ((payload && payload.notes) || []).map(note => ({
        id: note.id || '',
        title: note.title || '',
        workspaceId: note.workspace_id || ''
      }));
    },

    /**
     * The workspace's notes, for the link picker (FR-17).
     *
     * Scoped to ONE workspace because a ticket may only link notes from its
     * own — there is deliberately no cross-workspace note search here.
     */
    async workspaceNotes(studioId) {
      const payload = await request('GET', `/api/workspaces/${encodeURIComponent(studioId)}/notes`);
      const notes = (payload && (payload.notes || payload.data || payload)) || [];
      if (!Array.isArray(notes)) return [];
      return notes
        .map(note => ({ id: note.id || '', title: note.name || note.title || '' }))
        .filter(note => note.id);
    },

    /** Attaches a note. Idempotent, so a double-click is harmless (FR-77). */
    async linkNote(studioId, ticketId, noteId, version) {
      const payload = await request(
        'POST',
        `${ticketsBasePath(studioId)}/${encodeURIComponent(ticketId)}/notes`,
        { note_id: noteId, version }
      );
      return normalizeTicket(payload);
    },

    /**
     * Detaches a note. This removes the REFERENCE and nothing else — the note
     * itself is never modified or deleted (FR-18).
     */
    async unlinkNote(studioId, ticketId, noteId, version) {
      const payload = await request(
        'DELETE',
        `${ticketsBasePath(studioId)}/${encodeURIComponent(ticketId)}/notes`,
        { note_id: noteId, version }
      );
      return normalizeTicket(payload);
    },

    /**
     * The tickets referencing a note — the reverse direction of the same
     * relationship (FR-75, FR-78). Summaries only: the note surface displays
     * and navigates to tickets, it never mutates them.
     */
    async ticketsForNote(studioId, noteId) {
      const url =
        `/api/workspaces/${encodeURIComponent(studioId)}/notes/` +
        `${encodeURIComponent(noteId)}/tickets`;
      const payload = await request('GET', url);
      return ((payload && payload.tickets) || []).map(normalizeTicketSummary).filter(Boolean);
    },

    /**
     * Creates a ticket seeded from a note (FR-73 to FR-76).
     *
     * `input` carries the REVIEWED values — the user edits the prefill before
     * anything is created, so this is a decision rather than an automatic
     * conversion. The note is preserved untouched, and the copy is one-time:
     * nothing here establishes ongoing synchronization.
     */
    async createFromNote(studioId, noteId, input) {
      if (!CAPTURE_STATES.includes(input && input.state)) {
        throw new TicketApiError('Choose whether to add this to Backlog or create it Ready', {
          status: 400,
          code: 'validation_error',
          details: { field: 'state' }
        });
      }
      const url =
        `/api/workspaces/${encodeURIComponent(studioId)}/notes/` +
        `${encodeURIComponent(noteId)}/tickets`;
      const payload = await request('POST', url, {
        state: input.state,
        title: input.title,
        description: input.description || '',
        tags: input.tags || [],
        priority: input.priority || 0,
        due_date: input.dueDate || null,
        reference_url: input.referenceUrl || ''
      });
      return normalizeTicket(payload);
    },

    async remove(studioId, ticketId, version) {
      const suffix = typeof version === 'number' && version > 0 ? `?version=${version}` : '';
      await request(
        'DELETE',
        `${ticketsBasePath(studioId)}/${encodeURIComponent(ticketId)}${suffix}`
      );
      return true;
    }
  };

  /**
   * True when the ticket may legally move to `state` right now. Callers use
   * this to render only legal actions instead of offering a move the server
   * will refuse (FR-70).
   */
  function canTransitionTo(ticket, state) {
    return Boolean(
      ticket && Array.isArray(ticket.legalTransitions) && ticket.legalTransitions.includes(state)
    );
  }

  /**
   * The legal destinations for a ticket, as {id, label} pairs ready for a
   * menu. Backlog promotion is labeled explicitly rather than as a bare state
   * name, because "Ready" alone does not read as a commitment (FR-51).
   */
  /**
   * Labels a transition by the INTENT it expresses, not by its destination
   * state. "Promote to Ready" and "Reopen" are the same `ready` transition,
   * but they mean different things to the person clicking, and a bare "Move to
   * Ready" would hide that difference (FR-51, FR-70).
   */
  function transitionLabel(fromState, toState) {
    const key = `${fromState}->${toState}`;
    const labels = {
      'backlog->ready': 'Promote to Ready',
      'backlog->cancelled': 'Cancel',
      'ready->backlog': 'Return to Backlog',
      'ready->in_progress': 'Start work',
      'ready->cancelled': 'Cancel',
      'in_progress->ready': 'Stop work',
      'in_progress->review': 'Send for review',
      'in_progress->cancelled': 'Cancel',
      'review->in_progress': 'Request changes',
      'review->done': 'Mark done',
      'review->cancelled': 'Cancel',
      'done->ready': 'Reopen',
      'cancelled->ready': 'Reopen'
    };
    return labels[key] || `Move to ${stateLabel(toState)}`;
  }

  function transitionOptions(ticket) {
    if (!ticket || !Array.isArray(ticket.legalTransitions)) return [];
    return ticket.legalTransitions.map(id => ({
      id,
      label: transitionLabel(ticket.state, id)
    }));
  }

  // --- Tickets destination view ------------------------------------------
  //
  // The canonical Tickets surface. It renders only server-returned records:
  // there is no local ticket cache that could survive a failed write and no
  // placeholder data, so anything on screen is something the server actually
  // persisted.

  const view = {
    elements: {},
    tickets: [],
    selectedTicketId: '',
    initialized: false,
    // Board vs List. Both render the same loaded tickets, so switching views
    // never re-queries and never shows different data (FR-58).
    boardMode: false,
    // The in-flight drag's complete prior position, for exact rollback.
    drag: null,
    // The active query. Held here rather than read out of the DOM at request
    // time so a filter can be changed programmatically (deep link, realtime
    // refresh) without depending on a control existing.
    filters: {
      states: [],
      search: '',
      sort: '',
      descending: false,
      archive: '',
      includeDescendants: false
    },
    lastPage: null
  };

  function el(id) {
    return typeof document === 'undefined' ? null : document.getElementById(id);
  }

  /**
   * The workspace whose tickets are on screen.
   *
   * Resolved from the URL first and the page objects second. Classic `defer`
   * scripts run before the page's module scripts populate their state, so a
   * module that trusts `WorkspaceHubState.selectedId` alone silently loads
   * nothing on first paint — a failure mode this codebase has shipped before.
   * The path segment is available immediately and is always correct on
   * /workspaces/{id}.
   */
  function currentStudioId() {
    if (typeof window !== 'undefined' && window.location) {
      const match = String(window.location.pathname || '').match(/^\/workspaces\/([^/]+)/);
      if (match && match[1]) return decodeURIComponent(match[1]);
    }
    const detail = typeof window !== 'undefined' ? window.workspaceDetail : null;
    if (detail && detail.workspaceId) return detail.workspaceId;
    const state = window.WorkspaceHubState && window.WorkspaceHubState.getState();
    return (state && state.selectedId) || '';
  }

  function setStatus(message, tone = 'info') {
    const node = view.elements.status;
    if (!node) return;
    // textContent, never innerHTML: server messages can quote user input, and
    // the existing escaping boundary must hold here too (FR-104).
    node.textContent = message || '';
    node.dataset.tone = message ? tone : '';
    node.hidden = !message;
  }

  function buildStateChip(ticket) {
    const meta = stateMeta(ticket.state);
    const chip = document.createElement('span');
    chip.className = `ticket-state-chip ticket-state-${meta.id}`;
    chip.dataset.tone = meta.tone;
    // The label is real text, not a color swatch, so state survives
    // greyscale, high-contrast mode, and screen readers (FR-131).
    chip.textContent = meta.label;
    return chip;
  }

  function buildTicketCard(ticket) {
    const card = document.createElement('li');
    card.className = 'ticket-card';
    card.dataset.ticketId = ticket.id;
    card.dataset.owningWorkspaceId = ticket.owningWorkspaceId;
    card.dataset.state = ticket.state;

    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'ticket-card-open';
    button.setAttribute(
      'aria-label',
      `Open ticket ${ticket.displayNumber || ticket.id}: ${ticket.title} (${ticket.stateLabel})`
    );

    const header = document.createElement('span');
    header.className = 'ticket-card-header';

    const number = document.createElement('span');
    number.className = 'ticket-card-number';
    number.textContent = ticket.displayNumber || '';
    header.appendChild(number);
    header.appendChild(buildStateChip(ticket));

    // Owner badge: a rolled-up ticket must say who owns it, because that is
    // the workspace any mutation has to be addressed to (FR-11, FR-59).
    if (view.filters.includeDescendants && ticket.owningWorkspaceName) {
      const owner = document.createElement('span');
      owner.className = 'ticket-card-owner';
      owner.textContent = ticket.owningWorkspaceName;
      header.appendChild(owner);
    }

    const title = document.createElement('span');
    title.className = 'ticket-card-title';
    title.textContent = ticket.title;

    button.appendChild(header);
    button.appendChild(title);

    const meta = document.createElement('span');
    meta.className = 'ticket-card-meta';

    // Every cue below is text, not color. Priority, overdue, and needs-
    // attention all have to survive greyscale and a screen reader (FR-131).
    if (ticket.priority) {
      const priority = document.createElement('span');
      priority.className = 'ticket-card-priority';
      priority.dataset.priority = String(ticket.priority);
      priority.textContent = `P${ticket.priority}`;
      meta.appendChild(priority);
    }
    if (ticket.dueDate) {
      const due = document.createElement('span');
      const overdue = isOverdue(ticket);
      due.className = 'ticket-card-due';
      if (overdue) due.dataset.overdue = 'true';
      due.textContent = overdue
        ? `Overdue ${formatDate(ticket.dueDate)}`
        : `Due ${formatDate(ticket.dueDate)}`;
      meta.appendChild(due);
    }
    if (ticket.needsAttention) {
      const attention = document.createElement('span');
      attention.className = 'ticket-card-attention';
      attention.textContent = 'Needs attention';
      meta.appendChild(attention);
    }
    if (ticket.archived) {
      const archived = document.createElement('span');
      archived.className = 'ticket-card-archived';
      archived.textContent = 'Archived';
      meta.appendChild(archived);
    }
    if (ticket.tags.length) {
      const tags = document.createElement('span');
      tags.className = 'ticket-card-tags';
      tags.textContent = ticket.tags.join(', ');
      meta.appendChild(tags);
    }
    if (meta.childNodes.length) button.appendChild(meta);

    button.addEventListener('click', () => {
      void openDetail(ticket.owningWorkspaceId, ticket.id);
    });
    card.appendChild(button);
    return card;
  }

  /** Whether an open ticket's due date has passed. Closed work is never late. */
  function isOverdue(ticket) {
    if (!ticket.dueDate || ticket.state === 'done' || ticket.state === 'cancelled') return false;
    const due = new Date(ticket.dueDate);
    if (Number.isNaN(due.getTime())) return false;
    const today = new Date();
    today.setHours(0, 0, 0, 0);
    return due < today;
  }

  function formatDate(value) {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return String(value);
    return date.toLocaleDateString();
  }

  function renderList() {
    const list = view.elements.list;
    const empty = view.elements.empty;
    if (!list) return;

    list.replaceChildren();
    if (!view.tickets.length) {
      if (empty) {
        empty.hidden = false;
        // The empty state has to distinguish "nothing here" from "nothing
        // matched", or a user hunting a filtered-out ticket concludes it was
        // deleted (FR-133).
        empty.textContent = hasActiveFilters()
          ? 'No tickets match these filters. Clear them to see everything.'
          : 'No tickets yet. Create one above to get started.';
      }
      updateCount();
      return;
    }
    if (empty) empty.hidden = true;

    view.tickets.forEach(ticket => list.appendChild(buildTicketCard(ticket)));
    updateCount();
  }

  // --- Ticket Board -------------------------------------------------------
  //
  // The Board is a second presentation of the SAME data the List renders, from
  // the same client call — not a second data path (FR-62 to FR-64). That is
  // what makes counts, filters, owners, and refreshed records incapable of
  // diverging between the two views.
  //
  // Columns are fixed and derived from canonical Ticket state (FR-43, FR-44).
  // There is deliberately no column editor: a user-defined column would be a
  // second lifecycle vocabulary, which is exactly what this feature removes.
  // Existing custom board configuration is left untouched on the workspace for
  // migration diagnostics and rollback (FR-52 to FR-55).

  /** The five active Board columns. Cancelled is excluded by default (FR-45). */
  const BOARD_COLUMNS = TICKET_STATES.filter(state => state.activeColumn);

  function renderBoard() {
    const board = view.elements.board;
    if (!board) return;

    board.replaceChildren();
    const byState = new Map(BOARD_COLUMNS.map(column => [column.id, []]));
    // Cancelled tickets have no default column; they stay reachable through
    // the List's state filter rather than being silently dropped from view.
    view.tickets.forEach(ticket => {
      const bucket = byState.get(ticket.state);
      if (bucket) bucket.push(ticket);
    });

    BOARD_COLUMNS.forEach(column => {
      board.appendChild(buildBoardColumn(column, byState.get(column.id) || []));
    });
    updateCount();
  }

  function buildBoardColumn(column, tickets) {
    const el = document.createElement('section');
    el.className = 'ticket-board-column';
    el.dataset.state = column.id;

    const header = document.createElement('header');
    header.className = 'ticket-board-column-header';

    const name = document.createElement('h4');
    name.className = 'ticket-board-column-title';
    name.id = `ticket-board-column-${column.id}`;
    name.textContent = column.label;

    const count = document.createElement('span');
    count.className = 'ticket-board-column-count';
    count.textContent = String(tickets.length);

    header.appendChild(name);
    header.appendChild(count);
    el.appendChild(header);

    const body = document.createElement('ul');
    body.className = 'ticket-board-column-body';
    body.dataset.state = column.id;
    // The column is a labelled list so a screen reader reads its programmatic
    // name and item count, not just a stack of cards (FR-128).
    body.setAttribute('aria-labelledby', name.id);
    body.setAttribute('aria-label', `${column.label}, ${tickets.length} tickets`);

    tickets.forEach(ticket => body.appendChild(buildBoardCard(ticket)));
    wireColumnDropTarget(body, column.id);
    el.appendChild(body);
    return el;
  }

  function buildBoardCard(ticket) {
    const card = buildTicketCard(ticket);
    card.classList.add('ticket-board-card');

    // A rolled-up ticket is owned elsewhere. Dragging it inside this board
    // would imply the parent can reorder another workspace's rank space, which
    // it cannot — so the gesture is disabled and the card says why (FR-91).
    const foreign =
      view.filters.includeDescendants && ticket.owningWorkspaceId !== currentStudioId();
    card.draggable = !foreign;
    if (foreign) {
      card.dataset.foreignOwner = 'true';
      card.title = `Owned by ${ticket.owningWorkspaceName}. Open it there to move it.`;
    }

    card.addEventListener('dragstart', event => {
      if (foreign) {
        event.preventDefault();
        setStatus(
          `${ticket.displayNumber} is owned by ${ticket.owningWorkspaceName}. Open that workspace to move it.`,
          'error'
        );
        return;
      }
      // A complete prior-position snapshot, so a rejected move can be undone
      // exactly rather than approximately (FR-49).
      view.drag = {
        ticket,
        fromState: ticket.state,
        fromColumn: card.parentElement,
        nextSibling: card.nextElementSibling
      };
      card.classList.add('is-dragging');
      if (event.dataTransfer) {
        event.dataTransfer.effectAllowed = 'move';
        event.dataTransfer.setData('text/plain', ticket.id);
      }
    });

    card.addEventListener('dragend', () => {
      card.classList.remove('is-dragging');
      clearDropHints();
    });

    card.appendChild(buildMoveMenu(ticket));
    return card;
  }

  /**
   * A keyboard-operable menu offering exactly the moves a drag could make
   * (FR-50). Drag and drop cannot be the only way to change state.
   */
  function buildMoveMenu(ticket) {
    const options = transitionOptions(ticket).filter(option =>
      BOARD_COLUMNS.some(column => column.id === option.id)
    );
    const wrap = document.createElement('div');
    wrap.className = 'ticket-card-move';
    if (!options.length) return wrap;

    const label = document.createElement('label');
    label.className = 'ticket-card-move-label';
    label.textContent = 'Move';
    const select = document.createElement('select');
    select.className = 'ticket-card-move-select';
    select.setAttribute(
      'aria-label',
      `Move ticket ${ticket.displayNumber} ${ticket.title} from ${ticket.stateLabel}`
    );

    const placeholder = document.createElement('option');
    placeholder.value = '';
    placeholder.textContent = 'Move to…';
    select.appendChild(placeholder);

    // Only legal destinations appear. An illegal one is never rendered
    // disabled-but-visible, because an option a user cannot pick is noise.
    options.forEach(option => {
      const el = document.createElement('option');
      el.value = option.id;
      el.textContent = option.label;
      select.appendChild(el);
    });

    select.addEventListener('change', () => {
      const to = select.value;
      select.value = '';
      if (to) void applyTransition(ticket, to);
    });
    select.addEventListener('click', event => event.stopPropagation());

    label.appendChild(select);
    wrap.appendChild(label);
    return wrap;
  }

  function clearDropHints() {
    if (typeof document === 'undefined') return;
    document
      .querySelectorAll(
        '.ticket-board-column-body.is-drop-target, .ticket-board-column-body.is-drop-blocked'
      )
      .forEach(el => el.classList.remove('is-drop-target', 'is-drop-blocked'));
  }

  function wireColumnDropTarget(body, state) {
    body.addEventListener('dragover', event => {
      const drag = view.drag;
      if (!drag) return;
      const legal = drag.fromState === state || canTransitionTo(drag.ticket, state);
      // Legality is previewed from the server's own legal_transitions, so the
      // hint cannot promise a move the server will refuse.
      body.classList.toggle('is-drop-target', legal);
      body.classList.toggle('is-drop-blocked', !legal);
      if (legal) {
        event.preventDefault();
        if (event.dataTransfer) event.dataTransfer.dropEffect = 'move';
      }
    });

    body.addEventListener('dragleave', () => {
      body.classList.remove('is-drop-target', 'is-drop-blocked');
    });

    body.addEventListener('drop', event => {
      event.preventDefault();
      clearDropHints();
      const drag = view.drag;
      view.drag = null;
      if (!drag || drag.fromState === state) return;
      void applyTransition(drag.ticket, state, drag);
    });
  }

  /** Puts a card back exactly where it started after a refused move (FR-49). */
  function restoreDragPosition(drag) {
    if (!drag || !drag.fromColumn) return;
    const card = view.elements.board
      ? view.elements.board.querySelector(`[data-ticket-id="${cssEscape(drag.ticket.id)}"]`)
      : null;
    if (!card) return;
    drag.fromColumn.insertBefore(card, drag.nextSibling || null);
  }

  /** Switches between List and Board. Both render the same loaded tickets. */
  function setBoardMode(enabled) {
    view.boardMode = Boolean(enabled);
    const { list, empty, board, listBtn, boardBtn } = view.elements;
    if (list) list.hidden = view.boardMode;
    if (empty && view.boardMode) empty.hidden = true;
    if (board) board.hidden = !view.boardMode;

    [
      [listBtn, !view.boardMode],
      [boardBtn, view.boardMode]
    ].forEach(([button, active]) => {
      if (!button) return;
      button.classList.toggle('is-active', active);
      button.setAttribute('aria-selected', active ? 'true' : 'false');
    });

    render();
  }

  /** Paints whichever view is active from the already-loaded tickets. */
  function render() {
    if (view.boardMode) renderBoard();
    else renderList();
  }

  function hasActiveFilters() {
    const f = view.filters;
    return Boolean(f.states.length || f.search || f.archive || f.includeDescendants);
  }

  /**
   * Paints the count, and says so when the server truncated the page or could
   * not read part of a roll-up — presenting a partial list as complete is how
   * a user concludes their work vanished.
   */
  function updateCount() {
    const node = view.elements.count;
    if (!node) return;
    const shown = view.tickets.length;
    const page = view.lastPage;
    let label = shown === 1 ? '1 ticket' : `${shown} tickets`;
    if (page && page.truncated) label = `${shown} of ${page.total} tickets`;
    if (page && page.partialOwners && page.partialOwners.length) {
      label += ` · ${page.partialOwners.length} workspace(s) unavailable`;
    }
    node.textContent = label;
  }

  /** Loads the selected workspace's tickets from the server. */
  async function load() {
    const studioId = currentStudioId();
    if (!studioId) {
      view.tickets = [];
      view.lastPage = null;
      render();
      setStatus('Select a workspace to see its tickets.');
      return;
    }

    setStatus('Loading tickets…');
    try {
      const result = await api.list(studioId, {
        states: view.filters.states,
        search: view.filters.search,
        sort: view.filters.sort,
        descending: view.filters.descending,
        archive: view.filters.archive,
        includeDescendants: view.filters.includeDescendants
      });
      view.tickets = result.tickets;
      view.lastPage = result;
      render();
      setStatus('');
    } catch (error) {
      view.tickets = [];
      view.lastPage = null;
      render();
      setStatus(`Could not load tickets: ${error.message}`, 'error');
    }
  }

  /** Opens the server-backed detail view for one ticket. */
  async function openDetail(owningWorkspaceId, ticketId) {
    const detail = view.elements.detail;
    if (!detail) return;

    view.selectedTicketId = ticketId;
    detail.hidden = false;
    if (view.elements.detailBody) {
      view.elements.detailBody.replaceChildren();
    }
    if (view.elements.detailTitle) {
      view.elements.detailTitle.textContent = 'Loading ticket…';
    }

    try {
      // Always re-fetch rather than reusing the list copy: detail is where a
      // user acts on a ticket, so it must show the server's current record
      // and current version, not a snapshot that may already be stale.
      const ticket = await api.get(owningWorkspaceId, ticketId);
      renderDetail(ticket);
    } catch (error) {
      if (view.elements.detailTitle) {
        view.elements.detailTitle.textContent = 'Could not load ticket';
      }
      setStatus(`Could not load ticket: ${error.message}`, 'error');
    }
  }

  function appendDetailRow(parent, label, value) {
    if (!value) return;
    const row = document.createElement('div');
    row.className = 'ticket-detail-row';

    const term = document.createElement('dt');
    term.textContent = label;
    const description = document.createElement('dd');
    description.textContent = value;

    row.appendChild(term);
    row.appendChild(description);
    parent.appendChild(row);
  }

  function appendSectionTitle(parent, text) {
    const title = document.createElement('h4');
    title.className = 'ticket-detail-section-title';
    title.textContent = text;
    parent.appendChild(title);
    return title;
  }

  /**
   * Ticket detail in three sections: Work (what is being asked for),
   * Execution (how it would run), and History (what has happened). Keeping
   * them separate is what stops a Run outcome from reading as the Ticket's
   * own state (FR-69, FR-70).
   */
  function renderDetail(ticket) {
    view.selectedTicket = ticket;
    if (view.elements.detailTitle) {
      const number = ticket.displayNumber ? `${ticket.displayNumber} ` : '';
      view.elements.detailTitle.textContent = `${number}${ticket.title}`;
    }

    const body = view.elements.detailBody;
    if (!body) return;
    body.replaceChildren();

    body.appendChild(buildActionRow(ticket));

    // --- Work ---
    appendSectionTitle(body, 'Work');
    const summary = document.createElement('dl');
    summary.className = 'ticket-detail-summary';
    appendDetailRow(summary, 'State', ticket.stateLabel + (ticket.archived ? ' (archived)' : ''));
    appendDetailRow(summary, 'Ticket', ticket.qualifiedNumber || ticket.displayNumber);
    appendDetailRow(summary, 'Owner', ticket.owningWorkspaceName || ticket.owningWorkspaceId);
    appendDetailRow(summary, 'Priority', ticket.priority ? `P${ticket.priority}` : '');
    appendDetailRow(summary, 'Tags', ticket.tags.join(', '));
    appendDetailRow(
      summary,
      'Due',
      ticket.dueDate ? `${formatDate(ticket.dueDate)}${isOverdue(ticket) ? ' — overdue' : ''}` : ''
    );
    appendDetailRow(summary, 'Reference', ticket.referenceUrl);
    appendDetailRow(summary, 'Source', ticket.source);
    body.appendChild(summary);

    if (ticket.description) {
      const description = document.createElement('pre');
      description.className = 'ticket-detail-description';
      // Rendered as plain text in V1. Markdown is stored raw and escaped at
      // render time; introducing an HTML renderer here would widen the trust
      // boundary this feature is required to leave unchanged (FR-104).
      description.textContent = ticket.description;
      body.appendChild(description);
    }

    // --- Hierarchy (detail only; the Board never nests cards, FR-142) ---
    if (ticket.parent || ticket.subtickets.length) {
      appendSectionTitle(body, 'Hierarchy');
      const hierarchy = document.createElement('ul');
      hierarchy.className = 'ticket-detail-hierarchy';
      if (ticket.parent) {
        hierarchy.appendChild(buildSummaryRow('Parent', ticket.parent));
      }
      ticket.subtickets.forEach(sub => hierarchy.appendChild(buildSummaryRow('Subticket', sub)));
      body.appendChild(hierarchy);
    }

    // --- Execution ---
    //
    // Deliberately separate from Work. What a Run did is attempt-level data;
    // it never restates the Ticket's own lifecycle state (FR-8, FR-61), which
    // is why "Latest run" and "State" are different rows in different
    // sections rather than one conflated status.
    appendSectionTitle(body, 'Execution');

    if (ticket.needsAttention) {
      const attention = document.createElement('p');
      attention.className = 'ticket-detail-attention';
      attention.setAttribute('role', 'status');
      // The Ticket is still open and still wanted — the attempt failed, not
      // the work (FR-32). The copy says so, and the retry path is the
      // "Send for review"/"Stop work" actions already offered above.
      attention.textContent =
        'The latest run needs attention. This ticket is still open — retry it or take it over manually.';
      body.appendChild(attention);
    }

    const execution = document.createElement('dl');
    execution.className = 'ticket-detail-summary';
    appendDetailRow(execution, 'Assignee', ticket.assignee || 'Unassigned');
    appendDetailRow(execution, 'Schedule', ticket.scheduleEnabled ? 'Enabled' : 'None');
    appendDetailRow(
      execution,
      'Waiting for intent',
      ticket.awaitingExecutionIntent ? 'Yes — no run starts until you start it' : ''
    );
    appendDetailRow(execution, 'Latest run', ticket.currentRunId || '');
    appendDetailRow(execution, 'Started', ticket.startedAt ? formatDate(ticket.startedAt) : '');
    appendDetailRow(execution, 'Closed', ticket.completedAt ? formatDate(ticket.completedAt) : '');
    body.appendChild(execution);

    // Run history loads separately and asynchronously: it is attempt-level
    // data on a different store, and a slow or failing runs endpoint must not
    // stop the Ticket itself from rendering.
    const runsHost = document.createElement('div');
    runsHost.className = 'ticket-detail-runs';
    body.appendChild(runsHost);
    void renderRunHistory(runsHost, ticket);

    // --- Linked Notes ---
    // Notes are independent knowledge records. This section navigates to them
    // and detaches references; it never edits or deletes a Note (FR-18).
    const notesHost = document.createElement('div');
    notesHost.className = 'ticket-detail-notes';
    body.appendChild(notesHost);
    void renderLinkedNotes(notesHost, ticket);

    // --- History ---
    if (ticket.stateHistory.length) {
      appendSectionTitle(body, 'History');
      const history = document.createElement('ol');
      history.className = 'ticket-detail-history';
      ticket.stateHistory.forEach(entry => {
        const item = document.createElement('li');
        const from = entry.from ? `${stateLabel(entry.from)} → ` : 'Created in ';
        const when = entry.timestamp ? ` · ${formatDate(entry.timestamp)}` : '';
        const why = entry.reason ? ` · ${entry.reason}` : '';
        item.textContent = `${from}${stateLabel(entry.to)} · ${entry.actor || 'system'}${when}${why}`;
        history.appendChild(item);
      });
      body.appendChild(history);
    }
  }

  /**
   * Lists every attempt for a Ticket, newest first. Run status is shown as
   * the Run's own outcome and never restated as the Ticket's state — that
   * separation is the whole point of having both (FR-8, FR-61).
   */
  async function renderRunHistory(host, ticket) {
    if (!host) return;
    host.replaceChildren();

    let runs = [];
    try {
      runs = await api.runs(ticket.owningWorkspaceId, ticket.id);
    } catch {
      const failed = document.createElement('p');
      failed.className = 'ticket-detail-empty';
      failed.textContent = 'Run history is unavailable right now.';
      host.appendChild(failed);
      return;
    }

    // A Ticket that has never run is normal, not an error state.
    if (!runs.length) return;

    appendSectionTitle(host, `Runs (${runs.length})`);
    const list = document.createElement('ol');
    list.className = 'ticket-detail-runs-list';
    runs.forEach((run, index) => {
      const item = document.createElement('li');
      item.className = 'ticket-detail-run';
      if (index === 0) item.dataset.latest = 'true';

      const status = document.createElement('span');
      status.className = 'ticket-detail-run-status';
      status.dataset.status = run.status;
      status.textContent = index === 0 ? `${run.status} (latest)` : run.status;

      const when = document.createElement('span');
      when.className = 'ticket-detail-run-when';
      when.textContent = run.startedAt ? formatDate(run.startedAt) : '';

      item.appendChild(status);
      item.appendChild(when);
      if (run.error) {
        const error = document.createElement('span');
        error.className = 'ticket-detail-run-error';
        error.textContent = run.error;
        item.appendChild(error);
      }
      list.appendChild(item);
    });
    host.appendChild(list);
  }

  /**
   * Lists the Notes a Ticket references, with a way to open each one and to
   * detach it (FR-69, FR-77).
   *
   * Unlinking removes the REFERENCE only. The copy says so, because "Remove"
   * next to a note is otherwise easy to read as "delete the note".
   */
  async function renderLinkedNotes(host, ticket) {
    if (!host) return;
    host.replaceChildren();

    let notes = [];
    try {
      notes = await api.linkedNotes(ticket.owningWorkspaceId, ticket.id);
    } catch {
      const failed = document.createElement('p');
      failed.className = 'ticket-detail-empty';
      failed.textContent = 'Linked notes are unavailable right now.';
      host.appendChild(failed);
      return;
    }
    appendSectionTitle(host, notes.length ? `Linked notes (${notes.length})` : 'Linked notes');
    if (!notes.length) {
      const none = document.createElement('p');
      none.className = 'ticket-detail-empty';
      none.textContent = 'No notes linked yet.';
      host.appendChild(none);
    }
    host.appendChild(buildNotePicker(ticket, notes));
    if (!notes.length) return;
    const list = document.createElement('ul');
    list.className = 'ticket-detail-notes-list';

    notes.forEach(note => {
      const item = document.createElement('li');
      item.className = 'ticket-detail-note';

      const link = document.createElement('a');
      link.className = 'ticket-detail-note-link';
      link.href = `/workspaces/${encodeURIComponent(note.workspaceId)}/notes/${encodeURIComponent(note.id)}`;
      link.textContent = note.title || note.id;

      const unlink = document.createElement('button');
      unlink.type = 'button';
      unlink.className = 'ticket-detail-note-unlink';
      unlink.textContent = 'Unlink';
      // Spelled out for screen readers, where the button label alone is
      // ambiguous about what gets removed.
      unlink.setAttribute(
        'aria-label',
        `Unlink note ${note.title} from this ticket. The note itself is kept.`
      );
      unlink.addEventListener('click', () => void handleUnlinkNote(ticket, note));

      item.appendChild(link);
      item.appendChild(unlink);
      list.appendChild(item);
    });

    host.appendChild(list);

    const reassurance = document.createElement('p');
    reassurance.className = 'ticket-detail-note-hint';
    reassurance.textContent = 'Unlinking removes the reference only. The note is kept.';
    host.appendChild(reassurance);
  }

  /**
   * A same-workspace note picker (FR-17, FR-77).
   *
   * Scoped to the ticket's OWN workspace: a ticket may only reference notes
   * from its own, so there is deliberately no cross-workspace search. Already
   * linked notes are filtered out rather than shown disabled — an option that
   * does nothing is noise.
   */
  function buildNotePicker(ticket, linked) {
    const wrap = document.createElement('div');
    wrap.className = 'ticket-note-picker';

    const label = document.createElement('label');
    label.className = 'ticket-note-picker-label';
    label.textContent = 'Link a note';

    const select = document.createElement('select');
    select.className = 'ticket-note-picker-select';
    select.setAttribute('aria-label', `Link a note to ticket ${ticket.displayNumber}`);
    const placeholder = document.createElement('option');
    placeholder.value = '';
    placeholder.textContent = 'Loading notes…';
    select.appendChild(placeholder);
    select.disabled = true;

    label.appendChild(select);
    wrap.appendChild(label);

    const linkedIds = new Set(linked.map(note => note.id));
    void (async () => {
      let available = [];
      try {
        available = await api.workspaceNotes(ticket.owningWorkspaceId);
      } catch {
        placeholder.textContent = 'Notes are unavailable';
        return;
      }
      const options = available.filter(note => !linkedIds.has(note.id));
      if (!options.length) {
        placeholder.textContent = available.length
          ? 'All notes are already linked'
          : 'No notes in this workspace';
        return;
      }

      placeholder.textContent = 'Choose a note…';
      options.forEach(note => {
        const option = document.createElement('option');
        option.value = note.id;
        option.textContent = note.title || note.id;
        select.appendChild(option);
      });
      select.disabled = false;
    })();

    select.addEventListener('change', () => {
      const noteId = select.value;
      select.value = '';
      if (noteId) void handleLinkNote(ticket, noteId);
    });

    return wrap;
  }

  async function handleLinkNote(ticket, noteId) {
    try {
      const updated = await api.linkNote(
        ticket.owningWorkspaceId,
        ticket.id,
        noteId,
        ticket.version
      );
      renderDetail(updated);
      setStatus('Note linked.', 'success');
      announce(`Note linked to ${ticket.displayNumber}`);
    } catch (error) {
      if (error.isVersionConflict && error.currentTicket) {
        renderDetail(error.currentTicket);
        setStatus('Someone else changed this ticket. Showing the current version.', 'error');
        return;
      }
      setStatus(error.message, 'error');
    }
  }

  async function handleUnlinkNote(ticket, note) {
    try {
      const updated = await api.unlinkNote(
        ticket.owningWorkspaceId,
        ticket.id,
        note.id,
        ticket.version
      );
      renderDetail(updated);
      setStatus(`Unlinked "${note.title}". The note is kept.`, 'success');
      announce(`Note ${note.title} unlinked from ${ticket.displayNumber}`);
    } catch (error) {
      if (error.isVersionConflict && error.currentTicket) {
        renderDetail(error.currentTicket);
        setStatus('Someone else changed this ticket. Showing the current version.', 'error');
        return;
      }
      setStatus(error.message, 'error');
    }
  }

  function buildSummaryRow(label, summary) {
    const item = document.createElement('li');
    item.className = 'ticket-detail-hierarchy-item';

    const role = document.createElement('span');
    role.className = 'ticket-detail-hierarchy-role';
    role.textContent = label;

    const link = document.createElement('button');
    link.type = 'button';
    link.className = 'ticket-detail-hierarchy-link';
    link.textContent = `${summary.displayNumber} ${summary.title} (${summary.stateLabel})`.trim();
    link.addEventListener('click', () => {
      void openDetail(summary.owningWorkspaceId, summary.id);
    });

    item.appendChild(role);
    item.appendChild(link);
    return item;
  }

  /**
   * Renders only the actions legal for this Ticket right now, derived from the
   * server's own legal_transitions (FR-70). Offering a move the server will
   * refuse teaches users to distrust the UI.
   *
   * Group 2 exposes the planning actions. Review, Done, Cancel, and Reopen are
   * execution-sensitive and land with the Run lifecycle in Group 4.
   */
  function buildActionRow(ticket) {
    const row = document.createElement('div');
    row.className = 'ticket-detail-actions';

    // "Mark done" is the ONLY path to Done and it exists only from Review.
    // That is what makes acceptance an explicit human act rather than
    // something a successful Run can claim on the user's behalf (FR-29, FR-30).
    transitionOptions(ticket).forEach(option => {
      const button = document.createElement('button');
      button.type = 'button';
      const primary = option.id === 'done' || (ticket.state === 'backlog' && option.id === 'ready');
      button.className = `modern-btn ${primary ? 'modern-btn-primary' : 'modern-btn-secondary'} modern-btn-sm ticket-action`;
      button.dataset.transition = option.id;
      button.textContent = option.label;
      button.addEventListener('click', () => void applyTransition(ticket, option.id));
      row.appendChild(button);
    });

    const remove = document.createElement('button');
    remove.type = 'button';
    remove.className =
      'modern-btn modern-btn-secondary modern-btn-sm ticket-action ticket-action-delete';
    remove.textContent = 'Delete';
    remove.addEventListener('click', () => void handleDelete(ticket));
    row.appendChild(remove);

    return row;
  }

  /**
   * Routes a state change through the canonical transition endpoint — the only
   * way state ever moves (FR-88). A board drag, a card menu, and a detail
   * button all land here, so none of them can develop their own lifecycle
   * rules.
   *
   * `drag` carries the card's prior position when the move came from a drag,
   * so a refusal restores it exactly (FR-49).
   */
  async function applyTransition(ticket, to, drag) {
    setStatus(`Moving ${ticket.displayNumber} to ${stateLabel(to)}…`);
    try {
      // Addressed to the OWNING workspace, not the one being viewed — this is
      // what makes a rolled-up ticket mutate in the right place (FR-12).
      const updated = await api.transition(ticket.owningWorkspaceId, ticket.id, to, {
        version: ticket.version
      });
      if (view.selectedTicketId === updated.id) renderDetail(updated);
      await load();
      setStatus(`${updated.displayNumber} is now ${updated.stateLabel}.`, 'success');
      announce(`${updated.displayNumber} moved to ${updated.stateLabel}`);
    } catch (error) {
      // Put the card back before doing anything else: leaving it in the
      // destination column after a refusal is a lie about persisted state.
      restoreDragPosition(drag);

      if (error.isVersionConflict && error.currentTicket) {
        if (view.selectedTicketId === error.currentTicket.id) renderDetail(error.currentTicket);
        setStatus('Someone else changed this ticket. Showing the current version.', 'error');
        announce(`Move refused: ${ticket.displayNumber} was changed by someone else`);
        await load();
        return;
      }
      if (error.isIllegalTransition) {
        const legal = (error.details.legal_transitions || []).map(stateLabel).join(', ');
        const message = legal
          ? `${ticket.displayNumber} cannot move to ${stateLabel(to)}. Allowed: ${legal}.`
          : error.message;
        setStatus(message, 'error');
        announce(message);
        await load();
        return;
      }
      setStatus(error.message, 'error');
      announce(`Move failed: ${error.message}`);
      await load();
    }
  }

  async function handleDelete(ticket) {
    const confirmed =
      typeof window !== 'undefined' && typeof window.confirm === 'function'
        ? window.confirm(
            `Delete ${ticket.displayNumber} "${ticket.title}"?\n\n` +
              'Linked notes and any subtickets are kept. This cannot be undone.'
          )
        : true;
    if (!confirmed) return;

    try {
      await api.remove(ticket.owningWorkspaceId, ticket.id, ticket.version);
      closeDetail();
      await load();
      setStatus(`Deleted ${ticket.displayNumber}.`, 'success');
      announce(`Ticket ${ticket.displayNumber} deleted`);
    } catch (error) {
      setStatus(error.message, 'error');
    }
  }

  /** Publishes a live-region announcement for assistive technology (FR-129). */
  function announce(message) {
    const node = view.elements.live;
    if (!node) return;
    node.textContent = message;
  }

  function closeDetail() {
    const openerId = view.selectedTicketId;
    view.selectedTicketId = '';
    view.selectedTicket = null;
    if (view.elements.detail) view.elements.detail.hidden = true;

    // Focus returns to the card that opened the detail, not to the top of the
    // list — a keyboard user who closes a panel should be back where they
    // were, not re-navigating from the start (FR-130).
    const list = view.elements.list;
    if (!list) return;
    const opener =
      (openerId &&
        list.querySelector(`[data-ticket-id="${cssEscape(openerId)}"] .ticket-card-open`)) ||
      (list.firstElementChild && list.firstElementChild.querySelector('.ticket-card-open'));
    if (opener && opener.focus) opener.focus();
  }

  /** Minimal attribute-selector escaping for IDs used in querySelector. */
  function cssEscape(value) {
    if (typeof CSS !== 'undefined' && CSS && typeof CSS.escape === 'function') {
      return CSS.escape(value);
    }
    return String(value).replace(/["\\]/g, '\\$&');
  }

  /** Handles the create form. The Backlog/Ready choice is required (FR-19). */
  async function handleCreate(event) {
    if (event && event.preventDefault) event.preventDefault();

    const studioId = currentStudioId();
    if (!studioId) {
      setStatus('Select a workspace before creating a ticket.', 'error');
      return;
    }

    const titleInput = view.elements.formTitle;
    const descriptionInput = view.elements.formDescription;
    const stateInput =
      typeof document !== 'undefined'
        ? document.querySelector('input[name="hubTicketCreateState"]:checked')
        : null;

    if (!stateInput) {
      setStatus('Choose whether to add this to Backlog or create it Ready.', 'error');
      return;
    }

    setStatus('Creating ticket…');
    try {
      const ticket = await api.create(studioId, {
        state: stateInput.value,
        title: titleInput ? titleInput.value : '',
        description: descriptionInput ? descriptionInput.value : ''
      });
      if (titleInput) titleInput.value = '';
      if (descriptionInput) descriptionInput.value = '';
      setStatus(`Created ${ticket.displayNumber} in ${ticket.stateLabel}.`, 'success');
      await load();
    } catch (error) {
      // The message names what the server actually refused, so the user can
      // fix it rather than guess (FR-94).
      setStatus(error.message, 'error');
      if (error.field && titleInput && error.field === 'title' && titleInput.focus) {
        titleInput.focus();
      }
    }
  }

  function init() {
    if (view.initialized || typeof document === 'undefined') return;
    // The surface lives at #workspace-detail-tickets-surface on the workspace
    // page. Resolve it by class as a fallback rather than a single hard-coded
    // id: binding to an id that the served markup does not use is a silent
    // no-op — the form simply never gets wired and every click does nothing.
    const container =
      el('workspace-detail-tickets-surface') ||
      (typeof document.querySelector === 'function'
        ? document.querySelector('.hub-tickets-container')
        : null);
    if (!container) return;

    view.elements = {
      container,
      list: el('hubTicketsList'),
      empty: el('hubTicketsEmpty'),
      count: el('hubTicketsCount'),
      status: el('hubTicketsStatus'),
      form: el('hubTicketCreateForm'),
      formTitle: el('hubTicketCreateTitle'),
      formDescription: el('hubTicketCreateDescription'),
      refresh: el('hubTicketsRefreshBtn'),
      detail: el('hubTicketDetail'),
      detailTitle: el('hubTicketDetailTitle'),
      detailBody: el('hubTicketDetailBody'),
      detailClose: el('hubTicketDetailClose'),
      live: el('hubTicketsLiveRegion'),
      filterBar: el('hubTicketsFilters'),
      search: el('hubTicketsSearch'),
      sort: el('hubTicketsSort'),
      archive: el('hubTicketsArchive'),
      rollUp: el('hubTicketsRollUp'),
      board: el('hubTicketsBoard'),
      listBtn: el('hubTicketsViewList'),
      boardBtn: el('hubTicketsViewBoard')
    };

    if (view.elements.form) {
      view.elements.form.addEventListener('submit', handleCreate);
    }
    if (view.elements.refresh) {
      view.elements.refresh.addEventListener('click', () => void load());
    }
    if (view.elements.detailClose) {
      view.elements.detailClose.addEventListener('click', closeDetail);
    }
    if (view.elements.listBtn) {
      view.elements.listBtn.addEventListener('click', () => setBoardMode(false));
    }
    if (view.elements.boardBtn) {
      view.elements.boardBtn.addEventListener('click', () => setBoardMode(true));
    }
    wireFilterControls();

    view.initialized = true;
    void openDeepLinkedTicket();
  }

  /**
   * The canonical Ticket deep-link contract: `?ticket=<stable id>` on a
   * workspace URL opens that Ticket's detail (FR-83, FR-84).
   *
   * The stable ID is the link key, never the display number — numbers are
   * workspace-local, so a number in a URL would resolve to the wrong Ticket in
   * a different workspace. Every first-party surface that links to a Ticket
   * (Note page, search, Map, saved links) builds this shape.
   */
  async function openDeepLinkedTicket() {
    if (typeof window === 'undefined' || !window.location) return;
    let params;
    try {
      params = new URLSearchParams(window.location.search);
    } catch {
      return;
    }

    const ticketId = params.get('ticket') || '';
    // `?tickets=<state>` opens the destination filtered to one state — the
    // shape every "Backlog", "Needs review", or count shortcut links to, so a
    // panel showing a number can hand the user the list behind it (FR-65,
    // FR-80, FR-81).
    const stateFilter = (params.get('tickets') || '').trim().toLowerCase();

    const studioId = currentStudioId();
    if (!studioId) return;
    if (!ticketId && !stateFilter) return;

    if (stateFilter && stateFilter !== 'all') {
      if (TICKET_STATES.some(state => state.id === stateFilter)) {
        view.filters.states = [stateFilter];
        paintFilterChips();
      }
    }

    // Load the list first so closing detail returns to a populated view rather
    // than an empty one.
    await load();
    if (!ticketId) return;
    try {
      await openDetail(studioId, ticketId);
    } catch {
      setStatus('That ticket could not be found in this workspace.', 'error');
    }
  }

  /**
   * Wires the filter bar. Each control mutates view.filters and re-queries the
   * server; nothing filters client-side, so a state chip and a search box
   * behave identically whether the workspace has ten tickets or ten thousand.
   */
  function wireFilterControls() {
    const { filterBar, search, sort, archive, rollUp } = view.elements;

    if (filterBar) {
      filterBar.addEventListener('click', event => {
        const chip = event.target.closest('[data-ticket-state-filter]');
        if (!chip) return;
        const value = chip.getAttribute('data-ticket-state-filter');
        if (value === 'all') {
          view.filters.states = [];
        } else {
          const active = new Set(view.filters.states);
          if (active.has(value)) active.delete(value);
          else active.add(value);
          view.filters.states = [...active];
        }
        paintFilterChips();
        void load();
      });
    }

    if (search) {
      // Debounced: one request per pause in typing, not one per keystroke.
      let timer = null;
      search.addEventListener('input', () => {
        if (timer) clearTimeout(timer);
        timer = setTimeout(() => {
          view.filters.search = search.value.trim();
          void load();
        }, 250);
      });
    }

    if (sort) {
      sort.addEventListener('change', () => {
        view.filters.sort = sort.value;
        void load();
      });
    }
    if (archive) {
      archive.addEventListener('change', () => {
        view.filters.archive = archive.checked ? 'archived' : '';
        void load();
      });
    }
    if (rollUp) {
      rollUp.addEventListener('change', () => {
        view.filters.includeDescendants = rollUp.checked;
        void load();
      });
    }

    paintFilterChips();
  }

  /**
   * Applies one state filter and reloads — the entry point a count shortcut
   * calls (FR-65, FR-80). Passing 'all' or nothing clears the filter.
   *
   * Exported rather than reached into: other surfaces drive the destination
   * through this, so they cannot develop their own idea of what "the Backlog
   * view" means.
   */
  function setFilterState(state) {
    init();
    const wanted = String(state || '')
      .trim()
      .toLowerCase();
    view.filters.states =
      wanted && wanted !== 'all' && TICKET_STATES.some(item => item.id === wanted) ? [wanted] : [];
    paintFilterChips();
    return load();
  }

  /**
   * Puts the cursor in the create form and preselects a capture state.
   *
   * This is where an "Add to Backlog" affordance from another surface lands:
   * the user asked to capture something, so they should arrive typing rather
   * than hunting for the field (FR-19, FR-80).
   */
  function focusCreate(state) {
    init();
    const wanted = String(state || '')
      .trim()
      .toLowerCase();
    if (CAPTURE_STATES.includes(wanted) && typeof document !== 'undefined') {
      const choice = document.querySelector(
        `input[name="hubTicketCreateState"][value="${wanted}"]`
      );
      if (choice) choice.checked = true;
    }
    const title = view.elements.formTitle;
    if (title && typeof title.focus === 'function') title.focus();
  }

  /** Reflects the active state filters onto the chips, including aria-pressed. */
  function paintFilterChips() {
    const bar = view.elements.filterBar;
    if (!bar || typeof bar.querySelectorAll !== 'function') return;
    const active = new Set(view.filters.states);
    bar.querySelectorAll('[data-ticket-state-filter]').forEach(chip => {
      const value = chip.getAttribute('data-ticket-state-filter');
      const on = value === 'all' ? active.size === 0 : active.has(value);
      chip.classList.toggle('is-active', on);
      chip.setAttribute('aria-pressed', on ? 'true' : 'false');
    });
  }

  if (typeof document !== 'undefined') {
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', init);
    } else {
      init();
    }
  }

  window.WorkspaceHubTickets = {
    TICKET_STATES,
    CAPTURE_STATES,
    TicketApiError,
    api,
    canTransitionTo,
    formatQualifiedNumber,
    formatTicketNumber,
    normalizeTicket,
    normalizeTicketList,
    stateLabel,
    stateMeta,
    transitionOptions,
    BOARD_COLUMNS,
    setFilterState,
    focusCreate,
    // View surface, exported so the page's view toggle and realtime refresh
    // can drive it without reaching into module internals.
    init,
    load,
    render,
    setBoardMode,
    openDetail,
    closeDetail
  };
})();
