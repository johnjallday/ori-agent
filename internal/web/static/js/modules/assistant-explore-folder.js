import { chooseSearchFolder, exploreFolder } from './folder-search-review.js';
import { saveArrival } from './files-arrival.js';

// Ordinary Home conversation only. Guided hires, typed paths and file reads
// keep their own existing chooser and authorization; no Files token is passed
// to an agent. Closing/replacing the turn invalidates outstanding picker UI.
export function mountAssistantExplore(host, guided, onAssisted) {
  const choices = host.querySelector('[data-explore-choices]'),
    status = host.querySelector('[data-explore-status]'),
    slot = host.querySelector('[data-explore-review]');
  let active = null;
  function stop() {
    active?.abort();
    active = null;
    host.hidden = true;
    guided.hidden = false;
    choices.hidden = false;
    status.textContent = '';
  }
  host.addEventListener('click', async event => {
    if (event.target.closest('[data-explore-assisted]')) {
      stop();
      onAssisted();
      return;
    }
    const pick = event.target.closest('[data-explore-kind]');
    if (!pick || active) return;
    const controller = new AbortController();
    active = controller;
    choices.hidden = true;
    status.textContent = 'Choosing folder…';
    try {
      const review = await chooseSearchFolder(pick.dataset.exploreKind);
      if (controller.signal.aborted) return;
      status.textContent = '';
      if (!review) {
        status.textContent = 'No folder chosen.';
        return;
      }
      const result = await exploreFolder(review, {
        host: slot,
        inline: true,
        signal: controller.signal,
        preferWorkspace: true
      });
      if (!result || controller.signal.aborted) return;
      if (result.destination === 'workspace')
        location.assign(`/workspaces/${encodeURIComponent(result.workspace.slug)}`);
      else {
        // Search this folder in Files: the merged Files, scoped to it.
        if (!(await saveArrival(result.source)))
          throw new Error(
            'Cannot open that Files location right now. Your workspace is unchanged.'
          );
        if (!controller.signal.aborted) location.assign('/?view=tree');
      }
    } catch (error) {
      if (!controller.signal.aborted) status.textContent = error.message;
    } finally {
      if (active === controller) {
        active = null;
        choices.hidden = false;
        pick.focus();
      }
    }
  });
  return {
    open() {
      stop();
      host.hidden = false;
      guided.hidden = true;
      host.querySelector('button')?.focus();
    },
    stop
  };
}
