// A read-only projection of the current page. Each control hands exactly one
// candidate back to Filing; the ordinary picture's bulk File button is absent.
export function renderDownloadsReview(doc, files, picture, choices, actions) {
  const make = (tag, cls, text = '') => {
    const n = doc.createElement(tag);
    n.className = cls;
    n.textContent = text;
    return n;
  };
  const section = make('section', 'fj-frame fj-downloads-routing');
  section.setAttribute('aria-labelledby', 'fjRoutingTitle');
  const heading = make('h3', 'fj-frame-title', 'Where do these downloads belong?');
  heading.id = 'fjRoutingTitle';
  section.append(
    heading,
    make(
      'p',
      'fj-sub',
      'Review one file at a time. Nothing moves until you confirm its destination.'
    )
  );
  const examples = new Map((picture.folder_suggestions || []).map(e => [e.candidate_id, e]));
  const selected = new Map(choices.map(c => [c.candidate_id, c]));
  let count = 0;
  for (const file of files) {
    const choice = selected.get(file.id);
    if (
      !['pending', 'approved'].includes(file.state) ||
      file.decision === 'trash' ||
      choice?.operation === 'trash'
    )
      continue;
    count++;
    const row = make('article', 'fj-route-file');
    row.append(make('h4', 'fj-label', file.name));
    const example = examples.get(file.id);
    row.append(
      make(
        'p',
        'fj-sub',
        example
          ? `You filed ${example.name} in ${example.folder}.`
          : 'No saved folder example matches this file.'
      )
    );
    const buttons = make('div', 'fj-shelf-tools');
    const add = (key, label, fn, primary = false) => {
      const b = make('button', `fj-btn fj-btn-${primary ? 'primary' : 'secondary'}`, label);
      b.type = 'button';
      b.id = `fj-route-${file.id}-${key}`;
      b.addEventListener('click', fn);
      buttons.append(b);
    };
    if (example)
      add('example', 'Review filing', () => actions.folder(file, example.action_id), true);
    add('choose', example ? 'Choose another folder' : 'Choose a folder', () =>
      actions.folder(file)
    );
    add('leave', 'Leave in Downloads', () => actions.leave(file, choice));
    row.append(buttons);
    section.append(row);
  }
  if (!count)
    section.append(make('p', 'fj-sub', 'No files on this page are waiting for a destination.'));
  return section;
}
window.DownloadsDestinationReview = { renderDownloadsReview };
