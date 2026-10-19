package externalmemoryhttp

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/externalmemory"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// profileGlobalMemoryHref is where a promoted fact is reviewed.
const profileGlobalMemoryHref = "/profile#personalHQKnowledge"

// maxSuggestedBytes keeps a suggested wording under the 500-byte fact limit.
const maxSuggestedBytes = 480

// GlobalMemory promotes a mirror entry into the Personal HQ's reviewed
// memory. The server wires the personal assistant's knowledge service.
type GlobalMemory interface {
	PromoteExternal(ctx context.Context, req personalassistant.ExternalPromoteRequest) (personalassistant.WorkspacePromoteResult, personalassistant.KnowledgeItem, error)
}

// SetGlobalMemory wires Promote to global.
func (h *Handler) SetGlobalMemory(global GlobalMemory) {
	if h != nil {
		h.global = global
	}
}

type entryDTO struct {
	ID               string `json:"id"`
	Source           string `json:"source"`
	SourceLabel      string `json:"source_label"`
	Title            string `json:"title"`
	Excerpt          string `json:"excerpt"`
	Category         string `json:"category"`
	Folder           string `json:"folder"`
	FolderDisplay    string `json:"folder_display"`
	FolderUnverified bool   `json:"folder_unverified,omitempty"`
	Date             string `json:"date,omitempty"`
	Origin           string `json:"origin"`
	OriginDisplay    string `json:"origin_display"`
	Truncated        bool   `json:"truncated,omitempty"`
	PromotedItemID   string `json:"promoted_item_id,omitempty"`
	// SuggestedText and SuggestedCategory prefill Promote to global: one safe
	// line and a Personal HQ category the user can change.
	SuggestedText     string `json:"suggested_text"`
	SuggestedCategory string `json:"suggested_category"`
}

type entriesResponse struct {
	Entries []entryDTO            `json:"entries"`
	Total   int                   `json:"total"`
	Offset  int                   `json:"offset"`
	Limit   int                   `json:"limit"`
	Facets  externalmemory.Facets `json:"facets"`
}

// GetEntries handles GET /api/external-memory/entries?q=&source=&category=&folder=&since=&limit=&offset=.
// It searches the entries a reader may see: active, from enabled sources,
// outside each source's exclusions.
func (h *Handler) GetEntries(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	values := r.URL.Query()
	limit := parseBound(values.Get("limit"), 25, externalmemory.MaxBrowseLimit)
	query := externalmemory.Query{
		Text:     values.Get("q"),
		Source:   strings.TrimSpace(values.Get("source")),
		Category: strings.TrimSpace(values.Get("category")),
		Folder:   strings.TrimSpace(values.Get("folder")),
		Since:    strings.TrimSpace(values.Get("since")),
		Limit:    limit,
		Offset:   parseBound(values.Get("offset"), 0, 1<<20),
	}
	home := h.runner.Home()
	result := externalmemory.Search(externalmemory.VisibleEntries(h.runner.Store(), home), query, home)
	resp := entriesResponse{Entries: []entryDTO{}, Total: result.Total, Offset: query.Offset, Limit: limit, Facets: result.Facets}
	for _, hit := range result.Hits {
		resp.Entries = append(resp.Entries, toEntryDTO(hit.Entry, hit.Excerpt, home))
	}
	if err := orihttp.RespondJSON(w, http.StatusOK, resp); err != nil {
		logger.Debug("Failed to write external memory entries", logger.Fields{"error": err})
	}
}

type promoteRequest struct {
	Text     string `json:"text"`
	Category string `json:"category"`
}

type promoteResponse struct {
	Status  string   `json:"status"`
	Created bool     `json:"created"`
	ItemID  string   `json:"item_id,omitempty"`
	Href    string   `json:"href"`
	Message string   `json:"message"`
	Entry   entryDTO `json:"entry"`
}

// PromoteEntry handles POST /api/external-memory/entries/{id}/promote (FR20).
// The user's confirmed wording becomes an approved Personal HQ fact with
// external provenance, and the entry remembers the item it became. A second
// promote links that item.
func (h *Handler) PromoteEntry(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	entry, ok := h.visibleEntry(id)
	if !ok {
		_ = orihttp.RespondNotFound(w, "No visible external memory entry has that id.")
		return
	}
	var req promoteRequest
	if r.ContentLength != 0 {
		if !orihttp.ParseJSONBody(w, r, &req) {
			return
		}
	}
	suggestedText, suggestedCategory := suggestPromotion(entry)
	text := strings.TrimSpace(req.Text)
	if text == "" {
		text = suggestedText
	}
	category := strings.TrimSpace(req.Category)
	if category == "" {
		category = suggestedCategory
	}
	if h.global == nil {
		writeGlobalMemoryUnavailable(w)
		return
	}
	result, item, err := h.global.PromoteExternal(r.Context(), personalassistant.ExternalPromoteRequest{
		Source: entry.Source, EntryID: entry.ID, Category: category, Text: text, Title: entry.Title,
		PromotedItemID: entry.PromotedItemID,
	})
	switch {
	case errors.Is(err, personalassistant.ErrGlobalMemoryUnavailable):
		writeGlobalMemoryUnavailable(w)
		return
	case errors.Is(err, personalassistant.ErrValidation):
		_ = orihttp.RespondBadRequest(w, "Use one safe line of at most 500 characters and one of the listed categories. Keep secrets in the Vault.")
		return
	case errors.Is(err, personalassistant.ErrConflict), errors.Is(err, personalassistant.ErrKnowledgeLimit):
		_ = orihttp.RespondAPIError(w, http.StatusConflict, orihttp.NewAPIError("global_memory_changed",
			"Global memory changed or is full. Review it on your Profile page, then try again."))
		return
	case err != nil:
		logger.Debug("Promote external memory entry failed", logger.Fields{"source": entry.Source, "error": err})
		_ = orihttp.RespondAPIError(w, http.StatusServiceUnavailable,
			orihttp.NewAPIError("unavailable", "Global memory is temporarily unavailable. Try again."))
		return
	}
	if item.ID != "" && item.ID != entry.PromotedItemID {
		updated, found, saveErr := h.runner.Store().UpdateEntry(entry.Source, entry.ID, func(e *externalmemory.Entry) {
			e.PromotedItemID = item.ID
		})
		if saveErr != nil {
			logger.Warn("Could not record the promoted item on the external entry", logger.Fields{"source": entry.Source, "error": saveErr})
		} else if found {
			entry = updated
		}
	}
	resp := promoteResponse{
		Status: string(result), ItemID: item.ID, Href: profileGlobalMemoryHref,
		Entry: toEntryDTO(entry, "", h.runner.Home()),
	}
	switch result {
	case personalassistant.WorkspacePromoted:
		resp.Created = true
		resp.Message = "Promoted to global memory. Your assistant now knows it as your own fact."
	case personalassistant.WorkspacePromoteAlreadyPending:
		resp.Message = "This fact is already waiting for your review on your Profile page."
	default:
		resp.Message = "Already in global memory."
	}
	if err := orihttp.RespondJSON(w, http.StatusOK, resp); err != nil {
		logger.Debug("Failed to write promote response", logger.Fields{"error": err})
	}
}

func (h *Handler) visibleEntry(id string) (externalmemory.Entry, bool) {
	if id == "" {
		return externalmemory.Entry{}, false
	}
	for _, entry := range externalmemory.VisibleEntries(h.runner.Store(), h.runner.Home()) {
		if entry.ID == id {
			return entry, true
		}
	}
	return externalmemory.Entry{}, false
}

func writeGlobalMemoryUnavailable(w http.ResponseWriter) {
	_ = orihttp.RespondAPIError(w, http.StatusConflict, orihttp.NewAPIError("global_memory_unavailable",
		personalassistant.GlobalMemoryUnavailableMessage))
}

func toEntryDTO(entry externalmemory.Entry, excerpt, home string) entryDTO {
	text, category := suggestPromotion(entry)
	origin, _, _ := strings.Cut(entry.Origin, "#")
	return entryDTO{
		ID: entry.ID, Source: entry.Source, SourceLabel: externalmemory.SourceLabel(entry.Source),
		Title: entry.Title, Excerpt: excerpt, Category: entry.Category,
		Folder: entry.Folder, FolderDisplay: externalmemory.ShortFolder(entry.Folder, home),
		FolderUnverified: entry.FolderUnverified, Date: entry.Date,
		Origin: entry.Origin, OriginDisplay: externalmemory.ShortFolder(origin, home),
		Truncated: entry.Truncated, PromotedItemID: entry.PromotedItemID,
		SuggestedText: text, SuggestedCategory: category,
	}
}

// suggestPromotion proposes the wording and Personal HQ category for Promote
// to global: the entry's first line of prose, cut to fit one fact, and "how
// you work" for profile and preference entries, "projects" otherwise.
func suggestPromotion(entry externalmemory.Entry) (string, string) {
	text := ""
	for line := range strings.SplitSeq(entry.Body, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-*+#> "))
		if line != "" && !strings.HasPrefix(line, "```") {
			text = line
			break
		}
	}
	if text == "" {
		text = entry.Title
	}
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > maxSuggestedBytes {
		cut := maxSuggestedBytes
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		if space := strings.LastIndexByte(text[:cut], ' '); space > maxSuggestedBytes/2 {
			cut = space
		}
		text = strings.TrimSpace(text[:cut]) + "…"
	}
	return text, suggestedCategory(entry)
}

func suggestedCategory(entry externalmemory.Entry) string {
	category := entry.Category
	switch entry.Source {
	case externalmemory.SourceClaudeCode:
		if category == "user" || category == "feedback" {
			return "how_you_work"
		}
	case externalmemory.SourceGeminiCLI:
		return "how_you_work"
	}
	for _, word := range []string{"profile", "preference", "instruction", "identity", "tip", "failure", "feedback"} {
		if strings.Contains(category, word) {
			return "how_you_work"
		}
	}
	return "projects"
}

func parseBound(raw string, fallback, maximum int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return fallback
	}
	return min(n, maximum)
}
