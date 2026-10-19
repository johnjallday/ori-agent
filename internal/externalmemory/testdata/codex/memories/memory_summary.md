v1

## User Profile

Builds small, self-contained trading research tools and a personal agent app; prefers plain explanations over jargon and checks every claim against saved results.

Writes some requests in Korean and expects answers in the same language when asked.

## User preferences

- Keep strategy research simple: name the setup, the entry, the exit, and the fee assumption before any backtest.
- Separate planning from implementation; never start coding during a planning pass.
- 결과 보고서는 한국어로 작성하고 수치는 원본 그대로 유지한다.

## General Tips

- In chart-lab, completed candles only: never use the forming candle for a signal.
- Use the demo sandbox for anything user-visible before opening a PR.

## What's in Memory

### /Users/example/Projects/chart-lab

#### 2026-10-14

- EMA exit study with fee sensitivity: EMA20/EMA50 exits versus fixed stops
  - desc: Compared exit rules on hourly candles with 0.6% per-side fees.
  - learnings: Fees decide the ranking; report net results only.

### Older Memory Topics

#### /Users/example/Projects/notes-app

- Notes app release checklist: signing, notarization, changelog
  - desc: Release steps for the desktop notes app.
