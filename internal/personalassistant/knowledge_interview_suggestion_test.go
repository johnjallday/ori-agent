package personalassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// A real scan's stored offer words the suggestion; reading it changes nothing.
func TestInterviewSuggestion_FromARealScan(t *testing.T) {
	f := newFolderDigestFixture(t)
	ctx := context.Background()

	scanned, err := f.service.ScanChip(ctx, "local", "documents")
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	offer, err := f.service.StoredOffer(ctx, "local", scanned.ID)
	if err != nil {
		t.Fatal(err)
	}
	suggestion, ok := InterviewSuggestionFromOffer(offer)
	if !ok || suggestion.Text != "Thesis, a LaTeX manuscript" || suggestion.Folder != "Thesis" {
		t.Fatalf("suggestion = %+v ok=%v", suggestion, ok)
	}
	// The folder's other two projects follow; its loose files are not a project.
	if len(suggestion.Alternates) != 2 || suggestion.Alternates[0].Text != "website, a Node.js package" ||
		suggestion.Alternates[1].Text != "Album, a REAPER session" || suggestion.Alternates[1].Folder != "Album" {
		t.Fatalf("alternates = %+v", suggestion.Alternates)
	}
	after, err := f.store.Read(ctx, "local")
	if err != nil || after.Version != before.Version {
		t.Fatalf("StoredOffer wrote: version %d -> %d err=%v", before.Version, after.Version, err)
	}

	dump, err := f.service.ScanChip(ctx, "local", "downloads")
	if err != nil {
		t.Fatal(err)
	}
	offer, err = f.service.StoredOffer(ctx, "local", dump.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := InterviewSuggestionFromOffer(offer); ok {
		t.Fatalf("a dump proposed %+v", got)
	}

	if _, err := f.service.StoredOffer(ctx, "local", "missing"); !errors.Is(err, ErrFolderOfferNotFound) {
		t.Fatalf("unknown offer err = %v", err)
	}
	if _, err := f.service.StoredOffer(ctx, "local", "  "); !errors.Is(err, ErrValidation) {
		t.Fatalf("blank offer id err = %v", err)
	}
}

// The picker and file modes through the real scan, with only the native dialog
// replaced: a picked project names itself, and a file picked straight out of a
// container folder does not turn that container into the answer.
func TestInterviewSuggestion_FromPickedFoldersAndFiles(t *testing.T) {
	ctx := context.Background()
	suggest := func(t *testing.T, f *folderDigestFixture, scan func() (*FolderOfferView, error)) (InterviewSuggestion, bool) {
		t.Helper()
		view, err := scan()
		if err != nil || view == nil {
			t.Fatalf("scan: view=%+v err=%v", view, err)
		}
		offer, err := f.service.StoredOffer(ctx, "local", view.ID)
		if err != nil {
			t.Fatal(err)
		}
		return InterviewSuggestionFromOffer(offer)
	}

	t.Run("a picked project folder", func(t *testing.T) {
		f := newFolderDigestFixture(t)
		f.service.deps.Picker = fakeFolderPicker{path: filepath.Join(f.home, "Documents", "Thesis"), chosen: true}
		got, ok := suggest(t, f, func() (*FolderOfferView, error) { return f.service.ScanPicked(ctx, "local") })
		if !ok || got.Text != "Thesis, a LaTeX manuscript" || got.Folder != "Thesis" {
			t.Fatalf("suggestion = %+v ok=%v", got, ok)
		}
	})
	t.Run("a file picked inside a project", func(t *testing.T) {
		f := newFolderDigestFixture(t)
		f.service.deps.Picker = fakeFolderPicker{path: filepath.Join(f.home, "Documents", "Thesis", "main.tex"), chosen: true}
		got, ok := suggest(t, f, func() (*FolderOfferView, error) { return f.service.ScanPickedFile(ctx, "local") })
		if !ok || got.Folder != "Thesis" || strings.Contains(got.Text, "main.tex") {
			t.Fatalf("suggestion = %+v ok=%v", got, ok)
		}
	})
	t.Run("a file picked straight out of a container folder", func(t *testing.T) {
		f := newFolderDigestFixture(t)
		picked := filepath.Join(f.home, "Desktop", "paper.tex")
		if err := os.WriteFile(picked, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		f.service.deps.Picker = fakeFolderPicker{path: picked, chosen: true}
		view, err := f.service.ScanPickedFile(ctx, "local")
		if err != nil || view == nil || view.Verdict != "project" || view.Subject.Name != "Desktop" {
			t.Fatalf("the scan should name Desktop as the project: %+v err=%v", view, err)
		}
		offer, err := f.service.StoredOffer(ctx, "local", view.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := InterviewSuggestionFromOffer(offer); ok {
			t.Fatalf("Desktop was proposed as the answer: %+v", got)
		}
	})
	t.Run("a cancelled dialog scans nothing", func(t *testing.T) {
		f := newFolderDigestFixture(t)
		f.service.deps.Picker = fakeFolderPicker{}
		if view, err := f.service.ScanPicked(ctx, "local"); err != nil || view != nil {
			t.Fatalf("view=%+v err=%v", view, err)
		}
		if offers, err := f.service.WaitingFolderOffers(ctx, "local"); err != nil || len(offers) != 0 {
			t.Fatalf("a cancelled dialog recorded an offer: %+v err=%v", offers, err)
		}
	})
}

// bidiOverride is U+202E, a character the memory validator refuses. It is built
// from its code point so no invisible character sits in this file.
const bidiOverride = string(rune(0x202e))

// interviewFolderFixture is an interview over the same Personal HQ as a folder
// digest, with a lifecycle service that can approve a project fact.
type interviewFolderFixture struct {
	*folderDigestFixture
	knowledge *KnowledgeStore
	learning  *KnowledgeLearningService
	interview *KnowledgeInterviewService
	authority *switchableFolderAuthority
}

// switchableFolderAuthority revalidates folder_scan facts until gone is set,
// the way a folder that was moved, or whose workspace was deleted, stops doing.
type switchableFolderAuthority struct{ gone bool }

func (a *switchableFolderAuthority) Revalidate(_ context.Context, _ KnowledgeBinding, item KnowledgeItem) error {
	if a.gone || item.SourceKind != FolderScanSourceKind {
		return ErrRepairNeeded
	}
	return nil
}

func newInterviewFolderFixture(t *testing.T) *interviewFolderFixture {
	t.Helper()
	f := newFolderDigestFixture(t)
	knowledge := NewKnowledgeStore(f.resolver(), f.folder)
	authority := &switchableFolderAuthority{}
	learning := NewKnowledgeLifecycleService(knowledge, f.memory, authority)
	interview := NewKnowledgeInterviewService(knowledge)
	interview.SetFolderOffers(f.service)
	interview.SetCanonicalSavers(learning, f.profileStore)
	return &interviewFolderFixture{
		folderDigestFixture: f, knowledge: knowledge, interview: interview,
		learning: learning, authority: authority,
	}
}

// versions reads both sidecars' versions, to prove a read wrote nothing.
func (f *interviewFolderFixture) versions(t *testing.T) [2]int64 {
	t.Helper()
	digest, err := f.store.Read(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	knowledge, err := f.knowledge.Read(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	return [2]int64{digest.Version, knowledge.Version}
}

// rememberSubject approves the project fact a yes on Home would save for the
// offer's subject.
func (f *interviewFolderFixture) rememberSubject(t *testing.T, offerID string) {
	t.Helper()
	offer, err := f.service.StoredOffer(context.Background(), "local", offerID)
	if err != nil {
		t.Fatal(err)
	}
	f.rememberCandidate(t, offer, offer.Subject)
}

// rememberCandidate approves the project fact for one candidate of an offer,
// as a later yes on Home about that candidate would.
func (f *interviewFolderFixture) rememberCandidate(t *testing.T, offer FolderOffer, candidate FolderCandidateRecord) {
	t.Helper()
	offer.ID = "resolved-" + candidate.Name
	offer.Subject = candidate
	offer.Outcome = &FolderOutcome{Kind: FolderChoiceProject, WorkspaceID: "ws-" + candidate.Name}
	if learned := NewFolderScanProducer(f.learning).LearnFromOffer(context.Background(), "local", offer); !learned.Remembered {
		t.Fatalf("project fact for %s was not approved: %+v", candidate.Name, learned)
	}
}

// projectFact returns the approved project fact, for a lifecycle action on it.
func (f *interviewFolderFixture) projectFact(t *testing.T) KnowledgeItem {
	t.Helper()
	doc, err := f.knowledge.Read(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range doc.Items {
		if item.Category == "projects" && item.State == KnowledgeApproved {
			return item
		}
	}
	t.Fatal("no approved project fact")
	return KnowledgeItem{}
}

type failingFolderOffers struct{ calls int }

func (r *failingFolderOffers) WaitingFolderOffers(context.Context, string) ([]FolderOffer, error) {
	r.calls++
	return nil, errors.New("folder digest unreadable")
}

func TestInterviewFolderSnapshot_PrefillsFromAnOfferStillWaiting(t *testing.T) {
	f := newInterviewFolderFixture(t)
	ctx := context.Background()

	empty, err := f.interview.FolderSnapshot(ctx, "local")
	if err != nil || empty.Suggestion != nil || empty.RememberedProject != "" {
		t.Fatalf("nothing shown yet: %+v err=%v", empty, err)
	}

	scanned, err := f.service.ScanChip(ctx, "local", "documents")
	if err != nil {
		t.Fatal(err)
	}
	before := f.versions(t)
	pending, err := f.interview.FolderSnapshot(ctx, "local")
	if err != nil || pending.Suggestion == nil || pending.Suggestion.Text != "Thesis, a LaTeX manuscript" ||
		len(pending.Suggestion.Alternates) != 2 || pending.RememberedProject != "" {
		t.Fatalf("pending project offer: %+v err=%v", pending, err)
	}
	if after := f.versions(t); after != before {
		t.Fatalf("the snapshot wrote: %v -> %v", before, after)
	}

	// Set aside for later on Home: still proposed, and still without a scan.
	if _, err := f.service.Decide(ctx, "local", scanned.ID, FolderDecisionInput{Decision: FolderDecisionLater, RequestID: "req-later"}); err != nil {
		t.Fatal(err)
	}
	before = f.versions(t)
	later, err := f.interview.FolderSnapshot(ctx, "local")
	if err != nil || later.Suggestion == nil || later.Suggestion.Text != "Thesis, a LaTeX manuscript" {
		t.Fatalf("later offer: %+v err=%v", later, err)
	}
	if after := f.versions(t); after != before {
		t.Fatalf("the snapshot wrote: %v -> %v", before, after)
	}
	// The read must not bring the offer back the way Home's Current does.
	f.now = f.now.Add(8 * 24 * time.Hour)
	if _, err := f.interview.FolderSnapshot(ctx, "local"); err != nil {
		t.Fatal(err)
	}
	if stored, _ := f.service.StoredOffer(ctx, "local", scanned.ID); stored.Status != FolderOfferLater {
		t.Fatalf("the snapshot promoted a due offer: %s", stored.Status)
	}

	// A newer dump is the pending offer; the project set aside is still the answer.
	if _, err := f.service.ScanChip(ctx, "local", "downloads"); err != nil {
		t.Fatal(err)
	}
	behindDump, err := f.interview.FolderSnapshot(ctx, "local")
	if err != nil || behindDump.Suggestion == nil || behindDump.Suggestion.Folder != "Thesis" {
		t.Fatalf("project behind a dump: %+v err=%v", behindDump, err)
	}
}

func TestInterviewFolderSnapshot_NothingToPropose(t *testing.T) {
	ctx := context.Background()
	t.Run("a pending dump offer", func(t *testing.T) {
		f := newInterviewFolderFixture(t)
		if _, err := f.service.ScanChip(ctx, "local", "downloads"); err != nil {
			t.Fatal(err)
		}
		got, err := f.interview.FolderSnapshot(ctx, "local")
		if err != nil || got.Suggestion != nil || got.RememberedProject != "" {
			t.Fatalf("snapshot = %+v err=%v", got, err)
		}
	})
	t.Run("a declined offer", func(t *testing.T) {
		f := newInterviewFolderFixture(t)
		scanned, err := f.service.ScanChip(ctx, "local", "documents")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.service.Decide(ctx, "local", scanned.ID, FolderDecisionInput{Decision: FolderDecisionNo, RequestID: "req-no"}); err != nil {
			t.Fatal(err)
		}
		if got, err := f.interview.FolderSnapshot(ctx, "local"); err != nil || got.Suggestion != nil {
			t.Fatalf("snapshot = %+v err=%v", got, err)
		}
	})
	t.Run("no folder reader", func(t *testing.T) {
		f := newInterviewFolderFixture(t)
		if _, err := f.service.ScanChip(ctx, "local", "documents"); err != nil {
			t.Fatal(err)
		}
		f.interview.SetFolderOffers(nil)
		if got, err := f.interview.FolderSnapshot(ctx, "local"); err != nil || got.Suggestion != nil {
			t.Fatalf("snapshot = %+v err=%v", got, err)
		}
	})
	t.Run("a failing folder reader leaves the rest standing", func(t *testing.T) {
		f := newInterviewFolderFixture(t)
		scanned, err := f.service.ScanChip(ctx, "local", "documents")
		if err != nil {
			t.Fatal(err)
		}
		f.rememberSubject(t, scanned.ID)
		reader := &failingFolderOffers{}
		f.interview.SetFolderOffers(reader)
		got, err := f.interview.FolderSnapshot(ctx, "local")
		if err != nil || got.Suggestion != nil || got.RememberedProject == "" || reader.calls != 1 {
			t.Fatalf("snapshot = %+v err=%v calls=%d", got, err, reader.calls)
		}
	})
}

func TestInterviewFolderSnapshot_RememberedProjectIsNotProposedAgain(t *testing.T) {
	f := newInterviewFolderFixture(t)
	ctx := context.Background()
	scanned, err := f.service.ScanChip(ctx, "local", "documents")
	if err != nil {
		t.Fatal(err)
	}
	f.rememberSubject(t, scanned.ID)

	// The offer about Thesis is still waiting, but Thesis is already remembered:
	// the folder's next project is proposed instead, and Thesis is not among
	// its alternates.
	before := f.versions(t)
	got, err := f.interview.FolderSnapshot(ctx, "local")
	if err != nil || got.RememberedProject != "You are working on a project in the folder Thesis." ||
		got.Suggestion == nil || got.Suggestion.Folder != "website" {
		t.Fatalf("snapshot = %+v err=%v", got, err)
	}
	if len(got.Suggestion.Alternates) != 1 || got.Suggestion.Alternates[0].Folder != "Album" {
		t.Fatalf("alternates = %+v", got.Suggestion.Alternates)
	}
	if after := f.versions(t); after != before {
		t.Fatalf("the snapshot wrote: %v -> %v", before, after)
	}

	// A scan started in the wizard words the same offer the same way.
	offer, err := f.service.StoredOffer(ctx, "local", scanned.ID)
	if err != nil {
		t.Fatal(err)
	}
	suggested, ok, remembered := f.interview.SuggestionFromOffer(ctx, "local", offer)
	if !ok || remembered || suggested.Folder != "website" || len(suggested.Alternates) != 1 {
		t.Fatalf("suggestion = %+v ok=%v remembered=%v", suggested, ok, remembered)
	}
	if after := f.versions(t); after != before {
		t.Fatalf("SuggestionFromOffer wrote: %v -> %v", before, after)
	}

	// Rewording the fact drops its evidence from the new revision. The folder
	// it came from is still known, and the new wording is what is reported.
	fact := f.projectFact(t)
	if _, err := f.learning.EditApproved(ctx, "local", fact.ID, fact.Version, "reword-thesis", "My thesis is the priority"); err != nil {
		t.Fatal(err)
	}
	got, err = f.interview.FolderSnapshot(ctx, "local")
	if err != nil || got.RememberedProject != "My thesis is the priority" || got.Suggestion == nil || got.Suggestion.Folder != "website" {
		t.Fatalf("after rewording: %+v err=%v", got, err)
	}

	// The folder went away: the dossier shows the fact as needing review, so it
	// is no longer claimed as remembered and its project may be proposed again.
	f.authority.gone = true
	got, err = f.interview.FolderSnapshot(ctx, "local")
	if err != nil || got.RememberedProject != "" || got.Suggestion == nil || got.Suggestion.Folder != "Thesis" {
		t.Fatalf("with a stale source: %+v err=%v", got, err)
	}
	f.authority.gone = false

	// A forgotten fact is not "remembered" either.
	fact = f.projectFact(t)
	if _, err := f.learning.ForgetApproved(ctx, "local", fact.ID, fact.Version, "forget-thesis"); err != nil {
		t.Fatal(err)
	}
	if got, err = f.interview.FolderSnapshot(ctx, "local"); err != nil || got.RememberedProject != "" {
		t.Fatalf("a forgotten project is still reported: %+v err=%v", got, err)
	}
}

func TestInterviewFolderSnapshot_EveryProjectRemembered(t *testing.T) {
	f := newInterviewFolderFixture(t)
	ctx := context.Background()
	scanned, err := f.service.ScanChip(ctx, "local", "documents")
	if err != nil {
		t.Fatal(err)
	}
	offer, err := f.service.StoredOffer(ctx, "local", scanned.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Leave one project so the suggestion is built from a single candidate.
	offer.Queue = offer.Queue[:1]
	f.rememberCandidate(t, offer, offer.Subject)
	f.rememberCandidate(t, offer, offer.Queue[0])

	suggested, ok, remembered := f.interview.SuggestionFromOffer(ctx, "local", offer)
	if ok || !remembered {
		t.Fatalf("every project is remembered: %+v ok=%v remembered=%v", suggested, ok, remembered)
	}

	// Without the lifecycle service nothing is claimed and nothing is hidden.
	bare := NewKnowledgeInterviewService(f.knowledge)
	bare.SetFolderOffers(f.service)
	got, err := bare.FolderSnapshot(ctx, "local")
	if err != nil || got.RememberedProject != "" || got.Suggestion == nil || got.Suggestion.Folder != "Thesis" {
		t.Fatalf("unwired snapshot = %+v err=%v", got, err)
	}
}

// Saying no to a newer offer about a project must not let an older offer about
// the same project, set aside for later, bring it back in the interview.
func TestInterviewFolderSnapshot_DeclinedProjectStaysDeclined(t *testing.T) {
	f := newInterviewFolderFixture(t)
	ctx := context.Background()
	first, err := f.service.ScanChip(ctx, "local", "documents")
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.service.ScanChip(ctx, "local", "documents")
	if err != nil {
		t.Fatal(err)
	}
	if stored, _ := f.service.StoredOffer(ctx, "local", first.ID); stored.Status != FolderOfferLater {
		t.Fatalf("the first offer should be set aside: %s", stored.Status)
	}
	if _, err := f.service.Decide(ctx, "local", second.ID, FolderDecisionInput{Decision: FolderDecisionNo, RequestID: "req-no"}); err != nil {
		t.Fatal(err)
	}
	got, err := f.interview.FolderSnapshot(ctx, "local")
	if err != nil || got.Suggestion != nil {
		t.Fatalf("a declined project came back: %+v err=%v", got, err)
	}
}

// A scan started from the interview is an ordinary scan: its offer waits on
// Home, and reading it for the interview answers nothing on the user's behalf.
func TestInterviewFolderFeed_LeavesHomesOfferUsable(t *testing.T) {
	f := newInterviewFolderFixture(t)
	ctx := context.Background()
	outcomes := 0
	f.service.SetOnOutcome(func(context.Context, string, FolderOffer) { outcomes++ })

	scanned, err := f.service.ScanChip(ctx, "local", "documents")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.StoredOffer(ctx, "local", scanned.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.interview.FolderSnapshot(ctx, "local"); err != nil {
		t.Fatal(err)
	}
	if outcomes != 0 {
		t.Fatalf("a scan alone reported %d outcomes; the mission completes on an accepted outcome only", outcomes)
	}
	view, err := f.service.Current(ctx, "local")
	if err != nil || view.Offer == nil || view.Offer.ID != scanned.ID || view.Offer.Status != FolderOfferPending || view.Offer.Decision != "" {
		t.Fatalf("Home's offer after the interview read: %+v err=%v", view.Offer, err)
	}
	// Home can still answer it afterwards.
	decided, err := f.service.Decide(ctx, "local", scanned.ID, FolderDecisionInput{
		Decision: FolderDecisionYes, Choice: FolderChoiceProject, RequestID: "req-yes",
	})
	if err != nil || decided.Status != FolderOfferAwaitingOutcome {
		t.Fatalf("yes on Home after the interview: %+v err=%v", decided, err)
	}
}

func TestInterviewSuggestionFromOffer(t *testing.T) {
	project := func(name, marker string) FolderCandidateRecord {
		return FolderCandidateRecord{Key: strings.Repeat("a", 64), Name: name, Kind: FolderChoiceProject, Marker: marker}
	}
	root := func(candidate FolderCandidateRecord) FolderCandidateRecord {
		candidate.IsRoot = true
		return candidate
	}
	tidy := FolderCandidateRecord{Key: strings.Repeat("b", 64), Name: "Downloads", Kind: FolderChoiceTidy}
	offer := func(verdict folderdigest.Kind, subject FolderCandidateRecord) FolderOffer {
		return FolderOffer{ID: "offer-1", Verdict: string(verdict), Subject: subject, FolderName: "Documents"}
	}
	portfolio := offer(folderdigest.KindProject, project("Albums", ""))
	portfolio.Portfolio = &FolderPortfolioEvidence{Shape: "audio", Projects: 7}

	for _, tc := range []struct {
		name   string
		offer  FolderOffer
		text   string
		folder string
	}{
		{"a marker is named after the folder", offer(folderdigest.KindProject, project("Thesis", "LaTeX manuscript")), "Thesis, a LaTeX manuscript", "Thesis"},
		{"a mixed folder proposes its first project", offer(folderdigest.KindMixed, project("Thesis", "LaTeX manuscript")), "Thesis, a LaTeX manuscript", "Thesis"},
		{"a marker starting with a vowel takes an", offer(folderdigest.KindProject, project("Notes", "Obsidian vault")), "Notes, an Obsidian vault", "Notes"},
		{"an uppercase vowel also takes an", offer(folderdigest.KindProject, project("Set", "Ableton Live set")), "Set, an Ableton Live set", "Set"},
		{"no marker gives the bare name", offer(folderdigest.KindProject, project("Sketches", "")), "Sketches", "Sketches"},
		{"surrounding spaces are dropped", offer(folderdigest.KindProject, project("  Thesis ", " LaTeX manuscript ")), "Thesis, a LaTeX manuscript", "Thesis"},
		{"a dump gives nothing", offer(folderdigest.KindDump, tidy), "", ""},
		// An ambiguous root is stored with a project subject; the verdict rules it out.
		{"an ambiguous folder gives nothing", offer(folderdigest.KindAmbiguous, project("Stuff", "")), "", ""},
		{"an empty folder gives nothing", offer(folderdigest.KindEmpty, tidy), "", ""},
		{"a declined folder gives nothing", offer(folderdigest.KindDeclined, tidy), "", ""},
		{"a project verdict with a tidy subject gives nothing", offer(folderdigest.KindProject, tidy), "", ""},
		{"a portfolio gives nothing", portfolio, "", ""},
		{"a name the validator refuses gives nothing", offer(folderdigest.KindProject, project("Thesis"+bidiOverride, "LaTeX manuscript")), "", ""},
		{"a text over the answer limit gives nothing", offer(folderdigest.KindProject, project(strings.Repeat("n", 250), strings.Repeat("m", 250))), "", ""},
		{"a nameless subject gives nothing", offer(folderdigest.KindProject, project("  ", "LaTeX manuscript")), "", ""},
		// A file picked straight out of Downloads makes Downloads the scan's
		// project; the container is never the answer.
		{"a whole Downloads folder gives nothing", offer(folderdigest.KindProject, root(project("Downloads", ""))), "", ""},
		{"a whole documents folder gives nothing", offer(folderdigest.KindProject, root(project("documents", "git repository"))), "", ""},
		{"a whole Desktop folder gives nothing", offer(folderdigest.KindMixed, root(project("Desktop", ""))), "", ""},
		{"any other root folder is proposed", offer(folderdigest.KindProject, root(project("Novel", ""))), "Novel", "Novel"},
		{"a subfolder that happens to be called Documents is proposed", offer(folderdigest.KindProject, project("Documents", "")), "Documents", "Documents"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := InterviewSuggestionFromOffer(tc.offer)
			if ok != (tc.text != "") {
				t.Fatalf("ok = %v, suggestion = %+v", ok, got)
			}
			if got.Text != tc.text || got.Folder != tc.folder {
				t.Fatalf("suggestion = %+v, want text %q folder %q", got, tc.text, tc.folder)
			}
			if len(got.Alternates) != 0 {
				t.Fatalf("alternates = %+v, want none", got.Alternates)
			}
		})
	}
}

func TestInterviewSuggestionFromOffer_Alternates(t *testing.T) {
	candidate := func(name, marker, kind string) FolderCandidateRecord {
		return FolderCandidateRecord{Key: strings.Repeat("c", 64), Name: name, Kind: kind, Marker: marker}
	}
	texts := func(s InterviewSuggestion) []string {
		out := make([]string, 0, len(s.Alternates))
		for _, alternate := range s.Alternates {
			if len(alternate.Alternates) != 0 {
				t.Fatalf("an alternate carries its own alternates: %+v", alternate)
			}
			out = append(out, alternate.Text+"|"+alternate.Folder)
		}
		return out
	}
	offer := FolderOffer{
		ID: "offer-1", Verdict: string(folderdigest.KindMixed),
		Subject: candidate("Thesis", "LaTeX manuscript", FolderChoiceProject),
		Queue: []FolderCandidateRecord{
			candidate("website", "Node.js package", FolderChoiceProject),
			// The same wording twice is offered once.
			candidate("website", "Node.js package", FolderChoiceProject),
			// A refused name is skipped, not a reason to drop the rest.
			candidate("bad"+bidiOverride+"name", "", FolderChoiceProject),
			candidate("Documents", "", FolderChoiceTidy),
			candidate("Notes", "Obsidian vault", FolderChoiceProject),
			// The subject's own wording is never repeated as an alternate.
			candidate("Thesis", "LaTeX manuscript", FolderChoiceProject),
			candidate("Sketches", "", FolderChoiceProject),
			candidate("Fourth", "git repository", FolderChoiceProject),
		},
	}
	got, ok := InterviewSuggestionFromOffer(offer)
	if !ok || got.Text != "Thesis, a LaTeX manuscript" {
		t.Fatalf("suggestion = %+v ok=%v", got, ok)
	}
	want := []string{"website, a Node.js package|website", "Notes, an Obsidian vault|Notes", "Sketches|Sketches"}
	if strings.Join(texts(got), ";") != strings.Join(want, ";") {
		t.Fatalf("alternates = %v, want %v (at most three projects, in queue order)", texts(got), want)
	}

	// A subject that cannot be proposed gives nothing, whatever the queue holds.
	offer.Subject = candidate("Documents", "", FolderChoiceTidy)
	if got, ok := InterviewSuggestionFromOffer(offer); ok {
		t.Fatalf("a tidy subject proposed %+v", got)
	}
}

func TestInterviewSuggestionExcluding_RememberedProjects(t *testing.T) {
	candidate := func(key byte, name, marker, kind string) FolderCandidateRecord {
		return FolderCandidateRecord{Key: strings.Repeat(string(key), 64), Name: name, Kind: kind, Marker: marker}
	}
	thesis := candidate('a', "Thesis", "LaTeX manuscript", FolderChoiceProject)
	website := candidate('b', "website", "Node.js package", FolderChoiceProject)
	album := candidate('c', "Album", "", FolderChoiceProject)
	loose := candidate('d', "Documents", "", FolderChoiceTidy)
	offer := FolderOffer{
		ID: "offer-1", Verdict: string(folderdigest.KindMixed),
		Subject: thesis, Queue: []FolderCandidateRecord{website, album, loose},
	}
	keys := func(candidates ...FolderCandidateRecord) map[string]bool {
		out := map[string]bool{}
		for _, c := range candidates {
			out[c.Key] = true
		}
		return out
	}
	folders := func(s InterviewSuggestion) string {
		out := []string{s.Folder}
		for _, alternate := range s.Alternates {
			out = append(out, alternate.Folder)
		}
		return strings.Join(out, ",")
	}

	for _, tc := range []struct {
		name       string
		remembered map[string]bool
		want       string
		allKnown   bool
	}{
		{"nothing remembered", nil, "Thesis,website,Album", false},
		{"a remembered alternate is dropped", keys(website), "Thesis,Album", false},
		{"a remembered subject gives way to the next project", keys(thesis), "website,Album", false},
		{"two remembered leave the third", keys(thesis, website), "Album", false},
		{"every project remembered proposes nothing", keys(thesis, website, album), "", true},
		// Loose files are not a project, so they neither stand in nor count.
		{"only the tidy candidate is left", keys(thesis, website, album, loose), "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok, allKnown := interviewSuggestionExcluding(offer, tc.remembered)
			if ok != (tc.want != "") || allKnown != tc.allKnown {
				t.Fatalf("ok=%v allKnown=%v suggestion=%+v", ok, allKnown, got)
			}
			if ok && folders(got) != tc.want {
				t.Fatalf("folders = %s, want %s", folders(got), tc.want)
			}
		})
	}

	// A dump was never a project, so "already remembered" is not the reason.
	dump := FolderOffer{ID: "offer-2", Verdict: string(folderdigest.KindDump), Subject: loose}
	if _, ok, allKnown := interviewSuggestionExcluding(dump, keys(loose)); ok || allKnown {
		t.Fatalf("a dump: ok=%v allKnown=%v", ok, allKnown)
	}
}
