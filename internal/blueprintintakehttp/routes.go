package blueprintintakehttp

import "net/http"

func (h *Handler) Register(mux *http.ServeMux) {
	if h == nil || mux == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/blueprint-intakes/pending", h.GetPending)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/blueprint-intakes/{intakeKey}", h.GetIntake)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/blueprint-intakes/{intakeKey}/sources/files", h.UploadFiles)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/blueprint-intakes/{intakeKey}/sources/links", h.AddLink)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/blueprint-intakes/{intakeKey}/sources/folder", h.AddFolder)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/blueprint-intakes/{intakeKey}/consent", h.AcceptConsent)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/blueprint-intakes/{intakeKey}/skill/trust", h.TrustBundledSkill)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/blueprint-intakes/{intakeKey}/run", h.StartRun)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/blueprint-intakes/{intakeKey}/run", h.GetRun)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/blueprint-intakes/{intakeKey}/run/cancel", h.CancelRun)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/blueprint-intakes/{intakeKey}/proposal", h.GetProposal)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/blueprint-intakes/{intakeKey}/proposal/apply", h.ApplyProposal)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/blueprint-intakes/{intakeKey}/proposal/skip", h.SkipProposal)
}
