package folderagent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// setup-card-clarity FR6-FR14: the card names the agents a Home step hires,
// before the Home exists, and the plan digest binds what it named.

const testModel = "claude-sonnet-5-5 (claude)"

// hiresRecord is homeStageRecord with the Home facts the look recorded.
func hiresRecord(facts *folderlook.HomeFacts, model string) Record {
	rec := homeStageRecord(facts)
	rec.HomeHireModel = model
	return rec
}

func newHomeFacts(hires ...folderlook.HomeHire) *folderlook.HomeFacts {
	facts := &folderlook.HomeFacts{ProviderName: "Music Project Management", ProviderVersion: "0.5.0", RolesKnown: true, Hires: hires}
	for _, hire := range hires {
		facts.MissingRoles = append(facts.MissingRoles, hire.Name)
	}
	return facts
}

func TestPlan_HiresBlock(t *testing.T) {
	const home = "music-production-home"
	cases := []struct {
		name      string
		facts     *folderlook.HomeFacts
		model     string
		want      []HireEntry
		wantNote  string
		wantLines []string // hire lines
	}{
		{"one hire, before the Home exists",
			newHomeFacts(folderlook.HomeHire{Name: "Portfolio Manager", Job: "Plans the work."}), testModel,
			[]HireEntry{{Step: home, Name: "Portfolio Manager", Job: "Plans the work.", Model: testModel}}, "",
			[]string{"Hires Portfolio Manager"}},
		{"two hires share the one model",
			newHomeFacts(folderlook.HomeHire{Name: "Portfolio Manager", Job: "Plans."}, folderlook.HomeHire{Name: "Library Manager"}), testModel,
			[]HireEntry{
				{Step: home, Name: "Portfolio Manager", Job: "Plans.", Model: testModel},
				{Step: home, Name: "Library Manager", Model: testModel}}, "",
			[]string{"Hires Portfolio Manager", "Hires Library Manager"}},
		{"no model yet when the system has none",
			newHomeFacts(folderlook.HomeHire{Name: "Portfolio Manager"}), "",
			[]HireEntry{{Step: home, Name: "Portfolio Manager", Model: NoModelYet}}, "",
			[]string{"Hires Portfolio Manager"}},
		{"a name an agent already holds is hired beside it, and the row says so",
			newHomeFacts(folderlook.HomeHire{Name: "Portfolio Manager 2", Taken: "Portfolio Manager"}), testModel,
			[]HireEntry{{Step: home, Name: "Portfolio Manager 2", Model: testModel, Note: "Your existing Portfolio Manager stays as it is."}}, "",
			[]string{"Hires Portfolio Manager 2"}},
		{"every role filled: nothing to hire, no block, no note",
			&folderlook.HomeFacts{ProviderName: "Music Project Management", ProviderReady: true, HomeExists: true, RolesKnown: true}, testModel,
			nil, "", nil},
		{"roles unknown (the release could not be read): the sentence, never an empty block",
			&folderlook.HomeFacts{ProviderName: "Music Project Management", ProviderVersion: "0.5.0"}, testModel,
			nil, HiresUnnamed, nil},
		{"a record without host facts keeps the sentence",
			nil, testModel, nil, HiresUnnamed, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := hiresRecord(tc.facts, tc.model)
			plan := homePlan(t, rec)
			if !plan.Runnable {
				t.Fatalf("the plan does not run: %+v", plan)
			}
			if !reflect.DeepEqual(plan.Hires, tc.want) || plan.HiresNote != tc.wantNote {
				t.Fatalf("hires = %+v note = %q, want %+v note %q", plan.Hires, plan.HiresNote, tc.want, tc.wantNote)
			}
			// The block is never nil in the view and never both rows and a note.
			view := planViewOf(plan)
			if view.Hires == nil || (len(view.Hires) > 0 && view.HiresNote != "") {
				t.Fatalf("view hires = %+v note = %q", view.Hires, view.HiresNote)
			}
			// The folder's own agent is not a hire here: it was hired before the look.
			for _, e := range plan.Hires {
				if e.Name == rec.AgentName {
					t.Fatalf("the folder's own agent is listed as a hire: %+v", e)
				}
			}
			// Each row is a line the run hires under, so the card and the run agree.
			var lines []string
			for _, l := range plan.Lines {
				if l.Kind == FolderPlanHire && l.Name != HomeRolesLine {
					lines = append(lines, l.Name)
				}
			}
			if !reflect.DeepEqual(lines, tc.wantLines) {
				t.Fatalf("hire lines = %q, want %q", lines, tc.wantLines)
			}
		})
	}

	// Without the Home step ticked there is no hires block at all.
	rec := hiresRecord(newHomeFacts(folderlook.HomeHire{Name: "Portfolio Manager"}), testModel)
	group := stepHandleOfKind(t, rec, folderlook.StepGroup)
	if plan := BuildPlan(rec, []string{group}); len(plan.Hires) != 0 || plan.HiresNote != "" {
		t.Fatalf("a group plan hires nobody: %+v %q", plan.Hires, plan.HiresNote)
	}
}

func TestPlan_HiresAreInTheDigest(t *testing.T) {
	base := func() (*folderlook.HomeFacts, string) {
		return newHomeFacts(folderlook.HomeHire{Name: "Portfolio Manager", Job: "Plans the work."}), testModel
	}
	digest := func(mutate func(*folderlook.HomeFacts, *string)) string {
		facts, model := base()
		if mutate != nil {
			mutate(facts, &model)
		}
		return homePlan(t, hiresRecord(facts, model)).Digest
	}
	same := digest(nil)
	if again := digest(nil); again != same {
		t.Fatalf("the digest is not stable: %s %s", again, same)
	}
	changes := map[string]func(*folderlook.HomeFacts, *string){
		// The lines do not change for a job or a model: only the block does.
		"the job line": func(f *folderlook.HomeFacts, _ *string) { f.Hires[0].Job = "Plans something else." },
		"the model":    func(_ *folderlook.HomeFacts, m *string) { *m = "another-model (claude)" },
		"no model yet": func(_ *folderlook.HomeFacts, m *string) { *m = "" },
		"the note": func(f *folderlook.HomeFacts, _ *string) {
			f.Hires[0].Taken = "Portfolio Manager"
		},
		"the name": func(f *folderlook.HomeFacts, _ *string) {
			f.Hires[0].Name = "Portfolio Manager 2"
			f.MissingRoles[0] = "Portfolio Manager 2"
		},
		"another hire": func(f *folderlook.HomeFacts, _ *string) {
			f.Hires = append(f.Hires, folderlook.HomeHire{Name: "Library Manager"})
			f.MissingRoles = append(f.MissingRoles, "Library Manager")
		},
		"the roles becoming unknown (the sentence)": func(f *folderlook.HomeFacts, _ *string) {
			f.RolesKnown, f.MissingRoles, f.Hires = false, nil, nil
		},
	}
	seen := map[string]string{same: "unchanged"}
	for name, mutate := range changes {
		got := digest(mutate)
		if got == same {
			t.Fatalf("changing %s kept the digest", name)
		}
		if other, dup := seen[got]; dup {
			t.Fatalf("changing %s gave the digest of %s", name, other)
		}
		seen[got] = name
	}

	// The sentence itself is bound too: the same unknown roles worded differently differ.
	known := homePlan(t, hiresRecord(&folderlook.HomeFacts{ProviderName: "X", ProviderVersion: "1"}, testModel))
	altered := known
	altered.HiresNote = "something else"
	if planDigest(altered) == known.Digest {
		t.Fatal("the unnamed-hires sentence is not in the digest")
	}
}

func TestAssurance_CountsTheHires(t *testing.T) {
	rec := planRecord() // REAPER tools and the Home, six songs two folders down
	rec.Candidates[0].Action = folderlook.ActionInstall
	rec.Candidates[1].Action = folderlook.ActionInstall
	rec.HomeHireModel = testModel
	assure := func(plan Plan) string {
		return plan.Lines[len(plan.Lines)-1].Name
	}
	const prefix = "Nothing in Albums is moved, copied or read."

	// A Home that does not exist yet, its provider and the tools to install, one hire.
	rec.Candidates[0].Home = newHomeFacts(folderlook.HomeHire{Name: "Portfolio Manager"})
	plan := BuildPlan(rec, []string{"music-production-home", "reaper-tools"})
	if got, want := assure(plan), prefix+" Ori adds 2 plugins, Music Production Home, and 1 agent."; got != want {
		t.Fatalf("assurance = %q, want %q", got, want)
	}

	// Two hires.
	rec.Candidates[0].Home = newHomeFacts(folderlook.HomeHire{Name: "Portfolio Manager"}, folderlook.HomeHire{Name: "Library Manager"})
	plan = BuildPlan(rec, []string{"music-production-home", "reaper-tools"})
	if got, want := assure(plan), prefix+" Ori adds 2 plugins, Music Production Home, and 2 agents."; got != want {
		t.Fatalf("assurance = %q, want %q", got, want)
	}

	// Roles unknown: the generic words stand.
	rec.Candidates[0].Home = &folderlook.HomeFacts{ProviderName: "Music Project Management", ProviderVersion: "0.5.0"}
	plan = BuildPlan(rec, []string{"music-production-home", "reaper-tools"})
	if got, want := assure(plan), prefix+" Ori adds 2 plugins, Music Production Home, and its agents."; got != want {
		t.Fatalf("assurance = %q, want %q", got, want)
	}

	// A Home that exists and lacks a role: it adds that agent, not the Home.
	rec.Candidates[0].Action = folderlook.ActionAlreadyHere
	rec.Candidates[1].Action = folderlook.ActionAlreadyHere
	rec.Candidates[0].Home = &folderlook.HomeFacts{ProviderName: "Music Project Management", ProviderReady: true, HomeExists: true,
		RolesKnown: true, MissingRoles: []string{"Library Manager"}, Hires: []folderlook.HomeHire{{Name: "Library Manager"}}}
	plan = BuildPlan(rec, []string{"music-production-home"})
	if got, want := assure(plan), prefix+" Ori adds 1 agent."; got != want {
		t.Fatalf("assurance = %q, want %q", got, want)
	}

	// Nothing to add.
	rec.Candidates[0].Home = &folderlook.HomeFacts{ProviderReady: true, HomeExists: true, RolesKnown: true}
	plan = BuildPlan(rec, []string{"music-production-home"})
	if got, want := assure(plan), strings.TrimSuffix(prefix, "."); got != want {
		t.Fatalf("assurance = %q, want just the promise %q", got, want)
	}
}

// A plan whose hires changed between the card and Set it up is refused, and the
// fresh view carries what to confirm: a name an agent took meanwhile, or a model
// set in Settings (FR11).
func TestSetUp_RefusesAPlanWhoseHiresChanged(t *testing.T) {
	h := newHarness(t, demo(t))
	h.setups.setModel(testModel)
	view := h.start(t, "")
	if _, err := h.svc.AnswerPremise(context.Background(), "u1", view.WorkspaceID, PremiseConfirmed); err != nil {
		t.Fatal(err)
	}
	// The look recorded the Home's roles before the Home exists.
	setFacts := func(mutate func(*folderlook.HomeFacts)) {
		t.Helper()
		if _, err := h.svc.deps.Store.Update(view.WorkspaceID, func(r *Record) error {
			for i := range r.Candidates {
				if r.Candidates[i].Kind != folderlook.KindHomeProvider {
					continue
				}
				facts := r.Candidates[i].Home
				if facts == nil {
					facts = newHomeFacts(folderlook.HomeHire{Name: "Portfolio Manager", Job: "Plans the work."})
				}
				mutate(facts)
				r.Candidates[i].Home = facts
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	setFacts(func(*folderlook.HomeFacts) {})
	rec, _ := h.svc.Record("u1", view.WorkspaceID)
	handle := stepHandleOfKind(t, *rec, folderlook.StepHome)
	selected := []string{handle}

	shown, err := h.svc.Plan("u1", view.WorkspaceID, selected)
	if err != nil || len(shown.Hires) != 1 || shown.Hires[0].Name != "Portfolio Manager" || shown.Hires[0].Model != testModel {
		t.Fatalf("hires shown = %+v %v", shown.Hires, err)
	}

	// An agent took the name after the card was drawn.
	setFacts(func(f *folderlook.HomeFacts) {
		f.Hires[0] = folderlook.HomeHire{Name: "Portfolio Manager 2", Job: "Plans the work.", Taken: "Portfolio Manager"}
		f.MissingRoles[0] = "Portfolio Manager 2"
	})
	if _, err := h.svc.SetUp(context.Background(), "u1", view.WorkspaceID, SetUpRequest{PlanDigest: shown.Digest, Selected: selected}); !errors.Is(err, personalassistant.ErrFolderPlanChanged) {
		t.Fatalf("set up on the old hires = %v", err)
	}
	fresh, _ := h.svc.Plan("u1", view.WorkspaceID, selected)
	if fresh.Digest == shown.Digest || len(fresh.Hires) != 1 || fresh.Hires[0].Name != "Portfolio Manager 2" ||
		!strings.Contains(fresh.Hires[0].Note, "Portfolio Manager") {
		t.Fatalf("fresh plan hires = %+v", fresh.Hires)
	}

	// The system model changed after that card was drawn: refused again, and the
	// fresh card shows the new model.
	h.setups.setModel("another-model (claude)")
	if _, err := h.svc.SetUp(context.Background(), "u1", view.WorkspaceID, SetUpRequest{PlanDigest: fresh.Digest, Selected: selected}); !errors.Is(err, personalassistant.ErrFolderPlanChanged) {
		t.Fatalf("set up on the old model = %v", err)
	}
	latest, _ := h.svc.Plan("u1", view.WorkspaceID, selected)
	if latest.Digest == fresh.Digest || latest.Hires[0].Model != "another-model (claude)" {
		t.Fatalf("latest plan = %+v", latest.Hires)
	}
	if _, err := h.svc.SetUp(context.Background(), "u1", view.WorkspaceID, SetUpRequest{PlanDigest: latest.Digest, Selected: selected}); err != nil {
		t.Fatalf("set up on the fresh plan = %v", err)
	}
	h.svc.Wait()
	if len(h.setups.runs) != 1 || len(h.setups.runs[0].Hires) != 1 || h.setups.runs[0].Hires[0].Model != "another-model (claude)" {
		t.Fatalf("the run was not given the confirmed hires: %+v", h.setups.runs)
	}
}

// The card reads these keys from the plan the server sends; renaming one would
// silently blank the hires block, so the wire shape is pinned.
func TestPlanView_HiresWireShape(t *testing.T) {
	rec := hiresRecord(newHomeFacts(folderlook.HomeHire{Name: "Portfolio Manager 2", Job: "Plans the work.", Taken: "Portfolio Manager"}), testModel)
	raw, err := json.Marshal(planViewOf(homePlan(t, rec)))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Hires     []map[string]string `json:"hires"`
		HiresNote *string             `json:"hires_note"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"step": "music-production-home", "name": "Portfolio Manager 2", "job": "Plans the work.",
		"model": testModel, "note": "Your existing Portfolio Manager stays as it is."}
	if len(decoded.Hires) != 1 || !reflect.DeepEqual(decoded.Hires[0], want) || decoded.HiresNote != nil {
		t.Fatalf("hires on the wire = %+v note = %v", decoded.Hires, decoded.HiresNote)
	}
	// The unknown-roles sentence travels as hires_note, with an empty list.
	raw, err = json.Marshal(planViewOf(homePlan(t, hiresRecord(&folderlook.HomeFacts{ProviderName: "X", ProviderVersion: "1"}, testModel))))
	if err != nil {
		t.Fatal(err)
	}
	decoded.Hires, decoded.HiresNote = nil, nil
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Hires == nil || len(decoded.Hires) != 0 || decoded.HiresNote == nil || *decoded.HiresNote != HiresUnnamed {
		t.Fatalf("fallback on the wire = %+v note = %v", decoded.Hires, decoded.HiresNote)
	}
}
