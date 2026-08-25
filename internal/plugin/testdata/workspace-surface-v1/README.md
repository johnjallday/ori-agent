# Workspace Surface v1 canonical vectors

These files are the cross-repository contract fixtures for Workspace Surface
protocol `1`. Ori and `reaper-plugin` tests must consume copies with identical
bytes or verify a recorded fixture digest; do not rewrite lookalike local
fixtures with different semantics.

- `valid-identities.json` — matching Claude, Codex, and Ori identity.
- `valid-contribution.json` — complete non-REAPER contribution descriptor.
- `valid-workspace-projection.json` — allowed inert persistence only.
- `valid-catalog.json` — sanitized browser catalog projection.
- `valid-bridge-transcript.json` — opaque-frame parent bridge flow.
- `valid-service-transcript.json` — logical host/service flow. Host-only paths
  intentionally appear here and must never be projected to browser/agent data.
- `valid-errors.json` — stable sanitized public errors and forbidden fields.
- `invalid-vectors.json` — named fail-closed vectors with expected stable codes
  and no-call assertions where applicable.

Task 1.4 selected `mcp_stdio` and fixed the fast/normal/long call limits at
3/15/60 seconds, startup at 10 seconds, stop at 5 seconds, and one bounded
restart. A transport or timing semantic change must update the ADR, normative
contract, this directory, host, SDK, and plugin together.
