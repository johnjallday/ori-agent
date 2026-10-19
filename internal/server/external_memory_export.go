package server

import (
	"context"
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/externalmemory"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// externalMemoryExport feeds Copy for Claude import from the global profile
// and the approved Personal HQ facts. It reads the builder's services when
// called; without a hired assistant the export holds the profile only.
type externalMemoryExport struct{ b *ServerBuilder }

func (e externalMemoryExport) ClaudeExport(ctx context.Context) (externalmemory.ExportProfile, []externalmemory.ExportFact, error) {
	var profile externalmemory.ExportProfile
	if e.b != nil && e.b.userStore != nil {
		stored, err := e.b.userStore.Get(ctx, userprofile.LocalUserID)
		switch {
		case err == nil && stored != nil:
			profile = exportProfileFrom(stored)
		case err != nil && !errors.Is(err, userprofile.ErrNotFound):
			return externalmemory.ExportProfile{}, nil, err
		}
	}
	var facts []externalmemory.ExportFact
	if e.b != nil && e.b.personalAssistantLearning != nil {
		items, err := e.b.personalAssistantLearning.ReviewItems(ctx, userprofile.LocalUserID)
		if err == nil {
			for _, item := range items {
				if item.State != personalassistant.KnowledgeApproved || item.Text == "" || item.ReviewUnavailable != "" {
					continue
				}
				date := ""
				if !item.UpdatedAt.IsZero() {
					date = item.UpdatedAt.UTC().Format("2006-01-02")
				}
				facts = append(facts, externalmemory.ExportFact{
					Category: item.Category, Text: item.Text, Date: date,
					FromWorkspace: item.Source.WorkspaceID != "",
				})
			}
		}
		// No hired assistant or Personal HQ: there are no approved facts to
		// export, and the profile alone is still worth copying.
	}
	return profile, facts, nil
}

func exportProfileFrom(p *userprofile.UserProfile) externalmemory.ExportProfile {
	profile := externalmemory.ExportProfile{
		Name:            strings.TrimSpace(p.DisplayName),
		Role:            strings.TrimSpace(p.RoleCategory),
		TimeZone:        strings.TrimSpace(p.Timezone),
		Language:        strings.TrimSpace(p.Locale),
		About:           strings.TrimSpace(p.About),
		Specializations: p.Specializations,
		Preferences:     p.Preferences,
	}
	if !p.UpdatedAt.IsZero() {
		profile.Date = p.UpdatedAt.UTC().Format("2006-01-02")
	}
	return profile
}
