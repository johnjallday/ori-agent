package personalassistant

import (
	"context"
	"sync"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

type folderSetupRecheckKey struct{}

// WithFolderSetupRecheck requests one observational cache invalidation during
// this explicit review. It is not confirmation or permission to retry a run.
func WithFolderSetupRecheck(ctx context.Context) context.Context {
	return context.WithValue(ctx, folderSetupRecheckKey{}, &sync.Once{})
}

func (s *FolderObservationService) recheckSetupFacts(ctx context.Context, target foldercontext.Target, offer FolderOffer) {
	once, _ := ctx.Value(folderSetupRecheckKey{}).(*sync.Once)
	refresher, ok := s.digest.deps.Setup.(interface{ InvalidateSetupReadiness(string) })
	if once == nil || !ok || offer.ConversationReview == nil || offer.ConversationReview.Operation == FolderOperationSupport {
		return
	}
	once.Do(func() {
		doc, err := s.digest.store.Read(ctx, target.UserID)
		if err != nil {
			return
		}
		for _, prior := range doc.Offers {
			if prior.ConversationReview != nil && prior.ConversationReview.Target == target &&
				prior.Subject.Key == offer.Subject.Key && (prior.Status != FolderOfferPending || prior.Setup != nil) {
				return // accepted work keeps its recorded plan and recovery path
			}
		}
		refresher.InvalidateSetupReadiness(target.UserID)
	})
}
