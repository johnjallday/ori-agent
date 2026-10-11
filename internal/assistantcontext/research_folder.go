package assistantcontext

// ResearchFolderRef is bounded canonical reference/focus identity only. It
// contains no paths, names, observations, transcript or filesystem grant.
type ResearchFolderRef struct {
	Revision    string   `json:"revision"`
	SelectionID string   `json:"selection_id,omitempty"`
	FocusIDs    []string `json:"focus_ids,omitempty"`
}
