# Character Asset Provenance Register

Tracked review record for every character asset shipped by Ori, required by
`tasks/prd-cozy-character-experience.md` FR-103–FR-114. Each entry in
`internal/charactercatalog/catalog.json` links here by anchor, and
`internal/charactercatalog` fails to load if any entry omits its link.

**This register is not a legal opinion.** Per FR-108, a search that returns
nothing is recorded as *"no concerning match found in the searched index"* — never
as proof that an asset is unique, non-infringing, or cleared. Where a check was
not performed, it says so plainly rather than being left blank.

## How V1 art was made

All V1 character art is **hand-authored SVG committed to this repository**. It is
not AI-generated, not traced, and not derived from any reference sheet, stock
image, texture, brush, or third-party font. Each mark is built from primitive
shapes and the palette tokens declared in the catalog entry.

This is a deliberate choice recorded in the feature's drift log: it makes the
originality basis *authorship* rather than *search*, which is the only basis this
project can actually establish for itself.

Consequences for the FR-103–FR-114 checklist:

| Requirement | Status for V1 | Why |
|---|---|---|
| FR-104 — no imitation prompt/brief | **N/A** | No generative prompt exists. The brief is the catalog entry's `silhouette` / `signature_prop` / `description`, written to describe Ori's own shapes. |
| FR-105 — generation provider terms | **N/A** | No generation provider was used, so no provider terms govern these files. |
| FR-106 — provenance record | **Complete** | This register; one anchored record per asset below. |
| FR-107 — reverse-image + semantic search | **NOT PERFORMED** | See "Outstanding checks" below. Recorded as an absence of evidence, not a clean result. |
| FR-109 — trademark/name search | **OUTSTANDING for `Ori` only** | The eight working characters are named for their roles in ordinary English, so there is no distinctive mark to search. See the naming decision below. |
| FR-111 — third-party licenses | **None used** | Enforced mechanically: `validateAssetPath` rejects any URL, absolute path, or path outside `characters/`, so no external asset can enter the catalog. |
| FR-112 — post-optimization hash | **Complete** | SHA-256 of each shipped file recorded below; regenerate with `scripts/character-asset-hashes.sh`. |
| FR-113 — concept art is not production art | **Enforced** | The concept PNGs under the gitignored `docs/design/` are never referenced by the catalog or shipped; production assets are the tracked SVGs under `internal/web/static/characters/`. |
| FR-114 — assignment by ID, not filename | **Complete** | Agents store a catalog ID. Replacing an asset is a file swap plus a hash update here; no agent record changes. |

## Naming decision (2026-08-04)

**Working characters are named for the role they depict, not with invented
proper nouns.** The catalog originally used single-word names — Sable, Piko,
Rivet, Moss, Luma, Nox, Cairn, Tock — carried over from the concept lineup.
Those were dropped before any search was run, for two reasons:

1. Single common words are exactly where trademark collisions cluster, and a
   quick look at the entertainment category surfaced plausible conflicts for
   most of that set (existing game titles and named game characters). Each one
   would have needed the FR-110 "rename or professionally clear" conversation.
2. A role reads more usefully next to an agent's own name. `Character: Research
   Archivist` tells a user something; `Character: Sable` does not.

The names are now the roles themselves, and the catalog IDs are their slugs.
`internal/charactercatalog/catalog_test.go` enforces both, so the policy cannot
erode one entry at a time.

This removes the FR-109 obligation for eight of the nine names outright: a
descriptive role phrase in ordinary English is not a distinctive mark, and none
of them is being used as a brand.

## Outstanding checks (block FR-128 acceptance)

1. **FR-109 trademark / name search — the name `Ori` only.** This is the
   product's own name as well as the guide character's, so the decision sits
   above this feature. Search USPTO (and EUIPO/WIPO for other launch markets)
   in Nice classes 9 (downloadable software), 42 (SaaS), and 41 (entertainment,
   which is where a character name matters); add 25/28 only if merchandise is
   ever in scope. Record the register searched, date, classes, and result below,
   phrased per FR-108 as "no concerning match found in the searched index" —
   never as "cleared". A conflict blocks the name until renamed or
   professionally cleared (FR-110).

   Known context worth capturing in the record: *Ori and the Blind Forest* /
   *Ori and the Will of the Wisps* (Moon Studios / Xbox Game Studios) use **Ori**
   as a named game character. That is a different category from productivity
   software, but it is exactly the kind of match FR-110 expects to be
   documented and reasoned about rather than passed over.

2. **FR-107 reverse-image and semantic-similarity search** — not applicable to
   hand-authored vector marks and recorded as not performed. If raster exports
   are ever published (app-store art, marketing, merchandise), run TinEye or
   equivalent plus one semantic visual search on the exported raster and record
   the results here.

Until item 1 is complete, the name `Ori` should not be treated as cleared, and
FR-128 remains unsatisfied.

## Review template

Copy this block for every new or replaced asset.

```markdown
### <character-id>

- **Catalog ID:** <id> (`internal/charactercatalog/catalog.json`)
- **Entry version:** <n>
- **Assets:**
  - `characters/<id>/portrait.svg` — sha256 `<hash>`
  - `characters/<id>/sprite.svg` — sha256 `<hash>`
  - `characters/<id>/static.svg` — sha256 `<hash>`
- **Creation source / tool:** <e.g. hand-authored SVG, editor used>
- **Creation date:** <YYYY-MM-DD>
- **Prompt or brief summary:** <the described traits; must not name a franchise, artist, or existing character>
- **Provider terms version / review date:** <version + date, or N/A with reason>
- **Reference licenses:** <source + license, or "none used">
- **Human edits:** <what a person changed after first output>
- **Reverse-image search:** <service, date, result — or NOT PERFORMED + reason>
- **Semantic visual search:** <service, date, result — or NOT PERFORMED + reason>
- **Trademark / name search:** <registers searched, date, categories, result — or OUTSTANDING>
- **Reviewer:** <name>
- **Review date:** <YYYY-MM-DD>
- **Decision:** approved | blocked | needs redesign
```

## Records

Common to every V1 record below, stated once rather than repeated:

- **Creation source / tool:** hand-authored SVG, written directly as source in this repository
- **Creation date:** 2026-08-03
- **Provider terms:** N/A — no generation provider used
- **Reference licenses:** none used — no third-party image, texture, brush, or font
- **Human edits:** entire asset is human-authored; there is no machine output to edit
- **Reverse-image search:** NOT PERFORMED — no generated raster exists and no external index was queried; originality basis is authorship (see FR-108 note above)
- **Semantic visual search:** NOT PERFORMED — same reason
- **Reviewer:** unreviewed — pending project-owner sign-off
- **Review date:** —
- **Decision:** **provisional** — art may ship as V1 presentation; final approval blocked on the FR-109 name search

### ori-guide

- **Catalog ID:** `ori-guide` — **reserved**, may never be assigned to a working agent
- **Entry version:** 1
- **Brief:** a duck navigator with a broad bill, crown tuft, and open navigator stance, carrying a folded map and a location-pin satchel. Warm, concise, gently curious. Deliberately *not* modelled on any existing mascot, franchise creature, or brand character.
- **Name review:** OUTSTANDING — "Ori" is also this application's own product name; the search must cover both the product and character use.
- **Assets:** see `scripts/character-asset-hashes.sh` output committed below.

The eight working characters below share one name review, because they share one
reason: each is named for the role it depicts, in ordinary English. **Name
review: N/A — descriptive role phrase, not a distinctive mark, and not used as a
brand.** See the naming decision above for what was dropped and why.

### research-archivist

- **Catalog ID:** `research-archivist` · **Entry version:** 1
- **Brief:** wide-browed resident in a feather cape with a grounded stance, carrying a pocket ledger and reading lenses. Measured, precise, gently skeptical.

### project-coordinator

- **Catalog ID:** `project-coordinator` · **Entry version:** 1
- **Brief:** round-tailed resident with a forward stance and open expression, wearing a cross-body planner satchel. Upbeat, concise, socially aware.

### product-builder

- **Catalog ID:** `product-builder` · **Entry version:** 1
- **Brief:** compact resident with broad hands and planted feet, wearing a modular tool belt. Practical, direct, quietly playful.

### team-caretaker

- **Catalog ID:** `team-caretaker` · **Entry version:** 1
- **Brief:** soft rectangular resident with relaxed shoulders, carrying a messenger pouch and a living sprout. Calm, reassuring, never vague.

### insight-researcher

- **Catalog ID:** `insight-researcher` · **Entry version:** 1
- **Brief:** light-framed familiar with tall ears and folded crescent wings, holding a tabbed field notebook. Curious, lyrical, evidence-grounded.

### decision-strategist

- **Catalog ID:** `decision-strategist` · **Entry version:** 1
- **Brief:** alert familiar with tall ears and a diagonal cape, holding a map tube and a decision token. Candid, economical, respectfully challenging.

### operations-keeper

- **Catalog ID:** `operations-keeper` · **Entry version:** 1
- **Brief:** stacked rounded stones with a bright central core and modular utility pouches. Steady, transparent, low-drama.

### automation-specialist

- **Catalog ID:** `automation-specialist` · **Entry version:** 1
- **Brief:** narrow bird-profiled construct with mechanical joints, a wind-up key, and a status lens. Exact, brisk, surprisingly personable.

## Asset hashes

Regenerate after any asset change:

```bash
./scripts/character-asset-hashes.sh
```

The committed output lives in `docs/character-asset-hashes.txt`. A mismatch
between that file and the working tree means a shipped asset differs from its
reviewed record and must be re-reviewed before release (FR-112).
