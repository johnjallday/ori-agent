# Multi-Agent Support (Planner-First)

## Overview
Multi-agent support uses a planner-first step to decompose complex requests into subtasks, assigns them to specialized agents, and aggregates the results. The planner outputs a complexity score and routing rationale, and multi-agent mode auto-activates when the score meets a configurable threshold.

## Planner Flow
1. The planner receives the user request plus available agent metadata (roles, capabilities).
2. It returns JSON with:
   - `complexity_score` (0-10)
   - `rationale`
   - `tasks` (subtasks + dependencies)
   - `dynamic_agents` (optional; used only if no existing agent fits)
3. The orchestrator makes a routing decision based on:
   - `multi_agent_mode` (auto/force/off)
   - `multi_agent_threshold`

## Routing Modes
- `auto`: use the complexity score and threshold to decide.
- `force`: always multi-agent.
- `off`: always single-agent.

The routing decision is logged and attached to the response payload as `planner_decision` when available.

## Dynamic Agent Approval
If the planner requires a new agent:
1. The orchestrator returns `status: pending_approval` with `dynamic_agent_requests`.
2. The UI prompts for approval.
3. On approval, the server creates the agent and resumes the pending plan.

## API
### Chat
`POST /api/chat`
- Request fields:
  - `multi_agent_mode`: `auto|force|off`
  - `multi_agent_threshold`: number (0-10)
- Response fields (when available):
  - `planner_decision`
  - `planner_plan`
  - `dynamic_agent_requests`
  - `pending_plan_id`

### Dynamic Agent Approval
`GET /api/orchestration/dynamic-agents/approve?workspace_id=<id>`
`POST /api/orchestration/dynamic-agents/approve`
```json
{
  "workspace_id": "workspace-id",
  "request_id": "dynamic-agent-request-id",
  "approve": true,
  "approved_by": "user"
}
```

## CLI
`cmd/server` flags:
- `--multi-agent-mode auto|force|off`
- `--multi-agent-threshold <0-10>`

## UI
- Chat panel includes a Multi-agent mode selector (Auto/Force/Off).
- Planner decision appears as a small status indicator after responses.
- The plan summary is shown for multi-agent runs.
- Dynamic agent requests trigger an approval prompt.

## Manual Test: Weather -> Math Conversion
1. Ensure the system model is configured in Settings.
2. In the chat panel, set Multi-agent mode to `auto` or `force`.
3. Send: "Get the weather in New York in Fahrenheit."
4. Verify:
   - The plan shows a weather lookup task and a conversion task.
   - Multi-agent indicator shows the complexity score and routing decision.
   - The final response includes the Fahrenheit conversion.
