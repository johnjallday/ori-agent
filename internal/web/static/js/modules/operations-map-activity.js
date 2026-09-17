// The Operations map's share of the task-run show (tasks/prd-task-run-show.md §4.5).
//
// A workspace page already receives its own task events through
// window.workspaceRealtime, so this layer opens no stream of its own (FR30). It
// turns those events into a working pose and a speech bubble on the unit that
// is doing the work, pulses the belt button whose window shows what a tool
// touched, and draws finished runs as parcels at the unit's feet.
//
// Everything it paints is patched onto markup the Command view rebuilds on
// every render, so its state lives here and `paint()` puts it back.

// A line stays at least this long; the newest waiting line replaces older ones
// (FR18, FR33). The same numbers as the Home map.
export const DWELL_MS = 2500;
export const FINISH_MS = 2000;
const PULSE_MS = 1200;

const ACTIVITY_URL = '/api/workspace-map/activity';

// The belt shows windows, not tools. A tool call pulses the one window that
// shows what the tool worked on (FR31); any other tool pulses nothing.
export const BELT_WINDOW_FOR_TOOL = Object.freeze({
  workspace_tasks: 'objectives',
  task_runs: 'objectives',
  current_task: 'objectives',
  delegate_task: 'objectives',
  workspace_notes: 'inventory',
  workspace_save_note: 'inventory',
  notes_write: 'inventory',
  workspace_sessions: 'inventory',
  workspace_session_detail: 'inventory',
  workspace_directories: 'inventory',
  workspace_files: 'inventory'
});

function text(value) {
  return typeof value === 'string' ? value.trim() : '';
}

function agentKey(name) {
  return text(name).toLowerCase();
}

/**
 * Read one workspaceRealtime task event into the activity shape the bubble
 * lines understand. Only allowlisted fields are read: the stream also carries
 * tool arguments and results, and none of that belongs on the map.
 */
export function activityFromRealtime(event) {
  const type = text(event && event.type);
  const envelope = event && event.data && typeof event.data === 'object' ? event.data : {};
  const data = envelope.data && typeof envelope.data === 'object' ? envelope.data : envelope;
  const taskId = text(data.task_id);
  if (!taskId) return null;
  const base = { kind: 'task', task_id: taskId, agent_name: text(data.agent) };
  switch (type) {
    case 'task.started':
      return { ...base, phase: 'started' };
    case 'task.resumed':
      return { ...base, phase: 'resumed' };
    case 'task.thinking':
      return { ...base, phase: 'step', step: { type: 'thinking' } };
    case 'task.tool_call':
      return {
        ...base,
        phase: 'step',
        step: { type: 'tool_call', tool_name: text(data.tool_name) }
      };
    case 'task.tool_result': {
      const step = { type: 'tool_result', tool_name: text(data.tool_name) };
      if (typeof data.success === 'boolean') step.ok = data.success;
      return { ...base, phase: 'step', step };
    }
    case 'task.blocked':
      return { ...base, phase: 'blocked' };
    case 'task.completed':
      return { ...base, phase: 'finished', outcome: 'succeeded' };
    case 'task.failed':
      return { ...base, phase: 'finished', outcome: 'failed' };
    case 'task.deleted':
      return { ...base, phase: 'removed' };
    default:
      return null;
  }
}

function defaultLineFor(activity) {
  const lines = typeof window !== 'undefined' ? window.OriActivityBubbleLines : null;
  return lines && typeof lines.lineFor === 'function' ? lines.lineFor(activity) : '';
}

function decodeName(value) {
  try {
    return decodeURIComponent(String(value || ''));
  } catch (_) {
    return String(value || '');
  }
}

function unitKeyOf(el) {
  return agentKey(decodeName(el.getAttribute('data-cmd-map-select-agent')));
}

// How far a working unit steps forward; matches the CSS pose.
const STEP_PX = 10;

// Where an element sits inside the map world, from layout offsets. Unlike a
// measured rect these ignore transforms, so a unit that is mid-step still
// reports where its card stands.
function layoutBox(el, world) {
  let left = 0;
  let top = 0;
  let node = el;
  while (node && node !== world) {
    left += Number(node.offsetLeft) || 0;
    top += Number(node.offsetTop) || 0;
    node = node.offsetParent;
  }
  return {
    left,
    top,
    width: Number(el.offsetWidth) || 0,
    height: Number(el.offsetHeight) || 0
  };
}

export class OperationsMapActivity {
  /**
   * @param {object} options
   * @param {() => Element|null} options.root - the `.ws-cmd-opmap` currently on screen
   * @param {() => string} options.workspaceId
   * @param {() => string} [options.workspaceName]
   * @param {(payload: object) => void} [options.onOpenResult]
   */
  constructor(options = {}) {
    this.rootFn = options.root || (() => null);
    this.workspaceIdFn = options.workspaceId || (() => '');
    this.workspaceNameFn = options.workspaceName || (() => '');
    this.onOpenResult = options.onOpenResult || null;
    this.fetchImpl =
      options.fetchImpl || (typeof fetch === 'function' ? (...args) => fetch(...args) : null);
    this.now = options.now || (() => Date.now());
    this.setTimer = options.setTimeout || ((fn, ms) => setTimeout(fn, ms));
    this.clearTimer = options.clearTimeout || (timer => clearTimeout(timer));
    this.lineFor = options.lineFor || defaultLineFor;
    this.document = options.document || (typeof document !== 'undefined' ? document : null);
    this.resultCards = options.resultCards || null;

    this.units = new Map(); // agent key -> what that unit is showing
    this.taskAgents = new Map(); // task id -> agent name, for events that omit it
    this.parcels = [];
    this.knownParcelIds = null; // null until the first load: nothing "lands" on arrival
    this.landingIds = new Set();
    this.parcelsDisabled = false;
    this.parcelLoad = null;
    this.parcelTimer = null;
    this.cardHost = null;
  }

  // ---------- events ----------

  /** Returns true when the event changed what the map shows. */
  handleRealtimeEvent(event) {
    const activity = activityFromRealtime(event);
    if (!activity) return false;
    if (activity.agent_name) this.taskAgents.set(activity.task_id, activity.agent_name);
    const agent = activity.agent_name || this.taskAgents.get(activity.task_id) || '';

    if (!agent) {
      // No unit can show this run, but its parcel still arrives.
      if (activity.phase === 'finished') this.scheduleParcelLoad();
      return false;
    }
    const key = agentKey(agent);

    if (activity.phase === 'removed') {
      const unit = this.units.get(key);
      this.taskAgents.delete(activity.task_id);
      if (unit && unit.taskId === activity.task_id) this.dropUnit(key);
      this.parcels = this.parcels.filter(
        parcel => !(parcel.kind === 'task' && parcel.ref_id === activity.task_id)
      );
      this.paint();
      return true;
    }

    if (activity.step && activity.step.type === 'tool_call') {
      this.pulseBelt(activity.step.tool_name);
    }

    const unit = this.ensureUnit(key, agent);
    const finishing = activity.phase === 'finished';
    if (!finishing) {
      // Work on this agent again cancels a finish that was still on screen.
      if (unit.finishTimer) {
        this.clearTimer(unit.finishTimer);
        unit.finishTimer = null;
      }
      unit.taskId = activity.task_id;
    }
    this.offerLine(key, {
      line: this.lineFor(activity) || '',
      blocked: activity.phase === 'blocked',
      finishing,
      taskId: activity.task_id
    });
    this.paint();
    return true;
  }

  ensureUnit(key, agent) {
    let unit = this.units.get(key);
    if (!unit) {
      unit = {
        agent,
        taskId: '',
        line: '',
        blocked: false,
        finishing: false,
        shownAt: 0,
        pending: null,
        timer: null,
        finishTimer: null,
        paintedSignature: ''
      };
      this.units.set(key, unit);
    }
    return unit;
  }

  dropUnit(key) {
    const unit = this.units.get(key);
    if (!unit) return;
    if (unit.timer) this.clearTimer(unit.timer);
    if (unit.finishTimer) this.clearTimer(unit.finishTimer);
    this.units.delete(key);
  }

  // Latest wins, but nothing replaces a line before it has been readable for
  // DWELL_MS (FR18). A step with nothing new to say changes nothing.
  offerLine(key, next) {
    const unit = this.units.get(key);
    if (!unit) return;
    if (!next.line && !next.finishing) return;
    const elapsed = this.now() - unit.shownAt;
    if (!unit.line || elapsed >= DWELL_MS) {
      this.showLine(key, next);
      return;
    }
    unit.pending = next;
    if (unit.timer) return;
    unit.timer = this.setTimer(() => {
      unit.timer = null;
      const pending = unit.pending;
      unit.pending = null;
      if (pending && this.units.get(key) === unit) {
        this.showLine(key, pending);
        this.paint();
      }
    }, DWELL_MS - elapsed);
  }

  showLine(key, next) {
    const unit = this.units.get(key);
    if (!unit) return;
    unit.line = next.line;
    unit.blocked = next.blocked;
    unit.finishing = next.finishing;
    if (next.taskId) unit.taskId = next.taskId;
    unit.shownAt = this.now();
    if (!next.finishing) return;
    // "Done." stays up for FINISH_MS, then the unit steps back and its parcel
    // has somewhere to land.
    if (unit.finishTimer) this.clearTimer(unit.finishTimer);
    unit.finishTimer = this.setTimer(() => {
      unit.finishTimer = null;
      // Newer work waiting to be shown keeps the unit forward.
      if (this.units.get(key) === unit && !unit.pending) {
        this.units.delete(key);
        this.paint();
      }
      void this.loadParcels();
    }, FINISH_MS);
  }

  // The parcel is written when the run finishes, but this page's stream and the
  // parcel store see that event independently. A unit loads it once it has
  // stepped back; a run no unit is showing loads it shortly after.
  scheduleParcelLoad() {
    if (this.parcelTimer) this.clearTimer(this.parcelTimer);
    this.parcelTimer = this.setTimer(() => {
      this.parcelTimer = null;
      void this.loadParcels();
    }, 1000);
  }

  // ---------- belt pulse ----------

  pulseBelt(toolName) {
    const windowKey = BELT_WINDOW_FOR_TOOL[text(toolName)];
    const root = this.rootFn();
    if (!windowKey || !root || typeof root.querySelector !== 'function') return false;
    const button = root.querySelector(
      '.ws-cmd-map-belt-btn[data-cmd-map-window="' + windowKey + '"]'
    );
    if (!button || !button.classList) return false;
    button.classList.remove('is-activity-pulse');
    // Restart the animation when the same button pulses twice in a row.
    void button.offsetWidth;
    button.classList.add('is-activity-pulse');
    this.setTimer(() => button.classList.remove('is-activity-pulse'), PULSE_MS);
    return true;
  }

  // ---------- parcels ----------

  async loadParcels() {
    if (this.parcelsDisabled || !this.fetchImpl) return;
    if (this.parcelLoad) return this.parcelLoad;
    const workspaceId = this.workspaceIdFn();
    if (!workspaceId) return;
    this.parcelLoad = (async () => {
      try {
        const response = await this.fetchImpl(ACTIVITY_URL, {
          headers: { Accept: 'application/json' }
        });
        if (response && response.status === 404) {
          // The show is switched off: stay quiet for the rest of the page.
          this.parcelsDisabled = true;
          return;
        }
        if (!response || !response.ok) return;
        const snapshot = await response.json();
        this.applySnapshot(workspaceId, snapshot || {});
        this.paint();
      } catch (error) {
        console.warn('operations-map-activity: could not load results', error);
      } finally {
        this.parcelLoad = null;
      }
    })();
    return this.parcelLoad;
  }

  applySnapshot(workspaceId, snapshot) {
    const firstLoad = this.knownParcelIds === null;
    const parcels = (Array.isArray(snapshot.parcels) ? snapshot.parcels : []).filter(
      parcel => parcel && parcel.id && parcel.workspace_id === workspaceId
    );
    const known = this.knownParcelIds || new Set();
    this.landingIds = new Set(
      firstLoad ? [] : parcels.filter(parcel => !known.has(parcel.id)).map(parcel => parcel.id)
    );
    this.knownParcelIds = new Set(parcels.map(parcel => parcel.id));
    this.parcels = parcels;

    // A page opened in the middle of a run shows that run too.
    if (!firstLoad) return;
    (Array.isArray(snapshot.running) ? snapshot.running : []).forEach(run => {
      if (!run || run.workspace_id !== workspaceId || run.kind !== 'task') return;
      const agent = text(run.agent_name);
      if (!agent) return;
      const key = agentKey(agent);
      if (this.units.has(key)) return;
      this.taskAgents.set(text(run.task_id), agent);
      const unit = this.ensureUnit(key, agent);
      unit.taskId = text(run.task_id);
      const phase = run.blocked ? 'blocked' : 'started';
      this.showLine(key, {
        line: this.lineFor({ kind: 'task', phase }) || '',
        blocked: !!run.blocked,
        finishing: false,
        taskId: unit.taskId
      });
    });
  }

  parcelsByAgent() {
    const groups = new Map();
    this.parcels.forEach(parcel => {
      const key = agentKey(parcel.agent_name);
      if (!groups.has(key)) groups.set(key, []);
      groups.get(key).push(parcel);
    });
    groups.forEach(list =>
      list.sort((a, b) => (Date.parse(b.produced_at) || 0) - (Date.parse(a.produced_at) || 0))
    );
    return groups;
  }

  // ---------- painting ----------

  unitElements(root) {
    if (!root || typeof root.querySelectorAll !== 'function') return [];
    return Array.from(root.querySelectorAll('.ws-cmd-map-agent[data-cmd-map-select-agent]'));
  }

  /** Put the pose, bubbles, and parcels back onto whatever the view rendered. */
  paint() {
    const root = this.rootFn();
    if (!root || typeof root.querySelector !== 'function') return;
    const world = root.querySelector('.ws-cmd-map-world');
    const elements = this.unitElements(root);
    elements.forEach(el => {
      const unit = this.units.get(unitKeyOf(el)) || null;
      el.classList.toggle('is-activity-working', !!unit);
      el.classList.toggle('is-activity-blocked', !!(unit && unit.blocked));
    });
    if (!world || typeof world.querySelectorAll !== 'function') return;
    this.paintBubbles(world, elements);
    this.paintParcels(world, elements);
  }

  // A unit card clips everything inside it, so its bubble lives in the map
  // world above the card instead (FR29). It is placed from layout offsets,
  // which the step-forward transition does not move, plus that step.
  paintBubbles(world, elements) {
    const shown = new Map();
    Array.from(world.querySelectorAll('[data-cmd-activity-bubble]')).forEach(bubble =>
      shown.set(bubble.getAttribute('data-cmd-activity-agent'), bubble)
    );
    elements.forEach(el => {
      const key = unitKeyOf(el);
      const unit = this.units.get(key);
      const existing = shown.get(key) || null;
      shown.delete(key);
      if (!unit || !unit.line || !this.document) {
        if (existing && existing.parentNode) existing.parentNode.removeChild(existing);
        return;
      }
      const signature = [unit.line, unit.blocked ? 'blocked' : '', unit.taskId].join('|');
      let bubble = existing;
      if (!existing || existing.getAttribute('data-cmd-activity-bubble') !== signature) {
        // A render rebuilt the map around a line the user is already reading; it
        // comes back still instead of popping in again.
        const settled = !existing && unit.paintedSignature === signature;
        unit.paintedSignature = signature;
        bubble = this.document.createElement('span');
        bubble.className =
          'ws-cmd-map-activity-bubble' +
          (unit.blocked ? ' is-blocked' : '') +
          (unit.finishing ? ' is-finishing' : '') +
          (settled ? ' is-settled' : '');
        bubble.setAttribute('data-cmd-activity-bubble', signature);
        bubble.setAttribute('data-cmd-activity-agent', key);
        // Decorative: the unit's own label and the Tasks window say the same.
        bubble.setAttribute('aria-hidden', 'true');
        if (unit.blocked && unit.taskId) {
          // The one bubble that is a way in: it opens the question (FR27).
          bubble.setAttribute('data-cmd-activity-open-task', unit.taskId);
          bubble.setAttribute('title', 'Open this task');
        }
        bubble.textContent = unit.line;
        if (existing && existing.parentNode) existing.parentNode.replaceChild(bubble, existing);
        else world.appendChild(bubble);
      }
      const box = layoutBox(el, world);
      bubble.style.left = box.left + box.width / 2 + 'px';
      bubble.style.top = box.top - STEP_PX + 'px';
    });
    // A unit that is no longer drawn takes its bubble with it.
    shown.forEach(bubble => {
      if (bubble.parentNode) bubble.parentNode.removeChild(bubble);
    });
  }

  // A parcel is a button of its own, so it cannot sit inside the unit's button.
  // It is placed over the unit's feet in the map world instead, or at the
  // command post when the agent that ran it is no longer on the map (FR32).
  paintParcels(world, elements) {
    Array.from(world.querySelectorAll('[data-cmd-map-parcel]')).forEach(el => {
      if (el.parentNode) el.parentNode.removeChild(el);
    });
    if (!this.parcels.length || !this.document) return;
    const byKey = new Map(elements.map(el => [unitKeyOf(el), el]));
    const post =
      world.querySelector('.ws-cmd-map-agent.is-command-node') ||
      world.querySelector('.ws-cmd-map-command-repair');

    // Parcels whose agent has left gather at the post with that post's own.
    const placed = new Map();
    this.parcelsByAgent().forEach((list, key) => {
      const anchor = byKey.get(key) || post;
      if (!anchor) return;
      const entry = placed.get(anchor) || [];
      placed.set(anchor, entry.concat(list));
    });

    placed.forEach((list, anchor) => {
      list.sort((a, b) => (Date.parse(b.produced_at) || 0) - (Date.parse(a.produced_at) || 0));
      const newest = list[0];
      const box = layoutBox(anchor, world);
      const button = this.document.createElement('button');
      button.type = 'button';
      button.className =
        'ws-cmd-map-parcel' +
        (newest.outcome === 'failed' || newest.outcome === 'timeout' ? ' is-attention' : '') +
        (list.some(parcel => this.landingIds.has(parcel.id)) ? ' is-landing' : '');
      button.setAttribute('data-cmd-map-parcel', newest.id);
      const who = text(newest.agent_name) || 'this workspace';
      button.setAttribute(
        'aria-label',
        list.length === 1
          ? 'Result ready from ' + who + ': ' + text(newest.title) + '. Open'
          : list.length + ' results ready. Open the newest, from ' + who
      );
      button.textContent = list.length > 1 ? '+' + (list.length > 9 ? '9' : list.length) : '+1';
      button.style.left = box.left + box.width / 2 + 'px';
      button.style.top = box.top + box.height - 6 + 'px';
      world.appendChild(button);
    });
    this.landingIds = new Set();
  }

  // ---------- the result card ----------

  ensureCardHost() {
    const doc = this.document;
    if (!doc || !doc.body) return null;
    if (!this.cardHost || this.cardHost.isConnected === false) {
      this.cardHost = doc.createElement('div');
      this.cardHost.className = 'ws-map-card-host';
      this.cardHost.setAttribute('data-cmd-map-card-host', '');
      doc.body.appendChild(this.cardHost);
    }
    const root = this.rootFn();
    const rect =
      root && typeof root.getBoundingClientRect === 'function'
        ? root.getBoundingClientRect()
        : null;
    if (rect && this.cardHost.style) {
      // The Operations map is often taller than the window; the card centres
      // in the part of it the user can actually see.
      const view = typeof window !== 'undefined' ? window : {};
      const top = Math.max(rect.top, 0);
      const bottom = Math.min(rect.bottom, Number(view.innerHeight) || rect.bottom);
      this.cardHost.style.left = rect.left + 'px';
      this.cardHost.style.top = top + 'px';
      this.cardHost.style.width = rect.width + 'px';
      this.cardHost.style.height = Math.max(bottom - top, 0) + 'px';
    }
    return this.cardHost;
  }

  /** Open a parcel into the shared result card (FR44). */
  openParcel(parcelId) {
    const cards = this.resultCards || (typeof window !== 'undefined' ? window.OriResultCard : null);
    const parcel = this.parcels.find(entry => entry.id === parcelId);
    if (!cards || typeof cards.open !== 'function' || !parcel) return null;
    const host = this.ensureCardHost();
    if (!host) return null;
    const root = this.rootFn();
    const origin =
      this.unitElements(root).find(
        el =>
          agentKey(decodeName(el.getAttribute('data-cmd-map-select-agent'))) ===
          agentKey(parcel.agent_name)
      ) || null;
    const opened = cards.open({
      host,
      parcelId: parcel.id,
      origin,
      workspaceName: this.workspaceNameFn(),
      onPrimary: payload => {
        if (this.onOpenResult) this.onOpenResult(payload);
      }
    });
    return Promise.resolve(opened).then(payload => {
      if (!payload) return null;
      // Opened once, gone from the map (FR39).
      this.parcels = this.parcels.filter(entry => entry.id !== parcel.id);
      this.paint();
      return payload;
    });
  }

  dispose() {
    this.units.forEach((_, key) => this.dropUnit(key));
    if (this.parcelTimer) this.clearTimer(this.parcelTimer);
    this.parcelTimer = null;
  }
}
