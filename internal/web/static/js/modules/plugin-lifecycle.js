/**
 * Plugin lifecycle — the shared half of install / enable / update.
 *
 * The Plugins page and the Create Workspace wizard both put a user through the
 * same sequence: preview what a plugin will register, show that disclosure in
 * full, wait for an explicit confirmation, apply it, and report what actually
 * happened. Only the surrounding DOM differs.
 *
 * This module owns the parts that must not differ — request and error parsing,
 * the trust disclosure, and the confirm/cancel state machine — and owns no
 * page-specific markup at all. Callers supply the element the disclosure goes
 * into and the functions that talk to whichever endpoint they use.
 *
 * The state machine matters more than it looks. Two properties depend on it:
 * a confirmation can never be applied twice, and a confirmation that was
 * pending when the user navigated away can never be applied at all.
 */

const PL_STATES = {
  IDLE: 'idle',
  PREVIEWING: 'previewing',
  AWAITING_CONFIRMATION: 'awaiting_confirmation',
  APPLYING: 'applying',
  DONE: 'done',
  FAILED: 'failed',
  CANCELLED: 'cancelled'
};

/**
 * request performs one lifecycle call and returns a structured result rather
 * than throwing.
 *
 * Every caller needs the same three things — did it work, what came back, and
 * what to tell the user — and a rejected promise makes each of them re-derive
 * the third from an exception message. A non-JSON body (a proxy error page, a
 * truncated response) is reported as a failure with usable text instead of
 * surfacing a parse error the user cannot act on.
 */
async function plRequest(method, url, body) {
  let response;
  try {
    response = await fetch(url, {
      method,
      headers: { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body)
    });
  } catch (error) {
    return {
      ok: false,
      status: 0,
      data: {},
      error: 'Ori could not reach the server. Check your connection and try again.',
      offline: true,
      cause: error
    };
  }

  const text = await response.text().catch(() => '');
  let data = {};
  if (text) {
    try {
      data = JSON.parse(text);
    } catch (_) {
      data = {};
    }
  }
  if (!response.ok) {
    return {
      ok: false,
      status: response.status,
      data,
      error: plErrorText(data, response.status)
    };
  }
  return { ok: true, status: response.status, data, error: '' };
}

function plErrorText(data, status) {
  const candidates = [data && data.error, data && data.message];
  for (const candidate of candidates) {
    if (typeof candidate === 'string' && candidate.trim()) return candidate.trim();
  }
  if (status === 409) return 'This changed while you were working. Review it and try again.';
  return `The request failed (${status}).`;
}

/**
 * trustModel normalizes a trust report into the four things it can disclose.
 *
 * The server's report uses Go field names; nothing downstream should have to
 * know that. Missing sections come back as empty arrays so a renderer can ask
 * about any of them without guarding first.
 */
function plTrustModel(report) {
  const list = value => (Array.isArray(value) ? value.filter(item => item != null) : []);
  const raw = report && typeof report === 'object' ? report : {};
  const field = (entry, ...names) => {
    for (const name of names) {
      if (entry && entry[name] != null) return String(entry[name]);
    }
    return '';
  };
  return {
    name: String(raw.Name || raw.name || ''),
    mcpCommands: list(raw.MCPCommands || raw.mcp_commands).map(String),
    skills: list(raw.Skills || raw.skills).map(String),
    // A workspace-surface plugin adds far more than MCP servers and skills:
    // long-lived background services, browser UI mounted inside Ori,
    // downloadable executables, and named permission scopes. Every one of
    // those is something the plugin gains and the user is agreeing to.
    services: list(raw.Services || raw.services).map(entry => ({
      id: field(entry, 'ID', 'id'),
      transport: field(entry, 'Transport', 'transport'),
      executable: field(entry, 'Executable', 'executable')
    })),
    surfaces: list(raw.Surfaces || raw.surfaces).map(entry => ({
      label: field(entry, 'Label', 'label') || field(entry, 'ID', 'id'),
      capability: field(entry, 'Capability', 'capability'),
      browserUI: Boolean(entry && (entry.BrowserUI ?? entry.browser_ui))
    })),
    capabilities: list(raw.SurfaceCapabilities || raw.surface_capabilities).map(String),
    // Scopes are permission grants. They are listed on their own, not folded
    // into a count, because "which permissions" is the question.
    scopes: list(raw.SymbolicScopes || raw.symbolic_scopes).map(String),
    artifacts: list(raw.Artifacts || raw.artifacts).map(entry => ({
      id: field(entry, 'ID', 'id'),
      platform: field(entry, 'Platform', 'platform'),
      sha256: field(entry, 'SHA256', 'sha256')
    })),
    blueprints: list(raw.Blueprints || raw.blueprints).map(String),
    unsupported: list(raw.Unsupported || raw.unsupported).map(entry => ({
      kind: field(entry, 'kind', 'Kind'),
      detail: field(entry, 'detail', 'Detail')
    })),
    warnings: list(raw.Warnings || raw.warnings).map(String)
  };
}

function plIsEmptyTrust(model) {
  return (
    model.mcpCommands.length === 0 &&
    model.skills.length === 0 &&
    model.services.length === 0 &&
    model.surfaces.length === 0 &&
    model.capabilities.length === 0 &&
    model.scopes.length === 0 &&
    model.artifacts.length === 0 &&
    model.blueprints.length === 0 &&
    model.unsupported.length === 0 &&
    model.warnings.length === 0
  );
}

function plCreate(tag, className) {
  const el = document.createElement(tag);
  if (className) el.className = className;
  return el;
}

function plSection(title, className) {
  const wrapper = plCreate('div', className);
  const heading = plCreate('div', 'plugin-trust-section-title');
  heading.textContent = title;
  wrapper.appendChild(heading);
  return wrapper;
}

/**
 * renderTrustReport builds the complete disclosure as DOM.
 *
 * Complete is the point. This is the last thing a user sees before a plugin
 * gains the ability to run commands on their machine, so nothing here is
 * summarized, truncated, or hidden behind a "show more": the MCP commands are
 * the actual command lines, and the warnings are shown even when they make the
 * plugin look worse.
 *
 * Text goes in as textContent. A plugin manifest is third-party input, and a
 * command line rendered as markup is a command line that can carry script.
 */
function plRenderTrustReport(report) {
  const model = plTrustModel(report);
  const root = plCreate('div', 'plugin-trust-report');

  if (plIsEmptyTrust(model)) {
    const empty = plCreate('p', 'plugin-trust-empty');
    empty.textContent = 'This plugin registers nothing.';
    root.appendChild(empty);
    return root;
  }

  if (model.mcpCommands.length > 0) {
    const section = plSection('Runs these commands on your computer', 'plugin-trust-section');
    const list = plCreate('ul', 'plugin-trust-list');
    model.mcpCommands.forEach(command => {
      const item = plCreate('li', '');
      const code = plCreate('code', '');
      code.textContent = command;
      item.appendChild(code);
      list.appendChild(item);
    });
    section.appendChild(list);
    root.appendChild(section);
  }

  if (model.services.length > 0) {
    const section = plSection('Runs these background services', 'plugin-trust-section');
    const list = plCreate('ul', 'plugin-trust-list');
    model.services.forEach(service => {
      const item = plCreate('li', '');
      const label = service.executable || service.id;
      item.textContent = service.transport ? `${label} (${service.transport})` : label;
      list.appendChild(item);
    });
    section.appendChild(list);
    root.appendChild(section);
  }

  if (model.artifacts.length > 0) {
    const section = plSection('Downloads and runs these files', 'plugin-trust-section');
    const list = plCreate('ul', 'plugin-trust-list');
    model.artifacts.forEach(artifact => {
      const item = plCreate('li', '');
      const parts = [artifact.id || 'artifact'];
      if (artifact.platform) parts.push(artifact.platform);
      // The digest is what makes the download verifiable, so it is shown
      // rather than summarized away.
      if (artifact.sha256) parts.push(`sha256 ${artifact.sha256}`);
      item.textContent = parts.join(' · ');
      list.appendChild(item);
    });
    section.appendChild(list);
    root.appendChild(section);
  }

  if (model.scopes.length > 0) {
    // Permission grants, listed individually. Folding these into a count would
    // be the single most misleading thing this panel could do.
    const section = plSection('Grants these permissions', 'plugin-trust-section is-warning');
    const list = plCreate('ul', 'plugin-trust-list');
    model.scopes.forEach(scope => {
      const item = plCreate('li', '');
      item.textContent = scope;
      list.appendChild(item);
    });
    section.appendChild(list);
    root.appendChild(section);
  }

  if (model.surfaces.length > 0) {
    const section = plSection('Adds these screens inside Ori', 'plugin-trust-section');
    const list = plCreate('ul', 'plugin-trust-list');
    model.surfaces.forEach(surface => {
      const item = plCreate('li', '');
      item.textContent = surface.browserUI
        ? `${surface.label} (runs its own web page inside Ori)`
        : surface.label;
      list.appendChild(item);
    });
    section.appendChild(list);
    root.appendChild(section);
  }

  if (model.capabilities.length > 0) {
    const section = plSection('Adds these workspace capabilities', 'plugin-trust-section');
    const value = plCreate('p', 'plugin-trust-value');
    value.textContent = model.capabilities.join(', ');
    section.appendChild(value);
    root.appendChild(section);
  }

  if (model.blueprints.length > 0) {
    const section = plSection('Adds these blueprints', 'plugin-trust-section');
    const value = plCreate('p', 'plugin-trust-value');
    value.textContent = model.blueprints.join(', ');
    section.appendChild(value);
    root.appendChild(section);
  }

  if (model.skills.length > 0) {
    const section = plSection('Adds these skills', 'plugin-trust-section');
    const value = plCreate('p', 'plugin-trust-value');
    value.textContent = model.skills.join(', ');
    section.appendChild(value);
    root.appendChild(section);
  }

  if (model.unsupported.length > 0) {
    const section = plSection('Skipped — not supported yet', 'plugin-trust-section');
    const list = plCreate('ul', 'plugin-trust-list');
    model.unsupported.forEach(entry => {
      const item = plCreate('li', '');
      item.textContent = entry.detail ? `${entry.kind}: ${entry.detail}` : entry.kind;
      list.appendChild(item);
    });
    section.appendChild(list);
    root.appendChild(section);
  }

  if (model.warnings.length > 0) {
    const section = plSection('Warnings', 'plugin-trust-section is-warning');
    const list = plCreate('ul', 'plugin-trust-list');
    model.warnings.forEach(warning => {
      const item = plCreate('li', 'plugin-trust-warning');
      item.textContent = warning;
      list.appendChild(item);
    });
    section.appendChild(list);
    root.appendChild(section);
  }

  return root;
}

/**
 * createFlow builds the confirm-gated state machine for one lifecycle action.
 *
 * options.preview() and options.apply() are the caller's two calls; both
 * return a plRequest-shaped result. onState(state, payload) is invoked on
 * every transition so a surface can re-render without tracking state itself.
 *
 * The invariants this exists to hold:
 *
 *   - apply() runs at most once per confirmation. A double-click, a repeated
 *     Enter, or an impatient second press cannot install a plugin twice.
 *   - apply() never runs without preview() having returned first, so the user
 *     has always been shown what they are agreeing to.
 *   - cancel() and invalidate() make a pending confirmation unusable. The
 *     difference is intent: cancel is the user's, invalidate is the
 *     surface's (the modal closed, the selection changed), and an invalidated
 *     flow reports nothing back because nobody is there to read it.
 */
function plCreateFlow(options) {
  const settings = options || {};
  let state = PL_STATES.IDLE;
  let previewData = null;
  let generation = 0;

  const setState = (next, payload) => {
    state = next;
    if (typeof settings.onState === 'function') settings.onState(next, payload || null);
  };

  const flow = {
    get state() {
      return state;
    },
    get preview() {
      return previewData;
    },

    async start() {
      if (state === PL_STATES.PREVIEWING || state === PL_STATES.APPLYING) return null;
      const token = ++generation;
      setState(PL_STATES.PREVIEWING);
      const result = await settings.preview();
      // A response that arrived after the flow was cancelled, restarted, or
      // invalidated belongs to a question nobody is asking any more.
      if (token !== generation) return null;
      if (!result || !result.ok) {
        setState(PL_STATES.FAILED, result || null);
        return result || null;
      }
      previewData = result.data || {};
      setState(PL_STATES.AWAITING_CONFIRMATION, previewData);
      return result;
    },

    async confirm() {
      if (state !== PL_STATES.AWAITING_CONFIRMATION) return null;
      const token = ++generation;
      setState(PL_STATES.APPLYING);
      const result = await settings.apply(previewData);
      if (token !== generation) return null;
      if (!result || !result.ok) {
        setState(PL_STATES.FAILED, result || null);
        return result || null;
      }
      previewData = null;
      setState(PL_STATES.DONE, result.data || {});
      return result;
    },

    cancel() {
      generation++;
      previewData = null;
      setState(PL_STATES.CANCELLED);
    },

    // invalidate drops a pending confirmation without announcing anything.
    // Used when the surface itself goes away: the user did not decide, so
    // there is no decision to report.
    invalidate() {
      generation++;
      previewData = null;
      state = PL_STATES.IDLE;
    }
  };
  return flow;
}

// The lifecycle-position phrases every surface reports, lower-case so a
// caller can drop them into a sentence or capitalize them at the start of
// one. The Plugins page and the wizard's readiness/recovery panels all read
// from here, so "installed but disabled" cannot drift into two different
// wordings on two different screens — a real risk this shared module exists
// to remove, not just an inconvenience if it happened.
//
// Deliberately narrow: these three describe where the plugin's OWN lifecycle
// stands (on the machine, switched on or not). None of them claims anything
// about the live external application or service the plugin talks to —
// installing and enabling are prerequisites this build satisfied, not a
// verification that anything downstream works.
const PL_LIFECYCLE_LABELS = {
  NOT_INSTALLED: 'not installed',
  DISABLED: 'installed, still disabled',
  ENABLED: 'installed and enabled'
};

function plCapitalize(text) {
  return text ? text.charAt(0).toUpperCase() + text.slice(1) : text;
}

window.PluginLifecycle = {
  STATES: PL_STATES,
  LIFECYCLE_LABELS: PL_LIFECYCLE_LABELS,
  capitalize: plCapitalize,
  request: plRequest,
  errorText: plErrorText,
  trustModel: plTrustModel,
  isEmptyTrust: plIsEmptyTrust,
  renderTrustReport: plRenderTrustReport,
  createFlow: plCreateFlow
};
