// The pile is a picture of a completed look, not an inventory to fetch or a
// list of approved moves. Only the console's reviewed Build control can act.
const key = id => `ori:filing-station:deferred:${id}`;
const deferred = new Set();
export function stationDeferred(id) {
  try {
    return localStorage.getItem(key(id)) === '1' || deferred.has(id);
  } catch {
    return deferred.has(id);
  }
}
function defer(id, value) {
  if (value) deferred.add(id);
  else deferred.delete(id);
  try {
    if (value) localStorage.setItem(key(id), '1');
    else localStorage.removeItem(key(id));
  } catch {
    /* session state is still usable */
  }
  globalThis.document?.dispatchEvent?.(new CustomEvent('ori:filing-offer-changed'));
}
export function stationPile(raw) {
  if (
    !raw ||
    !Number.isSafeInteger(raw.count) ||
    raw.count <= 0 ||
    raw.names?.length !== Math.min(6, raw.count) ||
    !Array.isArray(raw.names) ||
    !raw.names.every(n => typeof n === 'string' && n.length > 0)
  )
    return null;
  return { count: raw.count, names: raw.names };
}
export function stationPileLine(view) {
  const n = view.filingPile.count;
  return `${n} loose ${n === 1 ? 'file' : 'files'} in ${view.folder}`;
}
function openBuild(view, button) {
  if (globalThis.window?.FileJanitorConsole?.openStationBuild?.(view.workspaceID, button)) return;
  if (view.route?.startsWith('/workspaces/')) {
    const url = new URL(view.route, window.location.origin);
    url.searchParams.set('mode', 'map');
    url.searchParams.set('panel', 'file-janitor');
    url.searchParams.set('station', 'build');
    window.location.assign(url.pathname + url.search);
  }
}
export function filingStationOffer(doc, view) {
  const box = doc.createElement('section');
  box.className = 'filing-station-offer';
  box.setAttribute('aria-label', 'Filing station offer');
  const control = (label, cls, handler) => {
    const b = doc.createElement('button');
    b.type = 'button';
    b.className = cls;
    b.textContent = label;
    b.addEventListener('click', () => handler(b));
    box.appendChild(b);
    return b;
  };
  const draw = () => {
    box.replaceChildren();
    if (stationDeferred(view.workspaceID)) {
      control('Show Filing station offer', 'btn btn-sm btn-outline-secondary', () => {
        defer(view.workspaceID, false);
        draw()?.focus();
      });
      return;
    }
    const title = doc.createElement('strong');
    title.textContent = stationPileLine(view);
    box.appendChild(title);
    const list = doc.createElement('p');
    list.textContent =
      view.filingPile.names.join(' · ') +
      (view.filingPile.count > 6 ? ` · and ${view.filingPile.count - 6} more` : '');
    box.appendChild(list);
    const primary = control('Build a Filing station', 'btn btn-sm btn-primary', b =>
      openBuild(view, b)
    );
    control('Not now', 'btn btn-sm btn-outline-secondary', () => {
      defer(view.workspaceID, true);
      draw();
      box.querySelector('button')?.focus();
    });
    return primary;
  };
  draw();
  return box;
}
// A compact stack of paper on an empty plot, beside (not inside) the manager
// control. The offer in the balloon is its accessible text equivalent.
export function drawFilingPlot(anchor, view) {
  const host = anchor?.parentElement;
  if (!host?.querySelector) return;
  let plot = host.querySelector('.filing-station-plot');
  if (!view?.filingPile || stationDeferred(view.workspaceID)) {
    plot?.remove();
    return;
  }
  if (!plot) {
    plot = host.ownerDocument.createElement('span');
    plot.className = 'filing-station-plot';
    plot.setAttribute('aria-hidden', 'true');
    host.appendChild(plot);
  }
  plot.textContent = String(view.filingPile.count);
}
