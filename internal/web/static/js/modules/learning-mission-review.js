import {
  LearningMissions,
  createLearningAPI,
  learningRoot,
  learningElement,
  learningButton,
  learningField,
  learningBlockers
} from './learning-missions.js';

export function learningForTicket(status, workspaceID, ticketID) {
  return (
    (status?.instances || []).find(
      row =>
        row.definition.studio_id === workspaceID &&
        row.progress.ticket_id === ticketID &&
        ['active', 'blocked', 'completed'].includes(row.state)
    ) || null
  );
}

// Mounted on the existing Ticket result page. Neither hydration nor Refresh
// prepares a nonce or runs work. Only the separately labelled buttons do so.
export class LearningTicketReview extends LearningMissions {
  constructor(root, api = createLearningAPI()) {
    super(root, api);
    this.workspaceID = root.dataset.studioId;
    this.ticketID = root.dataset.ticketId;
    this.ticketPath = `/api/workspaces/${encodeURIComponent(this.workspaceID)}/tickets/${encodeURIComponent(this.ticketID)}`;
    this.started = false;
  }

  async refresh() {
    this.status = await this.api.get(learningRoot);
    this.instance = learningForTicket(this.status, this.workspaceID, this.ticketID);
    this.ticket = await this.api.get(this.ticketPath);
    this.execution = null;
    try {
      this.execution = await this.api.get(`${this.ticketPath}/execution`);
    } catch (error) {
      if (!this.instance && error.status === 404) {
        this.root.hidden = true;
        return;
      }
      this.executionError = error.message;
    }
    this.root.hidden = false;
    this.render();
  }

  render() {
    const d = this.doc;
    this.body.replaceChildren();
    const title = learningElement(d, 'h3', 'Bounded draft · learning review');
    title.tabIndex = -1;
    this.body.append(
      title,
      learningElement(
        d,
        'p',
        'Execution permission and learning decisions are separate. Stopping learning does not stop this Ticket.',
        'learning-caption'
      )
    );
    if (this.execution && ['ready', 'backlog'].includes(this.ticket.state) && !this.started) {
      const { executor, input, disclosure } = this.execution;
      this.body.append(
        learningElement(d, 'h4', `${executor.agent} · ${executor.provider} / ${executor.model}`),
        learningElement(d, 'p', disclosure),
        learningElement(d, 'pre', input, 'learning-result')
      );
      this.body.append(
        learningButton(d, 'Start this Ticket with this input and model', () =>
          this.action(async () => {
            this.started = true; // even an unknown response must not auto-retry
            const outcome = await this.api.start(`${this.ticketPath}/start`, executor.digest);
            if (!outcome.start?.started)
              throw new Error(
                outcome.start?.error ||
                  'Start was not confirmed. Check the Ticket before trying again.'
              );
            await this.refresh();
          })
        )
      );
    } else if (!this.execution) {
      this.body.append(
        learningElement(
          d,
          'p',
          this.executionError || 'Execution is unavailable.',
          'learning-blocker'
        )
      );
    }
    const i = this.instance;
    if (i?.state === 'completed') {
      this.body.append(
        learningElement(
          d,
          'p',
          'Result-specific practice recorded. It is not a quality rating or proof of mastery.'
        )
      );
    } else if (i && ['active', 'blocked'].includes(i.state)) {
      if (i.progress.blocker)
        this.body.append(
          learningElement(
            d,
            'p',
            learningBlockers[i.progress.blocker] || 'Evidence is unavailable.',
            'learning-blocker'
          )
        );
      if (i.progress.run_id) {
        this.body.append(
          learningElement(
            d,
            'p',
            `Learning is pinned to Run ${i.progress.run_id}. Later attempts do not replace it.`,
            'learning-caption'
          )
        );
        this.body.append(
          learningButton(d, 'Inspect the exact result for learning', () =>
            this.action(() => this.openReview())
          )
        );
      } else
        this.body.append(
          learningElement(
            d,
            'p',
            'No completed learning attempt yet. Starting learning alone never executes this Ticket.'
          )
        );
    } else {
      this.body.append(
        learningElement(
          d,
          'p',
          'No active learning contract tracks this Ticket. Its bounded recipe remains available for separately reviewed execution.'
        )
      );
    }
    this.body.append(
      learningButton(d, 'Refresh learning and execution status', () =>
        this.action(() => this.refresh())
      )
    );
  }

  async openReview() {
    const i = this.instance;
    const out = await this.api.mutate(
      `${learningRoot}/instances/${encodeURIComponent(i.id)}/review`,
      this.status.owner.revision,
      { instance_revision: i.revision, action: 'prepare' }
    );
    if (!out.review || !out.result_text)
      throw new Error('Exact result bytes are unavailable. No decision was recorded.');
    this.status = await this.api.get(learningRoot);
    this.renderReview(out);
  }

  renderReview(out) {
    const d = this.doc;
    this.body.replaceChildren();
    const title = learningElement(d, 'h3', 'Inspect this exact result');
    title.tabIndex = -1;
    const ref = out.review.result;
    this.body.append(
      title,
      learningElement(
        d,
        'p',
        `Run ${ref.run_id} · Artifact ${ref.artifact_id}`,
        'learning-caption'
      ),
      learningElement(
        d,
        'p',
        'Complete canonical inline result; no history snippet or model rating. Your reason is a report of your judgment, not proof it is correct.'
      ),
      learningElement(d, 'pre', out.result_text, 'learning-result')
    );
    const select = options => {
      const input = learningElement(d, 'select', undefined, 'form-select');
      const blank = learningElement(d, 'option', 'Choose…');
      blank.value = '';
      input.append(blank);
      for (const [value, label] of options) {
        const option = learningElement(d, 'option', label);
        option.value = value;
        input.append(option);
      }
      return input;
    };
    const decision = select([
      ['use', 'Use it'],
      ['revise', 'Needs revision'],
      ['reject', 'Reject it']
    ]);
    const reason = select([
      ['meets_scope', 'Meets the requested scope'],
      ['missing_detail', 'Missing detail'],
      ['incorrect', 'Incorrect'],
      ['not_useful', 'Not useful'],
      ['other', 'Other (add a note)']
    ]);
    const note = learningElement(d, 'textarea', undefined, 'form-control');
    note.rows = 3;
    note.maxLength = 500;
    this.body.append(
      learningField(d, 'Your decision', decision),
      learningField(d, 'Reason', reason),
      learningField(d, 'Optional note (500 characters)', note)
    );
    this.body.append(
      learningButton(d, 'Record this result-specific decision', () =>
        this.action(async () => {
          if (!decision.value || !reason.value || (reason.value === 'other' && !note.value.trim()))
            throw new Error('Choose a decision and reason; Other requires a note.');
          await this.api.mutate(
            `${learningRoot}/instances/${encodeURIComponent(out.instance.id)}/review`,
            this.status.owner.revision,
            {
              instance_revision: out.instance.revision,
              action: 'submit',
              review_id: out.review.id,
              decision: decision.value,
              reason: reason.value,
              note: note.value
            }
          );
          await this.refresh();
        })
      ),
      learningButton(d, 'Close without a decision', () => this.action(() => this.refresh()))
    );
    decision.focus();
  }
}

if (typeof document !== 'undefined') {
  const init = () => {
    const root = document.getElementById('learningTicketReview');
    if (root) {
      const review = new LearningTicketReview(root);
      void review.action(() => review.refresh());
    }
  };
  if (document.readyState === 'loading')
    document.addEventListener('DOMContentLoaded', init, { once: true });
  else init();
}
