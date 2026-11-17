package filejanitor

import (
	"encoding/json"
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
	names := []string{"<img onerror=alert(1)>.png", "invoice‮gpj.exe", "a\nlog.pdf", strings.Repeat("界", 200) + ".pdf"}
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
		if strings.ContainsAny(file.Name, "\n‮") || utf8.RuneCountInString(file.Name) > maxDisplayNameRunes+1 {
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
