package projectlibrary

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// unreadableSong is a project file that gives no facts: not a project of its
// format.
const unreadableSong = "<Ableton MajorVersion=\"5\">\n"

func TestSongNotRead_ARecordedRefusalLastsUntilTheFileChanges(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Songs")
	saved := time.Date(2026, time.August, 1, 10, 0, 0, 0, time.UTC)
	path := writeSong(t, dir, "Song", "Song.rpp", []byte(unreadableSong), saved)
	r, scope, _, root := connectedRootAt(t, dir)
	opens := &openCounter{}
	r.factsOpened = opens.hook

	scanRoot(t, r, scope, root.ID, "first")
	if got := opens.take(); !slices.Equal(got, []string{filepath.Join("Song", "Song.rpp")}) {
		t.Fatalf("first scan opened %v", got)
	}
	if facts := factsAt(t, r, scope, root.ID, "Song"); facts == nil || facts.NotRead != NotReadUnreadable {
		t.Fatalf("first scan: %+v", facts)
	}
	// Unchanged: never opened again, however many scans.
	for i := 0; i < 2; i++ {
		scanRoot(t, r, scope, root.ID, "again-"+string(rune('a'+i)))
		if got := opens.take(); len(got) != 0 {
			t.Fatalf("an unchanged refused file was opened again: %v", got)
		}
	}

	// The person fixes the project and saves it: the next scan reads it, and the
	// refusal is gone.
	if err := os.WriteFile(path, []byte(rppProject("140", nil, [][][2]string{{{"0", "60"}}})), 0o600); err != nil {
		t.Fatal(err)
	}
	setSaved(t, path, saved.Add(24*time.Hour))
	scanRoot(t, r, scope, root.ID, "fixed")
	facts := factsAt(t, r, scope, root.ID, "Song")
	if facts == nil || facts.NotRead != "" || facts.TempoBPM != 140 || facts.TrackCount != 1 {
		t.Fatalf("after the fix: %+v", facts)
	}
}

// An older build kept an EMPTY record for a file that gave no facts. Under a
// consent that covers names such a record is read once more; if the file is still
// unreadable it becomes a recorded refusal, so it is not read again every scan.
func TestSongNotRead_ALegacyEmptyRecordIsReplacedAndNotReadForever(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Songs")
	saved := time.Date(2026, time.August, 1, 10, 0, 0, 0, time.UTC)
	writeSong(t, dir, "Song", "Song.rpp", []byte(unreadableSong), saved)
	r, scope, file, root := connectedRootAt(t, dir)
	opens := &openCounter{}
	r.factsOpened = opens.hook
	scanRoot(t, r, scope, root.ID, "first")
	opens.take()

	// Rewrite the stored record the way an older build left it: empty, no reason.
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.library.mutate(scope, doc.Revision, operation{key: "legacy-empty", action: "fixture", digest: "legacy-empty"}, func(current *Document) (string, error) {
		for i := range current.Entries {
			for j := range current.Entries[i].Observations {
				if o := &current.Entries[i].Observations[j]; o.Facts != nil {
					o.Facts.NotRead = ""
				}
			}
		}
		return "legacy", nil
	}); err != nil {
		t.Fatal(err)
	}
	if facts := factsAt(t, r, scope, root.ID, "Song"); facts == nil || facts.NotRead != "" || !facts.empty() {
		t.Fatalf("setup: %+v", facts)
	}

	// Numbers only: the legacy record stays, and is not read again.
	scanRoot(t, r, scope, root.ID, "numbers")
	if got := opens.take(); len(got) != 0 {
		t.Fatalf("a legacy record was read again under numbers only: %v", got)
	}

	// Names: read once more, replaced by the reason, then settled.
	grantNames(t, file, scope)
	scanRoot(t, r, scope, root.ID, "names")
	if got := opens.take(); len(got) != 1 {
		t.Fatalf("the upgrade opened %v, want the one file", got)
	}
	if facts := factsAt(t, r, scope, root.ID, "Song"); facts == nil || facts.NotRead != NotReadUnreadable || facts.Names != nil {
		t.Fatalf("the legacy record was not replaced by the reason: %+v", facts)
	}
	for i := 0; i < 2; i++ {
		scanRoot(t, r, scope, root.ID, "settled-"+string(rune('a'+i)))
		if got := opens.take(); len(got) != 0 {
			t.Fatalf("scan %d after the replacement opened %v: a recorded refusal must not be read again", i, got)
		}
	}
}

func TestSongNotRead_UnderANamesConsentAnUnreadableFileRecordsNoNames(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Songs")
	saved := time.Date(2026, time.August, 1, 10, 0, 0, 0, time.UTC)
	writeSong(t, dir, "Good", "Good.rpp", []byte(namedProject("92", nil, []namedTrack{{lines: []string{nameLine("Kick")}}})), saved)
	writeSong(t, dir, "Bad", "Song.rpp", []byte(unreadableSong), saved)
	r, scope, file, root := connectedRootAt(t, dir)
	grantNames(t, file, scope)
	scanRoot(t, r, scope, root.ID, "names")

	bad := factsAt(t, r, scope, root.ID, "Bad")
	if bad == nil || bad.NotRead != NotReadUnreadable || bad.Names != nil || !bad.empty() {
		t.Fatalf("an unreadable file under names: %+v", bad)
	}
	good := factsAt(t, r, scope, root.ID, "Good")
	if good == nil || good.NotRead != "" || good.Names == nil || len(good.Names.Tracks) != 1 {
		t.Fatalf("the readable file: %+v", good)
	}
}

// The progress a caller shows counts the songs that gave facts as read and every
// other target (recorded as not read, or not reached) as skipped, so "Learned K"
// is never inflated by a file that could not be read.
func TestSongNotRead_ProgressCountsOnlyTheSongsThatGaveFacts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Songs")
	saved := time.Date(2026, time.August, 1, 10, 0, 0, 0, time.UTC)
	writeSong(t, dir, "Good", "Good.rpp", []byte(rppProject("92", nil, [][][2]string{{{"0", "60"}}})), saved)
	writeSong(t, dir, "Bad", "Song.rpp", []byte(unreadableSong), saved)
	huge := writeSong(t, dir, "Huge", "Song.rpp", []byte(rppProject("92", nil, nil)), saved)
	if err := os.Truncate(huge, DefaultSongFactsLimits.MaxBytes+1); err != nil {
		t.Fatal(err)
	}
	setSaved(t, huge, saved)
	r, scope, _, root := connectedRootAt(t, dir)
	log := &progressLog{}
	scanRootWith(t, WithSongFactsProgress(context.Background(), log.report), r, scope, root.ID, "progress")

	reports := log.all()
	if len(reports) == 0 {
		t.Fatal("no progress was reported")
	}
	last := reports[len(reports)-1]
	if !last.Finished || last.Targets != 3 || last.Read != 1 || last.Skipped != 2 || last.Done != 3 {
		t.Fatalf("finished report = %+v, want 3 targets, 1 read, 2 skipped", last)
	}
}
