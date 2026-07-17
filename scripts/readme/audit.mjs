import process from 'node:process';
import { loadManifest, validateManifest } from './manifest.mjs';

async function main() {
  const root = process.cwd();
  const { manifest } = await loadManifest(root);
  const errors = validateManifest(manifest);
  const report = {
    status: manifest.acceptance_state === 'bootstrap' ? 'bootstrap' : errors.length > 0 ? 'error' : 'pending-drift-implementation',
    acceptance_state: manifest.acceptance_state,
    last_accepted_capture_commit: manifest.last_accepted_capture_commit,
    contract_errors: errors
  };
  console.log(JSON.stringify(report, null, 2));
  if (errors.length > 0) process.exitCode = 1;
}

main().catch(error => {
  console.error(`README audit failed: ${error.message}`);
  process.exitCode = 1;
});
