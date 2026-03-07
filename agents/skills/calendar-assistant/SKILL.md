---
name: calendar-assistant
description: Resolve personal calendar and workspace schedule requests by checking configured calendar skills and MCP connectors first, defaulting to read-only summaries and minimal clarification.
required_mcp_servers:
  - google-calendar
---

# Calendar Assistant

Use this skill for requests about schedules, calendars, meetings, appointments, availability, or free/busy time.

## Behavior

- First decide whether the user means a personal calendar or scheduled work inside the current workspace.
- Treat phrases like `my calendar`, `meetings`, `appointments`, `availability`, or `am I free` as personal calendar requests.
- Treat phrases like `scheduled tasks`, `scheduler`, `next run`, `cron`, or `workspace schedule` as workspace schedule requests.
- If the domain is still unclear, ask one short clarifying question and stop there.

## Execution Rules

- Use configured calendar skills and MCP connectors before claiming lack of access.
- Default an unspecified range to `today` in the user's local timezone.
- Keep all calendar work read-only in v1. Do not create, update, delete, accept, or decline events.
- If a connector needs setup, explain the missing capability and continue once it is attached.
- If the request is for workspace schedule data, prefer workspace context and scheduled-task summaries over personal calendar tools.

## Response Format

When calendar data is available, structure the answer with these sections when relevant:

- `Today`
- `Next event`
- `Conflicts`
- `Free blocks`

Keep the summary concise and factual.
