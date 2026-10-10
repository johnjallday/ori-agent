// Metadata-only presentation. No provider, filesystem, setup or authority calls.
// Every label is text; an observed name/marker is never HTML, a route or consent.
const count = value => (Number.isInteger(value) && value >= 0 ? value : 0);
const plural = (value, noun, many = `${noun}s`) => `${value} ${value === 1 ? noun : many}`;

export function observationSummary(observation) {
  if (!observation) return '';
  const kinds = (observation.kinds || [])
    .map(kind => `${count(kind.count)} ${kind.name}`)
    .join(' · ');
  const partial = observation.coverage?.partial ? 'Partial look' : 'Bounded look';
  return `${partial}: ${plural(count(observation.files), 'file')} observed${kinds ? ` · ${kinds}` : ''}.`;
}

export function coverageSummary(observation, locale) {
  const coverage = observation?.coverage || {};
  const date = new Date(observation?.scanned_at || '');
  const when = Number.isNaN(date.getTime()) ? 'Unknown scan time' : date.toLocaleString(locale);
  return `${when}. Up to ${coverage.max_depth || 3} levels, ${coverage.max_entries || 5000} entries and ${coverage.budget_seconds || 3} seconds. Hidden/tooling folders, links and unreadable entries may be skipped; this is not a complete tree.`;
}

export function folderPresentation(
  observation,
  { local = false, historical = false, locale } = {}
) {
  if (!observation) return null;
  const date = new Date(observation.scanned_at || '');
  const projects = observation.projects || [];
  const children = projects.filter(project => !project.root);
  const root = projects.find(project => project.root);
  const coverage = observation.coverage || {};
  const foldersOmitted = count(coverage.projects_omitted);
  const kindsOmitted = count(coverage.kinds_omitted);
  const omissions = [
    foldersOmitted ? plural(foldersOmitted, 'folder summary', 'folder summaries') : '',
    kindsOmitted ? plural(kindsOmitted, 'file-kind summary', 'file-kind summaries') : ''
  ].filter(Boolean);
  return {
    heading: String(observation.folder || 'Attached folder'),
    date: Number.isNaN(date.getTime())
      ? 'Unknown snapshot date'
      : date.toLocaleDateString(locale, { year: 'numeric', month: 'short', day: 'numeric' }),
    scannedAt: Number.isNaN(date.getTime()) ? '' : date.toISOString(),
    status: `${local ? 'Local preview · not sent' : historical ? 'Saved observations · historical' : 'Saved observations'} · ${coverage.partial ? 'Partial, bounded look' : 'Bounded look'}`,
    disclosure: 'Attached folder: contents not read.',
    rootMarker: root?.marker ? `${String(root.marker)} marker in the whole folder` : '',
    visibleRows: children.slice(0, 3),
    moreRows: children.slice(3),
    moreLabel: `Show ${plural(Math.max(0, children.length - 3), 'more observed folder')}`,
    empty: children.length
      ? ''
      : count(observation.files) === 0
        ? 'No files or child folders recorded in this bounded snapshot.'
        : 'No child folders recorded in this bounded snapshot.',
    omission: omissions.length
      ? `${omissions.join(' and ')} omitted from this snapshot; not available in Show more.`
      : '',
    details: [
      observationSummary(observation),
      `${plural(count(observation.entries), 'entry', 'entries')} observed.`,
      ...(observation.tree
        ? [
            `${plural(observation.tree.nodes?.length || 0, 'tree entry', 'tree entries')} recorded; ${plural(count(observation.tree.omitted), 'more observed entry', 'more observed entries')} omitted from the tree. Tree + Chat displays only these recorded relationships.`
          ]
        : []),
      ...projects.map(
        project =>
          `${project.root ? 'Whole folder' : 'Observed folder'} “${String(project.name)}”: ${plural(count(project.files), 'file')} observed.`
      ),
      'Parent and child counts overlap; do not add them as independent totals.',
      coverageSummary(observation, locale),
      `${plural(count(coverage.skipped_links), 'link')} skipped.${coverage.partial_reason ? ` Scan stopped at the ${coverage.partial_reason === 'time' ? 'time' : 'entry'} limit.` : ''}`,
      'Markers do not prove file contents, project progress or available integrations.'
    ]
  };
}

// Latest atomic folder turn only. Event-only reviews, detached revisions,
// imported and legacy prose never manufacture a discussion binding.
export function folderDiscussionBinding(messages, conversationId, saved) {
  if (
    !conversationId ||
    !saved?.revision ||
    !saved.observation?.id ||
    !Array.isArray(messages) ||
    messages.length < 3
  )
    return null;
  const [event, user, answer] = messages.slice(-3);
  if (
    !event ||
    !user ||
    !answer ||
    event.imported ||
    user.imported ||
    answer.imported ||
    event.role !== 'folder_context' ||
    event.id !== saved.revision ||
    event.folder_context?.version !== 1 ||
    event.folder_context.observation?.id !== saved.observation.id ||
    user.role !== 'user' ||
    answer.role !== 'assistant' ||
    !answer.id ||
    !answer.content?.trim()
  )
    return null;
  return {
    conversationId,
    revision: saved.revision,
    observationId: saved.observation.id,
    messageId: answer.id
  };
}

export function currentFolderDiscussion(state, binding, conversationId) {
  return Boolean(
    binding &&
    state &&
    !state.preview &&
    !state.pending &&
    !state.selecting &&
    binding.conversationId === conversationId &&
    state.conversationId === conversationId &&
    binding.revision === state.revision &&
    binding.observationId === state.observation?.id &&
    binding.generation === state.generation &&
    binding.messageId
  );
}

export function folderDiscussionOptions(observation) {
  const children = (observation?.projects || []).filter(project => !project.root);
  return children.map(project => ({
    id: project.id,
    label: `${String(project.name)}${project.marker ? ` · ${String(project.marker)} marker` : ''}`,
    ambiguous:
      children.filter(
        other =>
          other.name === project.name && String(other.marker || '') === String(project.marker || '')
      ).length > 1
  }));
}

export function folderDiscussionText(observation, projectId = '', { historical = false } = {}) {
  if (!observation) return '';
  const source = historical ? 'saved metadata observations' : 'metadata observations';
  if (!projectId)
    return `Let’s discuss the whole folder “${String(observation.folder)}” from its ${source}, without setting anything up.`;
  const project = (observation.projects || []).find(row => !row.root && row.id === projectId);
  if (!project || folderDiscussionOptions(observation).find(row => row.id === projectId)?.ambiguous)
    return '';
  return `Let’s discuss the observed folder “${String(project.name)}”${project.marker ? ` (marker: “${String(project.marker)}”)` : ''} in “${String(observation.folder)}” from its ${source}, without setting anything up.`;
}

/** Build semantic rows/disclosures using text nodes only. Shared by local
 * preview and saved canonical events; the caller retains identity/permissions. */
export function renderFolderSummary(container, observation, options = {}) {
  const view = folderPresentation(observation, options);
  if (!container || !view) return;
  const document = container.ownerDocument;
  const node = (tag, text, className = '') => {
    const element = document.createElement(tag);
    element.textContent = text;
    element.className = className;
    return element;
  };
  const rows = projects => {
    const list = node('ul', '', 'personal-assistant-folder-context__rows');
    for (const project of projects) {
      const row = node('li', '');
      row.append(
        node('span', String(project.name)),
        node(
          'span',
          project.marker ? `${String(project.marker)} marker` : 'Observed folder',
          'personal-assistant-folder-context__marker'
        )
      );
      list.append(row);
    }
    return list;
  };
  container.replaceChildren(node('h3', view.heading));
  const meta = node('p', '', 'personal-assistant-folder-context__note');
  const time = node('time', view.date);
  if (view.scannedAt) time.dateTime = view.scannedAt;
  meta.append(
    'Snapshot · ',
    time,
    ' · ',
    node('span', view.status, 'personal-assistant-folder-context__status')
  );
  container.append(meta, node('p', view.disclosure));
  const details = node('details', '', 'personal-assistant-folder-context__details');
  details.append(node('summary', 'Scan details'));
  if (view.rootMarker)
    details.append(node('p', view.rootMarker, 'personal-assistant-folder-context__note'));
  if (view.visibleRows.length) details.append(rows(view.visibleRows));
  if (view.empty)
    container.append(node('p', view.empty, 'personal-assistant-folder-context__note'));
  if (view.moreRows.length) {
    const more = node('details', '', 'personal-assistant-folder-context__more');
    more.append(node('summary', view.moreLabel), rows(view.moreRows));
    details.append(more);
  }
  if (view.omission)
    container.append(node('p', view.omission, 'personal-assistant-folder-context__note'));
  for (const line of view.details) details.append(node('p', line));
  if (options.local)
    details.append(
      node(
        'p',
        'Send shares the recorded file/folder and project names, kinds, counts, project markers, scan time and coverage with your configured model. Selecting a folder stays local.'
      )
    );
  else details.append(node('p', 'These are dated observations, not permission to inspect again.'));
  container.append(details);
}
