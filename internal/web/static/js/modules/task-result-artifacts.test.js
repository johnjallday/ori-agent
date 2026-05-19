import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  artifactToCSVFence,
  buildRunHistoryArtifact,
  buildTaskResultArtifact,
  detectTabularResult,
  parseDelimitedRecords,
  rowsToCSV,
} from './task-result-artifacts.js';

test('parseDelimitedRecords handles quoted commas', () => {
  assert.deepEqual(
    parseDelimitedRecords('date,summary\n2026-05-19,"High, tree pollen"'),
    [
      ['date', 'summary'],
      ['2026-05-19', 'High, tree pollen'],
    ],
  );
});

test('detectTabularResult parses markdown table output', () => {
  const artifact = detectTabularResult('| date | level |\n| --- | --- |\n| 2026-05-19 | High |');
  assert.equal(artifact.source, 'markdown_table');
  assert.deepEqual(artifact.columns, ['date', 'level']);
  assert.equal(artifact.rows[0].level, 'High');
  assert.equal(artifact.csv, 'date,level\n2026-05-19,High');
});

test('detectTabularResult uses task structured output first', () => {
  const artifact = detectTabularResult('plain text', {
    context: {
      structured_output: {
        location: 'NYC',
        value: 8,
      },
    },
  });
  assert.equal(artifact.source, 'output_schema');
  assert.deepEqual(artifact.columns, ['location', 'value']);
  assert.equal(artifact.rows[0].value, '8');
});

test('buildRunHistoryArtifact turns repeated runs into CSV rows', () => {
  const artifact = buildRunHistoryArtifact({
    execution_history: [
      {
        executed_at: '2026-05-18T12:00:00Z',
        status: 'success',
        result: '{"location":"NYC","level":"Moderate"}',
      },
      {
        executed_at: '2026-05-19T12:00:00Z',
        status: 'success',
        result: '{"location":"NYC","level":"High"}',
      },
    ],
  });
  assert.equal(artifact.source, 'run_history');
  assert.equal(artifact.rows.length, 2);
  assert.ok(artifact.columns.includes('executed_at'));
  assert.ok(artifact.columns.includes('level'));
  assert.match(artifact.csv, /2026-05-19T12:00:00Z,success,NYC,High/);
});

test('buildTaskResultArtifact prefers run history when multiple runs exist', () => {
  const artifact = buildTaskResultArtifact({
    result: '{"location":"NYC","level":"High"}',
    execution_history: [
      { executed_at: '2026-05-18T12:00:00Z', status: 'success', summary: 'Moderate' },
      { executed_at: '2026-05-19T12:00:00Z', status: 'success', summary: 'High' },
    ],
  });
  assert.equal(artifact.source, 'run_history');
  assert.deepEqual(artifact.columns, ['executed_at', 'status', 'summary']);
});

test('rowsToCSV quotes values and artifactToCSVFence wraps output', () => {
  const csv = rowsToCSV(['name', 'summary'], [{ name: 'NYC', summary: 'High, tree pollen' }]);
  assert.equal(csv, 'name,summary\nNYC,"High, tree pollen"');
  assert.equal(artifactToCSVFence({ csv }), '```csv\nname,summary\nNYC,"High, tree pollen"\n```');
});
