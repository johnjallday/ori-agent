# HQ onboarding reveal — review evidence

Sanitized, credential-free disposable-server captures. Atlas and the note text
are fictional fixtures; no personal workspace or Notes content is included.
These are review artifacts, not runtime character assets.

| Capture | What was exercised |
| --- | --- |
| [Ready](ready.webp) | Chromium: reviewed creation, canonical HQ on Map, compact welcome outside chat |
| [Saved](saved.webp) | Chromium: explicit first-note capture, canonical acknowledgment and matching file content |
| [Mobile editor](webkit-390-editor.webp) | Playwright WebKit, 390px CSS viewport: actual HQ above the focused editor, reachable actions |
| [200% editor](zoom-editor.webp) | Actual Chromium browser zoom from a disposable profile, not CSS/pinch scaling; HQ and all editor controls hit-tested |
| [Notes warning](notes-warning.webp) | Real occupied sandbox folder: HQ succeeds, existing content remains untouched, Notes-only recovery offered |

Reproduce with `scripts/e2e-fresh.sh` and the corresponding
`tests/hq-onboarding-reveal*.spec.ts` / `tests/hq-notes-onboarding.spec.ts`.
Each spec owns a fresh server. The resume spec additionally requires
`--sandbox-env ORI_HQ_REVEAL_SANDBOX`; the Notes recipient suite requires
`--sandbox-env ORI_NOTES_FIRST_ONBOARDING_SANDBOX`.

The result spec separately labels injected capability/status and visualViewport
fixtures. Its keyboard/touch tests do not imply physical device coverage.
Playwright WebKit is not native Safari or Wails. Physical iOS/Android keyboards
and screen-reader speech were not exercised. These stills alone do not prove
animation timing, durable identity, or file preservation; those are asserted by
the associated real-server and module tests.
