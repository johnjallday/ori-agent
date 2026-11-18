# UI consistency — selected review evidence

Exact, unmodified captures selected from milestone evidence. Source filenames,
SHA-256 hashes, bitmap dimensions and byte counts are in [provenance.json](provenance.json).
Original capture JSON accompanies each image: route, build/revision/dirty state,
engine, viewport, theme, font availability and fixture facts. These copies are
not new runs or additional test passes. Fictional owned fixtures only.

## Before / after

| Family | Before | After | Review scope |
| --- | --- | --- | --- |
| Skills | [image](skills-before.png) | [image](skills-after.png) | Chromium, light,320px, offline fallback; shared controls and static surfaces |
| Home Map | [image](home-before.png) | [image](home-after.png) | Chromium, light,320px, offline fallback; artwork retained, aligned surrounding chrome |
| Workspace Note | [image](note-before.png) | [image](note-after.png) | Chromium, light,320px, offline fallback; editor/toolbar geometry. Content fixture evolved; not a pixel-identical document comparison |

## Real states and native zoom

- [Approved/materialized Plan](plan-approved.png): WebKit, dark,320px, intended fonts. Real approvals/effects retained; pending, unassigned and unscheduled is not execution.
- [Notes station](notes-station.png): WebKit, dark,320px, intended fonts. Supported unscheduled HQ setup, verified fresh starter retirement, source-vault bytes unchanged; no model/runs.
- [Team Home](team-home.png): WebKit, dark,320px, intended fonts. Artwork/roster retained; repository host fixture, not a live external application.
- [Opaque surface host](surface-host.png): current WebKit, dark,320px, intended fonts. Actual isolated frame CSS/bridge; host styles do not leak into the frame. Repository example only.
- [Native Chrome agent walkthrough](native-agents.png): actual200% browser preference, light, intended fonts. The walkthrough remains visible beside its reachable Hire target; it was not hidden/dismissed for the test.
- [Native Chrome Plan](native-plan.png): actual200%, dark, intended fonts. Focused Save remains clear of the navbar/visible assistant. Native compositor bitmap1440×813 corresponds to720×406 CSS content inside the unchanged1440×900 outer window.

Native captures read the complete browser compositor without a clip. No CSS zoom,
device-scale emulation, pixel rescaling or retouching. Ordinary Playwright native-
zoom clips were cropped diagnostic evidence and are **not** the selected images.
Scoped checks do not certify Safari browser chrome, OS select popups, assistive
technology, drawn Canvas, published plugins or live readers/DAWs/providers.
