// Learning stays alongside finite Getting Started. Reads never request a
// suggestion or start work; all mutations originate from labelled controls.
export const learningRoot = '/api/learning-missions';

export function featuredLearning(status) {
  const rows = status?.instances || [];
  return (
    rows.find(row => ['active', 'blocked'].includes(row.state)) ||
    rows.find(row => row.state === 'offered') ||
    null
  );
}

export function learningTicketHref(instance) {
  const href = instance?.ticket_href || '';
  return /^\/workspaces\/[^/?#]+\/task\/[^/?#]+$/.test(href) ? href : '';
}

export const learningBlockers = {
  source_unavailable:
    'The original workspace or evidence is unavailable. Your recorded practice is not a penalty.',
  definition_unavailable: 'This learning structure is unavailable on this version of Ori.',
  execution_unavailable: 'No ready, isolated-capable agent is available in this workspace.',
  offer_expired: 'This offer expired. Dismiss it and request another when useful.',
  result_unavailable:
    'The exact result is not available yet. Check the linked Ticket; no completion has been inferred.'
};

export function createLearningAPI(
  request = (...args) => fetch(...args),
  newID = () => crypto.randomUUID()
) {
  async function call(path, body) {
    const response = await request(path, {
      method: body === undefined ? 'GET' : 'POST',
      cache: 'no-store',
      credentials: 'same-origin',
      headers: {
        Accept: 'application/json',
        ...(body === undefined ? {} : { 'Content-Type': 'application/json' })
      },
      ...(body === undefined ? {} : { body: JSON.stringify(body) })
    });
    if (!response.ok) {
      const error = new Error(
        response.status === 409
          ? 'The saved state changed. Refresh and review it before trying again.'
          : response.status === 404
            ? 'No eligible work or evidence is available here right now.'
            : 'The request was not confirmed. Check the saved state before trying again.'
      );
      error.status = response.status;
      throw error;
    }
    return response.json();
  }
  return {
    get: path => call(path),
    // Start has no durable HTTP receipt: never retry it automatically.
    start: (path, digest) => call(path, { executor_digest: digest }),
    async mutate(path, expectedRevision, fields) {
      const operationID = newID();
      try {
        return await call(path, {
          ...fields,
          expected_revision: expectedRevision,
          operation_id: operationID
        });
      } catch (error) {
        if (error.status && error.status < 500) throw error;
        // A lost reply is a read of the original operation, never another POST.
        try {
          return await call(`${learningRoot}/operations/${encodeURIComponent(operationID)}`);
        } catch {
          throw error;
        }
      }
    }
  };
}

export function learningElement(doc, tag, text, className = '') {
  const element = doc.createElement(tag);
  if (text !== undefined) element.textContent = text;
  if (className) element.className = className;
  return element;
}

export function learningButton(doc, label, run) {
  const button = learningElement(
    doc,
    'button',
    label,
    'modern-btn modern-btn-secondary modern-btn-sm'
  );
  button.type = 'button';
  button.addEventListener('click', run);
  return button;
}

export function learningField(doc, label, control) {
  // Explicit name keeps select option text out of the wrapping label's name.
  control.setAttribute('aria-label', label);
  const wrap = learningElement(doc, 'label', undefined, 'learning-field');
  wrap.append(learningElement(doc, 'span', label), control);
  return wrap;
}

export class LearningMissions {
  constructor(root, api = createLearningAPI()) {
    this.root = root;
    this.doc = root.ownerDocument;
    this.api = api;
    this.status = null;
    this.busy = false;
    this.body = learningElement(this.doc, 'div');
    this.message = learningElement(this.doc, 'p', '', 'learning-message');
    this.message.setAttribute('role', 'status');
    root.append(this.body, this.message);
  }

  async refresh() {
    this.status = await this.api.get(learningRoot);
    this.render();
  }

  async action(work) {
    if (this.busy) return;
    const hadFocus = this.root.contains(this.doc.activeElement);
    this.busy = true;
    this.root.setAttribute('aria-busy', 'true');
    this.message.textContent = 'Saving…';
    try {
      await work();
      this.message.textContent = '';
      // A replaced button must not strand focus on the document body.
      if (hadFocus && !this.root.contains(this.doc.activeElement))
        this.root.querySelector('h3')?.focus();
    } catch (error) {
      this.message.textContent = error.message;
    } finally {
      this.busy = false;
      this.root.removeAttribute('aria-busy');
    }
  }

  async decide(instance, decision) {
    await this.api.mutate(
      `${learningRoot}/instances/${encodeURIComponent(instance.id)}/decisions`,
      this.status.owner.revision,
      { instance_revision: instance.revision, decision }
    );
    await this.refresh();
  }

  render() {
    const d = this.doc;
    this.body.replaceChildren();
    const heading = learningElement(d, 'h3', 'Learning');
    heading.tabIndex = -1;
    this.body.append(
      heading,
      learningElement(
        d,
        'p',
        'Practice useful work. No points, streaks or completion quota.',
        'learning-caption'
      )
    );
    const instance = featuredLearning(this.status);
    if (!instance) {
      const completed = this.status.instances.filter(row => row.state === 'completed').length;
      if (completed)
        this.body.append(
          learningElement(
            d,
            'p',
            'Reviewed practice recorded. This describes a workflow you exercised, not mastery.'
          )
        );
      this.body.append(
        learningElement(
          d,
          'p',
          'Choose an existing workspace when you want a small delegation-and-review practice.'
        )
      );
      if (this.status.owner.preferences.mode !== 'off') {
        this.body.append(
          learningButton(d, 'Choose a workspace', () => this.action(() => this.chooseWorkspace()))
        );
      } else
        this.body.append(
          learningElement(
            d,
            'p',
            'Coaching is off. Ordinary Tickets and Getting Started are unchanged.'
          )
        );
    } else {
      const { definition, progress, state } = instance;
      this.body.append(
        learningElement(d, 'h4', definition.goal),
        learningElement(d, 'p', definition.scope),
        learningElement(d, 'p', definition.why, 'learning-caption')
      );
      const criteria = learningElement(d, 'ol', undefined, 'learning-steps');
      for (const label of [
        'Review a small input',
        'Explicitly start its Ticket',
        'Inspect the exact result and record a decision'
      ]) {
        criteria.append(learningElement(d, 'li', label));
      }
      this.body.append(criteria);
      if (progress.blocker)
        this.body.append(
          learningElement(
            d,
            'p',
            learningBlockers[progress.blocker] || 'Check the linked Ticket before continuing.',
            'learning-blocker'
          )
        );
      const controls = learningElement(d, 'div', undefined, 'learning-controls');
      const button = (label, decision) =>
        controls.append(
          learningButton(d, label, () => this.action(() => this.decide(instance, decision)))
        );
      if (state === 'offered') {
        if (!progress.blocker) button('Start learning — no execution', 'accept');
        button('Not now', 'defer');
        button('Not interested', 'decline');
      } else {
        const href = learningTicketHref(instance);
        if (href) {
          const link = learningElement(
            d,
            'a',
            progress.run_id ? 'Open Ticket and review result' : 'Open Ticket to review execution',
            'modern-btn modern-btn-primary modern-btn-sm'
          );
          link.href = href;
          controls.append(link);
        } else if (!progress.blocker && !progress.ticket_id) {
          controls.append(
            learningButton(d, 'Prepare a draft Ticket', () =>
              this.action(() => this.prepareForm(instance))
            )
          );
        }
        button('Stop mission — keep actual work', 'stop');
      }
      this.body.append(controls);
    }
    this.body.append(
      learningButton(d, 'Refresh learning', () => this.action(() => this.refresh()))
    );
  }

  async chooseWorkspace() {
    const data = await this.api.get('/api/workspaces');
    const rows = data.folders || data.workspaces || [];
    const select = learningElement(this.doc, 'select', undefined, 'form-select');
    for (const ws of rows.filter(ws => ws.kind !== 'group')) {
      const option = learningElement(this.doc, 'option', ws.name || 'Workspace');
      option.value = ws.id;
      select.append(option);
    }
    if (!select.options.length) {
      this.body.append(
        learningElement(
          this.doc,
          'p',
          'No workspace is available. Learning can wait; use Getting Started or ordinary work.'
        )
      );
      return;
    }
    this.render();
    const form = learningElement(this.doc, 'div', undefined, 'learning-form');
    form.append(
      learningField(this.doc, 'Workspace', select),
      learningElement(
        this.doc,
        'p',
        'Generic practice, chosen by Ori. Requesting this offer makes no model call and grants no file access.',
        'learning-caption'
      )
    );
    form.append(
      learningButton(this.doc, 'Request a practice', () =>
        this.action(async () => {
          await this.api.mutate(`${learningRoot}/proposals`, this.status.owner.revision, {
            studio_id: select.value
          });
          await this.refresh();
        })
      )
    );
    this.body.append(form);
    select.focus();
  }

  async prepareForm(instance) {
    const ws = await this.api.get(
      `/api/workspaces/${encodeURIComponent(instance.definition.studio_id)}`
    );
    this.render();
    const d = this.doc;
    const input = learningElement(d, 'textarea', undefined, 'form-control');
    input.rows = 5;
    input.maxLength = 4096;
    const select = learningElement(d, 'select', undefined, 'form-select');
    for (const name of [...new Set((ws.agent_instances || []).map(agent => agent.name))]) {
      const option = learningElement(d, 'option', name);
      option.value = name;
      select.append(option);
    }
    const form = learningElement(d, 'div', undefined, 'learning-form');
    form.append(
      learningField(d, 'Text and drafting requirements (maximum 4 KiB)', input),
      learningField(d, 'Assigned agent', select),
      learningElement(
        d,
        'p',
        'Only this supplied text can be sent. Saving creates a Ready Ticket; it does not run it. Review the current model and cost on that Ticket before Start.',
        'learning-caption'
      )
    );
    form.append(
      learningButton(d, 'Save reviewed input as a Ticket', () =>
        this.action(async () => {
          if (
            !input.value.trim() ||
            new TextEncoder().encode(input.value.trim()).length > 4096 ||
            !select.value
          )
            throw new Error('Choose an agent and supply nonempty text of at most 4 KiB.');
          await this.api.mutate(
            `${learningRoot}/instances/${encodeURIComponent(instance.id)}/work`,
            this.status.owner.revision,
            { instance_revision: instance.revision, input: input.value, assignee: select.value }
          );
          await this.refresh();
        })
      )
    );
    this.body.append(form);
    input.focus();
  }
}

if (typeof document !== 'undefined') {
  const init = () => {
    const root = document.getElementById('learningMissions');
    if (root) {
      const board = new LearningMissions(root);
      void board.action(() => board.refresh());
    }
  };
  if (document.readyState === 'loading')
    document.addEventListener('DOMContentLoaded', init, { once: true });
  else init();
}
