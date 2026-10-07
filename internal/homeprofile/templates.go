package homeprofile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	actionTemplatesCommit = "templates_commit"
	actionTemplatesForget = "templates_forget"
	actionSetup           = "setup"
)

// Templates row states. The card words each one; the service decides it.
const (
	// TemplatesDetectFirst: nothing was looked for yet, so there is no
	// application to read templates for.
	TemplatesDetectFirst = "detect_first"
	// TemplatesOtherApp: templates can be listed for one application only, and
	// it is not this Home's (not found, hidden, or not the main one).
	TemplatesOtherApp = "other_app"
	// TemplatesPluginMissing and TemplatesUpdatePlugin: the application's
	// project plugin is not installed, or is too old to list templates.
	TemplatesPluginMissing = "plugin_missing"
	TemplatesUpdatePlugin  = "update_plugin"
	// TemplatesNotRead: readable, and the owner has not agreed (or took it back).
	TemplatesNotRead = "not_read"
	// TemplatesListed and TemplatesEmpty: read under an active consent.
	TemplatesListed = "listed"
	TemplatesEmpty  = "empty"
	// TemplatesProblem: the last read listed nothing; Problem says why.
	TemplatesProblem = "problem"
	// TemplatesUnsupported: this Home's package names no application whose
	// templates can be listed.
	TemplatesUnsupported = "unsupported"
)

// TemplatesState is the templates row as the card needs it: what can happen
// next, for which application, and its folder names for the review dialog.
type TemplatesState struct {
	State   string   `json:"state"`
	AppID   string   `json:"app_id,omitempty"`
	AppName string   `json:"app_name,omitempty"`
	Folders []string `json:"folders,omitempty"`
	Problem string   `json:"problem,omitempty"`
	// Consented says the record holds an agreement that was not taken back,
	// whatever the state is. The card then shows what is stored and offers
	// Forget, even when nothing can be read right now.
	Consented bool `json:"consented,omitempty"`
}

// TemplatesReview is what the consent dialog shows before anything is read.
type TemplatesReview struct {
	ReviewID string   `json:"review_id"`
	AppID    string   `json:"app_id"`
	AppName  string   `json:"app_name"`
	Folders  []string `json:"folders"`
	Sentence string   `json:"sentence"`
}

// templatesState decides the row from the saved record and the installed
// plugins. It runs nothing.
func (s *Service) templatesState(home *workspace.Workspace, profile *workspace.HomeProfile) TemplatesState {
	consented := profile != nil && profile.Templates != nil && profile.Templates.Consent.Active()
	app, ok := s.templatesApp(home)
	if !ok {
		return TemplatesState{State: TemplatesUnsupported, Consented: consented}
	}
	state := TemplatesState{AppID: app.AppID, AppName: app.AppName, Folders: append([]string(nil), app.Folders...), Consented: consented}
	if profile == nil || profile.DetectedAt == nil {
		state.State = TemplatesDetectFirst
		return state
	}
	row, listed := profile.App(app.AppID)
	if !listed || row.Hidden || (profile.MainApp != nil && profile.MainApp.ID != app.AppID) {
		state.State = TemplatesOtherApp
		return state
	}
	switch {
	case !app.PluginInstalled:
		state.State = TemplatesPluginMissing
		return state
	case !app.OperationAvailable:
		state.State = TemplatesUpdatePlugin
		return state
	}
	templates := profile.Templates
	switch {
	case templates == nil || !templates.Consent.Active():
		state.State = TemplatesNotRead
	case templates.Problem != "":
		state.State, state.Problem = TemplatesProblem, templates.Problem
	case templates.ReadAt != nil && len(templates.Items) > 0:
		state.State = TemplatesListed
	case templates.ReadAt != nil:
		state.State = TemplatesEmpty
	default:
		state.State = TemplatesNotRead
	}
	return state
}

// templatesReadable returns the application a templates read would be for,
// or why there cannot be one right now.
func (s *Service) templatesReadable(home *workspace.Workspace, declared Declared, profile *workspace.HomeProfile) (TemplatesApp, error) {
	if !declared.Profile.Declares(projecttemplates.HomeProfileKindTemplates) {
		return TemplatesApp{}, ErrNotDeclared
	}
	if !s.writable(home) {
		return TemplatesApp{}, ErrReadOnly
	}
	app, _ := s.templatesApp(home)
	switch s.templatesState(home, profile).State {
	case TemplatesUnsupported, TemplatesPluginMissing, TemplatesUpdatePlugin:
		return TemplatesApp{}, ErrOperationUnavailable
	case TemplatesDetectFirst, TemplatesOtherApp:
		return TemplatesApp{}, fmt.Errorf("%w: templates cannot be read for this Home's applications", ErrInvalid)
	}
	return app, nil
}

// templatesReviewID binds a review to exactly what the dialog showed: this
// Home, this application, these folders and the consent as it stood. Anything
// else changing does not make a review stale.
func templatesReviewID(homeID string, app TemplatesApp, profile *workspace.HomeProfile) string {
	consent := "none"
	if profile != nil && profile.Templates != nil && profile.Templates.Consent != nil {
		record := profile.Templates.Consent
		consent = "granted:" + record.GrantedAt.UTC().Format(time.RFC3339Nano)
		if record.RevokedAt != nil {
			consent = "revoked:" + record.RevokedAt.UTC().Format(time.RFC3339Nano)
		}
	}
	sum := sha256.Sum256([]byte(strings.Join(append([]string{"home-profile-templates", homeID, app.AppID, consent}, app.Folders...), "\x00")))
	return hex.EncodeToString(sum[:16])
}

func templatesSentence(app TemplatesApp) string {
	return fmt.Sprintf("Ori will list the names of the files in %s inside your %s settings folder. It opens no template and changes nothing.",
		joinNames(app.Folders), app.AppName)
}

func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return "its templates folders"
	case 1:
		return names[0]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}

// TemplatesReview returns what the owner is about to agree to. It reads
// nothing and writes nothing.
func (s *Service) TemplatesReview(ownerID, homeID, requestID string) (TemplatesReview, error) {
	if !validRequestID(requestID) {
		return TemplatesReview{}, fmt.Errorf("%w: request_id", ErrInvalid)
	}
	home, err := s.lookup(ownerID, homeID)
	if err != nil {
		return TemplatesReview{}, err
	}
	declared, ok := s.declared(home)
	if !ok {
		return TemplatesReview{}, ErrNotDeclared
	}
	profile := home.GetAssistantProgramState().GetHomeProfile()
	app, err := s.templatesReadable(home, declared, profile)
	if err != nil {
		return TemplatesReview{}, err
	}
	return TemplatesReview{ReviewID: templatesReviewID(home.ID, app, profile), AppID: app.AppID, AppName: app.AppName,
		Folders: append([]string(nil), app.Folders...), Sentence: templatesSentence(app)}, nil
}

// TemplatesCommit records the owner's consent and lists the templates once.
// The review must still describe the Home. A plugin without the operation is
// ErrOperationUnavailable and nothing is recorded; a read that fails after the
// owner agreed keeps the consent and records what happened, so the row can
// say so instead of pretending nothing was asked.
func (s *Service) TemplatesCommit(ctx context.Context, ownerID, homeID, requestID, reviewID string) (View, error) {
	if !validRequestID(requestID) {
		return View{}, fmt.Errorf("%w: request_id", ErrInvalid)
	}
	home, err := s.lookup(ownerID, homeID)
	if err != nil {
		return View{}, err
	}
	declared, ok := s.declared(home)
	if !ok {
		return View{}, ErrNotDeclared
	}
	profile := home.GetAssistantProgramState().GetHomeProfile()
	// A repeated request_id replays before anything is read again.
	if receipt, handled := profile.Request(requestID); handled {
		if receipt.Action != actionTemplatesCommit {
			return View{}, fmt.Errorf("%w: request_id was used for another action", ErrInvalid)
		}
		view, readErr := s.Read(ownerID, homeID)
		view.Replayed = readErr == nil
		return view, readErr
	}
	app, err := s.templatesReadable(home, declared, profile)
	if err != nil {
		return View{}, err
	}
	if strings.TrimSpace(reviewID) == "" || reviewID != templatesReviewID(home.ID, app, profile) {
		return View{}, fmt.Errorf("%w: the templates review no longer describes this Home", ErrChanged)
	}
	facts, readErr := s.readFacts(ctx, home, app, true)
	if errors.Is(readErr, ErrOperationUnavailable) {
		return View{}, ErrOperationUnavailable
	}
	return s.write(ownerID, homeID, requestID, actionTemplatesCommit, nil,
		func(current *workspace.Workspace, profile *workspace.HomeProfile, now time.Time) error {
			// The read took a while, and the owner agreed to what the review
			// showed. The record being written must still be the one it
			// described: a Forget (or another consent) that landed meanwhile
			// wins, and so does an application that is no longer this Home's.
			if reviewID != templatesReviewID(current.ID, app, profile) {
				return fmt.Errorf("%w: the templates consent changed while the folders were read", ErrChanged)
			}
			if row, listed := profile.App(app.AppID); !listed || row.Hidden ||
				(profile.MainApp != nil && profile.MainApp.ID != app.AppID) {
				return ErrChanged
			}
			grantTemplatesConsent(profile, workspace.HomeProfileTemplatesHomeReview, "", now)
			applyTemplatesRead(profile, app, facts, readErr, now)
			return nil
		})
}

// TemplatesForget clears the list and withdraws the consent in one write.
func (s *Service) TemplatesForget(ownerID, homeID, requestID string) (View, error) {
	return s.write(ownerID, homeID, requestID, actionTemplatesForget, nil,
		func(_ *workspace.Workspace, profile *workspace.HomeProfile, now time.Time) error {
			if !forgetTemplates(profile, now) {
				return errUnchanged
			}
			return nil
		})
}

// grantTemplatesConsent records a consent, or keeps the one already active so
// reading again is not a second agreement.
func grantTemplatesConsent(profile *workspace.HomeProfile, source, offerID string, now time.Time) {
	if profile.Templates == nil {
		profile.Templates = &workspace.HomeProfileTemplates{}
	}
	if profile.Templates.Consent.Active() {
		return
	}
	profile.Templates.Consent = &workspace.HomeProfileTemplatesConsent{GrantedAt: now, Source: source, OfferID: offerID}
}

// applyTemplatesRead stores one read under the consent already on the record:
// the list, or what went wrong. A list never survives a failed read.
func applyTemplatesRead(profile *workspace.HomeProfile, app TemplatesApp, facts Facts, readErr error, now time.Time) {
	templates := profile.Templates
	if templates == nil || !templates.Consent.Active() {
		return
	}
	at := now
	templates.AppID = app.AppID
	templates.Items, templates.ReadAt, templates.Truncated = nil, nil, false
	templates.Problem, templates.ProblemAt, templates.EmptyReason = "", nil, ""
	switch {
	case errors.Is(readErr, ErrOperationUnavailable):
		templates.Problem, templates.ProblemAt = workspace.HomeProfileTemplatesUnavailable, &at
	case readErr != nil:
		templates.Problem, templates.ProblemAt = workspace.HomeProfileTemplatesFailed, &at
	default:
		templates.Items, templates.ReadAt, templates.Truncated = facts.Templates, &at, facts.Truncated
		if facts.Installed {
			applyVersion(profile, app.AppID, facts.Version)
		}
		// Nothing listed is said plainly when the plugin told why: it did not
		// find the application where it looks, or the application has no
		// templates folders at all. Empty folders need no reason.
		if len(templates.Items) == 0 && !templates.Truncated {
			switch {
			case !facts.Installed:
				templates.EmptyReason = workspace.HomeProfileTemplatesAppNotFound
			case !facts.TemplatesAvailable:
				templates.EmptyReason = workspace.HomeProfileTemplatesNoFolders
			}
		}
	}
}
