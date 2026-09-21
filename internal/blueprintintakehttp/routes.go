package blueprintintakehttp

import "net/http"

func (h *Handler) Register(mux *http.ServeMux) {
	if h == nil || mux == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/blueprint-intakes/{intakeKey}", h.GetIntake)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/blueprint-intakes/{intakeKey}/sources/files", h.UploadFiles)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/blueprint-intakes/{intakeKey}/consent", h.AcceptConsent)
}
