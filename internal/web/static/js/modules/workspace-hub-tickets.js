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
      version: Number(raw.version) || 0,
      createdAt: raw.created_at || '',
      updatedAt: raw.updated_at || ''
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
    async list(
      studioId,
      { states = [], includeDescendants = false, includeSubtickets = false } = {}
    ) {
      const params = new URLSearchParams();
      states.filter(Boolean).forEach(state => params.append('state', state));
      if (includeDescendants) params.set('include_descendants', 'true');
      if (includeSubtickets) params.set('include_subtickets', 'true');

      const query = params.toString();
      const url = query ? `${ticketsBasePath(studioId)}?${query}` : ticketsBasePath(studioId);
      const payload = await request('GET', url);
      return {
        tickets: normalizeTicketList(payload && payload.tickets),
        count: (payload && payload.count) || 0,
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
  function transitionOptions(ticket) {
    if (!ticket || !Array.isArray(ticket.legalTransitions)) return [];
    return ticket.legalTransitions.map(id => {
      if (ticket.state === 'backlog' && id === 'ready') {
        return { id, label: 'Promote to Ready' };
      }
      return { id, label: `Move to ${stateLabel(id)}` };
    });
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
    initialized: false
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

    const title = document.createElement('span');
    title.className = 'ticket-card-title';
    title.textContent = ticket.title;

    button.appendChild(header);
    button.appendChild(title);

    if (ticket.tags.length) {
      const tags = document.createElement('span');
      tags.className = 'ticket-card-tags';
      tags.textContent = ticket.tags.join(', ');
      button.appendChild(tags);
    }

    button.addEventListener('click', () => {
      void openDetail(ticket.owningWorkspaceId, ticket.id);
    });
    card.appendChild(button);
    return card;
  }

  function renderList() {
    const list = view.elements.list;
    const empty = view.elements.empty;
    if (!list) return;

    list.replaceChildren();
    if (!view.tickets.length) {
      if (empty) empty.hidden = false;
      if (view.elements.count) view.elements.count.textContent = '0 tickets';
      return;
    }
    if (empty) empty.hidden = true;

    view.tickets.forEach(ticket => list.appendChild(buildTicketCard(ticket)));
    if (view.elements.count) {
      view.elements.count.textContent =
        view.tickets.length === 1 ? '1 ticket' : `${view.tickets.length} tickets`;
    }
  }

  /** Loads the selected workspace's tickets from the server. */
  async function load() {
    const studioId = currentStudioId();
    if (!studioId) {
      view.tickets = [];
      renderList();
      setStatus('Select a workspace to see its tickets.');
      return;
    }

    setStatus('Loading tickets…');
    try {
      const result = await api.list(studioId);
      view.tickets = result.tickets;
      renderList();
      setStatus('');
    } catch (error) {
      view.tickets = [];
      renderList();
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

  function renderDetail(ticket) {
    if (view.elements.detailTitle) {
      const number = ticket.displayNumber ? `${ticket.displayNumber} ` : '';
      view.elements.detailTitle.textContent = `${number}${ticket.title}`;
    }

    const body = view.elements.detailBody;
    if (!body) return;
    body.replaceChildren();

    const summary = document.createElement('dl');
    summary.className = 'ticket-detail-summary';
    appendDetailRow(summary, 'State', ticket.stateLabel);
    appendDetailRow(summary, 'Ticket', ticket.qualifiedNumber || ticket.displayNumber);
    appendDetailRow(summary, 'Owner', ticket.owningWorkspaceName || ticket.owningWorkspaceId);
    appendDetailRow(summary, 'Priority', ticket.priority ? String(ticket.priority) : '');
    appendDetailRow(summary, 'Tags', ticket.tags.join(', '));
    appendDetailRow(summary, 'Due', ticket.dueDate || '');
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

    if (ticket.stateHistory.length) {
      const historyTitle = document.createElement('h4');
      historyTitle.className = 'ticket-detail-section-title';
      historyTitle.textContent = 'History';
      body.appendChild(historyTitle);

      const history = document.createElement('ol');
      history.className = 'ticket-detail-history';
      ticket.stateHistory.forEach(entry => {
        const item = document.createElement('li');
        const from = entry.from ? `${stateLabel(entry.from)} → ` : 'Created in ';
        item.textContent = `${from}${stateLabel(entry.to)} · ${entry.actor || 'system'}`;
        history.appendChild(item);
      });
      body.appendChild(history);
    }
  }

  function closeDetail() {
    view.selectedTicketId = '';
    if (view.elements.detail) view.elements.detail.hidden = true;
    // Focus returns to the list so keyboard users are not dropped at the top
    // of the document after closing (FR-130).
    if (view.elements.list && view.elements.list.firstElementChild) {
      const button = view.elements.list.firstElementChild.querySelector('.ticket-card-open');
      if (button && button.focus) button.focus();
    }
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
      detailClose: el('hubTicketDetailClose')
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

    view.initialized = true;
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
    // View surface, exported so the hub's view toggle and realtime refresh
    // can drive it without reaching into module internals.
    init,
    load,
    openDetail,
    closeDetail
  };
})();
