package agentcatalog

import (
	"slices"
	"sort"
)

// ResolveModel picks a concrete model for the given tier from the current
// model-category assignments (model ID -> assigned category IDs, as returned
// by store.ModelCategoryStore.GetAllModelAssignments()). It reuses the
// existing model-category system rather than a hard-coded model list (see
// PRD technical considerations).
//
// Returns ok=false when no model is currently assigned to the tier's
// category — callers should fall back to the caller's own default model and
// surface a non-blocking notice (FR A.6).
func ResolveModel(tier ModelTier, modelAssignments map[string][]string) (model string, ok bool) {
	categoryID := DefaultCategoryID(tier)
	if categoryID == "" {
		return "", false
	}

	var candidates []string
	for modelID, categoryIDs := range modelAssignments {
		if slices.Contains(categoryIDs, categoryID) {
			candidates = append(candidates, modelID)
		}
	}
	if len(candidates) == 0 {
		return "", false
	}

	// Deterministic choice: alphabetically first. The model-category
	// assignments are a flat set with no ranking, so any deterministic tie
	// break is as good as another; alphabetical keeps behavior stable and
	// easy to reason about in tests.
	sort.Strings(candidates)
	return candidates[0], true
}
