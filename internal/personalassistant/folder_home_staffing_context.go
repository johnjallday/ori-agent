package personalassistant

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
)

// FolderHomeStaffingContext binds advisory/selected draft state to an existing
// offer and its host-produced plan before role choices are attached. Display
// progress, a recommendation refresh and the selected team itself are not new
// context. This fingerprint is a CAS witness, never execution authority.
func FolderHomeStaffingContext(req FolderSetupRequest, base FolderSetupPlan) string {
	if base.Intent.Destination != nil && !reflect.DeepEqual(base.Intent.Destination, base.Destination) {
		return ""
	}
	base = base.Stamped()
	destination := base.Destination
	if req.UserID == "" || req.Offer.ID == "" || base.Digest == "" || base.Intent.HomeStaffing != nil ||
		!base.Intent.Portfolio || !base.Intent.StaffsHome || destination == nil || destination.Kind != "home" ||
		destination.OwnerUserID != req.UserID || destination.Validate() != nil {
		return ""
	}
	// The same known declaration/owner envelope used by the exact consumer.
	if err := validateFolderHomeStaffingEnvelope(base.Intent); err != nil {
		return ""
	}
	material := struct {
		Owner, OfferID, FolderKey, RootIdentity, SubjectIdentity string
		Subject                                                  FolderCandidateRecord
		Review                                                   *FolderConversationReview
		PlanDigest                                               string
	}{req.UserID, req.Offer.ID, req.Offer.FolderKey, req.Offer.RootIdentity, req.Offer.SubjectIdentity,
		req.Offer.Subject, req.Offer.ConversationReview, base.Digest}
	data, err := json.Marshal(material)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(append([]byte("folder-home-staffing-context:v1\x00"), data...))
	return hex.EncodeToString(digest[:])
}
