# Task Group: chart-lab strategy design and backtests

scope: Natural-language strategy definition, causal backtests, and fee-sensitive reports.
applies_to: cwd=/Users/example/Projects/chart-lab; reuse_rule=reuse the frozen study layout, recheck data gaps

## Task 1: EMA exit comparison with fee sensitivity, outcome=success

### rollout_summary_files

- rollout_summaries/2026-10-05T09-03-33-AbCd-ema_exit_fee_analysis.md (cwd=/Users/example/Projects/chart-lab)

### keywords

- ema20, ema50, exits, fees, close-stop

## Task 2: Hourly breakout draft, outcome=partial; untested

### rollout_summary_files

- rollout_summaries/2026-09-20T12-54-43-EfGh-hourly_breakout_draft.md (cwd=/Users/example/Projects/chart-lab)

### keywords

- breakout, hourly, draft

## User preferences

- when the user asks for "a full result", include the method, the data range, and every metric.
- when the user says "keep it simple", drop secondary indicators before tuning anything.

## Reusable knowledge

- Exits on an EMA20 close below EMA50 beat a fixed 3% stop once fees reach 0.6% per side.
- The 2021 data has two exchange gaps; never treat the series as continuous across them.

## Failures and how to do differently

- A large dashboard hid the one number the user asked for; lead with the net result.

# Task Group: notes-app releases

scope: Desktop notes app packaging and release.
applies_to: cwd=/Users/example/Projects/notes-app; reuse_rule=recheck signing identity

## Task 1: Ship release 2.3, outcome=success

### rollout_summary_files

- rollout_summaries/2026-08-02T10-00-00-IjKl-notes_release.md (cwd=/Users/example/Projects/notes-app)

## Reusable knowledge

- Notarization needs the hardened runtime flag on every nested binary.
