# 🏥 Plugin Health Check System - Design Plan

## Overview

A plugin health check system to ensure version compatibility and maintain stability between ori-agent and its plugins.

---

## **1. Version Compatibility Framework**

### **A. Version Metadata**
Add to each plugin:
```go
type PluginMetadata interface {
    Version() string                    // Plugin version (e.g., "0.0.5")
    MinAgentVersion() string            // Minimum ori-agent version required
    MaxAgentVersion() string            // Maximum compatible version (optional)
    APIVersion() string                 // Plugin API version (e.g., "v1", "v2")
}
```

Add to ori-agent:
```go
// internal/version/version.go
const (
    Version    = "0.0.7"      // Agent version
    APIVersion = "v1"          // Plugin API version
)
```

### **B. Compatibility Rules**
```go
type CompatibilityCheck struct {
    Compatible   bool
    Reason       string
    Severity     string  // "error", "warning", "info"
    Recommendation string
}
```

---

## **2. Health Check Components**

### **A. Startup Validation**
When plugins load:
```
1. Check plugin implements PluginMetadata
2. Verify plugin version is valid semver
3. Compare agent version against min/max requirements
4. Validate API version matches
5. Test basic plugin functionality (Definition(), Call() with empty args)
6. Mark as healthy/unhealthy with reason
```

### **B. Runtime Health Monitoring**
```go
type PluginHealth struct {
    Name              string
    Version           string
    Status            string  // "healthy", "degraded", "unhealthy"
    LastCheck         time.Time
    CompatibleWith    string  // Agent version
    Errors            []string
    Warnings          []string
    CallSuccessRate   float64  // % of successful calls
    AvgResponseTime   time.Duration
    TotalCalls        int64
    FailedCalls       int64
}
```

### **C. Health Check Types**

**1. Compatibility Check**
- Agent version vs plugin requirements
- API version matching
- Required interfaces implemented

**2. Functional Check**
- Can call `Definition()` successfully?
- Can call `Call()` without crashing?
- Returns valid JSON responses?

**3. Performance Check**
- Response time < threshold (e.g., 5s)
- No memory leaks
- No hanging processes

**4. Dependency Check**
- External tools available (REAPER, etc.)
- File paths exist and accessible
- Network connectivity (if needed)

---

## **3. API Design**

### **A. Health Check Endpoint**
```
GET /api/plugins/health

Response:
{
  "agent_version": "0.0.7",
  "agent_api_version": "v1",
  "timestamp": "2025-10-27T12:00:00Z",
  "plugins": [
    {
      "name": "music-project-manager",
      "version": "0.0.6",
      "status": "healthy",
      "compatible": true,
      "api_version": "v1",
      "last_check": "2025-10-27T11:59:00Z",
      "stats": {
        "total_calls": 150,
        "failed_calls": 2,
        "success_rate": 98.67,
        "avg_response_time_ms": 120
      }
    },
    {
      "name": "ori-reaper",
      "version": "0.0.3",
      "status": "degraded",
      "compatible": false,
      "api_version": "v1",
      "errors": [
        "Plugin version 0.0.3 is below minimum required 0.0.5"
      ],
      "warnings": [
        "Please update to ori-reaper v0.0.5+"
      ],
      "recommendation": "Update plugin using: ./releases/release-ori-reaper.sh v0.0.5"
    }
  ]
}
```

### **B. Individual Plugin Health**
```
GET /api/plugins/{name}/health

Response:
{
  "name": "music-project-manager",
  "version": "0.0.6",
  "status": "healthy",
  "compatible": true,
  "checks": [
    {
      "type": "version_compatibility",
      "passed": true,
      "details": "Plugin v0.0.6 compatible with agent v0.0.7"
    },
    {
      "type": "api_version",
      "passed": true,
      "details": "API v1 matches agent API v1"
    },
    {
      "type": "functionality",
      "passed": true,
      "details": "Definition() and Call() working correctly"
    },
    {
      "type": "dependencies",
      "passed": true,
      "details": "All required paths exist"
    }
  ]
}
```

---

## **4. Compatibility Matrix**

Store in `plugin_compatibility.json`:
```json
{
  "agent_version": "0.0.7",
  "api_version": "v1",
  "compatible_plugins": {
    "music-project-manager": {
      "min_version": "0.0.5",
      "recommended_version": "0.0.6",
      "breaking_changes": {
        "0.0.4": "Removed legacy SetSettings interface"
      }
    },
    "ori-reaper": {
      "min_version": "0.0.4",
      "recommended_version": "0.0.5",
      "breaking_changes": {
        "0.0.3": "Changed script discovery API"
      }
    }
  }
}
```

---

## **5. User Interface**

### **A. Plugin Dashboard**
```
╔════════════════════════════════════════════════════════╗
║  🏥 Plugin Health Status                               ║
╠════════════════════════════════════════════════════════╣
║  ✅ music-project-manager  v0.0.6     [Healthy]       ║
║     98.7% success rate • 120ms avg response           ║
║                                                        ║
║  ⚠️  ori-reaper           v0.0.3     [Degraded]       ║
║     Version incompatible - Update required            ║
║     → Run: ./releases/release-ori-reaper.sh v0.0.5    ║
║                                                        ║
║  ❌ ori-mac-os-tools      v0.0.2     [Unhealthy]      ║
║     API version mismatch (v0 vs v1)                   ║
║     → Rebuild plugin with latest SDK                  ║
╚════════════════════════════════════════════════════════╝
```

### **B. Startup Warnings**
```bash
⚠️  Plugin Health Warning:

The following plugins have compatibility issues:

❌ ori-reaper v0.0.3 (Incompatible)
   - Requires agent v0.0.6+ but you have v0.0.7
   - Update to v0.0.5: ./releases/release-ori-reaper.sh v0.0.5

⚠️  ori-mac-os-tools v0.0.2 (Degraded)
   - Using deprecated API v0, upgrade to v1 recommended
   - Some features may not work correctly

Continue anyway? (y/n)
```

---

## **6. Implementation Phases**

### **Phase 1: Foundation** (Week 1)
- [ ] Add `PluginMetadata` interface to pluginapi
- [ ] Implement version checking logic
- [ ] Add `internal/health/` package
- [ ] Store agent version/API version constants

### **Phase 2: Health Checks** (Week 2)
- [ ] Implement startup validation
- [ ] Add `/api/plugins/health` endpoint
- [ ] Create `PluginHealth` tracking struct
- [ ] Add basic compatibility checking

### **Phase 3: Monitoring** (Week 3)
- [ ] Add runtime stats tracking (call counts, errors)
- [ ] Implement periodic health checks
- [ ] Add performance monitoring
- [ ] Create health history/logs

### **Phase 4: User Experience** (Week 4)
- [ ] Build health dashboard UI
- [ ] Add startup warnings
- [ ] Create compatibility matrix file
- [ ] Add auto-update suggestions

### **Phase 5: Advanced Features** (Future)
- [ ] Auto-plugin updates
- [ ] Rollback on incompatibility
- [ ] Health alerts/notifications
- [ ] Plugin marketplace integration

---

## **7. Example Plugin Implementation**

```go
// music_project_manager.go
type musicProjectManagerTool struct {
    // ... existing fields
}

// Implement PluginMetadata
func (m *musicProjectManagerTool) Version() string {
    return "0.0.6"
}

func (m *musicProjectManagerTool) MinAgentVersion() string {
    return "0.0.6" // Requires agent 0.0.6+
}

func (m *musicProjectManagerTool) MaxAgentVersion() string {
    return "" // No max limit
}

func (m *musicProjectManagerTool) APIVersion() string {
    return "v1"
}

// Optional: Health check hook
func (m *musicProjectManagerTool) HealthCheck() error {
    // Check if REAPER paths exist
    if _, err := os.Stat(m.settings.ProjectDir); err != nil {
        return fmt.Errorf("project directory not accessible: %w", err)
    }
    return nil
}
```

---

## **8. Benefits**

✅ **Prevent Breaking Changes**: Catch incompatibilities before they cause issues
✅ **Better User Experience**: Clear warnings and upgrade paths
✅ **Debugging**: Track plugin health over time
✅ **Confidence**: Users know their plugins are working correctly
✅ **Maintenance**: Easier to identify problematic plugins
✅ **Future-Proof**: API versioning allows breaking changes

---

## **Priority Recommendation**

Start with **Phase 1 & 2** (Foundation + Basic Health Checks):
1. Add version metadata to plugins
2. Implement basic compatibility checking
3. Add `/api/plugins/health` endpoint
4. Show warnings on startup

This gives you 80% of the value with 20% of the work!

---

## **Next Steps**

1. Review and approve this plan
2. Create implementation issues/tasks
3. Begin Phase 1 development
4. Update existing plugins to implement new interfaces

---

**Status**: Planning
**Created**: 2025-10-27
**Last Updated**: 2025-10-27
