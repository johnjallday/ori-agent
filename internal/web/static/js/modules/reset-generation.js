(function () {
  const generationKey = 'ori.reset.generation';
  let loadedGeneration = '';
  try {
    loadedGeneration = window.localStorage?.getItem(generationKey) || '';
  } catch {
    // Storage can be disabled. The server-side reset fence remains authoritative.
  }

  window.addEventListener('storage', event => {
    if (event.key !== generationKey || !event.newValue || event.newValue === loadedGeneration) {
      return;
    }
    loadedGeneration = event.newValue;
    window.location.reload();
  });
})();
