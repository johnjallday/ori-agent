/*
 * Writes a PRE-TICKET workspace.json straight to disk, so the migration demo
 * runs against data that genuinely predates this feature rather than against
 * records the new code created.
 *
 *   node scripts/seed-legacy-workspace.mjs <workspacesRoot>
 *
 * Covers every legacy status class plus the data the migration must report
 * rather than guess at: an unknown status, an unreadable board date, a custom
 * column, and a Backlog record carrying execution details it should not have.
 */
import { mkdirSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { randomUUID } from 'node:crypto';

const root = resolve(process.argv[2] || '');
if (!root) {
  console.error('usage: node scripts/seed-legacy-workspace.mjs <workspacesRoot>');
  process.exit(1);
}

const id = randomUUID();
const slug = 'legacy-studio';
const dir = join(root, slug);
mkdirSync(dir, { recursive: true });

const at = n => new Date(Date.UTC(2026, 0, 1, 12, n, 0)).toISOString();

// Deliberately shaped like the OLD record: legacy status only, kanban_* in
// context, no ticket_state / ticket_number / state_history anywhere.
const tasks = [
  { id: 'legacy-backlog', description: 'Captured idea', status: 'backlog', backlog_rank: 2, created_at: at(0) },
  { id: 'legacy-backlog-2', description: 'Another idea', status: 'backlog', backlog_rank: 1, created_at: at(1) },
  { id: 'legacy-pending', description: 'Committed work', status: 'pending', created_at: at(2) },
  { id: 'legacy-assigned', description: 'Assigned work', status: 'assigned', to: 'builder', created_at: at(3) },
  { id: 'legacy-running', description: 'Running work', status: 'in_progress', created_at: at(4) },
  { id: 'legacy-failed', description: 'Failed work', status: 'failed', error: 'boom', created_at: at(5) },
  { id: 'legacy-timeout', description: 'Timed out work', status: 'timeout', created_at: at(6) },
  {
    id: 'legacy-completed',
    description: 'Finished work',
    status: 'completed',
    result: 'all done',
    context: { kanban_column_id: 'col-done', kanban_labels: ['shipped', 'infra'] },
    created_at: at(7)
  },
  {
    id: 'legacy-in-review',
    description: 'Finished but awaiting review',
    status: 'completed',
    context: { kanban_column_id: 'col-review' },
    created_at: at(8)
  },
  { id: 'legacy-cancelled', description: 'Abandoned work', status: 'cancelled', created_at: at(9) },
  // --- records the migration must REPORT rather than guess at ---
  { id: 'legacy-unknown-status', description: 'Mystery record', status: 'teleported', created_at: at(10) },
  {
    id: 'legacy-bad-date',
    description: 'Record with an unreadable board date',
    status: 'pending',
    context: { kanban_due_date: 'next tuesday' },
    created_at: at(11)
  },
  {
    id: 'legacy-custom-column',
    description: 'Record in a custom column',
    status: 'completed',
    context: { kanban_column_id: 'col-xyzzy-custom' },
    created_at: at(12)
  },
  {
    id: 'legacy-unsafe-backlog',
    description: 'Backlog record with execution details',
    status: 'backlog',
    to: 'builder',
    schedule_enabled: true,
    schedule: { type: 'daily', time_of_day: '09:00' },
    created_at: at(13)
  },
  {
    id: 'legacy-good-date',
    description: 'Record with a readable board date',
    status: 'pending',
    context: { kanban_due_date: '2026-09-01' },
    created_at: at(14)
  }
];

const workspace = {
  id,
  name: 'Legacy Studio',
  folder_slug: slug,
  status: 'active',
  shared_data: {},
  messages: [],
  tasks,
  created_at: at(0),
  updated_at: at(15)
};

writeFileSync(join(dir, 'workspace.json'), JSON.stringify(workspace, null, 2));
console.error(`seeded ${tasks.length} legacy tasks into ${dir}`);
console.log(id);
