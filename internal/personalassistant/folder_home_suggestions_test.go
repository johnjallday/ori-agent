package personalassistant

import "testing"

// Today's row for a song waiting in its Home leads to the Home: the plan's
// ready Home before anything is pressed, or the Home the run stopped in.
func TestFolderOfferSuggestedIn(t *testing.T) {
	home := &FolderHomeLink{Name: "Music Production Home", Route: "/workspaces/music-home/assistant"}
	cases := []struct {
		name string
		view FolderOfferView
		want bool
	}{
		{"a later song's plan", FolderOfferView{Plan: &FolderSetupPlan{SuggestedIn: home}}, true},
		{"a song stopped at the hand-off", FolderOfferView{Setup: &FolderSetupView{
			Status: FolderSetupStopped, StopReason: FolderStopHandoffReady, SuggestedIn: home}}, true},
		{"a run stopped for a model", FolderOfferView{Setup: &FolderSetupView{
			Status: FolderSetupStopped, StopReason: FolderStopNeedsModel, SuggestedIn: home}}, false},
		{"a run still going", FolderOfferView{Plan: &FolderSetupPlan{SuggestedIn: home},
			Setup: &FolderSetupView{Status: FolderSetupRunning}}, false},
		{"a plan for a Home still to set up", FolderOfferView{Plan: &FolderSetupPlan{}}, false},
		{"a link with no page", FolderOfferView{Plan: &FolderSetupPlan{SuggestedIn: &FolderHomeLink{Name: "Home"}}}, false},
		{"a link off the workspace pages", FolderOfferView{Plan: &FolderSetupPlan{
			SuggestedIn: &FolderHomeLink{Name: "Home", Route: "https://elsewhere.example"}}}, false},
	}
	for _, tc := range cases {
		if got := folderOfferSuggestedIn(tc.view) != nil; got != tc.want {
			t.Errorf("%s: suggested = %v, want %v", tc.name, got, tc.want)
		}
	}
}
