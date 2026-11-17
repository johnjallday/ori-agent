package filejanitor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func planFixture(t *testing.T, names ...string) (*Service, JanitorBatch, []JanitorCandidate, string) {
	t.Helper()
	s, batch, candidates := draftServiceFixture(t, names...)
	settings, err := s.store.LoadSettings("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	return s, batch, candidates, settings.RootPath
}

func readPlan(t *testing.T, s *Service, batchID string, query ReviewPlanQuery) ReviewPlan {
	t.Helper()
	plan, err := s.PlanReview(t.Context(), "local", "ws-1", batchID, query)
	if err != nil {
		t.Fatalf("plan check: %v", err)
	}
	return plan
}

func destinationOf(plan ReviewPlan, category Category) (PlanDestination, bool) {
	for _, destination := range plan.Destinations {
		if destination.Category == category {
			return destination, true
		}
	}
	return PlanDestination{}, false
}

// R7: a name already in the destination means the file gets a new name, and the
// plan says so, so the picture can tag it "renamed".
func TestPlanReview_AClashGivesANewNameAndSaysSo(t *testing.T) {
	s, batch, _, root := planFixture(t, "a.png", "b.png", "c.pdf")
	settings, _ := s.store.LoadSettings("ws-1")
	dir, err := DestinationDir(settings, CategoryImages)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.png"), []byte("already here"), 0o600); err != nil {
		t.Fatal(err)
	}

	plan := readPlan(t, s, batch.ID, ReviewPlanQuery{Destination: "images"})
	images, found := destinationOf(plan, CategoryImages)
	if !found || images.Count != 2 || images.RenamedCount != 1 || images.Destination != "Filed/Images" {
		t.Fatalf("images: %+v", images)
	}
	if plan.MoveCount != 3 || plan.Total != 3 || plan.TrashCount != 0 || plan.OverLimit {
		t.Fatalf("counts: %+v", plan)
	}
	byName := map[string]PlanItemView{}
	for _, item := range plan.Items {
		byName[item.Name] = item
	}
	if item := byName["a.png"]; !item.Renamed || item.FinalName != "a (2).png" {
		t.Fatalf("the clashing file must show its new name: %+v", item)
	}
	if item := byName["b.png"]; item.Renamed || item.FinalName != "b.png" {
		t.Fatalf("a file with no clash keeps its name: %+v", item)
	}
	// Planning created nothing and moved nothing.
	for _, name := range []string{"a.png", "b.png", "c.pdf"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("%s moved: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(settings.RootPath, "Filed", "Documents")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("planning created a destination folder: %v", err)
	}
}

// R36: a ticked file that changed or went missing is named, never silently
// dropped, never filed, and it changes the digest.
func TestPlanReview_AChangedOrMissingFileIsNamedNotDropped(t *testing.T) {
	s, batch, _, root := planFixture(t, "a.png", "b.png", "c.pdf")
	before := readPlan(t, s, batch.ID, ReviewPlanQuery{})
	if before.MoveCount != 3 || before.Changed.Total != 0 {
		t.Fatalf("setup: %+v", before)
	}
	f, err := os.OpenFile(filepath.Join(root, "b.png"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("more"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := os.Remove(filepath.Join(root, "c.pdf")); err != nil {
		t.Fatal(err)
	}

	after := readPlan(t, s, batch.ID, ReviewPlanQuery{})
	if after.Changed.Total != 2 || len(after.Changed.Items) != 2 {
		t.Fatalf("both files must be named: %+v", after.Changed)
	}
	reasons := map[string]string{}
	for _, item := range after.Changed.Items {
		reasons[item.Name] = item.Reason
	}
	if reasons["b.png"] != "changed" || reasons["c.pdf"] != "missing" {
		t.Fatalf("reasons: %v", reasons)
	}
	if after.MoveCount != 1 || after.Total != 1 {
		t.Fatalf("only the untouched file is left to file: %+v", after)
	}
	if after.PlanDigest == "" || after.PlanDigest == before.PlanDigest {
		t.Fatal("a changed file must change the digest")
	}
	if after.Revision == before.Revision {
		t.Fatal("the draft revision binds what changed on disk, so it must move too")
	}
}

// The digest is stable for the same plan and moves with anything the plan is.
func TestPlanReview_TheDigestIsStableAndSensitive(t *testing.T) {
	s, batch, _, _ := planFixture(t, "a.png", "b.png")
	first := readPlan(t, s, batch.ID, ReviewPlanQuery{})
	second := readPlan(t, s, batch.ID, ReviewPlanQuery{})
	if first.PlanDigest == "" || first.PlanDigest != second.PlanDigest {
		t.Fatalf("the same plan must have the same digest: %q %q", first.PlanDigest, second.PlanDigest)
	}
	settings, _ := s.store.LoadSettings("ws-1")
	dir, _ := DestinationDir(settings, CategoryImages)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.png"), []byte("clash"), 0o600); err != nil {
		t.Fatal(err)
	}
	clashed := readPlan(t, s, batch.ID, ReviewPlanQuery{})
	if clashed.PlanDigest == first.PlanDigest {
		t.Fatal("a new name clash must change the digest")
	}
	editDraft(t, s, batch.ID, "exclude_files", func(r *ReviewDraftEdit) {
		_, candidates, _ := s.BatchDetail("ws-1", batch.ID)
		r.CandidateIDs = []string{candidateNamed(t, candidates, "a.png").ID}
	})
	left := readPlan(t, s, batch.ID, ReviewPlanQuery{})
	if left.MoveCount != 1 || left.PlanDigest == clashed.PlanDigest {
		t.Fatalf("leaving a file out must change the plan: %+v", left)
	}
}

// R37 groundwork: Trash is listed on its own, one file at a time.
func TestPlanReview_TrashMarksAreListedSeparately(t *testing.T) {
	s, batch, candidates, _ := planFixture(t, "a.png", "b.png")
	editDraft(t, s, batch.ID, "mark_trash", func(r *ReviewDraftEdit) {
		r.CandidateIDs = []string{candidateNamed(t, candidates, "b.png").ID}
	})
	plan := readPlan(t, s, batch.ID, ReviewPlanQuery{Destination: PlanTrashDestination})
	if plan.TrashCount != 1 || plan.Trash.Count != 1 || plan.MoveCount != 1 || plan.Total != 2 {
		t.Fatalf("counts: %+v", plan)
	}
	if len(plan.Items) != 1 || plan.Items[0].Name != "b.png" || plan.Items[0].Operation != OperationTrash || plan.Items[0].FinalName != "" {
		t.Fatalf("trash items: %+v", plan.Items)
	}
	if len(plan.Trash.First) != 1 || plan.Trash.First[0].Name != "b.png" {
		t.Fatalf("trash first: %+v", plan.Trash)
	}
}

// R39: more than one press may file is reported with its counts, but no file is
// looked at and there is nothing to quote.
func TestPlanReview_OverTheLimitReportsCountsOnly(t *testing.T) {
	names := make([]string, PlanMaxOperations+1)
	for i := range names {
		names[i] = fmt.Sprintf("image-%04d.png", i)
	}
	s, batch, _, _ := planFixture(t, names...)
	plan := readPlan(t, s, batch.ID, ReviewPlanQuery{Destination: "images"})
	if !plan.OverLimit || plan.MoveCount != PlanMaxOperations+1 || plan.Total != PlanMaxOperations+1 {
		t.Fatalf("over limit: %+v", plan)
	}
	if plan.PlanDigest != "" || len(plan.Destinations) != 0 || len(plan.Items) != 0 || plan.Changed.Total != 0 {
		t.Fatalf("an over-limit plan quotes nothing and lists nothing: %+v", plan)
	}
}

func TestPlanReview_PagesRefusesAndPinsRevisions(t *testing.T) {
	s, batch, _, _ := planFixture(t, "a.png", "b.png", "c.png")
	page := readPlan(t, s, batch.ID, ReviewPlanQuery{Destination: "images", Limit: 2, Offset: 2})
	if page.ItemsTotal != 3 || len(page.Items) != 1 || page.Limit != 2 || page.Offset != 2 {
		t.Fatalf("page: %+v", page)
	}
	beyond := readPlan(t, s, batch.ID, ReviewPlanQuery{Destination: "images", Offset: 999})
	if len(beyond.Items) != 0 || beyond.ItemsTotal != 3 {
		t.Fatalf("beyond the end: %+v", beyond)
	}
	for name, query := range map[string]ReviewPlanQuery{
		"unknown destination": {Destination: "nowhere"},
		"limit too large":     {Destination: "images", Limit: PlanItemsPageMax + 1},
		"negative offset":     {Destination: "images", Offset: -1},
	} {
		if _, err := s.PlanReview(t.Context(), "local", "ws-1", batch.ID, query); !errors.Is(err, ErrInvalidAction) {
			t.Fatalf("%s: got %v", name, err)
		}
	}
	good := readPlan(t, s, batch.ID, ReviewPlanQuery{})
	if _, err := s.PlanReview(t.Context(), "local", "ws-1", batch.ID, ReviewPlanQuery{Revision: strings.Repeat("0", 64)}); !errors.Is(err, ErrReviewConflict) {
		t.Fatalf("a plan of different data must be a conflict: %v", err)
	}
	if _, err := s.PlanReview(t.Context(), "local", "ws-1", batch.ID, ReviewPlanQuery{Revision: good.Revision, GroupRevision: good.GroupRevision}); err != nil {
		t.Fatalf("the revisions on screen must be accepted: %v", err)
	}
	if _, err := s.PlanReview(t.Context(), "local", "ws-1", "latest", ReviewPlanQuery{}); !errors.Is(err, ErrReviewUnavailable) {
		t.Fatalf("a plan names a concrete scan: %v", err)
	}
}

// Task 2.6: the plan check runs on open and after every saved change, so its time
// for the most the one press can file (1,000 files) is the time a person waits for
// the button to say what it will do. This records it for a clean destination and
// for the worst case, where every one of the 1,000 names is already taken. The
// bound is deliberately loose (the figures are in the findings log); it exists to
// catch a change that makes the check scale badly, not to benchmark the machine.
func TestPlanReview_ThousandFilesTiming(t *testing.T) {
	if testing.Short() {
		t.Skip("timing run")
	}
	names := make([]string, PlanMaxOperations)
	for i := range names {
		names[i] = fmt.Sprintf("image-%04d.png", i)
	}
	s, batch, _, _ := planFixture(t, names...)
	settings, _ := s.store.LoadSettings("ws-1")
	editDraft(t, s, batch.ID, "select_group", func(r *ReviewDraftEdit) { r.GroupID = reviewGroupID("filing-images") })

	time1000 := func(label string) time.Duration {
		best := time.Hour
		for range 5 {
			started := time.Now()
			plan := readPlan(t, s, batch.ID, ReviewPlanQuery{})
			elapsed := time.Since(started)
			if plan.OverLimit || plan.MoveCount != PlanMaxOperations {
				t.Fatalf("%s: expected %d planned moves, got %+v", label, PlanMaxOperations, plan)
			}
			best = min(best, elapsed)
		}
		t.Logf("%s: plan check for %d files, best of 5: %s (%s/%s, %s)", label, PlanMaxOperations, best, runtime.GOOS, runtime.GOARCH, runtime.Version())
		return best
	}
	clean := time1000("clean destination")

	dir, err := DestinationDir(settings, CategoryImages)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("taken"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	clashing := time1000("every name already taken")
	if clean > 5*time.Second || clashing > 5*time.Second {
		t.Fatalf("the plan check for %d files took %s clean and %s with every name taken; the button waits on it", PlanMaxOperations, clean, clashing)
	}
}

// The picture asks about exactly the files it is showing, whichever five a card
// lists: one call returns their planned names.
func TestPlanReview_AnswersForExactlyTheFilesAskedAbout(t *testing.T) {
	s, batch, candidates, _ := planFixture(t, "a.png", "b.png", "c.png", "d.pdf")
	settings, _ := s.store.LoadSettings("ws-1")
	dir, _ := DestinationDir(settings, CategoryImages)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "c.png"), []byte("already here"), 0o600); err != nil {
		t.Fatal(err)
	}
	ids := []string{candidateNamed(t, candidates, "c.png").ID, candidateNamed(t, candidates, "d.pdf").ID}

	plan := readPlan(t, s, batch.ID, ReviewPlanQuery{CandidateIDs: ids})
	if plan.ItemsTotal != 2 || len(plan.Items) != 2 {
		t.Fatalf("items: %+v", plan.Items)
	}
	byName := map[string]PlanItemView{}
	for _, item := range plan.Items {
		byName[item.Name] = item
	}
	if item := byName["c.png"]; !item.Renamed || item.FinalName != "c (2).png" {
		t.Fatalf("c.png: %+v", item)
	}
	if item := byName["d.pdf"]; item.Renamed || item.FinalName != "d.pdf" {
		t.Fatalf("d.pdf: %+v", item)
	}
	for name, query := range map[string]ReviewPlanQuery{
		"a duplicate id":                  {CandidateIDs: []string{ids[0], ids[0]}},
		"an id that is not an id":         {CandidateIDs: []string{"../etc"}},
		"ids together with a destination": {CandidateIDs: ids, Destination: "images"},
		"too many ids":                    {CandidateIDs: make([]string, PlanItemsPageMax+1)},
	} {
		if _, err := s.PlanReview(t.Context(), "local", "ws-1", batch.ID, query); !errors.Is(err, ErrInvalidAction) {
			t.Fatalf("%s: got %v", name, err)
		}
	}
}

// A ticked file that changed on disk is not "left out by you": it is drawn as a
// file that changed, once, and still counts as waiting.
func TestReviewPicture_AChangedTickedFileIsDrawnAsChangedNotLeftOut(t *testing.T) {
	s, batch, _, root := planFixture(t, "a.png", "b.png", "c.pdf")
	f, err := os.OpenFile(filepath.Join(root, "b.png"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("changed"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := os.Remove(filepath.Join(root, "c.pdf")); err != nil {
		t.Fatal(err)
	}

	picture, err := s.ReadReviewPicture(t.Context(), "local", "ws-1", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if picture.Changed != 2 || picture.CouldNotFile.Count != 2 || picture.LeftOut.Count != 0 {
		t.Fatalf("both files are drawn as changed, none as left out: %+v", picture)
	}
	reasons := map[string]PictureFile{}
	for _, file := range picture.CouldNotFile.Files {
		reasons[file.Name] = file
	}
	if file := reasons["b.png"]; file.State != "changed" || file.Reason != pictureReasonChanged || file.CanRetry {
		t.Fatalf("b.png: %+v", file)
	}
	if file := reasons["c.pdf"]; file.State != "missing" || file.Reason != pictureReasonMissing {
		t.Fatalf("c.pdf: %+v", file)
	}
	sum := picture.Changed + picture.LeftOut.Count + picture.Trash.Count
	for _, folder := range picture.Folders {
		sum += folder.Count + folder.UnfiledCount
	}
	for _, row := range picture.Loose {
		sum += row.LooseCount
	}
	if sum != picture.Waiting || picture.Waiting != 3 {
		t.Fatalf("the sections add up to %d, the scan has %d waiting", sum, picture.Waiting)
	}
}

// The plan is path-free and leaves the saved scan exactly as it was.
func TestPlanReview_IsPathFreeAndWritesNothing(t *testing.T) {
	s, batch, _, root := planFixture(t, "a.png", "c.pdf")
	before := scanStateBytes(t, s)
	plan := readPlan(t, s, batch.ID, ReviewPlanQuery{Destination: "images"})
	body, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{root, "fingerprint", "token", "scope"} {
		if strings.Contains(string(body), private) {
			t.Fatalf("the plan leaked %q: %s", private, body)
		}
	}
	if after := scanStateBytes(t, s); !bytes.Equal(before, after) {
		t.Fatal("the plan check changed the saved scan state")
	}
	state, _ := s.store.LoadScanState("ws-1")
	if len(state.Approvals) != 0 || len(state.Actions) != 0 {
		t.Fatal("the plan check issued authority or acted")
	}
}
