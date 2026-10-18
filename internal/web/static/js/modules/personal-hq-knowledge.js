import { renderDossierSources, safeDossierRoute } from './personal-hq-sources.js';
import { factByteLength, newRequestID, reviewTextValid } from './personal-hq-fact.js';
import { MEMORY_STATION } from './memory-station.js';
import { stationLink } from './workspace-station-link.js';

// The fact rules live in personal-hq-fact.js so the assistant's Remember…
// review applies exactly the same ones; this page's behavior is unchanged.
export { reviewTextValid };

const REVIEW_API = '/api/personal-assistant/knowledge';

// The one explanation every global-memory control shows without an assistant
// and a Personal HQ (personalassistant.GlobalMemoryUnavailableMessage).
export const GLOBAL_MEMORY_UNAVAILABLE =
  'Global memory needs a personal assistant with a Personal HQ. Set one up from Home.';

const CATEGORIES = {
  how_you_work: 'How you work',
  people: 'People',
  projects: 'Projects',
  routines: 'Routines',
  sources: 'Sources'
};

export function groupReviewedKnowledge(items) {
  const queue = [];
  const approved = [];
  for (const item of Array.isArray(items) ? items : []) {
    if (!item || typeof item.id !== 'string' || !Number.isSafeInteger(item.version)) continue;
    if (item.state === 'candidate' || item.state === 'needs_review') queue.push(item);
    if (item.state === 'approved' && item.text && !item.review_unavailable) approved.push(item);
  }
  return { queue, approved };
}

export function dossierEmptyGuidance(category, calendar) {
  if (category === 'routines' && calendar) {
    if (calendar.status === 'available' || calendar.status === 'healthy_empty')
      return {
        message:
          'No approved Personal HQ routines yet. Calendar is connected for existing workflows; automatic routine learning is not enabled. Tell Ori directly below.'
      };
    if (
      (calendar.status === 'not_configured' || calendar.status === 'revoked') &&
      safeDossierRoute(calendar.action_route)
    )
      return {
        message:
          'No approved Personal HQ routines yet. Calendar setup is for existing workflows, not automatic reviewed-memory learning.',
        route: calendar.action_route,
        label: calendar.action_label || 'Review Calendar Ops setup'
      };
    if (calendar.status === 'unavailable')
      return {
        message:
          'No approved Personal HQ routines yet. Calendar status could not be verified, so no connection or reviewed-memory claim is made. You can tell Ori directly.',
        route: '#addPersonalHQFact',
        label: 'Tell Ori directly'
      };
  }
  if (category === 'people' || category === 'routines' || category === 'projects')
    return {
      message: `No approved Personal HQ ${category} yet. You can tell Ori directly; connecting a source does not automatically create reviewed facts.`,
      route: '#addPersonalHQFact',
      label: 'Tell Ori directly'
    };
  return { message: 'No approved Personal HQ facts in this section yet.' };
}

function element(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function actionButton(label, onClick, className = 'modern-btn modern-btn-secondary') {
  const button = element('button', className, label);
  button.type = 'button';
  button.addEventListener('click', onClick);
  return button;
}

function sourceLabel(item) {
  if (item.source?.kind === 'interview') return 'Your interview with Ori';
  if (item.source_kind === 'saved_app') return 'Saved app evidence';
  if (item.source_kind === 'file_janitor') return 'Approved File Janitor actions';
  if (item.source_kind === 'folder_scan') return 'A folder you showed Ori';
  if (item.source_kind === 'explicit') return 'You told Ori';
  if (item.source_kind === 'workspace_agent') return 'Proposed by a workspace agent';
  return 'Source not available';
}

// workspaceProposalSource describes where a workspace agent's proposal came
// from: "Proposed by Scout in Album 3 · from task 42", the workspace linking
// to its page when it still exists. Null for every other source.
export function workspaceProposalSource(item) {
  const source = item?.source;
  if (!source || source.kind !== 'workspace_agent') return null;
  const slug = typeof source.workspace_slug === 'string' ? source.workspace_slug : '';
  const taskID = typeof source.task_id === 'string' ? source.task_id : '';
  return {
    agent: source.agent || 'An agent',
    workspace: source.workspace_name || 'a workspace',
    href: slug ? `/workspaces/${encodeURIComponent(slug)}` : '',
    // Task IDs are UUIDs: show a short label and link to the task itself.
    task: taskID ? taskID.slice(0, 8) : '',
    taskHref:
      taskID && slug
        ? `/workspaces/${encodeURIComponent(slug)}?panel=backlog&task=${encodeURIComponent(taskID)}`
        : '',
    observedAt: source.observed_at || ''
  };
}

const APPROVED_SOURCE_CHIPS = {
  explicit: 'From you',
  interview: 'From your interview',
  workspace_agent: 'From a workspace',
  folder_scan: 'From a folder scan',
  saved_app: 'From saved apps',
  file_janitor: 'From File Janitor'
};

// approvedSourceChip names the origin of an approved global fact (FR 14):
// you, interview, workspace, folder scan, saved apps or janitor.
export function approvedSourceChip(item) {
  return APPROVED_SOURCE_CHIPS[item?.source?.kind || item?.source_kind] || '';
}

// promotedFromWorkspace: a fact you promoted from a workspace's Memory tab is
// your own statement that names the workspace it came from.
export function promotedFromWorkspace(item) {
  const source = item?.source;
  if (!source || source.kind !== 'explicit' || !source.workspace_id) return null;
  const slug = typeof source.workspace_slug === 'string' ? source.workspace_slug : '';
  return {
    workspace: source.workspace_name || 'a workspace',
    href: slug ? `/workspaces/${encodeURIComponent(slug)}` : ''
  };
}

function promotedSourceChip(promoted) {
  const chip = element('p', 'reviewed-knowledge-source');
  chip.append('From you, promoted from ');
  if (promoted.href) {
    const link = element('a', '', promoted.workspace);
    link.href = promoted.href;
    chip.append(link);
  } else {
    chip.append(promoted.workspace);
  }
  return chip;
}

function workspaceSourceChip(attribution) {
  const chip = element('p', 'reviewed-knowledge-source');
  chip.append(`Proposed by ${attribution.agent} in `);
  if (attribution.href) {
    const link = element('a', '', attribution.workspace);
    link.href = attribution.href;
    chip.append(link);
  } else {
    chip.append(attribution.workspace);
  }
  if (!attribution.task) {
    chip.append(' · from chat');
  } else if (attribution.taskHref) {
    chip.append(' · from task ');
    const task = element('a', '', attribution.task);
    task.href = attribution.taskHref;
    chip.append(task);
  } else {
    chip.append(` · from task ${attribution.task}`);
  }
  chip.append(` · ${reviewDate(attribution.observedAt)}`);
  return chip;
}

// copyTargetWorkspaces turns GET /api/workspaces into the Copy to workspace
// picker: active workspaces only, never the Personal HQ (its memory already
// is global memory), sorted by name.
export function copyTargetWorkspaces(payload) {
  const list = Array.isArray(payload?.workspaces) ? payload.workspaces : [];
  return list
    .filter(
      ws =>
        ws &&
        typeof ws.id === 'string' &&
        ws.id &&
        ws.designation !== 'personal_hq' &&
        (!ws.status || ws.status === 'active')
    )
    .map(ws => ({ id: ws.id, name: String(ws.name || ws.id), slug: String(ws.folder_slug || '') }))
    .sort((a, b) => a.name.localeCompare(b.name));
}

// workspaceMemoryLink opens a workspace's Memory station, where a copied fact
// lands; '' (no link) for a workspace without a usable address.
export function workspaceMemoryLink(slug) {
  return stationLink(slug, MEMORY_STATION);
}

// copiesSummary is the line shown next to a fact's copies.
export function copiesSummary(copies) {
  const names = (Array.isArray(copies) ? copies : []).map(copy => copy?.name).filter(Boolean);
  if (!names.length) return 'No workspace holds a copy of this fact.';
  return `Copies in: ${names.join(', ')}. A copy is ordinary workspace memory and does not change with this fact.`;
}

function reviewDate(value) {
  if (!value || Number.isNaN(Date.parse(value))) return 'Date unavailable';
  return new Date(value).toLocaleDateString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric'
  });
}

export function mountPersonalHQKnowledge(root = document) {
  const section = root.getElementById('personalHQKnowledge');
  if (!section) return null;
  const queueElement = root.getElementById('reviewedKnowledgeQueue');
  const factsElement = root.getElementById('reviewedKnowledgeFacts');
  const sourcesElement = root.getElementById('reviewedDossierSources');
  const status = root.getElementById('personalHQKnowledgeStatus');
  const checkButton = root.getElementById('checkSavedKnowledge');
  const janitorButton = root.getElementById('checkJanitorKnowledge');
  const buildLink = root.getElementById('personalHQBuildLink');
  const addForm = root.getElementById('addPersonalHQFact');
  const addText = root.getElementById('personalHQFactText');
  const addCategory = root.getElementById('personalHQFactCategory');
  const addLimit = root.getElementById('personalHQFactLimit');
  const addButton = root.getElementById('savePersonalHQFact');
  const retries = new Map();
  let stateVersion = 0;
  let explicitRetry;
  let busy = false;

  function setStatus(message, warning = false) {
    status.textContent = message;
    status.classList.toggle('is-warning', warning);
  }

  async function request(url, options) {
    const response = await fetch(url, { credentials: 'same-origin', ...options });
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) {
      const error = new Error(
        payload.error || `Request failed (${response.status}). Refresh and try again.`
      );
      error.status = response.status;
      throw error;
    }
    return payload;
  }

  async function reload() {
    section.setAttribute('aria-busy', 'true');
    try {
      const payload = await request(REVIEW_API);
      const capabilities = await request('/api/personal-assistant/capabilities').catch(() => null);
      if (!Number.isSafeInteger(payload.state_version) || payload.state_version < 1) {
        throw new Error('Current assistant state is unavailable');
      }
      stateVersion = payload.state_version;
      const { queue, approved } = groupReviewedKnowledge(payload.items);
      for (const [category, id] of Object.entries({
        how_you_work: 'reviewedDossierHowYouWork',
        people: 'reviewedDossierPeople',
        projects: 'reviewedDossierProjects',
        routines: 'reviewedDossierRoutines'
      })) {
        const list = root.getElementById(id);
        if (!list) continue;
        const entries = approved.filter(item => item.category === category);
        if (entries.length) {
          list.replaceChildren(...entries.slice(0, 8).map(item => element('li', '', item.text)));
          continue;
        }
        const calendar = capabilities?.capabilities?.cards?.find(card => card.key === 'calendar');
        const guidance = dossierEmptyGuidance(category, calendar);
        const empty = element('li', '', guidance.message);
        if (guidance.route) {
          const link = element('a', '', guidance.label);
          link.href = guidance.route;
          empty.append(' ', link);
        }
        list.replaceChildren(empty);
      }
      renderDossierSources(sourcesElement, payload.sources, capabilities?.capabilities);
      queueElement.replaceChildren();
      factsElement.replaceChildren();
      if (!queue.length) {
        queueElement.append(
          element(
            'p',
            'reviewed-knowledge-empty',
            'No suggestions waiting. You can check existing saved app evidence without starting a new scan.'
          )
        );
      } else {
        queue.forEach(item => queueElement.append(renderItem(item)));
      }
      if (!approved.length) {
        factsElement.append(
          element(
            'p',
            'reviewed-knowledge-empty',
            'No approved Personal HQ facts yet. Your global profile and other workspace memories remain in their own stores.'
          )
        );
      } else {
        approved.forEach(item => factsElement.append(renderItem(item)));
      }
      buildLink.hidden = true;
      if (addButton) addButton.disabled = false;
      setStatus(`${queue.length} to review · ${approved.length} approved in Personal HQ`);
      return true;
    } catch (error) {
      buildLink.hidden = error.status !== 409;
      // Without an assistant and a Personal HQ there is no global memory to
      // save into; say so instead of letting a save fail.
      if (addButton && error.status === 409) addButton.disabled = true;
      if (sourcesElement) {
        const warning =
          error.status === 409
            ? 'Build or repair Personal HQ to review sources. Your global profile remains available above.'
            : 'Source status could not be refreshed. Previous labels may be stale; refresh before relying on them.';
        if (!sourcesElement.childElementCount) sourcesElement.textContent = warning;
        else if (!sourcesElement.querySelector('.reviewed-dossier-stale')) {
          sourcesElement.prepend(element('p', 'reviewed-dossier-stale', warning));
        }
      }
      setStatus(
        error.status === 409
          ? `${GLOBAL_MEMORY_UNAVAILABLE} Your global profile is still available above.`
          : 'Review state is unavailable. The latest approval status is unknown; refresh to verify it.',
        true
      );
      // Do not clear a previously rendered list or an unsaved edit on a failed read.
      return false;
    } finally {
      section.setAttribute('aria-busy', 'false');
    }
  }

  async function saveExplicit(event) {
    event.preventDefault();
    if (busy) return;
    const text = addText.value;
    const category = addCategory.value;
    if (!stateVersion || !reviewTextValid(text)) {
      setStatus('Review one exact fact of at most 500 UTF-8 bytes before saving.', true);
      addText.focus();
      return;
    }
    try {
      const requestID =
        explicitRetry?.text === text &&
        explicitRetry?.category === category &&
        explicitRetry?.stateVersion === stateVersion
          ? explicitRetry.id
          : newRequestID();
      explicitRetry = { id: requestID, text, category, stateVersion };
    } catch (error) {
      setStatus(error.message, true);
      return;
    }
    busy = true;
    addButton.disabled = true;
    setStatus('Saving the exact fact to Personal HQ…');
    try {
      await request(`${REVIEW_API}/explicit`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          state_version: stateVersion,
          request_id: explicitRetry.id,
          category,
          text
        })
      });
      if (await reload()) {
        explicitRetry = undefined;
        addText.value = '';
        addLimit.textContent = '0 / 500 UTF-8 bytes';
        setStatus('Saved your exact fact in Personal HQ. You can edit or forget it below.');
      }
    } catch (error) {
      setStatus(
        error.status === 409
          ? 'The assistant state or canonical fact changed. Your wording is still here; refresh and review before retrying.'
          : 'Could not verify the save. Your wording is still here; retry with the same action or refresh.',
        true
      );
    } finally {
      busy = false;
      addButton.disabled = false;
    }
  }

  async function mutate(item, action, text, button) {
    if (busy) return;
    if (text !== undefined && !reviewTextValid(text)) {
      setStatus(
        'Use a single, exact fact of at most 500 UTF-8 bytes. Keep passwords and credentials in Vault.',
        true
      );
      return;
    }
    const key = `${item.id}:${action}`;
    const checkpoint = action === 'reconfirm' ? item.evidence_checkpoint : undefined;
    const prior = retries.get(key);
    let requestID;
    try {
      requestID =
        prior?.text === text && prior?.version === item.version && prior?.checkpoint === checkpoint
          ? prior.id
          : newRequestID();
    } catch (error) {
      setStatus(error.message, true);
      return;
    }
    retries.set(key, { id: requestID, version: item.version, text, checkpoint });
    busy = true;
    button.disabled = true;
    setStatus('Saving this reviewed change…');
    try {
      const body = { version: item.version, request_id: requestID };
      if (text !== undefined) body.text = text;
      if (action === 'reconfirm') {
        body.revision_id = item.revision_id;
        body.evidence_checkpoint = checkpoint;
      }
      await request(`${REVIEW_API}/${encodeURIComponent(item.id)}/${action}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
      });
      retries.delete(key);
      await reload();
      if (action === 'forget') {
        busy = false;
        await showCopiesAfterForget(item);
      }
    } catch (error) {
      setStatus(
        error.status === 409
          ? 'The fact or relationship changed. Your edit is still here; refresh and review the latest version before retrying.'
          : 'Could not verify the save. Your edit is still here; retry the same action or refresh to check the result.',
        true
      );
    } finally {
      busy = false;
      button.disabled = false;
    }
  }

  // renderCopies lists the workspaces holding a copy of a fact, each with a
  // Remove copy action, and resolves to the number of copies found.
  async function renderCopies(container, itemID) {
    container.replaceChildren(
      element('p', 'reviewed-knowledge-evidence', 'Checking which workspaces hold a copy…')
    );
    let copies;
    try {
      const payload = await request(`${REVIEW_API}/${encodeURIComponent(itemID)}/copies`);
      copies = Array.isArray(payload.copies) ? payload.copies : [];
    } catch {
      container.replaceChildren(
        element(
          'p',
          'reviewed-knowledge-warning',
          'Copies could not be checked. Refresh to try again.'
        )
      );
      return 0;
    }
    const list = element('ul', 'reviewed-knowledge-copies');
    for (const copy of copies) {
      const row = element('li');
      const href = workspaceMemoryLink(copy.slug);
      if (href) {
        const link = element('a', '', copy.name || 'Workspace');
        link.href = href;
        row.append(link);
      } else {
        row.append(copy.name || 'Workspace');
      }
      const remove = actionButton('Remove copy', async () => {
        if (busy) return;
        busy = true;
        remove.disabled = true;
        try {
          await request(
            `${REVIEW_API}/${encodeURIComponent(itemID)}/copies/${encodeURIComponent(copy.workspace_id)}`,
            { method: 'DELETE' }
          );
          setStatus(`Removed the copy from ${copy.name || 'that workspace'}.`);
          busy = false;
          await renderCopies(container, itemID);
        } catch {
          setStatus('Could not remove that copy. Refresh and try again.', true);
          remove.disabled = false;
        } finally {
          busy = false;
        }
      });
      row.append(' ', remove);
      list.append(row);
    }
    container.replaceChildren(element('p', 'reviewed-knowledge-evidence', copiesSummary(copies)));
    if (copies.length) container.append(list);
    return copies.length;
  }

  // showCopiesAfterForget keeps a forgotten fact's copies visible: the fact
  // leaves the list, but copies stay in their workspaces until removed.
  async function showCopiesAfterForget(item) {
    root.getElementById('reviewedKnowledgeForgottenCopies')?.remove();
    const panel = element('div', 'reviewed-knowledge-forgotten-copies');
    panel.id = 'reviewedKnowledgeForgottenCopies';
    const box = element('div');
    panel.append(
      element(
        'p',
        'reviewed-knowledge-forgotten-title',
        `You forgot "${item.text || 'this fact'}". Copies you made stay in their workspaces until you remove them.`
      ),
      box,
      actionButton('Done', () => panel.remove())
    );
    factsElement.before(panel);
    if (!(await renderCopies(box, item.id))) panel.remove();
  }

  async function copyForm(item, card) {
    if (card.querySelector('.reviewed-knowledge-copy')) return;
    const form = element('form', 'reviewed-knowledge-copy');
    const select = element('select', 'form-select');
    select.id = `copy-target-${item.id}`;
    const label = element('label', '', 'Copy this fact into a workspace');
    label.htmlFor = select.id;
    const copy = element('button', 'modern-btn modern-btn-primary', 'Copy');
    copy.type = 'submit';
    copy.disabled = true;
    select.disabled = true;
    select.append(element('option', '', 'Loading workspaces…'));
    const result = element('p', 'reviewed-knowledge-evidence');
    let targets = [];
    form.append(
      label,
      select,
      copy,
      actionButton('Cancel', () => form.remove()),
      result
    );
    card.append(form);
    try {
      targets = copyTargetWorkspaces(await request('/api/workspaces'));
      if (targets.length) {
        select.replaceChildren(
          ...targets.map(target => {
            const option = element('option', '', target.name);
            option.value = target.id;
            return option;
          })
        );
        select.disabled = false;
        copy.disabled = false;
      } else {
        select.replaceChildren(element('option', '', 'No other workspace yet'));
      }
    } catch {
      select.replaceChildren(element('option', '', 'Workspaces could not be loaded'));
    }
    form.addEventListener('submit', async event => {
      event.preventDefault();
      if (busy || !select.value) return;
      const name = select.selectedOptions[0]?.textContent || 'the workspace';
      busy = true;
      copy.disabled = true;
      try {
        const copied = await request(`${REVIEW_API}/${encodeURIComponent(item.id)}/copy`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ workspace_id: select.value, if_version: item.version })
        });
        result.replaceChildren(
          copied.created
            ? `Copied into ${name}. Its agents now have this fact in their workspace memory. `
            : `${name} already has this fact. `
        );
        // The copy is in that workspace's Memory station: link straight to it.
        const target = targets.find(entry => entry.id === select.value);
        const memoryHref = workspaceMemoryLink(target?.slug);
        if (memoryHref) {
          const open = element('a', '', `Open ${name}'s memory`);
          open.href = memoryHref;
          result.append(open);
        }
        setStatus(copied.created ? `Copied into ${name}.` : `${name} already has this fact.`);
      } catch (error) {
        setStatus(
          error.status === 409
            ? 'This fact changed. Refresh and review it before copying.'
            : 'Could not copy the fact. Try again.',
          true
        );
      } finally {
        busy = false;
        copy.disabled = false;
      }
    });
  }

  function editForm(item, parent, action) {
    if (parent.querySelector('form')) return;
    const form = element('form', 'reviewed-knowledge-edit');
    const label = element('label', '', 'Exact wording to save');
    const input = element('textarea', 'form-control');
    input.rows = 2;
    input.value = item.text || '';
    input.maxLength = 500;
    input.id = `review-wording-${item.id}`;
    label.htmlFor = input.id;
    const limit = element('small', '', `${factByteLength(input.value)} / 500 UTF-8 bytes`);
    input.addEventListener('input', () => {
      limit.textContent = `${factByteLength(input.value)} / 500 UTF-8 bytes`;
    });
    const save = element(
      'button',
      'modern-btn modern-btn-primary',
      action === 'reconfirm' ? 'Reconfirm this reviewed fact' : 'Save exact wording'
    );
    save.type = 'submit';
    form.append(label, input, limit);
    let acknowledgement;
    if (action === 'reconfirm') {
      const confirmLabel = element(
        'label',
        'reviewed-knowledge-acknowledge',
        'I reviewed the current verified filing decisions and this exact wording.'
      );
      acknowledgement = element('input');
      acknowledgement.type = 'checkbox';
      acknowledgement.required = true;
      confirmLabel.prepend(acknowledgement);
      form.append(confirmLabel);
    }
    form.append(
      save,
      actionButton('Cancel', () => form.remove())
    );
    form.addEventListener('submit', event => {
      event.preventDefault();
      if (acknowledgement && !acknowledgement.checked) {
        setStatus('Review and acknowledge the current evidence before reconfirming.', true);
        acknowledgement.focus();
        return;
      }
      mutate(item, action, input.value, save);
    });
    parent.append(form);
    input.focus();
  }

  function renderItem(item) {
    const card = element('article', 'reviewed-knowledge-item');
    const meta = element('p', 'reviewed-knowledge-meta');
    meta.append(
      element('span', '', CATEGORIES[item.category] || 'Personal HQ'),
      element('span', '', item.scope === 'global_profile' ? 'Global profile' : 'Personal HQ'),
      element('span', '', sourceLabel(item)),
      element(
        'span',
        'reviewed-knowledge-state',
        item.state === 'needs_review'
          ? 'Needs review'
          : item.state === 'candidate'
            ? 'Suggestion'
            : 'Approved'
      )
    );
    card.append(meta);
    card.append(
      element(
        'p',
        'reviewed-knowledge-statement',
        item.text || 'Current value unavailable — review the source before using this fact.'
      )
    );
    const attribution = workspaceProposalSource(item);
    const promoted = promotedFromWorkspace(item);
    if (attribution) {
      card.append(workspaceSourceChip(attribution));
    } else if (promoted) {
      card.append(promotedSourceChip(promoted));
    } else if (item.state === 'approved' && approvedSourceChip(item)) {
      card.append(element('p', 'reviewed-knowledge-source', approvedSourceChip(item)));
    }
    const evidence = attribution
      ? []
      : (Array.isArray(item.evidence) ? item.evidence : []).slice(0, 3);
    if (evidence.length) {
      const descriptions = evidence.map(
        entry => `${entry.summary || 'Saved source observation'} · ${reviewDate(entry.observed_at)}`
      );
      card.append(element('p', 'reviewed-knowledge-evidence', descriptions.join(' · ')));
    }
    if (item.state === 'needs_review') {
      card.append(
        element(
          'p',
          'reviewed-knowledge-warning',
          item.source_kind === 'file_janitor'
            ? 'A supporting filing decision was undone or became unavailable. Ori stopped using this fact. Reconfirm only after reviewing new evidence, or forget it.'
            : item.review_unavailable
              ? 'Source or canonical value changed. This fact is not in assistant context.'
              : 'This fact needs a fresh review before it can enter assistant context.'
        )
      );
      if (Array.isArray(item.fresh_evidence) && item.fresh_evidence.length === 3) {
        card.append(
          element(
            'p',
            'reviewed-knowledge-evidence',
            `Current verified support: ${item.fresh_evidence.map(entry => `${entry.summary} · ${reviewDate(entry.observed_at)}`).join(' · ')}`
          )
        );
      }
    }
    if (item.review_unavailable === 'operation_pending') {
      card.append(
        element(
          'p',
          'reviewed-knowledge-warning',
          item.can_resume_forget
            ? 'A previous Forget was interrupted. This fact is excluded from assistant context; finish the exact prepared removal without restoring old text.'
            : item.can_resume_operation
              ? 'A previous confirmed review was interrupted. No intermediate text is in assistant context. Ori will recheck the canonical value and source before continuing.'
              : 'A previous save is unfinished. Refresh to verify it; no intermediate text is in assistant context.'
        )
      );
      if (item.can_resume_forget || item.can_resume_operation) {
        const resume = actionButton(
          item.can_resume_forget ? 'Finish interrupted Forget' : 'Finish interrupted review',
          async () => {
            if (busy) return;
            busy = true;
            resume.disabled = true;
            setStatus(
              'Verifying the interrupted change against the canonical store and current source…'
            );
            try {
              await request(
                `${REVIEW_API}/${encodeURIComponent(item.id)}/${item.can_resume_forget ? 'resume-forget' : 'resume-operation'}`,
                {
                  method: 'POST'
                }
              );
              await reload();
            } catch (error) {
              setStatus(
                error.status === 409
                  ? 'The canonical fact or source changed; Ori did not adopt or remove the edited value. Review the source before retrying.'
                  : 'Could not verify the interrupted change. The fact remains excluded; retry later.',
                true
              );
            } finally {
              busy = false;
              resume.disabled = false;
            }
          }
        );
        card.append(resume);
      }
      return card;
    }
    const actions = element('div', 'reviewed-knowledge-actions');
    if (item.state === 'candidate') {
      const approve = actionButton(
        'Approve this wording',
        () => {
          if (window.confirm('Approve this exact wording for your Personal HQ?'))
            mutate(item, 'approve', undefined, approve);
        },
        'modern-btn modern-btn-primary'
      );
      actions.append(approve);
      actions.append(actionButton('Edit first', () => editForm(item, card, 'edit-candidate')));
      const reject = actionButton('Reject suggestion', () => {
        if (
          window.confirm(
            'Reject this suggestion? Ori will not automatically propose the same meaning again.'
          )
        )
          mutate(item, 'reject', undefined, reject);
      });
      actions.append(reject);
    } else if (item.state === 'approved' || item.state === 'needs_review') {
      if (item.state === 'approved') {
        actions.append(
          actionButton('Edit', () => {
            editForm(item, card, 'edit-approved');
            if (!card.querySelector('.reviewed-knowledge-copies-box')) {
              const box = element('div', 'reviewed-knowledge-copies-box');
              card.append(box);
              void renderCopies(box, item.id);
            }
          })
        );
        if (item.scope !== 'global_profile') {
          actions.append(actionButton('Copy to workspace…', () => copyForm(item, card)));
        }
      } else if (item.source_kind === 'file_janitor' && item.evidence_checkpoint) {
        actions.append(
          actionButton('Review current evidence and reconfirm', () =>
            editForm(item, card, 'reconfirm')
          )
        );
      }
      const forget = actionButton('Forget', () => {
        if (window.confirm('Forget this fact and suppress the same automatic suggestion?'))
          mutate(item, 'forget', undefined, forget);
      });
      actions.append(forget);
    }
    card.append(actions);
    return card;
  }

  async function checkSource(button, path, label) {
    if (busy) return;
    busy = true;
    button.disabled = true;
    try {
      const payload = await request(`${REVIEW_API}/${path}`, { method: 'POST' });
      if (await reload()) {
        setStatus(
          (payload.candidates || []).length
            ? `${label} checked. Review the suggestions below; nothing was approved automatically.`
            : `${label} checked. No new suggestion was admitted; no app scan or file action was run.`
        );
      }
    } catch (error) {
      setStatus(error.message || `${label} could not be checked. Try again later.`, true);
    } finally {
      busy = false;
      button.disabled = false;
    }
  }
  addText?.addEventListener('input', () => {
    addLimit.textContent = `${factByteLength(addText.value)} / 500 UTF-8 bytes`;
  });
  addForm?.addEventListener('submit', saveExplicit);
  document.addEventListener('personal-assistant-knowledge-changed', () => {
    if (!busy) void reload();
  });
  checkButton.addEventListener('click', () =>
    checkSource(checkButton, 'check-saved-apps', 'Saved app evidence')
  );
  janitorButton.addEventListener('click', () =>
    checkSource(janitorButton, 'check-janitor', 'Approved File Janitor decisions')
  );
  reload();
  return { reload };
}

if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', () => mountPersonalHQKnowledge(), { once: true });
  } else {
    mountPersonalHQKnowledge();
  }
}
