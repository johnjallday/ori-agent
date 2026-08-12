/*
 * Rewrites an EXISTING workspace.json back into its pre-Ticket shape, so the
 * migration demo runs against data that genuinely predates this feature.
 *
 *   node scripts/legacify-workspace.mjs <path/to/workspace.json>
 *
 * Creating the workspace through the API first (then legacifying it here) is
 * what makes the store discover it — the file store does not pick up
 * hand-dropped folders.
 *
 * Strips every canonical Ticket field and installs a spread of legacy records,
 * including the ones the migration must REPORT rather than guess at.
 */
import { readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';

const path = resolve(process.argv[2] || '');
if (!path) {
  console.error('usage: node scripts/legacify-workspace.mjs <path/to/workspace.json>');
  process.exit(1);
}

const ws = JSON.parse(readFileSync(path, 'utf8'));
const at = n => new Date(Date.UTC(2026, 0, 1, 12, n, 0)).toISOString();

ws.tasks = [
  { id: 'legacy-backlog', workspace_id: ws.id, description: 'Captured idea', status: 'backlog', backlog_rank: 2, created_at: at(0) },
  { id: 'legacy-backlog-2', workspace_id: ws.id, description: 'Another idea', status: 'backlog', backlog_rank: 1, created_at: at(1) },
  { id: 'legacy-pending', workspace_id: ws.id, description: 'Committed work', status: 'pending', created_at: at(2) },
  { id: 'legacy-assigned', workspace_id: ws.id, description: 'Assigned work', status: 'assigned', to: 'builder', created_at: at(3) },
  { id: 'legacy-running', workspace_id: ws.id, description: 'Running work', status: 'in_progress', created_at: at(4) },
  { id: 'legacy-failed', workspace_id: ws.id, description: 'Failed work', status: 'failed', error: 'boom', created_at: at(5) },
  { id: 'legacy-timeout', workspace_id: ws.id, description: 'Timed out work', status: 'timeout', created_at: at(6) },
  {
    id: 'legacy-completed',
    workspace_id: ws.id,
    description: 'Finished work',
    status: 'completed',
    result: 'all done',
    context: { kanban_column_id: 'col-done', kanban_labels: ['shipped', 'infra'] },
    created_at: at(7)
  },
  {
    id: 'legacy-in-review',
    workspace_id: ws.id,
    description: 'Finished but awaiting review',
    status: 'completed',
    context: { kanban_column_id: 'col-review' },
    created_at: at(8)
  },
  { id: 'legacy-cancelled', workspace_id: ws.id, description: 'Abandoned work', status: 'cancelled', created_at: at(9) },
  // --- records the migration must REPORT rather than guess at ---
  { id: 'legacy-unknown-status', workspace_id: ws.id, description: 'Mystery record', status: 'teleported', created_at: at(10) },
  {
    id: 'legacy-bad-date',
    workspace_id: ws.id,
    description: 'Record with an unreadable board date',
    status: 'pending',
    context: { kanban_due_date: 'next tuesday' },
    created_at: at(11)
  },
  {
    id: 'legacy-custom-column',
    workspace_id: ws.id,
    description: 'Record in a custom column',
    status: 'completed',
    context: { kanban_column_id: 'col-xyzzy-custom' },
    created_at: at(12)
  },
  {
    id: 'legacy-unsafe-backlog',
    workspace_id: ws.id,
    description: 'Backlog record with execution details',
    status: 'backlog',
    to: 'builder',
    schedule_enabled: true,
    schedule: { type: 'daily', time_of_day: '09:00' },
    created_at: at(13)
  },
  {
    id: 'legacy-good-date',
    workspace_id: ws.id,
    description: 'Record with a readable board date',
    status: 'pending',
    context: { kanban_due_date: '2026-09-01' },
    created_at: at(14)
  }
];

// Remove every trace of the migration having run.
delete ws.ticket_migration_version;
delete ws.ticket_sequence;

writeFileSync(path, JSON.stringify(ws, null, 2));
console.error(`legacified ${ws.tasks.length} tasks in ${path}`);
console.log(ws.id);
