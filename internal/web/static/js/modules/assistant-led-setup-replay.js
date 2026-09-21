// Replays the reviewed Create Workspace and Create Agent windows as the real
// modals, being clicked through. The picture is built from the live modal's own
// markup, so it looks like the modal you would have used, but it is only a
// picture: every control is replaced by inert text, every id and handler is
// left behind, the whole thing is `inert`, and it sends nothing. What it shows
// is only what Ori already recorded (names, blueprint, team, model
// availability); timers and the cursor move the view, never a status.

const ZOOM_WIDTH = 820;

// The workspace modal is copied with these elements tagged so the replay can
// find them after every id has been stripped.
const WORKSPACE_TARGETS = [
  'wizardStepper',
  'wizardStep1',
  'wizardStep2',
  'wizardStep3',
  'wizardStep4',
  'templateBuiltinGrid',
  'templateBriefingDefault',
  'wizardStep2RecapName',
  'wizardStep2RecapIcon',
  'folderNameInput',
  'workspaceTeamRoster',
  'workspaceReviewSummary',
  'wizardBackBtn',
  'wizardNextBtn',
  'createFolderBtn'
];

const STRIP_ATTRIBUTES = [
  'id',
  'for',
  'name',
  'form',
  'tabindex',
  'autocomplete',
  'aria-labelledby',
  'aria-describedby',
  'aria-controls',
  'aria-owns',
  'aria-modal',
  'aria-live',
  'aria-current'
];

// Blueprint cards for step 1: Blank first, then the built-in blueprints in the
// order the server lists them, with File Janitor guaranteed present so the
// replay always has something to click. Custom (user) templates are not shown,
// as in the real grid. Only display fields are read.
export function blueprintCards(templates) {
  const cards = [
    {
      id: 'blank',
      label: 'Blank',
      tagline: 'Start from an empty workspace.',
      icon: '✍',
      builtin: true
    }
  ];
  const list = Array.isArray(templates) ? templates : [];
  list
    .filter(template => template && template.builtin && template.id && template.id !== 'blank')
    .forEach(template =>
      cards.push({
        id: String(template.id),
        label: String(template.label || template.name || template.id),
        tagline: taglineFor(template),
        description: String(template.description || ''),
        icon: String(template.icon || '📁'),
        builtin: true
      })
    );
  if (!cards.some(card => card.id === 'file-janitor')) {
    cards.push({
      id: 'file-janitor',
      label: 'File Janitor',
      tagline: 'Review and file one intake folder safely.',
      icon: '📁',
      builtin: true
    });
  }
  return cards;
}

function taglineFor(template) {
  const tagline = String(template.tagline || '').trim();
  if (tagline) return tagline;
  const description = String(template.description || '').trim();
  if (!description) return '';
  const first = (description.match(/^.*?[.!?](?:\s|$)/)?.[0] || description).trim();
  return first.length > 80 ? `${first.slice(0, 79).trimEnd()}…` : first;
}

// The live modals this replay can copy. Either can be missing on a page; the
// caller then falls back to the plain windows.
export function replaySources(doc) {
  return {
    workspace: doc?.getElementById?.('addFolderModal') || null,
    agent: doc?.getElementById?.('addAgentModal') || null,
    agentForm: doc?.getElementById?.('agentCreateFormTemplate') || null
  };
}

function inertText(doc, source, text) {
  const node = doc.createElement('span');
  node.className = `${source.className || ''} assistant-replay-text`.trim();
  const dataset = source.dataset || {};
  Object.keys(dataset).forEach(key => {
    node.dataset[key] = dataset[key];
  });
  node.textContent = text;
  return node;
}

// Copies a live modal subtree and makes it a picture: no ids, no form controls,
// no scripts, no handlers (cloneNode never copies listeners), and nothing to
// focus. Tagged elements keep a `data-replay` name so the replay can find them.
export function sanitizeReplica(doc, source, targets = []) {
  const clone = source.cloneNode(true);
  targets.forEach(name => {
    const found = clone.querySelector(`#${name}`);
    if (found) found.dataset.replay = name;
  });
  clone.querySelectorAll('script, template, style').forEach(node => node.remove());
  clone.querySelectorAll('input, textarea, select, button').forEach(control => {
    const tag = control.tagName.toLowerCase();
    let text = '';
    if (tag === 'select') {
      text = control.selectedOptions?.[0]?.textContent?.trim() || '';
    } else if (tag === 'button') {
      text = control.textContent.trim();
    } else if (control.type === 'radio' || control.type === 'checkbox') {
      const box = inertText(doc, control, '');
      box.classList.add('assistant-replay-check');
      if (control.checked || control.hasAttribute('checked')) box.classList.add('is-checked');
      control.replaceWith(box);
      return;
    } else {
      text = control.getAttribute('placeholder') || '';
      if (control.getAttribute('type') === 'hidden') {
        control.remove();
        return;
      }
    }
    const replacement = inertText(doc, control, text);
    if (text && tag !== 'button' && tag !== 'select') {
      replacement.classList.add('assistant-replay-text--placeholder');
    }
    if (control.hasAttribute('hidden')) replacement.setAttribute('hidden', '');
    if (tag === 'button' && control.classList.contains('btn-close')) replacement.textContent = '×';
    control.replaceWith(replacement);
  });
  const all = [clone, ...clone.querySelectorAll('*')];
  all.forEach(node => {
    STRIP_ATTRIBUTES.forEach(attribute => node.removeAttribute(attribute));
    Array.from(node.attributes)
      .filter(attribute => attribute.name.startsWith('data-bs-'))
      .forEach(attribute => node.removeAttribute(attribute.name));
  });
  // Keep `.modal` (it carries Bootstrap's modal variables); the replay CSS
  // makes it static. `fade` and `show` would animate or display it for real.
  clone.classList.remove('fade', 'show');
  clone.removeAttribute('role');
  clone.removeAttribute('style');
  clone.setAttribute('aria-hidden', 'true');
  clone.setAttribute('inert', '');
  return clone;
}

function el(doc, tag, className, text) {
  const node = doc.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

// Plays one modal click-through inside `host`. `kind` is `workspace` or
// `agent`. The returned controller can `play()` (returns a promise that
// resolves when the replay ends or is skipped), `showFinal()` for reduced
// motion, and `stop()`.
export function createModalReplay({ doc, win, host, kind, spec, sources, fetchImpl }) {
  const stage = el(doc, 'div', 'assistant-led-replay');
  stage.dataset.kind = kind;
  const backdrop = el(doc, 'div', 'assistant-led-replay__backdrop');
  const zoom = el(doc, 'div', 'assistant-led-replay__zoom');
  const cursor = el(doc, 'div', 'assistant-led-replay__cursor');
  cursor.setAttribute('aria-hidden', 'true');
  stage.append(backdrop, zoom, cursor);
  host.append(stage);

  const state = { cancelled: false, skipping: false, resolveDone: null, replica: null };
  const summary = el(doc, 'span', 'visually-hidden', spec.summary);
  host.append(summary);

  function scale() {
    const width = stage.clientWidth || ZOOM_WIDTH;
    const factor = Math.min(1, width / ZOOM_WIDTH);
    zoom.style.zoom = String(factor);
    zoom.style.height = `${(stage.clientHeight || 520) / factor}px`;
  }

  // `window.__oriReplaySlow = 5` stretches every pause five-fold. It exists so
  // a person recording or screenshotting the replay can catch each phase.
  const slow = Math.max(1, Number(win.__oriReplaySlow) || 1);
  const wait = ms =>
    new Promise(resolve => {
      if (state.skipping || state.cancelled) return resolve();
      win.setTimeout(resolve, ms * slow);
    });

  function centerOf(node) {
    const stageRect = stage.getBoundingClientRect();
    const rect = node.getBoundingClientRect();
    return {
      x: rect.left - stageRect.left + rect.width / 2,
      y: rect.top - stageRect.top + rect.height / 2
    };
  }

  async function moveTo(node) {
    if (!node || state.skipping) return;
    const point = centerOf(node);
    cursor.dataset.visible = 'true';
    cursor.style.transform = `translate(${point.x}px, ${point.y}px)`;
    await wait(850);
  }

  async function click(node, effect) {
    await moveTo(node);
    if (state.cancelled) return;
    if (!state.skipping) {
      cursor.classList.remove('is-clicking');
      void cursor.offsetWidth; // restart the ripple
      cursor.classList.add('is-clicking');
      node?.classList.add('is-pressed');
      await wait(220);
      node?.classList.remove('is-pressed');
    }
    effect?.();
    await wait(350);
  }

  const q = name => state.replica?.querySelector(`[data-replay="${name}"]`);

  function setStep(number) {
    for (let index = 1; index <= 4; index += 1) {
      const section = q(`wizardStep${index}`);
      if (section) section.hidden = index !== number;
    }
    const steps = q('wizardStepper')?.querySelectorAll('.workspace-create-step') || [];
    steps.forEach((step, index) => {
      step.classList.toggle('is-active', index + 1 === number);
      step.classList.toggle('is-complete', index + 1 < number);
      step.classList.toggle('is-done', index + 1 < number);
    });
    const back = q('wizardBackBtn');
    const next = q('wizardNextBtn');
    const create = q('createFolderBtn');
    if (back) back.hidden = number === 1;
    if (next) next.hidden = number === 4;
    if (create) create.hidden = number !== 4;
    // Bring the stepper and this step's heading to the top of the modal body,
    // as the real wizard does, so what is being filled in is on screen.
    const body = state.replica?.querySelector('.modal-body');
    const stepper = q('wizardStepper');
    if (body) {
      body.scrollTo({
        top: number === 1 || !stepper ? 0 : Math.max(0, stepper.offsetTop - 12),
        behavior: state.skipping ? 'auto' : 'smooth'
      });
    }
  }

  function fill(node, text) {
    if (!node) return;
    node.textContent = text;
    node.classList.remove('assistant-replay-text--placeholder');
    node.classList.add('assistant-replay-typed');
  }

  async function workspaceCards() {
    const grid = q('templateBuiltinGrid');
    if (!grid) return null;
    let templates = [];
    try {
      const response = await fetchImpl('/api/project-templates', {
        headers: { Accept: 'application/json' }
      });
      if (response.ok) templates = (await response.json())?.templates || [];
    } catch {
      templates = [];
    }
    const building = win.OriWorkspaceBuildingArt;
    let target = null;
    blueprintCards(templates).forEach(card => {
      const item = el(doc, 'div', 'workspace-template-card');
      item.setAttribute('role', 'presentation');
      item.dataset.blueprint = card.id;
      const icon = el(doc, 'span', 'workspace-template-card-icon');
      const variant = building?.variantForBlueprint?.(card.id, card.builtin);
      const art = variant ? building?.svgForVariant?.(variant, { context: 'catalog' }) : '';
      if (art) {
        icon.classList.add('has-building-art');
        icon.innerHTML = art;
      } else {
        icon.textContent = card.icon;
      }
      item.append(icon, el(doc, 'span', 'workspace-template-card-label', card.label));
      if (card.tagline) item.append(el(doc, 'span', 'workspace-template-card-desc', card.tagline));
      grid.append(item);
      if (card.id === 'file-janitor') {
        target = item;
        item.dataset.description = card.description;
        item.dataset.icon = card.icon;
      }
    });
    return target;
  }

  function fillTeam() {
    const roster = q('workspaceTeamRoster');
    if (!roster) return;
    roster.replaceChildren();
    spec.team.forEach(member => {
      const row = el(doc, 'li', 'workspace-team-row');
      const main = el(doc, 'div', 'workspace-team-row-main');
      main.append(el(doc, 'strong', '', member.name));
      main.append(
        ' ',
        el(doc, 'span', 'workspace-team-badge', member.action === 'reuse' ? 'Reuse' : 'Create')
      );
      row.append(main);
      if (member.note) row.append(el(doc, 'div', 'workspace-team-row-copy', member.note));
      roster.append(row);
    });
  }

  function fillReview() {
    const summary = q('workspaceReviewSummary');
    if (!summary) return;
    summary.replaceChildren();
    [
      ['Blueprint', spec.blueprintLabel, spec.blueprintNote],
      ['Workspace name', spec.workspaceName, 'Created in your Ori Workspaces folder'],
      ['Team', spec.team.map(member => member.name).join(', '), 'One team member']
    ].forEach(([label, main, meta]) => {
      const card = el(doc, 'div', 'workspace-review-card');
      card.append(el(doc, 'span', 'workspace-review-card-label', label));
      card.append(el(doc, 'div', 'workspace-review-card-main', main));
      if (meta) card.append(el(doc, 'div', 'workspace-review-card-meta', meta));
      summary.append(card);
    });
  }

  function showCreated(button, text) {
    button?.classList.add('is-success');
    if (button) button.textContent = text;
    const footer = state.replica?.querySelector('.modal-footer');
    if (footer && !footer.querySelector('.assistant-replay-created')) {
      footer.prepend(el(doc, 'span', 'assistant-replay-created me-auto', spec.createdLine));
    }
  }

  async function playWorkspace() {
    setStep(1);
    const target = await workspaceCards();
    await wait(700);
    const scroller = state.replica.querySelector('.modal-body');
    if (target && scroller && !state.skipping) {
      scroller.scrollTo({ top: Math.max(0, target.offsetTop - 120), behavior: 'smooth' });
      await wait(500);
    }
    await click(target, () => {
      state.replica
        .querySelectorAll('.workspace-template-card')
        .forEach(card => card.classList.remove('is-selected'));
      target?.classList.add('is-selected');
      const briefing = q('templateBriefingDefault');
      if (briefing && target?.dataset.description)
        briefing.textContent = target.dataset.description;
    });
    await click(q('wizardNextBtn'), () => setStep(2));
    // The recap shows the blueprint that was clicked, not the default Blank.
    const recapIcon = q('wizardStep2RecapIcon');
    if (recapIcon && target?.dataset.icon) recapIcon.textContent = target.dataset.icon;
    fill(q('wizardStep2RecapName'), spec.blueprintLabel);
    await wait(500);
    await moveTo(q('folderNameInput'));
    fill(q('folderNameInput'), spec.workspaceName);
    await wait(1100);
    await click(q('wizardNextBtn'), () => {
      setStep(3);
      fillTeam();
    });
    await wait(900);
    await click(q('wizardNextBtn'), () => {
      setStep(4);
      fillReview();
    });
    await wait(900);
    await click(q('createFolderBtn'), () => showCreated(q('createFolderBtn'), spec.createdButton));
    await wait(1300);
  }

  async function playAgent() {
    const name = state.replica.querySelector('[data-agent-create-field="name"]');
    const model = state.replica.querySelector('[data-agent-create-field="model"]');
    await wait(900);
    await moveTo(name);
    fill(name, spec.agentName);
    await wait(1100);
    await moveTo(model);
    fill(model, spec.modelText);
    await wait(900);
    const create = state.replica.querySelector('[data-replay="createAgentBtn"]');
    await click(create, () => showCreated(create, spec.createdButton));
    await wait(1300);
  }

  function buildReplica() {
    if (kind === 'workspace') {
      const replica = sanitizeReplica(doc, sources.workspace, WORKSPACE_TARGETS);
      replica.classList.add('assistant-led-replay__modal');
      return replica;
    }
    const replica = sanitizeReplica(doc, sources.agent);
    replica.classList.add('assistant-led-replay__modal');
    const body = replica.querySelector('.modal-body');
    const formSource = sources.agentForm?.content;
    if (body && formSource) {
      const form = sanitizeReplica(doc, formSource.cloneNode(true).firstElementChild || formSource);
      form
        .querySelectorAll(
          '[data-agent-create-section="systemPrompt"], [data-agent-create-section="reasoningEffort"]'
        )
        .forEach(section => section.remove());
      body.replaceChildren(form);
    }
    const buttons = Array.from(replica.querySelectorAll('.modal-footer .assistant-replay-text'));
    const create =
      buttons.find(button => /create agent/i.test(button.textContent)) || buttons.at(-1);
    if (create) create.dataset.replay = 'createAgentBtn';
    return replica;
  }

  function mount() {
    if (state.replica) return;
    state.replica = buildReplica();
    state.replica.classList.add('is-entering');
    zoom.append(state.replica);
    scale();
  }

  function markFinal() {
    stage.dataset.state = 'done';
    cursor.dataset.visible = 'false';
  }

  async function play() {
    mount();
    stage.dataset.state = 'playing';
    await wait(500);
    state.replica.classList.remove('is-entering');
    try {
      if (kind === 'workspace') await playWorkspace();
      else await playAgent();
    } finally {
      markFinal();
    }
  }

  // Reduced motion, Skip, or Next: the finished picture at once.
  async function showFinal() {
    state.skipping = true;
    await play();
  }

  function stop() {
    state.cancelled = true;
    stage.remove();
    summary.remove();
  }

  function skip() {
    state.skipping = true;
  }

  return { play, showFinal, stop, skip, stage };
}
