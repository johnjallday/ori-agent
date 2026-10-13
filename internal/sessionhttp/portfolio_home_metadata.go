package sessionhttp

import (
	"context"
	"fmt"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

// PortfolioHomeTemplate reads the same trusted catalog as the explicit group
// reviewer without allocating a token, journey or workspace. A template whose
// source is not ready is reported as ErrPortfolioHomeTemplateNotReady, so a
// caller can tell "set the provider up" apart from "the host cannot do this".
func (h *Handler) PortfolioHomeTemplate(ctx context.Context, providerPluginID string) (projecttemplates.GroupTemplate, error) {
	entry, err := h.portfolioHomeTemplate(ctx, providerPluginID)
	if err != nil {
		return projecttemplates.GroupTemplate{}, err
	}
	if entry.SourceState != projecttemplates.GroupTemplateSourceReady {
		return projecttemplates.GroupTemplate{}, fmt.Errorf("%w: %w (%s)", ErrPortfolioSetupUnavailable, ErrPortfolioHomeTemplateNotReady, entry.SourceState)
	}
	return entry.GroupTemplate, nil
}
