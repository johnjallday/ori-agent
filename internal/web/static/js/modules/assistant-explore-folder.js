// The established Home chooser is only a picker. Its result belongs to the
// current composer; guided hires and the separate Files modal keep their flows.
export function mountAssistantExplore(host, guided, onAssisted, onPicked) {
  const choices = host.querySelector('[data-explore-choices]');
  let active;
  function stop() {
    active?.abort();
    active = null;
    host.hidden = true;
    guided.hidden = false;
    choices.hidden = false;
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
    try {
      if (
        await window.PersonalAssistantFolderContext?.pick(
          pick.dataset.exploreKind,
          controller.signal
        )
      ) {
        if (!controller.signal.aborted) onPicked();
      }
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
