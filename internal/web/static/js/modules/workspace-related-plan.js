// The "this came from a plan" summary shown on Task and Run detail
// (FR-148, FR-149, FR-150).
//
// A Task created by an approved Plan has a story behind it: an objective
// somebody wrote, a version somebody approved, and a reason it exists. Without
// a link back, that story is only reachable by remembering which plan it was —
// which nobody does a week later.
//
// The summary is deliberately COMPACT and deliberately read-only. It says what
// the plan is and links to it; it does not let you edit, review, or approve
// from here. Full editing lives on one canonical surface, and a second place to
// approve from is exactly the duplication this feature exists to remove.

// relatedPlanSummary renders the one-line description of the originating plan.
export function relatedPlanSummary(related) {
  if (!related?.plan_id) return '';

  const title = String(related.title || '').trim() || 'Untitled plan';
  const status = String(related.status_label || related.status || '').trim();
  const version = Number(related.plan_version || 0);

  const parts = [title];
  if (status) parts.push(status);
  if (version > 0) parts.push(`version ${version}`);
  return parts.join(' · ');
}

// relatedPlanProvenance says what THIS task or run was, inside the plan.
//
// The plan title alone answers "which plan"; this answers "which part of it",
// which is the question somebody looking at one failing task actually has.
export function relatedPlanProvenance(related) {
  const provenance = related?.provenance || {};
  const role = String(provenance.role || '').trim();
  const approvedBy = String(provenance.approved_by || '').trim();

  const parts = [];
  if (role === 'group') parts.push('A task group from this plan');
  else if (role === 'item') parts.push('A step from this plan');
  if (approvedBy) parts.push(`approved by ${approvedBy}`);

  return parts.join(', ');
}

// relatedPlanLink returns the canonical URL, which the SERVER supplies.
//
// Building it here would be a second place that knows the route shape, and the
// two would drift the first time it changed. An entry point that cannot get a
// URL from the server renders no link rather than guessing at one.
export function relatedPlanLink(related) {
  return String(related?.url || '').trim();
}

// fetchRelatedPlan looks up the plan behind a task or a run.
//
// A missing link is the ordinary case, not an error: most tasks are created
// directly and have no plan. It resolves to null, and the caller renders
// nothing rather than an empty panel implying something is broken.
export async function fetchRelatedPlan(workspaceId, kind, id, fetchImpl = fetch) {
  if (!workspaceId || !id) return null;
  const endpoint = kind === 'run' ? 'plan-for-run' : 'plan-for-task';

  try {
    const response = await fetchImpl(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/${endpoint}/${encodeURIComponent(id)}`
    );
    if (!response.ok) return null;
    const payload = await response.json();
    return payload?.plan_id ? payload : null;
  } catch {
    return null;
  }
}

// renderRelatedPlan fills a container, or hides it when there is no plan.
export function renderRelatedPlan(container, related, escapeHtml = value => value) {
  if (!container) return;

  const link = relatedPlanLink(related);
  if (!related?.plan_id || !link) {
    container.hidden = true;
    container.innerHTML = '';
    return;
  }

  const provenance = relatedPlanProvenance(related);
  container.hidden = false;
  container.innerHTML =
    `<a class="workspace-related-plan-link" href="${escapeHtml(link)}">` +
    `${escapeHtml(relatedPlanSummary(related))}</a>` +
    (provenance
      ? `<span class="workspace-related-plan-meta">${escapeHtml(provenance)}</span>`
      : '');
}
