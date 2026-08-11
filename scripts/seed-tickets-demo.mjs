/*
 * Seeds a demo workspace tree for the Group 2 Tickets demo.
 *
 *   node scripts/seed-tickets-demo.mjs [baseUrl]
 *
 * Creates a parent workspace with a child (for the descendant roll-up), a
 * spread of states/priorities/tags/due dates, a parent/subticket pair, and an
 * aged-out terminal ticket so the 14-day archive boundary has something on
 * both sides of it. Prints the parent studio id on the last line.
 */
const base = process.argv[2] || 'http://localhost:8931';

async function api(method, path, body) {
  const res = await fetch(`${base}${path}`, {
    method,
    headers: body ? { 'Content-Type': 'application/json' } : {},
    body: body ? JSON.stringify(body) : undefined
  });
  const text = await res.text();
  let payload = null;
  try {
    payload = JSON.parse(text);
  } catch {
    payload = text;
  }
  if (!res.ok) throw new Error(`${method} ${path} -> ${res.status}: ${text.slice(0, 300)}`);
  return payload;
}

async function createWorkspace(name, { parentId = '', kind = '' } = {}) {
  const body = { name };
  if (parentId) body.parent_id = parentId;
  if (kind) body.kind = kind;
  const payload = await api('POST', '/api/workspaces', body);
  return (payload.folder && payload.folder.id) || payload.id;
}

// Names are suffixed so re-seeding an existing demo sandbox does not collide
// with the folder slug of a previous run.
const stamp = Date.now().toString(36).slice(-4);

// Only a group workspace may have children, so the roll-up parent is a group.
const parentId = await createWorkspace(`Tickets Demo ${stamp}`, { kind: 'group' });
const childId = await createWorkspace(`Child Studio ${stamp}`, { parentId });
console.error(`parent=${parentId} child=${childId}`);

const tickets = `/api/workspaces/${parentId}/tickets`;
const childTickets = `/api/workspaces/${childId}/tickets`;

const day = n => {
  const d = new Date();
  d.setDate(d.getDate() + n);
  return d.toISOString();
};

// A spread across states, priorities, tags, and due dates.
const backlogFlaky = await api('POST', tickets, {
  state: 'backlog',
  title: 'Investigate flaky pipeline',
  description: 'The nightly build fails intermittently.',
  tags: ['infra', 'ci'],
  priority: 1,
  due_date: day(-3)
});
await api('POST', tickets, {
  state: 'backlog',
  title: 'Rewrite the onboarding docs',
  tags: ['docs'],
  priority: 4
});
const readyCache = await api('POST', tickets, {
  state: 'ready',
  title: 'Ship the cache fix',
  tags: ['infra'],
  priority: 2,
  due_date: day(0)
});
await api('POST', tickets, { state: 'ready', title: 'Plan the quarter', priority: 3, due_date: day(3) });

// Child workspace ticket, for the descendant roll-up.
await api('POST', childTickets, { state: 'ready', title: 'Child studio work', priority: 3 });

// A parent/subticket pair, so detail has hierarchy to show.
const parentTicket = await api('POST', tickets, { state: 'ready', title: 'Migrate the index' });
const subTicket = await api('POST', tickets, { state: 'ready', title: 'Back up the old index' });

// A closed ticket, aged past the recent window so Archive has content. The
// created_at/history timestamps are server-owned, so this one is driven
// through its real lifecycle and then back-dated on disk by the caller if
// needed; here it simply demonstrates a Done ticket inside the window.
const doneTicket = await api('POST', tickets, { state: 'ready', title: 'Retire the old exporter' });
for (const to of ['in_progress', 'review', 'done']) {
  await api('POST', `${tickets}/${doneTicket.id}/transition`, { to });
}

console.error(
  JSON.stringify(
    {
      backlogFlaky: backlogFlaky.display_number,
      readyCache: readyCache.display_number,
      parentTicket: parentTicket.id,
      subTicket: subTicket.id,
      doneTicket: doneTicket.display_number
    },
    null,
    2
  )
);

// Last line is the parent studio id, for shell capture.
console.log(parentId);
