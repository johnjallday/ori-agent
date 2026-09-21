// blueprint-intake-review.js — reviewed proposal UI for intake_review steps.
(function () {
  'use strict';

  const states = new Map();

  function key(ctx) {
    return `${ctx.workspaceId}:${ctx.step?.intake_key || ''}`;
  }

  function base(ctx) {
    return `/api/workspaces/${encodeURIComponent(ctx.workspaceId)}/blueprint-intakes/${encodeURIComponent(ctx.step.intake_key)}`;
  }

  function node(tag, className, text) {
    const element = document.createElement(tag);
    if (className) element.className = className;
    if (text !== undefined) element.textContent = text;
    return element;
  }

  async function request(ctx, suffix, options) {
    const response = await fetch(base(ctx) + suffix, options || {});
    const payload = await response.json().catch(() => ({}));
    if (!response.ok)
      throw new Error(payload.message || payload.error || 'The intake request failed.');
    return payload;
  }

  function getState(ctx) {
    let state = states.get(key(ctx));
    if (!state) {
      state = {
        loading: false,
        proposal: null,
        run: null,
        skill: null,
        bundledSkill: null,
        skillChoice: '',
        selections: new Map(),
        showUnchanged: false
      };
      states.set(key(ctx), state);
    }
    return state;
  }

  function render(container, ctx) {
    const state = getState(ctx);
    state.container = container;
    draw(container, ctx, state);
    if (!state.loading && !state.proposal && !state.run) void load(container, ctx, state);
  }

  async function load(container, ctx, state) {
    state.loading = true;
    ctx.setBusy(true, 'Loading proposal…');
    try {
      const payload = await request(ctx, '', { method: 'GET' });
      state.proposal = payload.proposal || null;
      state.run = payload.run || null;
      state.skill = payload.skill || null;
      state.bundledSkill = payload.bundled_skill || null;
      state.skillChoice = state.bundledSkill?.collision ? '' : 'bundled';
      seedSelections(state);
      draw(container, ctx, state);
      if (state.run?.state === 'running') void poll(container, ctx, state);
    } catch (error) {
      ctx.setError(error.message);
    } finally {
      state.loading = false;
      ctx.setBusy(false, '');
      draw(container, ctx, state);
    }
  }

  function seedSelections(state) {
    if (!state.proposal || state.selections.size) return;
    state.proposal.items.forEach(item => {
      state.selections.set(item.key, {
        key: item.key,
        selected:
          !item.unusable_reason &&
          !item.disabled_reason &&
          item.classification !== 'unchanged' &&
          item.classification !== 'no_longer_found',
        title: item.title,
        due_at: item.due_input || (item.due_at ? toLocalInput(item.due_at) : ''),
        start: item.all_day ? item.start?.slice(0, 10) || '' : toLocalInput(item.start),
        end: item.all_day ? item.end?.slice(0, 10) || '' : toLocalInput(item.end),
        conflict_resolution: item.conflict ? 'keep_current' : ''
      });
    });
  }

  function draw(container, ctx, state) {
    container.textContent = '';
    if (state.loading && !state.proposal) {
      container.appendChild(node('p', 'blueprint-intake-loading', 'Loading proposal…'));
      return;
    }
    if (!state.proposal) {
      drawRunState(container, ctx, state);
      return;
    }
    drawProposal(container, ctx, state);
  }

  function drawRunState(container, ctx, state) {
    if (state.skill && !state.skill.ready) {
      if (state.bundledSkill) {
        drawSkillReview(container, ctx, state);
        return;
      }
      const message = state.skill.missing
        ? `${state.skill.name} is ${state.skill.missing}. Review and enable it for ${state.skill.agent || 'the workspace entry agent'} before running.`
        : 'The required intake skill is not ready.';
      container.appendChild(node('p', 'blueprint-intake-skill-missing', message));
      return;
    }
    if (!state.run) {
      container.appendChild(
        node(
          'p',
          '',
          'Your sources are ready. Run the reviewed intake skill to prepare a proposal.'
        )
      );
      return;
    }
    const heading = node(
      'h4',
      '',
      state.run.state === 'running' ? 'Reading sources' : 'Intake run'
    );
    container.appendChild(heading);
    const list = node('ul', 'blueprint-intake-progress');
    (state.run.sources || []).forEach(source => {
      list.appendChild(
        node(
          'li',
          `blueprint-intake-progress-${source.state}`,
          `${source.source_name}: ${source.state}`
        )
      );
    });
    container.appendChild(list);
    if (state.run.error)
      container.appendChild(node('p', 'blueprint-intake-run-error', state.run.error));
    if (state.run.state === 'running') {
      const cancel = node('button', 'modern-btn modern-btn-secondary', 'Cancel');
      cancel.type = 'button';
      cancel.addEventListener('click', () => cancelRun(container, ctx, state));
      container.appendChild(cancel);
    }
  }

  function drawSkillReview(container, ctx, state) {
    const review = state.bundledSkill;
    container.appendChild(node('h4', '', `Review ${review.name}`));
    container.appendChild(node('p', '', review.description));
    const bundled = document.createElement('details');
    bundled.open = true;
    bundled.appendChild(node('summary', '', 'Bundled skill instructions'));
    bundled.appendChild(node('pre', 'blueprint-intake-skill-text', review.bundled_text));
    container.appendChild(bundled);
    if (review.collision) {
      container.appendChild(
        node(
          'p',
          'blueprint-intake-skill-missing',
          'A different skill with this name already exists. Compare both and choose which one this workspace should use.'
        )
      );
      const existing = document.createElement('details');
      existing.appendChild(node('summary', '', 'Existing skill instructions'));
      existing.appendChild(node('pre', 'blueprint-intake-skill-text', review.existing_text));
      container.appendChild(existing);
      const choices = node('div', 'blueprint-intake-skill-choices');
      for (const [value, labelText] of [
        ['existing', 'Use the existing skill'],
        ['bundled', 'Replace it with the bundled skill']
      ]) {
        const label = node('label', '');
        const radio = document.createElement('input');
        radio.type = 'radio';
        radio.name = `blueprint-intake-skill-${key(ctx)}`;
        radio.value = value;
        radio.checked = state.skillChoice === value;
        radio.addEventListener('change', () => (state.skillChoice = value));
        label.append(radio, document.createTextNode(labelText));
        choices.appendChild(label);
      }
      container.appendChild(choices);
    }
    const trust = node('button', 'modern-btn modern-btn-primary', 'Trust and enable');
    trust.type = 'button';
    trust.disabled = review.collision && !state.skillChoice;
    trust.addEventListener('click', () => trustSkill(container, ctx, state));
    container.appendChild(trust);
  }

  async function trustSkill(container, ctx, state) {
    ctx.setBusy(true, 'Trusting skill…');
    try {
      const payload = await request(ctx, '/skill/trust', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ choice: state.skillChoice })
      });
      state.bundledSkill = payload.bundled_skill;
      const refreshed = await request(ctx, '', { method: 'GET' });
      state.skill = refreshed.skill || null;
      state.bundledSkill = refreshed.bundled_skill || state.bundledSkill;
      draw(container, ctx, state);
      ctx.announce(`${state.skill.name} is trusted and enabled.`);
    } catch (error) {
      ctx.setError(error.message);
    } finally {
      ctx.setBusy(false, '');
    }
  }

  function drawProposal(container, ctx, state) {
    const proposal = state.proposal;
    const counts = {};
    const countLabels = {
      ticket: ['ticket', 'tickets'],
      memory: ['memory entry', 'memory entries'],
      note: ['note', 'notes'],
      calendar_event: ['calendar event', 'calendar events']
    };
    proposal.items.forEach(item => (counts[item.kind] = (counts[item.kind] || 0) + 1));
    const summary = node('div', 'blueprint-intake-proposal-counts');
    const classified = proposal.items.some(item => item.classification);
    if (classified) {
      const newCount = proposal.items.filter(item => item.classification === 'new').length;
      const changedCount = proposal.items.filter(item => item.classification === 'changed').length;
      summary.appendChild(
        node(
          'strong',
          'blueprint-intake-proposal-count',
          `${newCount} new item${newCount === 1 ? '' : 's'}`
        )
      );
      summary.appendChild(
        node('strong', 'blueprint-intake-proposal-count', `${changedCount} changed`)
      );
    }
    Object.entries(counts).forEach(([kind, count]) => {
      const labels = countLabels[kind] || [kind, `${kind}s`];
      summary.appendChild(
        node('strong', 'blueprint-intake-proposal-count', `${count} ${labels[count === 1 ? 0 : 1]}`)
      );
    });
    if (!proposal.items.length)
      summary.appendChild(node('strong', '', 'No supported items proposed'));
    container.appendChild(summary);
    if (proposal.notice?.truncated)
      container.appendChild(
        node('p', 'blueprint-intake-notice', 'Only the first 200 items are shown.')
      );
    if (proposal.notice?.unsupported)
      container.appendChild(
        node(
          'p',
          'blueprint-intake-notice',
          `${proposal.notice.unsupported} unsupported item${proposal.notice.unsupported === 1 ? ' was' : 's were'} left out.`
        )
      );

    const unchangedCount = proposal.items.filter(
      item => item.classification === 'unchanged'
    ).length;
    if (unchangedCount) {
      const unchanged = node(
        'button',
        'modern-btn modern-btn-secondary blueprint-intake-show-unchanged',
        state.showUnchanged ? 'Hide unchanged' : `Show ${unchangedCount} unchanged`
      );
      unchanged.type = 'button';
      unchanged.addEventListener('click', () => {
        state.showUnchanged = !state.showUnchanged;
        draw(container, ctx, state);
      });
      container.appendChild(unchanged);
    }

    const groupLabels = {
      ticket: 'Tickets',
      memory: 'Memory entries',
      note: 'Notes',
      calendar_event: 'Calendar events'
    };
    Object.keys(groupLabels).forEach(kind => {
      const groupItems = proposal.items.filter(
        item => item.kind === kind && (state.showUnchanged || item.classification !== 'unchanged')
      );
      if (!groupItems.length) return;
      container.appendChild(node('h4', 'blueprint-intake-proposal-group', groupLabels[kind]));
      groupItems.forEach(item => {
        const choice = state.selections.get(item.key);
        const card = node('article', 'blueprint-intake-proposal-item');
        const selectLabel = node('label', 'blueprint-intake-proposal-select');
        const checkbox = document.createElement('input');
        checkbox.type = 'checkbox';
        checkbox.checked = Boolean(choice?.selected);
        checkbox.disabled =
          proposal.status !== 'pending' ||
          item.classification === 'unchanged' ||
          item.classification === 'no_longer_found' ||
          Boolean(item.unusable_reason || item.disabled_reason);
        checkbox.addEventListener('change', () => (choice.selected = checkbox.checked));
        const actionLabel =
          item.classification === 'changed'
            ? 'Update'
            : item.classification === 'no_longer_found'
              ? 'Information only:'
              : 'Add';
        selectLabel.append(
          checkbox,
          document.createTextNode(` ${actionLabel} ${(countLabels[item.kind] || [item.kind])[0]}`)
        );
        card.appendChild(selectLabel);
        if (item.classification)
          card.appendChild(
            node(
              'strong',
              'blueprint-intake-classification',
              item.classification.replaceAll('_', ' ')
            )
          );
        (item.changes || []).forEach(change => {
          card.appendChild(
            node(
              'p',
              'blueprint-intake-change',
              `${change.field} was ${change.before || 'not set'}, now ${change.after || 'not set'}`
            )
          );
        });
        if (item.conflict) {
          const conflict = node('fieldset', 'blueprint-intake-conflict');
          conflict.appendChild(
            node('legend', '', 'This record was edited after intake. Choose which value to keep.')
          );
          [
            ['keep_current', `Keep current: ${item.conflict.current}`],
            ['use_proposed', `Use proposal: ${item.conflict.proposed}`]
          ].forEach(([value, text]) => {
            const label = node('label', '', text);
            const radio = document.createElement('input');
            radio.type = 'radio';
            radio.name = `conflict-${item.key}`;
            radio.value = value;
            radio.checked = choice?.conflict_resolution === value;
            radio.addEventListener('change', () => (choice.conflict_resolution = value));
            label.prepend(radio);
            conflict.appendChild(label);
          });
          card.appendChild(conflict);
        }

        if (item.kind !== 'memory') {
          const titleLabel = node('label', '', 'Title');
          const title = document.createElement('input');
          title.type = 'text';
          title.value = choice?.title || item.title;
          title.maxLength = 240;
          title.disabled =
            proposal.status !== 'pending' || Boolean(item.unusable_reason || item.disabled_reason);
          title.addEventListener('input', () => (choice.title = title.value));
          titleLabel.appendChild(title);
          card.appendChild(titleLabel);
        }

        if (item.kind === 'memory')
          card.appendChild(node('p', 'blueprint-intake-proposal-text', item.text));
        if (item.kind === 'note')
          card.appendChild(node('pre', 'blueprint-intake-proposal-text', item.body));
        if (item.kind === 'calendar_event') {
          const startLabel = node('label', '', item.all_day ? 'Date' : 'Starts');
          const start = document.createElement('input');
          start.type = item.all_day ? 'date' : 'datetime-local';
          start.value = choice?.start || '';
          start.disabled =
            proposal.status !== 'pending' || Boolean(item.unusable_reason || item.disabled_reason);
          start.addEventListener('input', () => (choice.start = start.value));
          startLabel.appendChild(start);
          card.appendChild(startLabel);
          if (!item.all_day) {
            const endLabel = node('label', '', 'Ends');
            const end = document.createElement('input');
            end.type = 'datetime-local';
            end.value = choice?.end || '';
            end.disabled = start.disabled;
            end.addEventListener('input', () => (choice.end = end.value));
            endLabel.appendChild(end);
            card.appendChild(endLabel);
          }
          if (item.location)
            card.appendChild(node('p', 'blueprint-intake-proposal-text', item.location));
        }
        if (item.unusable_reason || item.disabled_reason)
          card.appendChild(
            node('p', 'blueprint-intake-notice', item.unusable_reason || item.disabled_reason)
          );

        if (item.kind === 'ticket') {
          const dateLabel = node('label', '', 'Due date');
          const date = document.createElement('input');
          date.type = item.no_time_given ? 'date' : 'datetime-local';
          date.value = choice?.due_at || '';
          date.disabled = proposal.status !== 'pending';
          date.addEventListener('input', () => (choice.due_at = date.value));
          dateLabel.appendChild(date);
          if (item.due_at)
            dateLabel.appendChild(
              node('span', 'blueprint-intake-weekday', formatDate(item.due_at))
            );
          if (item.no_time_given)
            dateLabel.appendChild(node('span', 'blueprint-intake-marker', 'No time given'));
          card.appendChild(dateLabel);
        }
        if (item.partly_read)
          card.appendChild(node('span', 'blueprint-intake-marker', 'Source partly read'));
        const evidence = document.createElement('details');
        evidence.appendChild(node('summary', '', 'Show source quote'));
        evidence.appendChild(node('blockquote', '', item.source?.quote || 'No supporting quote.'));
        card.appendChild(evidence);
        const result = (proposal.results || []).find(entry => entry.key === item.key);
        if (result)
          card.appendChild(
            node(
              'p',
              `blueprint-intake-apply-${result.status}`,
              result.reason ? `${result.status}: ${result.reason}` : result.status
            )
          );
        container.appendChild(card);
      });
    });

    if (proposal.status === 'pending') {
      const skip = node(
        'button',
        'modern-btn modern-btn-secondary blueprint-intake-skip',
        'Skip this proposal'
      );
      skip.type = 'button';
      skip.addEventListener('click', () => skipProposal(container, ctx, state));
      container.appendChild(skip);
    }
  }

  function toLocalInput(value) {
    if (!value) return '';
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return '';
    const offset = date.getTimezoneOffset() * 60_000;
    return new Date(date.getTime() - offset).toISOString().slice(0, 16);
  }

  function formatDate(value) {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return value;
    return new Intl.DateTimeFormat(undefined, {
      weekday: 'long',
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: 'numeric',
      minute: '2-digit'
    }).format(date);
  }

  async function startRun(container, ctx, state) {
    ctx.setBusy(true, 'Starting intake…');
    try {
      const payload = await request(ctx, '/run', { method: 'POST' });
      state.run = payload.run;
      draw(container, ctx, state);
      void poll(container, ctx, state);
    } catch (error) {
      ctx.setError(error.message);
    } finally {
      ctx.setBusy(false, '');
    }
  }

  async function poll(container, ctx, state) {
    while (state.run?.state === 'running') {
      await new Promise(resolve => setTimeout(resolve, 750));
      try {
        const payload = await request(ctx, '/run', { method: 'GET' });
        state.run = payload.run;
        if (state.run.proposal) {
          state.proposal = state.run.proposal;
          seedSelections(state);
        }
        draw(container, ctx, state);
      } catch (error) {
        ctx.setError(error.message);
        return;
      }
    }
    ctx.setBusy(false, '');
  }

  async function cancelRun(container, ctx, state) {
    try {
      await request(ctx, '/run/cancel', { method: 'POST' });
      ctx.announce('Intake cancellation requested. Results already returned are kept.');
    } catch (error) {
      ctx.setError(error.message);
    }
    draw(container, ctx, state);
  }

  async function apply(container, ctx, state) {
    ctx.setBusy(true, 'Applying selected items…');
    try {
      const payload = await request(ctx, '/proposal/apply', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          proposal_hash: state.proposal.hash,
          items: Array.from(state.selections.values()).map(item => ({
            ...item,
            due_at:
              item.due_at && item.due_at.includes('T')
                ? new Date(item.due_at).toISOString()
                : item.due_at,
            start:
              item.start && item.start.includes('T')
                ? new Date(item.start).toISOString()
                : item.start,
            end: item.end && item.end.includes('T') ? new Date(item.end).toISOString() : item.end
          }))
        })
      });
      state.proposal = payload.proposal;
      draw(container, ctx, state);
      await ctx.confirm();
    } catch (error) {
      ctx.setError(error.message);
    } finally {
      ctx.setBusy(false, '');
    }
  }

  async function skipProposal(container, ctx, state) {
    ctx.setBusy(true, 'Skipping proposal…');
    try {
      const payload = await request(ctx, '/proposal/skip', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ proposal_hash: state.proposal.hash })
      });
      state.proposal = payload.proposal;
      draw(container, ctx, state);
      await ctx.confirm();
    } catch (error) {
      ctx.setError(error.message);
    } finally {
      ctx.setBusy(false, '');
    }
  }

  const renderer = {
    render,
    reset(ctx) {
      states.delete(key(ctx));
    },
    primaryLabel(ctx) {
      const state = getState(ctx);
      if (!state.proposal) return state.run?.state === 'failed' ? 'Retry intake' : 'Run intake';
      if (state.proposal.status === 'pending') return 'Apply selected';
      return 'Continue';
    },
    disablePrimary(ctx) {
      const state = getState(ctx);
      if (state.loading || state.run?.state === 'running') return true;
      if (!state.proposal) return Boolean(state.skill && !state.skill.ready);
      if (state.proposal.status === 'pending')
        return !Array.from(state.selections.values()).some(item => item.selected);
      return false;
    },
    async onPrimary(ctx) {
      const state = getState(ctx);
      const container = state.container;
      if (!state.proposal) return startRun(container, ctx, state);
      if (state.proposal.status === 'pending') return apply(container, ctx, state);
      return ctx.confirm();
    }
  };

  function register() {
    if (!window.SetupWizard?.registerStepRenderer) return false;
    window.SetupWizard.registerStepRenderer('intake_review', renderer);
    return true;
  }
  if (!register()) document.addEventListener('DOMContentLoaded', register);
  window.BlueprintIntakeReview = renderer;
})();
