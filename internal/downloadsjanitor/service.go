package downloadsjanitor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	workspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// JanitorBindingAlias is the alias of the workspace MCP binding scoped to the
// approved inbox root. It is separate from the workspace's own files binding so
// the Downloads root can carry a strictly smaller tool allowlist than the
// workspace folder does.
const JanitorBindingAlias = "downloads_janitor_root"

// filesystemServerName is the MCP server template both bindings instantiate.
const filesystemServerName = "filesystem"

// JanitorReadTools is the complete set of filesystem MCP tools the Downloads
// Curator may use against the approved root: listing and metadata only.
//
// Everything absent from this list is absent by design. `read_file` is excluded
// so metadata-only mode cannot read contents (FR-113), and every mutation tool
// (`move_file`, `delete_file`, `copy_file`, `write_file`, `create_directory`)
// is excluded so the agent can never mutate the folder (FR-112). Approved moves
// are issued by the Janitor service itself, not through the agent's tools.
var JanitorReadTools = []string{
	"list_directory",
	"list_directory_with_sizes",
	"search_files",
	"get_file_info",
}

// forbiddenAgentTools are the tools that must never appear on the Janitor
// binding. Listed explicitly so a regression is caught by name rather than by
// the allowlist happening to be short.
var forbiddenAgentTools = []string{
	"read_file",
	"read_text_file",
	"read_media_file",
	"move_file",
	"delete_file",
	"copy_file",
	"write_file",
	"edit_file",
	"create_directory",
}

// Setup failure codes. They are stable identifiers the UI maps to a repair
// action; the accompanying message is the only thing shown to the user, so raw
// OS error text never reaches the client (FR-110).
const (
	CodeInvalidPath        = "invalid_path"
	CodeRootMissing        = "root_missing"
	CodeNotADirectory      = "not_a_directory"
	CodePermissionDenied   = "permission_denied"
	CodeDestinationBlocked = "destination_blocked"
	CodeBindingFailed      = "binding_failed"
	CodePersistenceFailed  = "persistence_failed"
	CodeWorkspaceMissing   = "workspace_missing"
	CodeNotConfigured      = "not_configured"
	CodePending            = "pending"
)

// Repair actions the UI can offer for a failing component.
const (
	RepairChooseFolder    = "choose_folder"
	RepairGrantPermission = "grant_permission"
	RepairRelinkFolder    = "relink_folder"
	RepairRetry           = "retry"
)

// SetupError is a user-relayable setup or readiness failure. Code and Message
// are safe to return over the API; cause is kept for server logs only and is
// never serialized.
type SetupError struct {
	Code    string
	Message string
	Repair  string
	cause   error
}

func (e *SetupError) Error() string {
	if e == nil {
		return ""
	}
	if e.cause != nil {
		return fmt.Sprintf("%s: %s (%v)", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *SetupError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func setupErr(code, message, repair string, cause error) *SetupError {
	return &SetupError{Code: code, Message: message, Repair: repair, cause: cause}
}

// permissionGuidance is the actionable message for a folder Ori is not allowed
// to read. On macOS it names the specific Files & Folders control; it never
// asks for Full Disk Access, which is broader than this feature needs (FR-16).
func permissionGuidance(label string) string {
	if label == "" {
		label = "this folder"
	}
	if runtime.GOOS == "darwin" {
		return fmt.Sprintf("Ori does not have permission to read %s. Open System Settings → Privacy & Security → Files and Folders, allow Ori access to the folder, then try again.", label)
	}
	return fmt.Sprintf("Ori does not have permission to read %s. Check the folder's permissions and try again.", label)
}

// WorkspaceStore is the slice of workspace persistence the Janitor needs:
// reading a workspace and atomically updating it.
type WorkspaceStore interface {
	Get(id string) (*workspace.Workspace, error)
	Update(id string, fn func(*workspace.Workspace) error) error
}

// FolderWorkspaceSource reads the canonical workspace.json record. Template
// provenance — including the directory requirement this feature was created
// for — has no SQLite column, so a store whose Get serves the SQLite mirror
// returns it empty. Stores that can reach disk implement this, and the service
// prefers it whenever provenance matters.
type FolderWorkspaceSource interface {
	GetFolderWorkspace(id string) (*workspace.Workspace, error)
}

// Service owns Downloads Janitor setup and readiness for every workspace. It is
// the only component that resolves a user's folder selection into a configured
// root, and the only one that grants the workspace's Janitor MCP binding.
type Service struct {
	store      *Store
	workspaces WorkspaceStore
	scanner    *Scanner
	mover      Mover
	trash      TrashRemover
	notifier   Notifier
	provider   ClassificationProvider
	// automation reports whether the watcher and daily schedule are actually
	// registered. Until it is wired, those checks stay pending — which keeps a
	// workspace out of Ready rather than overstating what is running.
	automation AutomationStatus
	now        func() time.Time
}

// NewService wires the Janitor service to its settings store and the workspace
// store that owns directory references and MCP bindings.
func NewService(store *Store, workspaces WorkspaceStore) *Service {
	return &Service{store: store, workspaces: workspaces, scanner: NewScanner(store, workspaces), now: time.Now}
}

// Status is the full setup/readiness picture for one workspace.
type Status struct {
	Settings  JanitorSettings `json:"settings"`
	Readiness Readiness       `json:"readiness"`
	// Applies reports whether this workspace is a Downloads Janitor workspace at
	// all. The UI mounts its panel only when it is true, so the Janitor surface
	// never appears on an unrelated workspace.
	Applies bool `json:"applies"`
	// Privacy states in plain language what Ori reads and where anything read
	// goes. It travels with every status response so no surface can show the
	// feature without showing its privacy posture.
	Privacy PrivacyState `json:"privacy"`
	// Suggestion describes the folder the workspace's template asked for. It is
	// present before setup so the UI can pre-fill the picker; it is never a
	// resolved path and never implies access.
	Suggestion *SetupSuggestion `json:"suggestion,omitempty"`
}

// SetupSuggestion is the unresolved folder request recorded on the workspace at
// creation time, echoed back for the setup card.
type SetupSuggestion struct {
	Key              string `json:"key"`
	Label            string `json:"label,omitempty"`
	SuggestedPath    string `json:"suggested_path,omitempty"`
	AccessDisclosure string `json:"access_disclosure,omitempty"`
	// FilingRootName is the destination folder name that will be created inside
	// the chosen root, so the card can state exactly where files will go.
	FilingRootName string `json:"filing_root_name"`
	// DailyScanLocalTime is the catch-up time the card shows, in local time.
	DailyScanLocalTime string `json:"daily_scan_local_time"`
}

// DirectoryRequirementKey is the directory key Downloads Janitor's built-in
// template declares.
const DirectoryRequirementKey = "downloads-root"

// SetPaused pauses or resumes unattended scanning. It changes nothing else:
// settings, pending candidates, and history all survive, so resuming picks up
// where the user left off.
func (s *Service) SetPaused(workspaceID string, paused bool) (Status, error) {
	if _, err := s.store.UpdateSettings(workspaceID, func(settings *JanitorSettings) error {
		settings.Paused = paused
		return nil
	}); err != nil {
		return Status{}, err
	}
	return s.Status(workspaceID)
}

// readWorkspace loads a workspace, preferring the canonical folder record so
// callers see fields (template provenance above all) that the SQLite mirror
// does not carry.
func (s *Service) readWorkspace(workspaceID string) (*workspace.Workspace, error) {
	if s == nil || s.workspaces == nil {
		return nil, errors.New("workspace storage is unavailable")
	}
	return readWorkspaceRecord(s.workspaces, workspaceID)
}

// readWorkspaceRecord prefers the canonical folder record and falls back to the
// store's own Get. Shared by the service and the scanner so both agree on where
// a workspace's truth lives.
func readWorkspaceRecord(store WorkspaceStore, workspaceID string) (*workspace.Workspace, error) {
	if store == nil {
		return nil, errors.New("workspace storage is unavailable")
	}
	if folders, ok := store.(FolderWorkspaceSource); ok {
		if ws, err := folders.GetFolderWorkspace(workspaceID); err == nil && ws != nil {
			return ws, nil
		}
	}
	return store.Get(workspaceID)
}

// AppliesTo reports whether a workspace is a Downloads Janitor workspace: one
// created from the built-in template, or one already configured. The UI uses it
// to mount the Janitor panel only where it belongs, and it is answered
// server-side from provenance rather than trusted from the client.
func (s *Service) AppliesTo(workspaceID string) bool {
	if settings, err := s.store.LoadSettings(workspaceID); err == nil && settings.IsSetUp() {
		return true
	}
	ws, err := s.readWorkspace(workspaceID)
	if err != nil || ws == nil {
		return false
	}
	if ws.IsFromTemplate(TemplateID) {
		return true
	}
	for _, req := range ws.PendingDirectoryRequirements() {
		if req.Key == DirectoryRequirementKey {
			return true
		}
	}
	return false
}

// TemplateID is the built-in template a Downloads Janitor workspace is created
// from.
const TemplateID = "downloads-janitor"

// Status returns the workspace's settings plus a fresh readiness evaluation.
func (s *Service) Status(workspaceID string) (Status, error) {
	settings, err := s.store.LoadSettings(workspaceID)
	if err != nil {
		return Status{}, err
	}
	status := Status{
		Settings:  settings,
		Readiness: s.evaluateReadiness(settings),
		Applies:   s.AppliesTo(workspaceID),
		Privacy:   s.Privacy(settings),
	}
	if !settings.IsSetUp() {
		status.Suggestion = s.suggestion(workspaceID, settings)
	}
	return status, nil
}

// suggestion reads the unresolved directory requirement recorded on the
// workspace at creation. A workspace whose provenance is missing still gets a
// usable card: the caller sees the defaults, not an error.
func (s *Service) suggestion(workspaceID string, settings JanitorSettings) *SetupSuggestion {
	out := &SetupSuggestion{
		Key:                DirectoryRequirementKey,
		FilingRootName:     settings.FilingRootName,
		DailyScanLocalTime: settings.DailyScanLocalTime,
	}
	ws, err := s.readWorkspace(workspaceID)
	if err != nil || ws == nil {
		return out
	}
	for _, req := range ws.PendingDirectoryRequirements() {
		if req.Key != DirectoryRequirementKey && len(ws.PendingDirectoryRequirements()) > 1 {
			continue
		}
		out.Key = req.Key
		out.Label = req.Label
		out.SuggestedPath = req.SuggestedPath
		out.AccessDisclosure = req.AccessDisclosure
		break
	}
	if recipe, ok := ws.TemplateAutomationRecipeFor(out.Key); ok && recipe.DailyScan != nil {
		if normalized, err := workspace.NormalizeLocalTimeOfDay(recipe.DailyScan.LocalTime); err == nil {
			out.DailyScanLocalTime = normalized
		}
	}
	return out
}

// SetupRequest is a user-confirmed folder selection. Path is the only
// caller-supplied path anywhere in the feature, and it is accepted exactly once
// — at explicit confirmation — after which every later path is derived from the
// stored root.
type SetupRequest struct {
	WorkspaceID string
	// Path is the folder the user picked or confirmed. "~" is expanded here,
	// server-side, and only now (FR-13).
	Path string
	// DailyScanLocalTime and Timezone are optional; empty keeps the current or
	// default values.
	DailyScanLocalTime string
	Timezone           string
}

// ConfirmSetup validates a confirmed folder selection and configures the
// workspace for it. It is idempotent: repeating it with the same folder updates
// the existing directory reference and binding rather than creating duplicates
// (FR-24).
//
// Ordering is deliberate. Everything that can fail — path validation, the
// destination directory, the workspace binding — happens before the settings
// record is written, so a failed setup never leaves a workspace that looks
// configured (FR-14, FR-17, FR-19).
func (s *Service) ConfirmSetup(req SetupRequest) (Status, error) {
	workspaceID := strings.TrimSpace(req.WorkspaceID)
	if workspaceID == "" {
		return Status{}, setupErr(CodeWorkspaceMissing, "This workspace is unavailable.", "", nil)
	}

	current, err := s.store.LoadSettings(workspaceID)
	if err != nil {
		return Status{}, err
	}

	root, err := resolveSetupRoot(req.Path)
	if err != nil {
		return Status{}, err
	}

	next := current
	next.WorkspaceID = workspaceID
	next.RootPath = root
	if next.FilingRootName == "" {
		next.FilingRootName = DefaultFilingRootName
	}
	if t := strings.TrimSpace(req.DailyScanLocalTime); t != "" {
		normalized, timeErr := workspace.NormalizeLocalTimeOfDay(t)
		if timeErr != nil {
			return Status{}, setupErr(CodeInvalidPath, "The daily scan time must be a 24-hour time such as 09:00.", RepairRetry, timeErr)
		}
		next.DailyScanLocalTime = normalized
	}
	if tz := strings.TrimSpace(req.Timezone); tz != "" {
		if _, tzErr := time.LoadLocation(tz); tzErr != nil {
			return Status{}, setupErr(CodeInvalidPath, "That timezone is not recognized.", RepairRetry, tzErr)
		}
		next.Timezone = tz
	}

	// Create the destination folder now so the user sees "Filed" appear as part
	// of the setup they approved, rather than at the first move. Repeat-safe.
	if err := ensureFilingRoot(next.FilingRootPath()); err != nil {
		return Status{}, err
	}

	referenceID, err := s.ensureWorkspaceAccess(workspaceID, root)
	if err != nil {
		return Status{}, err
	}
	next.DirectoryReferenceID = referenceID
	if next.SetupCompletedAt.IsZero() {
		next.SetupCompletedAt = s.clock()
	}

	if _, err := s.store.UpdateSettings(workspaceID, func(settings *JanitorSettings) error {
		*settings = next
		return nil
	}); err != nil {
		if errors.Is(err, ErrInvalidSettings) {
			return Status{}, setupErr(CodeInvalidPath, "That folder configuration is not valid.", RepairRetry, err)
		}
		return Status{}, setupErr(CodePersistenceFailed, "Ori could not save this workspace's Downloads Janitor settings.", RepairRetry, err)
	}

	// Return through Status so the response carries everything a status
	// response does — privacy disclosure included. Building a Status literal
	// here is how the setup reply ended up with no privacy state at all.
	return s.Status(workspaceID)
}

func (s *Service) clock() time.Time {
	if s == nil || s.now == nil {
		return time.Now()
	}
	return s.now()
}

// resolveSetupRoot turns a user-confirmed selection into the normalized
// absolute path the rest of the feature derives everything from. This is the
// only place "~" is expanded, and the only place a caller-supplied path is
// accepted at all.
func resolveSetupRoot(raw string) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", setupErr(CodeInvalidPath, "Choose a folder to tidy.", RepairChooseFolder, nil)
	}
	if strings.ContainsRune(path, 0) {
		return "", setupErr(CodeInvalidPath, "That folder path is not valid.", RepairChooseFolder, nil)
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", setupErr(CodeInvalidPath, "Ori could not locate your home folder. Choose the folder directly instead.", RepairChooseFolder, err)
		}
		path = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", setupErr(CodeInvalidPath, "That folder path is not valid.", RepairChooseFolder, err)
	}
	abs = filepath.Clean(abs)

	info, err := os.Stat(abs)
	if err != nil {
		switch {
		case os.IsNotExist(err):
			return "", setupErr(CodeRootMissing, "That folder no longer exists. Choose a folder that is on this computer.", RepairChooseFolder, err)
		case os.IsPermission(err):
			return "", setupErr(CodePermissionDenied, permissionGuidance("that folder"), RepairGrantPermission, err)
		default:
			return "", setupErr(CodeInvalidPath, "Ori could not open that folder.", RepairChooseFolder, err)
		}
	}
	if !info.IsDir() {
		return "", setupErr(CodeNotADirectory, "That is a file, not a folder. Choose the folder that holds your downloads.", RepairChooseFolder, nil)
	}
	// Readability is part of validation, not a later surprise: a folder Ori
	// cannot list is not a folder Ori can tidy.
	entries, err := os.Open(abs) // #nosec G304 -- abs is the user's explicitly confirmed folder selection
	if err != nil {
		if os.IsPermission(err) {
			return "", setupErr(CodePermissionDenied, permissionGuidance("that folder"), RepairGrantPermission, err)
		}
		return "", setupErr(CodeInvalidPath, "Ori could not read that folder.", RepairChooseFolder, err)
	}
	defer func() { _ = entries.Close() }()
	if _, err := entries.Readdirnames(1); err != nil && err.Error() != "EOF" {
		if os.IsPermission(err) {
			return "", setupErr(CodePermissionDenied, permissionGuidance("that folder"), RepairGrantPermission, err)
		}
	}
	return abs, nil
}

// ensureFilingRoot creates <root>/Filed if it is missing. Repeating it is
// harmless, and it never creates a category folder — those appear only when an
// approved move needs one (FR-19, FR-82).
func ensureFilingRoot(path string) error {
	if strings.TrimSpace(path) == "" {
		return setupErr(CodeDestinationBlocked, "Ori could not work out where to file your downloads.", RepairRetry, nil)
	}
	info, err := os.Stat(path)
	switch {
	case err == nil && info.IsDir():
		return nil
	case err == nil:
		return setupErr(CodeDestinationBlocked, fmt.Sprintf("A file named %q already exists in that folder, so Ori cannot create the folder it files into. Rename or move it, then try again.", filepath.Base(path)), RepairRetry, nil)
	case !os.IsNotExist(err):
		if os.IsPermission(err) {
			return setupErr(CodePermissionDenied, permissionGuidance("that folder"), RepairGrantPermission, err)
		}
		return setupErr(CodeDestinationBlocked, "Ori could not prepare the folder it files into.", RepairRetry, err)
	}
	if err := os.MkdirAll(path, 0o750); err != nil {
		if os.IsPermission(err) {
			return setupErr(CodePermissionDenied, permissionGuidance("that folder"), RepairGrantPermission, err)
		}
		return setupErr(CodeDestinationBlocked, "Ori could not create the folder it files into.", RepairRetry, err)
	}
	return nil
}

// ensureWorkspaceAccess records the approved root as a workspace directory
// reference and grants exactly one read-only filesystem binding for it,
// returning the reference ID. Both are idempotent, so repeating setup with the
// same folder updates rather than duplicates (FR-24).
func (s *Service) ensureWorkspaceAccess(workspaceID, root string) (string, error) {
	if s.workspaces == nil {
		return "", setupErr(CodeWorkspaceMissing, "This workspace is unavailable.", "", nil)
	}
	var referenceID string
	err := s.workspaces.Update(workspaceID, func(ws *workspace.Workspace) error {
		if ws == nil {
			return errors.New("workspace is nil")
		}
		// Before linking a new folder, make sure the workspace's pre-existing
		// filesystem access is explicit. Ori synthesizes an all-tools binding
		// covering every directory reference when a workspace has no explicit
		// filesystem binding — linking Downloads into that would silently hand
		// the agent mutation tools over the Downloads root.
		pinExistingFilesystemAccess(ws)

		referenceID = ensureDirectoryReference(ws, root)
		if referenceID == "" {
			return errors.New("directory reference was not recorded")
		}
		ensureJanitorBinding(ws, root)
		return nil
	})
	if err != nil {
		var setupError *SetupError
		if errors.As(err, &setupError) {
			return "", setupError
		}
		return "", setupErr(CodeBindingFailed, "Ori could not link that folder to this workspace.", RepairRetry, err)
	}
	return referenceID, nil
}

// pinExistingFilesystemAccess materializes the synthesized workspace-filesystem
// binding as an explicit one when the workspace has none, freezing it at the
// roots it covers today. Without this, adding the Downloads reference would
// widen that synthesized binding to include Downloads with its full tool set.
func pinExistingFilesystemAccess(ws *workspace.Workspace) {
	for _, binding := range ws.MCPBindings {
		if strings.EqualFold(strings.TrimSpace(binding.ServerName), filesystemServerName) {
			return
		}
	}
	roots := existingDirectoryRoots(ws)
	if len(roots) == 0 {
		return
	}
	_ = ws.UpsertMCPBinding(workspace.MCPBinding{
		ID:         uuid.New().String(),
		ServerName: filesystemServerName,
		Alias:      "workspace_filesystem",
		Enabled:    true,
		Scope:      map[string]any{"roots": roots},
	})
}

// existingDirectoryRoots lists the absolute directory-reference paths a
// workspace already links, mirroring how Ori's synthesized filesystem binding
// derives its roots.
func existingDirectoryRoots(ws *workspace.Workspace) []string {
	seen := map[string]struct{}{}
	roots := make([]string, 0, len(ws.DirectoryReferences))
	for _, ref := range ws.DirectoryReferences {
		path := strings.TrimSpace(ref.Path)
		if path == "" {
			continue
		}
		cleaned := filepath.Clean(path)
		if !filepath.IsAbs(cleaned) {
			continue
		}
		key := strings.ToLower(cleaned)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		roots = append(roots, cleaned)
	}
	sort.Strings(roots)
	return roots
}

// ensureDirectoryReference returns the ID of the workspace's directory
// reference for root, creating one only if no reference already points there.
func ensureDirectoryReference(ws *workspace.Workspace, root string) string {
	target := filepath.Clean(root)
	for _, ref := range ws.DirectoryReferences {
		if filepath.Clean(ref.Path) == target {
			return ref.ID
		}
	}
	if err := ws.AddDirectoryReference(workspace.DirectoryReference{
		Name: filepath.Base(target),
		Path: target,
	}); err != nil {
		return ""
	}
	for _, ref := range ws.DirectoryReferences {
		if filepath.Clean(ref.Path) == target {
			return ref.ID
		}
	}
	return ""
}

// ensureJanitorBinding creates or updates the single filesystem binding scoped
// to the approved root, always with the read-only allowlist. Rewriting the
// allowlist on every setup is intentional: a binding that drifted (by an edit,
// an import, or an older build) is repaired rather than trusted.
func ensureJanitorBinding(ws *workspace.Workspace, root string) {
	root = filepath.Clean(root)
	allowed := append([]string(nil), JanitorReadTools...)
	for i := range ws.MCPBindings {
		if strings.EqualFold(strings.TrimSpace(ws.MCPBindings[i].Alias), JanitorBindingAlias) {
			ws.MCPBindings[i].ServerName = filesystemServerName
			ws.MCPBindings[i].Enabled = true
			ws.MCPBindings[i].Config = map[string]any{"roots": []string{root}}
			ws.MCPBindings[i].AllowedTools = allowed
			ws.MCPBindings[i].DefaultSideEffect = workspace.SideEffectRead
			ws.MCPBindings[i].UpdatedAt = time.Now()
			return
		}
	}
	_ = ws.UpsertMCPBinding(workspace.MCPBinding{
		ID:                uuid.New().String(),
		ServerName:        filesystemServerName,
		Alias:             JanitorBindingAlias,
		Enabled:           true,
		Config:            map[string]any{"roots": []string{root}},
		AllowedTools:      allowed,
		DefaultSideEffect: workspace.SideEffectRead,
	})
}

// evaluateReadiness runs every component check and derives the workspace state.
// It never reports Ready while a required component is failing or unchecked.
func (s *Service) evaluateReadiness(settings JanitorSettings) Readiness {
	now := s.clock()
	if !settings.IsSetUp() {
		return SetupRequiredReadiness(settings.WorkspaceID, now)
	}

	checks := []ComponentCheck{
		s.checkDirectoryAccess(settings),
		s.checkDestination(settings),
		s.checkBinding(settings),
		s.checkPersistence(settings),
		s.checkWatcher(settings),
		s.checkScheduler(settings),
	}

	return Readiness{
		WorkspaceID: settings.WorkspaceID,
		State:       DeriveReadinessState(settings.IsSetUp(), checks),
		Checks:      checks,
		Paused:      settings.Paused,
		CheckedAt:   now,
	}
}

func (s *Service) checkDirectoryAccess(settings JanitorSettings) ComponentCheck {
	check := ComponentCheck{Component: ComponentDirectoryAccess, Status: ComponentOK}
	info, err := os.Stat(settings.RootPath)
	switch {
	case err == nil && !info.IsDir():
		check.Status = ComponentFailed
		check.Code = CodeNotADirectory
		check.Message = "The folder Ori was tidying is no longer a folder."
		check.Repair = RepairRelinkFolder
	case os.IsNotExist(err):
		check.Status = ComponentFailed
		check.Code = CodeRootMissing
		check.Message = "The folder Ori was tidying is no longer there. Choose it again or pick a different folder."
		check.Repair = RepairRelinkFolder
	case os.IsPermission(err):
		check.Status = ComponentFailed
		check.Code = CodePermissionDenied
		check.Message = permissionGuidance("your configured folder")
		check.Repair = RepairGrantPermission
	case err != nil:
		check.Status = ComponentFailed
		check.Code = CodeInvalidPath
		check.Message = "Ori could not open the folder it was tidying."
		check.Repair = RepairRelinkFolder
	}
	if check.Status != ComponentOK {
		return check
	}
	// Listing is the capability the scanner actually needs, so prove it rather
	// than inferring it from the stat.
	dir, err := os.Open(settings.RootPath) // #nosec G304 -- the stored, previously confirmed root
	if err != nil {
		check.Status = ComponentFailed
		check.Code = CodePermissionDenied
		check.Message = permissionGuidance("your configured folder")
		check.Repair = RepairGrantPermission
		return check
	}
	_ = dir.Close()
	return check
}

func (s *Service) checkDestination(settings JanitorSettings) ComponentCheck {
	check := ComponentCheck{Component: ComponentDestination, Status: ComponentOK}
	path := settings.FilingRootPath()
	info, err := os.Stat(path)
	switch {
	case err == nil && info.IsDir():
		return check
	case err == nil:
		check.Status = ComponentFailed
		check.Code = CodeDestinationBlocked
		check.Message = fmt.Sprintf("A file named %q is in the way of the folder Ori files into.", filepath.Base(path))
		check.Repair = RepairRetry
	case os.IsNotExist(err):
		// Missing is recoverable: the destination is created on setup and again
		// before the first approved move.
		check.Status = ComponentFailed
		check.Code = CodeDestinationBlocked
		check.Message = "The folder Ori files into is missing. It will be recreated on the next approved move."
		check.Repair = RepairRetry
	case os.IsPermission(err):
		check.Status = ComponentFailed
		check.Code = CodePermissionDenied
		check.Message = permissionGuidance("your configured folder")
		check.Repair = RepairGrantPermission
	default:
		check.Status = ComponentFailed
		check.Code = CodeDestinationBlocked
		check.Message = "Ori could not check the folder it files into."
		check.Repair = RepairRetry
	}
	return check
}

// checkBinding verifies the workspace still grants exactly the read-only tools
// over the configured root — and, just as importantly, that nothing has widened
// that grant.
func (s *Service) checkBinding(settings JanitorSettings) ComponentCheck {
	check := ComponentCheck{Component: ComponentMCPBinding, Status: ComponentOK}
	fail := func(code, message string) ComponentCheck {
		return ComponentCheck{Component: ComponentMCPBinding, Status: ComponentFailed, Code: code, Message: message, Repair: RepairRelinkFolder}
	}
	if s.workspaces == nil {
		return fail(CodeBindingFailed, "Ori could not confirm this workspace's folder access.")
	}
	ws, err := s.readWorkspace(settings.WorkspaceID)
	if err != nil || ws == nil {
		return fail(CodeWorkspaceMissing, "Ori could not load this workspace.")
	}
	if settings.DirectoryReferenceID != "" {
		found := false
		for _, ref := range ws.DirectoryReferences {
			if ref.ID == settings.DirectoryReferenceID {
				found = true
				break
			}
		}
		if !found {
			return fail(CodeBindingFailed, "This workspace is no longer linked to the folder Ori was tidying.")
		}
	}
	for _, binding := range ws.MCPBindings {
		if !strings.EqualFold(strings.TrimSpace(binding.Alias), JanitorBindingAlias) {
			continue
		}
		if !binding.Enabled {
			return fail(CodeBindingFailed, "Folder access for this workspace is turned off.")
		}
		if !bindingToolsAreReadOnly(binding) {
			return fail(CodeBindingFailed, "This workspace's folder access no longer matches what Downloads Janitor requires. Relink the folder to repair it.")
		}
		return check
	}
	return fail(CodeBindingFailed, "This workspace has no folder access for Downloads Janitor yet.")
}

// bindingToolsAreReadOnly reports whether a binding exposes exactly the Janitor
// read tools: no mutation tool, no content read, and no legacy all-tools
// binding (a nil allowlist means "every tool", which is never acceptable here).
func bindingToolsAreReadOnly(binding workspace.MCPBinding) bool {
	if binding.AllowedTools == nil {
		return false
	}
	for _, tool := range binding.AllowedTools {
		normalized := strings.ToLower(strings.TrimSpace(tool))
		if slices.Contains(forbiddenAgentTools, normalized) {
			return false
		}
		if !slices.Contains(JanitorReadTools, normalized) {
			return false
		}
	}
	return true
}

// AutomationStatus reports what is actually registered for a workspace. It is
// an interface so readiness can ask the automation layer without the service
// depending on its lifecycle.
type AutomationStatus interface {
	// WatcherRegistered reports whether an enabled folder watcher exists.
	WatcherRegistered(workspaceID string) (bool, error)
	// SchedulerRegistered reports whether the daily catch-up is being serviced.
	SchedulerRegistered(workspaceID string) bool
}

// SetAutomationStatus wires the readiness checks for watcher and scheduler.
func (s *Service) SetAutomationStatus(status AutomationStatus) {
	if s != nil {
		s.automation = status
	}
}

// checkWatcher reports whether folder watching is actually running. A paused
// workspace is not failing — it is doing what the user asked — so it reports
// pending with a message that says so rather than an error.
func (s *Service) checkWatcher(settings JanitorSettings) ComponentCheck {
	check := ComponentCheck{Component: ComponentWatcher}
	if settings.Paused {
		check.Status = ComponentPending
		check.Code = CodePending
		check.Message = "Paused by you. Resume to start watching again."
		return check
	}
	if s.automation == nil {
		check.Status = ComponentPending
		check.Code = CodePending
		check.Message = "Folder watching is not running yet."
		return check
	}
	registered, err := s.automation.WatcherRegistered(settings.WorkspaceID)
	switch {
	case err != nil:
		check.Status = ComponentFailed
		check.Code = CodeBindingFailed
		check.Message = "Ori could not confirm folder watching is set up."
		check.Repair = RepairRetry
	case !registered:
		check.Status = ComponentFailed
		check.Code = CodeBindingFailed
		check.Message = "Folder watching is not set up for this workspace."
		check.Repair = RepairRetry
	default:
		check.Status = ComponentOK
	}
	return check
}

// checkScheduler reports whether the daily catch-up is being serviced.
func (s *Service) checkScheduler(settings JanitorSettings) ComponentCheck {
	check := ComponentCheck{Component: ComponentScheduler}
	if settings.Paused {
		check.Status = ComponentPending
		check.Code = CodePending
		check.Message = "Paused by you. Resume to run the daily catch-up again."
		return check
	}
	if s.automation == nil {
		check.Status = ComponentPending
		check.Code = CodePending
		check.Message = "The daily catch-up scan is not scheduled yet."
		return check
	}
	if !s.automation.SchedulerRegistered(settings.WorkspaceID) {
		check.Status = ComponentFailed
		check.Code = CodeBindingFailed
		check.Message = "The daily catch-up scan is not running."
		check.Repair = RepairRetry
		return check
	}
	check.Status = ComponentOK
	return check
}

func (s *Service) checkPersistence(settings JanitorSettings) ComponentCheck {
	check := ComponentCheck{Component: ComponentPersistence, Status: ComponentOK}
	dir, err := s.store.StateDir(settings.WorkspaceID)
	if err != nil {
		return ComponentCheck{Component: ComponentPersistence, Status: ComponentFailed, Code: CodePersistenceFailed, Message: "Ori could not find where to store this workspace's Downloads Janitor state.", Repair: RepairRetry}
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return ComponentCheck{Component: ComponentPersistence, Status: ComponentFailed, Code: CodePersistenceFailed, Message: "Ori cannot save this workspace's Downloads Janitor state.", Repair: RepairRetry}
	}
	probe := filepath.Join(dir, ".write-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil { // #nosec G304 -- resolved state dir plus a fixed name
		return ComponentCheck{Component: ComponentPersistence, Status: ComponentFailed, Code: CodePersistenceFailed, Message: "Ori cannot save this workspace's Downloads Janitor state.", Repair: RepairRetry}
	}
	_ = os.Remove(probe)
	return check
}
