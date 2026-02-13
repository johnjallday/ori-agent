# Home Assistant Task Routing

## Overview
On the home dashboard, the user can click the assistant and submit a task request. The system routes the request to a suitable existing agent or offers one-click agent creation when no good match is found, then hands the task into a real chat session.

This supports the scenarios:
1. `Plan my 3 day trip in LA`
2. `Check my email`

## User Flow
1. User clicks assistant avatar/speech bubble on home page.
2. User enters a task prompt in `Ask Ori`.
3. Client calls backend route endpoint (`POST /api/home-assistant/route`) to detect intent and match.
4. If backend routing is unavailable, client falls back to local scoring.
5. If match exists:
   - Show matched agent with reason.
   - Start a new session for that agent (fresh session per Ask Ori task).
   - Open chat panel and send the user prompt into chat.
   - Add the session to Ask Ori "recent sessions" chips for quick reopen.
   - Allow deleting a recent session directly from Ask Ori chips.
6. If no match:
   - Show: `No suitable agent found for this task. Would you like me to create one?`
   - User can create agent.
7. On create:
   - Try auto-config first.
   - Create agent with fallback defaults if auto-config unavailable.
   - Auto-select a model from available providers for the chosen agent type.
   - If no model can be auto-selected, open `Create New Agent` modal for user confirmation.
   - Open/create session for the new agent and send original prompt into chat.

## UI States
- `idle`: Input ready.
- `routing`: Send button disabled, matching in progress.
- `match_found`: Match summary shown, then auto-handoff to chat session.
- `no_match`: Action shown (`Create Agent`).
- `creating_agent`: Send and quick prompts disabled; creation in progress.
- `confirming_agent`: Create Agent modal is open for user review/confirmation.
- `opening_chat`: Session switch/create and prompt dispatch in progress.
- `completed`: Confirmation shown with follow-up actions (`Open Chat`, `Ask Another Task`).
- `recent_sessions`: Recently created Ask Ori sessions are shown as quick reopen buttons.
- `delete_session`: User can delete tracked sessions from Ask Ori recent-session controls.
- `error`: Retry action shown.

## API Contracts (Current)

### 1) List agents for matching
- `GET /api/agents/dashboard/list`
- Response (subset used):
```json
{
  "agents": [
    {
      "name": "Travel Planner",
      "type": "research",
      "role": "general",
      "status": "active",
      "metadata": {
        "description": "Plans multi-day travel itineraries",
        "tags": ["travel", "itinerary"],
        "favorite": true
      },
      "enabled_plugins": ["weather", "web-search"]
    }
  ]
}
```

### 2) Check auto-config availability
- `GET /api/agents/auto-config/availability`
- Response (subset used):
```json
{
  "available": true,
  "system_model_configured": true,
  "message": ""
}
```

### 3) Generate auto agent config
- `POST /api/agents/auto-config`
- Request:
```json
{
  "description": "Create an agent that plans multi-day travel itineraries..."
}
```
- Response (subset used):
```json
{
  "agent_name": "Travel Planner",
  "agent_type": "research",
  "model": "gpt-5",
  "temperature": 0.4,
  "system_prompt": "You are a travel planning assistant..."
}
```

### 4) Create agent
- `POST /api/agents`
- Request (example):
```json
{
  "name": "Travel Planner",
  "type": "research",
  "model": "gpt-5",
  "temperature": 0.4,
  "system_prompt": "You are a travel planning assistant...",
  "description": "Create an agent that plans multi-day travel itineraries...",
  "tags": ["travel", "itinerary", "home-assistant", "auto-created"]
}
```

### 5) Create session with chosen agent
- `POST /api/sessions`
- Request (example):
```json
{
  "title": "New Session",
  "agent_name": "Travel Planner"
}
```

### 6) Send task through active chat session
The dashboard uses `window.sendMessageToChat(prompt)`, which sends through the normal chat flow and includes active session context headers.

Chat request body (effective shape):
```json
{
  "message": "Plan my 3 day trip in LA",
  "agent_name": "Travel Planner"
}
```

## Email Handling Idea
For `Check my email`, treat it as an integration-gated task:
1. Require OAuth connect first (Gmail/Outlook).
2. Start with read-only scopes (`read`, `labels`, no send/delete).
3. First actions: summarize unread, classify urgent, draft suggested replies.
4. Any send/reply action requires explicit user confirmation.

## Backend Route Endpoint (Implemented)
- `POST /api/home-assistant/route`
- Request:
```json
{
  "prompt": "Plan my 3 day trip in LA"
}
```
- Response:
```json
{
  "intent": "travel_planning",
  "matched_agent": "Travel Planner",
  "score": 8,
  "requires_creation": false,
  "reasons": ["matches \"trip\"", "has plugin support for weather"],
  "suggested_agent_name": "Travel Planner",
  "suggested_agent_type": "research"
}
```

## Acceptance Criteria
- User can click the assistant area from home and submit a task.
- When a suitable agent exists, prompt is handed off into a real chat session automatically.
- When no suitable agent exists, user is offered one-click creation.
- Created agent receives the original prompt in chat immediately after creation.
- Email intent surfaces OAuth/read-only guidance.
