package server

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func TestInboxPlan_LinesInTheCardsOrderWithNoModelAndNoDownload(t *testing.T) {
	f := newInboxFixture(t, "create")
	plan := f.plan(t)
	var lines []string
	for _, line := range plan.Lines {
		lines = append(lines, line.Kind+": "+line.Name)
		if strings.Contains(line.Name+line.Detail, f.path) {
			t.Fatalf("a plan line names a path: %+v", line)
		}
	}
	want := []string{
		"agents: Hire File Curator",
		"workspace: A File Janitor workspace named Downloads",
		"folder: Linked where it is",
		"task: A first look: a metadata-only scan that proposes the first filing batch",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("lines =\n%s", strings.Join(lines, "\n"))
	}
	if got := plan.Lines[2].Detail; got != "Ori lists the files directly inside it and reads their names, types, sizes and dates. It creates a Filed folder inside it. Nothing moves until you approve it in the review." {
		t.Errorf("folder disclosure = %q", got)
	}
	intent := plan.Intent
	if intent.Recipe != folderdigest.BuiltinInboxFolder || intent.RecipeVersion != 1 || intent.Placement != "standalone" ||
		intent.Inbox == nil || intent.Model != nil || intent.Software != nil || intent.Integration != "" || intent.Provider != "" {
		t.Fatalf("intent = %+v", intent)
	}
	if intent.Inbox.WorkspaceName != "Downloads" || intent.Inbox.SubjectIdentity != f.identity || intent.Inbox.Agent.Action != "create" {
		t.Fatalf("inbox intent = %+v", intent.Inbox)
	}
	visual := plan.Visual
	if visual == nil || !visual.RequirementsChecked || len(visual.Plugins) != 0 || visual.Builtin != "File Janitor" ||
		visual.Model == nil || visual.Model.State != personalassistant.FolderSetupModelNotNeeded || visual.Placement != "standalone" ||
		visual.Entity == nil || visual.Entity.Name != "Downloads" || visual.Entity.Operation != "create" {
		t.Fatalf("visual = %+v model=%+v", visual, visual.Model)
	}
	if plan.Hire == nil || plan.Hire.Mode != personalassistant.FolderHireCreate || plan.Hire.AgentName != "File Curator" ||
		plan.Hire.RoleLabel != "specialist" || plan.Hire.Model != "gpt-5" {
		t.Fatalf("hire = %+v", plan.Hire)
	}
	if !strings.HasPrefix(plan.BriefDefault, "Prepare reviewable filing batches") {
		t.Fatalf("brief default = %q", plan.BriefDefault)
	}
	if again := f.plan(t); again.Digest != plan.Digest || len(plan.Digest) != 64 {
		t.Fatalf("digest not stable: %q vs %q", plan.Digest, again.Digest)
	}
	// A role with no model of its own is saved with the agent store's default,
	// and the card says so rather than "no model yet".
	f.team.plan.Roles[0].Model, f.team.plan.Roles[0].ModelConfigured = "", false
	if got := f.plan(t).Hire.Model; got != loadDefaultSettings().Model {
		t.Fatalf("default model = %q", got)
	}
}

func TestInboxPlan_ASavedFileCuratorIsAssignedNeverMadeTwice(t *testing.T) {
	f := newInboxFixture(t, "reuse")
	plan := f.plan(t)
	if plan.Lines[0].Name != "Assign File Curator" || plan.Hire.Mode != personalassistant.FolderHireBind || plan.Hire.Model != "gpt-5-mini" {
		t.Fatalf("line %q, hire %+v", plan.Lines[0].Name, plan.Hire)
	}
	if plan.Intent.Inbox.Agent.Action != "reuse" {
		t.Fatalf("agent action = %q", plan.Intent.Inbox.Agent.Action)
	}
	// The roster and the strict plan must agree, or there is no plan.
	f.team.roster.Action = "create"
	if _, err := f.host.Plan(t.Context(), personalassistant.FolderSetupRequest{UserID: "local", Offer: f.offer}); err == nil {
		t.Fatal("a roster that disagrees with the reviewed team must not be planned")
	}
}

func TestInboxPlan_ATakenNameGetsTheNextFreeOne(t *testing.T) {
	f := newInboxFixture(t, "create")
	f.team.taken["Downloads"], f.team.taken["Downloads 2"] = true, true
	plan := f.plan(t)
	if plan.Intent.Inbox.WorkspaceName != "Downloads 3" || plan.Lines[1].Name != "A File Janitor workspace named Downloads 3" {
		t.Fatalf("name = %q, line %q", plan.Intent.Inbox.WorkspaceName, plan.Lines[1].Name)
	}
}

func TestInboxPlan_ChangesWhenTheReviewedTeamChanges(t *testing.T) {
	f := newInboxFixture(t, "create")
	before := f.plan(t)
	f.team.plan.Roles[0].ConfigDigest = "config-2"
	if after := f.plan(t); after.Digest == before.Digest {
		t.Fatal("a different reviewed team must be a different review")
	}
}

func TestInboxPlan_OnlyAnInboxOfferIsPlannedAsOne(t *testing.T) {
	f := newInboxFixture(t, "create")
	for name, change := range map[string]func(o *personalassistant.FolderOffer){
		"not the root":          func(o *personalassistant.FolderOffer) { o.Subject.IsRoot = false },
		"another shape":         func(o *personalassistant.FolderOffer) { o.Subject.Shape = string(folderdigest.ShapeCorpus) },
		"suppressed capability": func(o *personalassistant.FolderOffer) { o.CapabilitySuppressed = true },
	} {
		offer := f.offer
		change(&offer)
		if _, ok := inboxRow(offer); ok {
			t.Errorf("%s: planned as an inbox", name)
		}
	}
}
