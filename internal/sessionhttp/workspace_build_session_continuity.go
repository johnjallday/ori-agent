package sessionhttp

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// Canonical draft retirement happens first. Cleanup only removes its pending
// source reviews; it never rolls back resources or accepted work.
func (h *Handler) closeBuildFolderReviews(ctx context.Context, sessionID string, retired ...string) {
	if h.workspaceBuild == nil {
		return
	}
	observationID := ""
	if len(retired) != 0 {
		observationID = retired[0]
	}
	if closer, ok := h.workspaceBuild.Folders.(interface {
		CloseBuildFolderReviews(context.Context, string, string, string) error
	}); ok {
		if err := closer.CloseBuildFolderReviews(ctx, h.workspaceBuildUserID(ctx), sessionID, observationID); err != nil {
			logger.Warn("Retired build folder review could not be closed", logger.Fields{"session_id": sessionID, "error": err})
		}
		return
	}
	if observationID != "" {
		return
	} // an older adapter cannot fence a detach
	// Older source adapters still expose only the software-review cleanup.
	if closer, ok := h.workspaceBuild.Folders.(interface {
		CloseBuildSoftwareReviews(context.Context, string, string) error
	}); ok {
		if err := closer.CloseBuildSoftwareReviews(ctx, h.workspaceBuildUserID(ctx), sessionID); err != nil {
			logger.Warn("Retired build software review could not be closed", logger.Fields{"session_id": sessionID, "error": err})
		}
	}
}
