package folderlook

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/foldermap"
)

func song(path string) foldermap.Project {
	return foldermap.Project{Path: path, Name: path, Shape: folderdigest.ShapeAudio, Format: "reaper", Marker: "*.rpp", Depth: 1}
}

func TestRequiredCandidates_RootSongNeedsTheIntegration(t *testing.T) {
	root := foldermap.Project{Shape: folderdigest.ShapeAudio, Format: "reaper", Marker: "*.rpp"}
	facts := foldermap.Facts{RootProject: &root, Shapes: []foldermap.ShapeCount{{Shape: folderdigest.ShapeAudio, Format: "reaper", Count: 1}}}

	got := RequiredCandidates(facts, Readiness{})
	if len(got) != 1 || got[0].ID != "integration:ori_reaper" || got[0].Action != ActionInstall || !got[0].Required {
		t.Fatalf("candidates = %+v", got)
	}
	if got[0].Detail != "install, REAPER is not on this Mac" || got[0].Name != "REAPER tools" {
		t.Fatalf("words = %q / %q", got[0].Name, got[0].Detail)
	}

	got = RequiredCandidates(facts, Readiness{InstalledApps: []folderdigest.InstalledApp{{ToolID: "reaper", Name: "REAPER"}}})
	if got[0].Action != ActionConnect || got[0].Source != SourceInstalledApp || got[0].Detail != "connect, REAPER is installed" {
		t.Fatalf("installed app: %+v", got[0])
	}

	got = RequiredCandidates(facts, Readiness{IntegrationReady: func(key string) bool { return key == "ori_reaper" }})
	if got[0].Action != ActionAlreadyHere || got[0].Source != SourceInstalled {
		t.Fatalf("ready integration: %+v", got[0])
	}
}

func TestRequiredCandidates_LogicHasNoReviewedIntegration(t *testing.T) {
	root := foldermap.Project{Shape: folderdigest.ShapeAudio, Format: "logic", Marker: "*.logicx"}
	facts := foldermap.Facts{RootProject: &root, Shapes: []foldermap.ShapeCount{{Shape: folderdigest.ShapeAudio, Format: "logic", Count: 1}}}
	if got := RequiredCandidates(facts, Readiness{}); len(got) != 0 {
		t.Fatalf("nothing is offered as a cure for Logic: %+v", got)
	}
}

func TestRequiredCandidates_PortfolioNeedsTheHomeAndItsTools(t *testing.T) {
	var members []foldermap.Project
	for _, p := range []string{"a", "b", "c", "d", "e"} {
		members = append(members, song(p))
	}
	facts := foldermap.Facts{Members: members,
		Shapes:    []foldermap.ShapeCount{{Shape: folderdigest.ShapeAudio, Format: "reaper", Count: 5}},
		Portfolio: &foldermap.Portfolio{Shape: folderdigest.ShapeAudio, Count: 5, HomeProviderKey: "music_project_management", HomeName: "Music Production Home"}}

	got := RequiredCandidates(facts, Readiness{})
	if len(got) != 2 || got[0].ID != "home:music_project_management" || got[0].Detail != "creates your Music Production Home" ||
		got[1].ID != "integration:ori_reaper" {
		t.Fatalf("candidates = %+v", got)
	}
	got = RequiredCandidates(facts, Readiness{HomeExists: func(string) bool { return true }})
	if got[0].Action != ActionAlreadyHere || got[0].Detail != "adds this folder to your Music Production Home" {
		t.Fatalf("existing Home: %+v", got[0])
	}
}

func TestRequiredCandidates_DumpNeedsFileCurator(t *testing.T) {
	got := RequiredCandidates(foldermap.Facts{Dump: true, RootLooseFiles: 30, RootLooseKinds: 6}, Readiness{})
	if len(got) != 1 || got[0].ID != "file-curator" || !got[0].Required {
		t.Fatalf("candidates = %+v", got)
	}
}

func TestRequiredCandidates_GroupAndFolderStagesNeedNothing(t *testing.T) {
	group := foldermap.Facts{Members: []foldermap.Project{song("a"), song("b")},
		Shapes: []foldermap.ShapeCount{{Shape: folderdigest.ShapeAudio, Format: "reaper", Count: 2}}}
	if got := RequiredCandidates(group, Readiness{}); len(got) != 0 {
		t.Fatalf("group: %+v", got)
	}
	if got := RequiredCandidates(foldermap.Facts{}, Readiness{}); len(got) != 0 {
		t.Fatalf("folder: %+v", got)
	}
}
