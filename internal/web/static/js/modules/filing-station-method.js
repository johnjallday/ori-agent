// One visit to the layout chooser. The host supplies availability, the
// recommendation and the current revision; a send opens the existing card.
(() => {
  function create({ doc, base, current, saved, cancel, send, hasPicture }) {
    const host = doc.createElement('section');
    host.className = 'fj-method';
    host.setAttribute('aria-label', 'Filing method');
    let data = null,
      busy = false,
      error = '';
    const el = (tag, cls, text) => {
      const n = doc.createElement(tag);
      n.className = cls;
      n.textContent = text;
      return n;
    };
    function control(text, fn, disabled = false) {
      const b = el('button', 'fj-btn fj-btn-secondary', text);
      b.type = 'button';
      b.disabled = disabled;
      b.addEventListener('click', fn);
      return b;
    }
    function draw(focus = false) {
      host.replaceChildren();
      const h = el(
        'h3',
        'fj-title',
        data ? `How should ${data.folder} be filed?` : 'How should this folder be filed?'
      );
      h.tabIndex = -1;
      host.appendChild(h);
      const note = el(
        'p',
        'fj-sub',
        error ||
          (busy
            ? 'Checking the filing choices…'
            : hasPicture
              ? 'Changing the method replaces your unfiled picture. Nothing on disk moves.'
              : 'Choose where files should go. Nothing moves until you press File.')
      );
      note.setAttribute('role', 'status');
      host.appendChild(note);
      if (data) {
        const grid = el('div', 'fj-shelves-grid', '');
        host.appendChild(grid);
        for (const [id, title, sample, help] of [
          [
            'own_folders',
            'Into my folders',
            'Folder → Your shelf',
            data.shelves
              ? 'Place files into the folders you choose.'
              : 'Unavailable: check the folder permission and whether this disk supports safe moves.'
          ],
          [
            'by_kind',
            'By kind',
            'Filed → Documents',
            'Sort files into Filed by type when you file.'
          ],
          [
            'send',
            'Send to another workspace',
            'This folder → Another workspace',
            data.send
              ? 'Connect a receiving workspace. Your local filing choice stays the same.'
              : 'No other workspace is available to receive these files.'
          ]
        ]) {
          const card = el('section', 'fj-shelf', '');
          const art = el('small', 'fj-label', sample);
          art.setAttribute('aria-hidden', 'true');
          card.appendChild(art);
          card.appendChild(el('h4', 'fj-shelf-title', title));
          if (data.suggested === id)
            card.appendChild(
              el(
                'p',
                'fj-method-suggested',
                id === 'own_folders'
                  ? 'Suggested · This folder already has shelves.'
                  : 'Suggested · Sort loose files by type.'
              )
            );
          card.appendChild(el('p', 'fj-sub', help));
          card.appendChild(
            control(
              title,
              () => (id === 'send' ? send() : void request(id)),
              busy || (id === 'send' && !data.send) || (id === 'own_folders' && !data.shelves)
            )
          );
          grid.appendChild(card);
        }
      } else if (!busy) host.appendChild(control('Check choices again', () => void request()));
      if (cancel) host.appendChild(control('Keep current picture', cancel, busy));
      if (focus) h.focus();
    }
    async function request(method = '') {
      if (busy || !current() || (method && !data)) return;
      busy = true;
      error = '';
      draw();
      try {
        const r = await fetch(
          base + '/method',
          method
            ? {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ revision: data.revision, method })
              }
            : { cache: 'no-store' }
        );
        const b = await r.json();
        if (!current()) return;
        const v = b.layout;
        if (
          !r.ok ||
          !v ||
          typeof v.folder !== 'string' ||
          !['own_folders', 'by_kind'].includes(v.suggested) ||
          !/^[a-f0-9]{64}$/.test(v.revision) ||
          typeof v.shelves !== 'boolean' ||
          typeof v.send !== 'boolean'
        )
          throw Error(b.message || 'The filing choices could not be confirmed.');
        data = v;
        if (method) {
          if (v.current !== method) throw Error('The method could not be confirmed.');
          saved(v.current);
          return;
        }
      } catch (e) {
        data = null;
        error = e.message;
      } finally {
        busy = false;
        if (current()) draw(true);
      }
    }
    draw();
    return { host, load: request };
  }
  window.FilingStationMethod = { create };
})();
