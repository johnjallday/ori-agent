package filejanitor

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// mixed206 is a 206-file folder that touches every group the picture draws:
// four confident folders, a sensitive pair, a copy pair, a song, a lone track and
// an unknown type.
func mixed206() []string {
	names := make([]string, 0, 206)
	for i := range 120 {
		names = append(names, fmt.Sprintf("image-%04d.png", i))
	}
	for i := range 60 {
		names = append(names, fmt.Sprintf("doc-%03d.pdf", i))
	}
	for i := range 10 {
		names = append(names, fmt.Sprintf("app-%02d.dmg", i))
	}
	names = append(names, "passport-a.pdf", "passport-b.pdf", "Budget.pdf", "Budget (2).pdf", "song.wav", "song.als", "solo.mp3", "mystery.qqq")
	for len(names) < 206 {
		names = append(names, fmt.Sprintf("archive-%03d.zip", len(names)))
	}
	return names
}

func candidateIDByName(t *testing.T, candidates []JanitorCandidate, name string) string {
	t.Helper()
	for _, candidate := range candidates {
		if candidate.Name == name {
			return candidate.ID
		}
	}
	t.Fatalf("no candidate named %q", name)
	return ""
}

// drawnOnce fails unless every waiting file is in exactly one list, every file
// that could not be filed is in its list, and nothing else is drawn.
func drawnOnce(t *testing.T, layout pictureLayout, candidates []JanitorCandidate) {
	t.Helper()
	seen := map[string]string{}
	add := func(section string, files []PictureFile) {
		for _, file := range files {
			if file.Placement != section {
				t.Fatalf("%s lists %s with placement %q", section, file.Name, file.Placement)
			}
			if previous, dup := seen[file.ID]; dup {
				t.Fatalf("%s is drawn twice: %s and %s", file.Name, previous, section)
			}
			seen[file.ID] = section
		}
	}
	for _, files := range layout.filed {
		add(PictureFiled, files)
	}
	for _, files := range layout.unfiled {
		add(PictureUnfiled, files)
	}
	for _, files := range layout.loose {
		add(PictureLoose, files)
	}
	add(PictureLeftOut, layout.leftOut)
	add(PictureTrash, layout.trash)
	add(PictureCouldNotFile, layout.couldNot)
	want := 0
	for _, candidate := range candidates {
		switch {
		case proposalPending(candidate), candidate.State == CandidateFailed, candidate.State == CandidateStale:
			want++
			if seen[candidate.ID] == "" {
				t.Fatalf("%s (%s) is not drawn anywhere", candidate.Name, candidate.State)
			}
		default:
			if seen[candidate.ID] != "" {
				t.Fatalf("%s (%s) was handled earlier but is drawn in %s", candidate.Name, candidate.State, seen[candidate.ID])
			}
		}
	}
	if len(seen) != want {
		t.Fatalf("drew %d files, want %d", len(seen), want)
	}
}

// R8, A3: every file waiting for a decision is drawn exactly once, whatever the
// person has done, and the counts add up to the number waiting.
func TestReviewPicture_EveryWaitingFileIsDrawnExactlyOnce(t *testing.T) {
	for _, tc := range []struct {
		name  string
		shape func(t *testing.T, candidates []JanitorCandidate, groups ReviewGroups) []ReviewChoiceView
	}{
		{"ticked on open", func(_ *testing.T, _ []JanitorCandidate, groups ReviewGroups) []ReviewChoiceView {
			return defaultReviewChoices(groups)
		}},
		{"nothing ticked", func(*testing.T, []JanitorCandidate, ReviewGroups) []ReviewChoiceView { return nil }},
		{"answers on every kind of row", func(t *testing.T, candidates []JanitorCandidate, groups ReviewGroups) []ReviewChoiceView {
			choices := defaultReviewChoices(groups)
			sensitive := reviewGroupID("sensitive")
			for _, name := range []string{"passport-a.pdf", "passport-b.pdf"} {
				choices = append(choices, ReviewChoiceView{CandidateID: candidateIDByName(t, candidates, name), Operation: OperationMove, Category: CategoryDocuments, SourceGroupID: sensitive})
			}
			choices = append(choices, ReviewChoiceView{CandidateID: candidateIDByName(t, candidates, "solo.mp3"), Operation: OperationTrash})
			return choices[3:] // three confident files left out of their ticked folders
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			batch, candidates := groupFixture(mixed206()...)
			// One file failed and one changed in an earlier attempt, one was kept.
			candidates[121].State, candidates[121].StateReason = CandidateFailed, "The move did not complete."
			candidates[190].State, candidates[190].StateReason = CandidateStale, "The file changed."
			candidates[191].State = CandidateSkipped
			groups := mustGroup(t, batch, candidates)
			view := ReviewDraftView{Revision: "draft", State: "saved", Choices: tc.shape(t, candidates, groups)}

			drawnOnce(t, layoutReviewPicture(groups, view, candidates), candidates)

			picture := buildReviewPicture(groups, view, candidates)
			total := picture.LeftOut.Count + picture.Trash.Count
			for _, folder := range picture.Folders {
				total += folder.Count + folder.UnfiledCount
			}
			for _, row := range picture.Loose {
				if row.Count != row.LooseCount+row.PlacedCount+row.TrashCount {
					t.Fatalf("row %s does not add up: %+v", row.Key, row)
				}
				total += row.LooseCount
			}
			if total != picture.Waiting || picture.Waiting != 203 {
				t.Fatalf("on-screen counts add up to %d, scan has %d waiting (want 203 of 206)", total, picture.Waiting)
			}
			if picture.CouldNotFile.Count != 2 || picture.Counts.Kept != 1 {
				t.Fatalf("earlier attempts: could not file %d, kept %d", picture.CouldNotFile.Count, picture.Counts.Kept)
			}
		})
	}
}

func looseRowOf(t *testing.T, picture ReviewPicture, key string) PictureLooseRow {
	t.Helper()
	for _, row := range picture.Loose {
		if row.Key == key {
			return row
		}
	}
	t.Fatalf("no %q row in %+v", key, picture.Loose)
	return PictureLooseRow{}
}

func names(files []PictureFile) []string {
	out := make([]string, 0, len(files))
	for _, file := range files {
		out = append(out, file.Name)
	}
	return out
}

// R26, R64: possible copies are drawn as pair cards with the file that has no
// ending first, three cards at most, and what is not drawn is counted in "more".
func TestReviewPicture_PossibleCopiesArePairCards(t *testing.T) {
	batch, candidates := groupFixture(
		"Budget-v2.pdf", "Budget (2).pdf", "Budget.pdf", // the original is listed first whatever the order
		"Plan.pdf", "Plan copy.pdf",
		"Memo.pdf", "Memo (2).pdf",
		"Notes.pdf", "Notes (2).pdf", // the fourth family is not drawn
		"alone.pdf",
	)
	groups := mustGroup(t, batch, candidates)
	picture := buildReviewPicture(groups, ReviewDraftView{Revision: "draft", State: "default"}, candidates)
	row := looseRowOf(t, picture, "versions")
	if row.Count != 9 || row.LooseCount != 9 || len(row.Pairs) != PicturePairLimit || len(row.Singles) != 0 || len(row.Bundles) != 0 {
		t.Fatalf("the row: %+v", row)
	}
	// The scan listed Budget-v2.pdf, then Budget (2).pdf, then Budget.pdf: the original
	// moves to the front and the rest keep their scan order.
	if got := names(row.Pairs[0].Files); strings.Join(got, "|") != "Budget.pdf|Budget-v2.pdf|Budget (2).pdf" {
		t.Fatalf("the original first, then the others in scan order, got %v", got)
	}
	if row.Pairs[0].Files[0].Ending != EndingNone || row.Pairs[0].Files[1].Ending != EndingV || row.Pairs[0].Files[2].Ending != EndingNumber {
		t.Fatalf("endings: %+v", row.Pairs[0].Files)
	}
	if row.Pairs[0].Count != 3 || row.Pairs[0].More != 0 || !familyPattern.MatchString(row.Pairs[0].FamilyID) {
		t.Fatalf("pair: %+v", row.Pairs[0])
	}
	// Notes and Notes (2) are the fourth family: not drawn, counted as more.
	if row.More != 2 {
		t.Fatalf("two files are beyond the three cards, got more=%d", row.More)
	}
}

// A copy marked for Trash stays on its pair's card with its own line (R29), its file
// is counted in the Trash box and not in the row, and a card with nothing loose left
// is not drawn.
func TestReviewPicture_ATrashedCopyStaysOnItsCard(t *testing.T) {
	batch, candidates := groupFixture("Budget.pdf", "Budget (2).pdf", "Plan.pdf", "Plan copy.pdf")
	groups := mustGroup(t, batch, candidates)
	id := func(name string) string { return candidateIDByName(t, candidates, name) }
	view := ReviewDraftView{Revision: "draft", State: "saved", Choices: []ReviewChoiceView{
		{CandidateID: id("Budget (2).pdf"), Operation: OperationTrash},
		{CandidateID: id("Plan.pdf"), Operation: OperationTrash},
		{CandidateID: id("Plan copy.pdf"), Operation: OperationTrash},
	}}
	picture := buildReviewPicture(groups, view, candidates)
	row := looseRowOf(t, picture, "versions")
	if row.Count != 4 || row.TrashCount != 3 || row.LooseCount != 1 {
		t.Fatalf("one file is still loose and three are in Trash: %+v", row)
	}
	if len(row.Pairs) != 1 || row.Pairs[0].Count != 2 {
		t.Fatalf("only the Budget card still has a loose file: %+v", row.Pairs)
	}
	byName := map[string]string{}
	for _, file := range row.Pairs[0].Files {
		byName[file.Name] = file.Placement
	}
	if byName["Budget.pdf"] != PictureLoose || byName["Budget (2).pdf"] != PictureTrash {
		t.Fatalf("the copy's line must say it is in Trash while the original stays loose: %v", byName)
	}
	if picture.Trash.Count != 3 {
		t.Fatalf("the Trash box still counts every marked file once: %+v", picture.Trash)
	}
	if row.More != 0 {
		t.Fatalf("everything loose is drawn: more=%d", row.More)
	}
}

// A bundle the person placed in a folder stays drawn, so the choice can be changed
// back from the card; the card reports where each file is, and a bundle whose files
// are all in Trash is left to the Trash box.
func TestReviewPicture_APlacedBundleStaysDrawnAndATrashedOneDoesNot(t *testing.T) {
	batch, candidates := groupFixture("Song.wav", "Song.als", "Take.wav", "Take.als")
	groups := mustGroup(t, batch, candidates)
	id := func(name string) string { return candidateIDByName(t, candidates, name) }
	view := ReviewDraftView{Revision: "draft", State: "saved", Choices: []ReviewChoiceView{
		{CandidateID: id("Song.wav"), Operation: OperationMove, Category: CategoryAudio},
		{CandidateID: id("Song.als"), Operation: OperationMove, Category: CategoryAudio},
		{CandidateID: id("Take.wav"), Operation: OperationTrash},
		{CandidateID: id("Take.als"), Operation: OperationTrash},
	}}
	row := looseRowOf(t, buildReviewPicture(groups, view, candidates), "audio")
	if len(row.Bundles) != 1 || row.Bundles[0].Name != "Song" {
		t.Fatalf("only the placed bundle is drawn: %+v", row.Bundles)
	}
	for _, file := range row.Bundles[0].Files {
		if file.Placement != PictureFiled || file.Category != CategoryAudio || !file.YourChoice {
			t.Fatalf("each file says where it went: %+v", file)
		}
	}
	if row.LooseCount != 0 || row.PlacedCount != 2 || row.TrashCount != 2 || row.More != 0 {
		t.Fatalf("row accounting: %+v", row)
	}
	// The card knows where its whole bundle is, for its menu and its split note.
	if bundle := row.Bundles[0]; bundle.LooseCount != 0 || bundle.PlacedCount != 2 || bundle.PlacedCategory != CategoryAudio || bundle.TrashCount != 0 {
		t.Fatalf("bundle state: %+v", bundle)
	}
}

// R33: a bundle whose files no longer share one destination says how they are split.
func TestReviewPicture_ASplitBundleCountsWhereEachFileIs(t *testing.T) {
	batch, candidates := groupFixture("Song.wav", "Song.als", "Song.mp3", "Song.aiff")
	groups := mustGroup(t, batch, candidates)
	id := func(name string) string { return candidateIDByName(t, candidates, name) }
	view := ReviewDraftView{Revision: "draft", State: "saved", Choices: []ReviewChoiceView{
		{CandidateID: id("Song.wav"), Operation: OperationMove, Category: CategoryAudio},
		{CandidateID: id("Song.als"), Operation: OperationMove, Category: CategoryDocuments},
		{CandidateID: id("Song.mp3"), Operation: OperationTrash},
	}}
	bundle := looseRowOf(t, buildReviewPicture(groups, view, candidates), "audio").Bundles[0]
	if bundle.Count != 4 || bundle.LooseCount != 1 || bundle.PlacedCount != 2 || bundle.TrashCount != 1 || bundle.PlacedCategory != "" {
		t.Fatalf("split bundle: %+v", bundle)
	}
}

// R31: files that share a name apart from the extension are a bundle card; a song
// with no partner is a single beside it; a card shows twelve files and counts the
// rest.
func TestReviewPicture_AudioFilesThatShareANameAreABundle(t *testing.T) {
	extensions := []string{".wav", ".als", ".mp3", ".aiff", ".flac", ".ogg", ".m4a", ".cue", ".band", ".logicx", ".flp", ".ptx", ".aup", ".aup3"}
	files := []string{"solo.mp3", "other.wav"}
	for _, extension := range extensions {
		files = append(files, "Song 130"+extension)
	}
	batch, candidates := groupFixture(files...)
	groups := mustGroup(t, batch, candidates)
	picture := buildReviewPicture(groups, ReviewDraftView{Revision: "draft", State: "default"}, candidates)
	row := looseRowOf(t, picture, "audio")
	if len(row.Bundles) != 1 || len(row.Pairs) != 0 {
		t.Fatalf("one bundle: %+v", row)
	}
	bundle := row.Bundles[0]
	if bundle.Name != "Song 130" || bundle.Count != len(extensions) || len(bundle.Files) != PictureBundleFileLimit || bundle.More != len(extensions)-PictureBundleFileLimit || !bundlePattern.MatchString(bundle.BundleID) {
		t.Fatalf("bundle: %+v", bundle)
	}
	if got := strings.Join(names(row.Singles), "|"); got != "solo.mp3|other.wav" {
		t.Fatalf("files with no partner are singles, got %s", got)
	}
	if want := len(extensions) - PictureBundleFileLimit; row.More != want {
		t.Fatalf("the files beyond the card are counted in more: got %d want %d", row.More, want)
	}
}

// A card with more files than it shows is paged by its own identifier, and the page
// holds every member wherever it is now. A malformed key is refused.
func TestReviewPicture_PairAndBundleListsPageByIdentifier(t *testing.T) {
	batch, candidates := groupFixture("Budget.pdf", "Budget (2).pdf", "Budget copy.pdf", "Song.wav", "Song.als")
	groups := mustGroup(t, batch, candidates)
	id := func(name string) string { return candidateIDByName(t, candidates, name) }
	view := ReviewDraftView{Revision: "draft", State: "saved", Choices: []ReviewChoiceView{
		{CandidateID: id("Budget (2).pdf"), Operation: OperationTrash},
	}}
	layout := layoutReviewPicture(groups, view, candidates)
	row := looseRowOf(t, buildReviewPicture(groups, view, candidates), "versions")
	family, err := layout.pictureList(PictureSectionFamily, row.Pairs[0].FamilyID)
	if err != nil || len(family) != 3 || family[0].Name != "Budget.pdf" {
		t.Fatalf("the whole family, original first: %v %v", names(family), err)
	}
	audio := looseRowOf(t, buildReviewPicture(groups, view, candidates), "audio")
	bundle, err := layout.pictureList(PictureSectionBundle, audio.Bundles[0].BundleID)
	if err != nil || len(bundle) != 2 {
		t.Fatalf("the whole bundle: %v %v", names(bundle), err)
	}
	for _, key := range []string{"", "fam-xyz", "../etc", "bun-" + strings.Repeat("g", 24)} {
		if _, err := layout.pictureList(PictureSectionFamily, key); !errors.Is(err, ErrInvalidAction) {
			t.Errorf("key %q should be refused, got %v", key, err)
		}
	}
	if list, err := layout.pictureList(PictureSectionFamily, "fam-"+strings.Repeat("0", 24)); err != nil || len(list) != 0 {
		t.Errorf("an unknown family is empty, not an error: %v %v", list, err)
	}
}

// R6: a file the person placed in a folder is listed first in that folder's card,
// ahead of the files Ori suggested for it, and the order is otherwise stable.
func TestReviewPicture_AFilePlacedByYouIsListedFirstInItsFolder(t *testing.T) {
	batch, candidates := groupFixture("x.pdf", "y.pdf", "z.pdf", "passport-a.pdf")
	groups := mustGroup(t, batch, candidates)
	id := func(name string) string { return candidateIDByName(t, candidates, name) }
	docs := reviewGroupID("filing-documents")
	view := ReviewDraftView{Revision: "draft", State: "saved", Choices: []ReviewChoiceView{
		{CandidateID: id("x.pdf"), Operation: OperationMove, Category: CategoryDocuments, SourceGroupID: docs},
		{CandidateID: id("y.pdf"), Operation: OperationMove, Category: CategoryDocuments, SourceGroupID: docs},
		{CandidateID: id("z.pdf"), Operation: OperationMove, Category: CategoryDocuments, SourceGroupID: docs},
		{CandidateID: id("passport-a.pdf"), Operation: OperationMove, Category: CategoryDocuments, SourceGroupID: reviewGroupID("sensitive")},
	}}
	picture := buildReviewPicture(groups, view, candidates)
	if len(picture.Folders) != 1 || picture.Folders[0].Count != 4 {
		t.Fatalf("expected one Documents card of four files: %+v", picture.Folders)
	}
	names := []string{}
	for _, file := range picture.Folders[0].Files {
		names = append(names, file.Name)
	}
	if names[0] != "passport-a.pdf" || !picture.Folders[0].Files[0].YourChoice {
		t.Fatalf("the file the person placed must come first and be tagged, got %v", names)
	}
	for _, file := range picture.Folders[0].Files[1:] {
		if file.YourChoice {
			t.Fatalf("a file Ori suggested must not be tagged: %+v", file)
		}
	}
}

// R4 to R6, R13, R24, R25: each placement rule, on a small folder.
func TestReviewPicture_PlacementRules(t *testing.T) {
	batch, candidates := groupFixture("a.png", "b.png", "c.png", "d.png", "x.pdf", "y.pdf", "passport-a.pdf", "passport-b.pdf", "song.wav", "old.dmg", "gone.pdf")
	candidates[9].State, candidates[9].StateReason = CandidateFailed, "The move did not complete."
	candidates[10].State = CandidateStale
	groups := mustGroup(t, batch, candidates)
	id := func(name string) string { return candidateIDByName(t, candidates, name) }
	sensitive := reviewGroupID("sensitive")
	view := ReviewDraftView{Revision: "draft", State: "saved", Choices: []ReviewChoiceView{
		// images: three of four ticked, d.png left out by the person
		{CandidateID: id("a.png"), Operation: OperationMove, Category: CategoryImages, SourceGroupID: reviewGroupID("filing-images")},
		{CandidateID: id("b.png"), Operation: OperationMove, Category: CategoryImages, SourceGroupID: reviewGroupID("filing-images")},
		{CandidateID: id("c.png"), Operation: OperationMove, Category: CategoryImages, SourceGroupID: reviewGroupID("filing-images")},
		// documents: nothing ticked, so the folder is drawn dashed
		// the sensitive pair placed in Documents with one answer
		{CandidateID: id("passport-a.pdf"), Operation: OperationMove, Category: CategoryDocuments, SourceGroupID: sensitive},
		{CandidateID: id("passport-b.pdf"), Operation: OperationMove, Category: CategoryDocuments, SourceGroupID: sensitive},
		// the song marked for Trash, one file at a time
		{CandidateID: id("song.wav"), Operation: OperationTrash},
	}}
	picture := buildReviewPicture(groups, view, candidates)

	if len(picture.Folders) != 2 || picture.Folders[0].Category != CategoryDocuments || picture.Folders[1].Category != CategoryImages {
		t.Fatalf("folders must follow the category registry order: %+v", picture.Folders)
	}
	documents, images := picture.Folders[0], picture.Folders[1]
	if documents.Count != 2 || documents.UnfiledCount != 2 || documents.GroupState != "unticked" || documents.Destination != "Filed/Documents" {
		t.Fatalf("documents is two placed files plus an unticked (dashed) pair of its own: %+v", documents)
	}
	for _, file := range documents.Files {
		if !file.YourChoice || file.Category != CategoryDocuments {
			t.Fatalf("a file the person placed must be tagged: %+v", file)
		}
	}
	if images.Count != 3 || images.GroupState != "partial" || images.UnfiledCount != 0 {
		t.Fatalf("images: three ticked, one left out: %+v", images)
	}
	if picture.LeftOut.Count != 1 || picture.LeftOut.Files[0].Name != "d.png" {
		t.Fatalf("a file taken out of a ticked folder is left out: %+v", picture.LeftOut)
	}
	if picture.Trash.Count != 1 || picture.Trash.Files[0].Name != "song.wav" {
		t.Fatalf("trash: %+v", picture.Trash)
	}
	rows := map[string]PictureLooseRow{}
	for _, row := range picture.Loose {
		rows[row.Key] = row
	}
	if row := rows["sensitive"]; row.Count != 2 || row.PlacedCount != 2 || row.PlacedCategory != CategoryDocuments || row.LooseCount != 0 || len(row.Singles) != 0 {
		t.Fatalf("a placed row stays visible with its note: %+v", row)
	}
	if row := rows["audio"]; row.Count != 1 || row.TrashCount != 1 || row.LooseCount != 0 {
		t.Fatalf("a trashed file leaves its row: %+v", row)
	}
	failed, stale := picture.CouldNotFile.Files[0], picture.CouldNotFile.Files[1]
	if picture.CouldNotFile.Count != 2 || failed.State != "failed" || !failed.CanRetry || failed.Reason == "" || stale.State != "stale" || stale.CanRetry {
		t.Fatalf("could not be filed: %+v", picture.CouldNotFile)
	}
	if picture.DraftState != "saved" || picture.DraftRevision != "draft" || picture.Revision != groups.Revision {
		t.Fatalf("the picture must carry the revisions edits quote: %+v", picture)
	}
}

// R4, R64, R69: the first screenful is bounded however big the folder is, and a
// hostile name is shown as plain bounded text.
func TestReviewPicture_FirstScreenfulIsBoundedAndNamesAreSafe(t *testing.T) {
	names := []string{"<img onerror=alert(1)>.png", "invoice\u202egpj.exe", "a\nlog.pdf", strings.Repeat("界", 200) + ".pdf"}
	for i := range 206 {
		names = append(names, fmt.Sprintf("image-%04d.png", i))
	}
	for i := range 30 {
		names = append(names, fmt.Sprintf("secret-%02d.pdf", i)) // a sensitive row far over twelve
	}
	batch, candidates := groupFixture(names...)
	groups := mustGroup(t, batch, candidates)
	picture := buildReviewPicture(groups, ReviewDraftView{Revision: "draft", State: "default", Choices: defaultReviewChoices(groups)}, candidates)

	for _, folder := range picture.Folders {
		if len(folder.Files) > PictureFolderFileLimit || len(folder.UnfiledFiles) > PictureFolderFileLimit {
			t.Fatalf("%s names more than five files", folder.Category)
		}
	}
	for _, row := range picture.Loose {
		if len(row.Singles) > PictureLooseSingleLimit || row.More != row.LooseCount-len(row.Singles) {
			t.Fatalf("%s: loose row bound or remainder wrong: %+v", row.Key, row)
		}
	}
	data, err := json.Marshal(picture)
	if err != nil || len(data) > 12000 {
		t.Fatalf("first screenful is %d bytes: %v", len(data), err)
	}
	for _, private := range []string{"fingerprint", "token", "scope", "candidate_ids", "<img"} {
		if strings.Contains(string(data), private) {
			t.Fatalf("picture leaked or failed to escape %q", private)
		}
	}
	var drawn []PictureFile
	for _, folder := range picture.Folders {
		drawn = append(drawn, folder.Files...)
		drawn = append(drawn, folder.UnfiledFiles...)
	}
	for _, row := range picture.Loose {
		drawn = append(drawn, row.Singles...)
	}
	for _, file := range drawn {
		if strings.ContainsAny(file.Name, "\n\u202e") || utf8.RuneCountInString(file.Name) > maxDisplayNameRunes+1 {
			t.Fatalf("unsafe or unbounded name: %q", file.Name)
		}
	}
}

// R2: the third lead-line sentence must be true for this scan.
func TestReviewContentRead_IsTheScansProvenance(t *testing.T) {
	with := func(kinds ...ClassifierKind) []JanitorCandidate {
		out := make([]JanitorCandidate, len(kinds))
		for i, kind := range kinds {
			out[i].Classifier = kind
		}
		return out
	}
	for _, tc := range []struct {
		name       string
		candidates []JanitorCandidate
		want       string
	}{
		{"names only", with(ClassifierMetadata, ClassifierMetadata), "none"},
		{"a model saw names and a fallback", with(ClassifierModel, ClassifierFallback, ClassifierMetadata), "none"},
		{"one file read inside", with(ClassifierMetadata, ClassifierContent, ClassifierMetadata), "some"},
		{"a file with no saved provenance", with(ClassifierMetadata, ""), "unknown"},
		{"content wins over a missing one", with("", ClassifierContent), "some"},
		{"an empty scan read nothing", nil, "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reviewContentRead(tc.candidates); got != tc.want {
				t.Fatalf("reviewContentRead = %q, want %q", got, tc.want)
			}
		})
	}
}
