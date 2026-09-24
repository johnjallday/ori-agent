import { renderDossierSources, safeDossierRoute } from './personal-hq-sources.js';

const REVIEW_API = '/api/personal-assistant/knowledge';
const MAX_FACT_BYTES = 500;
const encoder = new TextEncoder();

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

export function reviewTextValid(text) {
  return (
    typeof text === 'string' &&
    text.length > 0 &&
    text.trim() === text &&
    text === text.split(/\s+/u).join(' ') &&
    !Array.from(text).some(character => {
      const code = character.codePointAt(0);
      return (
        code < 32 ||
        (code >= 127 && code <= 159) ||
        (code >= 0x202a && code <= 0x202e) ||
        (code >= 0x2066 && code <= 0x2069)
      );
    }) &&
    encoder.encode(text).length <= MAX_FACT_BYTES
  );
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
  if (item.source_kind === 'saved_app') return 'Saved app evidence';
  if (item.source_kind === 'file_janitor') return 'Approved File Janitor actions';
  if (item.source_kind === 'explicit') return 'You told Ori';
  return 'Source not available';
}

function reviewDate(value) {
  if (!value || Number.isNaN(Date.parse(value))) return 'Date unavailable';
  return new Date(value).toLocaleDateString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric'
  });
}

function newRequestID() {
  if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID();
  if (!globalThis.crypto?.getRandomValues)
    throw new Error('Secure retry IDs are unavailable. Reload on a secure connection.');
  const bytes = globalThis.crypto.getRandomValues(new Uint8Array(16));
  return Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('');
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
      setStatus(`${queue.length} to review · ${approved.length} approved in Personal HQ`);
      return true;
    } catch (error) {
      buildLink.hidden = error.status !== 409;
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
          ? 'Build or repair Personal HQ to review assistant memory. Your global profile is still available above.'
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
    const limit = element('small', '', `${encoder.encode(input.value).length} / 500 UTF-8 bytes`);
    input.addEventListener('input', () => {
      limit.textContent = `${encoder.encode(input.value).length} / 500 UTF-8 bytes`;
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
    const evidence = (Array.isArray(item.evidence) ? item.evidence : []).slice(0, 3);
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
        actions.append(actionButton('Edit', () => editForm(item, card, 'edit-approved')));
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
    addLimit.textContent = `${encoder.encode(addText.value).length} / 500 UTF-8 bytes`;
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
