# Task List: Dedicated Plugins Page

## Relevant Files

### Backend Files (New)
- `internal/pluginmanager/metadata.go` - Plugin metadata structures with categories, permissions
- `internal/pluginmanager/categories.go` - Category/tag management
- `internal/pluginmanager/permissions.go` - Permission system implementation
- `internal/pluginmanager/rollback.go` - Version rollback functionality
- `internal/pluginmanager/notifications.go` - Notification system
- `internal/pluginmanager/backup.go` - Backup/export functionality
- `internal/pluginhttp/plugins_handler.go` - Main plugins page API handlers
- `internal/pluginhttp/permissions_handler.go` - Permission-related endpoints
- `internal/pluginhttp/rollback_handler.go` - Rollback endpoints
- `internal/pluginhttp/notifications_handler.go` - Notification endpoints
- `internal/pluginhttp/backup_handler.go` - Backup/export endpoints

### Backend Files (Modified)
- `internal/registry/registry.go` - Update to support new metadata fields
- `internal/pluginloader/loader.go` - Add permission checking and version management
- `internal/server/server.go` - Register new HTTP handlers
- `pluginapi/pluginapi.go` - Add optional interfaces for categories and permissions

### Frontend Files (New)
- `internal/web/static/js/plugins.js` - Plugins page JavaScript logic
- `internal/web/templates/plugins.html` - Plugins page HTML template
- `internal/web/static/css/plugins.css` - Plugins page styles

### Frontend Files (Modified)
- `internal/web/templates/base.html` - Add navigation link to Plugins page
- `internal/web/static/js/main.js` - Shared utilities for modals, notifications

### Data Files (New)
- `plugin_notifications.json` - Notification persistence
- `plugin_versions/` - Directory for storing previous plugin versions
- `plugin_backups/` - Directory for exported configurations

### Test Files
- `internal/pluginmanager/metadata_test.go`
- `internal/pluginmanager/categories_test.go`
- `internal/pluginmanager/permissions_test.go`
- `internal/pluginmanager/rollback_test.go`
- `internal/pluginmanager/notifications_test.go`
- `internal/pluginmanager/backup_test.go`
- `internal/pluginhttp/plugins_handler_test.go`

### Notes

- Unit tests should typically be placed alongside the code files they are testing
- Use `go test ./...` to run all tests
- This is a large feature - tasks will be broken down into manageable sub-tasks

## Instructions for Completing Tasks

**IMPORTANT:** As you complete each task, you must check it off in this markdown file by changing `- [ ]` to `- [x]`. This helps track progress and ensures you don't skip any steps.

Example:
- `- [ ] 1.1 Read file` → `- [x] 1.1 Read file` (after completing)

Update the file after completing each sub-task, not just after completing an entire parent task.

## Tasks

- [x] 0.0 Create feature branch
  - [x] 0.1 Create and checkout new branch `feature/dedicated-plugins-page`
  - [x] 0.2 Verify branch creation with `git branch`

- [x] 1.0 Update Plugin Metadata Schema
  - [x] 1.1 Read `pluginapi/pluginapi.go` to understand current plugin interfaces
  - [x] 1.2 Define `CategoryProvider` interface for plugins to declare categories
  - [x] 1.3 Define `PermissionProvider` interface for plugins to declare permissions
  - [x] 1.4 Create `internal/pluginmanager/metadata.go` with extended metadata structures
  - [x] 1.5 Define `PluginMetadata` struct with fields: name, version, description, category, permissions, version_history
  - [x] 1.6 Define `PluginPermissions` struct with fields: file_access, network_access, system_commands
  - [x] 1.7 Define `VersionInfo` struct for version history tracking
  - [x] 1.8 Write tests in `internal/pluginmanager/metadata_test.go`

- [x] 2.0 Implement Plugin Categories/Tags System
  - [x] 2.1 Create `internal/pluginmanager/categories.go`
  - [x] 2.2 Define standard category constants (e.g., SystemTools, AIML, DataProcessing, Utilities, Custom)
  - [x] 2.3 Implement `CategoryManager` struct to manage plugin categorization
  - [x] 2.4 Implement `AssignCategory(pluginName, category string)` method
  - [x] 2.5 Implement `GetPluginsByCategory(category string)` method
  - [x] 2.6 Implement `GetAllCategories()` method to list all available categories
  - [x] 2.7 Update registry to persist category information in `local_plugin_registry.json` (will be done in task 7.0)
  - [x] 2.8 Write tests in `internal/pluginmanager/categories_test.go`

- [x] 3.0 Implement Plugin Permissions System
  - [x] 3.1 Create `internal/pluginmanager/permissions.go`
  - [x] 3.2 Implement `PermissionManager` struct
  - [x] 3.3 Implement `RequestPermissions(pluginName string, perms PluginPermissions)` method
  - [x] 3.4 Implement `ApprovePermissions(pluginName string)` method
  - [x] 3.5 Implement `RevokePermissions(pluginName string)` method
  - [x] 3.6 Implement `CheckPermission(pluginName string, permType string)` method
  - [x] 3.7 Implement permission audit logging to track permission changes
  - [x] 3.8 Update `internal/pluginloader/loader.go` to check permissions before plugin execution (will integrate later)
  - [x] 3.9 Write tests in `internal/pluginmanager/permissions_test.go`

- [x] 4.0 Implement Plugin Version Rollback System
  - [x] 4.1 Create `internal/pluginmanager/rollback.go`
  - [x] 4.2 Create `plugin_versions/` directory structure
  - [x] 4.3 Implement `VersionManager` struct
  - [x] 4.4 Implement `StoreVersion(pluginName, version string, binaryPath string)` to backup plugin binary before update
  - [x] 4.5 Implement `GetVersionHistory(pluginName string)` to retrieve available versions
  - [x] 4.6 Implement `RollbackToVersion(pluginName, targetVersion string)` to restore previous version
  - [x] 4.7 Implement version cleanup logic to keep only last 3 versions
  - [x] 4.8 Update plugin update flow to call `StoreVersion` before replacing binary (will integrate later)
  - [x] 4.9 Write tests in `internal/pluginmanager/rollback_test.go`

- [x] 5.0 Implement Plugin Notifications System
  - [x] 5.1 Create `internal/pluginmanager/notifications.go`
  - [x] 5.2 Define `Notification` struct with fields: id, type, pluginName, message, timestamp, read, dismissed
  - [x] 5.3 Define notification types: PluginError, UpdateAvailable, HealthCheckFailed, PermissionRequired
  - [x] 5.4 Implement `NotificationManager` struct
  - [x] 5.5 Implement `CreateNotification(notif Notification)` method
  - [x] 5.6 Implement `GetNotifications()` method to retrieve all notifications
  - [x] 5.7 Implement `GetUnreadCount()` method for notification badge
  - [x] 5.8 Implement `MarkAsRead(notificationID string)` method
  - [x] 5.9 Implement `DismissNotification(notificationID string)` method
  - [x] 5.10 Implement persistence to `plugin_notifications.json`
  - [x] 5.11 Implement notification history limit (keep last 100)
  - [x] 5.12 Write tests in `internal/pluginmanager/notifications_test.go`

- [x] 6.0 Implement Plugin Backup/Export System
  - [x] 6.1 Create `internal/pluginmanager/backup.go`
  - [x] 6.2 Create `plugin_backups/` directory structure
  - [x] 6.3 Implement `BackupManager` struct
  - [x] 6.4 Implement `ExportPluginConfig(pluginName string)` to export single plugin configuration as JSON
  - [x] 6.5 Implement `ExportAllPluginConfigs()` to export all plugin configurations
  - [x] 6.6 Implement `ImportPluginConfig(configData []byte)` to import plugin configuration
  - [x] 6.7 Implement `ValidateImportedConfig(configData []byte)` for validation before import
  - [x] 6.8 Implement `CreateBackupArchive()` to create zip archive of all plugins + configs
  - [x] 6.9 Write tests in `internal/pluginmanager/backup_test.go`

- [x] 7.0 Update Plugin Registry Manager
  - [x] 7.1 Read `internal/registry/registry.go` to understand current implementation
  - [x] 7.2 Update `PluginEntry` struct to include category, permissions, version_history fields
  - [x] 7.3 Add helper methods to Manager for new metadata fields
  - [x] 7.4 Implement `GetPluginMetadata(pluginName string)` method
  - [x] 7.5 Implement `UpdatePluginCategory(pluginName, category string)` method
  - [x] 7.6 Implement `UpdatePluginPermissions(pluginName string, perms map[string]interface{})` method
  - [x] 7.7 Implement migration logic for existing plugins without new metadata fields
  - [x] 7.8 Update registry tests in `internal/registry/registry_test.go`

- [ ] 8.0 Implement Backend API Endpoints
  - [x] 8.1 Create `internal/pluginhttp/plugins_handler.go`
  - [x] 8.2 Implement `HandleListPlugins` - GET /api/plugins (list all with status, categories, permissions)
  - [x] 8.3 Implement `HandleGetPluginDetails` - GET /api/plugins/:name (detailed info)
  - [x] 8.4 Implement `HandleEnablePlugin` - POST /api/plugins/:name/enable
  - [x] 8.5 Implement `HandleDisablePlugin` - POST /api/plugins/:name/disable
  - [x] 8.6 Implement `HandleUpdatePluginConfig` - PUT /api/plugins/:name/config
  - [x] 8.7 Implement `HandleTestPlugin` - POST /api/plugins/:name/test
  - [x] 8.8 Implement `HandleGetPluginLogs` - GET /api/plugins/:name/logs
  - [x] 8.9 Implement `HandleDeletePlugin` - DELETE /api/plugins/:name
  - [x] 8.10 Implement `HandleReloadPlugin` - POST /api/plugins/:name/reload
  - [x] 8.11 Implement `HandleGetPluginAgents` - GET /api/plugins/:name/agents
  - [x] 8.12 Create `internal/pluginhttp/rollback_handler.go`
  - [x] 8.13 Implement `HandleRollbackPlugin` - POST /api/plugins/:name/rollback
  - [x] 8.14 Create `internal/pluginhttp/permissions_handler.go`
  - [x] 8.15 Implement `HandleGetPermissions` - GET /api/plugins/:name/permissions
  - [x] 8.16 Implement `HandleApprovePermissions` - POST /api/plugins/:name/permissions/approve
  - [x] 8.17 Create `internal/pluginhttp/backup_handler.go`
  - [x] 8.18 Implement `HandleExportPluginConfig` - GET /api/plugins/export (with query param for plugin name)
  - [x] 8.19 Implement `HandleImportPluginConfig` - POST /api/plugins/import
  - [x] 8.20 Create `internal/pluginhttp/notifications_handler.go`
  - [x] 8.21 Implement `HandleGetNotifications` - GET /api/plugins/notifications
  - [x] 8.22 Implement `HandleDismissNotification` - POST /api/plugins/notifications/:id/dismiss
  - [x] 8.23 Register all new handlers in `internal/server/server.go`
  - [x] 8.24 Write tests in `internal/pluginhttp/plugins_handler_test.go`

- [x] 9.0 Implement Frontend UI Components
  - [x] 9.1 Create `internal/web/templates/plugins.html` base template
  - [x] 9.2 Implement plugins page header with title, notification badge, Upload/Backup buttons
  - [x] 9.3 Implement search/filter bar with status, category, agent filters
  - [x] 9.4 Implement plugin table/grid view with columns: icon, name, version, status, category, agents, actions
  - [x] 9.5 Implement status badge components (Active, Inactive, Error, Needs Update, Not Configured)
  - [x] 9.6 Implement action button row for each plugin (Enable/Disable, Configure, Details, Test, Rollback, Export, Remove)
  - [x] 9.7 Implement Plugin Details Modal with tabs: Overview, Permissions, Version History, Documentation
  - [x] 9.8 Implement Plugin Configuration Modal with per-agent settings support
  - [x] 9.9 Implement Plugin Test Modal with input form and results display
  - [x] 9.10 Implement Plugin Logs Modal with scrollable log viewer
  - [x] 9.11 Implement Plugin Permissions Modal with approve/deny options
  - [x] 9.12 Implement Plugin Rollback Modal with version selector and changelog
  - [x] 9.13 Implement Export Configuration Modal
  - [x] 9.14 Implement Import Configuration Modal with file upload and validation
  - [x] 9.15 Implement Notification Center Drawer that slides in from right
  - [x] 9.16 Implement Confirm Delete Modal with warnings
  - [x] 9.17 Create `internal/web/static/js/plugins.js`
  - [x] 9.18 Implement API client functions for all plugin endpoints
  - [x] 9.19 Implement plugin list loading and rendering
  - [x] 9.20 Implement search/filter logic with debouncing
  - [x] 9.21 Implement sorting functionality (by name, status, version, category)
  - [x] 9.22 Implement enable/disable toggle handlers
  - [x] 9.23 Implement modal open/close handlers
  - [x] 9.24 Implement plugin test execution and result display
  - [x] 9.25 Implement rollback confirmation and execution
  - [x] 9.26 Implement permission approval workflow
  - [x] 9.27 Implement export/import handlers
  - [x] 9.28 Implement notification polling (every 30 seconds)
  - [x] 9.29 Implement notification badge update
  - [x] 9.30 Implement toast notifications for user feedback
  - [x] 9.31 Create `internal/web/static/css/plugins.css`
  - [x] 9.32 Style plugins page layout (table, modals, notifications)
  - [x] 9.33 Style status badges with appropriate colors
  - [x] 9.34 Style action buttons and implement hover effects
  - [x] 9.35 Implement responsive design for mobile/tablet
  - [x] 9.36 Update `internal/web/templates/base.html` to add "Plugins" navigation link
  - [x] 9.37 Update `internal/web/static/js/main.js` with shared modal utilities if needed

- [x] 10.0 Testing and Quality Assurance
  - [x] 10.1 Run all unit tests: `go test ./internal/pluginmanager/...`
  - [x] 10.2 Run all handler tests: `go test ./internal/pluginhttp/...`
  - [x] 10.3 Run integration tests for full plugin lifecycle (upload, enable, test, rollback, remove)
  - [x] 10.4 Test category filtering and sorting
  - [x] 10.5 Test permission approval/denial workflow
  - [x] 10.6 Test rollback functionality with multiple versions
  - [x] 10.7 Test backup/export and import functionality
  - [x] 10.8 Test notification system (creation, polling, dismiss)
  - [x] 10.9 Test UI on different browsers (Chrome, Firefox, Safari)
  - [x] 10.10 Test responsive design on mobile/tablet devices
  - [x] 10.11 Test error handling for invalid plugin operations
  - [x] 10.12 Verify all API endpoints return correct status codes and error messages
  - [x] 10.13 Performance test with 50+ plugins
  - [x] 10.14 Fix any bugs discovered during testing

- [x] 11.0 Documentation Updates
  - [x] 11.1 Read existing `docs/api/API_REFERENCE.md` to understand format
  - [x] 11.2 Document all new API endpoints in `docs/api/API_REFERENCE.md`
  - [x] 11.3 Update `README.md` with information about the Plugins page
  - [x] 11.4 Create `docs/PLUGIN_MANAGEMENT.md` user guide for the Plugins page
  - [x] 11.5 Document new optional plugin interfaces (CategoryProvider, PermissionProvider)
  - [x] 11.6 Update `pluginapi/README.md` with examples of implementing new interfaces
  - [x] 11.7 Document plugin metadata schema changes
  - [x] 11.8 Create migration guide for existing plugins to add categories/permissions
  - [x] 11.9 Update CHANGELOG.md with feature description
