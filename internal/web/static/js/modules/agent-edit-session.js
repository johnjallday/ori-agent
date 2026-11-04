// Page-scoped edit state for the full agent page (/agents/{name}).
//
// Each editable section (profile, configuration, prompt) keeps its own saved
// baseline, the definition version that baseline came from, an optional draft,
// and at most one submitted snapshot. A save sends only the section's changed
// fields plus the version it was based on; the answer is classified by the
// server's structured codes, never by its wording. Nothing here touches the
// DOM, so every rule is testable in Node.
(function () {
  'use strict';

  const SECTIONS = ['profile', 'configuration', 'prompt'];

  // Fields that must travel together: the server validates reasoning against
  // the provider/model pair, so a change to any of them sends all of them.
  const COUPLED = { configuration: [['model', 'llm_provider', 'reasoning_effort']] };

  const text = value => String(value ?? '');
  const trimmed = value => text(value).trim();
  const numberOrNull = value => {
    if (value === null || value === undefined || value === '') return null;
    const n = Number(value);
    return Number.isFinite(n) ? n : null;
  };
  const tags = value => (Array.isArray(value) ? value.map(trimmed).filter(Boolean) : []);

  // Canonical section values from a detail response or an update receipt.
  function valuesFromAgent(section, agent) {
    const a = agent || {};
    const metadata = a.metadata || {};
    switch (section) {
      case 'profile':
        return {
          name: trimmed(a.name),
          description: trimmed(metadata.description),
          tags: tags(metadata.tags),
          favorite: Boolean(metadata.favorite)
        };
      case 'configuration':
        return {
          role: trimmed(a.role) || 'general',
          model: trimmed(a.model),
          llm_provider: trimmed(a.provider ?? a.llm_provider),
          temperature: numberOrNull(a.temperature),
          max_output_tokens: numberOrNull(a.max_output_tokens) || 0,
          reasoning_effort: trimmed(a.reasoning_effort)
        };
      case 'prompt':
        return { system_prompt: trimmed(a.system_prompt) };
      default:
        throw new Error(`Unknown section ${section}`);
    }
  }

  // Normalizes a draft the same way, so an untouched field compares equal.
  // A number field left blank (null) means "leave the saved value alone".
  function normalizeDraft(section, baseline, draft) {
    const next = { ...baseline };
    for (const [key, value] of Object.entries(draft || {})) {
      if (!(key in baseline)) continue;
      if (key === 'tags') next.tags = tags(value);
      else if (key === 'favorite') next.favorite = Boolean(value);
      else if (key === 'temperature' || key === 'max_output_tokens') {
        const n = numberOrNull(value);
        next[key] = n === null ? baseline[key] : n;
      } else next[key] = trimmed(value);
    }
    if (section === 'configuration' && !next.role) next.role = 'general';
    return next;
  }

  function same(a, b) {
    return JSON.stringify(a) === JSON.stringify(b);
  }

  function changedFields(section, baseline, draft) {
    const next = normalizeDraft(section, baseline, draft);
    const changed = Object.keys(baseline).filter(key => !same(baseline[key], next[key]));
    for (const group of COUPLED[section] || []) {
      if (group.some(key => changed.includes(key))) {
        for (const key of group) if (!changed.includes(key)) changed.push(key);
      }
    }
    return changed;
  }

  // Only the intended section's changed fields, plus the version they were
  // based on. A stale untouched field is never sent just because it shares a
  // form with a changed one.
  function buildPatch(section, baseline, version, draft) {
    if (!baseline) throw new Error('This section has not loaded yet.');
    if (!trimmed(version)) throw new Error('The saved version is unknown. Reload before saving.');
    const next = normalizeDraft(section, baseline, draft);
    const payload = {};
    for (const key of changedFields(section, baseline, draft)) payload[key] = next[key];
    if (section === 'profile' && 'name' in payload && !payload.name) {
      throw new Error('Name is required.');
    }
    payload.expected_version = version;
    return payload;
  }

  const CODE_KINDS = {
    stale_agent_edit: 'stale',
    agent_changed_on_disk: 'changed_on_disk',
    shared_agent_edit_requires_confirmation: 'needs_confirmation',
    agent_rename_failed: 'rename_failed',
    agent_name_taken: 'name_taken',
    attached_agent_rename_blocked: 'protected',
    personal_assistant_profile_protected: 'protected',
    workspace_owned_agent: 'read_only',
    agent_unreadable: 'read_only',
    agent_root_unavailable: 'unavailable'
  };

  // classify turns an HTTP answer into what the editor must do. A missing or
  // malformed success body is "unconfirmed": the write may have landed.
  function classify(status, body, { json = true } = {}) {
    const data = body && typeof body === 'object' ? body : {};
    const message = trimmed(data.message || data.error);
    const code = trimmed(data.code) || trimmed(data.error);
    if (status >= 200 && status < 300) {
      if (!json || data.success === false || !trimmed(data.version)) {
        return { kind: 'unconfirmed', reason: 'malformed', status, message };
      }
      return { kind: 'saved', status, receipt: data, version: data.version, message };
    }
    const kind = CODE_KINDS[code];
    if (kind) return { kind, code, status, message, body: data };
    if (status === 400) return { kind: 'invalid', status, message, body: data };
    if (status === 404) return { kind: 'missing', status, message, body: data };
    if (status >= 500 && !json) return { kind: 'unconfirmed', reason: 'server', status, message };
    return { kind: 'error', status, message, body: data };
  }

  async function send(fetchImpl, url, payload) {
    let response;
    try {
      response = await fetchImpl(url, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });
    } catch (error) {
      return { kind: 'unconfirmed', reason: 'network', message: error?.message || '' };
    }
    let body = null;
    let json = true;
    try {
      body = await response.json();
    } catch {
      json = false;
    }
    return classify(response.status, body, { json });
  }

  // submitPatch sends one reviewed patch. A shared-definition confirmation
  // retries with exactly the same fields and version; declining sends nothing
  // more, and a concurrent save during the dialog still comes back stale.
  async function submitPatch({ fetchImpl, name, payload, confirmShared }) {
    const url = `/api/agents/${encodeURIComponent(name)}`;
    const first = await send(fetchImpl, url, payload);
    if (first.kind !== 'needs_confirmation') return first;
    const proceed = await confirmShared(first.body || {});
    if (!proceed) return { kind: 'declined', status: first.status, body: first.body };
    return send(fetchImpl, url, { ...payload, confirm_shared_edit: true });
  }

  // createSession holds the page's per-section state. Saving one section
  // never resets another; overlapping definition writes are refused here so a
  // page-local double click can never race two PATCHes.
  function createSession(agent) {
    const sections = {};
    let pendingSection = null;
    const blank = (section, source) => ({
      baseline: valuesFromAgent(section, source),
      version: trimmed(source?.version),
      draft: null,
      submitted: null,
      state: 'clean',
      latest: null
    });
    for (const section of SECTIONS) sections[section] = blank(section, agent);

    const get = section => {
      const s = sections[section];
      if (!s) throw new Error(`Unknown section ${section}`);
      return s;
    };
    const isDirty = section => {
      const s = get(section);
      return Boolean(s.draft) && changedFields(section, s.baseline, s.draft).length > 0;
    };

    return {
      sections: SECTIONS.slice(),
      state: section => get(section).state,
      baseline: section => ({ ...get(section).baseline }),
      version: section => get(section).version,
      draft: section => (get(section).draft ? { ...get(section).draft } : null),
      latest: section => (get(section).latest ? { ...get(section).latest } : null),
      isDirty,
      anyDirty: () => SECTIONS.some(isDirty),
      pending: () => pendingSection,
      changedFields: section => {
        const s = get(section);
        return s.draft ? changedFields(section, s.baseline, s.draft) : [];
      },
      setDraft(section, values) {
        const s = get(section);
        s.draft = { ...(s.draft || s.baseline), ...values };
        if (s.state === 'clean' || s.state === 'saved')
          s.state = isDirty(section) ? 'dirty' : 'clean';
      },
      discard(section) {
        const s = get(section);
        if (pendingSection === section) throw new Error('A save is in progress.');
        s.draft = null;
        s.latest = null;
        s.state = 'clean';
      },
      // beginSubmit returns the request for one section, or throws when the
      // page already has a definition write in flight or nothing changed.
      beginSubmit(section) {
        const s = get(section);
        if (pendingSection) throw new Error('Another change is still saving.');
        if (s.state === 'unconfirmed') {
          throw new Error('The last save was not confirmed. Check the current values first.');
        }
        if (!isDirty(section)) throw new Error('There are no changes to save.');
        const payload = buildPatch(section, s.baseline, s.version, s.draft);
        s.submitted = { ...s.draft };
        s.state = 'pending';
        pendingSection = section;
        return payload;
      },
      // finishSubmit records the outcome. The draft survives everything except
      // a confirmed save of exactly what was submitted.
      finishSubmit(section, outcome) {
        const s = get(section);
        if (pendingSection === section) pendingSection = null;
        const submitted = s.submitted;
        s.submitted = null;
        switch (outcome.kind) {
          case 'saved': {
            const receipt = outcome.receipt?.agent || {};
            const draft = s.draft;
            s.baseline = valuesFromAgent(section, { ...receipt, version: outcome.version });
            s.version = outcome.version;
            s.latest = null;
            // Input typed after Save was pressed is newer than the receipt.
            s.draft = draft && submitted && !same(draft, submitted) ? draft : null;
            s.state = s.draft && isDirty(section) ? 'dirty' : 'saved';
            return;
          }
          case 'rename_failed': {
            const receipt = outcome.body?.agent || {};
            s.baseline = valuesFromAgent(section, receipt);
            s.version = trimmed(outcome.body?.version) || s.version;
            s.state = 'error';
            return;
          }
          case 'unconfirmed':
            s.submitted = submitted;
            s.state = 'unconfirmed';
            return;
          case 'stale':
          case 'changed_on_disk':
            // Not yet a conflict: reconciling with the latest confirmed values
            // (observe) decides whether the edited fields actually moved.
            s.state = 'stale';
            return;
          case 'declined':
            s.state = isDirty(section) ? 'dirty' : 'clean';
            return;
          default:
            s.state = 'error';
        }
      },
      submitted: section => (get(section).submitted ? { ...get(section).submitted } : null),
      // observe reconciles every section (except one with a write in flight)
      // with confirmed values: a receipt or an authoritative read. Returns, per
      // section, what the page must repaint.
      //
      // Three-way rule: a section's version advances only when the fields it
      // edits (and their coupled fields) are unchanged between its baseline and
      // the confirmed values. Overlapping changes become a conflict that keeps
      // the draft; nothing is merged silently.
      observe(agentValues, version) {
        const out = {};
        const v = trimmed(version ?? agentValues?.version);
        // A section waiting on reconciliation whose fields turn out unaffected
        // is an ordinary draft again.
        const settle = s => {
          if (s.state === 'stale' || s.state === 'conflict') {
            s.latest = null;
            s.state = s.draft ? 'dirty' : 'clean';
          }
        };
        for (const section of SECTIONS) {
          if (section === pendingSection) continue;
          const s = get(section);
          const values = valuesFromAgent(section, agentValues);
          if (same(values, s.baseline)) {
            if (v) s.version = v;
            settle(s);
            out[section] = 'unchanged';
            continue;
          }
          if (!isDirty(section)) {
            s.baseline = values;
            if (v) s.version = v;
            s.draft = null;
            s.latest = null;
            if (s.state !== 'unconfirmed') s.state = 'clean';
            out[section] = 'refreshed';
            continue;
          }
          const edited = changedFields(section, s.baseline, s.draft);
          const moved = Object.keys(values).filter(key => !same(values[key], s.baseline[key]));
          if (moved.some(key => edited.includes(key))) {
            s.latest = values;
            s.state = 'conflict';
            out[section] = 'conflict';
            continue;
          }
          for (const key of moved) s.draft[key] = values[key];
          s.baseline = values;
          if (v) s.version = v;
          settle(s);
          out[section] = 'rebased';
        }
        return out;
      }
    };
  }

  window.AgentEditSession = {
    SECTIONS,
    valuesFromAgent,
    changedFields,
    buildPatch,
    classify,
    submitPatch,
    createSession
  };
})();
