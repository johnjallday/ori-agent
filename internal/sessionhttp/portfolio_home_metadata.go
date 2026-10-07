package sessionhttp

import (
	"context"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

// PortfolioHomeTemplate reads the same trusted catalog as the explicit group
// reviewer without allocating a token, journey or workspace.
func (h *Handler) PortfolioHomeTemplate(ctx context.Context, providerPluginID string) (projecttemplates.GroupTemplate, error) {
	entry, err := h.portfolioHomeTemplate(ctx, providerPluginID)
	if err != nil || entry.SourceState != projecttemplates.GroupTemplateSourceReady {
		return projecttemplates.GroupTemplate{}, ErrPortfolioSetupUnavailable
	}
	return entry.GroupTemplate, nil
}
