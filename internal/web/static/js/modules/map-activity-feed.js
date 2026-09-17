/*
 * map-activity-feed.js — the page's one connection to the task-run show's live
 * activity feed (tasks/prd-task-run-show.md FR3, FR7, FR65).
 *
 * Every map on a page shares ONE EventSource, however many workspaces are
 * running and however many times a map mounts: browsers allow only about six
 * connections per origin over HTTP/1.1, and the local server speaks HTTP/1.1.
 * The connection opens with the first subscriber and closes with the last.
 *
 * State is only ever what the server said. The stream's first event is a
 * `snapshot`, which REPLACES everything held here; `activity` events patch it.
 * A dropped connection clears what is running — nothing stays lit without a
 * live feed (FR15) — while unopened parcels keep their last known count (FR65).
 * A reconnect never replays missed events; it gets a fresh snapshot (FR7).
 *
 * A plain deferred script exposing window.OriMapActivityFeed, because
 * workspace-map.js is a classic script and cannot import a module.
 */
(function () {
  'use strict';

  var STREAM_URL = '/api/workspace-map/activity/stream';
  var SNAPSHOT_URL = '/api/workspace-map/activity';
  // Reconnect backoff: 1s, 2s, 5s, then every 15s (FR65).
  var BACKOFF_MS = [1000, 2000, 5000, 15000];

  var subscribers = [];
  var source = null;
  var sawSnapshot = false;
  var connected = false;
  // The server answered 404: the feature is off. Nothing reconnects until the
  // page reloads, so a disabled feature costs no background traffic (FR64).
  var disabled = false;
  var reconnectTimer = null;
  var attempt = 0;
  var running = Object.create(null);
  var parcels = Object.create(null);

  function warn(message, error) {
    if (typeof console !== 'undefined' && console && typeof console.warn === 'function') {
      console.warn('[map-activity-feed] ' + message, error || '');
    }
  }

  function parse(raw) {
    try {
      return JSON.parse(raw);
    } catch (error) {
      warn('ignoring an unreadable event', error);
      return null;
    }
  }

  function text(value) {
    return String(value == null ? '' : value);
  }

  function normalizeStep(step) {
    if (!step || typeof step !== 'object') return null;
    var out = { type: text(step.type) };
    if (step.tool_name) out.tool_name = text(step.tool_name);
    if (typeof step.ok === 'boolean') out.ok = step.ok;
    return out;
  }

  function normalizeRunning(raw) {
    return {
      activity_id: text(raw.activity_id),
      kind: text(raw.kind || 'task'),
      workspace_id: text(raw.workspace_id),
      agent_name: text(raw.agent_name),
      task_id: text(raw.task_id),
      blocked: !!raw.blocked,
      step: normalizeStep(raw.step),
      started_at: text(raw.started_at || raw.at),
      at: text(raw.at)
    };
  }

  function normalizeParcel(raw) {
    return {
      id: text(raw.id),
      workspace_id: text(raw.workspace_id),
      kind: text(raw.kind || 'task'),
      title: text(raw.title),
      agent_name: text(raw.agent_name),
      outcome: text(raw.outcome),
      produced_at: text(raw.produced_at)
    };
  }

  // Parsed, not compared as strings: the server trims trailing zeros from
  // fractional seconds, so "…:00Z" would sort after "…:00.1Z" as text.
  function timeOf(record) {
    var at = Date.parse(record.at || record.produced_at || '');
    return isFinite(at) ? at : 0;
  }

  function byNewest(a, b) {
    return timeOf(b) - timeOf(a);
  }

  function copy(record) {
    var out = {};
    Object.keys(record).forEach(function (key) {
      out[key] =
        record[key] && typeof record[key] === 'object'
          ? Object.assign({}, record[key])
          : record[key];
    });
    return out;
  }

  /**
   * What the feed knows about one workspace: its running activities (newest
   * first), its unopened parcels, and — on a live change — the event that
   * caused it. `lastEvent` is null when the view comes from a snapshot or a
   * disconnect, which is how a caller tells "this just happened" from "this is
   * how things are".
   */
  function viewFor(workspaceId, lastEvent) {
    var id = text(workspaceId);
    var list = [];
    Object.keys(running).forEach(function (key) {
      if (running[key].workspace_id === id) list.push(copy(running[key]));
    });
    list.sort(byNewest);
    var waiting = [];
    Object.keys(parcels).forEach(function (key) {
      if (parcels[key].workspace_id === id) waiting.push(copy(parcels[key]));
    });
    waiting.sort(byNewest);
    return {
      workspaceId: id,
      connected: connected,
      running: list,
      latest: list.length ? list[0] : null,
      count: list.length,
      blocked: list.some(function (activity) {
        return activity.blocked;
      }),
      parcels: waiting,
      lastEvent: lastEvent || null
    };
  }

  function workspaceIds() {
    var seen = Object.create(null);
    Object.keys(running).forEach(function (key) {
      seen[running[key].workspace_id] = true;
    });
    Object.keys(parcels).forEach(function (key) {
      seen[parcels[key].workspace_id] = true;
    });
    return Object.keys(seen).filter(Boolean);
  }

  function notify(workspaceId, lastEvent) {
    var view = viewFor(workspaceId, lastEvent);
    subscribers.slice().forEach(function (entry) {
      if (typeof entry.onChange !== 'function') return;
      try {
        entry.onChange(view.workspaceId, view);
      } catch (error) {
        warn('a subscriber failed', error);
      }
    });
  }

  function notifyConnection() {
    subscribers.slice().forEach(function (entry) {
      if (typeof entry.onConnection !== 'function') return;
      try {
        entry.onConnection(connected);
      } catch (error) {
        warn('a subscriber failed', error);
      }
    });
  }

  function applySnapshot(snapshot) {
    var touched = Object.create(null);
    workspaceIds().forEach(function (id) {
      touched[id] = true;
    });
    running = Object.create(null);
    (Array.isArray(snapshot && snapshot.running) ? snapshot.running : []).forEach(function (raw) {
      if (!raw || !raw.activity_id || !raw.workspace_id) return;
      var activity = normalizeRunning(raw);
      running[activity.activity_id] = activity;
    });
    parcels = Object.create(null);
    (Array.isArray(snapshot && snapshot.parcels) ? snapshot.parcels : []).forEach(function (raw) {
      if (!raw || !raw.id || !raw.workspace_id) return;
      var parcel = normalizeParcel(raw);
      parcels[parcel.id] = parcel;
    });
    workspaceIds().forEach(function (id) {
      touched[id] = true;
    });
    sawSnapshot = true;
    attempt = 0;
    if (!connected) {
      connected = true;
      notifyConnection();
    }
    Object.keys(touched).forEach(function (id) {
      notify(id, null);
    });
  }

  function applyActivity(raw) {
    if (!raw || !raw.activity_id || !raw.workspace_id) return;
    var id = text(raw.activity_id);
    var phase = text(raw.phase);
    var existing = running[id];
    var event = {
      kind: text(raw.kind || 'task'),
      phase: phase,
      activity_id: id,
      workspace_id: text(raw.workspace_id),
      agent_name: text(raw.agent_name),
      task_id: text(raw.task_id),
      step: normalizeStep(raw.step),
      outcome: text(raw.outcome),
      parcel_id: text(raw.parcel_id),
      count: typeof raw.count === 'number' ? raw.count : null,
      at: text(raw.at)
    };

    if (phase === 'finished') {
      delete running[id];
    } else {
      var activity = existing || normalizeRunning(raw);
      if (event.agent_name) activity.agent_name = event.agent_name;
      activity.at = event.at || activity.at;
      if (phase === 'started') {
        activity.blocked = false;
        activity.step = null;
      } else if (phase === 'step') {
        activity.step = event.step;
      } else if (phase === 'blocked') {
        activity.blocked = true;
      } else if (phase === 'resumed') {
        activity.blocked = false;
      }
      running[id] = activity;
    }
    notify(event.workspace_id, event);
  }

  function applyParcel(raw) {
    if (!raw || !raw.id || !raw.workspace_id) return;
    var parcel = normalizeParcel(raw);
    if (raw.opened) {
      delete parcels[parcel.id];
    } else {
      parcels[parcel.id] = parcel;
    }
    notify(parcel.workspace_id, { phase: raw.opened ? 'parcel_opened' : 'parcel', parcel: parcel });
  }

  function clearRunning() {
    var touched = Object.create(null);
    Object.keys(running).forEach(function (key) {
      touched[running[key].workspace_id] = true;
    });
    running = Object.create(null);
    if (connected) {
      connected = false;
      notifyConnection();
    }
    Object.keys(touched).forEach(function (id) {
      notify(id, null);
    });
  }

  function scheduleReconnect() {
    if (disabled || reconnectTimer || !subscribers.length) return;
    var delay = BACKOFF_MS[Math.min(attempt, BACKOFF_MS.length - 1)];
    attempt += 1;
    reconnectTimer = window.setTimeout(function () {
      reconnectTimer = null;
      open();
    }, delay);
  }

  // A stream that failed before it ever said anything might be a feature that
  // is switched off. One snapshot request tells a 404 apart from a server that
  // is merely down, which keeps reconnecting.
  function probeThenReconnect() {
    if (typeof window.fetch !== 'function') {
      scheduleReconnect();
      return;
    }
    var probe;
    try {
      probe = window.fetch(SNAPSHOT_URL, { headers: { Accept: 'application/json' } });
    } catch (error) {
      scheduleReconnect();
      return;
    }
    Promise.resolve(probe)
      .then(function (response) {
        if (response && response.status === 404) {
          disabled = true;
          return;
        }
        scheduleReconnect();
      })
      .catch(function () {
        scheduleReconnect();
      });
  }

  function handleDrop(es) {
    if (source !== es) return;
    try {
      es.close();
    } catch (error) {
      /* already closed */
    }
    source = null;
    var hadSnapshot = sawSnapshot;
    sawSnapshot = false;
    clearRunning();
    if (hadSnapshot) scheduleReconnect();
    else probeThenReconnect();
  }

  function open() {
    if (source || disabled || !subscribers.length) return;
    var EventSourceCtor = window.EventSource;
    if (typeof EventSourceCtor !== 'function') return;
    var es;
    try {
      es = new EventSourceCtor(STREAM_URL);
    } catch (error) {
      warn('could not open the activity stream', error);
      scheduleReconnect();
      return;
    }
    source = es;
    sawSnapshot = false;
    es.addEventListener('snapshot', function (event) {
      if (source !== es) return;
      var data = parse(event && event.data);
      if (data) applySnapshot(data);
    });
    es.addEventListener('activity', function (event) {
      if (source !== es || !sawSnapshot) return;
      applyActivity(parse(event && event.data));
    });
    es.addEventListener('parcel', function (event) {
      if (source !== es || !sawSnapshot) return;
      applyParcel(parse(event && event.data));
    });
    es.addEventListener('error', function () {
      handleDrop(es);
    });
  }

  function close() {
    if (reconnectTimer) {
      window.clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
    if (source) {
      try {
        source.close();
      } catch (error) {
        /* already closed */
      }
    }
    source = null;
    sawSnapshot = false;
    connected = false;
    attempt = 0;
    running = Object.create(null);
    parcels = Object.create(null);
  }

  /**
   * Subscribe to the feed. `listener` is either a function called as
   * (workspaceId, view) for every workspace whose view changed, or an object
   * { onChange(workspaceId, view), onConnection(connected) }. A subscriber that
   * joins an already-connected feed is told the current state straight away.
   * Returns a release function; the last release closes the connection.
   */
  function subscribe(listener) {
    var entry = typeof listener === 'function' ? { onChange: listener } : listener || {};
    subscribers.push(entry);
    if (subscribers.length === 1) {
      open();
    } else if (connected) {
      if (typeof entry.onConnection === 'function') entry.onConnection(true);
      workspaceIds().forEach(function (id) {
        if (typeof entry.onChange === 'function') entry.onChange(id, viewFor(id, null));
      });
    }
    var released = false;
    return function release() {
      if (released) return;
      released = true;
      var index = subscribers.indexOf(entry);
      if (index !== -1) subscribers.splice(index, 1);
      if (!subscribers.length) close();
    };
  }

  window.OriMapActivityFeed = {
    subscribe: subscribe,
    isConnected: function () {
      return connected;
    },
    get connected() {
      return connected;
    },
    viewFor: function (workspaceId) {
      return viewFor(workspaceId, null);
    },
    workspaceIds: workspaceIds,
    subscriberCount: function () {
      return subscribers.length;
    },
    _resetForTest: function () {
      subscribers = [];
      close();
      disabled = false;
    }
  };
})();
