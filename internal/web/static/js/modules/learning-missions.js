// Learning stays alongside finite Getting Started. Reads never request a
// suggestion or start work; all mutations originate from labelled controls.
export const learningRoot = '/api/learning-missions';

export function featuredLearning(status) {
  const rows = status?.instances || [];
  const active = rows.find(row => ['active', 'blocked'].includes(row.state));
  if (active) {
    const preview = rows.find(
      row => row.state === 'offered' && row.definition?.replaces_id === active.id
    );
    return status.owner?.preferences?.mode !== 'off' && preview ? preview : active;
  }
  return rows.find(row => row.state === 'offered') || null;
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
    'The exact result is not available yet. Check the linked Ticket; no completion has been inferred.',
  result_not_useful:
    'Your evaluation was recorded, but this result was not marked useful. No successful-reuse credit was recorded. Stop this mission to choose another; the Ticket remains available.',
  model_changed: 'The configured model changed. Dismiss this offer and review a fresh request.',
  preferences_changed: 'Your learning preferences changed. This old offer was not kept active.',
  replacement_changed:
    'Your current mission changed. Keep it and request a fresh alternative before accepting a replacement.'
};

export const learningStructures = {
  delegate_inspect: {
    label: 'Delegate and inspect',
    steps: [
      'Review a small input',
      'Explicitly start its Ticket',
      'Inspect the exact result and record a decision'
    ]
  },
  improve_result: {
    label: 'Improve a result',
    steps: [
      'Inspect the selected original result',
      'Supply a correction and explicitly start its revision Ticket',
      'Compare both exact results and describe what changed — rejection is valid practice'
    ]
  },
  repeat_success: {
    label: 'Repeat a success',
    steps: [
      'Inspect a previously useful supplied-text draft process',
      'Supply materially different input and explain the difference',
      'Explicitly start a new Ticket, then review its exact result — successful reuse requires choosing Use it'
    ]
  }
};

export const learningSignals = {
  chosen_workspace: 'You chose this workspace.',
  ready_bounded_process: 'A bounded drafting process is ready here.',
  chosen_structure: 'You chose this type of practice.',
  explicit_focus_words_match:
    'Words in your explicit focus match this workspace or a previously reviewed goal.',
  no_recorded_practice_for_structure:
    'No local review practice for this structure is recorded in this scope. Other experience is unknown.'
};

export function proposalMessage(out) {
  const state = out?.operation?.status;
  if (state === 'pending' || state === 'unknown')
    return 'The original request is pending or its outcome is unknown. No automatic retry will be made. Refresh saved learning before choosing another request.';
  if (state === 'failed')
    return 'No personalized offer was published. You can choose generic practice instead; any further model request still uses the call limits.';
  return '';
}

export function createLearningAPI(
  request = (...args) => fetch(...args),
  newID = () => crypto.randomUUID()
) {
  async function call(path, body) {
    let response;
    try {
      response = await request(path, {
        method: body === undefined ? 'GET' : 'POST',
        cache: 'no-store',
        credentials: 'same-origin',
        headers: {
          Accept: 'application/json',
          ...(body === undefined ? {} : { 'Content-Type': 'application/json' })
        },
        ...(body === undefined ? {} : { body: JSON.stringify(body) })
      });
    } catch {
      throw new Error(
        'The request could not be confirmed. Check the connection and refresh saved learning before trying again.'
      );
    }
    if (!response.ok) {
      let storageLimit = false;
      if (response.status === 429) {
        try {
          storageLimit = (await response.json()).error === 'storage_limit_reached';
        } catch {
          /* Unknown error bodies are never rendered. */
        }
      }
      const error = new Error(
        storageLimit
          ? 'Learning record capacity reached. Review or explicitly clear history in Local learning activity. Actual Tickets and Runs remain; clearing does not reset recent call limits.'
          : response.status === 409
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

export function learningResult(doc, label, text) {
  const result = learningElement(doc, 'pre', text, 'learning-result');
  result.tabIndex = 0;
  result.setAttribute('role', 'region');
  result.setAttribute('aria-label', label);
  return result;
}

export function focusLearningControl(control) {
  if (!control) return;
  const hero = control.ownerDocument.querySelector?.('.workspace-task-page-hero');
  if (hero && control.style) {
    const position = control.ownerDocument.defaultView?.getComputedStyle(hero).position;
    const stickyHeight = ['sticky', 'fixed'].includes(position)
      ? hero.getBoundingClientRect().height
      : 0;
    control.style.scrollMarginTop = `${stickyHeight + 96}px`;
  }
  control.focus({ preventScroll: true });
  control.scrollIntoView?.({ block: 'nearest' });
}

export function learningOriginal(doc, original, showInput = false) {
  const section = learningElement(doc, 'section', undefined, 'learning-original');
  const title = learningElement(doc, 'h4', 'Selected original · complete canonical result');
  title.tabIndex = -1;
  section.append(
    title,
    learningElement(
      doc,
      'p',
      `Run ${original.result.run_id} · Artifact ${original.result.artifact_id}`,
      'learning-caption'
    ),
    learningElement(
      doc,
      'p',
      'Exact inline bytes, not a history excerpt. The previous decision is a user report, not a quality rating.',
      'learning-caption'
    )
  );
  if (showInput)
    section.append(
      learningElement(doc, 'h4', 'Previously reviewed input'),
      learningResult(doc, 'Previously reviewed input', original.input)
    );
  section.append(learningResult(doc, 'Selected original result', original.result_text));
  const href = learningTicketHref({ ticket_href: original.ticket_href });
  if (href) {
    const link = learningElement(doc, 'a', 'Open original Ticket');
    link.href = href;
    section.append(link);
  }
  return section;
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
      const message = await work();
      this.message.textContent = typeof message === 'string' ? message : '';
      // A replaced button must not strand focus on the document body.
      if (hadFocus && !this.root.contains(this.doc.activeElement))
        focusLearningControl(this.root.querySelector('h3'));
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
    if (decision === 'defer')
      return 'Not now saved for this workspace, practice type and focus for seven days. No practice was lost.';
    if (decision === 'decline')
      return 'Not interested saved for this workspace, practice type and focus. Reset exclusions in Learning preferences whenever you choose.';
    return '';
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
        'Practice useful work. Learning adds no points, streaks or completion quota.',
        'learning-caption'
      )
    );
    if (
      this.status.owner.preferences.mode === 'automatic' &&
      !this.status.owner.preferences.suggestion_revision
    ) {
      this.body.append(
        learningElement(
          d,
          'p',
          'Automatic suggestions are paused until you review and save fresh scope/model consent in Learning preferences.',
          'learning-caption'
        )
      );
    }
    const instance = featuredLearning(this.status);
    const existingReview = (this.status.observations || []).find(
      row => row.ready && learningTicketHref(row)
    );
    if (this.status.owner.preferences.mode === 'off') {
      this.body.append(
        learningElement(
          d,
          'p',
          'Coaching is off. Ordinary Tickets, recorded practice and Getting Started are unchanged.'
        )
      );
      if (instance && ['active', 'blocked'].includes(instance.state)) {
        this.body.append(
          learningElement(
            d,
            'p',
            'Your accepted learning contract is saved, but coaching prompts are quiet.',
            'learning-caption'
          )
        );
        const href = learningTicketHref(instance);
        if (href) {
          const link = learningElement(d, 'a', 'Open existing Ticket');
          link.href = href;
          this.body.append(link);
        }
        this.body.append(
          learningButton(d, 'Stop mission — keep actual work', () =>
            this.action(() => this.decide(instance, 'stop'))
          )
        );
      }
    } else if (
      existingReview &&
      !this.status.instances.some(row => ['active', 'blocked'].includes(row.state))
    ) {
      this.body.append(
        learningElement(d, 'h4', 'Review existing work first'),
        learningElement(
          d,
          'p',
          'An opted-in, completed execution can record practice without a duplicate mission. Inspect its exact result and record your decision.'
        )
      );
      const link = learningElement(d, 'a', 'Open existing result review');
      link.href = learningTicketHref(existingReview);
      this.body.append(
        link,
        learningButton(d, 'Choose different practice explicitly', () =>
          this.action(() => this.chooseWorkspace())
        )
      );
    } else if (!instance) {
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
          'Choose an existing workspace for a small practice. Revision and reuse need a locally reviewed original result.'
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
      if (state === 'offered' && definition.replaces_id) {
        const current = this.status.instances.find(row => row.id === definition.replaces_id);
        const notice = learningElement(d, 'section', undefined, 'learning-original');
        notice.append(
          learningElement(d, 'h4', 'Replacement to review · not yet accepted'),
          learningElement(
            d,
            'p',
            `Current mission: ${current?.definition?.goal || 'Saved learning contract'}`
          ),
          learningElement(
            d,
            'p',
            'Your current mission is unchanged. Accepting this replacement stops only its learning tracking. Existing Tickets and Runs continue; this proposal creates no work.',
            'learning-caption'
          )
        );
        const href = learningTicketHref(current);
        if (href) {
          const link = learningElement(d, 'a', 'Open current Ticket');
          link.href = href;
          notice.append(link);
        }
        this.body.append(notice);
      }
      this.body.append(
        learningElement(d, 'h4', definition.goal),
        learningElement(d, 'p', definition.scope),
        learningElement(d, 'p', definition.why, 'learning-caption')
      );
      if (definition.host_signals?.length) {
        const reasons = learningElement(d, 'ul', undefined, 'learning-steps');
        for (const signal of definition.host_signals)
          if (Object.hasOwn(learningSignals, signal))
            reasons.append(learningElement(d, 'li', learningSignals[signal]));
        this.body.append(learningElement(d, 'h4', 'Why Ori offered this'), reasons);
      }
      this.body.append(
        learningElement(
          d,
          'p',
          definition.personalized
            ? `${definition.automatic ? 'Offered after a new recorded review under your automatic-suggestion consent. No work was started. ' : ''}Model-personalized wording; the actions and evidence rules below are defined by Ori.`
            : 'Generic practice, chosen by Ori. No model was called to write this offer.',
          'learning-caption'
        )
      );
      if (definition.coaching_depth === 'brief')
        this.body.append(
          learningElement(
            d,
            'p',
            'Guided · concise optional coaching. This scope has at least two recorded independent Ori-path attempts for this practice and evaluation. That is not a mastery claim. Keep full coaching in Learning preferences whenever you prefer; accepted contracts do not change.',
            'learning-caption'
          )
        );
      else if (definition.coaching_depth === 'basic')
        this.body.append(
          learningElement(
            d,
            'p',
            'Basic · the full criteria, without additional model-authored coaching. Help outside Ori remains unknown.',
            'learning-caption'
          )
        );
      else if (definition.coaching_depth === 'full')
        this.body.append(
          learningElement(
            d,
            'p',
            'Guided · optional coaching alongside the full criteria. Missing or unknown support never becomes an ability label.',
            'learning-caption'
          )
        );
      const structure =
        progress.blocker === 'definition_unavailable'
          ? undefined
          : learningStructures[definition.structure || 'delegate_inspect'];
      this.body.append(
        learningElement(d, 'h4', `Ori’s criteria · ${structure?.label || 'Unavailable structure'}`)
      );
      const criteria = learningElement(d, 'ol', undefined, 'learning-steps');
      for (const label of structure?.steps || []) {
        criteria.append(learningElement(d, 'li', label));
      }
      this.body.append(criteria);
      if (definition.hints?.length) {
        const hints = learningElement(d, 'details', undefined, 'learning-coaching');
        hints.append(learningElement(d, 'summary', 'Suggested coaching · model-authored'));
        for (const hint of definition.hints) hints.append(learningElement(d, 'p', hint));
        this.body.append(hints);
      }
      if (definition.original_receipt && !progress.blocker) {
        this.body.append(
          learningButton(d, 'Inspect selected original', () =>
            this.action(async () => {
              const original = await this.api.get(
                `${learningRoot}/instances/${encodeURIComponent(instance.id)}/original`
              );
              this.render();
              const view = learningOriginal(d, original, definition.structure === 'repeat_success');
              this.body.append(view);
              focusLearningControl(view.querySelector('h4'));
            })
          )
        );
      }
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
      if (state === 'offered' && progress.blocker === 'definition_unavailable') {
        button('Dismiss unsupported learning offer — keep actual work', 'stop');
      } else if (state === 'offered') {
        if (!progress.blocker)
          button(
            definition.replaces_id
              ? 'Accept replacement — keep actual work'
              : 'Start learning — no execution',
            'accept'
          );
        if (definition.replaces_id) button('Keep current mission', 'discard');
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
      if (
        !['source_unavailable', 'definition_unavailable', 'replacement_changed'].includes(
          progress.blocker
        )
      ) {
        const adjustments = learningElement(d, 'details', undefined, 'learning-coaching');
        adjustments.append(learningElement(d, 'summary', 'Adjust this practice'));
        if ((definition.input_limit || 4096) > 256)
          adjustments.append(
            learningButton(d, 'Make smaller', () =>
              this.action(() => this.adjustForm(instance, 'smaller'))
            )
          );
        adjustments.append(
          learningButton(d, 'Something else', () =>
            this.action(() => this.adjustForm(instance, 'alternative'))
          )
        );
        this.body.append(adjustments);
      }
    }
    if (this.status.competencies)
      this.body.append(
        learningButton(d, 'Recorded practice', () => this.action(() => this.practiceForm()))
      );
    this.body.append(
      learningButton(d, 'Learning preferences', () => this.action(() => this.preferencesForm())),
      learningButton(d, 'Local learning activity', () => this.action(() => this.diagnosticsForm())),
      learningButton(d, 'Refresh learning', () => this.action(() => this.refresh()))
    );
  }

  async diagnosticsForm() {
    const data = await this.api.get(`${learningRoot}/diagnostics`);
    this.render();
    const d = this.doc;
    const panel = learningElement(d, 'section', undefined, 'learning-form');
    const title = learningElement(d, 'h4', 'Local learning activity · not a score');
    title.tabIndex = -1;
    const count = value =>
      Number.isSafeInteger(value) && value >= 0 ? String(value) : 'Unavailable';
    panel.append(
      title,
      learningElement(
        d,
        'p',
        'Device-local counts only; nothing is sent to an analytics service. The last 30 days are bounded to 200 records per category. They do not measure mastery, retention or attention.',
        'learning-caption'
      )
    );
    if (data.coverage !== 'available')
      panel.append(
        learningElement(
          d,
          'p',
          'Coverage is partial or unavailable. Shown counts are not a complete history.',
          'learning-caption'
        )
      );
    const facts = learningElement(d, 'ul', undefined, 'learning-steps');
    for (const text of [
      `${count(data.offers)} offers created in the window · ${count(data.personalized_offers)} personalized · ${count(data.automatic_offers)} automatic`,
      `${count(data.accepted_offers)} of those offers were accepted. Current states: ${count(data.deferred_offers)} deferred · ${count(data.declined_offers)} declined · ${count(data.stopped_contracts)} stopped tracking (including replacements). These counts overlap.`,
      `Recorded decisions in the window: ${count(data.decisions?.use)} use · ${count(data.decisions?.revise)} revise · ${count(data.decisions?.reject)} reject. These may include retries, not new distinct practice.`,
      `Suggestion-design calls admitted: ${count(data.design_calls)} · ${count(data.automatic_calls)} automatic. This excludes separately approved actual Ticket executions.`,
      `Design outcomes: ${count(data.call_outcomes?.succeeded)} succeeded · ${count(data.call_outcomes?.pending)} pending · ${count(data.call_outcomes?.failed)} failed · ${count(data.call_outcomes?.unknown)} unknown · ${count(data.call_outcomes?.invalidated)} invalidated`,
      `Known reported token use: ${count(data.known_tokens)} · ${count(data.unknown_usage_calls)} calls with unknown usage. This is not a provider bill.`,
      `Reserved token allowance: ${count(data.reserved_tokens)}. Reservation is conservative accounting, not measured usage.`
    ])
      facts.append(learningElement(d, 'li', text));
    panel.append(facts, learningElement(d, 'h4', 'Retained practice · per competency'));
    if (data.practice?.coverage === 'unknown')
      panel.append(
        learningElement(
          d,
          'p',
          'Practice evidence is unavailable; no ability or zero-practice claim has been inferred.'
        )
      );
    for (const item of data.practice?.coverage === 'unknown' ? [] : data.practice?.items || []) {
      if (!['delegate', 'evaluate', 'improve', 'reuse'].includes(item.competency)) continue;
      panel.append(
        learningElement(
          d,
          'p',
          `${item.competency}: ${count(item.attempts)} distinct recorded attempts. These retained-history counts are not limited to the 30-day activity window.`
        )
      );
    }
    const clear = learningElement(d, 'input');
    clear.type = 'checkbox';
    clear.checked = false;
    panel.append(
      learningElement(d, 'h4', 'Clear learning history'),
      learningElement(
        d,
        'p',
        'Removes learning contracts, review receipts and practice records, and invalidates pending learning callbacks. Actual Tickets and Runs remain. Preferences and recent suggestion-call accounting remain; this does not reset the call limits.',
        'learning-caption'
      ),
      learningField(
        d,
        'Confirm clearing learning history and recorded practice — keep actual work',
        clear
      ),
      learningButton(d, 'Clear learning history', () =>
        this.action(async () => {
          if (!clear.checked) throw new Error('Confirm the separate learning-history clear first.');
          await this.api.mutate(`${learningRoot}/clear`, this.status.owner.revision, {
            scope: 'history'
          });
          await this.refresh();
          return 'Learning history cleared. Actual work, preferences and recent call-limit accounting remain.';
        })
      ),
      learningButton(d, 'Back to learning', () => this.action(() => this.refresh()))
    );
    this.body.append(panel);
    focusLearningControl(title);
  }

  practiceForm() {
    this.render();
    const d = this.doc;
    const summary = this.status.competencies;
    const panel = learningElement(d, 'section', undefined, 'learning-form');
    panel.setAttribute('aria-label', 'Recorded practice');
    const title = learningElement(d, 'h4', 'Recorded practice · not a skill score');
    title.tabIndex = -1;
    panel.append(
      title,
      learningElement(
        d,
        'p',
        'These are local, result-specific reviews. Independent means basic-mode practice without additional Ori coaching on the recorded path; help elsewhere and comprehension are not measured. Skips, declines and inactivity never lower these records.',
        'learning-caption'
      )
    );
    if (!summary || summary.coverage === 'unknown') {
      panel.append(
        learningElement(
          d,
          'p',
          'Practice records are unavailable. Nothing about your ability has been inferred.'
        )
      );
    } else {
      if (summary.coverage !== 'available')
        panel.append(
          learningElement(
            d,
            'p',
            'Only part of the local record is available. Counts may be incomplete.',
            'learning-caption'
          )
        );
      const names = {
        delegate: 'Delegate',
        evaluate: 'Evaluate',
        improve: 'Improve',
        reuse: 'Reuse'
      };
      const states = {
        not_yet_observed: 'Not yet observed',
        practiced_with_guidance: 'Practiced with guidance',
        practiced_independently: 'Practiced independently',
        reused_successfully: 'Reused successfully',
        support_unknown: 'Practice recorded · support unknown'
      };
      for (const item of summary.items || []) {
        if (!Object.hasOwn(names, item.competency)) continue;
        const section = learningElement(d, 'section', undefined, 'learning-coaching');
        section.setAttribute('aria-label', `${names[item.competency]} practice`);
        section.append(
          learningElement(d, 'h4', names[item.competency]),
          learningElement(
            d,
            'p',
            Object.hasOwn(states, item.state) ? states[item.state] : 'Practice coverage unknown'
          )
        );
        if (item.attempts)
          section.append(
            learningElement(
              d,
              'p',
              `${item.attempts} recorded attempt${item.attempts === 1 ? '' : 's'} · ${item.guided_attempts} guided · ${item.independent_attempts} basic · ${item.unknown_support_attempts} support unknown. These are user decisions, not objective quality ratings.`,
              'learning-caption'
            )
          );
        for (const [index, key] of (item.evidence_keys || []).entries()) {
          if (!/^[a-f0-9]{64}$/.test(key)) continue;
          section.append(
            learningButton(
              d,
              `Inspect ${names[item.competency].toLowerCase()} review ${index + 1}`,
              () =>
                this.action(async () => {
                  let evidence;
                  try {
                    evidence = await this.api.get(`${learningRoot}/evidence/${key}`);
                  } catch (error) {
                    if (error.status === 404) {
                      await this.refresh();
                      return 'The supporting source or full receipt is unavailable. Saved practice was refreshed; no missing content was reconstructed.';
                    }
                    throw error;
                  }
                  this.practiceForm();
                  const detail = learningElement(d, 'section', undefined, 'learning-original');
                  detail.setAttribute('aria-label', 'Recorded review evidence');
                  const heading = learningElement(
                    d,
                    'h4',
                    'Saved decision · exact reviewed result'
                  );
                  heading.tabIndex = -1;
                  detail.append(
                    heading,
                    learningElement(
                      d,
                      'p',
                      `Decision: ${evidence.review.decision} · Reason: ${evidence.review.reason.replaceAll('_', ' ')}`,
                      'learning-caption'
                    )
                  );
                  const support =
                    evidence.evidence?.support_version === 1
                      ? evidence.evidence.guidance
                      : 'unknown';
                  detail.append(
                    learningElement(d, 'p', `Recorded Ori support: ${support}`, 'learning-caption')
                  );
                  if (evidence.review.result)
                    detail.append(
                      learningElement(
                        d,
                        'p',
                        `Run ${evidence.review.result.run_id} · Artifact ${evidence.review.result.artifact_id}`,
                        'learning-caption'
                      )
                    );
                  if (evidence.review.note)
                    detail.append(learningElement(d, 'p', evidence.review.note));
                  if (evidence.review.change)
                    detail.append(
                      learningElement(d, 'p', `Reported change: ${evidence.review.change}`)
                    );
                  if (evidence.original)
                    detail.append(learningOriginal(d, evidence.original, true));
                  detail.append(
                    learningElement(d, 'h4', 'Recorded execution input'),
                    learningResult(d, 'Recorded execution input', evidence.input_text),
                    learningElement(d, 'h4', 'Exact reviewed result'),
                    learningResult(d, 'Exact reviewed result', evidence.result_text)
                  );
                  const href = learningTicketHref(evidence);
                  if (href) {
                    const link = learningElement(d, 'a', 'Open supporting Ticket');
                    link.href = href;
                    detail.append(link);
                  }
                  this.body.append(detail);
                  focusLearningControl(heading);
                })
            )
          );
        }
        panel.append(section);
      }
    }
    panel.append(learningButton(d, 'Back to learning', () => this.action(() => this.refresh())));
    this.body.append(panel);
    focusLearningControl(title);
  }

  async preferencesForm() {
    const data = await this.api.get('/api/workspaces');
    const rows = (data.folders || data.workspaces || []).filter(ws => ws.kind !== 'group');
    this.render();
    const d = this.doc;
    const prefs = this.status.owner.preferences;
    const model = this.status.model;
    const form = learningElement(d, 'section', undefined, 'learning-form');
    form.setAttribute('aria-label', 'Learning preferences');
    const title = learningElement(d, 'h4', 'Your learning preferences');
    title.tabIndex = -1;
    const select = (choices, value) => {
      const control = learningElement(d, 'select', undefined, 'form-select');
      for (const [key, label] of choices) {
        const option = learningElement(d, 'option', label);
        option.value = key;
        control.append(option);
      }
      control.value = value;
      return control;
    };
    const check = value => {
      const control = learningElement(d, 'input');
      control.type = 'checkbox';
      control.checked = Boolean(value);
      return control;
    };
    const mode = select(
      [
        ['on_request', 'Only when I ask'],
        ['automatic', 'Occasional automatic suggestions'],
        ['off', 'Coaching off']
      ],
      prefs.mode
    );
    const guidance = select(
      [
        ['guided', 'Guided — additional coaching'],
        ['basic', 'Basic — criteria without additional hints']
      ],
      prefs.guidance || 'guided'
    );
    const effort = select(
      [
        ['small', 'Small'],
        ['standard', 'Standard']
      ],
      prefs.effort || 'small'
    );
    const focus = learningElement(d, 'input', undefined, 'form-control');
    focus.type = 'text';
    focus.maxLength = 300;
    focus.value = prefs.focus || '';
    const fullGuidance = check(prefs.full_guidance);
    fullGuidance.disabled = guidance.value === 'basic';
    guidance.addEventListener('change', () => {
      fullGuidance.disabled = guidance.value === 'basic';
    });
    const observe = check(prefs.observe);
    const automaticConsent = check(false);
    const resetExclusions = check(false);
    const clearConsent = check(false);
    const scopes = learningElement(d, 'fieldset', undefined, 'learning-scopes');
    scopes.append(learningElement(d, 'legend', 'Approved workspaces · choose at most five'));
    const choices = rows.map(ws => {
      const checked = check(prefs.workspace_ids?.includes(ws.id));
      const name = ws.name || 'Workspace';
      const identity =
        rows.filter(row => (row.name || 'Workspace') === name).length > 1
          ? `${name} · ${ws.id}`
          : name;
      scopes.append(learningField(d, `Include ${identity}`, checked));
      return { id: ws.id, checked };
    });
    if (!choices.length)
      scopes.append(
        learningElement(
          d,
          'p',
          'No current workspace is available. You can still turn coaching off.'
        )
      );
    form.append(
      title,
      learningElement(
        d,
        'p',
        'These are learning permissions only. Saving never creates or starts a Ticket, requests a suggestion, or grants file or tool access.',
        'learning-caption'
      ),
      learningField(d, 'Coaching mode', mode),
      learningField(d, 'My explicit focus (optional)', focus),
      learningField(d, 'Guidance depth', guidance),
      learningField(d, 'Keep full optional coaching in guided mode', fullGuidance),
      learningElement(
        d,
        'p',
        'Future guided offers can use briefer optional coaching after two exact independent Ori-path attempts in the selected scope. Unknown support and visits never trigger that change. Criteria remain complete, and brief guided practice is still recorded as guided. Choose full coaching whenever useful.',
        'learning-caption'
      ),
      learningField(d, 'Preferred effort', effort),
      scopes,
      learningField(
        d,
        'Observe new bounded activity and reviewed work in these workspaces',
        observe
      ),
      learningElement(
        d,
        'p',
        'Observation is local and starts with consent, not a scan of past conversations or work. Missing evidence stays unknown. Removing a scope or turning coaching off stops ongoing observation; actual Tickets and earned practice remain.',
        'learning-caption'
      ),
      learningElement(d, 'h4', 'Automatic suggestions need separate consent'),
      learningElement(
        d,
        'p',
        model
          ? `Configured Default model: ${model.provider} / ${model.model}${model.effort ? ` · ${model.effort}` : ''}. A suggestion shares bounded metadata and previously reviewed goals from the approved workspaces, explicit preferences, local review-practice counts and separately opted-in activity counts. No chats, files or result bodies. Calls may incur provider charges.`
          : 'The configured Default model is unavailable or cannot enforce isolated, single-attempt, token-limited calls. Automatic suggestions cannot be enabled. On-request generic practice remains available.',
        'learning-caption'
      ),
      learningElement(
        d,
        'p',
        'Only a newly recorded, distinct result-specific review can trigger a suggestion; rejection is valid practice too. At most one automatic suggestion call per rolling day and ten suggestion-design calls total per rolling day, at least 30 seconds apart. Each call is limited to 16 KiB input, 1200 output tokens and 30 seconds. Visits and refreshes never generate, and restart does not catch up missed suggestions. A changed model requires renewed consent.',
        'learning-caption'
      ),
      learningField(
        d,
        'Approve automatic suggestions with this model and these scopes',
        automaticConsent
      )
    );
    automaticConsent.disabled = !model;
    const exclusions = prefs.exclusions || [];
    if (exclusions.length) {
      form.append(learningElement(d, 'h4', 'Saved exclusions'));
      const list = learningElement(d, 'ul', undefined, 'learning-steps');
      for (const exclusion of exclusions) {
        const workspace =
          rows.find(ws => ws.id === exclusion.studio_id)?.name || 'Unavailable workspace';
        const structure = learningStructures[exclusion.structure]?.label || 'Unavailable practice';
        const until = exclusion.until_ms
          ? `Not now until ${new Date(exclusion.until_ms).toLocaleDateString()}`
          : 'Not interested';
        list.append(
          learningElement(
            d,
            'li',
            `${workspace} · ${structure} · ${until}${exclusion.topic ? ' · saved focus' : ''}`
          )
        );
      }
      form.append(
        list,
        learningField(d, 'Reset saved Not now and Not interested exclusions', resetExclusions)
      );
    }
    form.append(
      learningButton(d, 'Save learning preferences', () =>
        this.action(async () => {
          const ids = choices.filter(choice => choice.checked.checked).map(choice => choice.id);
          if (ids.length > 5) throw new Error('Choose at most five approved workspaces.');
          if (
            mode.value !== 'off' &&
            (observe.checked || mode.value === 'automatic') &&
            !ids.length
          )
            throw new Error('Choose a workspace for ongoing observation or suggestions.');
          if (mode.value === 'automatic' && (!model || !automaticConsent.checked))
            throw new Error(
              'Review and approve this configured model and the selected scopes before enabling automatic suggestions.'
            );
          await this.api.mutate(`${learningRoot}/preferences`, this.status.owner.revision, {
            mode: mode.value,
            workspace_ids: mode.value === 'off' ? [] : ids,
            observe: mode.value !== 'off' && observe.checked,
            guidance: guidance.value,
            full_guidance: guidance.value === 'guided' && fullGuidance.checked,
            effort: effort.value,
            focus: focus.value,
            consent_version: 1,
            reset_exclusions: resetExclusions.checked,
            ...(mode.value === 'automatic' ? { model_fingerprint: model.fingerprint } : {})
          });
          await this.refresh();
          return 'Learning preferences saved. No suggestion was requested; existing Tickets and Runs are unchanged.';
        })
      ),
      learningField(
        d,
        'Confirm clearing preferences, scope consent and exclusions — keep recorded practice',
        clearConsent
      ),
      learningButton(d, 'Clear learning preferences', () =>
        this.action(async () => {
          if (!clearConsent.checked)
            throw new Error('Confirm which learning preferences will be cleared first.');
          await this.api.mutate(`${learningRoot}/clear`, this.status.owner.revision, {
            scope: 'preferences'
          });
          await this.refresh();
          return 'Preferences cleared. Ongoing observation and automatic suggestions need fresh consent; recorded practice and actual work remain.';
        })
      ),
      learningButton(d, 'Back to learning', () => this.action(() => this.refresh()))
    );
    this.body.append(form);
    focusLearningControl(title);
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
        'With personalization unchecked, generic practice makes no model call. Neither choice grants file access or executes work.',
        'learning-caption'
      )
    );
    const structure = learningElement(this.doc, 'select', undefined, 'form-select');
    const automaticChoice = learningElement(
      this.doc,
      'option',
      'Let Ori choose from eligible practice'
    );
    automaticChoice.value = '';
    structure.append(automaticChoice);
    for (const [value, { label }] of Object.entries(learningStructures)) {
      const option = learningElement(this.doc, 'option', label);
      option.value = value;
      structure.append(option);
    }
    structure.value = '';
    const personalize = learningElement(this.doc, 'input');
    personalize.type = 'checkbox';
    personalize.checked = false;
    const model = this.status.model;
    personalize.disabled = !model;
    form.append(
      learningField(this.doc, 'Practice structure', structure),
      learningField(
        this.doc,
        'Personalize this request with the configured Default model',
        personalize
      ),
      learningElement(
        this.doc,
        'p',
        model
          ? `Default model: ${model.provider} / ${model.model}${model.effort ? ` · ${model.effort}` : ''}. Checking this box authorizes one call, which may incur provider charges. It sends this workspace’s bounded display metadata, eligible practice choices and previously reviewed goals, explicit learning preferences, local review-practice counts and any separately opted-in activity counts. No file contents, conversations or result bodies. At most 16 KiB input, 1200 output tokens and 30 seconds; ten calls per rolling day, at least 30 seconds apart. It does not enable ongoing observation or automatic suggestions.`
          : 'Personalization is unavailable: the configured Default model must support isolated, single-attempt, token-limited calls. Generic practice remains available.',
        'learning-caption'
      ),
      learningButton(this.doc, 'Request a practice', () =>
        this.action(async () => {
          const out = await this.api.mutate(
            `${learningRoot}/proposals`,
            this.status.owner.revision,
            {
              studio_id: select.value,
              structure: structure.value,
              ...(personalize.checked && model
                ? { personalize: true, consent_version: 1, model_fingerprint: model.fingerprint }
                : {})
            }
          );
          await this.refresh();
          return proposalMessage(out);
        })
      )
    );
    this.body.append(form);
    select.focus();
  }

  async adjustForm(instance, kind) {
    const d = this.doc;
    const smaller = kind === 'smaller';
    const data = smaller ? null : await this.api.get('/api/workspaces');
    this.render();
    const form = learningElement(d, 'section', undefined, 'learning-form');
    const title = learningElement(
      d,
      'h4',
      smaller ? 'A smaller input boundary' : 'Choose different eligible practice'
    );
    title.tabIndex = -1;
    form.append(
      title,
      learningElement(
        d,
        'p',
        smaller
          ? `Limit the supplied input or correction to ${Math.max(256, Math.floor((instance.definition.input_limit || 4096) / 2))} bytes and one focus. Exact-result review criteria stay the same. This does not promise a lower provider charge.`
          : 'Choose a workspace for a different eligible process or reviewed original. If none is available, the current contract is kept; Ori will not disguise the same choice as a new one.'
      ),
      learningElement(
        d,
        'p',
        'No model call, Ticket creation or execution. An active mission remains unchanged until you explicitly accept a replacement.',
        'learning-caption'
      )
    );
    const select = learningElement(d, 'select', undefined, 'form-select');
    if (!smaller) {
      const rows = (data.folders || data.workspaces || []).filter(ws => ws.kind !== 'group');
      for (const ws of rows) {
        const option = learningElement(
          d,
          'option',
          rows.filter(row => row.name === ws.name).length > 1
            ? `${ws.name} · ${ws.id}`
            : ws.name || 'Workspace'
        );
        option.value = ws.id;
        select.append(option);
      }
      select.value = instance.definition.studio_id;
      if (!select.value && select.options.length) select.value = select.options[0].value;
      form.append(learningField(d, 'Alternative workspace', select));
    }
    form.append(
      learningButton(d, smaller ? 'Preview smaller practice' : 'Find a different option', () =>
        this.action(async () => {
          if (!smaller && !select.value)
            throw new Error('Choose an available workspace. Your current contract is unchanged.');
          await this.api.mutate(
            `${learningRoot}/instances/${encodeURIComponent(instance.id)}/alternatives`,
            this.status.owner.revision,
            {
              instance_revision: instance.revision,
              kind,
              ...(!smaller ? { studio_id: select.value } : {})
            }
          );
          await this.refresh();
          return 'A new proposal is saved. Nothing was executed; any active contract is unchanged until replacement acceptance.';
        })
      ),
      learningButton(d, 'Back to learning', () => this.action(() => this.refresh()))
    );
    this.body.append(form);
    focusLearningControl(title);
  }

  async prepareForm(instance) {
    const ws = await this.api.get(
      `/api/workspaces/${encodeURIComponent(instance.definition.studio_id)}`
    );
    const revision = instance.definition.structure === 'improve_result';
    const reuse = instance.definition.structure === 'repeat_success';
    const original =
      revision || reuse
        ? await this.api.get(
            `${learningRoot}/instances/${encodeURIComponent(instance.id)}/original`
          )
        : null;
    this.render();
    const d = this.doc;
    const input = learningElement(d, 'textarea', undefined, 'form-control');
    input.rows = 5;
    const inputLimit = instance.definition.input_limit || 4096;
    const limitLabel = inputLimit === 4096 ? '4 KiB' : `${inputLimit} bytes`;
    input.maxLength = inputLimit;
    const select = learningElement(d, 'select', undefined, 'form-select');
    for (const name of [...new Set((ws.agent_instances || []).map(agent => agent.name))]) {
      const option = learningElement(d, 'option', name);
      option.value = name;
      select.append(option);
    }
    const form = learningElement(d, 'div', undefined, 'learning-form');
    if (original) form.append(learningOriginal(d, original, reuse));
    const inputChange = learningElement(d, 'textarea', undefined, 'form-control');
    inputChange.rows = 3;
    inputChange.maxLength = 500;
    form.append(
      learningField(
        d,
        revision
          ? `Correction (maximum ${limitLabel})`
          : reuse
            ? `New text and drafting requirements (maximum ${limitLabel})`
            : `Text and drafting requirements (maximum ${limitLabel})`,
        input
      ),
      learningField(d, 'Assigned agent', select),
      learningElement(
        d,
        'p',
        `${revision ? 'Only the exact original result shown above and this correction can be sent.' : 'Only this newly supplied text can be sent; previous results are not inherited.'} Saving creates a Ready Ticket; it does not run it. Review the current model and cost on that Ticket before Start.`,
        'learning-caption'
      )
    );
    if (reuse)
      form.append(
        learningField(d, 'What is materially different? (required, 500 characters)', inputChange)
      );
    form.append(
      learningButton(
        d,
        revision
          ? 'Save reviewed correction as a Ticket'
          : reuse
            ? 'Save reviewed new input as a Ticket'
            : 'Save reviewed input as a Ticket',
        () =>
          this.action(async () => {
            if (
              !input.value.trim() ||
              new TextEncoder().encode(input.value.trim()).length > inputLimit ||
              !select.value
            )
              throw new Error(`Choose an agent and supply nonempty text of at most ${limitLabel}.`);
            if (
              reuse &&
              (!inputChange.value.trim() ||
                input.value.trim().split(/\s+/u).join(' ') ===
                  original.input.trim().split(/\s+/u).join(' '))
            )
              throw new Error(
                'Supply materially different input and explain the difference. Whitespace changes do not qualify.'
              );
            await this.api.mutate(
              `${learningRoot}/instances/${encodeURIComponent(instance.id)}/work`,
              this.status.owner.revision,
              {
                instance_revision: instance.revision,
                input: input.value,
                assignee: select.value,
                ...(reuse ? { input_change: inputChange.value } : {})
              }
            );
            await this.refresh();
          })
      )
    );
    form.append(learningButton(d, 'Back to learning', () => this.action(() => this.refresh())));
    this.body.append(form);
    focusLearningControl(original ? form.querySelector('h4') : input);
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
