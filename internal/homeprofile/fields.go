package homeprofile

import (
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// DefaultsInput is what a new project should start from. A zero or empty part
// is not set. The whole set is replaced by each save.
type DefaultsInput struct {
	TempoBPM      int    `json:"tempo_bpm,omitempty"`
	TimeSignature string `json:"time_signature,omitempty"`
	SampleRateHz  int    `json:"sample_rate_hz,omitempty"`
	BitDepth      int    `json:"bit_depth,omitempty"`
}

// FieldsInput is one save from the Home card. Every part is optional; what is
// absent is left as it is.
type FieldsInput struct {
	RequestID string
	// IfRevision is the record revision the card was showing (0 before the
	// Home has a record).
	IfRevision int64
	// MainApp names the owner's main application; "" clears it. Nil leaves it.
	MainApp *string
	// Defaults replaces the new-project defaults. Nil leaves them.
	Defaults *DefaultsInput
	// ConfirmApps says a found application is the owner's. HideApps is "Not
	// mine". ShowApps brings a hidden one back as a hint.
	ConfirmApps []string
	HideApps    []string
	ShowApps    []string
}

// SetFields stores the owner's confirmations and edits. Everything it writes
// is the owner's instruction from then on.
func (s *Service) SetFields(ownerID, homeID string, input FieldsInput) (View, error) {
	var formats map[string]int
	var signatures []Option
	if home, err := s.lookup(ownerID, homeID); err == nil {
		// The main-application rule runs again when the list of visible
		// applications changes, so the library is read before the Home write,
		// like Detect.
		if len(input.ConfirmApps)+len(input.HideApps)+len(input.ShowApps) > 0 {
			formats = s.libraryFormats(home)
		}
		if input.Defaults != nil {
			signatures = s.timeSignatures(home)
		}
	}
	revision := input.IfRevision
	return s.write(ownerID, homeID, input.RequestID, actionFields, &revision,
		func(_ *workspace.Workspace, profile *workspace.HomeProfile, now time.Time) error {
			changed, listChanged, err := applyAppEdits(profile, input, now)
			if err != nil {
				return err
			}
			if listChanged {
				applyMainAppRule(profile, formats)
			}
			if input.MainApp != nil {
				mainChanged, err := applyMainApp(profile, *input.MainApp, now)
				if err != nil {
					return err
				}
				changed = changed || mainChanged
			}
			if input.Defaults != nil {
				defaultsChanged, err := applyDefaults(profile, *input.Defaults, signatures, now)
				if err != nil {
					return err
				}
				changed = changed || defaultsChanged
			}
			if !changed {
				return errUnchanged
			}
			return nil
		})
}

// applyAppEdits applies confirm, hide and show. Every id must be a listed
// application and may appear in only one of the three lists. It reports
// whether anything changed, and whether the list of visible applications did
// (which is what makes the main-application rule worth running again).
func applyAppEdits(profile *workspace.HomeProfile, input FieldsInput, now time.Time) (changed, listChanged bool, err error) {
	if len(input.ConfirmApps)+len(input.HideApps)+len(input.ShowApps) > 3*workspace.HomeProfileMaxApps {
		return false, false, fmt.Errorf("%w: too many applications", ErrInvalid)
	}
	index := make(map[string]int, len(profile.Apps))
	for i, app := range profile.Apps {
		index[app.ID] = i
	}
	named := make(map[string]bool)
	claim := func(id string) (int, error) {
		at, listed := index[id]
		if !listed {
			return 0, fmt.Errorf("%w: %q was not detected", ErrInvalid, id)
		}
		if named[id] {
			return 0, fmt.Errorf("%w: %q is named twice", ErrInvalid, id)
		}
		named[id] = true
		return at, nil
	}
	for _, id := range input.ConfirmApps {
		at, claimErr := claim(id)
		if claimErr != nil {
			return false, false, claimErr
		}
		app := &profile.Apps[at]
		if app.Hidden {
			app.Hidden, listChanged = false, true
		}
		if app.ConfirmedAt == nil {
			confirmed := now
			app.ConfirmedAt, changed = &confirmed, true
		}
	}
	for _, id := range input.HideApps {
		at, claimErr := claim(id)
		if claimErr != nil {
			return false, false, claimErr
		}
		app := &profile.Apps[at]
		if app.Hidden {
			continue
		}
		app.Hidden, app.ConfirmedAt, listChanged = true, nil, true
		// "Not mine" also takes back what was said or read about it.
		if profile.MainApp != nil && profile.MainApp.ID == id {
			profile.MainApp = nil
		}
		if profile.Templates != nil && profile.Templates.AppID == id {
			forgetTemplates(profile, now)
		}
	}
	for _, id := range input.ShowApps {
		at, claimErr := claim(id)
		if claimErr != nil {
			return false, false, claimErr
		}
		if app := &profile.Apps[at]; app.Hidden {
			app.Hidden, listChanged = false, true
		}
	}
	return changed || listChanged, listChanged, nil
}

// applyMainApp records the owner's pick. Picking the application Ori already
// proposed confirms the proposal; picking another one replaces it.
func applyMainApp(profile *workspace.HomeProfile, id string, now time.Time) (bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		if profile.MainApp == nil {
			return false, nil
		}
		profile.MainApp = nil
		return true, nil
	}
	app, listed := profile.App(id)
	if !listed || app.Hidden {
		return false, fmt.Errorf("%w: %q was not detected", ErrInvalid, id)
	}
	confirmed := now
	if current := profile.MainApp; current != nil && current.ID == id {
		if current.ConfirmedAt != nil {
			return false, nil
		}
		current.ConfirmedAt = &confirmed
		return true, nil
	}
	profile.MainApp = &workspace.HomeProfileMainApp{ID: id, Source: workspace.HomeProfileSourceOwner, ConfirmedAt: &confirmed}
	return true, nil
}

// applyDefaults replaces the new-project defaults with the owner's values.
func applyDefaults(profile *workspace.HomeProfile, input DefaultsInput, signatures []Option, now time.Time) (bool, error) {
	input.TimeSignature = strings.TrimSpace(input.TimeSignature)
	if input.TempoBPM != 0 && (input.TempoBPM < workspace.HomeProfileMinTempo || input.TempoBPM > workspace.HomeProfileMaxTempo) {
		return false, fmt.Errorf("%w: tempo must be %d to %d BPM", ErrInvalid, workspace.HomeProfileMinTempo, workspace.HomeProfileMaxTempo)
	}
	if input.TimeSignature != "" && !optionAllowed(signatures, input.TimeSignature) {
		return false, fmt.Errorf("%w: time signature is not one of the choices", ErrInvalid)
	}
	if input.SampleRateHz != 0 && !intAllowed(workspace.HomeProfileSampleRates, input.SampleRateHz) {
		return false, fmt.Errorf("%w: sample rate is not one of the choices", ErrInvalid)
	}
	if input.BitDepth != 0 && !intAllowed(workspace.HomeProfileBitDepths, input.BitDepth) {
		return false, fmt.Errorf("%w: bit depth is not one of the choices", ErrInvalid)
	}
	next := &workspace.HomeProfileDefaults{TempoBPM: input.TempoBPM, TimeSignature: input.TimeSignature,
		SampleRateHz: input.SampleRateHz, BitDepth: input.BitDepth, Source: workspace.HomeProfileSourceOwner}
	if next.Empty() {
		if profile.Defaults == nil {
			return false, nil
		}
		profile.Defaults = nil
		return true, nil
	}
	if current := profile.Defaults; current != nil && current.TempoBPM == next.TempoBPM && current.TimeSignature == next.TimeSignature &&
		current.SampleRateHz == next.SampleRateHz && current.BitDepth == next.BitDepth {
		return false, nil
	}
	confirmed := now
	next.ConfirmedAt = &confirmed
	profile.Defaults = next
	return true, nil
}

func optionAllowed(options []Option, value string) bool {
	for _, option := range options {
		if option.Value == value {
			return true
		}
	}
	return false
}

func intAllowed(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// forgetTemplates clears a templates list and withdraws its consent in the
// same write, so nothing read under a consent outlives it.
func forgetTemplates(profile *workspace.HomeProfile, now time.Time) bool {
	templates := profile.Templates
	if templates == nil {
		return false
	}
	changed := len(templates.Items) > 0 || templates.ReadAt != nil || templates.Truncated || templates.Problem != "" ||
		templates.EmptyReason != ""
	templates.Items, templates.ReadAt, templates.Truncated = nil, nil, false
	templates.Problem, templates.ProblemAt, templates.EmptyReason = "", nil, ""
	if consent := templates.Consent; consent != nil && consent.RevokedAt == nil {
		revoked := now
		if revoked.Before(consent.GrantedAt) {
			revoked = consent.GrantedAt
		}
		consent.RevokedAt, changed = &revoked, true
	}
	return changed
}
