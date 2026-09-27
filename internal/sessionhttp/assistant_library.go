package sessionhttp

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The Home's pinned provider generation, package fingerprint, declaration and
// packaged role evidence must all match the installed enabled contribution.
// A similarly named or newly reinstalled plugin never inherits old grants.
func (h *Handler) assistantLibraryProviderEvidence(scope projectlibrary.Scope, home *workspace.Workspace) bool {
	if h == nil || h.installedPluginLister == nil || home == nil || home.ID != scope.HomeID ||
		home.OwnerUserID != scope.OwnerUserID {
		return false
	}
	state := home.GetAssistantProgramState()
	if state == nil || !state.PluginAvailable {
		return false
	}
	owner := state.HomeProvider
	if owner == nil && state.GroupTemplate != nil {
		owner = state.GroupTemplate.ProgramHomeOwner
	}
	if owner == nil || owner.PluginID != scope.ProviderID || owner.ProgramID != scope.ProgramID {
		return false
	}
	installed, err := h.installedPluginLister.List()
	if err != nil || !plugin.IndependentHomeProviderEvidenceAvailable(installed, owner) {
		return false
	}
	created := home.CreatedAt
	if state.GroupTemplate != nil && !state.GroupTemplate.CreatedAt.IsZero() {
		created = state.GroupTemplate.CreatedAt
	}
	// Installation generations restart at one after an uninstall. A byte-for-
	// byte reinstall can match the pinned fingerprint and generation, but it
	// must not inherit an older Home's root grants or reviews. The install
	// receipt is host-owned; an older record with no timestamp stays on the
	// existing fingerprint/generation compatibility path.
	for _, candidate := range installed {
		if candidate.Name == owner.PluginID && candidate.Enabled &&
			!candidate.InstalledAt.IsZero() && !created.IsZero() && candidate.InstalledAt.After(created) {
			return false
		}
	}
	return true
}

func (h *Handler) assistantLibraryStore() *projectlibrary.Store {
	return projectlibrary.NewStore(h.workspaceTaskStore).WithProviderEvidence(h.assistantLibraryProviderEvidence)
}

// ConfigureAssistantLibraryRoots binds the host's native chooser and a scoped
// token issuer, never an HTTP-supplied directory. The strict installed-provider
// check lives inside the Store used by both reviews and filesystem operations.
func (h *Handler) ConfigureAssistantLibraryRoots(picker projectlibrary.FolderPicker,
	selections *pathselection.Store, guards filejanitor.RootGuards) {
	if h == nil || h.workspaceTaskStore == nil || picker == nil || selections == nil {
		return
	}
	h.assistantLibraryRoots = projectlibrary.NewRoots(h.assistantLibraryStore(), picker, selections, guards)
}

func (h *Handler) SetAssistantLibraryPortfolioResolver(resolver projectlibrary.PortfolioRootResolver) {
	if h != nil {
		h.assistantLibraryOfferResolver = resolver
	}
}

// assistantLibraryScope is host-derived from the exact authenticated Home.
// A linked project's workspace URL never grants access to the owner's catalog.
func (h *Handler) assistantLibraryScope(w http.ResponseWriter, r *http.Request) (projectlibrary.Scope, *workspace.Workspace, bool) {
	if h == nil || h.workspaceTaskStore == nil || h.currentUserID == nil {
		_ = orihttp.RespondNotFound(w, "Project library is unavailable")
		return projectlibrary.Scope{}, nil, false
	}
	owner, err := h.currentUserID(r.Context())
	if err != nil || strings.TrimSpace(owner) == "" {
		_ = orihttp.RespondNotFound(w, "Project library is unavailable")
		return projectlibrary.Scope{}, nil, false
	}
	id := strings.TrimSpace(r.PathValue("workspaceID"))
	station, err := h.workspaceTaskStore.Get(id)
	if err != nil || station == nil || station.ID != id || station.OwnerUserID != owner {
		_ = orihttp.RespondNotFound(w, "Project library is unavailable")
		return projectlibrary.Scope{}, nil, false
	}
	state := station.GetAssistantProgramState()
	if state == nil || state.Key.Normalize().OwnerUserID != owner {
		_ = orihttp.RespondNotFound(w, "Project library is unavailable")
		return projectlibrary.Scope{}, nil, false
	}
	key := state.Key.Normalize()
	return projectlibrary.Scope{OwnerUserID: owner, HomeID: station.ID,
		ProviderID: key.PluginID, ProgramID: key.ProgramID}, station, true
}

func respondLibraryReadError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, projectlibrary.ErrNotInitialized), errors.Is(err, projectlibrary.ErrConflict):
		_ = orihttp.RespondConflict(w, "Project library changed or is not initialized")
	case errors.Is(err, projectlibrary.ErrLimit):
		_ = orihttp.RespondBadRequest(w, "Project library page limit exceeded")
	default:
		_ = orihttp.RespondNotFound(w, "Project library is unavailable")
	}
}

// The canonical store owns workspace/link records. The folder store only
// resolves the existing disk directory for the shared folder-owner check.
// Never inspect stale workspace/link metadata from the raw folder mirror.
type libraryFolderOwners struct {
	workspace.Store
	folders *workspace.FileStore
}

func (s libraryFolderOwners) GetFolderPath(id string) (string, error) {
	return s.folders.GetFolderPath(id)
}

// The primary is authoritative for identity and links. Canonical template
// provenance and the existing-file locator live in workspace.json; forward
// only the folder-reader interface the shared creator uses for that overlay.
func (s libraryFolderOwners) GetFolderWorkspace(id string) (*workspace.Workspace, error) {
	return s.folders.GetFolderWorkspace(id)
}

// GetAssistantLibraryActivation reports only read-only setup eligibility. It
// neither issues a child selection nor creates a project; creator review and
// commit must repeat all checks against current installed/source evidence.
func (h *Handler) GetAssistantLibraryActivation(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" || h.assistantLibraryRoots == nil || h.installedPluginLister == nil ||
		h.workspaceStore == nil || h.workspaceTaskStore == nil {
		_ = orihttp.RespondNotFound(w, "Library project setup is unavailable")
		return
	}
	var owners projectconnection.FolderOwnerStore = libraryFolderOwners{Store: h.workspaceTaskStore, folders: h.workspaceStore}
	inspector := projectlibrary.NewActivationInspector(h.assistantLibraryStore(), h.assistantLibraryRoots,
		h.installedPluginLister, owners)
	result, err := inspector.Eligibility(r.Context(), scope, r.PathValue("entryID"))
	if err != nil {
		if errors.Is(err, projectlibrary.ErrConflict) {
			_ = orihttp.RespondNotFound(w, "Library project not found")
		} else {
			respondLibraryReadError(w, err)
		}
		return
	}
	_ = orihttp.RespondSuccess(w, result)
}

// ListAssistantLibraryRoots projects at most 20 source grants and one bounded
// scan summary per root. Only the authenticated owner sees exact root paths.
func (h *Handler) ListAssistantLibraryRoots(w http.ResponseWriter, r *http.Request) {
	scope, station, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(r.URL.RawQuery) > 256 {
		_ = orihttp.RespondBadRequest(w, "Invalid root page")
		return
	}
	for name, input := range values {
		if name != "offset" || len(input) != 1 {
			_ = orihttp.RespondBadRequest(w, "Invalid root page")
			return
		}
	}
	offset := 0
	if raw := values.Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 || offset > 64 {
			_ = orihttp.RespondBadRequest(w, "Invalid root page")
			return
		}
	}
	doc, err := h.assistantLibraryStore().Read(scope)
	if err != nil {
		if errors.Is(err, projectlibrary.ErrNotInitialized) {
			_ = orihttp.RespondSuccess(w, map[string]any{"initialized": false,
				"provider_read_only": !h.assistantLibraryProviderEvidence(scope, station),
				"picker_available":   h.assistantLibraryRoots.PickerAvailable()})
			return
		}
		respondLibraryReadError(w, err)
		return
	}
	type rootCard struct {
		ID          string     `json:"id"`
		Path        string     `json:"path"`
		Revision    int64      `json:"revision"`
		RevokedAt   *time.Time `json:"revoked_at,omitempty"`
		NeedsReview bool       `json:"needs_review,omitempty"`
		HasScopes   bool       `json:"has_scopes,omitempty"`
		LastScan    *struct {
			ID            string    `json:"id"`
			Status        string    `json:"status"`
			StartedAt     time.Time `json:"started_at"`
			Scope         string    `json:"scope,omitempty"`
			PartialReason string    `json:"partial_reason,omitempty"`
			Entries       int       `json:"entries_seen"`
			Skipped       int       `json:"skipped_entries"`
		} `json:"last_scan,omitempty"`
	}
	rows := make([]rootCard, 0, 20)
	for i := offset; i < len(doc.Roots) && i < offset+20; i++ {
		root := doc.Roots[i]
		row := rootCard{ID: root.ID, Path: root.Path, Revision: root.Revision, RevokedAt: root.RevokedAt}
		for _, scan := range doc.Scans {
			if scan.RootID == root.ID && scan.RootRevision == root.Revision &&
				(scan.Status == "complete" || scan.Status == "partial") && len(scan.KnownScopes) > 0 {
				row.HasScopes = true
				break
			}
		}
		for _, inactive := range station.GetAssistantProgramState().ProjectLibraryInactiveRoots {
			if inactive == root.ID {
				row.NeedsReview = true
				break
			}
		}
		for j := len(doc.Scans) - 1; j >= 0; j-- {
			if doc.Scans[j].RootID != root.ID {
				continue
			}
			scan := doc.Scans[j]
			row.LastScan = &struct {
				ID            string    `json:"id"`
				Status        string    `json:"status"`
				StartedAt     time.Time `json:"started_at"`
				Scope         string    `json:"scope,omitempty"`
				PartialReason string    `json:"partial_reason,omitempty"`
				Entries       int       `json:"entries_seen"`
				Skipped       int       `json:"skipped_entries"`
			}{scan.ID, scan.Status, scan.StartedAt, scan.Scope, scan.PartialReason, scan.EntriesSeen, scan.SkippedLinks + scan.SkippedOther}
			break
		}
		rows = append(rows, row)
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"initialized": true, "revision": doc.Revision,
		"total_roots": len(doc.Roots), "roots": rows, "formats": folderdigest.ProjectFormatOptions(), "next_offset": func() int {
			if offset+len(rows) < len(doc.Roots) {
				return offset + len(rows)
			}
			return 0
		}(), "provider_read_only": !h.assistantLibraryProviderEvidence(scope, station),
		"picker_available": h.assistantLibraryRoots.PickerAvailable()})
}

// ListAssistantLibraryScanScopes is a read-only, revision-bound page of
// server-known narrower choices, not a filesystem walk or a path selector.
func (h *Handler) ListAssistantLibraryScanScopes(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	if len(r.URL.RawQuery) > 256 || h.assistantLibraryRoots == nil {
		_ = orihttp.RespondBadRequest(w, "Invalid scan scope page")
		return
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(values["if_revision"]) != 1 || values.Get("if_revision") == "" {
		_ = orihttp.RespondBadRequest(w, "Invalid scan scope page")
		return
	}
	for name, input := range values {
		if (name != "if_revision" && name != "offset") || len(input) != 1 {
			_ = orihttp.RespondBadRequest(w, "Invalid scan scope page")
			return
		}
	}
	revision, err := strconv.ParseInt(values.Get("if_revision"), 10, 64)
	if err != nil || revision <= 0 {
		_ = orihttp.RespondBadRequest(w, "Invalid scan scope page")
		return
	}
	offset := 0
	if raw := values.Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 || offset > 512*1024 {
			_ = orihttp.RespondBadRequest(w, "Invalid scan scope page")
			return
		}
	}
	rootID := r.PathValue("rootID")
	if !h.libraryRootExists(w, scope, rootID) {
		return
	}
	page, err := h.assistantLibraryRoots.ListScanScopes(scope, rootID, revision, offset)
	if err != nil {
		respondLibraryReadError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, page)
}

func (h *Handler) SearchAssistantLibrary(w http.ResponseWriter, r *http.Request) {
	scope, station, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	if len(r.URL.RawQuery) > 4096 {
		_ = orihttp.RespondBadRequest(w, "Invalid library search")
		return
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		_ = orihttp.RespondBadRequest(w, "Invalid library search")
		return
	}
	allowed := map[string]bool{"text": true, "stage": true, "status": true, "format": true,
		"connection": true, "availability": true, "root_id": true, "priority": true,
		"sort": true, "direction": true, "page_size": true, "cursor": true}
	for name, values := range values {
		if !allowed[name] || len(values) != 1 {
			_ = orihttp.RespondBadRequest(w, "Invalid library search")
			return
		}
	}
	query := projectlibrary.Search{Text: values.Get("text"), Stage: values.Get("stage"),
		Status: values.Get("status"), Format: values.Get("format"), Connection: values.Get("connection"),
		Availability: values.Get("availability"), RootID: values.Get("root_id"), Sort: values.Get("sort"),
		Direction: values.Get("direction"), Cursor: values.Get("cursor")}
	if raw := values.Get("priority"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			_ = orihttp.RespondBadRequest(w, "Invalid library priority")
			return
		}
		query.Priority = &value
	}
	if raw := values.Get("page_size"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			_ = orihttp.RespondBadRequest(w, "Invalid library page size")
			return
		}
		query.PageSize = value
	}
	page, err := h.assistantLibraryStore().Query(scope, query)
	if err != nil {
		respondLibraryReadError(w, err)
		return
	}
	page.ProviderReadOnly = !h.assistantLibraryProviderEvidence(scope, station)
	_ = orihttp.RespondSuccess(w, page)
}

func (h *Handler) GetAssistantLibraryProject(w http.ResponseWriter, r *http.Request) {
	scope, station, ok := h.assistantLibraryScope(w, r)
	if !ok {
		return
	}
	detail, err := h.assistantLibraryStore().Detail(scope, r.PathValue("entryID"))
	if err != nil {
		if errors.Is(err, projectlibrary.ErrConflict) {
			_ = orihttp.RespondNotFound(w, "Library project not found")
		} else {
			respondLibraryReadError(w, err)
		}
		return
	}
	detail.ProviderReadOnly = !h.assistantLibraryProviderEvidence(scope, station)
	_ = orihttp.RespondSuccess(w, detail)
}
