package server

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func (o buildFolderObservations) ReviewHomeStaffing(ctx context.Context, draft foldercontext.Target, sessionID, observationID, candidateID, workspaceName string, input personalassistant.FolderHomeStaffingInput) (personalassistant.FolderOfferView, error) {
	service := o.service()
	if service == nil {
		return personalassistant.FolderOfferView{}, personalassistant.ErrFolderOutcomeUnavailable
	}
	return service.ReviewBuildHomeStaffing(ctx, draft, sessionID, observationID, candidateID, workspaceName, input)
}
