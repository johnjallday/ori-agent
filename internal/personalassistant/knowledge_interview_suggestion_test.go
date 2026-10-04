package personalassistant

import (
	"context"
	"errors"
	"strings"
	"testing"

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

func TestInterviewSuggestionFromOffer(t *testing.T) {
	project := func(name, marker string) FolderCandidateRecord {
		return FolderCandidateRecord{Key: strings.Repeat("a", 64), Name: name, Kind: FolderChoiceProject, Marker: marker}
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
		{"a name the validator refuses gives nothing", offer(folderdigest.KindProject, project("Thesis‮", "LaTeX manuscript")), "", ""},
		{"a text over the answer limit gives nothing", offer(folderdigest.KindProject, project(strings.Repeat("n", 250), strings.Repeat("m", 250))), "", ""},
		{"a nameless subject gives nothing", offer(folderdigest.KindProject, project("  ", "LaTeX manuscript")), "", ""},
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
