# Custom Workspace Dashboard

Write your own HTML dashboard for a workspace and Ori renders it inside that
workspace, reading live workspace data.

A dashboard is an ordinary file in your workspace folder. There is no in-app
editor, no install step, and no configuration screen — you (or an Ori agent
working on your behalf) put a file in a folder, reload the page, and it is
there.

---

## Quick start

1. Find your workspace folder. It is the directory containing that workspace's
   `workspace.json`.
2. Create `.ori/dashboard/` inside it.
3. Copy the worked example from
   [`docs/examples/workspace-dashboard/`](../examples/workspace-dashboard/) into
   that directory — all four files.
4. Reload the workspace page. A **Dashboard** tab appears beside Details, Map,
   and Tickets.

```
<workspace folder>/
├── workspace.json
└── .ori/
    └── dashboard/
        ├── index.html      ← required entry point
        ├── dashboard.css
        ├── dashboard.js
        └── ori-bridge.js
```

Asking an agent works too, because the target is just a file:

> "Write me a dashboard for this workspace showing my open tasks."

---

## The folder convention

| | |
|---|---|
| Dashboard directory | `<workspace folder>/.ori/dashboard/` |
| Entry point | `index.html` (exactly this name) |
| Other files | Any CSS, JS, images, or fonts in that directory are servable |

`.ori` is Ori's existing sidecar directory inside a workspace folder, already
used for other per-workspace state. It is hidden in Finder — press
<kbd>⌘</kbd><kbd>⇧</kbd><kbd>.</kbd> to reveal it, or navigate there with **Go →
Go to Folder**.

A workspace with no `.ori/dashboard/index.html` has no dashboard: no tab, no
catalog entry, and no change to any screen.

**Detection is live.** The file's presence is checked on every page load, not
cached at startup. Create it and reload; delete it and reload. There is no
restart, no re-registration, and no "refresh dashboards" button.

---

## Writing the HTML

### Styles and scripts must be in separate files

This is the one rule that catches everyone. The dashboard frame is served with:

```
script-src 'self'; style-src 'self'
```

and **no `unsafe-inline`**. So an inline `<script>`, an inline `<style>`, or a
`style="..."` attribute is blocked by the browser and silently does nothing —
you get a blank or unstyled page with no error in the app.

```html
<!-- Does not work -->
<style>body { color: red }</style>
<script>console.log('hi')</script>

<!-- Works -->
<link rel="stylesheet" href="dashboard.css" />
<script src="dashboard.js"></script>
```

Both files live beside `index.html` in the dashboard directory.

### Getting data: the bridge

The dashboard cannot use `fetch()` — `connect-src 'none'` blocks every outbound
request, including to Ori's own API. All data arrives over `postMessage` from
the host.

Copy [`ori-bridge.js`](../examples/workspace-dashboard/ori-bridge.js) into your
dashboard folder unchanged and load it before your own script. It gives you:

```js
await Ori.whenReady();                       // resolves once the host handshake completes
const summary = await Ori.invoke('workspace.summary');
const tasks   = await Ori.invoke('workspace.tasks.list', { status: 'open', limit: 10 });
```

`Ori.invoke` rejects with an `Error` when the host refuses the call, so wrap it
in `try`/`catch` and render your own error state.

### Render data as text, not markup

Use `textContent` (or `createElement`), never `innerHTML`, when rendering
workspace data. Task titles and note names are whatever someone typed.

---

## The v1 operation vocabulary

Six read-only operations. This list is fixed: a dashboard cannot call anything
else, and there is no generic query escape hatch.

Every list operation accepts `limit` (1–100, default 25) and `offset`, and
returns `total`, `limit`, `offset`, and `has_more` alongside its collection.

### `workspace.summary`

Input: `{}`

```jsonc
{
  "id": "…", "name": "Marketing Site", "kind": "workspace",
  "designation": "outpost",            // optional
  "description": "…",                  // optional
  "tags": ["web"],                     // optional
  "counts": { "tasks": 12, "open_tasks": 3, "agents": 2, "notes": 8, "sessions": 5 }
}
```

### `workspace.tasks.list`

Input: `{ "limit": 25, "offset": 0, "status": "open" }` — all optional.
`status` accepts `open` (anything not finished) or an exact status such as
`completed`, `pending`, or `in_progress`. Omit it for every task.

```jsonc
{
  "tasks": [
    { "id": "…", "title": "Ship the landing page", "status": "pending",
      "priority": 2, "assignee": "writer" }
  ],
  "total": 12, "limit": 25, "offset": 0, "has_more": false
}
```

### `workspace.notes.list`

Input: `{ "limit": 25, "offset": 0 }`

```jsonc
{ "notes": [{ "id": "…", "name": "Brand voice" }], "total": 8, "limit": 25, "offset": 0, "has_more": false }
```

Names only — note bodies are not available (see [What a dashboard cannot
do](#what-a-dashboard-cannot-do)).

### `workspace.agents.list`

Input: `{ "limit": 25, "offset": 0 }`

```jsonc
{
  "agents": [
    { "id": "…", "name": "writer", "role": "Copywriter",
      "instance_number": 1, "entry_point": true }
  ],
  "total": 2, "limit": 25, "offset": 0, "has_more": false
}
```

### `workspace.sessions.list`

Input: `{ "limit": 25, "offset": 0 }`

```jsonc
{
  "sessions": [
    { "title": "Kickoff", "agent_name": "writer", "updated_at": "2026-09-01T12:00:00Z" }
  ],
  "total": 5, "limit": 25, "offset": 0, "has_more": false
}
```

### `workspace.files.list`

Lists the workspace's `files/` directory. Input: `{ "limit": 25, "offset": 0 }`

```jsonc
{
  "files": [
    { "name": "brief.md", "size": 2048, "is_dir": false,
      "modified_at": "2026-09-01T12:00:00Z" }
  ],
  "total": 1, "limit": 25, "offset": 0, "has_more": false
}
```

### Response size

Responses are bounded. A page that would exceed the host's message budget is
returned with fewer entries and `has_more: true` — so **always page with
`offset` until `has_more` is false** rather than assuming `limit` was honored.

---

## Iterating on a dashboard while it is open

Edit the file in your own editor and **reload the workspace page**. There is no
live reload; the frame is not watching the filesystem.

Reloading is enough for CSS and JS changes too: Ori fingerprints the whole
dashboard directory, so editing any file changes the version in the asset URLs
and the browser fetches the new bytes rather than a cached copy.

If you keep the page open and edit the file, the currently open frame keeps
running the old code until you reload. A frame open across an edit may show a
"no longer available" error on its next asset request — reload and it is fine.

### When something goes wrong

The frame is sandboxed and opaque, so browser devtools will not show you much
about the page inside it. Ori therefore reports failures itself, in the
Dashboard tab, naming the file:

> Ori could not open `/…/.ori/dashboard/index.html`. It is empty.

The tab stays visible when a dashboard is refused, precisely so you can read
that message. A dashboard is refused when `index.html` is empty, larger than
8 MiB, or unreadable.

If your page renders but looks unstyled or does nothing, the cause is almost
always inline `<style>`/`<script>` — see [Styles and scripts must be in
separate files](#styles-and-scripts-must-be-in-separate-files).

---

## What a dashboard cannot do

These are deliberate boundaries, not gaps waiting to be filled. Read them before
designing against the feature.

A dashboard **cannot**:

- **Make network requests of any kind.** No `fetch`, no `XMLHttpRequest`, no
  WebSocket, no analytics beacon, no external image, font, or script. Everything
  it loads must live in its own dashboard folder. This is what makes it safe to
  paste a dashboard someone else wrote.
- **Reach Ori's API**, even though the app is on the same host.
- **Read or write anything outside its own workspace.** Another workspace's data
  is unreachable, including by naming that workspace — the workspace is decided
  by the host, and a workspace id sent in operation input is rejected.
- **Read secrets.** Vault contents, API keys, and provider credentials are not
  in the vocabulary at all. Neither are agent system prompts or per-workspace
  prompt refinements.
- **Read the content of anything.** Notes return names, sessions return titles,
  files return names and sizes, tasks return titles and status. Note bodies,
  session transcripts, file contents, and task details/results are not
  available.
- **Change anything.** Every operation is read-only. A dashboard cannot create
  or edit a task, note, or file, cannot run an agent, and cannot send mail.
- **Interact with the surrounding page.** It cannot read `parent.document`,
  cookies, `localStorage`, or anything else belonging to Ori — the frame runs on
  an opaque origin with no `allow-same-origin`.
- **Ask Ori a question, create a task, or open Setup.** Those host actions exist
  for plugin surfaces and are refused for dashboards.
- **Be shared or installed.** There is no marketplace and no import mechanism;
  copying files between folders is the whole distribution story.
- **Appear anywhere else.** Dashboards render in workspace detail only — not on
  Home, not in the Action Center.

Write actions are a plausible v2 and nothing in the design forecloses them, but
they are not in this release. Direct API access is not a deferral — it is a
standing boundary.

---

## Security model

The protection is structural rather than procedural. A dashboard is untrusted
by construction, and the boundaries hold whether or not anyone reviewed the
file:

| Boundary | Mechanism |
|---|---|
| No network egress | `connect-src 'none'` in the frame's Content-Security-Policy |
| No access to the app page | `<iframe sandbox="allow-scripts">` — no `allow-same-origin`, so the document has an opaque origin |
| No cookies or credentials | `credentialless` frame; `referrerpolicy="no-referrer"` |
| No inline code execution | `script-src 'self'; style-src 'self'` with no `unsafe-inline` |
| Files stay in the folder | Path containment with symlink rejection at every component, an 8 MiB per-file cap, and a MIME allowlist |
| Data stays in the workspace | The workspace comes from trusted host context; input schemas reject a supplied workspace id |
| Nothing can be changed | Every operation is declared read-only |

Each of these has a test asserting it individually, so a regression names which
barrier fell.

---

## Shipped behavior for the design's open questions

Recorded here so the decisions are discoverable rather than folklore:

1. **Folder is `.ori/dashboard/`.** It reuses Ori's existing sidecar directory
   instead of taking a visible `dashboard/` at the workspace root, which would
   collide with real project folders. The cost is discoverability in Finder,
   noted above.
2. **The asset version is a fingerprint of the whole dashboard directory** — the
   entry file by content, every other file by path, size, and modification time.
   The entry file is never cached, so its own hash does not affect freshness;
   sibling assets are cached aggressively and are what would otherwise go stale.
3. **The bridge helper ships in the example rather than at a reserved URL.**
   Copy `ori-bridge.js` into your folder. Serving a host-provided helper from a
   reserved path inside the user's own asset namespace is a design commitment
   that was not worth making yet.
4. **The dashboard view is URL-addressable** as `?mode=dashboard`, so a link to
   it survives reload and sharing. A link naming a workspace with no dashboard
   falls back to Details. (Tickets is not URL-addressable; making it so is a
   separate change.)
5. **Deleting the file removes the tab on the next load.** A frame already open
   is not polled; it surfaces an error on its next request instead.
6. **Group workspaces support dashboards** exactly like concrete ones. Both
   render the same workspace page, and special-casing groups would add a rule
   with no user-visible justification.

---

## Related

- Worked example: [`docs/examples/workspace-dashboard/`](../examples/workspace-dashboard/)
- Manual test guide: [`tasks/test-guide-331-workspace-dashboard-template.md`](../../tasks/test-guide-331-workspace-dashboard-template.md)
