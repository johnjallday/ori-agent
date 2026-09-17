/*
 * activity-bubble-lines.js — the one line a working agent's speech bubble says
 * (tasks/prd-task-run-show.md FR24–FR28).
 *
 * A pure table from an activity-feed event to a short string. It is a plain
 * deferred script exposing window.OriActivityBubbleLines, because its callers
 * (workspace-map.js and the Operations map) include classic scripts that cannot
 * import an ES module.
 *
 * Honesty rule (FR26): a line never claims anything the event does not prove.
 * No counts, no file names, no results — the only variable text that can ever
 * appear is the tool's own name, turned into a label.
 */
(function () {
  'use strict';

  var TOOL_LABEL_MAX = 28;

  // First match wins, in this order (FR25). Patterns are matched against the
  // lower-cased tool name. Only host-owned capabilities have a row: tools that
  // come from a domain plugin fall through to "Using <tool>…", because the host
  // must not name a plugin's domain (the compiled domain-extraction audit in
  // internal/server enforces that).
  var TOOL_LINES = [
    { patterns: ['read', 'get_file', 'list', 'open'], line: 'Reading files…' },
    { patterns: ['write', 'create', 'save', 'edit'], line: 'Writing it down…' },
    { patterns: ['search', 'web', 'fetch', 'browse'], line: 'Looking it up…' },
    { patterns: ['note'], line: 'Taking notes…' },
    { patterns: ['calendar'], line: 'Checking the calendar…' },
    { patterns: ['mail', 'gmail'], line: 'Going through mail…' }
  ];

  var STARTED_LINES = {
    task: 'On it.',
    daily_brief: 'Preparing today’s brief…',
    file_janitor: 'Sorting through the folder…'
  };

  /** The tool name as a label: underscores become spaces, cut to 28 characters. */
  function toolLabel(name) {
    var label = String(name == null ? '' : name)
      .replace(/_/g, ' ')
      .replace(/\s+/g, ' ')
      .trim();
    if (label.length > TOOL_LABEL_MAX) label = label.slice(0, TOOL_LABEL_MAX).trim();
    return label;
  }

  function toolCallLine(name) {
    var lower = String(name == null ? '' : name).toLowerCase();
    for (var i = 0; i < TOOL_LINES.length; i++) {
      var entry = TOOL_LINES[i];
      for (var j = 0; j < entry.patterns.length; j++) {
        if (lower.indexOf(entry.patterns[j]) !== -1) return entry.line;
      }
    }
    var label = toolLabel(name);
    return label ? 'Using ' + label + '…' : '';
  }

  /**
   * The line for one activity event, or '' when the event has nothing new to
   * say (a successful tool result, or a run that went silent and was dropped).
   * A caller keeps the current line on ''.
   */
  function lineFor(event) {
    var ev = event || {};
    var kind = String(ev.kind || 'task');
    var phase = String(ev.phase || '');
    var step = ev.step || null;

    switch (phase) {
      case 'started':
        return STARTED_LINES[kind] || STARTED_LINES.task;
      case 'step':
        if (!step) return '';
        if (step.type === 'thinking') return 'Thinking it through…';
        if (step.type === 'tool_call') return toolCallLine(step.tool_name);
        if (step.type === 'tool_result') return step.ok === false ? 'Hit a snag, adjusting…' : '';
        if (step.type === 'delegation') return 'Asking for a hand…';
        return '';
      case 'blocked':
        return 'I need your input.';
      case 'resumed':
        return 'Back to it.';
      case 'finished':
        if (ev.outcome === 'succeeded' || ev.outcome === 'partial') {
          if (kind === 'file_janitor' && Number(ev.count) === 0) return 'Nothing to tidy.';
          return 'Done.';
        }
        if (ev.outcome === 'failed' || ev.outcome === 'timeout') return 'This one didn’t work out.';
        return '';
      default:
        return '';
    }
  }

  window.OriActivityBubbleLines = Object.freeze({
    lineFor: lineFor,
    toolLabel: toolLabel
  });
})();
