// The station picture lives in the existing Filing dialog. Moving a chip
// changes only this visit's picture. File is the only mutation request.
(() => {
  function create({ doc, batch, base, current, key, receipt, back }) {
    const node = (tag, cls, text) => {
      const n = doc.createElement(tag);
      if (cls) n.className = cls;
      if (text) n.textContent = text;
      return n;
    };
    const host = node('section', 'fj-shelves');
    const button = (text, fn, cls = 'fj-btn fj-btn-secondary') => {
      const b = node('button', cls, text);
      b.type = 'button';
      b.addEventListener('click', fn);
      return b;
    };
    const folders = new Map(),
      places = new Map(),
      shown = new Set();
    let picture,
      selected = '',
      message = '',
      busy = false,
      waiting = null,
      naming = null,
      readReview = null,
      serial = 0;
    const headers = { 'Content-Type': 'application/json' };
    function draw(focus) {
      host.replaceChildren();
      const title = node(
        'h3',
        'fj-title',
        picture ? `${picture.folder}, after filing` : 'Your folder, after filing'
      );
      title.tabIndex = -1;
      host.appendChild(title);
      const notice = node(
        'p',
        'fj-sub',
        message ||
          (picture
            ? 'Select a file, then choose Move here on a folder. Nothing moves until you press File.'
            : 'Opening the folder picture…')
      );
      notice.setAttribute('role', 'status');
      host.appendChild(notice);
      if (!picture) {
        if (!busy) {
          host.appendChild(button('Reload picture', () => void load()));
          host.appendChild(button('Change filing method', back, 'fj-link'));
        }
        if (focus === 'title') title.focus();
        return;
      }
      const active = () => current() && !busy && !waiting && !readReview;
      const grid = node('div', 'fj-shelves-grid');
      host.appendChild(grid);
      let focusTarget;
      const move = dest => {
        if (!active() || !selected) return;
        const id = selected;
        places.set(id, dest);
        selected = '';
        message = dest
          ? 'Placed in ' + folders.get(dest).name + ' for review.'
          : 'Left loose for review.';
        draw(id);
      };
      const shelf = (dest, name) => {
        const card = node('section', dest ? 'fj-shelf' : 'fj-shelf fj-shelf-loose');
        card.setAttribute('aria-label', name);
        grid.appendChild(card);
        if (folders.get(dest)?.newName) {
          card.classList.add('fj-shelf-new');
          card.appendChild(node('small', 'fj-label', 'New folder · made only when filed'));
        }
        card.appendChild(node('h4', 'fj-shelf-title', name));
        const files = node('div', 'fj-shelf-files');
        card.appendChild(files);
        for (const f of picture.files) {
          if ((places.get(f.id) || '') !== dest) continue;
          const chip = button(
            f.name,
            () => {
              if (!active()) return;
              selected = selected === f.id ? '' : f.id;
              message = selected ? `Selected ${f.name}. Choose a folder or leave it loose.` : '';
              draw(f.id);
            },
            'fj-shelf-file'
          );
          chip.setAttribute('aria-pressed', String(f.id === selected));
          chip.disabled = !active();
          files.appendChild(chip);
          if (f.id === focus) focusTarget = chip;
        }
        const action = button(dest ? 'Move here' : 'Leave loose', () => move(dest));
        action.setAttribute('aria-label', dest ? 'Move here: ' + name : 'Leave loose');
        action.disabled = !active() || !selected || (places.get(selected) || '') === dest;
        card.appendChild(action);
      };
      for (const k of shown) {
        const f = folders.get(k);
        if (f) shelf(k, f.name);
      }
      shelf('', `Still loose in ${picture.folder}`);
      const newTarget = node('section', 'fj-shelf fj-shelf-new');
      grid.appendChild(newTarget);
      newTarget.appendChild(node('h4', 'fj-shelf-title', 'New folder'));
      if (!naming) {
        const start = button('Name a new folder', () => {
          if (!active()) return;
          naming = { name: '', parent: '' };
          draw('new-name');
        });
        start.disabled = !active();
        newTarget.appendChild(start);
      } else {
        const form = node('form', 'fj-shelf-tools');
        newTarget.appendChild(form);
        const parent = node('select', 'fj-select');
        parent.setAttribute('aria-label', 'Create folder in');
        const top = node('option', '', picture.folder);
        top.value = '';
        parent.appendChild(top);
        for (const [k, f] of folders) {
          if (f.parent || f.newName) continue;
          const opt = node('option', '', f.name);
          opt.value = k;
          parent.appendChild(opt);
        }
        parent.value = naming.parent;
        parent.disabled = busy;
        parent.addEventListener('change', () => {
          naming.parent = parent.value;
        });
        form.appendChild(parent);
        const input = node('input', 'fj-input');
        input.setAttribute('aria-label', 'New folder name');
        input.maxLength = 255;
        input.required = true;
        input.value = naming.name;
        input.disabled = busy;
        input.addEventListener('input', () => {
          naming.name = input.value;
        });
        form.appendChild(input);
        const check = node('button', 'fj-btn fj-btn-secondary', 'Use this folder name');
        check.type = 'submit';
        check.disabled = busy;
        form.appendChild(check);
        form.addEventListener('submit', e => {
          e.preventDefault();
          void checkName();
        });
        const cancel = button('Cancel', () => {
          naming = null;
          draw(selected || 'title');
        });
        cancel.disabled = busy;
        form.appendChild(cancel);
        if (focus === 'new-name') focusTarget = input;
      }
      const toolbar = node('div', 'fj-shelf-tools');
      host.appendChild(toolbar);
      const select = node('select', 'fj-select');
      select.setAttribute('aria-label', 'Choose an existing folder');
      select.disabled = !active();
      toolbar.appendChild(select);
      const placeholder = node('option', '', 'Choose an existing folder');
      placeholder.value = '';
      select.appendChild(placeholder);
      for (const [k, f] of folders) {
        if (f.newName) continue;
        const option = node('option', '', f.name);
        option.value = k;
        select.appendChild(option);
      }
      const add = button('Add shelf', () => {
        if (!active() || !select.value) return;
        shown.add(select.value);
        message = folders.get(select.value).name + ' is in the picture.';
        draw(selected || 'title');
      });
      add.disabled = true;
      toolbar.appendChild(add);
      const sub = button('Show subfolders', () => {
        if (active() && select.value) void load(select.value);
      });
      sub.disabled = true;
      toolbar.appendChild(sub);
      select.addEventListener('change', () => {
        add.disabled = !active() || !select.value;
        sub.disabled =
          add.disabled ||
          !!folders.get(select.value)?.parent ||
          !!folders.get(select.value)?.located;
      });
      const search = node('form', 'fj-shelf-tools');
      const label = node('label', 'fj-label', 'Find a folder by name');
      search.appendChild(label);
      const name = node('input', 'fj-input');
      name.setAttribute('aria-label', 'Existing folder name, such as Orders or Orders/Winter');
      name.placeholder = 'Orders or Orders/Winter';
      name.required = true;
      name.maxLength = 511;
      name.disabled = !active();
      const find = node('button', 'fj-btn fj-btn-secondary', 'Add named shelf');
      find.type = 'submit';
      find.disabled = !active();
      label.appendChild(name);
      search.appendChild(find);
      host.appendChild(search);
      search.addEventListener('submit', e => {
        e.preventDefault();
        if (active()) void findShelf(name.value);
      });
      if (picture.partial)
        host.appendChild(
          node(
            'p',
            'fj-sub',
            'Only part of this folder is listed. Use Find a folder by name to add another shelf.'
          )
        );
      const count = picture.files.filter(f => places.get(f.id)).length;
      const loose = picture.files.filter(f => !places.get(f.id));
      if (loose.length && !naming && !waiting) {
        const box = node('section', 'fj-shelf-read');
        box.setAttribute('aria-label', 'Optional first-lines read');
        host.appendChild(box);
        if (!readReview) {
          const ask = button(
            `Read the first lines of the ${loose.length} loose ${loose.length === 1 ? 'file' : 'files'} to suggest folders`,
            () => void reviewRead(loose)
          );
          ask.disabled = busy;
          box.appendChild(ask);
          if (focus === 'read') focusTarget = ask;
        } else {
          const { files, folders, processor, remaining, reason, approval } = readReview;
          const heading = node('h4', 'fj-shelf-title', 'Review what Ori may read');
          heading.tabIndex = -1;
          box.appendChild(heading);
          if (focus === 'read-review') focusTarget = heading;
          if (reason) box.appendChild(node('p', 'fj-sub', reason));
          if (files.length)
            box.appendChild(
              node(
                'p',
                'fj-sub',
                `${files.length} ${files.length === 1 ? 'document' : 'documents'}: ${files.join(' · ')}.`
              )
            );
          if (remaining)
            box.appendChild(
              node(
                'p',
                'fj-sub',
                `${remaining} loose ${remaining === 1 ? 'file is' : 'files are'} outside this read.`
              )
            );
          if (approval && !reason) {
            box.appendChild(
              node(
                'p',
                'fj-sub',
                `${processor.provider} · ${processor.model}. ${processor.off_device ? 'Excerpts and these folder names leave this device.' : 'The text stays on this device.'} Only the opening excerpt of each named document is shared. Folder choices: ${folders.join(' · ')}. No files will move.`
              )
            );
            const allow = button(
              'Approve this read',
              () => void applyRead(),
              'fj-btn fj-btn-primary'
            );
            allow.disabled = busy;
            box.appendChild(allow);
          }
          const cancel = button(reason ? 'Back to picture' : 'Not now', () => {
            readReview = null;
            message = 'Nothing was read.';
            draw('read');
          });
          cancel.disabled = busy;
          box.appendChild(cancel);
        }
      }
      const footer = node('footer', 'fj-shelf-footer');
      host.appendChild(footer);
      footer.appendChild(
        node('span', '', `${count} ready to file · ${picture.files.length - count} stay loose`)
      );
      if (!picture.supported)
        footer.appendChild(
          node('p', 'fj-sub', 'This disk cannot safely file into these folders. Nothing will move.')
        );
      if (count > 1000)
        footer.appendChild(
          node('p', 'fj-sub', 'File up to 1,000 files at once. Leave more files loose to continue.')
        );
      const file = button(
        waiting && !busy
          ? 'Check filing result'
          : busy
            ? 'Please wait…'
            : `File ${count} ${count === 1 ? 'file' : 'files'}`,
        () => void submit(),
        'fj-btn fj-btn-primary'
      );
      file.disabled =
        busy ||
        Boolean(readReview) ||
        Boolean(naming) ||
        !picture.supported ||
        !count ||
        count > 1000;
      footer.appendChild(file);
      const reset = button('Filing into my folders · Change', back, 'fj-link');
      reset.id = 'fileJanitorChangeMethod';
      reset.disabled = busy || !!waiting || !!readReview;
      footer.appendChild(reset);
      if (focus === 'title') title.focus();
      else focusTarget?.focus();
    }
    async function reviewRead(loose) {
      if (busy || waiting || readReview || !current()) return;
      busy = true;
      message = 'Checking names and the processor. Nothing has been read.';
      draw();
      try {
        const r = await fetch(base + '/picture/read-review', {
          method: 'POST',
          headers,
          body: JSON.stringify({ revision: picture.revision, files: loose.map(f => f.id) })
        });
        const { review } = await r.json();
        if (!current()) return;
        if (
          !r.ok ||
          !Array.isArray(review?.files) ||
          !Array.isArray(review.folders) ||
          !Number.isSafeInteger(review.remaining) ||
          (!review.reason &&
            (!review.approval ||
              !review.processor?.provider ||
              !review.processor.model ||
              typeof review.processor.off_device !== 'boolean'))
        )
          throw Error('Could not verify this read. Nothing has been read.');
        readReview = review;
        message = 'Review the scope below. Nothing has been read.';
      } catch (e) {
        message = e.message;
      } finally {
        if (current()) {
          busy = false;
          draw(readReview ? 'read-review' : 'read');
        }
      }
    }
    async function applyRead() {
      if (busy || !readReview?.approval || !current()) return;
      const { approval } = readReview;
      busy = true;
      message = 'Reading the approved documents…';
      draw();
      try {
        const r = await fetch(base + '/picture/read-lines', {
          method: 'POST',
          headers,
          body: JSON.stringify({ approval })
        });
        const { result } = await r.json();
        if (!current()) return;
        if (
          !r.ok ||
          !result ||
          !Number.isSafeInteger(result.read) ||
          !Number.isSafeInteger(result.remaining) ||
          !result.placements
        )
          throw Error('No suggestions were applied. Review the folder and processor again.');
        const next = Object.entries(result.placements);
        if (
          next.some(
            ([id, k]) => !picture.files.some(f => f.id === id) || places.get(id) || !folders.has(k)
          )
        )
          throw Error('The picture changed. No suggestions were applied.');
        for (const [id, k] of next) {
          places.set(id, k);
          shown.add(k);
        }
        message = `${result.read} read · ${result.remaining} not read · ${next.length} placed in the picture. Nothing moved.`;
      } catch (e) {
        message = e.message;
      } finally {
        if (current()) {
          readReview = null;
          busy = false;
          draw('title');
        }
      }
    }
    async function findShelf(name) {
      if (busy || waiting || !current()) return;
      busy = true;
      message = 'Checking that folder…';
      draw();
      let focus = 'title';
      try {
        const r = await fetch(base + '/picture/find-folder', {
          method: 'POST',
          headers,
          body: JSON.stringify({ name, revision: picture.revision })
        });
        const b = await r.json();
        if (!current()) return;
        const f = b.shelf;
        if (
          !r.ok ||
          !f ||
          f.name !== name ||
          !/^[a-f0-9]{64}$/.test(f.key) ||
          (f.parent && !/^[a-f0-9]{64}$/.test(f.parent))
        )
          throw Error(b.message || 'That folder is not available in this picture.');
        if (!folders.has(f.key)) folders.set(f.key, { ...f, located: f.name });
        shown.add(f.key);
        message = f.name + ' is in the picture.';
        focus = selected || 'title';
      } catch (error) {
        message = error.message;
      } finally {
        if (current()) {
          busy = false;
          draw(focus);
        }
      }
    }
    async function checkName() {
      if (!naming || busy || !current()) return;
      const { name, parent } = naming;
      busy = true;
      message = 'Checking the folder name…';
      draw();
      let focus = 'new-name';
      try {
        const r = await fetch(base + '/picture/check-folder', {
          method: 'POST',
          headers,
          body: JSON.stringify({
            name,
            parent,
            located: folders.get(parent)?.located,
            revision: picture.revision
          })
        });
        const b = await r.json();
        if (!current()) return;
        if (!r.ok || b.available !== true)
          throw Error(b.message || b.error?.message || 'That folder name is not available.');
        const k = 'new:' + JSON.stringify([parent, name]);
        const display = parent ? folders.get(parent).name + '/' + name : name;
        folders.set(k, {
          name: display,
          newName: name,
          parent,
          located: folders.get(parent)?.located
        });
        shown.add(k);
        if (selected) {
          places.set(selected, k);
          focus = selected;
          selected = '';
        } else focus = 'title';
        naming = null;
        message = display + ' is in the picture. No folder has been created.';
      } catch (error) {
        message = error.message;
      } finally {
        if (current()) {
          busy = false;
          draw(focus);
        }
      }
    }
    async function load(parentKey = '') {
      if (busy || !current()) return;
      const turn = ++serial;
      busy = true;
      message = '';
      draw();
      try {
        const r = await fetch(
          base +
            '/picture?layout=shelves' +
            (parentKey ? '&parent=' + encodeURIComponent(parentKey) : ''),
          { cache: 'no-store' }
        );
        const p = (await r.json()).picture;
        if (!current() || turn !== serial) return;
        if (
          !r.ok ||
          p?.batch !== batch ||
          !/^[a-f0-9]{64}$/.test(p.revision || '') ||
          typeof p.folder !== 'string' ||
          typeof p.supported !== 'boolean' ||
          !Array.isArray(p.files) ||
          p.files.some(f => !f?.id || typeof f.name !== 'string') ||
          !Array.isArray(p.folders) ||
          p.folders.some(f => !/^[a-f0-9]{64}$/.test(f?.key || '') || typeof f.name !== 'string') ||
          (!parentKey &&
            p.files.some(f => f.suggested && !p.folders.some(dir => dir.key === f.suggested)))
        )
          throw Error('Could not verify this folder picture. Check access and reload.');
        if (parentKey && p.revision !== picture.revision)
          throw Error('The folder changed. Reload before filing.');
        if (!parentKey) {
          picture = p;
          folders.clear();
          shown.clear();
          places.clear();
          naming = null;
          selected = '';
          waiting = null;
          for (const f of p.files) {
            if (f.suggested) {
              shown.add(f.suggested);
              places.set(f.id, f.suggested);
            }
          }
        }
        for (const f of p.folders) folders.set(f.key, { ...f, parent: parentKey });
        if (parentKey)
          message = p.folders.length
            ? `Choose one of the ${p.folders.length} subfolders in the folder list.`
            : 'No visible subfolders here.';
      } catch (error) {
        if (!current() || turn !== serial) return;
        message = error.message;
        picture = null;
      } finally {
        if (current() && turn === serial) {
          busy = false;
          draw('title');
        }
      }
    }
    async function submit() {
      if (busy || naming || readReview || !picture?.supported || !current()) return;
      const placements = picture.files
        .filter(f => places.get(f.id))
        .map(f => ({
          file: f.id,
          ...(folders.get(places.get(f.id)).newName
            ? { new_name: folders.get(places.get(f.id)).newName }
            : { shelf: places.get(f.id) }),
          located: folders.get(places.get(f.id)).located,
          parent: folders.get(places.get(f.id)).parent
        }));
      if (!placements.length || placements.length > 1000) return;
      waiting ||= { revision: picture.revision, client_key: key(), placements };
      busy = true;
      message = 'Checking the picture and filing the placed files.';
      draw();
      try {
        const r = await fetch(base + '/file?layout=shelves', {
          method: 'POST',
          headers,
          body: JSON.stringify(waiting)
        });
        const result = await r.json();
        if (!current()) return;
        if (!r.ok) {
          if ([400, 403, 404, 409, 413].includes(r.status)) {
            waiting = null;
            picture = null;
          }
          throw Error(
            result.message ||
              result.error?.message ||
              'The result could not be confirmed. Check this same filing again.'
          );
        }
        if (!result.filing?.filing_id)
          throw Error('The result could not be confirmed. Check this same filing again.');
        await receipt(result.filing);
      } catch (error) {
        if (current()) message = error.message;
      } finally {
        if (current()) {
          busy = false;
          draw('title');
        }
      }
    }
    draw();
    return { host, load, pending: () => busy || Boolean(waiting) };
  }
  window.FilingStationShelves = { create };
})();
