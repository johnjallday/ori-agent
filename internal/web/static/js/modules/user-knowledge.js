const KNOWLEDGE_REQUEST_CONCURRENCY = 5;

function cleanText(value) {
  return String(value || '').trim();
}

function parseAssistantProgram(value) {
  if (!value) return {};
  if (typeof value === 'object') return value;
  try {
    const parsed = JSON.parse(String(value));
    return parsed && typeof parsed === 'object' ? parsed : {};
  } catch (_) {
    return {};
  }
}

function workspacePath(workspace, suffix = '') {
  const slug = cleanText(workspace?.folder_slug);
  if (!slug) return '';
  return `/workspaces/${encodeURIComponent(slug)}${suffix}`;
}

function profileDetailCount(profile) {
  if (!profile || typeof profile !== 'object') return 0;
  let count = 0;
  for (const key of ['display_name', 'email', 'timezone', 'locale', 'role_category', 'about']) {
    if (cleanText(profile[key])) count += 1;
  }
  if (Array.isArray(profile.specializations) && profile.specializations.some(cleanText)) count += 1;
  const preferences = profile.preferences || {};
  for (const key of ['response_style', 'units', 'language']) {
    if (cleanText(preferences[key])) count += 1;
  }
  return count;
}

export function activeKnowledgeWorkspaces(payload) {
  const raw = Array.isArray(payload)
    ? payload
    : Array.isArray(payload?.folders)
      ? payload.folders
      : Array.isArray(payload?.workspaces)
        ? payload.workspaces
        : [];
  const byID = new Map();
  for (const workspace of raw) {
    const id = cleanText(workspace?.id);
    const status = cleanText(workspace?.status).toLowerCase();
    if (
      !id ||
      cleanText(workspace?.kind).toLowerCase() === 'group' ||
      status === 'missing' ||
      status === 'trashed'
    ) {
      continue;
    }
    if (!byID.has(id)) byID.set(id, workspace);
  }
  return [...byID.values()].sort(
    (left, right) =>
      cleanText(left.name).localeCompare(cleanText(right.name)) ||
      cleanText(left.id).localeCompare(cleanText(right.id))
  );
}

export function assistantLearningSources(workspaces) {
  const byStation = new Map();
  for (const workspace of activeKnowledgeWorkspaces(workspaces)) {
    const envelope = parseAssistantProgram(workspace.assistant_program);
    const state = envelope.state && typeof envelope.state === 'object' ? envelope.state : null;
    const link = envelope.link && typeof envelope.link === 'object' ? envelope.link : null;
    const stationID = cleanText(state ? workspace.id : link?.station_workspace_id);
    if (!stationID || byStation.has(stationID)) continue;
    byStation.set(stationID, {
      stationID,
      workspaceID: cleanText(workspace.id),
      workspaceName: cleanText(workspace.name) || 'Workspace',
      workspaceSlug: cleanText(workspace.folder_slug),
      stationName:
        cleanText(state?.declaration?.station_name) ||
        cleanText(state?.declaration?.program_name) ||
        'Shared assistant',
      route: workspacePath(workspace, '/assistant')
    });
  }
  return [...byStation.values()].sort(
    (left, right) =>
      left.stationName.localeCompare(right.stationName) ||
      left.stationID.localeCompare(right.stationID)
  );
}

function normalizeMemoryEntries(payload) {
  return (Array.isArray(payload?.entries) ? payload.entries : [])
    .filter(entry => cleanText(entry?.text))
    .map(entry => ({
      index: Number(entry.index) || 0,
      type: cleanText(entry.type) || 'fact',
      date: cleanText(entry.date),
      provenance: cleanText(entry.provenance),
      text: cleanText(entry.text)
    }));
}

function currentLearningRevision(learning) {
  if (!learning || learning.deleted_at || !Array.isArray(learning.revisions)) return null;
  for (let index = learning.revisions.length - 1; index >= 0; index -= 1) {
    const revision = learning.revisions[index];
    if (revision && cleanText(revision.text)) return revision;
  }
  return null;
}

function evidenceCount(value) {
  return Array.isArray(value) ? value.length : 0;
}

export function buildUserKnowledgeView({
  profile = {},
  workspaces = [],
  memories = [],
  learningDocuments = [],
  failures = 0
} = {}) {
  const workspaceByID = new Map(activeKnowledgeWorkspaces(workspaces).map(item => [item.id, item]));
  const memoryByWorkspace = new Map(
    (Array.isArray(memories) ? memories : []).map(item => [
      cleanText(item?.workspaceID),
      item?.payload || {}
    ])
  );
  const workspaceScopes = [];
  let workspaceEntryCount = 0;

  for (const workspace of workspaceByID.values()) {
    const payload = memoryByWorkspace.get(cleanText(workspace.id)) || {};
    const entries = normalizeMemoryEntries(payload);
    workspaceEntryCount += entries.length;
    // MEMORY.md may also contain headings and free-form prose, but the runtime
    // deliberately does not inject those lines. This view reports only context
    // Ori actually receives, not every byte present in the source file.
    if (entries.length === 0) continue;
    workspaceScopes.push({
      workspaceID: cleanText(workspace.id),
      workspaceName: cleanText(workspace.name) || 'Workspace',
      workspaceSlug: cleanText(workspace.folder_slug),
      route: workspacePath(workspace, '?panel=settings'),
      entries,
      overBudget: payload.over_budget === true
    });
  }

  const approvedByID = new Map();
  const candidateByID = new Map();
  const documents = Array.isArray(learningDocuments) ? learningDocuments : [];
  for (const source of documents) {
    const documentState = source?.document || {};
    for (const candidate of Array.isArray(documentState.candidates)
      ? documentState.candidates
      : []) {
      const id = cleanText(candidate?.id);
      if (!id || candidate?.rejected_at || cleanText(candidate?.approved_learning_id)) continue;
      if (!candidateByID.has(id)) {
        candidateByID.set(id, {
          id,
          state: 'candidate',
          type: cleanText(candidate.type) || 'pattern',
          text: cleanText(candidate.text),
          confidence: cleanText(candidate.confidence),
          evidenceCount: evidenceCount(candidate.evidence),
          stationName: cleanText(source.stationName) || 'Shared assistant',
          route: cleanText(source.route)
        });
      }
    }
    for (const learning of Array.isArray(documentState.learnings) ? documentState.learnings : []) {
      const id = cleanText(learning?.id);
      const revision = currentLearningRevision(learning);
      if (!id || !revision || approvedByID.has(id)) continue;
      approvedByID.set(id, {
        id,
        state: 'approved',
        type: cleanText(revision.type) || 'pattern',
        text: cleanText(revision.text),
        confidence: cleanText(revision.confidence),
        evidenceCount: evidenceCount(revision.evidence),
        stationName: cleanText(source.stationName) || 'Shared assistant',
        route: cleanText(source.route)
      });
    }
  }

  // If a station endpoint is temporarily unavailable, the ordinary workspace
  // Memory response still exposes approved managed learnings. Use those as a
  // read-only fallback while keeping pending candidates fail-closed.
  for (const memory of Array.isArray(memories) ? memories : []) {
    const workspace = workspaceByID.get(cleanText(memory?.workspaceID));
    for (const learning of Array.isArray(memory?.payload?.managed_learnings)
      ? memory.payload.managed_learnings
      : []) {
      const id = cleanText(learning?.id);
      if (!id || !cleanText(learning?.text) || approvedByID.has(id)) continue;
      approvedByID.set(id, {
        id,
        state: 'approved',
        type: cleanText(learning.type) || 'pattern',
        text: cleanText(learning.text),
        confidence: cleanText(learning.confidence),
        evidenceCount: evidenceCount(learning.evidence),
        stationName: 'Shared assistant',
        route: workspace ? workspacePath(workspace, '/assistant') : ''
      });
    }
  }

  const sortKnowledge = (left, right) =>
    left.stationName.localeCompare(right.stationName) ||
    left.text.localeCompare(right.text) ||
    left.id.localeCompare(right.id);
  const candidates = [...candidateByID.values()].filter(item => item.text).sort(sortKnowledge);
  const approvedLearnings = [...approvedByID.values()]
    .filter(item => item.text)
    .sort(sortKnowledge);

  return {
    profile,
    workspaceScopes,
    candidates,
    approvedLearnings,
    failures: Math.max(0, Number(failures) || 0),
    stats: {
      globalDetails: profileDetailCount(profile),
      workspaceEntries: workspaceEntryCount,
      approvedLearnings: approvedLearnings.length,
      pendingCandidates: candidates.length
    }
  };
}

async function mapWithConcurrency(items, worker, concurrency = KNOWLEDGE_REQUEST_CONCURRENCY) {
  const output = new Array(items.length);
  let cursor = 0;
  const run = async () => {
    while (cursor < items.length) {
      const index = cursor;
      cursor += 1;
      try {
        output[index] = { status: 'fulfilled', value: await worker(items[index], index) };
      } catch (reason) {
        output[index] = { status: 'rejected', reason };
      }
    }
  };
  await Promise.all(Array.from({ length: Math.min(concurrency, items.length) }, run));
  return output;
}

async function responseJSON(fetchImpl, url) {
  const response = await fetchImpl(url, { headers: { Accept: 'application/json' } });
  if (!response.ok) throw new Error(`Request failed (${response.status})`);
  return response.json();
}

function makeElement(tagName, className, text) {
  const element = document.createElement(tagName);
  if (className) element.className = className;
  if (text !== undefined) element.textContent = text;
  return element;
}

function appendSourceLink(parent, item, label) {
  if (!item.route) return;
  const link = makeElement('a', 'user-knowledge-source-link', label);
  link.href = item.route;
  parent.append(link);
}

export class UserKnowledgeManager {
  constructor({ fetchImpl = globalThis.fetch?.bind(globalThis) } = {}) {
    this.fetchImpl = fetchImpl;
    this.snapshot = null;
  }

  init() {
    this.root = document.getElementById('userKnowledgeOverview');
    if (!this.root || typeof this.fetchImpl !== 'function') return;
    this.summary = document.getElementById('userKnowledgeSummary');
    this.workspaceList = document.getElementById('userKnowledgeWorkspaceList');
    this.learningList = document.getElementById('userKnowledgeLearningList');
    this.status = document.getElementById('userKnowledgeStatus');
    this.refreshButton = document.getElementById('refreshUserKnowledgeBtn');
    this.refreshButton?.addEventListener('click', () => this.load());
    document.addEventListener('ori:user-profile-updated', event => {
      if (!this.snapshot || !event.detail?.profile) return;
      this.snapshot.profile = event.detail.profile;
      this.render(buildUserKnowledgeView(this.snapshot));
    });
    void this.load();
  }

  async load() {
    this.setLoading(true);
    let failures = 0;
    let profile = {};
    let workspaces = [];
    const [profileResult, workspaceResult] = await Promise.allSettled([
      responseJSON(this.fetchImpl, '/api/user/profile'),
      responseJSON(this.fetchImpl, '/api/workspaces')
    ]);
    if (profileResult.status === 'fulfilled') profile = profileResult.value?.profile || {};
    else failures += 1;
    if (workspaceResult.status === 'fulfilled') {
      workspaces = activeKnowledgeWorkspaces(workspaceResult.value);
    } else {
      failures += 1;
    }

    const memoryResults = await mapWithConcurrency(workspaces, async workspace => ({
      workspaceID: workspace.id,
      payload: await responseJSON(
        this.fetchImpl,
        `/api/workspaces/${encodeURIComponent(workspace.id)}/memory`
      )
    }));
    const memories = memoryResults
      .filter(result => result.status === 'fulfilled')
      .map(result => result.value);
    failures += memoryResults.filter(result => result.status === 'rejected').length;

    const sources = assistantLearningSources(workspaces);
    const learningResults = await mapWithConcurrency(sources, async source => ({
      ...source,
      document: await responseJSON(
        this.fetchImpl,
        `/api/workspaces/${encodeURIComponent(source.workspaceID)}/assistant-program/learnings`
      )
    }));
    const learningDocuments = learningResults
      .filter(result => result.status === 'fulfilled')
      .map(result => result.value);
    failures += learningResults.filter(result => result.status === 'rejected').length;

    this.snapshot = { profile, workspaces, memories, learningDocuments, failures };
    this.render(buildUserKnowledgeView(this.snapshot));
    this.setLoading(false);
  }

  setLoading(loading) {
    this.root?.setAttribute('aria-busy', String(loading));
    if (this.refreshButton) this.refreshButton.disabled = loading;
    if (this.status && loading) {
      this.status.hidden = false;
      this.status.textContent = 'Gathering remembered context…';
      this.status.className = 'user-knowledge-status';
    }
  }

  render(view) {
    this.renderSummary(view.stats);
    this.renderWorkspaceScopes(view.workspaceScopes);
    this.renderLearnings(view.candidates, view.approvedLearnings);
    if (this.status) {
      this.status.hidden = view.failures === 0;
      this.status.textContent =
        view.failures > 0
          ? `${view.failures} knowledge source${view.failures === 1 ? '' : 's'} could not be read. The rest of this view is current.`
          : '';
      this.status.className = 'user-knowledge-status is-warning';
    }
  }

  renderSummary(stats) {
    if (!this.summary) return;
    this.summary.replaceChildren();
    const values = [
      ['Global details', stats.globalDetails],
      ['Workspace memories', stats.workspaceEntries],
      ['Reviewed learnings', stats.approvedLearnings],
      ['Needs review', stats.pendingCandidates]
    ];
    for (const [label, value] of values) {
      const item = makeElement('div', 'user-knowledge-stat');
      item.append(
        makeElement('strong', 'user-knowledge-stat-value', String(value)),
        makeElement('span', 'user-knowledge-stat-label', label)
      );
      this.summary.append(item);
    }
  }

  renderWorkspaceScopes(scopes) {
    if (!this.workspaceList) return;
    this.workspaceList.replaceChildren();
    if (scopes.length === 0) {
      this.workspaceList.append(
        makeElement(
          'div',
          'user-knowledge-empty',
          'No workspace-specific memories yet. Facts saved in a workspace will appear here without becoming global preferences.'
        )
      );
      return;
    }
    for (const scope of scopes) {
      const card = makeElement('article', 'user-knowledge-source-card');
      const header = makeElement('div', 'user-knowledge-source-header');
      const heading = makeElement('div');
      heading.append(
        makeElement('span', 'user-knowledge-scope-label', 'Workspace'),
        makeElement('h4', 'user-knowledge-source-title', scope.workspaceName)
      );
      header.append(heading);
      if (scope.overBudget) {
        header.append(makeElement('span', 'user-knowledge-warning-badge', 'Over prompt budget'));
      }
      card.append(header);
      const list = makeElement('ul', 'user-knowledge-memory-list');
      const visibleEntries = scope.entries.slice(0, 5);
      for (const entry of visibleEntries) {
        const row = makeElement('li', 'user-knowledge-memory-row');
        row.append(
          makeElement('span', 'user-knowledge-memory-type', entry.type),
          makeElement('span', 'user-knowledge-memory-text', entry.text)
        );
        list.append(row);
      }
      if (visibleEntries.length > 0) card.append(list);
      if (scope.entries.length > visibleEntries.length) {
        card.append(
          makeElement(
            'p',
            'user-knowledge-source-meta',
            `${scope.entries.length - visibleEntries.length} more curated memor${scope.entries.length - visibleEntries.length === 1 ? 'y' : 'ies'}`
          )
        );
      }
      appendSourceLink(card, scope, 'Review in workspace');
      this.workspaceList.append(card);
    }
  }

  renderLearnings(candidates, approved) {
    if (!this.learningList) return;
    this.learningList.replaceChildren();
    const items = [...candidates, ...approved];
    if (items.length === 0) {
      this.learningList.append(
        makeElement(
          'div',
          'user-knowledge-empty',
          'No shared assistant learnings yet. Repeated patterns stay out of context until they are reviewed and approved.'
        )
      );
      return;
    }
    for (const item of items) {
      const card = makeElement(
        'article',
        `user-knowledge-learning-card${item.state === 'candidate' ? ' is-candidate' : ''}`
      );
      const header = makeElement('div', 'user-knowledge-learning-header');
      header.append(
        makeElement(
          'span',
          `user-knowledge-learning-state is-${item.state}`,
          item.state === 'candidate' ? 'Needs review' : 'Reviewed learning'
        ),
        makeElement('span', 'user-knowledge-learning-confidence', item.confidence)
      );
      card.append(
        header,
        makeElement('p', 'user-knowledge-learning-text', item.text),
        makeElement(
          'p',
          'user-knowledge-source-meta',
          `${item.stationName} · ${item.evidenceCount} evidence item${item.evidenceCount === 1 ? '' : 's'}`
        )
      );
      appendSourceLink(
        card,
        item,
        item.state === 'candidate' ? 'Review proposal' : 'Open assistant home'
      );
      this.learningList.append(card);
    }
  }
}

if (typeof document !== 'undefined') {
  document.addEventListener('DOMContentLoaded', () => {
    if (document.getElementById('userKnowledgeOverview')) {
      new UserKnowledgeManager().init();
    }
  });
}
