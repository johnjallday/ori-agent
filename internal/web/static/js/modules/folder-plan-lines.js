// Folder setup plan lines: what one Set up does, line by line, as the host
// computed it. Home's folder card, the mission card and the Create Workspace
// build pane draw the same lines with the same words.
//
// A classic script with no imports or exports, so the classic build pane can
// load it with a <script> tag and the ES modules can import it for its effect;
// both read window.OriFolderPlanLines.
(function () {
  'use strict';

  // The states of one plan line while the server runs the setup, and
  // "already": a line the plan has nothing to do for, checked before the run.
  const SETUP_LINE_STATES = ['waiting', 'working', 'done', 'failed', 'already'];

  // setupLinesView normalizes the server's plan or run lines for rendering. Every
  // field is text; an unknown state is shown as a neutral row, never trusted.
  function setupLinesView(lines) {
    return (Array.isArray(lines) ? lines : [])
      .filter(line => line && typeof line === 'object' && String(line.name || '').trim())
      .map(line => ({
        kind: String(line.kind || '').trim(),
        name: String(line.name).trim(),
        detail: String(line.detail || '').trim(),
        state: SETUP_LINE_STATES.includes(line.state) ? line.state : ''
      }));
  }

  const SETUP_LINE_GLYPHS = {
    waiting: ['○', 'Waiting'],
    working: ['◔', 'In progress'],
    done: ['✓', 'Done'],
    failed: ['!', 'Did not finish'],
    already: ['✓', 'Already in place']
  };

  // satisfiedLinesView marks the lines a plan has nothing left to do for: a
  // plugin line (integration or provider) whose need the plan reports as
  // ready is "already", checked before Set it up is pressed. A run's own
  // states (working, done, failed) win; only an unstated or waiting line is
  // marked. needs is the plan's needs list ({kind, name, state}).
  function satisfiedLinesView(lines, needs) {
    const ready = new Set(
      (Array.isArray(needs) ? needs : [])
        .filter(need => need && typeof need === 'object' && need.state === 'ready')
        .map(need => String(need.kind || '').trim())
        .filter(Boolean)
    );
    if (!ready.size) return lines;
    return lines.map(line =>
      ready.has(line.kind) && (!line.state || line.state === 'waiting')
        ? { ...line, state: 'already' }
        : line
    );
  }

  // What each kind of line is about, as a Bootstrap icon (head.tmpl loads the
  // font), so a checklist says "a Home", "a folder", "a team" before its
  // sentence is read. The kinds are personalassistant's FolderPlan* constants;
  // one the pane does not know gets a plain dot.
  const SETUP_LINE_KIND_ICONS = {
    integration: 'bi-plug',
    provider: 'bi-box-seam',
    home: 'bi-house-door',
    group: 'bi-houses',
    workspace: 'bi-building',
    workspaces: 'bi-buildings',
    member: 'bi-building',
    folder: 'bi-folder-symlink',
    mode: 'bi-eye',
    agents: 'bi-people',
    assistant: 'bi-person-badge',
    task: 'bi-clipboard-check',
    library: 'bi-collection',
    songs: 'bi-music-note-list',
    profile: 'bi-sliders',
    unavailable: 'bi-dash-circle',
    arrangement: 'bi-bookmark-check',
    first_look_read: 'bi-search',
    first_look_suggest: 'bi-lightbulb'
  };

  // setupLineKindIcon is the Bootstrap icon class for one line kind.
  function setupLineKindIcon(kind) {
    return SETUP_LINE_KIND_ICONS[String(kind || '').trim()] || 'bi-dot';
  }

  // renderSetupLines draws a plan or a run's lines into a list. The state is a
  // glyph and a visually hidden word, never colour alone. With
  // options.kindIcons each row also leads with its kind's icon (the build
  // pane's checklist); Home's card and the mission card keep their plain rows.
  function renderSetupLines(list, lines, doc = document, options = {}) {
    if (!list) return;
    list.replaceChildren();
    lines.forEach(line => {
      const item = doc.createElement('li');
      item.dataset.kind = line.kind || 'other';
      if (line.state) item.dataset.state = line.state;
      if (options.kindIcons) {
        const kind = doc.createElement('span');
        kind.className = 'pa-folder__plan-kind';
        kind.setAttribute('aria-hidden', 'true');
        const glyph = doc.createElement('i');
        glyph.className = `bi ${setupLineKindIcon(line.kind)}`;
        kind.append(glyph);
        item.append(kind);
      }
      const icon = doc.createElement('span');
      icon.className = 'pa-folder__plan-icon';
      icon.setAttribute('aria-hidden', 'true');
      icon.textContent = line.state ? SETUP_LINE_GLYPHS[line.state][0] : '•';
      const body = doc.createElement('span');
      body.className = 'pa-folder__plan-text';
      body.append(line.name);
      if (line.detail) {
        const detail = doc.createElement('small');
        detail.textContent = line.detail;
        body.append(detail);
      }
      if (line.state) {
        const word = doc.createElement('span');
        word.className = 'visually-hidden';
        word.textContent = ` — ${SETUP_LINE_GLYPHS[line.state][1]}`;
        body.append(word);
      }
      item.append(icon, body);
      list.append(item);
    });
  }

  globalThis.OriFolderPlanLines = {
    setupLinesView,
    satisfiedLinesView,
    renderSetupLines,
    setupLineKindIcon
  };
})();
