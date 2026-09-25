package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
)

// FolderOfferWorkspaceRequest is the workspace the assistant sets up for a
// "show me a folder" project offer the user confirmed: the folder's name and
// the shape's blueprint (blank when empty). The offer id marks the workspace
// as this offer's, which the folder link afterwards verifies.
type FolderOfferWorkspaceRequest struct {
	Name       string
	TemplateID string
	OfferID    string
}

// CreateFolderOfferWorkspace creates the workspace through the ordinary
// creation pipeline — validation, blueprint scaffolding and roster, provenance,
// the allowlist, the workspace.created event — by posting the same request
// the Create Workspace modal would, in process. It returns the new workspace's
// id. The folder itself is linked by the caller afterwards; no path is here.
func (h *Handler) CreateFolderOfferWorkspace(ctx context.Context, req FolderOfferWorkspaceRequest) (string, error) {
	if h == nil {
		return "", errors.New("workspace creation is unavailable")
	}
	name := strings.TrimSpace(req.Name)
	offerID := strings.TrimSpace(req.OfferID)
	if name == "" || offerID == "" {
		return "", errors.New("a folder offer workspace needs a name and an offer id")
	}
	body, err := json.Marshal(createWorkspaceRequest{
		Name: name, TemplateID: strings.TrimSpace(req.TemplateID),
		EntryPoint: folderDigestEntryPoint, FolderOfferID: offerID,
	})
	if err != nil {
		return "", err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, "/api/workspaces", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	h.HandleWorkspaces(recorder, httpRequest)
	if recorder.Code != http.StatusCreated {
		return "", fmt.Errorf("folder offer workspace creation failed (%d): %s", recorder.Code, createFailureMessage(recorder.Body.Bytes()))
	}
	var response struct {
		Folder struct {
			ID string `json:"id"`
		} `json:"folder"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || strings.TrimSpace(response.Folder.ID) == "" {
		return "", errors.New("folder offer workspace creation returned no workspace id")
	}
	return response.Folder.ID, nil
}

// createFailureMessage pulls the user-facing error out of a refused create,
// for the log line; it is never shown as-is.
func createFailureMessage(body []byte) string {
	var payload struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil && strings.TrimSpace(payload.Error) != "" {
		return strings.TrimSpace(payload.Error)
	}
	return strings.TrimSpace(string(body))
}
