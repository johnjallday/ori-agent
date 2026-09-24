package personalassistant

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

func folderdigestShape(s string) folderdigest.Shape { return folderdigest.Shape(s) }

// folderDigestFixture is a hired, active relationship over a temporary HQ
// with a fake home holding one folder per chip: Documents is a mixed tree,
// Downloads a dump, Desktop empty.
type folderDigestFixture struct {
	*knowledgeFixture
	home    string
	now     time.Time
	ids     int
	service *FolderDigestService
	store   *FolderDigestStore
}

func newFolderDigestFixture(t *testing.T) *folderDigestFixture {
	t.Helper()
	kf := newKnowledgeFixture(t)
	// macOS temp dirs sit behind a symlink (/var → /private/var); the service
	// stores canonical paths, so the fixture's home must be canonical too.
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	seedFolderTrees(t, home)
	f := &folderDigestFixture{knowledgeFixture: kf, home: home, now: time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)}
	f.store = NewFolderDigestStore(NewKnowledgeStore(kf.resolver(), kf.folder))
	f.service = f.newService()
	return f
}

// newService builds a service over the same store, as a restarted server
// would: the in-memory offer paths are gone.
func (f *folderDigestFixture) newService() *FolderDigestService {
	return NewFolderDigestService(f.store, FolderDigestDeps{
		ValidateRoot: func(raw string) (string, error) {
			resolved, err := filepath.EvalSymlinks(raw)
			if err != nil {
				return "", &FolderRootError{Message: "That folder no longer exists. Choose a folder that is on this computer."}
			}
			if resolved == f.home {
				return "", &FolderRootError{Message: "That is your whole home folder."}
			}
			return resolved, nil
		},
		HomeDir: func() (string, error) { return f.home, nil },
		Now:     func() time.Time { return f.now },
		NewID: func() string {
			f.ids++
			return fmt.Sprintf("offer-%d", f.ids)
		},
		Tidier: &fakeFolderTidier{},
	})
}

func seedFolderTrees(t *testing.T, home string) {
	t.Helper()
	write := func(rel string, age int) {
		full := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		at := time.Now().AddDate(0, 0, -age)
		if err := os.Chtimes(full, at, at); err != nil {
			t.Fatal(err)
		}
	}
	// Downloads: 25 loose files of 6 kinds.
	for i, ext := range []string{".pdf", ".png", ".zip", ".dmg", ".csv", ".txt"} {
		for n := range 5 {
			if i == 5 && n > 0 {
				break
			}
			if i == 4 && n > 3 {
				break
			}
			write(fmt.Sprintf("Downloads/file-%d%s", n, ext), 2)
		}
	}
	// Documents: 40 loose files of 6 kinds, three projects, one folder of scans.
	for _, ext := range []string{".pdf", ".png", ".zip", ".dmg", ".csv", ".txt"} {
		for n := range 7 {
			if ext == ".txt" && n > 4 {
				break
			}
			write(fmt.Sprintf("Documents/loose-%d%s", n, ext), 3)
		}
	}
	write("Documents/Thesis/main.tex", 1)
	for n := range 5 {
		write(fmt.Sprintf("Documents/Thesis/chapters/ch-%d.tex", n), 1)
	}
	write("Documents/website/package.json", 3)
	for n := range 4 {
		write(fmt.Sprintf("Documents/website/src/index-%d.js", n), 3)
	}
	write("Documents/Album/Song.rpp", 7)
	for n := range 3 {
		write(fmt.Sprintf("Documents/Album/Media/take-%d.wav", n), 7)
	}
	for n := range 5 {
		write(fmt.Sprintf("Documents/Scans/scan-%d.pdf", n), 40)
	}
	// Desktop: two files.
	write("Desktop/todo.txt", 0)
	write("Desktop/photo.png", 0)
}

func TestFolderDigest_ChipScanBuildsOfferWithoutPaths(t *testing.T) {
	f := newFolderDigestFixture(t)
	ctx := context.Background()

	offer, err := f.service.ScanChip(ctx, "local", "documents")
	if err != nil {
		t.Fatal(err)
	}
	if offer.Verdict != "mixed" || offer.Subject.Name != "Thesis" || offer.Subject.Shape != "manuscript" || offer.ProjectsCount != 3 || offer.LooseFiles != 40 {
		t.Fatalf("offer=%+v", offer)
	}
	if offer.Reason != "3 projects and 40 loose files" || !offer.Remember || offer.NeedsPick || offer.Folder != "Documents" {
		t.Fatalf("offer=%+v", offer)
	}

	view, err := f.service.Current(ctx, "local")
	if err != nil || view.Offer == nil || view.Offer.ID != offer.ID {
		t.Fatalf("view=%+v err=%v", view, err)
	}
	if len(view.Chips) != 3 || view.PickerAvailable || view.PickerNote == "" {
		t.Fatalf("chooser=%+v", view)
	}

	sidecar := filepath.Join(f.folder.path, ".ori", folderDigestFileName)
	info, err := os.Stat(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("sidecar mode = %o", info.Mode().Perm())
	}
	data, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), f.home) || strings.Contains(string(data), "/Documents") {
		t.Errorf("sidecar stores a path:\n%s", data)
	}
	if !strings.Contains(string(data), `"rel_path": "Thesis"`) || !strings.Contains(string(data), FolderKey(filepath.Join(f.home, "Documents"))) {
		t.Errorf("sidecar lacks the folder key or display name:\n%s", data)
	}
	doc, err := f.store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	stored := doc.Offer(offer.ID)
	if stored == nil || len(stored.Queue) != 3 || stored.Queue[2].Kind != FolderChoiceTidy || stored.Queue[0].Name != "website" {
		t.Fatalf("stored offer=%+v", stored)
	}
}

func TestFolderDigest_NoTombstonesAndRescanSkipsIt(t *testing.T) {
	f := newFolderDigestFixture(t)
	ctx := context.Background()
	offer, err := f.service.ScanChip(ctx, "local", "documents")
	if err != nil {
		t.Fatal(err)
	}
	declined, err := f.service.Decide(ctx, "local", offer.ID, FolderDecisionInput{Decision: FolderDecisionNo, RequestID: "req-no"})
	if err != nil || declined.Status != FolderOfferDeclined {
		t.Fatalf("declined=%+v err=%v", declined, err)
	}
	if view, err := f.service.Current(ctx, "local"); err != nil || view.Offer != nil {
		t.Fatalf("declined offer still pending: %+v %v", view.Offer, err)
	}
	again, err := f.service.ScanChip(ctx, "local", "documents")
	if err != nil {
		t.Fatal(err)
	}
	if again.Subject.Name != "website" || again.ProjectsCount != 2 || again.Reason != "2 projects and 40 loose files" {
		t.Fatalf("rescan re-offered a declined project: %+v", again)
	}
	doc, _ := f.store.Read(ctx, "local")
	if len(doc.Tombstones) != 1 || doc.Tombstones[0].Name != "Thesis" || len(doc.Decisions) != 1 {
		t.Fatalf("records=%+v %+v", doc.Tombstones, doc.Decisions)
	}
}

func TestFolderDigest_LaterHidesForAWeekThenResurfaces(t *testing.T) {
	f := newFolderDigestFixture(t)
	ctx := context.Background()
	offer, err := f.service.ScanChip(ctx, "local", "downloads")
	if err != nil {
		t.Fatal(err)
	}
	if offer.Verdict != "dump" || offer.Reason != "25 loose files of 6 kinds" || offer.Remember {
		t.Fatalf("offer=%+v", offer)
	}
	if _, err := f.service.Decide(ctx, "local", offer.ID, FolderDecisionInput{Decision: FolderDecisionLater, RequestID: "req-later"}); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(6 * 24 * time.Hour)
	if view, _ := f.service.Current(ctx, "local"); view.Offer != nil {
		t.Fatalf("later offer came back after six days: %+v", view.Offer)
	}
	f.now = f.now.Add(2 * 24 * time.Hour)
	view, err := f.service.Current(ctx, "local")
	if err != nil || view.Offer == nil || view.Offer.ID != offer.ID || view.Offer.Status != FolderOfferPending {
		t.Fatalf("later offer did not resurface: %+v %v", view.Offer, err)
	}
}

func TestFolderDigest_OnePendingOfferAtATime(t *testing.T) {
	f := newFolderDigestFixture(t)
	ctx := context.Background()
	first, err := f.service.ScanChip(ctx, "local", "documents")
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.service.ScanChip(ctx, "local", "downloads")
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := f.store.Read(ctx, "local")
	if got := doc.Offer(first.ID); got.Status != FolderOfferLater || got.LaterUntil == nil {
		t.Fatalf("earlier offer=%+v, want later", got)
	}
	if view, _ := f.service.Current(ctx, "local"); view.Offer == nil || view.Offer.ID != second.ID {
		t.Fatalf("pending=%+v", view.Offer)
	}
	// An empty folder produces a card but never a pending question.
	empty, err := f.service.ScanChip(ctx, "local", "desktop")
	if err != nil || empty.Verdict != "empty" || empty.Status != FolderOfferClosed || empty.Reason != "2 files" {
		t.Fatalf("empty=%+v err=%v", empty, err)
	}
	if view, _ := f.service.Current(ctx, "local"); view.Offer == nil || view.Offer.ID != second.ID {
		t.Fatalf("an empty scan must not displace the pending offer: %+v", view.Offer)
	}
}

func TestFolderDigest_YesChoicesAndReplay(t *testing.T) {
	f := newFolderDigestFixture(t)
	ctx := context.Background()
	offer, err := f.service.ScanChip(ctx, "local", "documents")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Decide(ctx, "local", offer.ID, FolderDecisionInput{Decision: FolderDecisionYes, RequestID: "req-yes"}); !errors.Is(err, ErrFolderOfferChoice) {
		t.Fatalf("mixed yes without a choice err=%v", err)
	}
	yes, err := f.service.Decide(ctx, "local", offer.ID, FolderDecisionInput{Decision: FolderDecisionYes, Choice: FolderChoiceTidy, RequestID: "req-yes"})
	if err != nil {
		t.Fatal(err)
	}
	// A tidy runs in the same request, so the offer comes back resolved.
	if yes.Status != FolderOfferResolved || yes.Choice != FolderChoiceTidy || !yes.Subject.IsRoot || yes.Subject.Name != "Documents" || yes.Outcome == nil || yes.Outcome.Kind != FolderChoiceTidy || yes.Outcome.WorkspaceID == "" {
		t.Fatalf("yes=%+v", yes)
	}
	doc, _ := f.store.Read(ctx, "local")
	stored := doc.Offer(offer.ID)
	if len(stored.Queue) != 3 || stored.Queue[0].Name != "Thesis" || stored.Queue[0].Kind != FolderChoiceProject {
		t.Fatalf("queue after tidy choice=%+v", stored.Queue)
	}

	replay, err := f.service.Decide(ctx, "local", offer.ID, FolderDecisionInput{Decision: FolderDecisionNo, RequestID: "req-yes"})
	if err != nil || replay.Status != FolderOfferResolved || replay.Decision != FolderDecisionYes {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	doc, _ = f.store.Read(ctx, "local")
	if len(doc.Decisions) != 1 || len(doc.Receipts) != 1 {
		t.Fatalf("replay wrote again: %d decisions %d receipts", len(doc.Decisions), len(doc.Receipts))
	}
	if _, err := f.service.Decide(ctx, "local", offer.ID, FolderDecisionInput{Decision: FolderDecisionNo, RequestID: "req-other"}); !errors.Is(err, ErrFolderOfferDecided) {
		t.Fatalf("second decision err=%v", err)
	}
	if _, err := f.service.Decide(ctx, "local", offer.ID, FolderDecisionInput{Decision: "maybe", RequestID: "r"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("bad decision err=%v", err)
	}
	if _, err := f.service.Decide(ctx, "local", "missing", FolderDecisionInput{Decision: FolderDecisionNo, RequestID: "r"}); !errors.Is(err, ErrFolderOfferNotFound) {
		t.Fatalf("missing offer err=%v", err)
	}
	path, err := f.service.SubjectPath(ctx, "local", offer.ID)
	if err != nil || path != filepath.Join(f.home, "Documents") {
		t.Fatalf("subject path=%q err=%v", path, err)
	}
}

func TestFolderDigest_ProjectSubjectPathAndAmbiguousChoice(t *testing.T) {
	f := newFolderDigestFixture(t)
	ctx := context.Background()
	offer, err := f.service.ScanChip(ctx, "local", "documents")
	if err != nil {
		t.Fatal(err)
	}
	yes, err := f.service.Decide(ctx, "local", offer.ID, FolderDecisionInput{Decision: FolderDecisionYes, Choice: FolderChoiceProject, RequestID: "req"})
	if err != nil || yes.Subject.Name != "Thesis" || yes.Choice != FolderChoiceProject {
		t.Fatalf("yes=%+v err=%v", yes, err)
	}
	path, err := f.service.SubjectPath(ctx, "local", offer.ID)
	if err != nil || path != filepath.Join(f.home, "Documents", "Thesis") {
		t.Fatalf("subject path=%q err=%v", path, err)
	}
}

func TestFolderDigest_NextQueuedCandidateAfterTheSitting(t *testing.T) {
	f := newFolderDigestFixture(t)
	ctx := context.Background()
	offer, err := f.service.ScanChip(ctx, "local", "documents")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Decide(ctx, "local", offer.ID, FolderDecisionInput{Decision: FolderDecisionNo, RequestID: "req"}); err != nil {
		t.Fatal(err)
	}
	if view, _ := f.service.Current(ctx, "local"); view.Offer != nil {
		t.Fatalf("next candidate offered in the same sitting: %+v", view.Offer)
	}
	f.now = f.now.Add(FolderNextOfferDelay)
	view, err := f.service.Current(ctx, "local")
	if err != nil || view.Offer == nil {
		t.Fatalf("no next offer: %+v %v", view, err)
	}
	if view.Offer.Verdict != "project" || view.Offer.Subject.Name != "website" || view.Offer.Reason == "" || view.Offer.NeedsPick {
		t.Fatalf("next offer=%+v", view.Offer)
	}
	path, err := f.service.SubjectPath(ctx, "local", view.Offer.ID)
	if err != nil || path != filepath.Join(f.home, "Documents", "website") {
		t.Fatalf("next subject path=%q err=%v", path, err)
	}
	// Deciding it leaves Album and the tidy branch for later, one at a time.
	if _, err := f.service.Decide(ctx, "local", view.Offer.ID, FolderDecisionInput{Decision: FolderDecisionNo, RequestID: "req-2"}); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(FolderNextOfferDelay)
	view, _ = f.service.Current(ctx, "local")
	if view.Offer == nil || view.Offer.Subject.Name != "Album" {
		t.Fatalf("third offer=%+v", view.Offer)
	}
	if _, err := f.service.Decide(ctx, "local", view.Offer.ID, FolderDecisionInput{Decision: FolderDecisionNo, RequestID: "req-3"}); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(FolderNextOfferDelay)
	view, _ = f.service.Current(ctx, "local")
	if view.Offer == nil || view.Offer.Verdict != "dump" || view.Offer.LooseFiles != 40 || view.Offer.Reason != "40 loose files of 6 kinds" {
		t.Fatalf("tidy offer=%+v", view.Offer)
	}
}

func TestFolderDigest_RestartKeepsChipOffersAndLosesPickedOnes(t *testing.T) {
	f := newFolderDigestFixture(t)
	ctx := context.Background()
	chipOffer, err := f.service.ScanChip(ctx, "local", "downloads")
	if err != nil {
		t.Fatal(err)
	}
	restarted := f.newService()
	view, err := restarted.Current(ctx, "local")
	if err != nil || view.Offer == nil || view.Offer.NeedsPick {
		t.Fatalf("chip offer after restart=%+v err=%v", view.Offer, err)
	}
	if _, err := restarted.Decide(ctx, "local", chipOffer.ID, FolderDecisionInput{Decision: FolderDecisionYes, RequestID: "r"}); err != nil {
		t.Fatalf("chip yes after restart: %v", err)
	}

	picked := filepath.Join(f.home, "Documents", "Thesis")
	f.service.deps.Picker = fakeFolderPicker{path: picked, chosen: true}
	offer, err := f.service.ScanPicked(ctx, "local")
	if err != nil || offer == nil || offer.Verdict != "project" || offer.Subject.Name != "Thesis" || !offer.Subject.IsRoot {
		t.Fatalf("picked=%+v err=%v", offer, err)
	}
	restarted = f.newService()
	view, err = restarted.Current(ctx, "local")
	if err != nil || view.Offer == nil || !view.Offer.NeedsPick {
		t.Fatalf("picked offer after restart=%+v err=%v", view.Offer, err)
	}
	if _, err := restarted.Decide(ctx, "local", offer.ID, FolderDecisionInput{Decision: FolderDecisionYes, RequestID: "r2"}); !errors.Is(err, ErrFolderPathLost) {
		t.Fatalf("picked yes after restart err=%v", err)
	}
	// Saying no still works without the path.
	if _, err := restarted.Decide(ctx, "local", offer.ID, FolderDecisionInput{Decision: FolderDecisionNo, RequestID: "r3"}); err != nil {
		t.Fatalf("picked no after restart err=%v", err)
	}
}

func TestFolderDigest_PickerCancelAndUnavailable(t *testing.T) {
	f := newFolderDigestFixture(t)
	ctx := context.Background()
	if _, err := f.service.ScanPicked(ctx, "local"); !errors.Is(err, ErrFolderPickerUnavailable) {
		t.Fatalf("no picker err=%v", err)
	}
	f.service.deps.Picker = fakeFolderPicker{}
	offer, err := f.service.ScanPicked(ctx, "local")
	if err != nil || offer != nil {
		t.Fatalf("cancel=%+v err=%v", offer, err)
	}
	f.service.deps.Picker = fakeFolderPicker{path: f.home, chosen: true}
	var rootErr *FolderRootError
	if _, err := f.service.ScanPicked(ctx, "local"); !errors.As(err, &rootErr) {
		t.Fatalf("home folder err=%v", err)
	}
	if view, _ := f.service.Current(ctx, "local"); !view.PickerAvailable || view.PickerNote != "" {
		t.Fatalf("view=%+v", view)
	}
}

func TestFolderDigest_ChipsHiddenWhenMissingAndUnknownRefused(t *testing.T) {
	f := newFolderDigestFixture(t)
	ctx := context.Background()
	if err := os.RemoveAll(filepath.Join(f.home, "Desktop")); err != nil {
		t.Fatal(err)
	}
	view, err := f.service.Current(ctx, "local")
	if err != nil || len(view.Chips) != 2 {
		t.Fatalf("chips=%+v err=%v", view.Chips, err)
	}
	if _, err := f.service.ScanChip(ctx, "local", "desktop"); !errors.Is(err, ErrFolderChipMissing) {
		t.Fatalf("missing chip err=%v", err)
	}
	if _, err := f.service.ScanChip(ctx, "local", "pictures"); !errors.Is(err, ErrFolderChipUnknown) {
		t.Fatalf("unknown chip err=%v", err)
	}
	if _, err := f.service.ScanChip(ctx, "local", "../secrets"); !errors.Is(err, ErrFolderChipUnknown) {
		t.Fatalf("path-like chip err=%v", err)
	}
}

func TestFolderDigest_PausedScansButDoesNotPromiseToRemember(t *testing.T) {
	f := newFolderDigestFixture(t)
	ctx := context.Background()
	state, err := f.relationships.GetState(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	state.Status = StatusPaused
	if _, err := f.relationships.UpdateState(ctx, state, state.StateVersion); err != nil {
		t.Fatal(err)
	}
	offer, err := f.service.ScanChip(ctx, "local", "documents")
	if err != nil {
		t.Fatal(err)
	}
	if offer.Remember {
		t.Fatalf("paused offer promises to remember: %+v", offer)
	}
	view, err := f.service.Current(ctx, "local")
	if err != nil || !view.Paused || view.Offer == nil {
		t.Fatalf("view=%+v err=%v", view, err)
	}
}

func TestFolderDigest_NotHiredIsRefused(t *testing.T) {
	f := newFolderDigestFixture(t)
	ctx := context.Background()
	state, err := f.relationships.GetState(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	state.Status = StatusAwaitingHQ
	state.HQWorkspaceID = ""
	state.HQEntryAgentInstanceID = ""
	if _, err := f.relationships.UpdateState(ctx, state, state.StateVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ScanChip(ctx, "local", "documents"); !errors.Is(err, ErrNeedsHQ) {
		t.Fatalf("needs-HQ scan err=%v", err)
	}
	if _, err := f.service.Current(ctx, "foreign"); err == nil {
		t.Fatal("foreign user resolved the local relationship")
	}
}

func TestFolderDigestStore_RejectsUnknownFieldsAndPaths(t *testing.T) {
	f := newFolderDigestFixture(t)
	ctx := context.Background()
	if _, err := f.service.ScanChip(ctx, "local", "downloads"); err != nil {
		t.Fatal(err)
	}
	sidecar := filepath.Join(f.folder.path, ".ori", folderDigestFileName)
	data, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(data), `"schema_version": 1,`, `"schema_version": 1, "root_path": "/Users/x",`, 1)
	if err := os.WriteFile(sidecar, []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Read(ctx, "local"); !errors.Is(err, ErrKnowledgeCorrupt) {
		t.Fatalf("unknown field err=%v", err)
	}
	if err := os.WriteFile(sidecar, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Read(ctx, "local"); err != nil {
		t.Fatalf("restored document err=%v", err)
	}
	_, err = f.store.Mutate(ctx, "local", func(d *FolderDigestDocument) error {
		d.Offers[0].FolderName = "with/slash"
		return nil
	})
	if err == nil {
		t.Fatal("a name with a separator was written")
	}
	_, err = f.store.Update(ctx, "local", 999, func(*FolderDigestDocument) error { return nil })
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale version err=%v", err)
	}
}

// fakeFolderLinker stands in for the host: it accepts one workspace id per
// offer and records every link it made.
type fakeFolderLinker struct {
	expectedOffer map[string]string // workspace id → offer id it was created for
	links         []FolderLinkRequest
	linked        map[string]string // workspace id → path
}

func (l *fakeFolderLinker) LinkFolder(_ context.Context, req FolderLinkRequest) (FolderLinkResult, error) {
	if l.linked == nil {
		l.linked = map[string]string{}
	}
	offerID, ok := l.expectedOffer[req.WorkspaceID]
	if !ok {
		return FolderLinkResult{}, ErrFolderWorkspaceNotFound
	}
	if offerID != req.OfferID {
		return FolderLinkResult{}, ErrFolderWorkspaceRefused
	}
	if existing, ok := l.linked[req.WorkspaceID]; ok && existing != req.Path {
		return FolderLinkResult{}, ErrFolderWorkspaceRefused
	}
	l.linked[req.WorkspaceID] = req.Path
	l.links = append(l.links, req)
	return FolderLinkResult{Route: "/workspaces/" + req.WorkspaceID, DirectoryID: "dir-" + req.WorkspaceID, FirstTaskSeeded: true}, nil
}

func TestFolderDigest_ResolveLinksOnceAndRefusesOtherWorkspaces(t *testing.T) {
	f := newFolderDigestFixture(t)
	ctx := context.Background()
	linker := &fakeFolderLinker{expectedOffer: map[string]string{"ws-thesis": "offer-1", "ws-other": "offer-99"}}
	f.service.deps.Linker = linker
	f.service.deps.BlueprintAvailable = func(id string) bool { return id == "writing-project" }
	var resolvedOffers []string
	f.service.deps.OnResolved = func(_ context.Context, _ string, offer FolderOffer) FolderLearning {
		resolvedOffers = append(resolvedOffers, offer.ID)
		return FolderLearning{Remembered: true}
	}

	offer, err := f.service.ScanChip(ctx, "local", "documents")
	if err != nil {
		t.Fatal(err)
	}
	if offer.Blueprint != "writing-project" || offer.BlueprintNote != "" || offer.BlueprintLabel != "Writing project" {
		t.Fatalf("blueprint on mixed offer=%+v", offer)
	}
	// A pending offer cannot be resolved: the modal only reports a created
	// workspace after the yes.
	if _, err := f.service.Resolve(ctx, "local", offer.ID, FolderResolveInput{WorkspaceID: "ws-thesis", RequestID: "r0"}); !errors.Is(err, ErrFolderOfferDecided) {
		t.Fatalf("resolve before yes err=%v", err)
	}
	if _, err := f.service.Decide(ctx, "local", offer.ID, FolderDecisionInput{Decision: FolderDecisionYes, Choice: FolderChoiceProject, RequestID: "req-yes"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Resolve(ctx, "local", offer.ID, FolderResolveInput{WorkspaceID: "ws-other", RequestID: "r1"}); !errors.Is(err, ErrFolderWorkspaceRefused) {
		t.Fatalf("foreign workspace err=%v", err)
	}
	if _, err := f.service.Resolve(ctx, "local", offer.ID, FolderResolveInput{WorkspaceID: "missing", RequestID: "r2"}); !errors.Is(err, ErrFolderWorkspaceNotFound) {
		t.Fatalf("missing workspace err=%v", err)
	}
	resolved, err := f.service.Resolve(ctx, "local", offer.ID, FolderResolveInput{WorkspaceID: "ws-thesis", RequestID: "r3"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != FolderOfferResolved || resolved.Outcome == nil || resolved.Outcome.WorkspaceID != "ws-thesis" || resolved.Outcome.Route != "/workspaces/ws-thesis" || resolved.Outcome.Blueprint != "writing-project" {
		t.Fatalf("resolved=%+v outcome=%+v", resolved, resolved.Outcome)
	}
	if len(linker.links) != 1 || linker.links[0].Path != filepath.Join(f.home, "Documents", "Thesis") || linker.links[0].Name != "Thesis" || linker.links[0].Shape != "manuscript" {
		t.Fatalf("links=%+v", linker.links)
	}
	// Replay by request id and by workspace id both return the stored result
	// without linking again.
	for _, requestID := range []string{"r3", "r4"} {
		again, err := f.service.Resolve(ctx, "local", offer.ID, FolderResolveInput{WorkspaceID: "ws-thesis", RequestID: requestID})
		if err != nil || again.Status != FolderOfferResolved || again.Outcome.WorkspaceID != "ws-thesis" {
			t.Fatalf("replay %s: %+v err=%v", requestID, again, err)
		}
	}
	if len(linker.links) != 1 {
		t.Fatalf("replay linked again: %d links", len(linker.links))
	}
	if _, err := f.service.Resolve(ctx, "local", offer.ID, FolderResolveInput{WorkspaceID: "ws-second", RequestID: "r5"}); !errors.Is(err, ErrFolderOfferDecided) {
		t.Fatalf("second workspace on a resolved offer err=%v", err)
	}
	if len(resolvedOffers) != 1 || resolvedOffers[0] != offer.ID {
		t.Fatalf("OnResolved calls=%v", resolvedOffers)
	}
	// The next question waits for a later sitting, then names website.
	f.now = f.now.Add(FolderNextOfferDelay)
	view, err := f.service.Current(ctx, "local")
	if err != nil || view.Offer == nil || view.Offer.Subject.Name != "website" {
		t.Fatalf("next offer=%+v err=%v", view.Offer, err)
	}
}

func TestFolderDigest_BlueprintFallsBackToBlankWithNote(t *testing.T) {
	f := newFolderDigestFixture(t)
	ctx := context.Background()
	f.service.deps.BlueprintAvailable = func(string) bool { return false }
	offer, err := f.service.ScanChip(ctx, "local", "documents")
	if err != nil {
		t.Fatal(err)
	}
	if offer.Blueprint != "" || offer.BlueprintLabel != "Writing project" || offer.BlueprintNote != "The Writing project blueprint is not installed, so this starts as a blank workspace." {
		t.Fatalf("fallback=%+v", offer)
	}
	// A dump offer has no blueprint at all.
	dump, err := f.service.ScanChip(ctx, "local", "downloads")
	if err != nil {
		t.Fatal(err)
	}
	if dump.Blueprint != "" || dump.BlueprintNote != "" || dump.BlueprintLabel != "" {
		t.Fatalf("dump blueprint=%+v", dump)
	}
}

func TestFolderFirstTask_PerShape(t *testing.T) {
	cases := map[string]string{
		"code":       "List the open TODOs and the last thing changed",
		"audio":      "Summarize the session's tracks and the most recent edits",
		"manuscript": "Summarize the current draft and its open sections",
		"corpus":     "Build the sources index from the documents already in this folder: one line per document with a citation and a one-paragraph summary",
		"notes":      "Tell me what is in this folder and what looks most active",
		"":           "Tell me what is in this folder and what looks most active",
	}
	for shape, want := range cases {
		description, details := FolderFirstTask(folderdigestShape(shape))
		if description != want || !strings.Contains(details, "workspace_directory_read") {
			t.Errorf("%q: %q / %q", shape, description, details)
		}
	}
}

// fakeFolderTidier records tidy requests and answers like the host runner:
// a folder already owned opens the existing workspace, anything else gets a
// fresh one.
type fakeFolderTidier struct {
	owned  map[string]string // path → workspace id already tidying it
	calls  []FolderTidyRequest
	fail   error
	nextID int
}

func (f *fakeFolderTidier) TidyFolder(_ context.Context, req FolderTidyRequest) (FolderTidyResult, error) {
	f.calls = append(f.calls, req)
	if f.fail != nil {
		return FolderTidyResult{}, f.fail
	}
	if id, ok := f.owned[req.Path]; ok {
		return FolderTidyResult{WorkspaceID: id, Route: "/workspaces/" + id + "?panel=file-janitor", Existing: true, Note: "Another File Janitor already tidies this folder, so I opened it."}, nil
	}
	f.nextID++
	id := fmt.Sprintf("janitor-%d", f.nextID)
	if f.owned == nil {
		f.owned = map[string]string{}
	}
	f.owned[req.Path] = id
	return FolderTidyResult{WorkspaceID: id, Route: "/workspaces/" + id + "?panel=file-janitor&batch_id=batch-1", BatchID: "batch-1"}, nil
}

func TestFolderDigest_TidyRunsTheEngineAndResolvesInOneRequest(t *testing.T) {
	f := newFolderDigestFixture(t)
	ctx := context.Background()
	tidier := &fakeFolderTidier{}
	f.service.deps.Tidier = tidier

	offer, err := f.service.ScanChip(ctx, "local", "downloads")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := f.service.Decide(ctx, "local", offer.ID, FolderDecisionInput{Decision: FolderDecisionYes, RequestID: "req-tidy"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != FolderOfferResolved || resolved.Outcome == nil || resolved.Outcome.Kind != FolderChoiceTidy ||
		resolved.Outcome.WorkspaceID != "janitor-1" || !strings.Contains(resolved.Outcome.Route, "batch_id=batch-1") {
		t.Fatalf("resolved=%+v outcome=%+v", resolved, resolved.Outcome)
	}
	if len(tidier.calls) != 1 || tidier.calls[0].Path != filepath.Join(f.home, "Downloads") || tidier.calls[0].Name != "Downloads" || tidier.calls[0].RequestID != "req-tidy" {
		t.Fatalf("tidy calls=%+v", tidier.calls)
	}
	// A retried click returns the same workspace without a second setup.
	replay, err := f.service.Decide(ctx, "local", offer.ID, FolderDecisionInput{Decision: FolderDecisionYes, RequestID: "req-tidy"})
	if err != nil || replay.Outcome == nil || replay.Outcome.WorkspaceID != "janitor-1" || len(tidier.calls) != 1 {
		t.Fatalf("replay=%+v err=%v calls=%d", replay, err, len(tidier.calls))
	}

	// A second tidy on the same folder (a new offer after a rescan of the
	// same root) opens the existing workspace.
	f.now = f.now.Add(FolderNextOfferDelay)
	again, err := f.service.ScanChip(ctx, "local", "downloads")
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.service.Decide(ctx, "local", again.ID, FolderDecisionInput{Decision: FolderDecisionYes, RequestID: "req-tidy-2"})
	if err != nil || second.Outcome == nil || second.Outcome.WorkspaceID != "janitor-1" || second.Outcome.Note == "" {
		t.Fatalf("second tidy=%+v err=%v", second, err)
	}
	if len(tidier.calls) != 2 {
		t.Fatalf("calls=%d", len(tidier.calls))
	}
}

func TestFolderDigest_TidyFailureLeavesTheOfferPending(t *testing.T) {
	f := newFolderDigestFixture(t)
	ctx := context.Background()
	tidier := &fakeFolderTidier{fail: ErrFolderOutcomeUnavailable}
	f.service.deps.Tidier = tidier
	offer, err := f.service.ScanChip(ctx, "local", "downloads")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Decide(ctx, "local", offer.ID, FolderDecisionInput{Decision: FolderDecisionYes, RequestID: "req"}); !errors.Is(err, ErrFolderOutcomeUnavailable) {
		t.Fatalf("err=%v", err)
	}
	view, _ := f.service.Current(ctx, "local")
	if view.Offer == nil || view.Offer.ID != offer.ID || view.Offer.Status != FolderOfferPending {
		t.Fatalf("offer after failed tidy=%+v", view.Offer)
	}
	// The mixed offer's tidy branch sends the named project back to the queue.
	tidier.fail = nil
	mixed, err := f.service.ScanChip(ctx, "local", "documents")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := f.service.Decide(ctx, "local", mixed.ID, FolderDecisionInput{Decision: FolderDecisionYes, Choice: FolderChoiceTidy, RequestID: "req-mixed"})
	if err != nil || resolved.Status != FolderOfferResolved || !resolved.Subject.IsRoot || resolved.Outcome.Kind != FolderChoiceTidy {
		t.Fatalf("mixed tidy=%+v err=%v", resolved, err)
	}
	if tidier.calls[len(tidier.calls)-1].Path != filepath.Join(f.home, "Documents") {
		t.Fatalf("mixed tidy path=%s", tidier.calls[len(tidier.calls)-1].Path)
	}
	f.now = f.now.Add(FolderNextOfferDelay)
	next, _ := f.service.Current(ctx, "local")
	if next.Offer == nil || next.Offer.Subject.Name != "Thesis" || next.Offer.Verdict != "project" {
		t.Fatalf("next offer after tidy=%+v", next.Offer)
	}
}

type fakeFolderPicker struct {
	path   string
	chosen bool
}

func (fakeFolderPicker) Available() bool { return true }

func (p fakeFolderPicker) Choose(context.Context, string) (string, bool, error) {
	return p.path, p.chosen, nil
}
