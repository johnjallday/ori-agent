package resourcecatalog

import "time"

// CategorySummary is one category as the Home shows it: a count of what is
// present and current on this computer, and how fresh it is.
type CategorySummary struct {
	ID          string     `json:"id"`
	Label       string     `json:"label"`
	Present     int        `json:"present"`
	State       string     `json:"state"`
	InspectedAt *time.Time `json:"inspected_at,omitempty"`
	ProfileRow  string     `json:"profile_row,omitempty"`
}

// CatalogSummary is the Studio catalog's projection for its Home. It is
// read from the same published revision the Studio shows and never holds an
// annotation, a source location or an entry.
type CatalogSummary struct {
	Revision    int64             `json:"revision"`
	PublishedAt *time.Time        `json:"published_at,omitempty"`
	Approved    bool              `json:"approved"`
	Provider    ProviderView      `json:"provider"`
	Categories  []CategorySummary `json:"categories"`
}

// HomeSummary projects one Studio's catalog for its Home. Counts include only
// present entries of sources approved and not withdrawn on this computer:
// withdrawn, other-setup and no-longer-found entries never count.
func (s *Service) HomeSummary(owner, studioID string) (CatalogSummary, error) {
	state, err := s.loadState(owner, studioID)
	if err != nil {
		return CatalogSummary{}, err
	}
	summary := CatalogSummary{
		Revision: state.observations.Revision, PublishedAt: state.observations.PublishedAt, Approved: state.hasGrants,
		Provider:   ProviderView{Available: state.status.Available, Reason: state.status.Reason},
		Categories: []CategorySummary{},
	}
	if state.declaration == nil {
		return summary, nil
	}
	for _, category := range state.declaration.Categories {
		view := state.categoryView(category)
		summary.Categories = append(summary.Categories, CategorySummary{
			ID: category.ID, Label: category.Label, Present: view.Counts.Present, State: view.State,
			InspectedAt: view.InspectedAt, ProfileRow: category.ProfileRow,
		})
	}
	return summary, nil
}
