// One presenter watches a finite preparation request. Polls are GET only;
// neither reload nor a timer retries a grant, starts a scan, or files a file.
(() => {
  const parts = ['plot', 'foundation', 'walls', 'roof', 'open'];
  const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
  const label = b => {
    if (b.looking)
      return b.counted
        ? `Looking at ${b.total || 0} loose files · ${b.checked || 0} of ${b.total || 0} so far. Nothing moves.`
        : 'Looking at the loose files. No count yet. Nothing moves.';
    if (b.part === 'open')
      return b.eligible
        ? `Review ${b.eligible} ${b.eligible === 1 ? 'file' : 'files'}`
        : 'Nothing to file yet';
    return (
      {
        plot: 'Waiting to build',
        foundation: 'Foundation · Filing added',
        walls: 'Walls · Folder access recorded',
        roof: 'Roof · First look finished'
      }[b.part] || 'Checking the station…'
    );
  };
  function observer({ read, paint, reduced = () => false }) {
    let token = 0,
      current = null;
    const publish = b => {
      current = { ...b, line: label(b) };
      paint(current);
    };
    const show = async (b, t, animate) => {
      if (t !== token || !parts.includes(b?.part)) return;
      const from = parts.indexOf(current?.part || 'plot'),
        end = parts.indexOf(b.part);
      // Only fill in parts already proven by the receipt. A page opening on a
      // completed building skips this sequence entirely.
      if (animate && !reduced())
        for (let i = from + 1; i < end; i++) {
          publish({ ...b, part: parts[i], looking: false, revealing: true });
          await pause(260);
          if (t !== token) return;
        }
      publish(b);
    };
    return {
      current: () => current,
      reset() {
        token++;
        current = null;
      },
      async watch(id, started, stillPreparing) {
        const t = ++token;
        if (started) publish({ id, part: 'plot' });
        do {
          let b;
          try {
            b = await read(id);
          } catch {
            return;
          }
          if (t !== token) return;
          // A press can finish before the first GET. Conversely the first
          // GET may precede admission; that response is not a finished build.
          await show(b, t, started);
          if (t !== token) return;
          if ((b?.part === 'open' && b.operation_id) || (!stillPreparing() && !b?.running)) return;
          await pause(240);
        } while (t === token);
      }
    };
  }
  window.FilingStationBuild = { observer, label };
})();
