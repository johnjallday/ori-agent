# Product Requirements Document: Location-Based Workflow Management

## Introduction/Overview

This feature enables users to automate workflows, agent executions, and scheduled tasks based on their physical location. Users can define location zones (e.g., "Home", "Office", "Coffee Shop") and configure automatic triggers when entering/leaving these zones, or schedule tasks that only run when at specific locations. This provides context-aware automation that adapts to the user's environment.

**Problem Statement:** Users want their AI agents and workflows to behave differently based on where they are. For example, automatically starting a "work mode" agent when arriving at the office, or ensuring backup tasks only run when at home on the home network.

**Goal:** Provide a flexible, privacy-respecting location detection system integrated with ori-agent's existing orchestration and scheduling infrastructure.

## Goals

1. Enable automatic workflow triggering based on location arrival/departure events
2. Support location-aware scheduled task execution (tasks that only run at specific locations)
3. Provide multiple location detection methods with intelligent fallback
4. Maintain strong privacy protections (location data never leaves the machine)
5. Integrate seamlessly with existing agent, workspace, and orchestration systems
6. Support both simple single-agent triggers and complex multi-agent workflows
7. Provide intuitive UI for managing locations and location-based rules

## User Stories

**As a user:**
- I want to define named location zones (e.g., "Home", "Office") so I can reference them in workflows
- I want workflows to automatically start when I arrive at specific locations
- I want workflows to automatically stop when I leave specific locations
- I want to schedule tasks that only run when I'm at certain locations (e.g., "backup at 6pm, but only if home")
- I want agents to have context about my current location so they can make location-aware decisions
- I want to use different detection methods (WiFi, IP, GPS) with automatic fallback
- I want to see my current detected location in the UI
- I want to manually override detected location when needed
- I want to configure location-based triggers through the existing agent/workspace UI

**As a developer:**
- I want to access location context from plugins via AgentContext
- I want to trigger workflows based on location events programmatically
- I want to query current location state via API

## Functional Requirements

### Location Detection

1. **FR-1:** The system must support multiple location detection methods with configurable priority:
   - WiFi network SSID detection (highest priority)
   - IP address-based location (geolocation)
   - GPS coordinates from system APIs (requires user permission)
   - Manual location selection by user

2. **FR-2:** The system must implement intelligent fallback: if the highest priority method fails, try the next method in the priority list.

3. **FR-3:** The system must detect location changes and emit events when the user enters or exits a defined location zone.

4. **FR-4:** The system must support location zones defined by:
   - WiFi SSID matching (exact or pattern)
   - IP address ranges or geolocation city/region
   - GPS coordinate radius (center point + radius in meters)
   - Manual user selection

5. **FR-5:** The system must refresh location detection at a configurable interval (default: every 60 seconds).

### Location Zone Management

6. **FR-6:** Users must be able to create, edit, and delete named location zones through the UI.

7. **FR-7:** Each location zone must have:
   - Unique name (e.g., "Home", "Office")
   - One or more detection rules (WiFi SSID, IP range, GPS coordinates)
   - Optional description

8. **FR-8:** The system must display the currently detected location zone in the UI (e.g., "Current Location: Home").

9. **FR-9:** Users must be able to manually override the detected location (e.g., "I'm at Office even though WiFi says otherwise").

### Workflow Triggers

10. **FR-10:** Users must be able to configure "on arrival" triggers for location zones that:
    - Execute a single agent
    - Execute a multi-agent orchestration workflow
    - Execute a workspace task
    - Call a specific plugin tool

11. **FR-11:** Users must be able to configure "on departure" triggers for location zones with the same action types as arrival triggers.

12. **FR-12:** Location triggers must be configurable per agent and per workspace.

13. **FR-13:** The system must prevent duplicate triggers (e.g., if location detection runs multiple times while still in the same zone).

14. **FR-14:** Trigger actions must run asynchronously and not block location detection.

### Scheduled Task Integration

15. **FR-15:** The existing cron-like scheduled task system must support location constraints (e.g., "Run every day at 6pm, but only if at Home").

16. **FR-16:** Scheduled tasks with location constraints must be skipped if the location condition is not met, and logged accordingly.

17. **FR-17:** Users must be able to configure location constraints through the workspace/task scheduling UI.

### Agent and Plugin Integration

18. **FR-18:** Agents must have access to current location context via the AgentContext (similar to agent name, config path).

19. **FR-19:** Plugins implementing `AgentAwareTool` must receive location information in the context.

20. **FR-20:** The system must provide an internal API for plugins to query current location and subscribe to location change events.

### Privacy and Data Storage

21. **FR-21:** All location data must be stored locally only (no external transmission).

22. **FR-22:** Location zones and detection rules must be stored in a local configuration file (e.g., `locations.json`).

23. **FR-23:** Location history (if any) must be opt-in and stored locally with configurable retention period.

24. **FR-24:** GPS coordinates should only be stored as part of zone definitions (center point + radius), not as raw tracking data.

### User Interface

25. **FR-25:** The main UI must display current location status (zone name or "Unknown").

26. **FR-26:** There must be a dedicated "Locations" settings page for managing location zones.

27. **FR-27:** Agent configuration UI must have a section for location-based triggers (arrival/departure actions).

28. **FR-28:** Workspace task scheduling UI must support adding location constraints to scheduled tasks.

29. **FR-29:** Users must be able to test location detection methods and see results in real-time (e.g., "Currently connected to WiFi: MyNetwork").

### API Requirements

30. **FR-30:** The system must provide HTTP API endpoints:
    - `GET /api/location/current` - Get current location
    - `GET /api/location/zones` - List all location zones
    - `POST /api/location/zones` - Create location zone
    - `PUT /api/location/zones/:id` - Update location zone
    - `DELETE /api/location/zones/:id` - Delete location zone
    - `POST /api/location/override` - Manually set current location
    - `GET /api/location/triggers` - List all location-based triggers

## Non-Goals (Out of Scope)

1. **Real-time GPS tracking:** This is not a location tracking/logging system. Only current location and zone transitions are tracked.
2. **Remote location access:** Location data will never be transmitted to external services or APIs.
3. **Geofencing mobile apps:** This is desktop-focused; mobile integration is out of scope for v1.
4. **Historical location analytics:** No dashboards or reports on where users have been.
5. **Sharing locations with other users:** No collaborative location features.
6. **Integration with third-party location services:** (Google Maps API, etc.) - only system-level APIs are used.

## Design Considerations

### UI Components

- **Location Status Indicator:** Small widget in the main UI showing current location (e.g., in header/navbar)
- **Locations Settings Page:** Table of defined zones with add/edit/delete actions
- **Location Zone Editor:** Modal/page for creating/editing zones with:
  - Name input
  - Detection method selectors (WiFi SSID, IP range, GPS)
  - Test detection button
- **Agent Trigger Configuration:** Section in agent settings for arrival/departure triggers
- **Task Scheduler Enhancement:** Add "Only when at location: [dropdown]" option to scheduled tasks

### Integration Points

- Extend `internal/workspace/` to handle location-aware task scheduling
- Extend `internal/orchestration/` to support location triggers
- Add location context to `pluginapi.AgentContext`
- New handler module: `internal/locationhttp/` for HTTP endpoints
- New package: `internal/location/` for detection logic and zone management

### Detection Method Priority (Default)

1. WiFi SSID (most reliable for known locations)
2. IP address geolocation (fallback for unknown WiFi)
3. GPS (if user grants permission, useful for outdoor locations)
4. Manual override (always respected)

## Technical Considerations

### Architecture Decision: Core Feature vs. Plugin

**Recommendation: Core Feature (Built into ori-agent)**

**Rationale:**
- **AgentContext Integration:** Location is environmental context (like agent name, config path) that should be universally available to ALL plugins via `AgentContext`
- **Deep Integration Required:** Location-aware scheduling, triggers, and orchestration need tight coupling with core systems (`internal/workspace/`, `internal/orchestration/`, event bus)
- **Dependency Direction:** Plugins should depend on core features, not vice versa. If plugins need location context, location must be in core
- **First-Class Event Type:** Location changes are fundamental events that belong in the core event bus
- **Universal Availability:** Every agent should have location context without requiring plugin installation

**Implementation Path:**
1. Create new core package: `internal/location/` for detection logic and zone management
2. Create new HTTP handler: `internal/locationhttp/` for REST API endpoints
3. Extend `pluginapi.AgentContext` to include `CurrentLocation string` field
4. Integrate location detection service into `internal/server/server.go` (similar to other core services)
5. Platform-specific detection code organized as internal implementations (modular within core):
   ```
   internal/location/
   ├── manager.go         # Main location manager
   ├── detector.go        # Detector interface
   ├── wifi_darwin.go     # macOS WiFi detection
   ├── wifi_linux.go      # Linux WiFi detection
   ├── wifi_windows.go    # Windows WiFi detection
   ├── ip_detector.go     # IP-based detection (cross-platform)
   ├── gps_darwin.go      # macOS GPS detection
   └── zones.go           # Zone management
   ```
6. Extend workspace task scheduler to check location conditions before execution
7. Add location trigger support to orchestration system

### Dependencies

- **Go libraries:**
  - WiFi detection: `github.com/mdlayher/wifi` (Linux), CoreWLAN framework (macOS), netsh (Windows)
  - IP geolocation: `github.com/oschwald/geoip2-golang` with local MaxMind DB
  - GPS: Platform-specific (CoreLocation on macOS, Windows Location API, gpsd on Linux)

- **Ori-agent integration:**
  - Extend `pluginapi.AgentContext` to include `CurrentLocation string`
  - Add location event types to event bus
  - Extend workspace task scheduler to check location conditions

### State Management

- **Location zones:** Stored in `locations.json` (server config directory, alongside `settings.json`)
- **Current location:** In-memory state in location manager, exposed via HTTP API and `AgentContext`
- **Trigger configurations:** Stored in agent/workspace configs (extend existing config files)
- **Location override:** Temporary state, persisted to `app_state.json`
- **Location detection settings:** Stored in `settings.json` (detection method priority, polling interval)

### Performance Considerations

- Location detection runs asynchronously (non-blocking)
- Default polling interval: 60 seconds (configurable)
- WiFi/IP detection should complete in <500ms
- GPS detection may take 5-10 seconds (show "Detecting..." state)
- Trigger execution is async (doesn't block detection loop)

### Platform Support

- **macOS:** Full support (WiFi, IP, GPS, manual)
- **Linux:** WiFi, IP, manual (GPS requires gpsd)
- **Windows:** WiFi, IP, manual (GPS requires Windows 10+ Location API)

### Security Considerations

- Location data stored with appropriate file permissions (600)
- No network transmission of location data (except IP geolocation lookup to MaxMind, if enabled)
- GPS coordinates stored only as zone definitions, not raw tracking
- User must explicitly grant GPS permissions (system dialog)
- Location detection runs in server process, but detection methods are isolated by OS security models
- IP geolocation database (MaxMind GeoLite2) stored locally, updated periodically

## Success Metrics

1. **Adoption:** 30% of active users configure at least one location zone within 30 days
2. **Reliability:** Location detection accuracy >95% for WiFi-based zones
3. **Performance:** Location detection completes in <1 second 99% of the time
4. **Trigger Success Rate:** >98% of location-based triggers execute successfully
5. **User Satisfaction:** Positive feedback on location automation in user surveys

## Open Questions

1. **Should location detection run when the server is running in background mode only?** Or also when UI is active?
2. **Should there be rate limiting on trigger executions?** (e.g., max 1 trigger per location per 5 minutes)
3. **How should conflicts be handled?** If user is detected in multiple overlapping zones, which takes priority?
4. **Should the plugin support custom detection scripts?** (e.g., user provides bash script that returns location)
5. **Desktop app integration:** Should the macOS menu bar app show current location? Display notifications on location changes?
6. **Should location changes be logged?** If yes, what retention policy? (considering privacy)
7. **Timezone awareness:** Should location zones include timezone information to adjust scheduled tasks automatically?

## Implementation Phases (Suggestion)

**Phase 1: Core Infrastructure**
- Create `internal/location/` package with manager and detector interface
- Implement WiFi detection (macOS first, then Linux/Windows)
- Implement manual location override
- Add `CurrentLocation` to `AgentContext`
- Basic zone management (create/read/update/delete zones)
- HTTP API endpoints (`internal/locationhttp/`)
- Store zones in `locations.json`
- Location status indicator in UI

**Phase 2: Trigger System**
- Location change event emission via event bus
- Arrival/departure triggers for single agents
- Integration with `internal/orchestration/`
- UI for configuring triggers in agent settings
- Trigger execution (async, non-blocking)
- Duplicate trigger prevention

**Phase 3: Scheduled Task Integration**
- Extend workspace task scheduler to check location conditions
- Add location constraint field to task definitions
- UI enhancements for task scheduling (location dropdown)
- Skip and log tasks when location condition not met

**Phase 4: Advanced Features**
- IP geolocation support (MaxMind GeoLite2 integration)
- GPS support (platform-specific: macOS CoreLocation, Windows Location API, Linux gpsd)
- Multi-agent orchestration triggers
- Location history (opt-in, with retention policy)
- Menu bar app integration (show location, notification on changes)
- Timezone awareness for location zones

---

**Document Status:** Draft for Review
**Target Audience:** Junior Developer
**Next Steps:** Review open questions, get stakeholder feedback, proceed with Phase 1 implementation
