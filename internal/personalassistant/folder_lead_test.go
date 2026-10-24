package personalassistant

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

// mixedFolder seeds a folder with one Go module beside the given number of loose
// files of six kinds, observes it as a held pick, and returns the candidates.
func mixedFolder(t *testing.T, loose int) (*folderDigestFixture, *FolderObservationService, foldercontext.Target, *foldercontext.Observation, string) {
	t.Helper()
	f, service, source := observationFixture(t)
	f.service.deps.Setup = &fakeFolderSetup{
		lines: []FolderPlanLine{{Kind: FolderPlanWorkspace, Name: "Creates a workspace"}},
		hire:  &FolderHire{Mode: FolderHireCreate, AgentName: "File Curator", RoleLabel: "specialist"}, briefDefault: "Keep it tidy.",
	}
	f.service.deps.Creator = &fakeFolderCreator{}
	service.ConfigureConversationReviews(func(context.Context, FolderOffer) error { return nil }, func(foldercontext.Target) (func(), bool) { return func() {}, true })
	root := filepath.Join(f.home, "Documents", "Work")
	kinds := []string{"pdf", "png", "zip", "dmg", "csv", "mov"}
	for i := 0; i < loose; i++ {
		writeFolderFiles(t, root, fmt.Sprintf("file-%02d.%s", i, kinds[i%len(kinds)]))
	}
	writeFolderFiles(t, root, "widget/go.mod", "widget/main.go", "widget/util.go", "widget/store.go", "widget/handler.go", "widget/config.go")
	observation := observeSelected(t, f, service, source, root)
	project := ""
	for _, shown := range observation.Projects {
		if shown.Name == "widget" {
			project = shown.ID
		}
	}
	if project == "" {
		t.Fatalf("the module was not disclosed: %+v", observation.Projects)
	}
	return f, service, source, observation, project
}

func reviewOf(t *testing.T, service *FolderObservationService, source foldercontext.Target, observation *foldercontext.Observation, candidate string) FolderOfferView {
	t.Helper()
	view, err := service.Review(context.Background(), source, "conversation", observation.ID, candidate)
	if err != nil {
		t.Fatalf("review %s: %v", candidate, err)
	}
	return view
}

// FR12, FR13: with a dump beside a Go module the tidy hire leads, says why from
// the evidence, and the project hire is one link away; the link's review leads
// nowhere and names the way back. Neither creates anything.
func TestFolderLead_ADumpBesideAProjectLeadsWithTheTidy(t *testing.T) {
	f, service, source, observation, project := mixedFolder(t, 23)
	ctx := context.Background()
	recognition, err := service.Recognize(ctx, source, observation.ID)
	if err != nil || recognition.Shape != "inbox" || recognition.CandidateID != folderRootCandidateID {
		t.Fatalf("recognition = %+v %v", recognition, err)
	}
	if got := service.HireCandidate(ctx, source, observation.ID); got.CandidateID != folderRootCandidateID {
		t.Fatalf("hire candidate = %+v", got)
	}
	tidy := reviewOf(t, service, source, observation, folderRootCandidateID)
	lead := tidy.Lead
	if lead == nil || !lead.Leading || lead.AlternativeID != project ||
		lead.AlternativeLabel != "hire Development Assistant for widget instead" ||
		!strings.HasPrefix(lead.Reason, "23 loose files of 6 kinds sit beside a Go module changed on ") ||
		!strings.HasSuffix(lead.Reason, ": the loose files are the bigger job.") {
		t.Fatalf("tidy lead = %+v", lead)
	}
	if strings.Contains(lead.Reason+lead.AlternativeLabel, f.home) {
		t.Fatal("the lead names a path")
	}
	// The other hire is the project's own review, which does not lead.
	other := reviewOf(t, service, source, observation, project)
	if other.Lead == nil || other.Lead.Leading || other.Lead.Reason != "" ||
		other.Lead.AlternativeID != folderRootCandidateID || other.Lead.AlternativeLabel != "hire File Curator to tidy the loose files instead" {
		t.Fatalf("project lead = %+v", other.Lead)
	}
	if other.Subject.Shape == "inbox" || tidy.Subject.Shape != "inbox" {
		t.Fatalf("shapes: tidy %q project %q", tidy.Subject.Shape, other.Subject.Shape)
	}
	// Swapping created nothing but the pending review.
	if doc, err := f.store.Read(ctx, "local"); err != nil || len(doc.Decisions) != 0 || doc.Pending() == nil {
		t.Fatalf("a swap decided something: %+v %v", doc.Decisions, err)
	}
}

// With few loose files beside the module the project leads, and swapping the
// evidence swapped the lead: the same folder, a different verdict.
func TestFolderLead_FewLooseFilesBesideAProjectLeadWithTheProject(t *testing.T) {
	_, service, source, observation, project := mixedFolder(t, 5)
	ctx := context.Background()
	recognition, err := service.Recognize(ctx, source, observation.ID)
	if err != nil || recognition.Shape != "code" || recognition.CandidateID != project {
		t.Fatalf("recognition = %+v %v", recognition, err)
	}
	lead := reviewOf(t, service, source, observation, project).Lead
	if lead == nil || !lead.Leading || lead.AlternativeID != folderRootCandidateID ||
		!strings.HasPrefix(lead.Reason, "5 loose files of 5 kinds sit beside a Go module changed on ") ||
		!strings.HasSuffix(lead.Reason, ": the project is the bigger job.") {
		t.Fatalf("project lead = %+v", lead)
	}
	tidy := reviewOf(t, service, source, observation, folderRootCandidateID)
	if tidy.Lead == nil || tidy.Lead.Leading || tidy.Lead.Reason != "" || tidy.Subject.Shape != "inbox" {
		t.Fatalf("the tidy alternative = %+v shape %q", tidy.Lead, tidy.Subject.Shape)
	}
}

// FR14: a collection keeps its group proposal, a repository its own card, and
// several repositories with nothing loose stay a choice of project.
func TestFolderLead_NeverReplacesTheCardsOtherFoldersOwn(t *testing.T) {
	f, service, source := observationFixture(t)
	ctx := context.Background()
	songs := filepath.Join(f.home, "Music", "Songs")
	writeFolderFiles(t, songs, "Night Drive/Night Drive.rpp", "Harbor Lights/Harbor Lights.rpp", "Low Tide/Low Tide.rpp", "loose-1.wav")
	collection := observeSelected(t, f, service, source, songs)
	if got, err := service.Recognize(ctx, source, collection.ID); err != nil || got.Verdict != BuildFolderCollection {
		t.Fatalf("collection = %+v %v", got, err)
	}
	repo := filepath.Join(f.home, "Code", "ori-site")
	writeFolderFiles(t, repo, "go.mod", "main.go", "notes.txt", "a.pdf", "b.png")
	software := observeSelected(t, f, service, source, repo)
	if got, err := service.Recognize(ctx, source, software.ID); err != nil || got.Label != "software project" || got.CandidateID != folderRootCandidateID {
		t.Fatalf("a repository = %+v %v", got, err)
	}
	two := filepath.Join(f.home, "Code", "Clients")
	writeFolderFiles(t, two, "api/go.mod", "api/main.go", "site/package.json", "site/index.js")
	several := observeSelected(t, f, service, source, two)
	if got, err := service.Recognize(ctx, source, several.ID); err != nil || got.Verdict != BuildFolderUnrecognized {
		t.Fatalf("two repositories and nothing loose = %+v %v", got, err)
	}
}

func TestFolderLead_IsBoundedAndPathFree(t *testing.T) {
	good := FolderLead{Leading: true, Reason: "23 loose files sit beside a Go module", AlternativeID: "candidate-3", AlternativeLabel: "hire File Curator to tidy the loose files instead"}
	if err := good.validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(l *FolderLead){
		"a path in the reason":       func(l *FolderLead) { l.Reason = "see /Users/me/Documents" },
		"a reason on a non-leader":   func(l *FolderLead) { l.Leading = false },
		"no alternative":             func(l *FolderLead) { l.AlternativeID = "" },
		"a path-like alternative id": func(l *FolderLead) { l.AlternativeID = "../x" },
		"a line break":               func(l *FolderLead) { l.AlternativeLabel = "a\nb" },
	} {
		lead := good
		change(&lead)
		if lead.validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
