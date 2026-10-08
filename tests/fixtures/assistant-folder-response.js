// Synthetic metadata only. Never import this fixture into the application bundle.
const coverage = {
  max_depth: 3,
  max_entries: 5000,
  budget_seconds: 3,
  partial: false,
  skipped_links: 0,
  projects_omitted: 0,
  kinds_omitted: 0
};
const snapshot = (id, folder, projects = [], extra = {}) => ({
  version: 1,
  id,
  folder,
  scanned_at: '2026-10-05T10:00:00Z',
  files: 12,
  entries: 18,
  kinds: [{ name: '.txt', count: 12 }],
  projects,
  coverage: { ...coverage },
  ...extra
});
const project = (id, name, files, marker = '', root = false) => ({
  id,
  name,
  files,
  ...(marker ? { marker } : {}),
  ...(root ? { root } : {})
});

export const folderResponseFixtures = {
  album: snapshot(
    'synthetic-album',
    'Album collection',
    [
      project('whole', 'Album collection', 12, '', true),
      project('aurora', 'Aurora', 4, 'REAPER'),
      project('tide', 'Tide', 3, 'Logic Pro'),
      project('art', 'Artwork', 2),
      project('notes', 'Notes', 3)
    ],
    {
      kinds: [
        { name: '.rpp', count: 1 },
        { name: '.logicx', count: 1 },
        { name: '.txt', count: 10 }
      ]
    }
  ),
  documents: snapshot('synthetic-documents', 'Research papers', [
    project('whole', 'Research papers', 12, '', true),
    project('reading', 'Reading', 8),
    project('drafts', 'Drafts', 4)
  ]),
  rootOnly: snapshot('synthetic-root', 'Solo project', [
    project('whole', 'Solo project', 12, 'REAPER', true)
  ]),
  empty: snapshot('synthetic-empty', 'Empty folder', [], { files: 0, entries: 0, kinds: [] }),
  oneSetupOption: snapshot('synthetic-mixed', 'Mixed collection', [
    project('whole', 'Mixed collection', 12, '', true),
    project('logic', 'Logic project', 4, 'Logic Pro'),
    project('reaper', 'REAPER project', 4, 'REAPER'),
    project('plain', 'Reference notes', 4)
  ]),
  partial: snapshot(
    'synthetic-partial',
    'Large collection',
    Array.from({ length: 8 }, (_, i) => project(`p${i}`, `Observed folder ${i + 1}`, i + 1)),
    {
      coverage: {
        ...coverage,
        partial: true,
        partial_reason: 'entries',
        projects_omitted: 4,
        kinds_omitted: 2,
        skipped_links: 1
      }
    }
  ),
  hostile: snapshot('synthetic-hostile', '<img onerror=alert(1)>', [
    project('same1', '同じ名前 🎼', 2),
    project('same2', '同じ名前 🎼', 2),
    project('html', '<button onclick=alert(1)>Run setup', 3, 'REAPER'),
    project('long', 'Long name '.repeat(9), 5)
  ]),
  historical: snapshot(
    'synthetic-historical',
    'Saved collection',
    [project('whole', 'Saved collection', 12, '', true), project('old', 'Earlier draft', 12)],
    { scanned_at: '2025-01-10T10:00:00Z' }
  )
};
export const onlySetupCandidate = 'plain'; // Other observed children must remain discussable.
export const folderContentSentinel = 'ORI_SYNTHETIC_ATTACHMENT_BODY_MUST_NOT_REACH_PROVIDER_713';
// Relative to a caller-owned disposable HOME. No real names or contents.
export const syntheticFolderFiles = [
  ['Music/Album collection/Aurora/demo.rpp', folderContentSentinel],
  ['Music/Album collection/Tide/demo.logicx/metadata.txt', folderContentSentinel],
  ['Music/Album collection/Artwork/cover.txt', folderContentSentinel],
  ['Music/Album collection/Notes/note.txt', folderContentSentinel],
  ['Documents/Research papers/Reading/paper.txt', folderContentSentinel],
  ['Documents/Research papers/Drafts/draft.txt', folderContentSentinel]
];
