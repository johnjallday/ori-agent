package personalassistanthttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// ChooseFolderSetupEntry edits only a pending review's file choice. It is not
// Set up or Open, and cannot accept a path, run state or extra authority.
func (h *Handler) ChooseFolderSetupEntry(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.folderDigest == nil {
		orihttp.ServiceUnavailable(w, "Folder review is unavailable")
		return
	}
	chooser, ok := h.folderDigest.(interface {
		ChooseSetupEntry(context.Context, string, string, personalassistant.FolderSetupEntryInput) (personalassistant.FolderOfferView, error)
	})
	if !ok {
		orihttp.ServiceUnavailable(w, "File choice is unavailable")
		return
	}
	id := strings.TrimSpace(r.PathValue("offerID"))
	if id == "" {
		orihttp.BadRequest(w, "Offer id is required")
		return
	}
	req, ok := decodeFolderEntryChoice(w, r)
	if !ok {
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	offer, err := chooser.ChooseSetupEntry(r.Context(), userID, id, personalassistant.FolderSetupEntryInput{PlanDigest: req.PlanDigest, EntryName: req.EntryName})
	if errors.Is(err, personalassistant.ErrFolderPlanChanged) {
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{"error": "The file review changed. Review the current plan.", "plan_changed": true, "offer": offer})
		return
	}
	if err != nil {
		writeFolderDigestError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"offer": offer})
}

// Two exact string fields only: no duplicate keys, case aliases, nulls, nested
// authority or trailing JSON. Keep this stricter editor separate from legacy bodies.
func decodeFolderEntryChoice(w http.ResponseWriter, r *http.Request) (personalassistant.FolderSetupEntryInput, bool) {
	bad := func() (personalassistant.FolderSetupEntryInput, bool) {
		orihttp.BadRequest(w, "File choice requires only plan_digest and entry_name strings")
		return personalassistant.FolderSetupEntryInput{}, false
	}
	if r.Body == nil {
		return bad()
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFolderDigestBodyBytes))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return bad()
	}
	values := map[string]string{}
	for decoder.More() {
		key, err := decoder.Token()
		name, ok := key.(string)
		if err != nil || !ok || (name != "plan_digest" && name != "entry_name") {
			return bad()
		}
		if _, repeated := values[name]; repeated {
			return bad()
		}
		token, err := decoder.Token()
		value, ok := token.(string)
		if err != nil || !ok {
			return bad()
		}
		values[name] = value
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') || len(values) != 2 {
		return bad()
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return bad()
	}
	return personalassistant.FolderSetupEntryInput{PlanDigest: values["plan_digest"], EntryName: values["entry_name"]}, true
}
