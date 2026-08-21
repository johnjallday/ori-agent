import { workspacePageURL, workspaceRootURL } from './workspace-routes.js';

// What is stopping this plan, and the one thing to do about it (FR-156).
//
// A plan can be stuck for five unrelated reasons — it needs an answer, it needs
// approval, it needs an agent that is not here, a check has not passed, or its
// work failed — and each has a different fix. Rendering "blocked" for all five
// leaves the user to guess which, and guessing wrong wastes the trip.
//
// So each blocker names its own cause and carries exactly ONE primary action.
// One, not a menu: the point of a primary action is that the user does not have
// to decide what to try first. Secondary options stay on the page where they
// already live.

// BLOCKER_ORDER is the precedence when several apply at once.
//
// It runs from "cannot proceed at all" to "could proceed if you looked". A plan
// missing an agent AND awaiting approval must show the missing agent first:
// approving would not start anything, so leading with approval would send the
// user to do something that changes nothing.
const BLOCKER_ORDER = ['capability', 'failure', 'validation', 'input', 'approval', 'slot'];

// blockersFor returns every reason this plan is stopped, most blocking first.
//
// It reads the plan's own state rather than being told: a caller that computed
// blockers itself could disagree with the server about why nothing is moving,
// and the disagreement would surface as a button that does nothing.
export function blockersFor(plan) {
  if (!plan) return [];

  const blockers = [];
  const progress = plan.progress || {};
  const status = plan.status;

  // An unavailable agent or capability. Nothing else can be tried until the
  // world changes, so this outranks everything.
  const missing = unavailableAssignees(plan);
  if (missing.length > 0) {
    blockers.push({
      kind: 'capability',
      reason:
        missing.length === 1
          ? `This plan needs ${missing[0]}, which is not in this workspace.`
          : `This plan needs agents that are not in this workspace: ${missing.join(', ')}.`,
      action: { label: 'Add the missing agent', href: agentsHref(plan) }
    });
  }

  if (progress.failed > 0) {
    blockers.push({
      kind: 'failure',
      reason: `${progress.failed} task(s) failed. Nothing downstream can start until they are resolved.`,
      action: { label: 'Review the failed work', href: planHref(plan) }
    });
  }

  if (status === 'needs_input') {
    blockers.push({
      kind: 'input',
      reason: 'This plan is waiting on your answers before it can be drafted.',
      action: { label: 'Answer the questions', href: planHref(plan) }
    });
  }

  if (status === 'in_review') {
    blockers.push({
      kind: 'approval',
      reason: 'This plan is waiting for your approval. Nothing has been created yet.',
      action: { label: 'Review and approve', href: planHref(plan) }
    });
  }

  // Waiting for the workspace slot is a blocker, but the mildest one: it
  // resolves by itself when the plan ahead finishes, so the action explains
  // rather than demands.
  if (progress.waiting_for_slot > 0) {
    blockers.push({
      kind: 'slot',
      reason: 'Another plan is executing in this workspace. This one starts when that finishes.',
      action: { label: 'See what is running', href: planHref(plan) }
    });
  }

  return blockers.sort(
    (left, right) => BLOCKER_ORDER.indexOf(left.kind) - BLOCKER_ORDER.indexOf(right.kind)
  );
}

// primaryBlocker returns the one to lead with, or null when nothing is stuck.
export function primaryBlocker(plan) {
  const blockers = blockersFor(plan);
  return blockers.length > 0 ? blockers[0] : null;
}

// unavailableAssignees lists agents the plan assigns that the workspace does
// not have.
//
// A null roster means the roster is unknown, and an unknown roster flags
// nothing: reporting every assignee as missing because nobody could read the
// list would be worse than saying nothing at all.
function unavailableAssignees(plan) {
  const available = plan?.available_agents;
  if (!Array.isArray(available)) return [];

  const known = new Set(available.map(name => String(name).trim().toLowerCase()));
  const missing = new Set();
  for (const group of plan?.draft?.groups || []) {
    for (const item of group?.items || []) {
      const assignee = String(item?.assignee || '').trim();
      if (assignee && !known.has(assignee.toLowerCase())) {
        missing.add(assignee);
      }
    }
  }
  return [...missing];
}

function planHref(plan) {
  const workspaceSlug = plan?.workspace_slug || plan?.studio_id || '';
  const planID = plan?.id || '';
  if (!workspaceSlug || !planID) return '';
  return workspacePageURL(workspaceSlug, ['plans', planID]);
}

function agentsHref(plan) {
  const workspaceSlug = plan?.workspace_slug || plan?.studio_id || '';
  return workspaceSlug ? workspaceRootURL(workspaceSlug) : '';
}

// POLL_INTERVAL_MS is the fallback refresh when no server event arrives.
//
// Bounded on purpose (FR-155). A plan's work runs for minutes, so polling
// faster buys nothing and costs a request per tab per second; polling slower
// makes a finished step look stuck.
export const POLL_INTERVAL_MS = 5000;

// shouldPoll reports whether a plan is in a state where progress can still
// change on its own.
//
// A finished plan never changes again, and polling one forever is how a page
// left open overnight becomes a load-bearing source of traffic.
export function shouldPoll(plan) {
  return ['executing', 'approved', 'paused'].includes(plan?.status);
}
