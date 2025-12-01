# Agents Edit Page Tasks

## Summary
- Added `agents-edit.html` to allow updating agent description, tags, avatar color, and favorite flag.
- Hooked detail page "Edit" action to navigate to the new edit page and persist changes via `PATCH /api/agents/{name}`.

## Plan & Status
- [x] Create edit page shell and wire navigation from detail view.
- [x] Load agent metadata via `/api/agents/{name}/detail` and prefill fields.
- [x] Support editing description, tags, avatar color, favorite flag, name, type, role, and model.
- [x] Persist updates to `/api/agents/{name}` with PATCH (renames supported) and redirect back to details.
- [ ] Add toasts/inline validation (future).
- [ ] Add integration/UX test coverage (future).

## Files Touched
- `internal/web/static/agents-edit.html`
- `internal/web/static/js/agents-edit.js`
- `internal/web/static/js/agents-detail.js`
