package personalassistant

import (
	"encoding/hex"
	"fmt"
)

// Home staffing can only be attached once the actual provider declaration and
// owner-scoped destination are known. A pre-install display guess cannot become
// authority. Existing plans without this field retain their separate contract.
func validateFolderHomeStaffing(intent FolderSetupIntent) error {
	team := intent.HomeStaffing
	if team == nil {
		if intent.RequiresHomeStaffing {
			return fmt.Errorf("%w: choose the Home team", errFolderDigestInvalid)
		}
		return nil
	}
	if err := validateFolderHomeStaffingEnvelope(intent); err != nil {
		return err
	}
	if err := team.Validate(); err != nil {
		return fmt.Errorf("%w: Home staffing selection", errFolderDigestInvalid)
	}
	for _, role := range team.Roles {
		if role.Scope != "home" {
			return fmt.Errorf("%w: Home staffing scope", errFolderDigestInvalid)
		}
	}
	return nil
}

func validateFolderHomeStaffingEnvelope(intent FolderSetupIntent) error {
	destination := intent.Destination
	if !intent.Portfolio || !intent.StaffsHome || intent.Recipe != "" || intent.Placement != "grouped" ||
		destination == nil || destination.Validate() != nil || destination.Kind != "home" ||
		(destination.Status != "new" && destination.Status != "existing") || destination.OwnerUserID == "" ||
		intent.CreatesHome != (destination.Status == "new") ||
		destination.ProviderPlugin == "" || destination.ProviderVersion == "" || destination.ProgramID == "" ||
		intent.ProviderPlugin != destination.ProviderPlugin || intent.ProviderVersion != destination.ProviderVersion ||
		intent.HomeTemplate != "plugin-home:"+destination.ProviderPlugin+":"+destination.ProgramID {
		return fmt.Errorf("%w: Home staffing destination", errFolderDigestInvalid)
	}
	for _, digest := range []string{destination.DeclarationDigest, destination.CompatibilityHash} {
		if _, err := hex.DecodeString(digest); err != nil || len(digest) != 64 {
			return fmt.Errorf("%w: Home staffing declaration", errFolderDigestInvalid)
		}
	}
	return nil
}
