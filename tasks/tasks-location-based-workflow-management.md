# Tasks: Location-Based Workflow Management

## Relevant Files

### Core Plugin API
- `pluginapi/pluginapi.go` - Extend `AgentContext` struct to include location field
- `pluginapi/pluginapi_test.go` - Unit tests for AgentContext with location

### Location Detection Infrastructure
- `internal/location/manager.go` - Main location manager with detection loop and state management
- `internal/location/manager_test.go` - Unit tests for location manager
- `internal/location/detector.go` - Detector interface definition
- `internal/location/zone.go` - Zone definition, matching logic, and storage
- `internal/location/zone_test.go` - Unit tests for zone management
- `internal/location/wifi_detector.go` - WiFi-based detection interface
- `internal/location/wifi_darwin.go` - macOS WiFi detection (CoreWLAN)
- `internal/location/wifi_linux.go` - Linux WiFi detection (using system commands)
- `internal/location/wifi_windows.go` - Windows WiFi detection (netsh)
- `internal/location/manual_detector.go` - Manual location override detector
- `internal/location/types.go` - Location types, events, and data structures

### HTTP API
- `internal/locationhttp/handlers.go` - HTTP handlers for location API endpoints
- `internal/locationhttp/handlers_test.go` - Unit tests for HTTP handlers

### Server Integration
- `internal/server/server.go` - Add location manager to Server struct and wire up initialization

### UI Components
- `internal/web/static/js/modules/location.js` - Location management JavaScript module
- `internal/web/static/js/modules/location-settings.js` - Location settings page JavaScript
- `internal/web/static/css/location.css` - Styles for location UI components

### Configuration
- `locations.json` - Storage file for location zones (created at runtime)

### Notes

- Location detection is platform-specific. Use build tags for platform-specific WiFi detectors.
- Location manager runs a background goroutine for periodic detection (default: 60 seconds).
- All location data is stored locally and never transmitted externally.
- Unit tests should mock detectors to avoid requiring actual WiFi hardware.

## Instructions for Completing Tasks

**IMPORTANT:** As you complete each task, you must check it off in this markdown file by changing `- [ ]` to `- [x]`. This helps track progress and ensures you don't skip any steps.

Example:
- `- [ ] 1.1 Read file` → `- [x] 1.1 Read file` (after completing)

Update the file after completing each sub-task, not just after completing an entire parent task.

## Tasks

- [x] 0.0 Create feature branch
  - [x] 0.1 Create and checkout a new branch for this feature (e.g., `git checkout -b feature/location-based-workflows`)

- [x] 1.0 Extend AgentContext with location field
  - [x] 1.1 Read `pluginapi/pluginapi.go` to understand the current `AgentContext` structure
  - [x] 1.2 Add `CurrentLocation string` field to the `AgentContext` struct (after `AgentDir` field)
  - [x] 1.3 Add documentation comment explaining the location field represents the current detected location zone name
  - [x] 1.4 Create `pluginapi/pluginapi_test.go` if it doesn't exist
  - [x] 1.5 Write test to verify `AgentContext` can be created with location field populated

- [x] 2.0 Create core location detection infrastructure
  - [x] 2.1 Create `internal/location/` directory
  - [x] 2.2 Create `internal/location/types.go` with core data structures:
    - [x] 2.2.1 Define `DetectionMethod` enum (WiFi, IP, GPS, Manual)
    - [x] 2.2.2 Define `Zone` struct (ID, Name, Description, DetectionRules)
    - [x] 2.2.3 Define `DetectionRule` interface for zone matching
    - [x] 2.2.4 Define `WiFiRule` struct implementing `DetectionRule` (SSID matching)
    - [x] 2.2.5 Define `LocationChangeEvent` struct for event bus
  - [x] 2.3 Create `internal/location/detector.go` with `Detector` interface:
    - [x] 2.3.1 Define `Detector` interface with `Detect(ctx context.Context) (string, error)` method
    - [x] 2.3.2 Define `Name()` method returning detector name
    - [x] 2.3.3 Add documentation for detector interface
  - [x] 2.4 Create `internal/location/manual_detector.go`:
    - [x] 2.4.1 Implement `ManualDetector` struct with in-memory state
    - [x] 2.4.2 Implement `Detect()` method returning manually set location
    - [x] 2.4.3 Implement `SetLocation(location string)` method for manual override
    - [x] 2.4.4 Add thread-safe access using mutex
  - [x] 2.5 Create `internal/location/wifi_detector.go`:
    - [x] 2.5.1 Define `WiFiDetector` interface extending `Detector`
    - [x] 2.5.2 Add `GetCurrentSSID()` method
  - [x] 2.6 Create `internal/location/wifi_darwin.go` (macOS-specific):
    - [x] 2.6.1 Add build tag `//go:build darwin`
    - [x] 2.6.2 Implement `DarwinWiFiDetector` using system commands or CoreWLAN
    - [x] 2.6.3 Implement `Detect()` to return current WiFi SSID
    - [x] 2.6.4 Add error handling for no WiFi connection
  - [x] 2.7 Create `internal/location/wifi_linux.go` (Linux-specific):
    - [x] 2.7.1 Add build tag `//go:build linux`
    - [x] 2.7.2 Implement `LinuxWiFiDetector` using `iwgetid` or NetworkManager
    - [x] 2.7.3 Implement `Detect()` to return current WiFi SSID
    - [x] 2.7.4 Add error handling for missing tools or no connection
  - [x] 2.8 Create `internal/location/wifi_windows.go` (Windows-specific):
    - [x] 2.8.1 Add build tag `//go:build windows`
    - [x] 2.8.2 Implement `WindowsWiFiDetector` using `netsh wlan show interfaces`
    - [x] 2.8.3 Implement `Detect()` to parse netsh output for SSID
    - [x] 2.8.4 Add error handling for WiFi disabled or not connected
  - [x] 2.9 Create `internal/location/manager.go`:
    - [x] 2.9.1 Define `Manager` struct with detectors slice, current location, zones map, mutex
    - [x] 2.9.2 Implement `NewManager(detectors []Detector, zones []Zone)` constructor
    - [x] 2.9.3 Implement `Start(ctx context.Context, interval time.Duration)` to run detection loop
    - [x] 2.9.4 Implement `detectLocation()` to try detectors in priority order with fallback
    - [x] 2.9.5 Implement `matchZone(detectedValue string)` to find matching zone by detection rules
    - [x] 2.9.6 Implement `GetCurrentLocation()` method returning current zone name
    - [x] 2.9.7 Implement `SetManualLocation(location string)` for manual override
    - [x] 2.9.8 Add location change event emission when location changes
    - [x] 2.9.9 Add duplicate detection prevention (don't emit events if location unchanged)
  - [x] 2.10 Create `internal/location/manager_test.go`:
    - [x] 2.10.1 Create mock detector for testing
    - [x] 2.10.2 Test location detection with single detector
    - [x] 2.10.3 Test fallback to second detector when first fails
    - [x] 2.10.4 Test zone matching logic
    - [x] 2.10.5 Test location change event emission
    - [x] 2.10.6 Test duplicate event prevention

- [x] 3.0 Implement location zone management system
  - [x] 3.1 Create `internal/location/zone.go`:
    - [x] 3.1.1 Implement `Zone` struct methods for JSON serialization
    - [x] 3.1.2 Implement `WiFiRule.Matches(value string) bool` for SSID matching (exact match)
    - [x] 3.1.3 Add support for wildcard matching (e.g., "HomeNetwork*")
    - [x] 3.1.4 Implement `AddZone(zone Zone)` method on manager
    - [x] 3.1.5 Implement `RemoveZone(zoneID string)` method
    - [x] 3.1.6 Implement `UpdateZone(zone Zone)` method
    - [x] 3.1.7 Implement `GetZones()` method returning all zones
    - [x] 3.1.8 Implement `GetZoneByID(id string)` method
  - [x] 3.2 Implement zone persistence:
    - [x] 3.2.1 Implement `SaveZones(filepath string)` method to write zones to JSON
    - [x] 3.2.2 Implement `LoadZones(filepath string)` method to read zones from JSON
    - [x] 3.2.3 Add file permission check (ensure 600 permissions for privacy)
    - [x] 3.2.4 Handle missing file gracefully (create with empty zones)
  - [x] 3.3 Create `internal/location/zone_test.go`:
    - [x] 3.3.1 Test WiFi rule exact matching
    - [x] 3.3.2 Test WiFi rule wildcard matching
    - [x] 3.3.3 Test zone CRUD operations
    - [x] 3.3.4 Test zone persistence (save/load)
    - [x] 3.3.5 Test handling of corrupted JSON file

- [x] 4.0 Create HTTP API endpoints for location management
  - [x] 4.1 Create `internal/locationhttp/` directory
  - [x] 4.2 Create `internal/locationhttp/handlers.go`:
    - [x] 4.2.1 Define `Handler` struct with location manager dependency
    - [x] 4.2.2 Implement `NewHandler(manager *location.Manager)` constructor
    - [x] 4.2.3 Implement `GetCurrentLocation` handler (GET /api/location/current)
    - [x] 4.2.4 Implement `GetZones` handler (GET /api/location/zones)
    - [x] 4.2.5 Implement `CreateZone` handler (POST /api/location/zones)
    - [x] 4.2.6 Implement `UpdateZone` handler (PUT /api/location/zones/:id)
    - [x] 4.2.7 Implement `DeleteZone` handler (DELETE /api/location/zones/:id)
    - [x] 4.2.8 Implement `SetManualLocation` handler (POST /api/location/override)
    - [x] 4.2.9 Add JSON request/response serialization
    - [x] 4.2.10 Add error handling and appropriate HTTP status codes
    - [x] 4.2.11 Add request validation (zone name required, valid detection rules)
  - [x] 4.3 Create `internal/locationhttp/handlers_test.go`:
    - [x] 4.3.1 Create mock location manager for testing
    - [x] 4.3.2 Test GET /api/location/current endpoint
    - [x] 4.3.3 Test GET /api/location/zones endpoint
    - [x] 4.3.4 Test POST /api/location/zones endpoint (create zone)
    - [x] 4.3.5 Test PUT /api/location/zones/:id endpoint (update zone)
    - [x] 4.3.6 Test DELETE /api/location/zones/:id endpoint
    - [x] 4.3.7 Test POST /api/location/override endpoint (manual location)
    - [x] 4.3.8 Test error cases (invalid JSON, missing zone, etc.)

- [x] 5.0 Integrate location manager with server initialization
  - [x] 5.1 Read `internal/server/server.go` to understand server initialization pattern
  - [x] 5.2 Add `locationManager *location.Manager` field to `Server` struct
  - [x] 5.3 Add `locationHandler *locationhttp.Handler` field to `Server` struct
  - [x] 5.4 In `New()` function, after initializing other managers:
    - [x] 5.4.1 Create detector instances (WiFi detector for current platform, manual detector)
    - [x] 5.4.2 Load zones from `locations.json` file
    - [x] 5.4.3 Initialize location manager with detectors and zones
    - [x] 5.4.4 Start location manager detection loop with 60-second interval
    - [x] 5.4.5 Initialize location HTTP handler with manager
  - [x] 5.5 In `Run()` function, register location HTTP routes:
    - [x] 5.5.1 Register `GET /api/location/current` route
    - [x] 5.5.2 Register `GET /api/location/zones` route
    - [x] 5.5.3 Register `POST /api/location/zones` route
    - [x] 5.5.4 Register `PUT /api/location/zones/:id` route
    - [x] 5.5.5 Register `DELETE /api/location/zones/:id` route
    - [x] 5.5.6 Register `POST /api/location/override` route
  - [x] 5.6 Update plugin loader to pass current location in `AgentContext`:
    - [x] 5.6.1 Find where `AgentContext` is created in plugin loading code
    - [x] 5.6.2 Call `s.locationManager.GetCurrentLocation()` to get current location
    - [x] 5.6.3 Populate `CurrentLocation` field in `AgentContext` before passing to plugins

- [ ] 6.0 Add location status indicator to UI
  - [ ] 6.1 Create `internal/web/static/js/modules/location.js`:
    - [ ] 6.1.1 Create `LocationIndicator` class to manage location status display
    - [ ] 6.1.2 Implement `fetchCurrentLocation()` to call GET /api/location/current
    - [ ] 6.1.3 Implement `updateIndicator(location)` to update UI with current location
    - [ ] 6.1.4 Implement polling mechanism to refresh location every 30 seconds
    - [ ] 6.1.5 Add error handling for API failures (show "Unknown" or last known location)
    - [ ] 6.1.6 Export module for use in main app
  - [ ] 6.2 Update main UI HTML to include location indicator:
    - [ ] 6.2.1 Find appropriate location in header/navbar (read existing HTML structure)
    - [ ] 6.2.2 Add location indicator element with ID `location-indicator`
    - [ ] 6.2.3 Add appropriate styling (small badge or icon with text)
  - [ ] 6.3 Update `internal/web/static/js/app.js` to initialize location indicator:
    - [ ] 6.3.1 Import location module
    - [ ] 6.3.2 Initialize `LocationIndicator` on page load
    - [ ] 6.3.3 Start location polling

- [ ] 7.0 Create location settings page in UI
  - [ ] 7.1 Create `internal/web/static/js/modules/location-settings.js`:
    - [ ] 7.1.1 Create `LocationSettingsManager` class
    - [ ] 7.1.2 Implement `fetchZones()` to call GET /api/location/zones
    - [ ] 7.1.3 Implement `renderZonesList(zones)` to display zones in table format
    - [ ] 7.1.4 Implement `showCreateZoneModal()` to display zone creation form
    - [ ] 7.1.5 Implement `createZone(zoneData)` to call POST /api/location/zones
    - [ ] 7.1.6 Implement `showEditZoneModal(zone)` to display zone editing form
    - [ ] 7.1.7 Implement `updateZone(zoneData)` to call PUT /api/location/zones/:id
    - [ ] 7.1.8 Implement `deleteZone(zoneId)` with confirmation to call DELETE /api/location/zones/:id
    - [ ] 7.1.9 Implement `testDetection()` to show current WiFi SSID and detected location
    - [ ] 7.1.10 Implement `setManualLocation(location)` to call POST /api/location/override
    - [ ] 7.1.11 Add form validation (zone name required, at least one detection rule)
    - [ ] 7.1.12 Add success/error notifications after API calls
  - [ ] 7.2 Add location settings section to existing settings page HTML:
    - [ ] 7.2.1 Find settings page HTML file structure
    - [ ] 7.2.2 Add "Locations" tab or section
    - [ ] 7.2.3 Add zones list table with columns: Name, Detection Method, Description, Actions
    - [ ] 7.2.4 Add "Add Location Zone" button
    - [ ] 7.2.5 Add "Test Detection" button to show current detection status
    - [ ] 7.2.6 Add modal for creating/editing zones with form fields:
      - Zone name input
      - Description textarea
      - Detection method selector (WiFi/Manual)
      - WiFi SSID input (if WiFi selected)
      - Save and Cancel buttons
  - [ ] 7.3 Create `internal/web/static/css/location.css` for location-specific styles:
    - [ ] 7.3.1 Style location indicator badge (compact, non-intrusive)
    - [ ] 7.3.2 Style zones table (consistent with existing settings tables)
    - [ ] 7.3.3 Style zone creation/edit modal
    - [ ] 7.3.4 Add responsive styles for mobile viewing
  - [ ] 7.4 Integrate location settings module into settings page:
    - [ ] 7.4.1 Import location-settings module in settings page JavaScript
    - [ ] 7.4.2 Initialize `LocationSettingsManager` when settings page loads
    - [ ] 7.4.3 Load and render zones list on page load

- [x] 8.0 Write tests for location detection and zone management
  - [x] 8.1 Run existing tests to ensure no regressions: `go test ./internal/location/...`
  - [x] 8.2 Run HTTP handler tests: `go test ./internal/locationhttp/...`
  - [x] 8.3 Add integration test in `internal/location/integration_test.go`:
    - [x] 8.3.1 Test full detection cycle (WiFi detector → zone matching → location change event)
    - [x] 8.3.2 Test manual override takes precedence over WiFi detection
    - [x] 8.3.3 Test zone persistence (save → reload → verify zones intact)
    - [x] 8.3.4 Test concurrent access to location manager (thread safety)
  - [x] 8.4 Verify all tests pass: `go test ./...`
  - [x] 8.5 Check test coverage: `go test -cover ./internal/location/... ./internal/locationhttp/...`
  - [x] 8.6 Ensure coverage is at least 80% for new code

## Phase 1 Complete - Backend Fully Functional ✅

All backend functionality is complete and tested. Remaining work is UI implementation (Tasks 6-7).
