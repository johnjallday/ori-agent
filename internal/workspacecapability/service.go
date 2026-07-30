package workspacecapability

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// WorkspaceStore is the slice of the workspace store the lifecycle service
// needs. Update is the canonical mutate-then-save primitive: it holds the
// per-workspace lock across read and write, which is what makes a concurrent
// double install produce one record rather than two.
type WorkspaceStore interface {
	Get(id string) (*workspace.Workspace, error)
	Update(id string, fn func(*workspace.Workspace) error) error
}

// Error codes returned by the lifecycle service. They are stable strings so the
// HTTP layer can map them onto status codes and the browser can branch on them.
const (
	// CodeWorkspaceMissing means the workspace does not exist.
	CodeWorkspaceMissing = "workspace_missing"
	// CodeCapabilityUnavailable means the requested ID is not in this build's
	// compiled allowlist.
	CodeCapabilityUnavailable = "capability_unavailable"
	// CodeInstallLimit means the workspace already holds the maximum number of
	// active installs this capability permits.
	CodeInstallLimit = "install_limit_reached"
	// CodeInstallFailed means the install could not be persisted, or a
	// capability-specific install step failed and was rolled back.
	CodeInstallFailed = "install_failed"
	// CodeInstallIncomplete means an install step failed AND its rollback also
	// failed, so the workspace needs repair. This is the only outcome that can
	// leave a record behind after a failure.
	CodeInstallIncomplete = "install_incomplete"
)

// Error is a lifecycle failure with a stable code and a user-facing message.
type Error struct {
	Code    string
	Message string
	// Repair names a user-visible action that can resolve the failure, when one
	// exists.
	Repair string
	// Err is the underlying cause, kept for logs. It is never sent to a client.
	Err error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

// Service owns the capability lifecycle for a workspace: what is available,
// what is installed, and installing it.
//
// Ownership is enforced at the HTTP boundary, matching the convention every
// other workspace handler follows (FR-140). The service takes an already
// authorized workspace ID.
type Service struct {
	registry *Registry
	store    WorkspaceStore
	now      func() time.Time
}

// NewService builds the lifecycle service.
func NewService(registry *Registry, store WorkspaceStore) *Service {
	return &Service{registry: registry, store: store, now: time.Now}
}

// Registry exposes the compiled allowlist backing this service.
func (s *Service) Registry() *Registry { return s.registry }

func (s *Service) clock() time.Time {
	if s == nil || s.now == nil {
		return time.Now()
	}
	return s.now()
}

// CatalogItem is one capability as the catalog surface sees it: what it is,
// whether this workspace has it, and — when installed — its derived health.
type CatalogItem struct {
	Definition Definition `json:"definition"`
	Installed  bool       `json:"installed"`
	// Record is the persisted install record when installed.
	Record *workspace.InstalledCapability `json:"record,omitempty"`
	// Status is derived at read time from the capability's service, never from
	// a persisted status string (FR-6). Zero-valued for a capability that is
	// not installed.
	Status *Status `json:"status,omitempty"`
	// Available reports whether the ID resolves to a definition compiled into
	// this build. An installed-but-unavailable capability stays listed so the
	// user can see it, but nothing about it can execute (FR-14).
	Available bool `json:"available"`
	// Unavailable explains why Available is false.
	Unavailable string `json:"unavailable,omitempty"`
	// NeedsMigration reports definition-version drift (FR-13).
	NeedsMigration bool `json:"needs_migration,omitempty"`
}

// Catalog lists every capability relevant to one workspace: all compiled
// definitions (FR-16-FR-18), plus any installed record this build cannot
// resolve, which is listed last so it stays visible rather than vanishing.
func (s *Service) Catalog(workspaceID string) ([]CatalogItem, error) {
	ws, err := s.loadWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}

	installed := ws.GetInstalledCapabilities()
	byID := make(map[string]workspace.InstalledCapability, len(installed))
	for _, record := range installed {
		byID[workspace.NormalizeCapabilityID(record.ID)] = record
	}

	items := make([]CatalogItem, 0, len(installed)+1)
	for _, def := range s.registry.Definitions() {
		item := CatalogItem{Definition: def, Available: true}
		if record, ok := byID[def.ID]; ok {
			delete(byID, def.ID)
			item.Installed = true
			recordCopy := record
			item.Record = &recordCopy
			item.NeedsMigration = record.Version != def.Version
			item.Status = s.statusFor(record, workspaceID)
		}
		items = append(items, item)
	}

	// Installed records with no compiled definition. Listed, never executed.
	for _, record := range installed {
		id := workspace.NormalizeCapabilityID(record.ID)
		if _, unresolved := byID[id]; !unresolved {
			continue
		}
		delete(byID, id)
		resolved := s.registry.Resolve(record)
		recordCopy := resolved.Record
		items = append(items, CatalogItem{
			Definition:  resolved.Definition,
			Installed:   true,
			Record:      &recordCopy,
			Status:      &Status{State: StatusUnavailable, Detail: resolved.Unavailable},
			Available:   false,
			Unavailable: resolved.Unavailable,
		})
	}

	return items, nil
}

// statusFor derives one installed capability's health, downgrading a failure to
// a renderable state rather than failing the whole catalog (FR-145).
func (s *Service) statusFor(record workspace.InstalledCapability, workspaceID string) *Status {
	status, _, err := s.registry.Status(record, workspaceID)
	if err != nil {
		logger.Warn("Capability status check failed", logger.Fields{
			"workspace_id": workspaceID,
			"capability":   record.ID,
			"error":        err.Error(),
		})
	}
	return &status
}

// InstallRequest asks for one capability to be installed on one workspace.
type InstallRequest struct {
	WorkspaceID  string
	CapabilityID string
	// Source records which flow performed the install (FR-5). Defaults to
	// InstallSourceInPlace.
	Source string
}

// InstallResult describes the outcome of an install.
type InstallResult struct {
	Record workspace.InstalledCapability `json:"record"`
	// AlreadyInstalled reports that the capability was already present, so
	// nothing changed. Repeating an install is success, not an error (FR-9).
	AlreadyInstalled bool       `json:"already_installed"`
	Definition       Definition `json:"definition"`
	Status           Status     `json:"status"`
}

// Install installs a built-in capability on a workspace.
//
// The transaction is deliberately narrow (FR-10, FR-19-FR-23): it writes the
// install record and nothing else. It does not rename, reparent, or retemplate
// the workspace; does not touch tasks, agents, or the workspace team; does not
// create another workspace; does not request or grant folder access; and does
// not register a watcher or schedule. Everything that needs the user's approval
// happens later, in setup.
func (s *Service) Install(req InstallRequest) (InstallResult, error) {
	def, err := s.resolveInstallable(req.CapabilityID)
	if err != nil {
		return InstallResult{}, err
	}

	ws, err := s.loadWorkspace(req.WorkspaceID)
	if err != nil {
		return InstallResult{}, err
	}

	// Idempotent fast path. Also checked inside the Update below, under the
	// per-workspace lock, which is what actually makes concurrent installs safe;
	// this only avoids a pointless write.
	if existing, ok := ws.GetInstalledCapability(def.ID); ok {
		return InstallResult{
			Record:           existing,
			AlreadyInstalled: true,
			Definition:       def,
			Status:           *s.statusFor(existing, req.WorkspaceID),
		}, nil
	}

	source := workspace.InstallSourceInPlace
	if trimmed := strings.TrimSpace(req.Source); trimmed != "" {
		source = trimmed
	}
	record := workspace.InstalledCapability{
		ID:          def.ID,
		Version:     def.Version,
		InstalledAt: s.clock(),
		Source:      source,
	}

	alreadyInstalled := false
	updateErr := s.store.Update(req.WorkspaceID, func(w *workspace.Workspace) error {
		if existing, ok := w.GetInstalledCapability(def.ID); ok {
			alreadyInstalled = true
			record = existing
			return nil
		}
		if err := enforceInstallLimit(w, def); err != nil {
			return err
		}
		added, addErr := w.AddInstalledCapability(record)
		if addErr != nil {
			return addErr
		}
		if !added {
			alreadyInstalled = true
		}
		return nil
	})
	if updateErr != nil {
		var limitErr *Error
		if errors.As(updateErr, &limitErr) {
			return InstallResult{}, limitErr
		}
		return InstallResult{}, &Error{
			Code:    CodeInstallFailed,
			Message: fmt.Sprintf("%s could not be installed. Nothing was changed.", def.Display.Name),
			Err:     updateErr,
		}
	}

	if alreadyInstalled {
		return InstallResult{
			Record:           record,
			AlreadyInstalled: true,
			Definition:       def,
			Status:           *s.statusFor(record, req.WorkspaceID),
		}, nil
	}

	// Capability-specific install work, if this capability has any. File Janitor
	// deliberately has none in v1 — the narrow transaction above IS the install.
	// The hook and its rollback exist so a capability that does need setup work
	// cannot leave a half-built install behind (FR-15).
	if err := s.runInstallHook(def, req.WorkspaceID); err != nil {
		return InstallResult{}, s.rollbackInstall(def, req.WorkspaceID, err)
	}

	return InstallResult{
		Record:     record,
		Definition: def,
		Status:     *s.statusFor(record, req.WorkspaceID),
	}, nil
}

// runInstallHook invokes the capability's optional Installer.
func (s *Service) runInstallHook(def Definition, workspaceID string) error {
	runtime, ok := s.registry.Runtime(def.ID)
	if !ok {
		return nil
	}
	installer, ok := runtime.(Installer)
	if !ok {
		return nil
	}
	return installer.OnCapabilityInstall(workspaceID)
}

// rollbackInstall undoes a partial install: it stops any automation the failed
// step may have started, then removes the install record so no station, catalog
// entry, or status can claim the capability is active (FR-15).
//
// If the rollback itself fails the record stays, and the caller gets
// CodeInstallIncomplete — a repairable state the user can see and retry, which
// is strictly better than a silently half-installed capability.
func (s *Service) rollbackInstall(def Definition, workspaceID string, cause error) error {
	if runtime, ok := s.registry.Runtime(def.ID); ok {
		if controller, ok := runtime.(AutomationController); ok {
			if stopErr := controller.StopCapabilityAutomation(workspaceID); stopErr != nil {
				logger.Warn("Capability rollback could not stop automation", logger.Fields{
					"workspace_id": workspaceID,
					"capability":   def.ID,
					"error":        stopErr.Error(),
				})
			}
		}
	}

	removeErr := s.store.Update(workspaceID, func(w *workspace.Workspace) error {
		w.RemoveInstalledCapability(def.ID)
		return nil
	})
	if removeErr != nil {
		logger.Error("Capability install rollback failed", logger.Fields{
			"workspace_id": workspaceID,
			"capability":   def.ID,
			"error":        removeErr.Error(),
		})
		return &Error{
			Code:    CodeInstallIncomplete,
			Message: fmt.Sprintf("%s could not finish installing, and cleaning up also failed. Try removing it and installing again.", def.Display.Name),
			Repair:  "remove_capability",
			Err:     errors.Join(cause, removeErr),
		}
	}

	return &Error{
		Code:    CodeInstallFailed,
		Message: fmt.Sprintf("%s could not be installed. Nothing was changed.", def.Display.Name),
		Err:     cause,
	}
}

// enforceInstallLimit rejects an install that would exceed the capability's
// declared per-workspace maximum (FR-8). One record per ID is already
// structurally guaranteed by the workspace model; this catches a definition
// that declares a limit the model alone would not enforce.
func enforceInstallLimit(w *workspace.Workspace, def Definition) error {
	limit := def.Requirements.MaxInstallsPerWorkspace
	if limit <= 0 {
		return nil
	}
	active := 0
	for _, record := range w.GetInstalledCapabilities() {
		if workspace.NormalizeCapabilityID(record.ID) == def.ID {
			active++
		}
	}
	if active < limit {
		return nil
	}
	return &Error{
		Code:    CodeInstallLimit,
		Message: fmt.Sprintf("%s is already installed in this workspace.", def.Display.Name),
	}
}

// resolveInstallable returns the compiled definition for id, or a fail-closed
// error. A capability this build does not know about can never be installed.
func (s *Service) resolveInstallable(id string) (Definition, error) {
	def, ok := s.registry.Definition(id)
	if !ok {
		return Definition{}, &Error{
			Code:    CodeCapabilityUnavailable,
			Message: "That capability is not available in this version of Ori.",
		}
	}
	return def, nil
}

func (s *Service) loadWorkspace(workspaceID string) (*workspace.Workspace, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return nil, &Error{Code: CodeWorkspaceMissing, Message: "Workspace not found."}
	}
	ws, err := s.store.Get(workspaceID)
	if err != nil || ws == nil {
		return nil, &Error{Code: CodeWorkspaceMissing, Message: "Workspace not found.", Err: err}
	}
	return ws, nil
}
