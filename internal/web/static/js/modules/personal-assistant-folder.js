// Show me a folder — the assistant's first proactive act.
//
// The user points the assistant at a folder; the server looks at its shape
// (names, dates, kinds, project markers — never contents) and returns one
// explained offer. This module owns the chooser, the exploring scene and the
// offer card, which appear in the Home assistant drawer's conversation as the
// assistant's replies to "Explore a folder". It never sends a filesystem
// location — chips are identifiers the server resolves, and the native picker
// runs server-side.

import {
  lastSavedLabel,
  libraryOpenAction,
  libraryOpenChoices,
  libraryOpenRoute,
  openLibrarySong,
  RECENT_SONGS_QUERY,
  recentSongs,
  songFactsLabel,
  songLineText
} from './library-open.js';
import { FOLDER_CHIP_ICON, folderChooserView } from './personal-assistant-folder-chooser.js';
import { MODEL_SETTINGS_URL } from './setup-journey-account-steps.js';

// The chooser's render decision lives in its own module so the interview wizard
// can use it without mounting this card; it is re-exported for existing callers.
export { FOLDER_CHIP_ICON, folderChooserView };

const DIGEST_ENDPOINT = '/api/personal-assistant/folder-digest';

// FOLDER_QUEST_ACTION_URL is where the "Show your assistant a folder" mission
// card sends the user (show-folder-quest.js opens the chooser from it). The
// Quests widget matches a mission on it to render a pending offer inline;
// it knows no quest IDs.
export const FOLDER_QUEST_ACTION_URL = '/?quest=show-folder';

// folderActionAvailable reports whether "Show me a folder" may be offered:
// only for a hired assistant with a built HQ, active or paused (FR1, FR52).
export function folderActionAvailable(personalAssistant) {
  const state = String(personalAssistant?.state || '').trim();
  return state === 'active' || state === 'paused';
}

export function firstFolderPromptView(digest, available) {
  return {
    expand: available === true && digest?.prompt_first_folder === true,
    line: "Now let's explore a folder you're working in."
  };
}

// folderOfferWaiting reports whether an offer still needs the user: it has not
// been answered, or it was answered yes and its setup has not finished.
export function folderOfferWaiting(offer) {
  const status = String(offer?.status || '').trim();
  return Boolean(offer?.id) && (status === 'pending' || status === 'awaiting_outcome');
}

// folderPlacement says where the folder flow shows in the drawer. While it
// runs in the conversation ('thread') the chooser, the scene and the offer card
// are the assistant's replies there. An offer that was already waiting when the
// page loaded has no conversation around it, so its card goes in Needs you
// ('needs') and stays there while it is acted on. Otherwise nothing shows.
export function folderPlacement({ available, inThread, pinned, offer } = {}) {
  if (available !== true) return 'none';
  if (inThread === true) return 'thread';
  const id = String(offer?.id || '');
  return id && id === String(pinned || '') ? 'needs' : 'none';
}

// folderPinnedOffer is the offer to keep in Needs you after a digest read: one
// that is waiting, a set-up folder whose first look is still news (its click, a
// run, an answer, another try, or a result that just came in), or the one
// already placed there (the user may be part-way through acting on it). Nothing
// is pinned while the flow is in the conversation.
export function folderPinnedOffer({ inThread, pinned, offer } = {}) {
  const id = String(offer?.id || '');
  if (inThread === true || !id) return '';
  return folderOfferWaiting(offer) || folderFirstLookOpen(offer) || id === String(pinned || '')
    ? id
    : '';
}

// folderChipBusy: "Explore a folder" cannot be asked for again while a folder
// is being chosen or explored.
export function folderChipBusy({ inThread, chooserOpen, scanning, busy } = {}) {
  return inThread === true && (chooserOpen === true || scanning === true || busy === true);
}

// A view of what the server has actually observed. A folder graphic moving
// towards the portrait is only a metaphor: no files are moved or uploaded.
// Never derive counts or a blueprint from a proposed workspace plan.
//
// The scene is the assistant's reply once a folder has been chosen. While one
// is still being chosen the chooser is the whole reply, so there is no scene.
export function folderSceneView({
  scanning = false,
  failed = false,
  scanName = '',
  offer = null
} = {}) {
  const offered = folderOfferView(offer).visible;
  if (scanning) {
    return {
      visible: true,
      phase: 'scanning',
      label: `Exploring ${scanName || 'your folder'}…`,
      finds: []
    };
  }
  if (failed)
    return { visible: true, phase: 'error', label: 'I could not explore that folder.', finds: [] };
  if (offered) {
    const finds = [];
    const count = n => (Number.isSafeInteger(n) && n > 0 ? n : 0);
    if (count(offer.projects_count)) finds.push(plural(offer.projects_count, 'project'));
    if (count(offer.loose_files)) finds.push(plural(offer.loose_files, 'loose file'));
    if (!finds.length && offer.verdict === 'project' && offer.subject?.marker) {
      finds.push(String(offer.subject.marker));
    }
    const folder = String(offer.folder || '').trim() || 'your folder';
    return {
      visible: true,
      phase: 'found',
      label:
        offer.status === 'resolved' ? `${folder} is ready.` : `Here's what I noticed in ${folder}.`,
      finds
    };
  }
  return { visible: false, phase: 'choosing', label: '', finds: [] };
}

function segments(parts) {
  return parts.map(part =>
    typeof part === 'string'
      ? { text: part, strong: false }
      : { text: String(part.text), strong: true }
  );
}

function plural(n, noun) {
  const count = Number(n) || 0;
  return `${count} ${noun}${count === 1 ? '' : 's'}`;
}

// projectConfirmView is the card for a project subject: what the scan found
// ("Thesis looks like a LaTeX manuscript."), the plan as a question ("Set up
// Thesis as a Writing project workspace?"), and Set up / Adjust…. Set up has
// the assistant create the workspace itself when the server offers that
// (create_available); Adjust… opens the Create Workspace modal pre-filled,
// which is also Set up's whole path when the server does not. A confirm
// reached from a mixed or ambiguous offer gets Back instead of Not this one
// and Later, which belong to the offer it came from.
function projectConfirmView(offer, base, subject, remember, { back = false } = {}) {
  if (offer.capability && !back) return capabilityConfirmView(offer, base, subject);
  const marker = String(offer.subject?.marker || '').trim();
  // The plan names its blueprint only when that blueprint will be used; a
  // missing one is explained by the note instead.
  const label = String(offer.blueprint || '').trim()
    ? String(offer.blueprint_label || '').trim()
    : '';
  const note = String(offer.blueprint_note || '').trim();
  const headline = marker
    ? segments([{ text: subject }, ` looks like a ${marker}.`])
    : segments([{ text: subject }, ' looks like a project.']);
  let question = label
    ? `Set up ${subject} as a ${label} workspace?`
    : `Set up a workspace for ${subject}?`;
  if (note) question += ` ${note}`;
  const destination = folderSetupDestinationLabel(offer);
  if (destination) question += ` ${destination}`;
  if (remember)
    question += ` I will also remember that ${subject} is a project you are working on.`;
  const createAvailable = offer.create_available === true;
  const actions = createAvailable
    ? [
        {
          id: 'setup',
          label: 'Set up',
          style: 'primary',
          decision: 'yes',
          choice: 'project',
          create: true
        },
        {
          id: 'adjust',
          label: 'Adjust…',
          style: 'outline',
          decision: 'yes',
          choice: 'project',
          modal: true
        }
      ]
    : [
        {
          id: 'yes',
          label: 'Set up workspace',
          style: 'primary',
          decision: 'yes',
          choice: 'project',
          modal: true
        }
      ];
  if (back) actions.push({ id: 'back', label: 'Back', style: 'link', back: true });
  else {
    actions.push({ id: 'no', label: 'Not this one', style: 'outline', decision: 'no' });
    actions.push({ id: 'later', label: 'Later', style: 'link', decision: 'later' });
  }
  return { ...base, headline, question, actions, confirming: back };
}

// The states of one plan line while the server runs the setup.
const SETUP_LINE_STATES = ['waiting', 'working', 'done', 'failed'];

// setupLinesView normalizes the server's plan or run lines for rendering. Every
// field is text; an unknown state is shown as a neutral row, never trusted.
export function setupLinesView(lines) {
  return (Array.isArray(lines) ? lines : [])
    .filter(line => line && typeof line === 'object' && String(line.name || '').trim())
    .map(line => ({
      kind: String(line.kind || '').trim(),
      name: String(line.name).trim(),
      detail: String(line.detail || '').trim(),
      state: SETUP_LINE_STATES.includes(line.state) ? line.state : ''
    }));
}

const SETUP_STOP_COPY = {
  plan_changed:
    'What Set up would do now differs from the plan you approved, so I stopped before going further.',
  needs_pick: subject =>
    `Ori no longer has ${subject} open (the server was restarted). Pick the folder again to carry on.`,
  needs_choice: subject => `${subject} has more than one project file. Choose the one to set up.`,
  needs_model: 'The workspace and folder are set up. The agent needs a model before it can start.',
  install_failed:
    'The integration could not be installed or enabled, so nothing after it ran. If you install it from Plugins, Try again continues from there.',
  interrupted: 'Setup was interrupted before it finished.',
  consent_stale:
    'The workspace and folder are set up. Your shared assistant was approved for an older version of this blueprint, so I did not add it. Continue setup to choose the agent yourself.',
  assistant_missing:
    'The workspace and folder are set up. The shared assistant this Home used is gone, so I did not add one on my own. Continue setup to choose the agent yourself.',
  failed: 'A step did not finish.'
};

// A collection's run stops in the same ways; only what is already true differs.
const PORTFOLIO_STOP_COPY = {
  needs_model:
    'What finished is kept. The Home needs a model before its agents can be added; nothing was listed yet.',
  needs_pick: subject =>
    `Ori no longer has ${subject} open (the server was restarted). Pick the folder again to list it.`
};

const CONTINUE_SETUP = {
  id: 'resume',
  label: 'Continue setup',
  style: 'outline',
  journey: true
};

function stoppedActions(reason, setup) {
  const retry = { id: 'retry', label: 'Try again', style: 'primary', oneCard: true, retry: true };
  switch (reason) {
    case 'plan_changed':
    case 'consent_stale':
    case 'assistant_missing':
      return [{ ...CONTINUE_SETUP, style: 'primary' }];
    case 'needs_pick':
      return [
        { id: 'repick', label: 'Pick it again', style: 'primary', repick: true },
        CONTINUE_SETUP
      ];
    case 'needs_choice':
      return [
        ...(Array.isArray(setup.entry_candidates) ? setup.entry_candidates : [])
          .map(name => String(name || '').trim())
          .filter(Boolean)
          .map((name, index) => ({
            id: `choose-${index}`,
            label: name,
            style: 'outline',
            oneCard: true,
            entry: name
          })),
        CONTINUE_SETUP
      ];
    case 'needs_model':
      return [
        { id: 'model', label: 'Set up a model', style: 'primary', href: MODEL_SETTINGS_URL },
        { ...retry, style: 'outline' },
        CONTINUE_SETUP
      ];
    default:
      return [retry, CONTINUE_SETUP];
  }
}

// setupRunView is the card while the server runs (or has stopped) a one-card
// setup: the plan lines with a state each, one status line, and — only when
// stopped — one plain sentence saying what finished and what is needed. A raw
// stop reason code is never shown.
export function setupRunView(setup, subject, { portfolio = false } = {}) {
  const lines = setupLinesView(setup?.lines);
  const status = String(setup?.status || 'running');
  if (status !== 'stopped') {
    const working = lines.find(line => line.state === 'working');
    return {
      status: status === 'done' ? 'running' : status,
      lines,
      statusLine: working ? `Working on: ${working.name}` : 'Starting…',
      question: `Setting up ${subject}…`,
      actions: []
    };
  }
  const reason = String(setup?.stop_reason || 'failed');
  const finished = lines.filter(line => line.state === 'done').length;
  const progress = lines.length ? `${finished} of ${lines.length} steps finished. ` : '';
  let sentence =
    (portfolio && PORTFOLIO_STOP_COPY[reason]) || SETUP_STOP_COPY[reason] || SETUP_STOP_COPY.failed;
  if (typeof sentence === 'function') sentence = sentence(subject);
  return {
    status,
    lines,
    statusLine: '',
    question: `${progress}${sentence}`,
    actions: stoppedActions(reason, setup || {})
  };
}

// setupModalView is the run pop-up: the same run the card shows, framed so the
// user can see the assistant is working — a phase (running, stopped, done), a
// step count, a percentage, and the lines. Pure data; the DOM is drawn below.
// It is not visible for an offer with no one-card run.
//
// A Home run that finished ends on the songs saved most recently (`songs`, the
// library's rows sorted by last_saved, and `total`, the library's song count):
// each with an Open, the receipt folded under "What I set up". With no such
// song the done screen is exactly the receipt and the Home link.
export function setupModalView(offer, { songs = [], total = 0, now = new Date() } = {}) {
  if (!offer) return { visible: false };
  const receipt = folderReceiptView(offer);
  // A settled offer no longer carries its run, only its receipt; the pop-up that
  // watched the run still ends on it. Without either there is nothing to show.
  if (!offer.setup && !receipt.visible) return { visible: false };
  const folder = String(offer.folder || '').trim() || 'this project';
  const subject = String(offer.subject?.name || '').trim() || folder;
  const run = setupRunView(offer.setup, subject, { portfolio: Boolean(offer.portfolio) });
  const steps = run.lines.length;
  const finished = run.lines.filter(line => line.state === 'done').length;
  if (receipt.visible) {
    // The run is over, so every line it reached is done, except a step that
    // was allowed to fail without stopping it (the Home's profile): that one
    // keeps saying it did not finish, and is not counted.
    const settled = run.lines.map(line => ({
      ...line,
      state: line.state === 'failed' ? 'failed' : line.state ? 'done' : ''
    }));
    const unfinished = settled.filter(line => line.state === 'failed').length;
    const done = {
      visible: true,
      phase: 'done',
      eyebrow: 'All set',
      title: receipt.home ? `${receipt.homeName} is ready` : `${subject} is ready`,
      status: receipt.home
        ? 'Here is what I set up. Open a project from the library when you want to work on it.'
        : 'Here is what I set up. The first task starts when you open the workspace.',
      count: steps ? `${steps - unfinished} of ${steps} steps finished` : '',
      percent: 100,
      lines: settled,
      profile: receipt.profile || null,
      receiptRows: receipt.rows,
      route: receipt.route,
      openLabel: receipt.openLabel,
      actions: []
    };
    const picks = receipt.home ? recentSongs(songs) : [];
    if (!picks.length) return done;
    const library = plural(Math.max(Number(total) || 0, picks.length), 'song');
    return {
      ...done,
      title: 'Pick a song to start with',
      status: `${receipt.homeName} is ready with ${library}. These are the ones you saved most recently.`,
      songs: picks.map(row => {
        const name = String(row.name || '').trim() || 'This song';
        return {
          id: String(row.id),
          name,
          saved: lastSavedLabel(row.last_saved_at, now),
          facts: songFactsLabel(row.facts),
          ariaLabel: libraryOpenAction({ ...row, name }, false).ariaLabel
        };
      }),
      openLabel: `Browse all ${library}`
    };
  }
  const stopped = run.status === 'stopped';
  return {
    visible: true,
    phase: stopped ? 'stopped' : 'running',
    eyebrow: stopped ? 'Setup paused' : 'Your assistant is working',
    title: stopped ? `${subject} needs you` : `Setting up ${subject}`,
    // The count line already says how many steps finished.
    status: stopped ? run.question.replace(/^\d+ of \d+ steps finished\. /, '') : run.statusLine,
    count: steps ? `${finished} of ${steps} steps finished` : '',
    percent: steps ? Math.round((finished / steps) * 100) : 0,
    lines: run.lines,
    receiptRows: [],
    route: '',
    openLabel: '',
    actions: run.actions
  };
}

// Display-only canonical disclosure. The browser never sends this name or any
// witness fields as a destination; confirmation sends the plan digest only.
export function folderSetupDestinationLabel(offer) {
  const destination =
    offer?.status === 'resolved' && offer?.outcome?.parent
      ? offer.outcome.parent
      : offer?.setup
        ? offer.setup.destination || offer.destination
        : offer?.destination || offer?.plan?.destination;
  if (!destination) return '';
  if (destination.status === 'standalone') return 'Separate project without a parent Home.';
  const name = String(destination.name || '').trim();
  if (!name || !['existing', 'new'].includes(destination.status)) return '';
  const operation =
    offer?.operation === 'link_supporting_folder'
      ? 'supporting folder'
      : offer?.portfolio
        ? 'collection in its library'
        : 'separate project';
  const kind =
    destination.kind === 'project' ? 'Project' : destination.kind === 'group' ? 'Group' : 'Home';
  return `Destination: ${destination.status === 'new' ? 'new ' : ''}${kind} “${name}” · ${operation}.`;
}

// A reviewed capability rides the same result card. With a plan from the server
// the card is the one consent: Set up runs the whole setup on the server and
// Adjust… opens the step-by-step journey. Without one it opens the existing
// install → project-setup journey. It never uses the blank-workspace creator.
function capabilityConfirmView(offer, base, subject) {
  const capability = offer.capability;
  const portfolio = offer.portfolio;
  // A collection rides the same one-consent plan and run as a single project.
  const run = offer.setup
    ? setupRunView(offer.setup, subject, { portfolio: Boolean(portfolio) })
    : null;
  const planLines = !run ? setupLinesView(offer.plan?.lines) : [];
  const hasPlan = planLines.length > 0;
  const continuing = base.status === 'awaiting_outcome';
  const decline = {
    id: 'no',
    label: String(capability.decline_label || 'No thanks'),
    style: 'outline',
    decision: 'no'
  };
  const later = { id: 'later', label: 'Later', style: 'link', decision: 'later' };
  let actions;
  if (run) actions = run.actions;
  else if (continuing && hasPlan)
    // Adjust… was pressed earlier: the one-click plan stays, beside the journey.
    actions = [
      { id: 'setup', label: 'Set up', style: 'primary', oneCard: true },
      { id: 'resume', label: 'Continue setup', style: 'outline', journey: true }
    ];
  else if (continuing)
    actions = [{ id: 'resume', label: 'Continue setup', style: 'primary', journey: true }];
  else if (base.decided) actions = [];
  else if (hasPlan)
    actions = [
      {
        id: 'setup',
        // The plan above says what happens, so the button is the plain verb.
        label: 'Set up',
        style: 'primary',
        oneCard: true
      },
      { id: 'adjust', label: 'Adjust…', style: 'outline', journey: true },
      decline,
      later
    ];
  else
    actions = [
      {
        id: 'setup',
        label: String(capability.accept_label || 'Set up'),
        style: 'primary',
        journey: true
      },
      decline,
      later
    ];
  const headline = portfolio
    ? segments([{ text: subject }, ` has ${portfolio.projects} music projects.`])
    : segments([{ text: subject }, ` looks like a ${capability.recognized} project.`]);
  let question = portfolio
    ? String(capability.question || `Set up a ${capability.workspace}?`)
    : `Set up ${subject} as a ${capability.workspace}?`;
  if (hasPlan) question += ' Here is everything Set up will do:';
  if (run) question = run.question;
  else if (capability.revived)
    question = `You said no before, but this is a whole collection now. ${question}`;
  const destination = folderSetupDestinationLabel(offer);
  if (destination) question += ` ${destination}`;
  return {
    ...base,
    headline,
    question,
    // The plan lists every consequence, so the pre-setup install promise would
    // only repeat it; a run shows its own progress instead.
    capabilityDetail: hasPlan || run ? '' : String(capability.integration || ''),
    reason: run ? '' : String(capability.evidence || base.reason),
    actions,
    resume: continuing,
    plan: hasPlan ? { lines: planLines, digest: String(offer.plan?.digest || '') } : null,
    setup: run
  };
}

// folderOfferView is the whole render decision for one offer (FR22). Every
// string comes from the verdict and the server's counts and names; the
// reason line is always present. options.confirm ('project' or 'tidy') shows
// the plan card for a mixed or ambiguous offer instead of its question.
export function folderOfferView(offer, options = {}) {
  if (!offer || typeof offer !== 'object') return { visible: false };
  const verdict = String(offer.verdict || '').trim();
  const folder = String(offer.folder || '').trim() || 'that folder';
  const subject = String(offer.subject?.name || '').trim() || folder;
  const status = String(offer.status || '').trim();
  const reason = String(offer.reason || '').trim();
  const remember = offer.remember === true;
  const decided = status !== 'pending' && status !== 'closed';
  // Which plan is being confirmed on the card: 'project' or 'tidy', or ''.
  const requested =
    options?.confirm === true || options?.confirmProject === true
      ? 'project'
      : String(options?.confirm || '');
  const confirm = decided ? '' : requested;
  const base = {
    visible: true,
    verdict,
    status,
    reason,
    decided,
    confirming: false,
    needsPick: offer.needs_pick === true
  };
  const view = verdictView(offer, base, { verdict, folder, subject, remember, confirm });
  if (
    status === 'awaiting_outcome' &&
    offer?.capability &&
    (!offer.portfolio || view.plan) &&
    !view.setup
  ) {
    if (view.plan) {
      view.question =
        'Project setup has not finished. Set up does what is left in one go, or continue step by step. Here is what is left:';
      view.capabilityDetail = '';
    } else {
      view.question = 'Project setup has not finished. Continue to review its current steps.';
      view.capabilityDetail =
        'Opening setup checks its current steps; it does not by itself create another workspace or enable live project control.';
    }
  }
  if (
    status === 'resolved' &&
    (offer?.outcome?.kind === 'project' || offer?.outcome?.kind === 'home') &&
    folderReceiptView(offer).visible
  ) {
    view.question = "Here's what I set up:";
    view.reason = '';
    view.capabilityDetail = ''; // the pre-setup install promise is no longer true
  }
  // A yes needs the folder's path, and a dialog-chosen folder is held in
  // memory only: after a server restart the card asks for the folder again
  // before offering anything a yes would need.
  if (view.visible && ['changed', 'unavailable'].includes(offer.destination_status)) {
    const destination = folderSetupDestinationLabel(offer);
    return {
      ...view,
      plan: null,
      actions: [],
      question: `${destination ? `${destination} ` : ''}${
        offer.destination_status === 'changed'
          ? 'The reviewed destination has changed.'
          : 'The reviewed destination is unavailable.'
      } Use Review workspace setup to refresh the review. Your draft is unchanged.`
    };
  }
  if (view.visible && view.needsPick && status === 'awaiting_outcome') {
    return {
      ...repickView(view, subject),
      actions: [{ id: 'repick', label: 'Pick it again', style: 'primary', repick: true }]
    };
  }
  if (view.visible && view.needsPick && !view.decided) return repickView(view, subject);
  if (view.visible && !view.setup && !view.plan && offer.setup_unavailable_reason) {
    const reasons = {
      integration_unavailable: 'integration readiness could not be verified',
      home_provider_unavailable: 'Home-provider readiness could not be verified',
      destination_unavailable: 'the destination could not be verified',
      setup_unavailable: 'its prerequisites could not be verified'
    };
    view.capabilityDetail = `One-click setup is unavailable: ${reasons[offer.setup_unavailable_reason] || reasons.setup_unavailable}. Step-by-step setup still requires its own review and confirmation.`;
  }
  if (view.visible && status === 'pending' && offer.operation === 'link_supporting_folder') {
    return {
      ...view,
      question: `Link ${subject} as a supporting folder? ${folderSetupDestinationLabel(offer)} This grants read access to this folder only. The primary project entry, blueprint, mode, agents and tasks stay unchanged.`,
      capabilityDetail: '',
      plan: null,
      actions: offer.create_available
        ? [
            {
              id: 'setup',
              label: 'Link supporting folder',
              style: 'primary',
              decision: 'yes',
              choice: 'project',
              create: true
            }
          ]
        : []
    };
  }
  return view;
}

// repickView keeps what the scan said and swaps the yes for "Pick it again".
function repickView(view, subject) {
  return {
    ...view,
    confirming: false,
    question: `Ori no longer has ${subject} open (the server was restarted). Pick the folder again to carry on.`,
    actions: [
      { id: 'repick', label: 'Pick it again', style: 'primary', repick: true },
      { id: 'no', label: 'Not this one', style: 'outline', decision: 'no' },
      { id: 'later', label: 'Later', style: 'link', decision: 'later' }
    ]
  };
}

// tidyConfirmView is the plan for a tidy: what Ori will set up and how it
// keeps its hands off the files. Set up runs it and then shows the setup as
// it happened (the assistant-led setup card's walkthrough); Adjust… opens the
// ordinary File Janitor creator, whose own wizard asks for the folder.
function tidyConfirmView(offer, base, folder) {
  return {
    ...base,
    confirming: true,
    headline: segments(['Set up File Janitor for ', { text: folder }, '?']),
    question:
      'Ori will create a File Janitor workspace with a File Curator, watch the folder while paused, scan it once and propose moves — nothing moves until you approve a batch.',
    actions: [
      {
        id: 'setup',
        label: 'Set up',
        style: 'primary',
        decision: 'yes',
        choice: 'tidy',
        walkthrough: true
      },
      { id: 'adjust', label: 'Adjust…', style: 'outline', manual: 'file-janitor' },
      { id: 'back', label: 'Back', style: 'link', back: true }
    ]
  };
}

function verdictView(offer, base, { verdict, folder, subject, remember, confirm }) {
  if (confirm === 'tidy' && ['dump', 'mixed', 'ambiguous'].includes(verdict)) {
    return tidyConfirmView(offer, base, folder);
  }
  switch (verdict) {
    case 'project':
      return projectConfirmView(offer, base, subject, remember);
    case 'dump':
      return {
        ...base,
        headline: segments([
          { text: folder },
          ` has ${plural(offer.loose_files, 'loose file')} of ${plural(offer.loose_kinds, 'kind')}.`
        ]),
        question: 'Want me to tidy it? I will propose moves and you approve each batch.',
        actions: [
          // The tidy's plan is confirmed on its own card first.
          { id: 'yes', label: 'Tidy it', style: 'primary', confirm: 'tidy' },
          { id: 'no', label: 'Not this one', style: 'outline', decision: 'no' },
          { id: 'later', label: 'Later', style: 'link', decision: 'later' }
        ]
      };
    case 'mixed':
      if (confirm === 'project')
        return projectConfirmView(offer, base, subject, remember, { back: true });
      return {
        ...base,
        headline: segments([
          'I see ',
          { text: plural(offer.projects_count, 'project') },
          ' and ',
          { text: plural(offer.loose_files, 'loose file') },
          ` in ${folder}.`
        ]),
        question: 'Start with a project, or a tidy?',
        actions: [
          // Each plan is confirmed on its own card first.
          { id: 'project', label: `Start with ${subject}`, style: 'primary', confirm: 'project' },
          { id: 'tidy', label: 'Tidy the loose files', style: 'outline', confirm: 'tidy' },
          { id: 'later', label: 'Later', style: 'link', decision: 'later' }
        ]
      };
    case 'ambiguous':
      if (confirm === 'project')
        return projectConfirmView(offer, base, subject, remember, { back: true });
      return {
        ...base,
        headline: segments(['I am not sure what ', { text: subject }, ' is.']),
        question: 'Is this a project you work in, or a folder to tidy?',
        actions: [
          { id: 'project', label: "It's a project", style: 'primary', confirm: 'project' },
          { id: 'tidy', label: 'Tidy it', style: 'outline', confirm: 'tidy' },
          { id: 'no', label: 'Neither', style: 'link', decision: 'no' }
        ]
      };
    case 'empty':
      return {
        ...base,
        headline: segments(['Nothing in ', { text: folder }, ' needs me yet.']),
        question: 'Try Downloads, or pick another folder.',
        actions: [{ id: 'another', label: 'Show another folder', style: 'outline', open: true }]
      };
    case 'declined':
      return {
        ...base,
        headline: segments(['You asked me not to ask about ', { text: folder }, '.']),
        question: 'Pick another folder whenever you like.',
        actions: [{ id: 'another', label: 'Show another folder', style: 'outline', open: true }]
      };
    default:
      return { visible: false };
  }
}

// A receipt is read only from the resolved server outcome; the workspace name
// and route are never reconstructed from the folder name or blueprint plan.
// options.busy and options.message are a first-look click of ours in flight and
// what it was told; they only change the first look's part of the view.
export function folderReceiptView(offer, options = {}) {
  if (offer?.status === 'resolved' && offer?.outcome?.kind === 'home')
    return homeReceiptView(offer);
  if (offer?.status !== 'resolved' || offer?.outcome?.kind !== 'project') return { visible: false };
  const rows = Array.isArray(offer?.outcome?.receipt) ? offer.outcome.receipt : [];
  const workspace = rows.find(row => row.kind === 'workspace');
  const route = resolvedRouteFor(offer);
  const homeRoute = String(offer?.outcome?.home_route || '').trim();
  const verifiedHomeRoute =
    /^\/workspaces\/[a-z0-9][a-z0-9-]*\/assistant#projectLibraryPanel$/.test(homeRoute)
      ? homeRoute
      : '';
  // A reviewed plugin quest records its exact child/Home route but does not
  // create the older folder-link receipt rows. Route-only follow-up is inert.
  if (
    !rows.length &&
    !(
      offer?.capability?.setup_source === 'plugin' &&
      /^\/workspaces\/[a-z0-9][a-z0-9-]*\/?$/.test(route) &&
      verifiedHomeRoute
    )
  )
    return { visible: false };
  const openLabel = `Open ${String(workspace?.name || offer?.subject?.name || 'workspace').trim()}`;
  // The stored receipt is from the moment of setup. Where the first look stands
  // now is read live, so its row and the card's buttons follow it.
  const firstLook = folderFirstLookView(offer?.first_task, {
    openLabel,
    route,
    busy: options.busy,
    message: options.message
  });
  const liveDetail = firstLook.visible ? String(offer.first_task.detail || '').trim() : '';
  return {
    visible: true,
    rows: rows.map(row => ({
      kind: String(row.kind || ''),
      name: String(row.name || ''),
      detail: row.kind === 'task' && liveDetail ? liveDetail : String(row.detail || '')
    })),
    route,
    homeRoute: verifiedHomeRoute,
    openLabel,
    ...(firstLook.visible ? { firstLook } : {})
  };
}

// The states a folder's first look can be in, as the server reports them.
const FIRST_LOOK_STATES = ['seeded', 'running', 'waiting', 'finished', 'failed'];
// The full report is the task's ticket in its workspace, and nothing else.
const FIRST_LOOK_REPORT = /^\/workspaces\/[a-z0-9][a-z0-9-]*\?ticket=[A-Za-z0-9_-]+$/;
const FIRST_LOOK_NOT_STARTED = 'The first look could not start. Try again.';

// What Start first look costs, said beside the button before it is pressed.
export const FIRST_LOOK_HINT = 'Spends model tokens for one read-only look at the folder.';

// folderFirstLookOpen reports whether a set-up folder's first look still has
// something to show on Home: it is waiting for its click, running, waiting on
// the user, did not finish, or has a result. The server only sends the block
// while that is so.
export function folderFirstLookOpen(offer) {
  return (
    String(offer?.status || '') === 'resolved' &&
    Boolean(offer?.id) &&
    FIRST_LOOK_STATES.includes(String(offer?.first_task?.state || ''))
  );
}

// firstLookStartMessage is the sentence for a start the server refused (FR15).
// The codes are the start endpoint's own. One that only means "it is already
// going" has no sentence: the card is refreshed instead.
export function firstLookStartMessage(reason, workspaceName) {
  const name = String(workspaceName || '').trim() || 'the workspace';
  switch (String(reason || '').trim()) {
    case 'setup_wizard_opening':
      return `Open ${name} to finish its setup first.`;
    case 'unassigned':
      return `The first task has no agent yet. Open ${name} to assign one.`;
    case 'no_model':
      return 'Add a model in Settings to run the first look.';
    case 'local_activation_required':
      return `Open ${name} and activate it on this computer.`;
    case 'already_consumed':
    case 'not_pending':
      return '';
    default:
      return FIRST_LOOK_NOT_STARTED;
  }
}

// folderFirstLookView is the render decision for a folder's first look on its
// receipt (FR13, FR15, FR18): one primary action, the workspace as the other,
// and one line of status. Nothing is inferred: the state, whether a click would
// start it, the sentence, the excerpt and the report link all come from the
// server. `busy` is a click of ours that is still in flight; `message` is what
// that click was told.
export function folderFirstLookView(look, options = {}) {
  const state = String(look?.state || '');
  if (!look || typeof look !== 'object' || !FIRST_LOOK_STATES.includes(state)) {
    return { visible: false };
  }
  const openLabel = String(options.openLabel || '').trim() || 'Open workspace';
  const local = value => {
    const target = String(value || '').trim();
    return target.startsWith('/') && !target.startsWith('//') ? target : '';
  };
  const route = local(options.route) || local(look.workspace_route);
  const open = style => (route ? [{ id: 'open', label: openLabel, style, href: route }] : []);
  const busy = options.busy === true;
  const said = String(options.message || '').trim() || String(look.message || '').trim();
  const base = {
    visible: true,
    state,
    status: '',
    message: '',
    hint: '',
    excerpt: '',
    actions: []
  };

  switch (state) {
    case 'seeded': {
      if (look.can_start === true) {
        return {
          ...base,
          message: String(options.message || '').trim(),
          hint: FIRST_LOOK_HINT,
          actions: [
            {
              id: 'start-first-look',
              label: busy ? 'Starting…' : 'Start first look',
              style: 'primary',
              start: true,
              busy
            },
            ...open('outline')
          ]
        };
      }
      // It cannot start yet. The fix is in the workspace, except for a missing
      // model, which is fixed in Settings.
      const fix =
        look.reason === 'no_model'
          ? [{ id: 'model', label: 'Open Settings', style: 'primary', href: MODEL_SETTINGS_URL }]
          : [];
      return {
        ...base,
        message: said || firstLookStartMessage(look.reason, look.workspace_name),
        actions: [...fix, ...open(fix.length ? 'outline' : 'primary')]
      };
    }
    case 'running':
      return { ...base, status: 'Running…', actions: open('primary') };
    case 'waiting':
      return {
        ...base,
        message: said || 'The first look is waiting for your answer.',
        actions: open('primary')
      };
    case 'finished': {
      const report = String(look.ticket_route || '').trim();
      const link = FIRST_LOOK_REPORT.test(report)
        ? [{ id: 'report', label: 'Open the full report', style: 'primary', href: report }]
        : [];
      return {
        ...base,
        excerpt: String(look.result_excerpt || '').trim(),
        actions: [...link, ...open(link.length ? 'outline' : 'primary')]
      };
    }
    default:
      return {
        ...base,
        message: said || 'The first look did not finish. Try again.',
        actions: [
          {
            id: 'retry-first-look',
            label: busy ? 'Starting…' : 'Try again',
            style: 'primary',
            retry: true,
            busy
          },
          ...open('outline')
        ]
      };
  }
}

// homeReceiptView is a one-card collection setup's receipt: the Home, the
// agents it added, the listing and the shared assistant. Open lands on the
// Home's library (no folder handoff: the run already connected and listed it).
// A collection resolved step by step carries no rows and shows the old note.
// profileReceiptLine is the one quiet line a finished Home setup shows about
// the Home's profile: the package's own title, what the profile holds, and a
// Review link to its card on the Home. The words and the route are the
// server's; a route that is not that card is dropped. It blocks nothing.
export function profileReceiptLine(rows) {
  const row = (Array.isArray(rows) ? rows : []).find(entry => entry?.kind === 'profile');
  const name = String(row?.name || '').trim();
  const detail = String(row?.detail || '').trim();
  if (!name || !detail) return null;
  const route = String(row.route || '').trim();
  return {
    text: `${name}: ${detail}`,
    route: /^\/workspaces\/[a-z0-9][a-z0-9-]*\/assistant#homeProfilePanel$/.test(route)
      ? route
      : '',
    linkLabel: 'Review'
  };
}

function homeReceiptView(offer) {
  const rows = Array.isArray(offer?.outcome?.receipt) ? offer.outcome.receipt : [];
  const home = rows.find(row => row?.kind === 'home');
  const route = String(home?.route || '').trim();
  if (!home || !/^\/workspaces\/[a-z0-9][a-z0-9-]*\/assistant#projectLibraryPanel$/.test(route))
    return { visible: false };
  const homeName = String(home.name || '').trim() || 'your Home';
  // The Home's workspace ID, read from the server's outcome, never from the route.
  const homeID = String(offer?.outcome?.workspace_id || '').trim();
  return {
    visible: true,
    home: true,
    homeName,
    profile: profileReceiptLine(rows),
    homeID: homeID.length <= 160 ? homeID : '',
    rows: rows.map(row => ({
      kind: String(row.kind || ''),
      name: String(row.name || ''),
      detail: String(row.detail || '')
    })),
    route,
    homeRoute: '',
    openLabel: `Open ${homeName}`
  };
}

// folderOutcomeNote is the line shown under a decided offer.
export function folderOutcomeNote(offer) {
  const status = String(offer?.status || '').trim();
  const subject = String(offer?.subject?.name || '').trim() || 'that folder';
  switch (status) {
    case 'awaiting_outcome':
      if (offer?.capability)
        return `Continue reviewed setup for ${subject}; no folder was changed.`;
      return offer?.choice === 'tidy'
        ? `Setting up a tidy of ${subject}…`
        : `Setting up a workspace for ${subject}…`;
    case 'later':
      return 'I will ask again in a week.';
    case 'declined':
      return `I will not ask about ${subject} again.`;
    case 'resolved':
      if (offer?.outcome?.kind === 'home') {
        return offer.outcome.existing
          ? 'Your Music Production Home has this collection waiting in its library. Nothing was moved, linked, or scanned.'
          : 'Music Production Home is ready. Your project folders were not moved or linked.';
      }
      if (offer?.outcome?.kind === 'tidy') {
        return (
          String(offer?.outcome?.note || '').trim() ||
          `File Janitor is set up for ${subject}; its first proposals are ready to review.`
        );
      }
      if (offer?.outcome?.blueprint && String(offer?.blueprint_label || '').trim()) {
        return `${subject} is set up as a ${String(offer.blueprint_label).trim()} workspace.`;
      }
      return `The workspace for ${subject} is ready.`;
    default:
      return '';
  }
}

// folderProjectModalOptions is what a project yes hands the Create Workspace
// modal (FR28): the folder's name, its preferred blueprint (empty means the
// blank workspace), the fallback note, and the offer identifier the server
// needs to attach the folder afterwards. The folder's path is not here.
export function folderProjectModalOptions(offer) {
  return {
    entryPoint: 'folder_digest',
    name: String(offer?.subject?.name || '').trim(),
    blueprint: String(offer?.blueprint || '').trim(),
    blueprintNote: String(offer?.blueprint_note || '').trim(),
    folderOfferId: String(offer?.id || '').trim()
  };
}

const state = {
  reviewSlot: null,
  reviewGeneration: 0,
  workspaceName: '',
  editingName: false,
  digest: null,
  offer: null,
  busy: false,
  scanning: false,
  contextProgress: null,
  scanFailed: false,
  freshScan: false,
  scanName: '',
  chooserOpen: false,
  available: false,
  handOver: false,
  prompting: false,
  // The flow is running in the conversation: the user asked for it, or the
  // assistant opened it with its one first-folder prompt.
  inThread: false,
  // The user asked, so their request is shown above the assistant's replies.
  userAsked: false,
  // The offer placed in Needs you because it was waiting when the page loaded.
  pinned: '',
  // The plan being confirmed on the card before anything is decided:
  // 'project', 'tidy', or ''.
  confirm: '',
  // What the assistant is doing right now, shown under the card while a
  // setup runs.
  progress: '',
  // A Start first look (or Try again) click that is still in flight, and what
  // the last one was told when it did not start.
  firstLookBusy: false,
  firstLookMessage: ''
};

// announceOffer tells the mission card (progression-widget.js) what the
// chooser shows, so the two render the same offer in the same state. It also
// carries the first look being followed, which may belong to another folder
// than the offer on the card.
function announceOffer() {
  if (typeof document === 'undefined') return;
  document.dispatchEvent(
    new CustomEvent('personal-assistant:folder-offer', {
      detail: {
        offer: state.offer,
        confirm: state.confirm,
        firstLook: currentFirstLook(),
        firstLookBusy: state.firstLookBusy,
        firstLookMessage: state.firstLookMessage
      }
    })
  );
}

const SETUP_LINE_GLYPHS = {
  waiting: ['○', 'Waiting'],
  working: ['◔', 'In progress'],
  done: ['✓', 'Done'],
  failed: ['!', 'Did not finish']
};

// renderSetupLines draws a plan or a run's lines into a list. The state is a
// glyph and a visually hidden word, never colour alone. Used by the Home panel
// card and the mission card, so both show the same lines.
export function renderSetupLines(list, lines, doc = document) {
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

const RECEIPT_LABELS = {
  workspace: ['Workspace', '◈'],
  folder: ['Folder', '▤'],
  blueprint: ['Blueprint', '✦'],
  agent: ['Agent', '●'],
  task: ['First task', '✓'],
  home: ['Home', '⌂'],
  library: ['Library', '☰'],
  assistant: ['Shared assistant', '●'],
  profile: ['Profile', '◎']
};

// renderProfileLine draws the done screen's line about the Home's profile.
function renderProfileLine(node, line, doc = document) {
  if (!node) return;
  node.replaceChildren();
  node.hidden = !line;
  if (!line) return;
  node.append(doc.createTextNode(line.text));
  if (line.route) {
    const link = doc.createElement('a');
    link.href = line.route;
    link.textContent = line.linkLabel;
    node.append(doc.createTextNode(' '), link);
  }
}

// renderReceiptRows draws the receipt's rows (what Set up made) into a list.
// Shared by the card and the run pop-up so both say the same thing.
function renderReceiptRows(list, rows, doc = document) {
  if (!list) return;
  list.replaceChildren();
  rows.forEach(row => {
    const li = doc.createElement('li');
    const [label, glyph] = RECEIPT_LABELS[row.kind] || ['Set up', '•'];
    li.dataset.kind = RECEIPT_LABELS[row.kind] ? row.kind : 'other';
    const icon = doc.createElement('span');
    icon.className = 'pa-folder__receipt-icon';
    icon.setAttribute('aria-hidden', 'true');
    icon.textContent = glyph;
    li.append(
      icon,
      doc.createTextNode(`${label} · ${row.name}${row.detail ? ` (${row.detail})` : ''}`)
    );
    list.append(li);
  });
}

function elements() {
  const root = document.getElementById('personalAssistantFolder');
  if (!root) return null;
  return {
    root,
    thread: document.getElementById('personalAssistantThread'),
    activity: document.getElementById('personalAssistantActivityMount'),
    needs: document.getElementById('personalAssistantNeedsYouCards'),
    request: document.getElementById('personalAssistantFolderRequest'),
    chooser: document.getElementById('personalAssistantFolderChooser'),
    scene: document.getElementById('personalAssistantFolderScene'),
    sceneAvatar: document.getElementById('personalAssistantFolderSceneAvatar'),
    sceneLabel: document.getElementById('personalAssistantFolderSceneLabel'),
    sceneFinds: document.getElementById('personalAssistantFolderSceneFinds'),
    chips: document.getElementById('personalAssistantFolderChips'),
    title: document.getElementById('personalAssistantFolderTitle'),
    note: document.getElementById('personalAssistantFolderNote'),
    status: document.getElementById('personalAssistantFolderStatus'),
    offer: document.getElementById('personalAssistantFolderOffer'),
    headline: document.getElementById('personalAssistantFolderOfferHeadline'),
    question: document.getElementById('personalAssistantFolderOfferQuestion'),
    nameLabel: document.getElementById('personalAssistantFolderWorkspaceNameLabel'),
    workspaceName: document.getElementById('personalAssistantFolderWorkspaceName'),
    reason: document.getElementById('personalAssistantFolderOfferReason'),
    capability: document.getElementById('personalAssistantFolderOfferCapability'),
    why: document.querySelector('#personalAssistantFolderOffer .pa-folder__why'),
    receipt: document.getElementById('personalAssistantFolderReceipt'),
    result: document.getElementById('personalAssistantFolderFirstLookResult'),
    plan: document.getElementById('personalAssistantFolderPlan'),
    actions: document.getElementById('personalAssistantFolderOfferActions'),
    offerNote: document.getElementById('personalAssistantFolderOfferNote'),
    error: document.getElementById('personalAssistantFolderOfferError')
  };
}

function setText(el, text, hideWhenEmpty = true) {
  if (!el) return;
  el.textContent = text || '';
  if (hideWhenEmpty) el.hidden = !text;
}

function showStatus(message) {
  const els = elements();
  setText(els?.status, message);
}

function showError(message) {
  const els = elements();
  setText(els?.error, message);
  runModal.error = String(message || '');
  renderRunModal();
}

// The run pop-up: opened by pressing Set up (or Try again), it follows the same
// offer the card does. Closing it never stops the run; the card keeps showing
// progress. `wanted` is true only while the user has it open, so a page that
// loads onto a run in progress does not pop a dialog over what they were doing.
const runModal = { wanted: false, bound: false, error: '' };

// The last screen of a Home run: the songs saved most recently, read once when
// the open pop-up first reaches done, for the offer it watched. Nothing here
// is stored; a pop-up closed before done, or a page loaded later, never asks.
const runSongs = {
  offerID: '',
  homeID: '',
  status: '', // '' | 'loading' | 'ready' | 'failed'
  rows: [],
  total: 0,
  opening: '', // the song being opened; every Open waits for it
  message: '', // what the status line says while opening or choosing a file
  choices: null, // { id, files } when a song folder holds several project files
  requests: new Map(), // song → request ID, reused when the same song is retried
  drawn: '' // the list the DOM was last drawn from
};

async function loadRecentSongs(offerID, homeID) {
  Object.assign(runSongs, {
    offerID,
    homeID,
    status: 'loading',
    rows: [],
    total: 0,
    opening: '',
    message: '',
    choices: null,
    requests: new Map()
  });
  try {
    const response = await fetch(
      `/api/workspaces/${encodeURIComponent(homeID)}/assistant-program/library/projects?${RECENT_SONGS_QUERY}`,
      { headers: { Accept: 'application/json' } }
    );
    const page = response.ok ? await readJSON(response) : null;
    if (runSongs.offerID !== offerID) return;
    if (!page || !Array.isArray(page.rows)) throw new Error('no library page');
    runSongs.rows = recentSongs(page.rows);
    runSongs.total = Number(page.total) || 0;
    runSongs.status = 'ready';
  } catch (_) {
    // The list is a shortcut; without it the done screen is the one it always was.
    if (runSongs.offerID === offerID) runSongs.status = 'failed';
  }
  renderRunModal();
}

function runModalElements() {
  const root = document.getElementById('folderSetupRunModal');
  if (!root) return null;
  return {
    root,
    eyebrow: document.getElementById('folderSetupRunEyebrow'),
    title: document.getElementById('folderSetupRunTitle'),
    bar: document.getElementById('folderSetupRunBar'),
    fill: document.getElementById('folderSetupRunFill'),
    count: document.getElementById('folderSetupRunCount'),
    status: document.getElementById('folderSetupRunStatus'),
    profile: document.getElementById('folderSetupRunProfile'),
    steps: document.getElementById('folderSetupRunSteps'),
    songs: document.getElementById('folderSetupRunSongs'),
    more: document.getElementById('folderSetupRunMore'),
    receipt: document.getElementById('folderSetupRunReceipt'),
    error: document.getElementById('folderSetupRunError'),
    actions: document.getElementById('folderSetupRunActions')
  };
}

// openRecentSong is the pop-up's Open: the library's one-click open, then the
// song's workspace. A folder with several project files comes back as file
// chips under the song; any other failure is the server's own message, and
// the buttons come back so another song can be tried.
async function openRecentSong(song, selectedFile = '') {
  if (runSongs.opening || !runSongs.homeID || state.busy) return;
  const requestID = runSongs.requests.get(song.id) || `open-${requestId()}`;
  runSongs.requests.set(song.id, requestID);
  runSongs.opening = song.id;
  runSongs.message = `Opening ${song.name}…`;
  runModal.error = '';
  renderRunModal();
  try {
    const result = await openLibrarySong(runSongs.homeID, song.id, { requestID, selectedFile });
    runSongs.requests.delete(song.id);
    const route = libraryOpenRoute(result);
    if (route) {
      window.location.assign(route); // Stay "Opening…" while the page changes.
      return;
    }
    runSongs.choices = null;
    runSongs.message = '';
    runModal.error = `${song.name} is ready. Browse all songs to open it.`;
  } catch (error) {
    const files = libraryOpenChoices(error);
    if (files.length) {
      runSongs.choices = { id: song.id, files };
      runSongs.message = `${song.name} has more than one project file. Choose the one to open.`;
    } else {
      runSongs.message = '';
      runModal.error = error?.message || `${song.name} could not be opened. Nothing was changed.`;
    }
  }
  runSongs.opening = '';
  renderRunModal();
  if (runSongs.choices?.id === song.id) {
    document.querySelector(`#folderSetupRunSongs [data-folder-run-choice] button`)?.focus();
  }
}

function songRow(song) {
  const item = document.createElement('li');
  item.className = 'folder-run__song';
  item.dataset.songId = song.id;
  const text = document.createElement('span');
  text.className = 'folder-run__song-text';
  const name = document.createElement('strong');
  name.textContent = song.name;
  text.append(name);
  if (song.saved || song.facts) {
    // One muted line: "Saved 3 days ago · 14 tracks · 92 BPM · 3:41".
    const saved = document.createElement('small');
    saved.textContent = songLineText(song.saved, song.facts);
    if (song.facts) saved.dataset.songFacts = song.facts;
    text.append(saved);
  }
  const open = document.createElement('button');
  open.type = 'button';
  // Not .btn-outline-primary: components.css makes that a white glass button.
  open.className = 'btn btn-sm folder-run__song-open';
  open.dataset.folderRunOpen = song.id;
  open.setAttribute('aria-label', song.ariaLabel);
  open.addEventListener('click', () => void openRecentSong(song));
  item.append(text, open);
  return item;
}

function fileChoices(song, files) {
  const group = document.createElement('div');
  group.className = 'folder-run__choices';
  group.dataset.folderRunChoice = song.id;
  group.setAttribute('role', 'group');
  group.setAttribute('aria-label', `Project file to open for ${song.name}`);
  files.forEach(file => {
    const chip = document.createElement('button');
    chip.type = 'button';
    chip.className = 'btn btn-sm btn-outline-secondary';
    chip.textContent = file;
    chip.addEventListener('click', () => void openRecentSong(song, file));
    group.append(chip);
  });
  return group;
}

// renderRunSongs draws the song list once per list and keeps each row's state
// (Opening…, disabled, file chips) in step on every render, so a re-render
// never takes focus away from the button the user is on.
function renderRunSongs(els, view) {
  if (!els.songs || !els.more || !els.receipt) return;
  const songs = view.songs || [];
  els.songs.hidden = !songs.length;
  // The songs lead; what was set up folds under "What I set up".
  if (songs.length && els.receipt.parentElement !== els.more) els.more.append(els.receipt);
  if (!songs.length && els.receipt.parentElement === els.more) els.more.before(els.receipt);
  els.more.hidden = !songs.length || !view.receiptRows.length;
  const drawn = JSON.stringify(songs);
  if (drawn !== runSongs.drawn) {
    runSongs.drawn = drawn;
    els.songs.replaceChildren(...songs.map(songRow));
  }
  songs.forEach(song => {
    const item = els.songs.querySelector(`[data-song-id="${CSS.escape(song.id)}"]`);
    if (!item) return;
    const open = item.querySelector('[data-folder-run-open]');
    open.disabled = Boolean(runSongs.opening) || state.busy;
    open.textContent = runSongs.opening === song.id ? 'Opening…' : 'Open';
    const chosen = runSongs.choices?.id === song.id ? runSongs.choices.files : null;
    let group = item.querySelector('[data-folder-run-choice]');
    if (!chosen) group?.remove();
    else if (!group) {
      group = fileChoices(song, chosen);
      item.append(group);
    }
    group?.querySelectorAll('button').forEach(chip => {
      chip.disabled = Boolean(runSongs.opening);
    });
  });
}

function openRunModal() {
  const els = runModalElements();
  const Modal = globalThis.bootstrap?.Modal;
  if (!els || !Modal) return;
  runModal.wanted = true;
  runModal.error = '';
  if (!runModal.bound) {
    els.root.addEventListener('hidden.bs.modal', () => {
      runModal.wanted = false;
    });
    runModal.bound = true;
  }
  renderRunModal();
  Modal.getOrCreateInstance(els.root).show();
}

function closeRunModal() {
  runModal.wanted = false;
  const root = document.getElementById('folderSetupRunModal');
  if (root) globalThis.bootstrap?.Modal?.getInstance(root)?.hide();
}

function renderRunModal() {
  if (!runModal.wanted) return;
  const els = runModalElements();
  if (!els) return;
  const offerID = String(state.offer?.id || '');
  const listed = runSongs.offerID === offerID && runSongs.status === 'ready';
  const view = setupModalView(
    state.offer,
    listed ? { songs: runSongs.rows, total: runSongs.total } : {}
  );
  if (!view.visible) {
    closeRunModal();
    return;
  }
  const done = view.phase === 'done';
  // The first time the open pop-up reaches done on a Home, ask the library once.
  const receipt = done ? folderReceiptView(state.offer) : null;
  if (receipt?.home && receipt.homeID && offerID && runSongs.offerID !== offerID) {
    void loadRecentSongs(offerID, receipt.homeID);
  }
  els.root.dataset.phase = view.phase;
  setText(els.eyebrow, view.eyebrow, false);
  setText(els.title, view.title, false);
  setText(els.count, view.count);
  setText(els.status, (view.songs?.length && runSongs.message) || view.status, false);
  renderProfileLine(els.profile, done ? view.profile : null);
  els.bar.setAttribute('aria-valuenow', String(view.percent));
  els.fill.style.width = `${view.percent}%`;
  renderSetupLines(els.steps, view.lines);
  els.steps.hidden = done || !view.lines.length;
  renderReceiptRows(els.receipt, view.receiptRows);
  els.receipt.hidden = !done || !view.receiptRows.length;
  renderRunSongs(els, view);
  els.error.textContent = runModal.error;
  els.error.hidden = !runModal.error;
  els.actions.replaceChildren();
  if (done && view.route) {
    const open = document.createElement('a');
    // Beside the songs' Open buttons, browsing the library is the quieter way on.
    open.className = view.songs?.length ? 'btn btn-outline-secondary' : 'btn btn-primary';
    open.href = view.route;
    open.textContent = view.openLabel;
    els.actions.append(open);
  }
  view.actions.forEach(action => {
    const button = document.createElement('button');
    button.type = 'button';
    button.className = action.style === 'primary' ? 'btn btn-primary' : 'btn btn-outline-secondary';
    button.dataset.folderRunAction = action.id;
    button.textContent = action.label;
    button.disabled = state.busy;
    button.addEventListener('click', () => {
      // Retrying keeps the pop-up; anything that opens another dialog or page
      // closes it first.
      if (!action.oneCard) closeRunModal();
      runAction(action);
    });
    els.actions.append(button);
  });
}

function requestId() {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function')
    return crypto.randomUUID();
  return `req-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

async function readJSON(response) {
  try {
    return await response.json();
  } catch (_) {
    return null;
  }
}

function renderChooser() {
  const els = elements();
  if (!els?.chooser) return;
  const view = folderChooserView(state.digest);
  if (els.title)
    els.title.textContent = state.offer
      ? 'Or explore another folder'
      : state.handOver
        ? firstFolderPromptView(state.digest, true).line
        : 'Which folder should I explore?';
  els.chooser.hidden = !state.chooserOpen;
  if (els.chips) {
    els.chips.replaceChildren();
    view.chips.forEach(chip => {
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'pa-folder__chip';
      button.dataset.chip = chip.id;
      button.textContent = chip.label;
      button.disabled = state.busy;
      button.addEventListener('click', () => scan({ chip: chip.id }));
      els.chips.appendChild(button);
    });
    if (view.pickerVisible) {
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'pa-folder__chip';
      button.dataset.picker = 'true';
      button.textContent = `${FOLDER_CHIP_ICON} ${view.pickerLabel}`;
      button.disabled = state.busy;
      button.addEventListener('click', () => scan({ picker: true }));
      els.chips.appendChild(button);
    }
    if (view.filePickerVisible) {
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'pa-folder__chip';
      button.dataset.filePicker = 'true';
      button.textContent = view.filePickerLabel;
      button.disabled = state.busy;
      button.addEventListener('click', () => scan({ file: true }));
      els.chips.appendChild(button);
    }
  }
  setText(els.note, view.note);
}

function renderScene() {
  const els = elements();
  if (!els?.scene) return;
  const view = folderSceneView(
    state.contextProgress || {
      scanning: state.scanning,
      failed: state.scanFailed,
      scanName: state.scanName,
      offer: state.offer
    }
  );
  els.scene.hidden = !view.visible;
  if (!view.visible) return;
  els.scene.dataset.phase = view.phase;
  els.scene.dataset.freshScan = String(state.freshScan);
  setText(els.sceneLabel, view.label, false);
  if (els.sceneFinds) {
    els.sceneFinds.replaceChildren();
    view.finds.forEach(find => {
      const badge = document.createElement('span');
      badge.textContent = find;
      els.sceneFinds.appendChild(badge);
    });
  }
  // Clone the already rendered identity, including custom portraits. No new
  // asset lookup, assistant name interpolation, or second appearance pipeline.
  const portrait = document.getElementById('personalAssistantPanelAvatar');
  if (els.sceneAvatar && portrait && els.sceneAvatar.innerHTML !== portrait.innerHTML) {
    els.sceneAvatar.replaceChildren(
      ...Array.from(portrait.childNodes, node => node.cloneNode(true))
    );
  }
}

export function conversationFolderOfferView(offer, options = {}) {
  const view = folderOfferView(offer, options);
  if (offer?.conversation_id && offer.status === 'closed')
    return {
      ...view,
      question: 'This setup review is closed. It created no workspace.',
      reason: '',
      actions: []
    };
  if (!offer?.conversation_id || offer.status !== 'pending') return view;
  const actions = (view.actions || []).filter(
    action =>
      action.decision !== 'no' &&
      action.decision !== 'later' &&
      (offer.needs_pick ||
        offer.capability ||
        offer.create_available ||
        (!action.modal && !action.create))
  );
  if (!offer.needs_pick && !offer.capability && !offer.create_available)
    view.question =
      'Workspace setup is unavailable here. You can keep discussing the observed folder without creating one.';
  // A blocked review says only why it is blocked and how to refresh it; it does
  // not describe a confirmation that is not available.
  const blocked = ['changed', 'unavailable'].includes(offer.destination_status);
  if (!offer.needs_pick && !offer.capability && offer.create_available && !blocked) {
    for (let i = 0; i < actions.length; i++) {
      if (actions[i].modal)
        actions[i] = { id: 'rename', label: 'Adjust name', style: 'outline', rename: true };
    }
    const type = offer.blueprint ? offer.blueprint_label || offer.blueprint : 'Blank workspace';
    view.question += ` Type: ${type}. Confirming creates the workspace, links this selected folder, and seeds its existing read-only first task. That task starts only when you press Start first look; it then reads file contents under its workspace permissions. Provider and staffing steps keep their existing gates.`;
  }
  actions.push({ id: 'keep-chatting', label: 'Keep chatting', style: 'link', closeReview: true });
  return { ...view, actions };
}

function renderOffer(place) {
  const els = elements();
  if (!els?.offer) return;
  const focusedAction =
    state.reviewSlot && els.actions?.contains(document.activeElement)
      ? document.activeElement.dataset.folderAction
      : '';
  if (state.reviewSlot) window.PersonalAssistantTranscript?.beforeChange?.();
  const view = conversationFolderOfferView(state.offer, { confirm: state.confirm });
  const receipt = folderReceiptView(state.offer, {
    busy: state.firstLookBusy,
    message: state.firstLookMessage
  });
  const firstLook = receipt.firstLook || { visible: false };
  // The one offer card is a reply in the conversation while the flow runs
  // there, and a card in Needs you when it was found waiting on load. It is
  // moved, never copied, so its controls and their handlers stay the same.
  const home = state.reviewSlot || (place === 'needs' && els.needs ? els.needs : els.root);
  if (els.offer.parentElement !== home) home.append(els.offer);
  els.offer.dataset.placement = place;
  els.offer.hidden = !view.visible || place === 'none';
  els.root.dataset.state = view.visible
    ? `offer-${view.verdict}`
    : state.chooserOpen
      ? 'chooser'
      : 'idle';
  if (!view.visible) return;
  els.offer.dataset.verdict = view.verdict;
  els.offer.dataset.status = view.status;
  if (els.headline) {
    els.headline.replaceChildren();
    view.headline.forEach(part => {
      if (part.strong) {
        const strong = document.createElement('strong');
        strong.textContent = part.text;
        els.headline.appendChild(strong);
      } else {
        els.headline.append(part.text);
      }
    });
  }
  setText(els.question, view.question, false);
  if (els.nameLabel)
    els.nameLabel.hidden = !(
      state.reviewSlot &&
      state.editingName &&
      state.offer?.status === 'pending' &&
      !state.offer?.capability
    );
  if (els.workspaceName && document.activeElement !== els.workspaceName)
    els.workspaceName.value = state.workspaceName;
  setText(els.capability, view.capabilityDetail);
  setText(els.reason, view.reason, false);
  if (els.why) els.why.hidden = !view.reason;
  if (els.receipt) {
    els.receipt.hidden = !receipt.visible;
    renderReceiptRows(els.receipt, receipt.visible ? receipt.rows : []);
  }
  // What the agent found, once its first look has finished.
  setText(els.result, firstLook.visible ? firstLook.excerpt : '');
  if (els.plan) {
    const lines = view.setup?.lines || view.plan?.lines || [];
    renderSetupLines(els.plan, lines);
    els.plan.hidden = !lines.length;
    els.plan.dataset.mode = view.setup ? view.setup.status : 'plan';
  }
  if (els.actions) {
    els.actions.replaceChildren();
    els.actions.hidden =
      view.decided &&
      !receipt.route &&
      !receipt.homeRoute &&
      !(firstLook.visible && firstLook.actions.length) &&
      (!view.resume || !(view.actions || []).length);
    if (firstLook.visible) {
      // The first look's own actions: its one primary (Start first look, Try
      // again, or the report) and the workspace as the other.
      firstLook.actions.forEach(action => {
        const style = action.style === 'primary' ? 'btn-primary' : 'btn-outline-secondary';
        const control = document.createElement(action.href ? 'a' : 'button');
        control.className = `btn btn-sm ${style}`;
        control.dataset.folderAction = action.id;
        control.textContent = action.label;
        if (action.href) {
          control.href = action.href;
        } else {
          control.type = 'button';
          control.disabled = action.busy === true;
          control.addEventListener('click', () => void startFirstLook());
        }
        els.actions.append(control);
      });
    } else if (receipt.route) {
      const open = document.createElement('a');
      open.className = 'btn btn-sm btn-primary';
      open.href = receipt.route;
      open.textContent = receipt.openLabel;
      els.actions.append(open);
    }
    if (receipt.homeRoute) {
      const home = document.createElement('a');
      home.className = 'btn btn-sm btn-outline-secondary';
      home.href = receipt.homeRoute;
      home.textContent = 'Review this link in the Home library';
      els.actions.append(home);
    }
    if (!receipt.route && !receipt.homeRoute && (!view.decided || view.resume)) {
      view.actions.forEach(action => {
        const button = document.createElement('button');
        button.type = 'button';
        button.className =
          action.style === 'primary'
            ? 'btn btn-sm btn-primary'
            : action.style === 'outline'
              ? 'btn btn-sm btn-outline-secondary'
              : 'btn btn-sm btn-link';
        button.dataset.folderAction = action.id;
        button.textContent = action.label;
        button.disabled = state.busy;
        button.addEventListener('click', () => runAction(action));
        els.actions.appendChild(button);
      });
    }
  }
  if (els.offerNote) {
    // A one-card run says what it is doing; a stopped one already said what it
    // needs in the question, so the outcome note would only repeat it.
    // The first look speaks on the same line: that it is running, why it cannot
    // start, or what its click will cost.
    const lookNote = firstLook.visible
      ? firstLook.status || firstLook.message || firstLook.hint
      : '';
    const note =
      state.progress ||
      lookNote ||
      (view.setup
        ? view.setup.statusLine
        : view.decided && !receipt.visible
          ? folderOutcomeNote(state.offer)
          : '');
    els.offerNote.replaceChildren();
    els.offerNote.hidden = !note;
    if (note) {
      els.offerNote.append(note);
      const route = String(state.offer?.outcome?.route || '').trim();
      if (view.status === 'resolved' && !receipt.visible && route.startsWith('/')) {
        const link = document.createElement('a');
        link.href = route;
        link.textContent = 'Open it';
        els.offerNote.append(' ', link);
      }
    }
  }
  if (
    focusedAction &&
    !els.actions.contains(document.activeElement) &&
    !document.getElementById('personalAssistantPanel')?.hidden
  ) {
    const replacement = Array.from(els.actions.querySelectorAll('[data-folder-action]')).find(
      control => control.dataset.folderAction === focusedAction
    );
    const control = replacement || els.offer;
    if (!replacement) control.tabIndex = -1;
    control.focus({ preventScroll: true });
  }
  if (state.reviewSlot) window.PersonalAssistantTranscript?.changed?.();
}

function render() {
  const els = elements();
  if (!els) return;
  const place = folderPlacement({
    available: state.available,
    inThread: state.inThread,
    pinned: state.pinned,
    offer: state.offer
  });
  els.root.hidden = Boolean(state.reviewSlot) || place !== 'thread';
  if (els.request) els.request.hidden = !(place === 'thread' && state.userAsked);
  window.PersonalAssistantPanel?.setFolderBusy?.(state.available && folderChipBusy(state));
  if (!state.available) {
    if (els.offer) els.offer.hidden = true;
    return;
  }
  renderChooser();
  renderOffer(state.reviewSlot ? 'thread' : place);
  renderScene();
  if (state.reviewSlot && els.scene) els.scene.hidden = true;
  renderRunModal();
  syncSetupPolling();
}

// enterThread starts the flow in the conversation, or keeps it there. When the
// user asked, their request is shown, the turn becomes the latest thing in the
// conversation, and Home is told so it can fold what is above. The assistant's
// own first-folder prompt does neither: it is a message, not a request.
function enterThread({ byUser }) {
  state.inThread = true;
  state.pinned = '';
  if (!byUser) return;
  state.userAsked = true;
  const els = elements();
  if (els?.thread && els.thread.lastElementChild !== els.root) els.thread.append(els.root);
  try {
    document.dispatchEvent(
      new CustomEvent('personal-assistant:folder-started', { detail: { by: 'user' } })
    );
  } catch (_) {
    // Listeners only tidy their own surface; none is required to start.
  }
}

// runAction carries out one of the offer's actions, wherever its button was
// pressed: the panel's card or the mission card (act below).
function runAction(action) {
  if (action.closeReview) {
    void window.PersonalAssistantFolderSetup?.closeReview?.(state.offer);
    return;
  }
  if (action.rename) {
    state.editingName = true;
    render();
    elements()?.workspaceName?.focus();
    return;
  }
  if (action.repick && state.offer?.conversation_id) {
    void window.PersonalAssistantFolderContext?.open?.();
    return;
  }
  if (action.open) {
    openChooser();
    return;
  }
  if (action.repick) {
    // Straight to the dialog where there is one; the chips otherwise.
    if (state.digest?.picker_available === true) scan({ picker: true });
    else openChooser();
    return;
  }
  if (action.confirm || action.back) {
    state.confirm = action.back ? '' : String(action.confirm);
    render();
    announceOffer();
    return;
  }
  if (action.href) {
    window.location.assign(action.href);
    return;
  }
  if (action.oneCard) {
    startOneCardSetup(action);
    return;
  }
  if (action.journey) {
    void startCapabilityJourney();
    return;
  }
  if (action.modal) {
    startProjectOutcome(action);
    return;
  }
  if (action.manual === 'file-janitor') {
    startManualTidy();
    return;
  }
  decide(action);
}

const SETUP_POLL_MS = 1500;
let setupPollTimer = null;

// Bound setup replies/polls cannot take over a different conversation's card.
// Existing standalone journeys retain their independent lifetime.
export function folderReviewResponseCurrent(offer, generation, currentGeneration, currentOffer) {
  return (
    !offer?.conversation_id || (generation === currentGeneration && offer.id === currentOffer?.id)
  );
}

function reviewResponseGuard(offer) {
  const generation = state.reviewGeneration;
  return () => folderReviewResponseCurrent(offer, generation, state.reviewGeneration, state.offer);
}

// Confirm the displayed plan only; the server rechecks it and runs the setup.
async function startOneCardSetup(action) {
  const offer = state.offer;
  const current = reviewResponseGuard(offer);
  if (!offer?.id || state.busy) return;
  if (offer.needs_pick) {
    showOfferFailure({
      needs_pick: true,
      error: 'Ori no longer has that folder open. Pick it again.'
    });
    render();
    return;
  }
  const digest = String(offer.plan?.digest || offer.setup?.plan_digest || '');
  if (!digest) {
    showError('This setup has no plan to confirm. Choose Adjust… to set it up step by step.');
    return;
  }
  state.busy = true;
  showError('');
  render();
  try {
    const body = { plan_digest: digest };
    if (action?.entry) body.entry_name = action.entry;
    const { ok, payload } = await postOffer(offer.id, 'setup', body);
    if (!current()) return;
    if (ok) {
      // Show the run in a pop-up so it is plain that the assistant is working.
      openRunModal();
      return;
    }
    if (payload?.plan_changed && payload.offer) {
      // The card on screen was stale: show what Set up would do now.
      state.offer = payload.offer;
      announceOffer();
      showError(String(payload.error || 'What Set up does has changed. Review the new plan.'));
      return;
    }
    showOfferFailure(payload);
  } catch (_) {
    if (current()) showError('Set up could not be started. Try again.');
  } finally {
    state.busy = false;
    render();
  }
}

// syncSetupPolling polls the existing digest read while a run is going, and
// only then: a one-card setup, or a folder's first look while the drawer that
// shows it is open. A page that loads onto either resumes it the same way;
// nothing polls otherwise, so an idle page makes no extra request.
function syncSetupPolling() {
  const running = state.offer?.setup?.status === 'running' || firstLookPolling();
  if (running && !setupPollTimer) {
    setupPollTimer = setInterval(() => void pollTick(), SETUP_POLL_MS);
  } else if (!running && setupPollTimer) {
    clearInterval(setupPollTimer);
    setupPollTimer = null;
  }
}

// A first look takes a minute or more, so it is read on every other tick.
let firstLookTick = 0;

async function pollTick() {
  if (state.offer?.setup?.status === 'running') return pollSetup();
  if (!firstLookPolling()) {
    // The look settled or the drawer closed: stop asking.
    syncSetupPolling();
    return undefined;
  }
  firstLookTick = (firstLookTick + 1) % 2;
  if (firstLookTick !== 0) return undefined;
  return refreshFirstLook(currentFirstLook()?.offer_id);
}

function drawerOpen() {
  const panel = document.getElementById('personalAssistantPanel');
  return Boolean(panel) && !panel.hidden;
}

// The look is followed only while it runs and only while the drawer is open
// (FR17). The mission card keeps its own slower read of the board.
function firstLookPolling() {
  return currentFirstLook()?.state === 'running' && drawerOpen();
}

// currentFirstLook is the first look this page follows: the one on the card
// when the card is a set-up folder, else the latest one the digest names.
function currentFirstLook() {
  if (folderFirstLookOpen(state.offer)) return state.offer.first_task;
  const look = state.digest?.first_look;
  return look && FIRST_LOOK_STATES.includes(String(look.state || '')) ? look : null;
}

// applyFirstLook records where a look stands, on the card it belongs to and as
// the digest's latest. A null look means the server has nothing to show for
// that offer any more.
function applyFirstLook(offerID, look) {
  const id = String(offerID || '');
  if (!id) return;
  if (state.offer?.id === id && state.offer.status === 'resolved') {
    const next = { ...state.offer };
    if (look) next.first_task = look;
    else delete next.first_task;
    state.offer = next;
  }
  if (state.digest && (!state.digest.first_look || state.digest.first_look.offer_id === id)) {
    state.digest = { ...state.digest, first_look: look || undefined };
  }
}

// What changes on Home when a look moves: the mission card reads the board
// again, and Today lists the look under Working on or its result under Done.
function firstLookMoved() {
  if (typeof window === 'undefined') return;
  try {
    window.dispatchEvent(new Event('ori:progression-refresh'));
  } catch (_) {
    // The mission card has its own poll.
  }
  void window.PersonalAssistantToday?.refresh?.();
}

// refreshFirstLook reads one set-up folder's offer and takes the server's word
// for where its first look stands.
async function refreshFirstLook(offerID) {
  const id = String(offerID || '');
  if (!id) return;
  const before = currentFirstLook()?.state;
  try {
    const response = await fetch(`${DIGEST_ENDPOINT}?offer_id=${encodeURIComponent(id)}`, {
      headers: { Accept: 'application/json' }
    });
    if (!response.ok) return;
    const payload = await readJSON(response);
    const offer = payload?.folder_digest?.offer;
    if (!offer || offer.id !== id) return;
    if (state.offer?.id === id && offer.status === 'resolved') state.offer = offer;
    applyFirstLook(id, offer.first_task || null);
  } catch (_) {
    // The next tick, or the next open, reads it again.
    return;
  }
  const after = currentFirstLook()?.state;
  if (after !== 'seeded') state.firstLookMessage = '';
  render();
  announceOffer();
  if (before !== after) firstLookMoved();
}

// startFirstLook is the one click that spends model tokens on a folder's first
// look. The receipt's button and the mission card's both land here. A look that
// did not finish is run again through the ordinary manual task run, so the
// one-time start is never asked for twice (FR11). It resolves to what happened,
// for a caller that has its own card to update.
async function startFirstLook() {
  const look = currentFirstLook();
  const idle = { started: false, message: '' };
  if (!look || state.firstLookBusy) return idle;
  const retry = look.state === 'failed';
  if (!retry && !(look.state === 'seeded' && look.can_start === true)) return idle;
  const workspaceID = String(look.workspace_id || '').trim();
  const taskID = String(look.task_id || '').trim();
  if (!workspaceID || (retry && !taskID)) return idle;

  state.firstLookBusy = true;
  state.firstLookMessage = '';
  render();
  announceOffer();
  let started = false;
  let reason = 'start_failed';
  try {
    const request = { method: 'POST', headers: { 'Content-Type': 'application/json' } };
    const response = retry
      ? await fetch('/api/orchestration/tasks/execute', {
          ...request,
          body: JSON.stringify({ task_id: taskID })
        })
      : await fetch(`/api/workspaces/${encodeURIComponent(workspaceID)}/folder-first-task/start`, {
          ...request,
          body: '{}'
        });
    const payload = await readJSON(response);
    if (retry) started = response.ok && payload?.success !== false;
    else if (response.ok) {
      started = payload?.started === true;
      reason = String(payload?.reason || 'start_failed');
    }
  } catch (_) {
    started = false;
  }
  if (started) {
    // The run is handed to the executor. Say so now; the read below confirms it.
    applyFirstLook(look.offer_id, {
      ...look,
      state: 'running',
      can_start: false,
      reason: '',
      message: '',
      detail: 'Running…'
    });
  } else {
    state.firstLookMessage = retry
      ? FIRST_LOOK_NOT_STARTED
      : firstLookStartMessage(reason, look.workspace_name);
  }
  state.firstLookBusy = false;
  const message = state.firstLookMessage;
  render();
  announceOffer();
  if (started) firstLookMoved();
  // The server's reading replaces ours. A refused start may mean the look is
  // already running, or finished, somewhere else: the card then shows that.
  await refreshFirstLook(look.offer_id);
  return { started, message };
}

async function pollSetup() {
  const current = reviewResponseGuard(state.offer);
  const id = state.offer?.id;
  if (!id) return;
  try {
    // The card's own offer, by name: another waiting offer may be the "current"
    // one, and a finished run must still show its own receipt.
    const response = await fetch(`${DIGEST_ENDPOINT}?offer_id=${encodeURIComponent(id)}`, {
      headers: { Accept: 'application/json' }
    });
    if (!response.ok) return;
    const payload = await readJSON(response);
    if (!current() || state.offer?.id !== id) return;
    // Once the run settles the read returns the resolved offer, which ends the poll.
    state.offer = payload?.folder_digest?.offer || state.offer;
    announceOffer();
    render();
  } catch (_) {
    // The next tick tries again.
  }
}

// A card confirmation records intent before opening the reviewed install
// quest. A failed modal load leaves a Continue setup action for retry; the
// digest never creates a placeholder workspace or installs on scan.
async function startCapabilityJourney() {
  const offer = state.offer;
  const current = reviewResponseGuard(offer);
  if (!offer?.id || !offer?.capability?.setup_quest_id || state.busy) return;
  state.busy = true;
  showError('');
  render();
  try {
    if (offer.status === 'pending') {
      const { ok, payload } = await postOffer(offer.id, 'decide', {
        decision: 'yes',
        choice: 'project'
      });
      if (!current()) return;
      if (!ok) {
        showOfferFailure(payload);
        return;
      }
    }
    if (state.offer.portfolio?.existing_home) {
      await startExistingHomeCollection(state.offer);
      return;
    }
    if (state.offer.portfolio) {
      await startPortfolioSetup(state.offer);
      return;
    }
    const { openSpecialistSetupJourney } = await import('./setup-journey.js');
    if (!current()) return;
    const capability = state.offer.capability;
    const selection =
      capability.setup_source === 'plugin'
        ? {
            source: 'plugin',
            plugin_id: capability.setup_plugin_id,
            quest_id: capability.setup_quest_id
          }
        : { source: 'host', quest_id: capability.setup_quest_id };
    selection.folder_offer_id = state.offer.id;
    if (!(await openSpecialistSetupJourney(selection)) && current()) {
      showError('Could not open setup. Choose Continue setup to try again.');
    }
  } catch (error) {
    if (current())
      showError(error?.message || 'Could not open setup. Choose Continue setup to try again.');
  } finally {
    state.busy = false;
    render();
  }
}

// portfolioProviderAction names what confirming the portfolio card does to the
// reviewed Home provider. An older installed reviewed release is updated; the
// server refuses that while a Home still uses it.
export function portfolioProviderAction(provider = {}) {
  if (provider.installed && provider.update) {
    return `update the installed provider from ${provider.installed_version || 'its release'} to ${provider.version || 'the reviewed release'}`;
  }
  return provider.installed
    ? 'enable the installed provider'
    : 'install and enable the reviewed provider';
}

// The owner already has this Home, so nothing is installed or created. The
// server re-reads that Home itself (the browser names none) and records it as the
// outcome; landing in its library is navigation, and the library asks for its own
// initialize, root, and scan reviews before anything is granted or scanned.
async function startExistingHomeCollection(offer) {
  const current = reviewResponseGuard(offer);
  const result = await postOffer(offer.id, 'existing-home', {});
  if (!current()) return;
  if (!result.ok) {
    if (result.payload?.needs_pick) showOfferFailure(result.payload);
    else
      showError(
        'Your Music Production Home could not be read right now, so nothing was added. Open the Home and use Add folder there.'
      );
    return;
  }
  render();
  continuePortfolioToLibrary(result.payload?.offer);
}

async function startPortfolioSetup(offer) {
  const current = reviewResponseGuard(offer);
  const url = `${DIGEST_ENDPOINT}/offers/${encodeURIComponent(offer.id)}/home-provider`;
  const post = async data => {
    const response = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify(data)
    });
    const body = await readJSON(response);
    if (!response.ok) {
      throw new Error(body?.error || body?.message || 'The Home provider is unavailable.');
    }
    return body.home_provider;
  };
  let provider = await post({});
  if (!current()) return;
  if (!provider?.plugin_id) throw new Error('The reviewed Home provider is unavailable.');
  if (!provider.ready) {
    const disclosure = provider.disclosure || {};
    const action = portfolioProviderAction(provider);
    const parts = [
      `Set up Music Production Home: ${action}?`,
      `Plugin: ${provider.plugin_id} ${provider.version || ''}`,
      provider.source ? `Reviewed source: ${provider.source}` : '',
      (disclosure.AssistantProgramHomes || []).length
        ? `Homes: ${disclosure.AssistantProgramHomes.join(', ')}`
        : '',
      (disclosure.Skills || []).length ? `Skills: ${disclosure.Skills.join(', ')}` : '',
      'Only the reviewed provider is installed; project integrations and the native music apps are not installed.'
    ].filter(Boolean);
    if (!window.confirm(parts.join('\n'))) return;
    provider = await post({ confirm: true, reviewed_version: provider.version });
    if (!current()) return;
    if (!provider?.ready) throw new Error('The Home provider could not be enabled.');
  }
  const response = await fetch('/api/workspaces/group-templates', {
    headers: { Accept: 'application/json' }
  });
  const payload = await readJSON(response);
  if (!current()) return;
  if (!response.ok) throw new Error('The Home setup templates are unavailable.');
  const matches = (payload?.group_templates || []).filter(
    item => item.kind === 'managed_home' && item.provider?.plugin_id === provider.plugin_id
  );
  if (matches.length !== 1) throw new Error('The reviewed Home template is missing or ambiguous.');
  const template = matches[0];
  if (template.home?.state === 'exists' && template.home.workspace_id) {
    const result = await postOffer(offer.id, 'resolve', { home_id: template.home.workspace_id });
    if (!current()) return;
    if (!result.ok)
      throw new Error('That Home was not created for this offer. Pick another collection.');
    render();
    continuePortfolioToLibrary(result.payload?.offer);
    return;
  }
  if (template.availability?.state !== 'creatable')
    throw new Error('This Home cannot be prepared right now.');
  const manager = window.sessionManager;
  const picker = window.GroupTemplateCreator;
  if (!manager?.showAddWorkspaceModal || !picker?.select)
    throw new Error('The Home creator is unavailable.');
  manager.showAddWorkspaceModal({
    kind: 'group',
    entryPoint: 'folder_digest_portfolio',
    drafts: { group: { name: template.proposed_group_name || 'Music Production Home' } },
    onCreated: async ({ groupId }) => {
      const result = await postOffer(offer.id, 'resolve', { home_id: groupId });
      if (!current()) return;
      if (!result.ok)
        throw new Error(
          'The Home was built, but its folder offer is still open. Continue setup to reconcile it.'
        );
      render();
      // Returned, not navigated: the creator still staffs and navigates after
      // this callback and would overwrite a navigation started here. It honours
      // the URL only when nothing needs its own landing notice.
      return portfolioLibraryURL(result.payload?.offer) || undefined;
    }
  });
  const context = manager.workspaceCreatorContext;
  for (let retry = 0; retry < 40 && picker.stateFor(context)?.status !== 'ready'; retry++) {
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  if (!current()) return;
  if (manager.workspaceCreatorContext !== context || !picker.select(manager, template.id)) {
    throw new Error('The exact Home template could not be selected. Nothing was created.');
  }
}

// This is navigation only. Resolving the portfolio offer never grants a root
// or starts a scan; the Home's separate review UI owns those decisions.
export function portfolioLibraryURL(offer) {
  if (offer?.outcome?.kind !== 'home') return '';
  const route = resolvedRouteFor(offer);
  if (!/^\/workspaces\/[^/?#]+\/?$/.test(route) || !offer?.id) return '';
  return `${route.replace(/\/$/, '')}/assistant?folder_offer_id=${encodeURIComponent(offer.id)}#projectLibraryPanel`;
}

// Landing in the Home's library is not a consequence: the library opens on the
// carried collection and still asks for its own initialize, root, and scan
// reviews, so a separate "continue?" dialog only delays it. Cancelling here
// would leave a Home the user just created and no obvious way back to the folder.
function continuePortfolioToLibrary(offer) {
  const url = portfolioLibraryURL(offer);
  if (url) window.location.assign(url);
}

// startManualTidy is the tidy's Adjust…: the ordinary File Janitor creator,
// named after the folder, whose own setup wizard asks for the folder through
// its picker. The offer stays where it is; nothing is decided by looking.
function startManualTidy() {
  const offer = state.offer;
  const manager = typeof window !== 'undefined' ? window.sessionManager : null;
  if (!manager?.showAddWorkspaceModal) {
    if (typeof window !== 'undefined')
      window.location.assign('/workspaces/new?template=file-janitor');
    return;
  }
  showError('');
  manager.showAddWorkspaceModal({
    entryPoint: 'folder_digest',
    name: `File Janitor — ${String(offer?.folder || '').trim() || 'folder'}`,
    blueprint: 'file-janitor',
    blueprintNote: 'Choose the folder to tidy in the setup wizard after creating.'
  });
}

// act runs the current offer's action with this id, for the mission card that
// renders the same offer inline. Returns false when there is no such action
// to run: no offer, an offer already decided, or an unknown id.
function act(actionId) {
  const view = folderOfferView(state.offer, { confirm: state.confirm });
  if (!view.visible || (view.decided && !view.resume)) return false;
  const action = (view.actions || []).find(candidate => candidate.id === actionId);
  if (!action) return false;
  runAction(action);
  return true;
}

// openHQCard opens the drawer on the card that builds the HQ, expanded even when
// the user deferred it with Not now. It builds nothing.
function openHQCard() {
  const panel = window.PersonalAssistantPanel;
  if (!panel || typeof panel.open !== 'function') return false;
  if (!panel.open(document.getElementById('personalAssistantLauncher'))) return false;
  window.PersonalAssistantToday?.expand?.();
  return window.PersonalAssistantHQCard?.expand?.() === true;
}

// openChooser is the user asking to explore a folder: the chip above the
// composer, Home's toolbar button, the `folder=show` link, the mission's
// Start, or an offer's own "Show another folder". Every one of them starts
// the same turn in the conversation.
//
// It is the one-card flow, with the chips and the picker, then a read-only scan,
// then the verdict card with Set up. The composer's "Add folder" chip is the chat
// flow and calls PersonalAssistantFolderContext itself. Every folder starts at
// the HQ, so until one is built this opens its card instead of a chooser that
// the server would refuse (FR24, FR25).
function openChooser() {
  if (!state.available) {
    openHQCard();
    return;
  }
  enterThread({ byUser: true });
  state.chooserOpen = true;
  state.scanFailed = false;
  showError('');
  render();
  const els = elements();
  els?.root?.scrollIntoView?.({ block: 'nearest' });
  els?.chips?.querySelector('button')?.focus?.();
}

async function load() {
  state.scanFailed = false;
  state.freshScan = false;
  let revealFirstPrompt = false;
  const generation = state.reviewGeneration;
  const endpoint =
    state.reviewSlot && state.offer?.id
      ? `${DIGEST_ENDPOINT}?offer_id=${encodeURIComponent(state.offer.id)}`
      : DIGEST_ENDPOINT;
  try {
    const response = await fetch(endpoint, { headers: { Accept: 'application/json' } });
    if (!response.ok) throw new Error(`folder digest ${response.status}`);
    const payload = await readJSON(response);
    if (generation !== state.reviewGeneration) return;
    state.digest = payload?.folder_digest || null;
    state.offer = state.digest?.offer || null;
    state.confirm = '';
    state.firstLookMessage = '';
    state.pinned = folderPinnedOffer(state);
    const prompt = firstFolderPromptView(state.digest, state.available);
    if (prompt.expand && !state.prompting) {
      revealFirstPrompt = true;
      // The assistant speaks first: its prompt is the first message in the
      // conversation, with no request from the user above it.
      enterThread({ byUser: false });
      state.handOver = true;
      state.chooserOpen = true;
      state.prompting = true;
      // The chooser shows immediately; persisting the receipt cannot delay it.
      void fetch(`${DIGEST_ENDPOINT}/prompted`, { method: 'POST' })
        .then(response => {
          if (!response.ok) throw new Error('Could not save the folder prompt');
          state.digest.prompt_first_folder = false;
        })
        .catch(() => showStatus('Could not save the prompt. It may appear again after a reload.'))
        .finally(() => {
          state.prompting = false;
        });
    }
  } catch (_) {
    state.digest = state.digest || { chips: [], picker_available: false };
  }
  render();
  // What needs the user comes before the conversation and can put the first
  // prompt below the fold. Reveal it once inside an already-open drawer,
  // without scrolling Home when the relationship changes in a closed panel.
  if (revealFirstPrompt && !document.getElementById('personalAssistantPanel')?.hidden) {
    elements()?.chooser?.scrollIntoView?.({ block: 'center', behavior: 'instant' });
  }
  announceOffer();
}

async function scan(body) {
  if (state.busy) return;
  // Exploring happens in the conversation. A scan asked for from a card in
  // Needs you ("Pick it again") starts the turn there; one asked for from the
  // chooser is already in it. The dialog's own status line is in the chooser.
  enterThread({ byUser: !state.inThread });
  if (body.picker || body.file) state.chooserOpen = true;
  state.busy = true;
  state.scanning = !body.picker && !body.file;
  state.scanFailed = false;
  state.freshScan = false;
  state.scanName =
    body.picker || body.file
      ? ''
      : folderChooserView(state.digest).chips.find(chip => chip.id === body.chip)?.label ||
        'your folder';
  const els = elements();
  if (els?.why) els.why.open = false;
  showError('');
  showStatus(
    body.file ? 'Choose a file in the dialog…' : body.picker ? 'Choose a folder in the dialog…' : ''
  );
  render();
  try {
    const response = await fetch(`${DIGEST_ENDPOINT}/scan`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify(body)
    });
    const payload = await readJSON(response);
    if (!response.ok) {
      state.scanFailed = true;
      showStatus(
        String(payload?.error || 'That folder could not be looked at. Choose a different folder.')
      );
      return;
    }
    if (payload?.cancelled) {
      showStatus('');
      return;
    }
    state.offer = payload?.offer || null;
    state.freshScan = Boolean(state.offer);
    state.confirm = '';
    state.handOver = false;
    // The folder is chosen: the scene and the offer are the replies now, and
    // "Explore a folder" can be asked for again.
    if (state.offer) state.chooserOpen = false;
    showStatus('');
    announceOffer();
  } catch (_) {
    state.scanFailed = true;
    showStatus('That folder could not be looked at right now.');
  } finally {
    state.scanning = false;
    state.busy = false;
    render();
    // What the assistant found is its newest reply: bring it into view.
    if (state.freshScan) elements()?.offer?.scrollIntoView?.({ block: 'nearest' });
    if (pendingProjectRun?.offerID === state.offer?.id && !state.offer?.needs_pick) {
      void onSetupProjectReady(pendingProjectRun.runID);
    }
  }
}

// postOffer sends one offer action (decide or resolve) and returns the
// response, updating the shown offer on success.
async function postOffer(offerId, action, body) {
  const generation = state.reviewGeneration;
  const response = await fetch(
    `${DIGEST_ENDPOINT}/offers/${encodeURIComponent(offerId)}/${action}`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify({ ...body, request_id: requestId() })
    }
  );
  const payload = await readJSON(response);
  if (
    (response.ok || response.status === 409) &&
    payload?.offer?.id === offerId &&
    generation === state.reviewGeneration &&
    state.offer?.id === offerId
  ) {
    state.offer = payload.offer;
    state.confirm = '';
    announceOffer();
  }
  return { ok: response.ok, payload };
}

function showOfferFailure(payload) {
  if (payload?.needs_pick) {
    // Said on the card that was pressed, which now offers Pick it again, and
    // in the chooser as well when that is open.
    const message = String(payload?.error || 'Pick the folder again.');
    showError(message);
    if (state.offer) state.offer = { ...state.offer, needs_pick: true };
    if (state.chooserOpen) showStatus(message);
    return;
  }
  showError(String(payload?.error || 'That could not be saved. Try again.'));
}

export function folderDecisionProgress(offer, action) {
  const subject = String(offer?.subject?.name || offer?.folder || '').trim() || 'the folder';
  if (action.walkthrough) return `Setting up File Janitor for ${subject}…`;
  if (action.create && offer?.operation === 'link_supporting_folder')
    return `Linking ${subject} as a supporting folder in ${offer.destination?.name || 'the reviewed project'}…`;
  if (action.create) return `Setting up the workspace for ${subject}…`;
  return '';
}

async function decide(action) {
  const offer = state.offer;
  if (!offer?.id || state.busy) return;
  const generation = state.reviewGeneration;
  if (action.create && state.editingName && !elements()?.workspaceName?.reportValidity()) return;
  state.busy = true;
  showError('');
  // A setup takes a few seconds (a workspace, a grant, a scan); the card
  // says so rather than sitting there disabled.
  state.progress = folderDecisionProgress(offer, action);
  render();
  try {
    const body = { decision: action.decision, choice: action.choice || '' };
    // The card's confirmed plan: the assistant sets the workspace up itself.
    if (action.create === true) {
      body.create = true;
      if (offer.conversation_id) body.review_digest = offer.review_digest || '';
      if (offer.conversation_id && state.editingName) body.workspace_name = state.workspaceName;
    }
    const { ok, payload } = await postOffer(offer.id, 'decide', body);
    if (generation !== state.reviewGeneration) return;
    if (!ok) {
      showOfferFailure(payload);
      return;
    }
    // A tidy, or a workspace the assistant set up, resolves in the same
    // request. A tidy from the confirmed plan is shown as it happened (the
    // setup card's walkthrough, ending on its review); anything else opens
    // its route: the first review batch, the workspace that already manages
    // the folder, or the new workspace.
    const route = resolvedRouteFor(state.offer);
    // The server-authored receipt is the confirmation of what Set up actually
    // made. Stay on it until the user presses Open <workspace>.
    if (action.create === true && folderReceiptView(state.offer).visible) return;
    if (action.walkthrough && (await revealSetupWalkthrough(state.offer))) return;
    if (route && typeof window !== 'undefined') window.location.assign(route);
  } catch (_) {
    if (generation === state.reviewGeneration) showError('That could not be saved. Try again.');
  } finally {
    state.progress = '';
    state.busy = false;
    render();
  }
}

// revealSetupWalkthrough shows a fresh tidy as the setup it was: the
// assistant-led setup card (under Needs you) with its receipts — the
// workspace, the File Curator, the folder, the paused watch, the first scan —
// and its walkthrough open, ending on the review. Returns false when there is
// no fresh run to show (the folder was already managed, or the card is not on
// this page), so the caller opens the route instead.
async function revealSetupWalkthrough(offer) {
  if (typeof window === 'undefined') return false;
  if (String(offer?.status || '') !== 'resolved' || offer?.outcome?.kind !== 'tidy') return false;
  if (offer?.outcome?.existing === true) return false;
  const setup = window.AssistantLedSetup;
  if (!setup || typeof setup.load !== 'function') return false;
  let projection = null;
  try {
    projection = await setup.load('');
  } catch (_) {
    return false;
  }
  if (!projection?.run) return false;
  const panel = window.PersonalAssistantPanel;
  if (panel && typeof panel.open === 'function') {
    // The setup card is what this opens the drawer for, so focus is left to it.
    panel.open(document.getElementById('personalAssistantLauncher'), { focus: false });
  }
  // The card is under Needs you, which the conversation folded away.
  window.PersonalAssistantToday?.expand?.();
  const card = document.getElementById('assistantLedSetup');
  card?.scrollIntoView?.({ block: 'start', behavior: 'smooth' });
  const milestones = projection.milestones || [];
  if (milestones.length && setup.presentation && typeof setup.presentation.open === 'function') {
    setup.presentation.open(milestones, { invoker: setup.els?.replay || null });
  }
  return true;
}

// resolvedRouteFor returns the page a resolved outcome should open — a tidy's
// first review batch or a set-up workspace — or '' when the offer is not
// resolved or the route is not a local path.
export function resolvedRouteFor(offer) {
  if (String(offer?.status || '') !== 'resolved') return '';
  const kind = offer?.outcome?.kind;
  if (kind !== 'tidy' && kind !== 'project' && kind !== 'home') return '';
  const route = String(offer?.outcome?.route || '').trim();
  return route.startsWith('/') && !route.startsWith('//') ? route : '';
}

// tidyRouteFor is resolvedRouteFor for a tidy only.
export function tidyRouteFor(offer) {
  return offer?.outcome?.kind === 'tidy' ? resolvedRouteFor(offer) : '';
}

// startProjectOutcome runs a project yes (FR28, FR29): the Create Workspace
// modal opens pre-filled; only once it reports a created workspace is the yes
// recorded and the folder attached. Closing the modal without creating
// leaves the offer exactly where it was.
function startProjectOutcome(action) {
  const offer = state.offer;
  if (!offer?.id || state.busy) return;
  if (offer.needs_pick) {
    showOfferFailure({
      needs_pick: true,
      error: 'Ori no longer has that folder open. Pick it again.'
    });
    render();
    return;
  }
  const manager = typeof window !== 'undefined' ? window.sessionManager : null;
  if (!manager?.showAddWorkspaceModal) {
    showError('The workspace creator is not available on this page.');
    render();
    return;
  }
  showError('');
  manager.showAddWorkspaceModal({
    ...folderProjectModalOptions(offer),
    onCreated: async ({ workspaceId } = {}) => {
      const id = String(workspaceId || '').trim();
      if (!id) return;
      state.busy = true;
      render();
      try {
        const decided = await postOffer(offer.id, 'decide', {
          decision: action.decision,
          choice: action.choice || ''
        });
        if (!decided.ok) {
          showOfferFailure(decided.payload);
          return;
        }
        const resolved = await postOffer(offer.id, 'resolve', { workspace_id: id });
        if (!resolved.ok) showOfferFailure(resolved.payload);
      } catch (_) {
        showError('The workspace was created, but the folder could not be attached. Try again.');
      } finally {
        state.busy = false;
        render();
      }
    }
  });
}

let resolvingProjectRun = '';
let pendingProjectRun = null;
// Called before workspace navigation as well as when a ready quest is
// reopened after a restart. The offer ID must still be the card on Home.
export async function resolveFolderProjectRun(runID, offerID) {
  if (state.offer?.id !== offerID) return;
  await onSetupProjectReady(runID);
}

async function onSetupProjectReady(runID) {
  const offer = state.offer;
  if (
    !offer?.id ||
    !offer.capability ||
    offer.portfolio ||
    offer.status !== 'awaiting_outcome' ||
    !runID ||
    resolvingProjectRun === runID
  )
    return;
  resolvingProjectRun = runID;
  pendingProjectRun = { offerID: offer.id, runID };
  try {
    const { ok } = await postOffer(offer.id, 'resolve', { run_id: runID });
    // A different or historical quest can be ready on the same page. The
    // server refuses it; leave this card resumable without claiming a match.
    if (ok) {
      pendingProjectRun = null;
      render();
    }
  } catch (_) {
    // A network interruption can be retried by reopening setup or re-picking.
  } finally {
    resolvingProjectRun = '';
  }
}

function onStatus(personalAssistant) {
  const available = folderActionAvailable(personalAssistant);
  const changed = available !== state.available;
  state.available = available;
  if (!available) {
    state.handOver = false;
    state.chooserOpen = false;
    state.inThread = false;
    state.userAsked = false;
    render();
    return;
  }
  // Nothing opens by itself: the chooser is asked for with "Explore a folder",
  // and the read below decides whether the assistant speaks first or an offer
  // is still waiting.
  if (changed || !state.digest) void load();
  else render();
}

// The conversation keeps whichever of its two parts was used last nearest the
// composer: a request sent after the folder turn goes below it.
function keepLatestLast() {
  const els = elements();
  if (!els?.thread || !els.activity || els.root.hidden) return;
  if (els.thread.lastElementChild !== els.activity) els.thread.append(els.activity);
}

function init() {
  const els = elements();
  if (!els) return;
  document.addEventListener('personal-assistant:status', event => {
    onStatus(event.detail?.personalAssistant);
  });
  window.addEventListener('ori:setup-project-ready', event => {
    void onSetupProjectReady(String(event.detail?.run_id || ''));
  });
  document.addEventListener('personal-assistant:sent', keepLatestLast);
  // The first look is followed only while the drawer is open. Opening it reads
  // the look once, which also resumes the poll when it is still running.
  document.addEventListener('personal-assistant:opened', () => {
    const look = currentFirstLook();
    if (look?.state === 'running') void refreshFirstLook(look.offer_id);
  });
  const panelState = window.PersonalAssistantPanel?._state;
  if (panelState?.personalAssistant) onStatus(panelState.personalAssistant);
  els.workspaceName?.addEventListener('input', () => {
    state.workspaceName = els.workspaceName.value;
  });
  window.PersonalAssistantFolder = {
    open: openChooser,
    openChooser,
    reload: load,
    current: () => state.offer,
    // The mission card's in-place action: start (or retry) the latest folder's
    // first look, and read where it stands.
    startFirstLook,
    firstLook: currentFirstLook,
    showConversationReview: (slot, offer) => {
      if (!slot?.isConnected || !offer?.conversation_id) return;
      if (state.offer?.id !== offer.id || state.reviewSlot !== slot) {
        state.reviewGeneration++;
        state.workspaceName = offer.subject?.name || '';
        state.editingName = false;
        slot.replaceChildren();
      }
      state.reviewSlot = slot;
      state.offer = offer;
      state.inThread = true;
      state.userAsked = false;
      state.chooserOpen = false;
      state.pinned = '';
      render();
    },
    contextProgress: context => {
      const mount = document.getElementById('personalAssistantContextProgress');
      const scene = elements()?.scene;
      if (!mount || !scene) return;
      if (scene.parentElement !== mount) mount.append(scene);
      state.contextProgress = { scanning: context.selecting, scanName: context.scanName };
      renderScene();
    },
    updateConversationReview: offer => {
      if (state.offer?.id !== offer?.id || !offer?.conversation_id) return;
      state.offer = offer;
      render();
    },
    parkConversation: () => {
      if (!state.reviewSlot) return;
      state.reviewGeneration++;
      const els = elements();
      if (els?.offer && els.root) els.root.append(els.offer);
      state.reviewSlot = null;
      state.inThread = false;
      state.userAsked = false;
      state.pinned = '';
      closeRunModal();
      render();
    },
    act
  };
}

if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
}
