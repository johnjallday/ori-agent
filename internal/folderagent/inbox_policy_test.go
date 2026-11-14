package folderagent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func TestRootIdentity_ExactPhysicalKnownRoot(t *testing.T) {
	home := t.TempDir()
	downloads := filepath.Join(home, "Downloads")
	for _, p := range []string{downloads, filepath.Join(downloads, "Songs"), downloads + "-Archive", filepath.Join(home, "other", "Downloads"), filepath.Join(home, "downloads")} {
		if err := os.MkdirAll(p, 0750); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(home, "alias")
	if err := os.Symlink(downloads, alias); err != nil {
		t.Fatal(err)
	}
	known := func(id string) (string, error) {
		if id != "downloads" {
			return "", errors.New("unknown known folder")
		}
		return downloads, nil
	}
	for _, tc := range []struct {
		path string
		want folderlook.RootPolicy
	}{
		{downloads, folderlook.RootDownloads}, {downloads + string(filepath.Separator), folderlook.RootDownloads},
		{alias, folderlook.RootDownloads}, {filepath.Join(downloads, "Songs"), folderlook.RootOrdinary},
		{downloads + "-Archive", folderlook.RootOrdinary}, {filepath.Join(home, "other", "Downloads"), folderlook.RootOrdinary},
	} {
		t.Run(tc.path, func(t *testing.T) {
			got := ResolveRootIdentity(tc.path, known)
			if got.Policy != tc.want || len(got.Binding) != 64 {
				t.Fatalf("identity=%+v", got)
			}
		})
	}
	// Use the filesystem itself as the case authority (macOS volumes may be case sensitive).
	lower := filepath.Join(home, "downloads")
	a, _ := os.Stat(downloads)
	b, _ := os.Stat(lower)
	want := folderlook.RootOrdinary
	if os.SameFile(a, b) {
		want = folderlook.RootDownloads
	}
	if got := ResolveRootIdentity(lower, known); got.Policy != want {
		t.Fatalf("case=%+v want %s", got, want)
	}
	for _, resolver := range []func(string) (string, error){nil, func(string) (string, error) { return "", errors.New("unavailable") }, func(string) (string, error) { return filepath.Join(home, "missing"), nil }} {
		if got := ResolveRootIdentity(downloads, resolver); got.Policy != folderlook.RootOrdinary || got.Binding == "" {
			t.Fatalf("failure=%+v", got)
		}
	}
	if got := ResolveRootIdentity("Downloads", known); got.Policy != folderlook.RootUnresolved {
		t.Fatal(got)
	}
	// The known folder may itself be redirected through a symlink.
	if got := ResolveRootIdentity(downloads, func(string) (string, error) { return alias, nil }); got.Policy != folderlook.RootDownloads {
		t.Fatal(got)
	}
}

func downloadsHarness(t *testing.T, root string) *harness {
	t.Helper()
	h := newHarness(t, root)
	h.svc.deps.KnownFolderRoot = func(string) (string, error) { return root, nil }
	return h
}

func assertInbox(t *testing.T, report View) {
	t.Helper()
	if report.Plan == nil || !report.Plan.Runnable || len(report.Plan.Steps) != 1 || report.Plan.Steps[0].Kind != folderlook.StepFileCurator || !report.Plan.Steps[0].Chosen {
		t.Fatalf("plan=%+v", report.Plan)
	}
	if !strings.Contains(report.PolicyNote, "Downloads stays an inbox") || !report.ExploreFolder {
		t.Fatalf("policy=%+v", report)
	}
	if len(report.Candidates) != 1 || report.Candidates[0].ID != "file-curator" {
		t.Fatalf("candidates=%+v", report.Candidates)
	}
}

func TestDownloads_FreshSelectionRoutesNeutralEvenWithProjects(t *testing.T) {
	for _, choice := range []personalassistant.FolderChoice{{Chip: "downloads"}, {Picker: true}, {SelectionToken: "trusted-token"}} {
		t.Run(choice.Chip+choice.SelectionToken+func() string {
			if choice.Picker {
				return "picker"
			}
			return ""
		}(), func(t *testing.T) {
			root := demo(t)
			h := downloadsHarness(t, root)
			chosen, err := h.svc.Choose(t.Context(), "u1", choice)
			if err != nil {
				t.Fatal(err)
			}
			started, err := h.svc.Start(t.Context(), "u1", chosen.Token, "", Hire{})
			if err != nil {
				t.Fatal(err)
			}
			h.svc.Wait()
			report, err := h.svc.View(t.Context(), "u1", started.WorkspaceID)
			if err != nil {
				t.Fatal(err)
			}
			assertInbox(t, report)
			if !strings.Contains(report.FactsLine, "7 REAPER songs") {
				t.Fatalf("facts=%s", report.FactsLine)
			}
			rec, _ := h.svc.Record("u1", started.WorkspaceID)
			if rec.Facts.Portfolio == nil || foldermap.StageFor(rec.Facts) != foldermap.StageHome {
				t.Fatal("policy destroyed portfolio evidence")
			}
			raw, _ := json.Marshal(rec)
			if strings.Contains(string(raw), root) || strings.Contains(string(raw), "PolicyBinding") || strings.Contains(string(raw), "RootPolicy") {
				t.Fatal("persisted identity or path")
			}
			if _, err := h.svc.View(t.Context(), "other", started.WorkspaceID); !errors.Is(err, ErrNotOwner) {
				t.Fatal(err)
			}
			for _, id := range []string{"group", "music-production-home", "home:ori_music", "integration:ori_reaper", "reaper-tools"} {
				plan, _ := h.svc.Plan("u1", started.WorkspaceID, []string{"file-curator", id})
				if plan.Runnable {
					t.Fatalf("accepted %s", id)
				}
				if _, err := h.svc.SetUp(t.Context(), "u1", started.WorkspaceID, SetUpRequest{PlanDigest: plan.Digest, Selected: []string{"file-curator", id}}); !errors.Is(err, personalassistant.ErrFolderPlanChanged) {
					t.Fatalf("%s: %v", id, err)
				}
			}
			if len(h.setups.runs) != 0 {
				t.Fatal("forbidden request ran")
			}
			if _, err := h.svc.SetUp(t.Context(), "u1", started.WorkspaceID, SetUpRequest{PlanDigest: report.Plan.Digest}); err != nil {
				t.Fatal(err)
			}
			h.svc.Wait()
			if len(h.setups.runs) != 1 || len(h.setups.runs[0].Candidates) != 1 || h.setups.runs[0].Candidates[0].Kind != folderlook.KindFileCurator {
				t.Fatalf("runs=%+v", h.setups.runs)
			}
		})
	}
}

func TestDownloads_SparseRootMarkerAndUnavailableIdentity(t *testing.T) {
	for _, paths := range [][]string{nil, {"Song.rpp"}, {"a.pdf", "b.txt"}} {
		root := build(t, paths...)
		h := downloadsHarness(t, root)
		v := h.start(t, "")
		report, _ := h.svc.View(t.Context(), "u1", v.WorkspaceID)
		assertInbox(t, report)
		h.svc.deps.KnownFolderRoot = func(string) (string, error) { return "", errors.New("missing") }
		unknown, _ := h.svc.View(t.Context(), "u1", v.WorkspaceID)
		if unknown.PolicyNote != "" {
			t.Fatalf("missing peer made this available folder unresolved: %+v", unknown)
		}
		if _, err := h.svc.SetUp(t.Context(), "u1", v.WorkspaceID, SetUpRequest{PlanDigest: report.Plan.Digest}); !errors.Is(err, personalassistant.ErrFolderPlanChanged) {
			t.Fatal(err)
		}
	}
}

func TestDownloads_LegacyProjectionReadPurityRestartAndStaleAnchor(t *testing.T) {
	root := demo(t)
	h := newHarness(t, root)
	v := h.start(t, "")
	old, _ := h.svc.View(t.Context(), "u1", v.WorkspaceID)
	// Simulate a pending pre-policy record with an unsafe recommendation.
	_, err := h.svc.deps.Store.Update(v.WorkspaceID, func(r *Record) error {
		r.Sections = &folderlook.Sections{Plan: "Create [music-production-home] and [group].", PlanSteps: []string{"music-production-home", "group"}, Goal: "Release songs", Next: "Work on songs"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := h.svc.deps.Store.Read(v.WorkspaceID)
	h.svc.deps.KnownFolderRoot = func(string) (string, error) { return root, nil }
	restarted := NewService(h.svc.deps)
	for range 2 {
		report, _ := restarted.View(context.Background(), "u1", v.WorkspaceID)
		assertInbox(t, report)
		if report.Plan.Words != "" || report.SuggestedGoal != "" || report.FirstTicket != "" {
			t.Fatalf("stale prose=%+v", report)
		}
		_ = restarted.List(t.Context(), "u1")
	}
	after, _ := h.svc.deps.Store.Read(v.WorkspaceID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("read wrote/recomputed saved evidence")
	}
	if _, err := restarted.SetUp(t.Context(), "u1", v.WorkspaceID, SetUpRequest{PlanDigest: old.Plan.Digest, Selected: old.Plan.Selected}); !errors.Is(err, personalassistant.ErrFolderPlanChanged) {
		t.Fatal(err)
	}
	report, _ := restarted.View(t.Context(), "u1", v.WorkspaceID)
	other := build(t, "Song.rpp")
	h.ws.mu.Lock()
	h.ws.anchors[v.WorkspaceID] = other
	h.ws.mu.Unlock()
	if _, err := restarted.SetUp(t.Context(), "u1", v.WorkspaceID, SetUpRequest{PlanDigest: report.Plan.Digest}); !errors.Is(err, personalassistant.ErrFolderPlanChanged) {
		t.Fatal(err)
	}
	if len(h.setups.runs) > 0 {
		t.Fatal("stale plan dispatched")
	}
}

func TestDownloads_ModelCannotRestoreForbiddenStepsOrGoal(t *testing.T) {
	root := demo(t)
	h := downloadsHarness(t, root)
	turns := &scriptedTurns{responses: []*llm.ChatResponse{{Content: "What it is: Songs and loose files.\nPlan: Create [music-production-home] and [group], connect [reaper-tools].\nSuggested goal: Release songs.\nNext: Work on songs."}}}
	withTurns(h, turns)
	v := h.start(t, "")
	report, _ := h.svc.View(t.Context(), "u1", v.WorkspaceID)
	assertInbox(t, report)
	reference := turns.references[0]
	if !strings.Contains(reference, "Downloads stays an inbox") || strings.Contains(reference, "[group]") || strings.Contains(reference, "[reaper-tools]") || strings.Contains(reference, root) {
		t.Fatalf("reference=%s", reference)
	}
	if report.SuggestedGoal != "" || report.FirstTicket != "" {
		t.Fatal("project suggestions leaked")
	}
	if _, err := h.svc.SetUp(t.Context(), "u1", v.WorkspaceID, SetUpRequest{PlanDigest: report.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	if len(h.tickets.backlog) != 0 || len(h.ws.goals) != 0 {
		t.Fatal("implicit project goal/ticket")
	}
}

func TestDownloads_DispatchRechecksAndDoesNotRewriteCompletedReceipt(t *testing.T) {
	root := demo(t)
	h := newHarness(t, root)
	v := h.start(t, "")
	rec, _ := h.svc.Record("u1", v.WorkspaceID)
	plan := BuildPlan(*rec, nil)
	h.svc.deps.KnownFolderRoot = func(string) (string, error) { return root, nil }
	if err := h.svc.CheckPlan(v.WorkspaceID, plan); !errors.Is(err, personalassistant.ErrFolderPlanChanged) {
		t.Fatal(err)
	}
	_, _ = h.svc.deps.Store.Update(v.WorkspaceID, func(r *Record) error {
		r.State = StateSetUp
		r.Setup = &Setup{Status: personalassistant.FolderSetupDone, Selected: plan.Selected, Lines: plan.Lines, Receipt: &SetupReceipt{Home: &WorkspaceRef{ID: "home", Name: "Music Production Home"}}}
		return nil
	})
	before, _ := h.svc.deps.Store.Read(v.WorkspaceID)
	view, _ := h.svc.View(t.Context(), "u1", v.WorkspaceID)
	after, _ := h.svc.deps.Store.Read(v.WorkspaceID)
	if !reflect.DeepEqual(before, after) || view.Setup.Receipt.Home.ID != "home" {
		t.Fatal("rewrote completed organization")
	}
	if err := h.svc.CheckPlan(v.WorkspaceID, plan); !errors.Is(err, personalassistant.ErrFolderPlanChanged) {
		t.Fatal("historical receipt authorized new run", err)
	}
}
