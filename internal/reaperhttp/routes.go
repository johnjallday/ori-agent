package reaperhttp

import "net/http"

func (h *Handler) Register(mux *http.ServeMux) {
	if h == nil || mux == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/reaper/state", h.GetState)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/reaper/actions", h.GetActions)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/reaper/actions/{actionID}/run", h.RunAction)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/reaper/tracks/{index}/rename", h.RenameTrack)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/reaper/tracks/{index}/color", h.ColorTrack)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/reaper/tracks/{index}/mute", h.MuteTrack)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/reaper/tracks/{index}/solo", h.SoloTrack)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/reaper/tracks/{index}/arm", h.ArmTrack)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/reaper/tracks/{index}/move", h.MoveTrack)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/reaper/tracks/undo", h.UndoTrackEdit)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/reaper/scripts", h.ListScripts)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/reaper/scripts", h.CreateScript)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/reaper/scripts/{scriptID}", h.GetScript)
	mux.HandleFunc("PUT /api/workspaces/{workspaceID}/reaper/scripts/{scriptID}", h.UpdateScript)
	mux.HandleFunc("DELETE /api/workspaces/{workspaceID}/reaper/scripts/{scriptID}", h.DeleteScript)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/reaper/script-proposals", h.ListProposals)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/reaper/script-proposals/{proposalID}/run", h.RunProposal)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/reaper/script-proposals/{proposalID}/save", h.SaveProposal)
	mux.HandleFunc("DELETE /api/workspaces/{workspaceID}/reaper/script-proposals/{proposalID}", h.DiscardProposal)
}
