package homeprofile

import (
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// View is the Home card: the rows the Home's package declares, the stored
// values, and what the owner can do right now.
type View struct {
	// Available is false for a Home whose installed package declares no
	// profile. Nothing else is set then.
	Available bool `json:"available"`
	// ReadOnly means the Home's provider is unavailable for changes: values
	// stay readable and every control is off.
	ReadOnly bool                                `json:"read_only"`
	Title    string                              `json:"title,omitempty"`
	Intro    string                              `json:"intro,omitempty"`
	Fields   []projecttemplates.HomeProfileField `json:"fields,omitempty"`
	// Revision is the stored record's revision, 0 before the Home has one. A
	// save names it back as if_revision.
	Revision int64 `json:"revision"`
	// Profile is the stored record without its request receipts; nil before
	// anything was detected or saved.
	Profile *workspace.HomeProfile `json:"profile"`
	// Choices are the values the defaults row may take.
	Choices *Choices `json:"choices,omitempty"`
	// Replayed is true when this response repeats an earlier request_id.
	Replayed bool `json:"replayed,omitempty"`
}

// Choices lists what each part of the new-project defaults may be set to.
type Choices struct {
	MinTempo       int      `json:"min_tempo"`
	MaxTempo       int      `json:"max_tempo"`
	TimeSignatures []Option `json:"time_signatures"`
	SampleRates    []int    `json:"sample_rates"`
	BitDepths      []int    `json:"bit_depths"`
}

func (s *Service) view(home *workspace.Workspace, state *workspace.AssistantProgramState) View {
	declared, ok := s.declared(home)
	if !ok {
		return View{}
	}
	view := View{
		Available: true, ReadOnly: !s.writable(home),
		Title: declared.Profile.Title, Intro: declared.Profile.Intro,
		Fields: append([]projecttemplates.HomeProfileField(nil), declared.Profile.Fields...),
	}
	if profile := state.GetHomeProfile(); profile != nil {
		view.Revision = profile.Revision
		profile.Requests = nil
		view.Profile = profile
	}
	if declared.Profile.Declares(projecttemplates.HomeProfileKindDefaults) {
		signatures := s.timeSignatures(home)
		if signatures == nil {
			signatures = []Option{}
		}
		view.Choices = &Choices{
			MinTempo: workspace.HomeProfileMinTempo, MaxTempo: workspace.HomeProfileMaxTempo,
			TimeSignatures: signatures,
			SampleRates:    append([]int(nil), workspace.HomeProfileSampleRates...),
			BitDepths:      append([]int(nil), workspace.HomeProfileBitDepths...),
		}
	}
	return view
}
