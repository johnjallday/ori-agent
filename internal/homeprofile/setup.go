package homeprofile

import (
	"context"
	"fmt"
	"time"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// SetupInput is the profile step of a setup card the owner pressed.
type SetupInput struct {
	// RequestID identifies the card's run, so a resumed run replays instead of
	// looking and reading again.
	RequestID string
	// OfferID is the card the consent came from.
	OfferID string
	// GrantTemplates is true only when that card created this Home and its
	// profile line told the owner one application's templates folder is read.
	// Pressing Set up was the consent, exactly as it is for song details.
	GrantTemplates bool
}

// Setup fills a Home's profile from a setup card the owner pressed: it looks
// for installed applications, proposes the main one, and, when the card gave
// that consent, lists one application's templates once.
//
// A failed or missing facts operation is not an error here: the profile keeps
// the applications and the main one, and the templates row records what
// happened. A Home that already has a templates consent record, given or
// taken back, keeps it untouched: a card never turns a consent back on.
func (s *Service) Setup(ctx context.Context, ownerID, homeID string, input SetupInput) (View, error) {
	if !validRequestID(input.RequestID) {
		return View{}, fmt.Errorf("%w: request_id", ErrInvalid)
	}
	home, err := s.lookup(ownerID, homeID)
	if err != nil {
		return View{}, err
	}
	stored := home.GetAssistantProgramState().GetHomeProfile()
	_, handled := stored.Request(input.RequestID)
	declared, isDeclared := s.declared(home)
	if handled || !isDeclared || !s.writable(home) {
		return s.write(ownerID, homeID, input.RequestID, actionSetup, nil, func(*workspace.Workspace, *workspace.HomeProfile, time.Time) error {
			return errUnchanged
		})
	}
	found := s.installedApps()
	ids := foundIDs(found)
	formats := s.libraryFormats(home)

	app, appOK := s.templatesApp(home)
	hadConsent := stored != nil && stored.Templates != nil && stored.Templates.Consent != nil
	grant := input.GrantTemplates && !hadConsent && appOK && ids[app.AppID] &&
		declared.Profile.Declares(projecttemplates.HomeProfileKindTemplates)

	var facts Facts
	var readErr error
	versionApp, version := "", ""
	if grant {
		facts, readErr = s.readFacts(ctx, home, app, true)
	} else {
		versionApp, version = s.readVersion(ctx, home, ids)
	}
	return s.write(ownerID, homeID, input.RequestID, actionSetup, nil,
		func(_ *workspace.Workspace, profile *workspace.HomeProfile, now time.Time) error {
			applyDetection(profile, found, now)
			applyMainAppRule(profile, formats)
			applyVersion(profile, versionApp, version)
			// Checked again on the record being written: a consent recorded
			// since the read above is left alone.
			if grant && (profile.Templates == nil || profile.Templates.Consent == nil) {
				grantTemplatesConsent(profile, workspace.HomeProfileTemplatesFolderOffer, input.OfferID, now)
				applyTemplatesRead(profile, app, facts, readErr, now)
			}
			return nil
		})
}
