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
    handoff: 'bi-person-check',
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

  // handoffSections splits a single song's lines at its handoff line: what the
  // assistant does for the Home, under a heading naming the Home, then what the
  // Home's Portfolio Manager does for the song, under the handoff line itself.
  // With nothing to create for the Home, the first heading says the song is
  // added to it. names is { home, song } from the plan's destination and
  // visual. null for a plan that hands nothing over.
  function handoffSections(lines, names = {}) {
    const at = lines.findIndex(line => line.kind === 'handoff');
    if (at < 0) return null;
    const first = lines.slice(0, at);
    const home = String(names?.home || '').trim() || 'your Home';
    const song = String(names?.song || '').trim();
    const creates = first.some(line => line.kind === 'home' || line.kind === 'agents');
    return {
      heading:
        creates || !song
          ? `Your assistant sets up ${home}`
          : `Your assistant adds ${song} to ${home}`,
      first,
      handoff: lines[at],
      rest: lines.slice(at + 1)
    };
  }

  const HANDOFF_LINE = /^Your (.+) then (?:opens|suggests opening) (.+)$/;

  // handoffLineName is the handoff line's words for its state (FR19): the plan
  // says "Your Portfolio Manager then suggests opening Album-5"; while it works,
  // "Portfolio Manager is opening Album-5…"; once done, "Portfolio Manager
  // opened Album-5". A waiting or failed line keeps the plan's words, and a
  // line not worded that way is shown as it is. The run never stores these
  // words, so a resumed plan is still the one that was confirmed.
  function handoffLineName(line, song) {
    const name = String(line?.name || '');
    const title = String(song || '').trim();
    const match = title ? name.match(HANDOFF_LINE) : null;
    if (!match || match[2] !== title) return name;
    if (line.state === 'working') return `${match[1]} is opening ${title}…`;
    if (line.state === 'done') return `${match[1]} opened ${title}`;
    return name;
  }

  // handoffLineParts reads the manager ("Your Portfolio Manager") and the song
  // from a plan's handoff line; null when there is none or it is worded
  // otherwise.
  function handoffLineParts(lines) {
    const line = (Array.isArray(lines) ? lines : []).find(item => item?.kind === 'handoff');
    const match = String(line?.name || '').match(HANDOFF_LINE);
    return match ? { manager: `Your ${match[1]}`, song: match[2] } : null;
  }

  // handoffNames reads the Home and song a plan or run view names: the
  // destination it goes into, and the workspace it creates or, when the
  // picture is of the Home, the song its handoff line names.
  function handoffNames(source) {
    const entity = source?.visual?.entity;
    const song =
      entity?.kind === 'workspace'
        ? String(entity?.name || '').trim()
        : handoffLineParts(source?.lines)?.song || '';
    return {
      home: String(source?.destination?.name || source?.home?.name || '').trim(),
      song
    };
  }

  // renderSetupLines draws a plan or a run's lines into a list. The state is a
  // glyph and a visually hidden word, never colour alone. With
  // options.kindIcons each row also leads with its kind's icon (the build
  // pane's checklist); Home's card and the mission card keep their plain rows.
  // A single song's plan is drawn in two sections (handoffSections), named
  // from options.sections ({ home, song }). options.half draws one of them:
  // 'home' (the assistant's card: the Home's lines, closed by the handoff
  // line) or 'song' (the Home's suggestion: what opening the song does).
  function renderSetupLines(list, lines, doc = document, options = {}) {
    if (!list) return;
    list.replaceChildren();
    let sections = handoffSections(lines, options.sections);
    if (sections && options.half === 'song') {
      lines = sections.rest;
      sections = null;
    } else if (sections && options.half === 'home') {
      lines = [...sections.first, sections.handoff];
    }
    if (sections) {
      list.dataset.sections = 'handoff';
      const heading = doc.createElement('li');
      heading.className = 'pa-folder__plan-section';
      heading.dataset.kind = 'section';
      heading.dataset.section = 'home';
      const title = doc.createElement('span');
      title.className = 'pa-folder__plan-section-title';
      title.setAttribute('role', 'heading');
      title.setAttribute('aria-level', '4');
      title.textContent = sections.heading;
      heading.append(title);
      list.append(heading);
    } else {
      delete list.dataset.sections;
    }
    lines.forEach(line => {
      const item = doc.createElement('li');
      if (sections && line === sections.handoff) {
        // The handoff line heads the second section and keeps its progress.
        item.className = 'pa-folder__plan-section';
        item.dataset.section = 'handoff';
      }
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
      if (sections && line === sections.handoff) {
        const title = doc.createElement('span');
        title.className = 'pa-folder__plan-section-title';
        title.setAttribute('role', 'heading');
        title.setAttribute('aria-level', '4');
        title.textContent = handoffLineName(line, options.sections?.song);
        body.append(title);
      } else {
        body.append(line.name);
      }
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

  const HIRE_MODES = ['create', 'bind', 'pending'];

  // initialsOf is the two letters an agent's badge shows.
  function initialsOf(name) {
    const words = String(name || '')
      .split(/\s+/)
      .filter(word => /^[\p{L}\p{N}]/u.test(word));
    return words
      .slice(0, 2)
      .map(word => Array.from(word)[0].toUpperCase())
      .join('');
  }

  function segments(parts) {
    return parts.map(part =>
      typeof part === 'string'
        ? { text: part, strong: false }
        : { text: String(part.text), strong: true }
    );
  }

  // hireView is a recognized project's plan presented as a hire: who the setup
  // hires (or assigns) for the folder, the one question whose answer becomes the
  // workspace goal, and the heading the plan's lines sit under. All of it is
  // written from the server's hire facts; null when the plan names no hire.
  // Home's card, the composer's card and the build pane show the same view.
  function hireView(plan, subject, { needs = [] } = {}) {
    const hire = plan?.hire;
    if (!hire || typeof hire !== 'object' || !HIRE_MODES.includes(hire.mode)) return null;
    const name = hire.mode === 'pending' ? '' : String(hire.agent_name || '').trim();
    const role = String(hire.role_label || '').trim();
    const model = String(hire.model || '').trim();
    let headline;
    if (name && hire.mode === 'bind')
      headline = segments(['Assign ', { text: name }, ' to ', { text: subject }]);
    else if (name) headline = segments(['Hire ', { text: name }, ' for ', { text: subject }]);
    else {
      const kind = role || 'project';
      const article = /^[aeiou]/i.test(kind) ? 'an' : 'a';
      headline = segments([`Hire ${article} ${kind} assistant for `, { text: subject }]);
    }
    const detail = name
      ? [role ? `${role} role` : '', model ? `uses ${model}` : 'no model yet']
          .filter(Boolean)
          .join(' · ')
      : `${role || 'Project'} role · named once its plugin is installed`;
    // A plan that installs or enables a plugin first says its other lines happen
    // once that is done.
    const waits =
      hire.mode === 'pending' ||
      (Array.isArray(needs) && needs.some(need => need && need.state && need.state !== 'ready'));
    const brief = String(plan.brief_default || '').trim();
    const monitoringTime = /^\d{2}:\d{2}$/.test(String(plan.monitoring?.time || ''))
      ? plan.monitoring.time
      : '';
    return {
      mode: hire.mode,
      confirmLabel:
        plan.optional_task === true
          ? hire.assigned_here === true
            ? 'Confirm setup'
            : hire.mode === 'bind'
              ? 'Assign & set up'
              : 'Hire & set up'
          : hire.mode === 'bind'
            ? 'Assign & start'
            : 'Hire & start',
      headline,
      agent: { initials: initialsOf(name || role || 'Agent'), detail },
      brief: brief
        ? {
            label: 'What should it do with it?',
            value: brief,
            hint: 'This becomes the workspace goal. You can change it later.'
          }
        : null,
      // An inbox folder's optional watching and daily catch-up, off unless the
      // user ticks it. The plan names the time; the choice travels in the
      // request, never in the plan.
      monitoring: monitoringTime
        ? {
            label: `Also watch this folder and run a daily catch-up scan at ${monitoringTime}`,
            hint: 'Ori notices new files after a short settle time and proposes them for review. Nothing is filed by itself.',
            line: 'Watching turned on'
          }
        : null,
      linesHeading: waits ? 'Also, once installed' : 'Also, in the background',
      adjust: null
    };
  }

  const entryChoices = new WeakMap();
  let entryChoiceID = 0;

  function entryChoiceView(plan) {
    const name = typeof plan?.entry_name === 'string' ? plan.entry_name : '';
    const candidates = [
      ...new Set(
        (Array.isArray(plan?.entry_candidates) ? plan.entry_candidates : [])
          .filter(value => typeof value === 'string' && value)
          .slice(0, 64)
      )
    ];
    return {
      visible: Boolean(name && (!candidates.length || candidates.includes(name))),
      name,
      candidates: candidates.length > 1 ? candidates : []
    };
  }

  // Native radios keep their identity across saves/renders, so arrow keys and
  // focus keep working. Filenames are text and values, never markup or links.
  function renderEntryChoice(container, plan, options = {}, doc = document) {
    if (!container) return;
    const view = entryChoiceView(options.readOnly ? { ...plan, entry_candidates: [] } : plan);
    container.hidden = !view.visible;
    if (!view.visible) return;
    const candidates = options.readOnly ? [] : view.candidates;
    const key = JSON.stringify(candidates);
    let state = entryChoices.get(container);
    if (!state || state.key !== key) {
      container.replaceChildren();
      state = { key, inputs: [], focused: null };
      if (candidates.length) {
        state.fieldset = doc.createElement('fieldset');
        state.fieldset.className = 'folder-entry-choice';
        const legend = doc.createElement('legend');
        legend.textContent = 'Which file is the song?';
        state.fieldset.append(legend);
        const group = `folder-entry-${++entryChoiceID}`;
        for (const name of candidates) {
          const label = doc.createElement('label');
          label.className = 'folder-entry-choice__option';
          const input = doc.createElement('input');
          input.type = 'radio';
          input.name = group;
          input.value = name;
          const text = doc.createElement('span');
          text.textContent = name;
          label.append(input, text);
          state.fieldset.append(label);
          state.inputs.push(input);
        }
        container.append(state.fieldset);
      } else {
        state.caption = doc.createElement('p');
        state.caption.className = 'folder-entry-choice__caption';
        container.append(state.caption);
      }
      entryChoices.set(container, state);
    }
    if (state.caption) {
      const missing =
        options.readOnly &&
        plan?.stop_reason === 'needs_choice' &&
        !plan?.entry_candidates?.includes(view.name);
      state.caption.textContent = `${missing ? 'Previously chose' : 'Opens'} ${view.name}`;
    }
    if (state.fieldset) {
      if (options.disabled && state.inputs.includes(doc.activeElement))
        state.focused = doc.activeElement;
      state.fieldset.disabled = Boolean(options.disabled);
      // Keep the native click/arrow selection visible while its save is busy.
      // The canonical reply wins when editing resumes, including on refusal.
      const selected = options.disabled && state.pending ? state.pending : view.name;
      if (!options.disabled) state.pending = '';
      for (const input of state.inputs) {
        input.checked = input.value === selected;
        input.onchange = () => {
          if (!options.disabled && input.checked && input.value !== view.name) {
            state.pending = input.value;
            options.onChange?.(input.value);
          }
        };
      }
      if (!options.disabled && state.focused) {
        if (
          !doc.activeElement ||
          doc.activeElement === doc.body ||
          state.inputs.includes(doc.activeElement)
        )
          state.focused.focus?.({ preventScroll: true });
        state.focused = null;
      }
    }
  }

  globalThis.OriFolderPlanLines = {
    entryChoiceView,
    renderEntryChoice,
    setupLinesView,
    satisfiedLinesView,
    handoffSections,
    handoffNames,
    handoffLineName,
    handoffLineParts,
    renderSetupLines,
    setupLineKindIcon,
    hireView
  };
})();
