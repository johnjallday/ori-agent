// Folder setup plan lines: what one Set up does, line by line, as the host
// computed it. Home's folder card, the mission card and the Create Workspace
// build pane draw the same lines with the same words.
//
// A classic script with no imports or exports, so the classic build pane can
// load it with a <script> tag and the ES modules can import it for its effect;
// both read window.OriFolderPlanLines.
(function () {
  'use strict';

  // The states of one plan line while the server runs the setup.
  const SETUP_LINE_STATES = ['waiting', 'working', 'done', 'failed'];

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
    failed: ['!', 'Did not finish']
  };

  // renderSetupLines draws a plan or a run's lines into a list. The state is a
  // glyph and a visually hidden word, never colour alone.
  function renderSetupLines(list, lines, doc = document) {
    if (!list) return;
    list.replaceChildren();
    lines.forEach(line => {
      const item = doc.createElement('li');
      item.dataset.kind = line.kind || 'other';
      if (line.state) item.dataset.state = line.state;
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

  globalThis.OriFolderPlanLines = { setupLinesView, renderSetupLines };
})();
