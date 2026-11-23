import { documentsFilingCheck } from './documents-filing-check.js';

// Review is deliberately separate from chat submission and from Filing. A
// redraw/revisit may load receipts, but only the approval button can send a POST.
export function documentsOrganizationOffer(doc, base, fetchImpl, route = '') {
  const node = (tag, text, cls) => {
    const n = doc.createElement(tag);
    if (text) n.textContent = text;
    if (cls) n.className = cls;
    return n;
  };
  const button = (text, fn, primary = false) => {
    const b = node(
      'button',
      text,
      primary ? 'btn btn-primary btn-sm' : 'btn btn-sm folder-agent-card__secondary'
    );
    b.type = 'button';
    b.addEventListener('click', fn);
    return b;
  };
  const section = node('section', null, 'folder-agent-card__organization');
  const panel = node('div');
  const status = node('p', null, 'folder-agent-card__note');
  status.setAttribute('role', 'status');
  const open = button(
    'Read documents and suggest groups',
    () => (expanded ? close() : showHistory()),
    true
  );
  open.setAttribute('aria-expanded', 'false');
  let generation = 0,
    expanded = false;
  const close = () => {
    generation++;
    expanded = false;
    panel.replaceChildren();
    status.textContent = '';
    open.setAttribute('aria-expanded', 'false');
    open.focus();
  };
  const start = message => {
    expanded = true;
    open.setAttribute('aria-expanded', 'true');
    panel.replaceChildren();
    status.textContent = message;
    return ++generation;
  };
  const read = async (path, options = {}) => {
    const r = await fetchImpl(`${base}/${path}`, { cache: 'no-store', ...options });
    if (!r.ok) throw new Error();
    return r.json();
  };
  const post = (path, body) =>
    read(path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body)
    });
  const dismiss = () => button('Not now', close);
  const failed = ticket => {
    if (ticket !== generation) return;
    status.textContent =
      'This scope changed or access is unavailable. Check the workspace and review again.';
    panel.replaceChildren(button('Check again', showHistory), dismiss());
  };
  const listFiles = (parent, files) => {
    const list = node('ul');
    for (const item of files)
      list.append(
        node(
          'li',
          `${item.source.path} — ${item.status}${item.truncated ? ' (excerpt limited)' : ''}`
        )
      );
    parent.append(list);
  };
  const history = reviews => {
    const ticket = start('Organizing reviews');
    const sources = new Map();
    for (const review of reviews)
      for (const file of review.files) sources.set(file.source.ref, { review, file });
    for (const r of reviews.slice().reverse()) {
      const details = node('details');
      details.open = r === reviews.at(-1);
      details.append(node('summary', `${new Date(r.created).toLocaleString()} · ${r.status}`));
      details.append(node('p', `${r.processor.model} · ${r.processor.name}`));
      if (r.status === 'reading')
        details.append(
          node('p', 'A review is in progress. Check again for the result; nothing will be resent.')
        );
      else
        details.append(
          node(
            'p',
            `${r.read} readable · ${r.skipped} skipped · ${r.remaining} more candidates seen${r.partial ? ' (partial listing)' : ''}.`
          )
        );
      listFiles(details, r.files);
      for (const f of r.files.filter(x => x.placement)) {
        details.append(
          node(
            'p',
            `Suggested for ${f.source.path}: ${f.placement.destination} (${f.placement.convention ? 'from your Documents convention' : 'based on your confirmed placements'}).`
          )
        );
        if (f.placement.convention) continue;
        details.append(button('Remember for Documents', () => preview([], [], null, true)));
        const proof = node('details');
        proof.append(node('summary', 'Show those decisions'));
        const decisions = reviews.flatMap(review => review.decisions || []);
        for (const id of f.placement.support) {
          const d = decisions.find(row => row.id === id);
          if (d)
            proof.append(
              node(
                'p',
                `${new Date(d.time).toLocaleString()} · ${d.proposed} → ${d.choice} · ${d.outcome}`
              )
            );
        }
        details.append(proof);
      }
      for (const group of (r.groups || []).filter(g => !g.dismissed)) {
        details.append(node('h4', group.title));
        const change = async (action, extra = {}) => {
          const n = start('Saving your choice…');
          try {
            await post('organization-view', {
              review: r.id,
              revision: r.revision,
              group: group.id,
              action,
              ...extra
            });
            if (n === generation) await showHistory();
          } catch {
            failed(n);
          }
        };
        if (!group.kept)
          details.append(
            button('Keep as a view', () => change('keep')),
            button('Not now — dismiss group', () => change('not_now'))
          );
        else {
          details.append(node('p', 'Saved view · no files moved'));
          const name = node('input');
          name.value = group.title;
          name.maxLength = 50;
          name.setAttribute('aria-label', 'Group name');
          details.append(
            name,
            button('Rename group', () => change('rename', { name: name.value }))
          );
        }
        const list = node('ul');
        for (const ref of group.members) {
          const origin = sources.get(ref);
          if (!origin) continue;
          const f = origin.file;
          const row = node('li', `${f.source.path} — ${group.reasons[ref]}`);
          row.append(
            button('Open original', async () => {
              try {
                await post('organization-source', { review: origin.review.id, source: ref });
                if (generation === ticket) status.textContent = 'Opened in the system viewer.';
              } catch {
                if (generation === ticket)
                  status.textContent =
                    'The original could not be opened. It may have changed, or the desktop viewer is unavailable.';
              }
            })
          );
          if (group.kept) {
            row.append(button('Leave loose', () => change('loose', { source: ref })));
            for (const target of reviews)
              for (const dest of (target.groups || []).filter(
                other => other.kept && other !== group && !other.members.includes(ref)
              ))
                row.append(
                  button(`Move to view: ${dest.title}`, () =>
                    change('move_member', {
                      source: ref,
                      to: dest.id,
                      to_review: target.id,
                      to_revision: target.revision
                    })
                  )
                );
          }
          list.append(row);
        }
        details.append(list);
        if (group.kept && group.members.length)
          details.append(
            documentsFilingCheck({ node, button, group, review: r, sources, route, post })
          );
        if (group.example_folder)
          details.append(node('p', `Example folder: ${group.example_folder}`));
      }
      if (r.decisions?.length) {
        const log = node('details');
        log.append(node('summary', 'Organizing history'));
        for (const d of r.decisions) {
          const observed = d.kind === 'observed';
          const row = node('div');
          row.append(
            node(
              'p',
              `${new Date(d.time).toLocaleString()} · ${observed ? `${d.proposed} → ${d.choice}` : `You chose ${d.choice} (was ${d.proposed})`}. ${d.outcome}${d.exception ? ' · Excluded from suggestions' : ''}`
            )
          );
          if (!observed) {
            const names = d.sources.map(ref => sources.get(ref)?.file.source.path).filter(Boolean);
            if (names.length) row.append(node('p', `Documents: ${names.join(', ')}`));
          }
          const mark = async action => {
            const n = start('Updating decision…');
            try {
              await post('organization-view', {
                action,
                decision: d.id,
                review: r.id,
                revision: r.revision
              });
              if (n === generation) await showHistory();
            } catch {
              failed(n);
            }
          };
          if (observed && !d.intentional && !d.exception)
            row.append(button('I placed this intentionally', () => mark('confirm_observation')));
          if (!d.exception) row.append(button('This was an exception', () => mark('exception')));
          log.append(row);
        }
        details.append(log);
      }
      panel.append(details);
    }
    panel.append(
      button(reviews.at(-1)?.status === 'reading' ? 'Check status' : 'Preview another batch', () =>
        reviews.at(-1)?.status === 'reading' ? showHistory() : preview([], [])
      ),
      dismiss()
    );
    return ticket;
  };
  async function showHistory() {
    const ticket = start('Checking saved reviews…');
    try {
      const { reviews } = await read('organization-reviews');
      if (ticket !== generation) return;
      if (reviews.length) history(reviews);
      else await preview([], []);
    } catch {
      failed(ticket);
    }
  }
  async function preview(examples, exclude, focusRef, teach = false) {
    const ticket = start('Checking the scope…');
    const q = new URLSearchParams();
    for (const ref of examples) q.append('example', ref);
    for (const ref of exclude) q.append('exclude', ref);
    try {
      const { preview: p } = await read(`organization-preview?${q}`);
      if (ticket !== generation) return;
      status.textContent = `Scope preview · ${p.files.length} files. Nothing has been read or sent.`;
      const model = p.processor;
      panel.append(
        node(
          'p',
          model.name
            ? `Excerpts go to ${model.name} using ${model.model}.`
            : `No supported processor for ${model.model || 'this lead'}. Configure the lead with a direct Claude API model, then check again.`
        )
      );
      panel.append(
        node(
          'p',
          'Ori compares the locations of previously reviewed files in Documents and the example folders you include (including their Invoices folders). Incomplete listings do not establish a move. No monitoring is enabled.'
        )
      );
      const convention = p.convention;
      if (convention?.text)
        panel.append(node('p', `Organizing convention shared with this batch: ${convention.text}`));
      if (convention?.conflict)
        panel.append(
          node('p', 'Conflicting Documents conventions. Review them in Memory; none will be sent.')
        );
      if (convention?.revision && !convention.conflict) {
        const editor = node('details');
        editor.open = teach;
        editor.append(node('summary', 'Remember for Documents'));
        const input = node('textarea');
        input.value =
          convention.text ||
          (teach ? 'File invoices in the Invoices folder under each property.' : '');
        input.maxLength = 320;
        input.setAttribute('aria-label', 'Organizing convention in your words');
        editor.append(input);
        const save = async text => {
          const n = start('Updating workspace memory…');
          try {
            await post('organization-convention', { revision: convention.revision, text });
            if (n === generation) await preview(examples, exclude);
          } catch {
            failed(n);
          }
        };
        editor.append(
          button('Remember for Documents', () => {
            if (input.value.trim()) void save(input.value.trim());
          })
        );
        if (convention.text) editor.append(button('Forget convention', () => save('')));
        panel.append(editor);
      }
      const choices = (title, rows, folder) => {
        if (!rows?.length) return;
        const field = node('fieldset');
        field.append(node('legend', title));
        for (const opt of rows) {
          const label = node('label', null, 'd-block');
          const checkbox = node('input');
          checkbox.type = 'checkbox';
          checkbox.checked = opt.included;
          checkbox.disabled = folder && !opt.included && examples.length >= 2;
          checkbox.addEventListener('change', () => {
            const set = new Set(folder ? examples : exclude);
            if (folder ? checkbox.checked : !checkbox.checked) set.add(opt.ref);
            else set.delete(opt.ref);
            void preview(folder ? [...set] : examples, folder ? [] : [...set], opt.ref);
          });
          label.append(checkbox, node('span', opt.name));
          field.append(label);
          if (opt.ref === focusRef) queueMicrotask(() => checkbox.focus());
        }
        panel.append(field);
      };
      choices(
        'Loose files',
        p.options?.filter(x => !x.example),
        false
      );
      choices('Include example folders (up to two)', p.folders, true);
      choices(
        'Reference files — never moved',
        p.options?.filter(x => x.example),
        false
      );
      if (!p.files.length)
        panel.append(
          node(
            'p',
            'No selected documents to read. Select a file or add a supported PDF, Word or text document.'
          )
        );
      if (p.remaining || p.partial)
        panel.append(
          node(
            'p',
            `${p.remaining} additional candidates seen.${p.partial ? ' Only part of the folder was examined.' : ''}`
          )
        );
      if (p.approval) {
        panel.append(
          button(
            'Approve this read',
            async () => {
              const request = start('Reading approved documents…');
              const controller = new AbortController();
              panel.append(
                button('Cancel review', () => {
                  controller.abort();
                  close();
                })
              );
              try {
                await read('organization-reviews', {
                  method: 'POST',
                  signal: controller.signal,
                  headers: { 'Content-Type': 'application/json' },
                  body: JSON.stringify({ approval: p.approval, selection: { examples, exclude } })
                });
                if (request === generation) await showHistory();
              } catch {
                failed(request);
              }
            },
            true
          )
        );
      }
      panel.append(dismiss());
    } catch {
      failed(ticket);
    }
  }
  section.append(open, status, panel);
  return section;
}
