package projectlibrary

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// progressLog collects the reports a pass makes.
type progressLog struct {
	mu      sync.Mutex
	reports []SongFactsProgress
}

func (p *progressLog) report(r SongFactsProgress) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reports = append(p.reports, r)
}

func (p *progressLog) all() []SongFactsProgress {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]SongFactsProgress(nil), p.reports...)
}

// scanRootWith is scanRoot with the caller's context, so a test can carry a
// progress callback into the scan.
func scanRootWith(t *testing.T, ctx context.Context, r *Roots, scope Scope, rootID, key string) Scan {
	t.Helper()
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := r.ReviewScan(scope, rootID, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	result, replay, err := r.CommitScan(ctx, scope, rootID, review.Token, key)
	if err != nil || replay {
		t.Fatalf("scan %s: %+v replay=%v err=%v", key, result, replay, err)
	}
	return result
}

func TestSongFactsProgress_ReportsEachSongThenOneFinishedReport(t *testing.T) {
	r, scope, file, tree, root := connectedMusicRoot(t)
	factsMusicTree(t, tree)
	grantSongDetails(t, file, scope)
	log := &progressLog{}
	ctx := WithSongFactsProgress(context.Background(), log.report)

	scanRootWith(t, ctx, r, scope, root.ID, "progress-scan")

	reports := log.all()
	if len(reports) != 3 {
		t.Fatalf("reports = %+v, want one per song opened and one finished", reports)
	}
	// The newest-saved song is read first; each report names the song's folder.
	for i, want := range []string{"Single", "Alternates"} {
		got := reports[i]
		if got.Song != want || got.Finished || got.Targets != 2 || got.Done != i || got.Read != i {
			t.Fatalf("report %d = %+v, want song %q, 2 targets, %d done", i, got, want, i)
		}
	}
	last := reports[2]
	if !last.Finished || last.Song != "" || last.Targets != 2 || last.Done != 2 || last.Read != 2 || last.Skipped != 0 || last.Stopped != "" {
		t.Fatalf("finished report = %+v", last)
	}
	for _, report := range reports {
		if strings.ContainsAny(report.Song, `/\`) {
			t.Fatalf("a report named a path: %q", report.Song)
		}
	}
}

func TestSongFactsProgress_ASwitchedOffHomeReportsNothingAndOpensNoFile(t *testing.T) {
	r, scope, file, tree, root := connectedMusicRoot(t)
	factsMusicTree(t, tree)
	grantSongDetails(t, file, scope)
	switchSongDetailsOff(t, file, scope)
	opens := &openCounter{}
	r.factsOpened = opens.hook
	log := &progressLog{}

	scanRootWith(t, WithSongFactsProgress(context.Background(), log.report), r, scope, root.ID, "off-scan")

	if reports := log.all(); len(reports) != 0 {
		t.Fatalf("a switched-off Home reported progress: %+v", reports)
	}
	if got := opens.take(); len(got) != 0 {
		t.Fatalf("a switched-off Home opened %v", got)
	}
}

func TestSongFactsProgress_AHomeWithoutConsentReportsNothingAndOpensNoFile(t *testing.T) {
	r, scope, _, tree, root := connectedMusicRoot(t)
	factsMusicTree(t, tree)
	opens := &openCounter{}
	r.factsOpened = opens.hook
	log := &progressLog{}

	scanRootWith(t, WithSongFactsProgress(context.Background(), log.report), r, scope, root.ID, "none-scan")

	if reports := log.all(); len(reports) != 0 {
		t.Fatalf("a Home with no consent reported progress: %+v", reports)
	}
	if got := opens.take(); len(got) != 0 {
		t.Fatalf("a Home with no consent opened %v", got)
	}
}

func TestSongFactsProgress_NoCallbackIsTheOldBehaviour(t *testing.T) {
	r, scope, file, tree, root := connectedMusicRoot(t)
	factsMusicTree(t, tree)
	grantSongDetails(t, file, scope)
	scanRootWith(t, context.Background(), r, scope, root.ID, "quiet-scan")
	if facts := factsAt(t, r, scope, root.ID, "Single"); facts == nil || facts.TrackCount != 2 {
		t.Fatalf("Single facts without a callback = %+v", facts)
	}
}

func TestSongFactsName(t *testing.T) {
	cases := []struct {
		relative, file, want string
	}{
		{"Albums/Album-1", "Song.rpp", "Album-1"},
		{"Single", "Song.rpp", "Single"},
		{"", "Loose Idea.RPP", "Loose Idea"},
		{".", "Loose Idea.rpp", "Loose Idea"},
	}
	for _, tc := range cases {
		target := songFactsTarget{Relative: tc.relative, File: FactsSource{File: tc.file}}
		if got := songFactsName(target); got != tc.want {
			t.Errorf("songFactsName(%q, %q) = %q, want %q", tc.relative, tc.file, got, tc.want)
		}
	}
}
