// Package externalmemoryhttp serves the Connected memory API on the Profile
// page over the read-only external memory mirror: the source cards (switches,
// exclusions, sync status), Sync now, the entry browser, and Promote to
// global.
package externalmemoryhttp

import (
	"context"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/externalmemory"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// syncRequestTimeout bounds a Sync now request. The sync itself keeps going
// if the browser goes away; it is not tied to the request's cancellation.
const syncRequestTimeout = 2 * time.Minute

// Exclusion limits keep a PATCH body bounded.
const (
	maxExclusions      = 100
	maxExclusionLength = 1024
)

// Handler serves /api/external-memory/*.
type Handler struct {
	runner *externalmemory.Runner
	global GlobalMemory
}

// NewHandler builds the handler. A nil runner makes every endpoint 503.
func NewHandler(runner *externalmemory.Runner) *Handler {
	return &Handler{runner: runner}
}

type sourceDTO struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Kind      string `json:"kind"`
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	// Location is the folder or file Ori looks for.
	Location         string                       `json:"location"`
	LocationDisplay  string                       `json:"location_display"`
	Exclusions       []string                     `json:"exclusions"`
	EntryCount       int                          `json:"entry_count"`
	FolderCount      int                          `json:"folder_count"`
	Folders          []externalmemory.FolderCount `json:"folders"`
	LastSyncStarted  *time.Time                   `json:"last_sync_started,omitempty"`
	LastSyncFinished *time.Time                   `json:"last_sync_finished,omitempty"`
	LastResult       string                       `json:"last_result,omitempty"`
	LastError        string                       `json:"last_error,omitempty"`
	SkippedFiles     int                          `json:"skipped_files"`
	LastChanges      changesDTO                   `json:"last_changes"`
	StoreFull        bool                         `json:"store_full,omitempty"`
	PasteDate        *time.Time                   `json:"paste_date,omitempty"`
	// NextDue is the next scheduled sync, for an enabled file source.
	NextDue *time.Time `json:"next_due,omitempty"`
}

type changesDTO struct {
	Added   int `json:"added"`
	Updated int `json:"updated"`
	Removed int `json:"removed"`
}

type sourcesResponse struct {
	Sources           []sourceDTO              `json:"sources"`
	Sync              externalmemory.RunStatus `json:"sync"`
	LastCompletedSync *time.Time               `json:"last_completed_sync,omitempty"`
	NextSyncDue       time.Time                `json:"next_sync_due"`
	SyncIntervalHours int                      `json:"sync_interval_hours"`
}

type patchSourceRequest struct {
	Enabled    *bool     `json:"enabled"`
	Exclusions *[]string `json:"exclusions"`
}

// GetSources handles GET /api/external-memory/sources.
func (h *Handler) GetSources(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	h.writeSources(w, http.StatusOK)
}

// PatchSource handles PATCH /api/external-memory/sources/{source}: the enable
// switch and the exclusion list. Turning a source on, or changing what an
// enabled source excludes, queues a sync so the card fills within seconds;
// turning it off hides its entries at once.
func (h *Handler) PatchSource(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	id := strings.TrimSpace(r.PathValue("source"))
	if _, ok := h.runner.Registry().Get(id); !ok {
		_ = orihttp.RespondNotFound(w, "unknown external memory source")
		return
	}
	var req patchSourceRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	var exclusions []string
	if req.Exclusions != nil {
		cleaned, message := validateExclusions(*req.Exclusions, h.runner.Home())
		if message != "" {
			_ = orihttp.RespondBadRequest(w, message)
			return
		}
		exclusions = cleaned
	}

	resync := false
	if _, err := h.runner.Store().UpdateSources(func(doc *externalmemory.SourcesDocument) error {
		state := doc.Source(id)
		if req.Enabled != nil {
			resync = resync || (*req.Enabled && !state.Enabled)
			state.Enabled = *req.Enabled
		}
		if req.Exclusions != nil {
			resync = resync || (state.Enabled && !slices.Equal(state.Exclusions, exclusions))
			state.Exclusions = exclusions
		}
		doc.Sources[id] = state
		return nil
	}); err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to save the source setting: "+err.Error())
		return
	}
	if resync {
		h.runner.RunInBackground(externalmemory.ReasonEnabled)
	}
	h.writeSources(w, http.StatusOK)
}

// Sync handles POST /api/external-memory/sync (Sync now). It runs the sync and
// answers with the updated cards; when a sync is already running it answers
// 202 at once with that sync's status.
func (h *Handler) Sync(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), syncRequestTimeout)
	defer cancel()
	_, started, err := h.runner.TryRun(ctx, externalmemory.ReasonManual)
	if !started {
		h.writeSources(w, http.StatusAccepted)
		return
	}
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Sync failed: "+err.Error())
		return
	}
	h.writeSources(w, http.StatusOK)
}

func (h *Handler) ready(w http.ResponseWriter) bool {
	if h == nil || h.runner == nil {
		_ = orihttp.RespondAPIError(w, http.StatusServiceUnavailable,
			orihttp.NewAPIError("unavailable", "External memory is not available."))
		return false
	}
	return true
}

func (h *Handler) writeSources(w http.ResponseWriter, status int) {
	store, home := h.runner.Store(), h.runner.Home()
	doc := store.LoadSources()
	nextDue := h.runner.NextDue()
	resp := sourcesResponse{
		Sources:           []sourceDTO{},
		Sync:              h.runner.Status(),
		LastCompletedSync: doc.LastCompletedSync,
		NextSyncDue:       nextDue,
		SyncIntervalHours: int(externalmemory.SyncInterval / time.Hour),
	}
	for _, source := range h.runner.Registry().All() {
		id := source.ID()
		state := doc.Source(id)
		live := externalmemory.LiveEntries(store, id, home)
		folders := externalmemory.FolderCounts(live, home)
		dto := sourceDTO{
			ID:               id,
			Label:            externalmemory.SourceLabel(id),
			Kind:             sourceKind(id),
			Enabled:          state.Enabled,
			Available:        source.Available(),
			Location:         source.Location(),
			LocationDisplay:  externalmemory.ShortFolder(source.Location(), home),
			Exclusions:       state.Exclusions,
			EntryCount:       len(live),
			FolderCount:      len(folders),
			Folders:          folders,
			LastSyncStarted:  state.LastSyncStarted,
			LastSyncFinished: state.LastSyncFinished,
			LastResult:       state.LastResult,
			LastError:        state.LastError,
			SkippedFiles:     state.SkippedFiles,
			LastChanges:      changesDTO{Added: state.Counts.Added, Updated: state.Counts.Updated, Removed: state.Counts.Removed},
			StoreFull:        state.StoreFull,
			PasteDate:        state.PasteDate,
		}
		if state.Enabled && dto.Kind == "files" {
			due := nextDue
			dto.NextDue = &due
		}
		resp.Sources = append(resp.Sources, dto)
	}
	if err := orihttp.RespondJSON(w, status, resp); err != nil {
		logger.Debug("Failed to write external memory sources", logger.Fields{"error": err})
	}
}

func sourceKind(id string) string {
	if id == externalmemory.SourceClaudeAccount {
		return "paste"
	}
	return "files"
}

// validateExclusions trims and de-duplicates the list, keeping the user's
// spelling. Each item must be an absolute folder or start with ~/.
func validateExclusions(raw []string, home string) ([]string, string) {
	if len(raw) > maxExclusions {
		return nil, "Too many exclusions (at most 100)."
	}
	out := []string{}
	seen := map[string]bool{}
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if len(item) > maxExclusionLength {
			return nil, "An exclusion is too long."
		}
		expanded := item
		if strings.HasPrefix(item, "~/") || item == "~" {
			if home == "" {
				return nil, "A ~ path cannot be used here; give the full folder path."
			}
			expanded = filepath.Join(home, strings.TrimPrefix(item, "~"))
		}
		if !filepath.IsAbs(expanded) {
			return nil, "Each exclusion must be a full folder path or start with ~/."
		}
		key := filepath.Clean(expanded)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out, ""
}
