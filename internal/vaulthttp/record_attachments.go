package vaulthttp

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/vault"
)

const (
	maxVaultImportUploadBytes     = 100 << 20
	maxVaultAttachmentUploadBytes = vault.MaxRecordAttachmentBytes + (1 << 20)
)

func (h *Handler) handleRecordAttachments(w http.ResponseWriter, r *http.Request, recordPath string) {
	parts := splitRecordPathParts(recordPath)
	if len(parts) < 2 || parts[1] != "attachments" {
		_ = orihttp.RespondNotFound(w, "vault record attachment endpoint not found")
		return
	}

	recordID := strings.TrimSpace(parts[0])
	if recordID == "" {
		_ = orihttp.RespondBadRequest(w, "record id is required")
		return
	}

	switch len(parts) {
	case 2:
		if !orihttp.RequireMethod(w, r, http.MethodPost) {
			return
		}
		h.handleCreateRecordAttachment(w, r, recordID)
	case 3:
		attachmentID := strings.TrimSpace(parts[2])
		if attachmentID == "" {
			_ = orihttp.RespondBadRequest(w, "attachment id is required")
			return
		}

		switch r.Method {
		case http.MethodGet:
			h.handleDownloadRecordAttachment(w, r, recordID, attachmentID)
		case http.MethodDelete:
			h.handleDeleteRecordAttachment(w, r, recordID, attachmentID)
		default:
			_ = orihttp.RespondMethodNotAllowed(w)
		}
	default:
		_ = orihttp.RespondNotFound(w, "vault record attachment endpoint not found")
	}
}

func splitRecordPathParts(path string) []string {
	trimmed := strings.Trim(strings.TrimSpace(path), "/")
	if trimmed == "" {
		return nil
	}

	rawParts := strings.Split(trimmed, "/")
	parts := make([]string, 0, len(rawParts))
	for _, part := range rawParts {
		part = strings.TrimSpace(part)
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func (h *Handler) handleCreateRecordAttachment(w http.ResponseWriter, r *http.Request, recordID string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxVaultAttachmentUploadBytes)
	if err := r.ParseMultipartForm(maxVaultAttachmentUploadBytes); err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			respondVaultError(w, vault.ErrRecordAttachmentTooLarge)
			return
		}
		_ = orihttp.RespondBadRequest(w, "Failed to parse attachment upload: "+err.Error())
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	file, header, err := r.FormFile("file")
	if err != nil {
		_ = orihttp.RespondBadRequest(w, "Attachment file is required")
		return
	}
	defer func() { _ = file.Close() }()

	filename, ok := orihttp.ValidateUploadFilename(w, header.Filename)
	if !ok {
		return
	}

	content, err := io.ReadAll(io.LimitReader(file, vault.MaxRecordAttachmentBytes+1))
	if err != nil {
		_ = orihttp.RespondBadRequest(w, "Failed to read attachment upload")
		return
	}
	if int64(len(content)) > vault.MaxRecordAttachmentBytes {
		respondVaultError(w, vault.ErrRecordAttachmentTooLarge)
		return
	}

	mimeType := strings.TrimSpace(header.Header.Get("Content-Type"))
	if mimeType == "" && len(content) > 0 {
		mimeType = http.DetectContentType(content)
	}

	attachment, err := h.store.AddRecordAttachment(r.Context(), recordID, vault.RecordAttachment{
		Name:      filename,
		MimeType:  mimeType,
		Kind:      strings.TrimSpace(r.FormValue("kind")),
		SizeBytes: int64(len(content)),
	}, content, accessFromQuery(r))
	if err != nil {
		respondVaultError(w, err)
		return
	}

	decorateRecordAttachment(attachment, recordID, r)
	orihttp.Created(w, map[string]any{
		"success":    true,
		"attachment": attachment,
	})
}

func (h *Handler) handleDownloadRecordAttachment(w http.ResponseWriter, r *http.Request, recordID string, attachmentID string) {
	attachment, content, err := h.store.GetRecordAttachmentContent(r.Context(), recordID, attachmentID, accessFromQuery(r))
	if err != nil {
		respondVaultError(w, err)
		return
	}

	filename := strings.TrimSpace(attachment.Name)
	if filename == "" {
		filename = attachment.ID
	}
	contentType := strings.TrimSpace(attachment.MimeType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(content)))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": filename}))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func (h *Handler) handleDeleteRecordAttachment(w http.ResponseWriter, r *http.Request, recordID string, attachmentID string) {
	if err := h.store.DeleteRecordAttachment(r.Context(), recordID, attachmentID, accessFromQuery(r)); err != nil {
		respondVaultError(w, err)
		return
	}

	orihttp.Success(w, map[string]any{"success": true})
}

func parseImportRequest(w http.ResponseWriter, r *http.Request) (vault.ImportRequest, bool) {
	if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data") {
		return parseImportMultipartRequest(w, r)
	}

	return parseImportJSONRequest(w, r)
}

func parseImportJSONRequest(w http.ResponseWriter, r *http.Request) (vault.ImportRequest, bool) {
	var req struct {
		TargetVaultID  string             `json:"target_vault_id,omitempty"`
		ImportPassword string             `json:"import_password"`
		RestoreGrants  *bool              `json:"restore_grants,omitempty"`
		Bundle         vault.ExportBundle `json:"bundle"`
		CreateVault    *struct {
			Name          string `json:"name,omitempty"`
			Description   string `json:"description,omitempty"`
			VaultPassword string `json:"vault_password"`
		} `json:"create_vault,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return vault.ImportRequest{}, false
	}

	restoreGrants := true
	if req.RestoreGrants != nil {
		restoreGrants = *req.RestoreGrants
	}

	importReq := vault.ImportRequest{
		TargetVaultID: vaultIDFromRequest(r, req.TargetVaultID),
		Password:      req.ImportPassword,
		Bundle:        req.Bundle,
		RestoreGrants: restoreGrants,
	}
	if req.CreateVault != nil {
		importReq.NewVaultName = req.CreateVault.Name
		importReq.NewVaultDescription = req.CreateVault.Description
		importReq.NewVaultPassword = req.CreateVault.VaultPassword
	}

	return importReq, true
}

func parseImportMultipartRequest(w http.ResponseWriter, r *http.Request) (vault.ImportRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxVaultImportUploadBytes)
	if err := r.ParseMultipartForm(maxVaultImportUploadBytes); err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			_ = orihttp.RespondBadRequest(w, fmt.Sprintf("Import bundle exceeds %d MB", maxVaultImportUploadBytes/(1<<20)))
			return vault.ImportRequest{}, false
		}
		_ = orihttp.RespondBadRequest(w, "Failed to parse import upload: "+err.Error())
		return vault.ImportRequest{}, false
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	file, _, err := r.FormFile("file")
	if err != nil {
		_ = orihttp.RespondBadRequest(w, "Import bundle file is required")
		return vault.ImportRequest{}, false
	}
	defer func() { _ = file.Close() }()

	var bundle vault.ExportBundle
	if err := json.NewDecoder(file).Decode(&bundle); err != nil {
		_ = orihttp.RespondBadRequest(w, "The selected import file is not valid JSON")
		return vault.ImportRequest{}, false
	}

	restoreGrants := true
	rawRestoreGrants := strings.TrimSpace(r.FormValue("restore_grants"))
	if rawRestoreGrants != "" {
		parsedRestoreGrants, err := strconv.ParseBool(rawRestoreGrants)
		if err != nil {
			_ = orihttp.RespondBadRequest(w, "restore_grants must be true or false")
			return vault.ImportRequest{}, false
		}
		restoreGrants = parsedRestoreGrants
	}

	return vault.ImportRequest{
		TargetVaultID:       vaultIDFromRequest(r, r.FormValue("target_vault_id")),
		Password:            strings.TrimSpace(r.FormValue("import_password")),
		Bundle:              bundle,
		RestoreGrants:       restoreGrants,
		NewVaultName:        strings.TrimSpace(r.FormValue("create_vault_name")),
		NewVaultDescription: strings.TrimSpace(r.FormValue("create_vault_description")),
		NewVaultPassword:    strings.TrimSpace(r.FormValue("create_vault_password")),
	}, true
}

func decorateRecordAttachmentURLs(record *vault.Record, r *http.Request) {
	if record == nil || len(record.Payload) == 0 {
		return
	}

	var payload map[string]any
	if err := json.Unmarshal(record.Payload, &payload); err != nil || payload == nil {
		return
	}

	rawAttachments, ok := payload["attachments"].([]any)
	if !ok || len(rawAttachments) == 0 {
		return
	}

	mutated := false
	for _, rawAttachment := range rawAttachments {
		item, ok := rawAttachment.(map[string]any)
		if !ok {
			continue
		}

		attachmentID := strings.TrimSpace(anyStringValue(item["id"]))
		if attachmentID == "" {
			continue
		}
		if strings.TrimSpace(anyStringValue(item["download_url"])) != "" {
			continue
		}
		if strings.TrimSpace(anyStringValue(item["content_base64"])) != "" || strings.TrimSpace(anyStringValue(item["base64_data"])) != "" {
			continue
		}

		item["download_url"] = recordAttachmentURL(record.ID, attachmentID, r)
		mutated = true
	}

	if !mutated {
		return
	}

	if payloadJSON, err := json.Marshal(payload); err == nil {
		record.Payload = payloadJSON
	}
}

func decorateRecordAttachment(attachment *vault.RecordAttachment, recordID string, r *http.Request) {
	if attachment == nil {
		return
	}

	attachment.DownloadURL = recordAttachmentURL(recordID, attachment.ID, r)
}

func recordAttachmentURL(recordID string, attachmentID string, r *http.Request) string {
	path := "/api/vault/records/" + url.PathEscape(strings.TrimSpace(recordID)) + "/attachments/" + url.PathEscape(strings.TrimSpace(attachmentID))
	if r == nil {
		return path
	}

	values := url.Values{}
	for _, key := range []string{"vault_id", "workspace_id", "actor_type", "actor_id"} {
		value := strings.TrimSpace(r.URL.Query().Get(key))
		if value != "" {
			values.Set(key, value)
		}
	}
	if encoded := values.Encode(); encoded != "" {
		return path + "?" + encoded
	}

	return path
}

func anyStringValue(value any) string {
	if value == nil {
		return ""
	}

	switch typed := value.(type) {
	case string:
		return typed
	default:
		return fmt.Sprintf("%v", value)
	}
}
