# Ori Evolution System Plan (Revised)

## 0. Implementation Status (reconciled 2026-08-03)

The plan below is the original design document, kept as-written for historical
context. This section is the current, evidence-checked status against the
Rollout Roadmap in section 7.

| Phase | Status |
|---|---|
| 0 - Data Integrity | Partial |
| 1 - Core XP + Stages | Done |
| 2 - Feeding | Done |
| 3 - Specialization Paths | Done |
| 4 - Hatch + Handoff Suggestions | Partial |
| 5 - UX and Observability | Partial |

- **Phase 0 (Partial):** The persistence bug this phase targets is fixed -
  `internal/store/file_store.go` now saves and loads the agent's `Evolution`
  field in full, with `normalizeLoadedAgent` initializing defaults when nil.
  What's still missing is the phase's own named exit criterion: a restart
  round-trip test asserting `Evolution` specifically survives (the existing
  `TestFileStore_SaveLoad_NestedRoundTrip` in that package never sets or
  checks it; `internal/onboarding/manager_test.go` has that kind of test, but
  only for the separate `AssistantProgress` object).
- **Phase 1 (Done):** XP formula, the five stage thresholds, duplicate-message
  suppression, the hourly XP cap, and no-XP-on-failure are all implemented in
  `internal/evolution/service.go` and wired from every chat provider handler
  through `trackAgentStatistics`. Substantial test coverage in
  `internal/evolution/service_test.go`. The optional +0-20 quality bonus from
  section 4.3 remains unimplemented, matching the plan's own "phase-gated"
  framing.
- **Phase 2 (Done):** `POST /api/agents/{name}/feed` is implemented in
  `internal/evolutionhttp/handler.go` with validation, source tagging, and
  both unit and end-to-end integration test coverage.
- **Phase 3 (Done):** Path selection is gated to the Learner stage and, beyond
  being stored, is actually consumed - `internal/chathttp/provider_common.go`
  appends path-specific system-prompt guidance and reprioritizes the tool
  list by path, unit-tested in `internal/chathttp/evolution_prompt_test.go`.
- **Phase 4 (Partial):** `GetSuggestions` in `internal/evolution/service.go`
  is a real keyword-based intent-classification engine, not a stub, and every
  suggestion it emits sets `RequiresApproval: true`; the `evolution.AgentStore`
  interface it depends on has no `CreateAgent` method, so it is structurally
  incapable of auto-hatching an agent. What's missing is any frontend: nothing
  under `internal/web/static/js` calls `/api/evolution/suggestions` or
  displays a suggestion, so there is no way for a user to see or act on one
  yet.
- **Phase 5 (Partial):** The sidebar rank/XP display, agent-card stage badges
  and XP bar, and a stage-up toast are real and wired
  (`internal/web/static/js/modules/sidebar.js`, `agents.js`,
  `dashboard-agents.js`, `agents-roster.js`, `stage-up-toast.js`). The
  evolution-specific activity log (`internal/agenthttp/activity_log.go`,
  routed at `GET .../activity`) has no frontend consumer - the dashboard's
  activity feed reads a separate, unrelated orchestration endpoint - so
  "support can diagnose progression issues from logs" is only true via raw
  JSONL files or direct API calls today.

Data-model drift from section 6.1 worth knowing before reading further: the
plan's `XP`/`Stage`/`Path` fields shipped as `Experience` (json `"experience"`)
and typed `AgentStage`/`AgentPath`; `AwardAssistantXP` from section 6.3 was
never built as a separate entry point (assistant XP instead mirrors every
agent XP award 1:1, resolving open question 11.1 in favor of strict
sum-of-agent-XP by default); and `EvaluateStageTransitions` exists but has no
callers - stage transitions happen inline inside the unexported `awardXP()`.

Also: `internal/progression` (quest-log onboarding, `t2-build-hq`-style quest
IDs, surfaced by `progression-widget.js`) is a **different, unrelated**
system from the agent-leveling "Ori Evolution System" this document
describes. Don't conflate the two when searching for "progression".

## 1. Vision
Ori should evolve from a static set of agents into a system where:
- the main assistant progresses globally as the primary assistant profile,
- each agent progresses individually based on real usage,
- specialization and handoff are explicit and useful (not cosmetic).

The goal is better long-term productivity and higher task completion quality, not just gamification.

## 2. Product Goals
1. Increase repeat usage by making agent growth visible and meaningful.
2. Improve task-agent matching through specialization and handoff suggestions.
3. Preserve user trust with deterministic progression rules and safe automation boundaries.
4. Keep rollout low-risk with backward-compatible persistence and feature flags.

## 3. Non-Goals (Initial Release)
- Fully autonomous agent creation without user approval.
- Hidden scoring rules that users cannot inspect.
- Cross-device synced progression (local-only first).
- Revenue/perks economy system.

## 4. Evolution Model

### 4.1 Dual Progression
- Assistant Level (global): reflects overall usage and unlocks orchestration-level capabilities.
- Agent Level (per agent): reflects how mature each agent is for its role.

### 4.2 Agent Stages
| Stage | Level Range | Intent | Primary Mechanic | Functional Unlock |
| --- | --- | --- | --- | --- |
| Spark | 0-1 | First-use onboarding | Hatch prompt | Chat only |
| Infant | 2-9 | Early learning | Feeding + baseline XP | Better memory/context retention hints |
| Learner | 10-24 | Directed growth | Path selection | Path-tuned prompt/tool defaults |
| Expert | 25-49 | Reliable specialist | Optimization | Faster workflows + cleaner defaults |
| Sentient | 50+ | Proactive assistant | Guardrailed suggestions | Propose workflows/sub-agent plans (approval required) |

### 4.3 XP Rules (v1)
- Per completed user-agent exchange: `10 XP + floor(total_tokens / 100)`.
- Feed action bonus: `+25 XP` for validated feed input.
- Quality bonus (optional, phase-gated): `+0-20 XP` based on explicit user feedback.
- Anti-gaming guardrails:
  - Ignore duplicate messages with same normalized content within short window.
  - Cap XP per agent per hour.
  - Do not grant XP on failed or cancelled requests.

## 5. Current-State Constraints to Address
1. `internal/store/file_store.go` writes `status/statistics/metadata` but the nested `agent_settings.json` loader only restores `type/settings/plugins`; progression fields would be lost on restart.
2. XP tracking currently piggybacks on usage tracking in `internal/chathttp/handlers_helpers.go` via `trackAgentStatistics`; progression logic should not be tightly coupled to cost estimation.
3. Existing `types.AgentStatistics` is usage-oriented; progression metadata should be isolated for clean ownership and easier migration.

## 6. Technical Design

### 6.1 Data Model Changes
- Add a dedicated evolution object instead of overloading statistics:
  - `internal/types/agent_extended.go`:
    - `type AgentEvolution struct { Level int; XP int64; Stage string; Path string; ParentID string; FeedCount int64; LastEvolvedAt time.Time }`
  - `internal/agent/agent.go`:
    - Add `Evolution *types.AgentEvolution`.
- Add assistant progression state:
  - `internal/types/types.go`:
    - Add `AssistantProgress` to `AppState` (level, xp, rank, unlocks).

### 6.2 Persistence and Migration
- Update `internal/store/file_store.go` load path for nested agents to restore all persisted fields:
  - role, capabilities, settings, plugins, mcp_servers, status, statistics, metadata, evolution.
- Migration strategy:
  - If `Evolution == nil`, initialize defaults (`Level=0`, `XP=0`, `Stage="spark"`).
  - Preserve backward compatibility with both old flat agent JSON and new nested settings.

### 6.3 XP Engine
- Introduce `internal/evolution/service.go` as the single entry point:
  - `AwardMessageXP(agentName, tokenCount, context)`
  - `AwardFeedXP(agentName, feedMeta)`
  - `AwardAssistantXP(...)`
  - `EvaluateStageTransitions(...)`
- Call service from chat flow after successful responses (not from every token callback).
- Keep cost tracking and XP logic separate.

### 6.4 APIs
- `GET /api/evolution/assistant` -> assistant progress + unlocks.
- `GET /api/agents/{name}/evolution` -> agent level/stage/path/xp.
- `POST /api/agents/{name}/feed` -> feed context + feed source metadata.
- `GET /api/evolution/suggestions` -> hatch/handoff recommendations with confidence and reason.

## 7. Rollout Roadmap

### Phase 0: Data Integrity (must ship first)
- Fix nested load/save symmetry in `file_store.go`.
- Add migration defaults for missing evolution state.
- Exit criteria:
  - Restart persistence test proves progression survives process restart.

### Phase 1: Core XP + Stages
- Implement evolution service and stage transitions.
- Wire successful chat completion to XP award.
- Exit criteria:
  - Unit tests for XP math and stage boundaries.
  - Integration test for command/agent XP accumulation.

### Phase 2: Feeding
- Implement feed endpoint + validation and source tagging.
- Grant feed XP only on accepted feed payloads.
- Exit criteria:
  - API contract tests for valid/invalid feed payloads.
  - Feed actions visible in agent evolution state.

### Phase 3: Specialization Paths
- Add path selection at Learner stage (Coder/Researcher/Writer initial set).
- Apply path to prompt/tool defaults with explicit user visibility.
- Exit criteria:
  - Path selection persisted.
  - Observable behavioral delta in system prompt assembly.

### Phase 4: Hatch + Handoff Suggestions
- Add suggestion engine based on repeated task pattern detection.
- Require explicit user confirmation before creating/hatching any agent.
- Exit criteria:
  - Suggestion precision targets met in dogfood usage.
  - Zero auto-created agents without explicit user action.

### Phase 5: UX and Observability
- Sidebar user rank/XP, agent card stage badges, progress bars.
- Add event logs for evolution transitions and feed actions.
- Exit criteria:
  - UI reflects backend state accurately after reload.
  - Support can diagnose progression issues from logs.

## 8. Success Metrics
1. 7-day retention uplift among users with evolution enabled.
2. Increase in multi-agent usage (users with >= 2 active agents/week).
3. Feed adoption rate (users performing at least one feed action/week).
4. Improved task completion rate for specialized agents vs non-specialized baseline.
5. Low support incident rate for progression bugs (especially persistence loss).

## 9. Risks and Mitigations
- Risk: Users game XP with spam.
  - Mitigation: duplicate detection, XP caps, failed-request exclusion.
- Risk: Progression feels cosmetic.
  - Mitigation: tie each stage to real functional unlocks.
- Risk: Autonomous behavior reduces trust.
  - Mitigation: explicit approval gates and user-visible reasoning.
- Risk: Migration regressions.
  - Mitigation: backward-compatible loader + restart integration tests.

## 10. Test Plan
- Unit tests:
  - `internal/evolution/service_test.go` for XP math, stage transitions, caps.
  - Extend `internal/types/agent_extended_test.go` for evolution defaults.
- Integration tests:
  - Add persistence restart tests around `internal/store/file_store.go`.
  - Add API tests for feed and evolution endpoints.
- Manual checks:
  - Level/stage progression in UI after restart.
  - Handoff suggestions appear only with sufficient evidence.

## 11. Open Questions
1. Should assistant XP be strictly sum-of-agent XP, or separately weighted by orchestration actions?
2. Which minimum confidence threshold should trigger hatch suggestions?
3. Should path switching be allowed after Learner stage, and if so, with what penalty/cooldown?
4. What user-facing copy best explains stage mechanics without implying true autonomy?

---

## Side Note: Fun UX Add-Ons (Future)
Keep these out of MVP and revisit after core stability:
- Celebrate level-ups with a short animation and a clear "what unlocked" card.
- Add simple daily/weekly quests (for example: complete 3 tasks, feed 1 agent).
- Introduce visible unlock rewards (titles, badges, theme variants, agent card accents).
- Provide an XP activity log so users can see exactly why XP changed.
- Let users name themselves and Ori during onboarding, then start with a "first mission" prompt.
- Keep proactive behavior opt-in with explicit approval controls.
