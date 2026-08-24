import { createWorkspaceSurfaceSDK } from './workspace-surface-sdk.js';

const sdk = createWorkspaceSurfaceSDK();
const liveToken = document.querySelector('[data-live-token]');
const bridgeState = document.querySelector('[data-bridge-state]');
const cadence = document.querySelector('[data-cadence]');
const checks = document.querySelector('[data-check-count]');
const visits = document.querySelector('[data-visit-count]');
const result = document.querySelector('[data-result]');
const confirmationResult = document.querySelector('[data-confirmation-result]');
const rejection = document.querySelector('[data-rejection]');
let visible = true;
let timer = null;
let checkCount = 0;
let requestInFlight = false;

function setText(node, value) {
  if (node) node.textContent = String(value || '');
}

async function readStatus() {
  if (!visible || requestInFlight) return;
  requestInFlight = true;
  try {
    const status = await sdk.invoke('status.read', {});
    checkCount += 1;
    setText(checks, checkCount);
    setText(cadence, '1 second · visible');
    setText(bridgeState, status?.value || 'Ready');
  } catch (error) {
    setText(bridgeState, error.code || 'Unavailable');
  } finally {
    requestInFlight = false;
  }
}

function stopPolling(reason = 'Paused') {
  if (timer) clearInterval(timer);
  timer = null;
  setText(cadence, reason);
}

function startPolling() {
  stopPolling('Starting…');
  if (!visible) return;
  void readStatus();
  timer = setInterval(() => void readStatus(), 1000);
}

sdk.on('ready', async () => {
  liveToken?.classList.add('is-ready');
  setText(liveToken?.lastChild, 'Bridge ready');
  setText(bridgeState, 'Authenticated parent');
  try {
    const saved = await sdk.getState('opens');
    const previous = saved?.found ? Number(saved.value?.count || 0) : 0;
    const next = previous + 1;
    await sdk.setState(
      'opens',
      { count: next },
      {
        schemaVersion: 1,
        expectedRevision: saved?.revision || '0'
      }
    );
    setText(visits, next);
  } catch (error) {
    setText(visits, error.code || 'Unavailable');
  }
  startPolling();
});

sdk.on('visibility', event => {
  visible = Boolean(event.visible);
  if (visible) startPolling();
  else stopPolling('Paused · hidden');
});

sdk.on('invalidated', () => {
  stopPolling('Stopped · invalidated');
  setText(bridgeState, 'Session ended');
});

document.querySelector('[data-invoke-greeting]')?.addEventListener('click', async () => {
  const name = String(document.querySelector('#demo-name')?.value || '').trim();
  setText(result, 'Invoking greeting.create…');
  try {
    const output = await sdk.invoke('greeting.create', { name });
    setText(result, output?.message || 'The operation completed.');
  } catch (error) {
    setText(result, `${error.code || 'error'} · ${error.message}`);
  }
});

document.querySelector('[data-confirmation]')?.addEventListener('click', async () => {
  setText(confirmationResult, 'Waiting for Ori approval…');
  try {
    const output = await sdk.invoke('setting.validate', { enabled: true });
    setText(
      confirmationResult,
      output?.accepted ? 'Approved and completed once.' : 'Not accepted.'
    );
  } catch (error) {
    setText(confirmationResult, `${error.code || 'cancelled'} · ${error.message}`);
  }
});

document.querySelector('[data-degrade]')?.addEventListener('click', async () => {
  setText(confirmationResult, 'Waiting for Ori approval…');
  try {
    await sdk.invoke('setting.validate', { enabled: false });
    await sdk.statusChanged();
    setText(confirmationResult, 'Degraded intentionally. Open Setup to repair it.');
  } catch (error) {
    setText(confirmationResult, `${error.code || 'cancelled'} · ${error.message}`);
  }
});

document.querySelector('[data-undeclared]')?.addEventListener('click', async () => {
  setText(rejection, 'Invoking service.admin…');
  try {
    await sdk.invoke('service.admin', {});
    setText(rejection, 'Unexpectedly accepted.');
  } catch (error) {
    setText(rejection, `${error.code || 'rejected'} · no service call recorded`);
  }
});

document.querySelector('[data-close]')?.addEventListener('click', async () => {
  stopPolling('Stopped · closing');
  try {
    await sdk.close();
  } catch (_error) {
    // Host teardown is authoritative; the frame may disappear before resolve.
  }
});

window.addEventListener('pagehide', () => stopPolling('Stopped · closed'));
