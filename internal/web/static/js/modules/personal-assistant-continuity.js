(function (global) {
  'use strict';

  const API_ROOT = '/api/personal-assistant';

  function unwrap(payload, key) {
    if (!payload || typeof payload !== 'object') return null;
    if (payload.data && typeof payload.data === 'object') return payload.data[key] || null;
    return payload[key] || null;
  }

  function splitList(value) {
    return String(value || '')
      .split(',')
      .map(item => item.trim())
      .filter(Boolean);
  }

  function conflictView(payload) {
    const current = payload && payload.current;
    return {
      message:
        (payload && payload.error) ||
        'The assistant changed. Review the current values, then apply your edit again.',
      current: current && typeof current === 'object' ? current : null,
      requiresReapply: true
    };
  }

  function capabilityCopy(card) {
    const source = card && typeof card === 'object' ? card : {};
    return [
      `Can read: ${source.can_read || 'Nothing until this source is configured.'}`,
      `Can propose: ${source.can_propose || 'No proposals are mapped.'}`,
      `Confirmation: ${source.requires_confirmation || 'Existing confirmation policy applies.'}`,
      source.mapped_write ? 'Mapped write: Yes, behind its existing gate.' : 'Mapped write: No.'
    ];
  }

  async function responseJSON(response) {
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) {
      const error = new Error(payload.error || `Request failed (${response.status})`);
      error.status = response.status;
      error.payload = payload;
      throw error;
    }
    return payload;
  }

  function mount(options) {
    const opts = options || {};
    const doc = opts.document || global.document;
    const fetchImpl = opts.fetch || global.fetch;
    if (!doc || typeof fetchImpl !== 'function') return null;
    const panel = doc.getElementById('personalAssistantContinuity');
    const form = doc.getElementById('personalAssistantContinuityForm');
    if (!panel || !form) return null;

    const els = {
      status: doc.getElementById('personalAssistantContinuityStatus'),
      close: doc.getElementById('personalAssistantContinuityClose'),
      name: doc.getElementById('personalAssistantContinuityName'),
      mandate: doc.getElementById('personalAssistantContinuityMandate'),
      focus: doc.getElementById('personalAssistantContinuityFocus'),
      timezone: doc.getElementById('personalAssistantContinuityTimezone'),
      days: doc.getElementById('personalAssistantContinuityDays'),
      time: doc.getElementById('personalAssistantContinuityTime'),
      scope: doc.getElementById('personalAssistantContinuityScope'),
      future: doc.getElementById('personalAssistantContinuityFuture'),
      enabled: doc.getElementById('personalAssistantContinuityEnabled'),
      notify: doc.getElementById('personalAssistantContinuityNotify'),
      next: doc.getElementById('personalAssistantContinuityNext'),
      pause: doc.getElementById('personalAssistantContinuityPause'),
      memory: doc.getElementById('personalAssistantContinuityMemory'),
      capabilities: doc.getElementById('personalAssistantCapabilities')
    };
    let state = null;
    let busy = false;
    let lastTrigger = null;
    let returnToAssistant = false;
    let returnAssistantView = 'today';

    function announce(message, tone) {
      if (!els.status) return;
      els.status.textContent = message || '';
      els.status.dataset.tone = tone || '';
    }

    function setBusy(value) {
      busy = Boolean(value);
      Array.from(form.querySelectorAll('button, input, select, textarea')).forEach(control => {
        control.disabled = busy;
      });
    }

    function renderState(next) {
      state = next;
      if (!state) return;
      const brief = state.daily_brief || {};
      els.name.value = state.display_name || '';
      els.mandate.value = state.mandate || '';
      els.focus.value = Array.isArray(state.focus_areas) ? state.focus_areas.join(', ') : '';
      els.timezone.value = brief.timezone || '';
      els.days.value = Array.isArray(brief.schedule_days) ? brief.schedule_days.join(', ') : '';
      els.time.value = brief.schedule_time || '';
      const selected = Array.isArray(brief.selected_workspace_ids)
        ? brief.selected_workspace_ids
        : [];
      els.scope.value =
        brief.scope === 'selected' && selected.length === 1 && selected[0] === state.hq_workspace_id
          ? 'personal_hq'
          : brief.scope === 'selected'
            ? 'selected'
            : 'all';
      els.future.checked = Boolean(brief.include_future_workspaces);
      els.enabled.checked = Boolean(brief.schedule_enabled);
      els.notify.checked = Boolean(brief.notify_on_ready);
      if (els.next) {
        els.next.textContent = brief.next_generation_at
          ? `Next scheduled generation: ${brief.next_generation_at}`
          : state.state === 'paused'
            ? 'Paused — no proactive generation is scheduled. Your rhythm is preserved.'
            : 'No next generation is currently scheduled.';
      }
      if (els.pause) {
        const paused = state.state === 'paused';
        els.pause.textContent = paused ? 'Resume proactive routine' : 'Pause proactive routine';
        els.pause.dataset.action = paused ? 'resume' : 'pause';
      }
      const memoryLink = doc.getElementById('personalAssistantTodayMemory');
      if (els.memory && memoryLink && memoryLink.getAttribute('href')) {
        els.memory.setAttribute('href', memoryLink.getAttribute('href'));
      }
    }

    // renderSuggestion is the post-hire workspace recommendation, rendered
    // above the capability cards because it is the one thing on this surface
    // the user has not done yet. It is server-owned: absent for a generic
    // relationship, and gone once the workspace exists.
    function renderSuggestion(projection) {
      const suggestion = projection && projection.suggestion;
      if (!suggestion || !suggestion.title) return;
      const route = String(suggestion.action_route || '');
      const article = doc.createElement('article');
      article.className = 'pa-capability pa-capability--suggested';
      article.dataset.role = 'capability-suggestion';
      const heading = doc.createElement('h4');
      heading.textContent = suggestion.title;
      article.appendChild(heading);
      if (suggestion.body) {
        const body = doc.createElement('p');
        body.textContent = suggestion.body;
        article.appendChild(body);
      }
      if (route.startsWith('/') && !route.startsWith('//')) {
        const link = doc.createElement('a');
        link.href = route;
        link.textContent = suggestion.action_label || 'Create the workspace';
        article.appendChild(link);
      }
      els.capabilities.appendChild(article);
    }

    function renderCapabilities(projection) {
      if (!els.capabilities) return;
      els.capabilities.replaceChildren();
      renderSuggestion(projection);
      const cards = projection && Array.isArray(projection.cards) ? projection.cards : [];
      if (!cards.length) {
        const empty = doc.createElement('p');
        empty.textContent = 'Optional source status is not available yet.';
        els.capabilities.appendChild(empty);
        return;
      }
      cards.forEach(card => {
        const article = doc.createElement('article');
        article.className = 'pa-capability';
        article.dataset.status = card.status || 'unavailable';
        const heading = doc.createElement('h4');
        heading.textContent = `${card.label || card.key} — ${String(card.status || 'unavailable').replaceAll('_', ' ')}`;
        article.appendChild(heading);
        capabilityCopy(card).forEach(line => {
          const copy = doc.createElement('p');
          copy.textContent = line;
          article.appendChild(copy);
        });
        if (
          card.action_route &&
          String(card.action_route).startsWith('/') &&
          !String(card.action_route).startsWith('//')
        ) {
          const link = doc.createElement('a');
          link.href = card.action_route;
          link.textContent = card.action_label || 'Review setup';
          article.appendChild(link);
        }
        els.capabilities.appendChild(article);
      });
    }

    async function load() {
      announce('Loading current working agreement…');
      const [statePayload, capabilitiesPayload] = await Promise.all([
        fetchImpl(API_ROOT, { headers: { Accept: 'application/json' } }).then(responseJSON),
        fetchImpl(`${API_ROOT}/capabilities`, { headers: { Accept: 'application/json' } })
          .then(responseJSON)
          .catch(() => ({}))
      ]);
      renderState(unwrap(statePayload, 'personal_assistant'));
      renderCapabilities(unwrap(capabilitiesPayload, 'capabilities'));
      announce('Current values loaded. Changes affect future briefs only.');
      return state;
    }

    async function mutate(path, body, method) {
      const response = await fetchImpl(`${API_ROOT}${path}`, {
        method: method || 'POST',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: JSON.stringify(body)
      });
      return responseJSON(response);
    }

    form.addEventListener('submit', async event => {
      event.preventDefault();
      if (busy || !state) return;
      setBusy(true);
      announce('Saving working agreement…');
      const originalName = state.display_name || '';
      const requestedName = els.name.value.trim();
      const scopeChoice = els.scope.value;
      const agreement = {
        mandate: els.mandate.value,
        focus_areas: splitList(els.focus.value),
        timezone: els.timezone.value.trim(),
        schedule_days: splitList(els.days.value).map(day => day.toLowerCase()),
        schedule_time: els.time.value,
        schedule_enabled: els.enabled.checked,
        scope: scopeChoice === 'all' ? 'all' : 'selected',
        selected_workspace_ids:
          scopeChoice === 'personal_hq'
            ? [state.hq_workspace_id]
            : scopeChoice === 'selected'
              ? state.daily_brief.selected_workspace_ids || []
              : [],
        include_future_workspaces: els.future.checked,
        notify_on_ready: els.notify.checked
      };
      let phase = requestedName && requestedName !== originalName ? 'rename' : 'agreement';
      try {
        if (phase === 'rename') {
          const renamePayload = await mutate('/rename', {
            if_version: state.state_version,
            name: requestedName
          });
          renderState(unwrap(renamePayload, 'personal_assistant'));
        }
        phase = 'agreement';
        const payload = await mutate(
          '/working-agreement',
          {
            ...agreement,
            if_version: state.state_version,
            if_config_revision: state.daily_brief && state.daily_brief.config_revision
          },
          'PATCH'
        );
        renderState(unwrap(payload, 'personal_assistant'));
        announce(
          'Working agreement saved. Previous briefs and records were not changed.',
          'success'
        );
        global.dispatchEvent(
          new global.CustomEvent('personal-assistant:status', {
            detail: { personalAssistant: state }
          })
        );
      } catch (error) {
        if (error.status === 409) {
          const view = conflictView(error.payload);
          if (view.current) renderState(view.current);
          if (phase === 'rename') els.name.value = requestedName;
          announce(
            `${view.message} Your unsaved edit was not applied; review and reapply it.`,
            'warning'
          );
        } else if (phase === 'rename') {
          await load().catch(() => null);
          if (state && state.display_name !== requestedName) els.name.value = requestedName;
          announce(
            'The rename did not finish cleanly. Current records were preserved; review the name and save again to retry.',
            'warning'
          );
        } else {
          announce(
            requestedName !== originalName
              ? 'The rename finished, but the working-agreement edit did not. Review the current values and reapply the edit.'
              : error.message || 'Could not save. Nothing else was changed.',
            'error'
          );
        }
      } finally {
        setBusy(false);
      }
    });

    if (els.pause) {
      els.pause.addEventListener('click', async () => {
        if (busy || !state) return;
        const action = els.pause.dataset.action === 'resume' ? 'resume' : 'pause';
        setBusy(true);
        announce(
          action === 'pause' ? 'Pausing proactive generation…' : 'Resuming proactive generation…'
        );
        try {
          const payload = await mutate(`/${action}`, { if_version: state.state_version });
          renderState(unwrap(payload, 'personal_assistant'));
          announce(
            action === 'pause'
              ? 'Paused. Your schedule, history, memory, connections, and permissions were preserved.'
              : 'Resumed with the preserved Daily Brief rhythm.',
            'success'
          );
          global.dispatchEvent(
            new global.CustomEvent('personal-assistant:status', {
              detail: { personalAssistant: state }
            })
          );
        } catch (error) {
          announce(error.message || 'The routine state did not change.', 'error');
        } finally {
          setBusy(false);
        }
      });
    }

    function open(trigger) {
      lastTrigger = trigger || doc.activeElement;
      const assistantPanel = global.PersonalAssistantPanel;
      returnToAssistant = Boolean(
        trigger?.closest?.('#personalAssistantPanel') && assistantPanel?._state?.open
      );
      returnAssistantView = assistantPanel?._state?.activeView || 'today';
      if (returnToAssistant && typeof assistantPanel.close === 'function') {
        assistantPanel.close({ restoreFocus: false });
      }
      panel.hidden = false;
      load().catch(error =>
        announce(error.message || 'Working agreement is unavailable.', 'error')
      );
      if (typeof panel.scrollTo === 'function') panel.scrollTo({ top: 0 });
      if (els.close && typeof els.close.focus === 'function') els.close.focus();
    }

    function close() {
      panel.hidden = true;
      const trigger = lastTrigger;
      lastTrigger = null;
      if (returnToAssistant && global.PersonalAssistantPanel?.open) {
        returnToAssistant = false;
        const launcher = doc.getElementById('personalAssistantLauncher');
        global.PersonalAssistantPanel.open(launcher, {
          view: returnAssistantView,
          focusTab: true
        });
        return;
      }
      returnToAssistant = false;
      if (trigger && doc.contains(trigger) && typeof trigger.focus === 'function') {
        trigger.focus();
        return;
      }
      const launcher = doc.getElementById('personalAssistantLauncher');
      if (launcher && !launcher.hidden && typeof launcher.focus === 'function') launcher.focus();
    }

    if (els.close) els.close.addEventListener('click', close);
    doc.addEventListener('click', event => {
      const link =
        event.target &&
        event.target.closest &&
        event.target.closest('a[href*="personal-assistant=working-agreement"]');
      if (!link) return;
      event.preventDefault();
      open(link);
    });
    try {
      if (
        new URL(global.location.href).searchParams.get('personal-assistant') === 'working-agreement'
      )
        open();
    } catch (_) {
      // Ignore malformed test/location shims.
    }

    doc.addEventListener('keydown', event => {
      if (event.key !== 'Escape' || panel.hidden || doc.querySelector?.('.modal.show')) return;
      close();
    });

    return { open, close, load, renderState, renderCapabilities };
  }

  const api = { mount, splitList, conflictView, capabilityCopy };
  global.PersonalAssistantContinuity = api;
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  if (global.document) {
    if (global.document.readyState === 'loading') {
      global.document.addEventListener('DOMContentLoaded', () => mount());
    } else {
      mount();
    }
  }
})(typeof window !== 'undefined' ? window : globalThis);
