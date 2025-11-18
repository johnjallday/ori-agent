# Ori User Test Guide

Use this checklist before tagging a release to ensure end-to-end workflows behave as expected. Tick each box and capture notes in your release document for traceability.

## Prerequisites

- [ ] Local environment variables set (`make check-env` succeeds)
- [ ] Fresh build artifacts (`make build`, `make menubar`, plugins as needed)
- [ ] Access to sample settings (`settings.json`, `locations.json`, demo plugins)
- [ ] Clean activity log directory to capture new runs

## Checklist

### 1. Environment Smoke

- [ ] Start the server via `make run` (or `go run cmd/server/main.go`) without errors
- [ ] Hit `/healthz` and confirm `200 OK`
- [ ] Connect a WebSocket or CLI client, send a trivial request, and verify streaming responses

### 2. Core Agent Flows

- [ ] Run prompts through each bundled provider (OpenAI, Anthropic, local) and verify route selection + retries
- [ ] Confirm activity log entries are written per request under `activity_logs/`
- [ ] Validate rate-limit/backoff behavior by sending rapid consecutive requests

### 3. Plugin Coverage

- [ ] Load at least one `example_plugins` entry and one `uploaded_plugins` artifact
- [ ] Confirm plugin registry sync updates hashes in `plugin_registry_cache.json`
- [ ] Execute plugin actions and verify permission prompts + outputs

### 4. UI Bundle

- [ ] Build the frontend (`npm run build` or `make build`) and serve the compiled `dist/`
- [ ] Walk through login/connection flow, run a sample command, and review error banners
- [ ] Check command palette/help shortcuts render as expected

### 5. Menubar & Multi-OS Touches

- [ ] macOS: run `make menubar`, verify tray icon, menu commands, and agent trigger
- [ ] Windows/Linux: run CLI/Docker workflow (`make docker-run`) and execute a sample prompt
- [ ] Confirm platform-specific notifications or fallbacks behave correctly

### 6. Persistence & Settings

- [ ] Edit `settings.json` or `locations.json`, restart the server, and ensure changes persist
- [ ] Rotate API keys and re-run `make check-env`
- [ ] Resume an existing `sessions/` conversation to confirm state restoration

### 7. Documentation Walkthrough

- [ ] Follow README “Getting Started” from a clean clone, ensuring no missing steps
- [ ] Update README and Release Notes with any deltas discovered

### 8. Post-Test Wrap-Up

- [ ] Record pass/fail status plus manual observations in the release document
- [ ] File or link to issues for any failures, and block release until resolved
- [ ] Attach screenshots/logs of critical flows to the release package

Keep this file updated as new surfaces are added so the checklist remains comprehensive.
