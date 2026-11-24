// Only an opaque, short-lived selection crosses the same-tab handoff. Paths,
// labels and filenames never enter browser storage, history or query strings.
const handoffKey = 'ori.folderSearchReview.v1';
async function post(op, body) {
  const response = await fetch(`/api/file-search/${op}`, {
    method: 'POST',
    cache: 'no-store',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  });
  if (!response.ok)
    throw new Error(
      'This folder cannot be used for filename search. Choose a local folder outside Ori storage.'
    );
  return response.json();
}
export async function chooseSearchFolder(kind) {
  const { intent } = await post('begin', { kind });
  return post('review', { intent });
}
export function openFolderReview(review) {
  sessionStorage.setItem(handoffKey, review.id);
  location.assign('/?create=1&folder_review=1');
}
export async function resumeFolderReview() {
  const intent = sessionStorage.getItem(handoffKey);
  sessionStorage.removeItem(handoffKey);
  if (!intent) throw new Error('Choose the folder again in Files.');
  return post('resume-review', { intent });
}
