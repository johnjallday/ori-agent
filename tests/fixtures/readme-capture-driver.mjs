#!/usr/bin/env node

import { mkdirSync, writeFileSync } from 'node:fs';
import path from 'node:path';

const values = Object.fromEntries(
  process.argv.slice(2).reduce((pairs, value, index, argv) => {
    if (value.startsWith('--')) pairs.push([value.slice(2), argv[index + 1]]);
    return pairs;
  }, []),
);

if (!values['base-url'] || !values['run-dir']) {
  throw new Error('Fixture driver requires --base-url and --run-dir.');
}

const response = await fetch(`${values['base-url']}/health`);
if (!response.ok) throw new Error(`Fixture driver saw unhealthy server: ${response.status}`);
const health = await response.json();
if (health.status !== 'ok') throw new Error('Fixture driver did not receive Ori health payload.');

mkdirSync(path.join(values['run-dir'], 'sidecars'), { recursive: true });
writeFileSync(
  path.join(values['run-dir'], 'sidecars', 'runtime-probe.json'),
  `${JSON.stringify(
    {
      health,
      base_url: values['base-url'],
      fixture_only: true,
      credential_present: Boolean(
        process.env.OPENAI_API_KEY ||
          process.env.ANTHROPIC_API_KEY ||
          process.env.AWS_ACCESS_KEY_ID ||
          process.env.GITHUB_TOKEN,
      ),
    },
    null,
    2,
  )}\n`,
);
writeFileSync(
  path.join(values['run-dir'], 'scene-statuses.json'),
  `${JSON.stringify([{ id: 'runtime-probe', status: 'passed', fixture_only: true }], null, 2)}\n`,
);
