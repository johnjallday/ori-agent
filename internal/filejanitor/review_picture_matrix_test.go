package filejanitor

import (
	"fmt"
	"testing"
)

// matrixNames is n files in three kinds: confident documents, confident images and
// files nothing recognises (a question). The mix is fixed so every size draws the same
// kinds of card.
func matrixNames(n int) []string {
	names := make([]string, 0, n)
	for i := 0; i < n; i++ {
		switch i % 10 {
		case 0, 1, 2, 3, 4, 5: // 60% documents
			names = append(names, fmt.Sprintf("report-%04d.pdf", i))
		case 6, 7, 8: // 30% images
			names = append(names, fmt.Sprintf("photo-%04d.png", i))
		default: // 10% a question
			names = append(names, fmt.Sprintf("mystery-%04d.zzzz", i))
		}
	}
	return names
}

// A3, A8: a folder of any size is drawn with every waiting file shown exactly once,
// the counts adding up, no more names on a card than the bounds allow, and what is
// not named counted as "and N more". This is the regression matrix over sizes the
// card-grid version was never asked about: none, one, the page size and one over it,
// and the largest a single press can file.
func TestReviewPicture_SizeMatrix(t *testing.T) {
	for _, n := range []int{0, 1, 50, 51, 200, 206, 1000} {
		t.Run(fmt.Sprintf("%d files", n), func(t *testing.T) {
			batch, candidates := groupFixture(matrixNames(n)...)
			groups := mustGroup(t, batch, candidates)
			view := ReviewDraftView{Revision: "draft", State: "saved", Choices: defaultReviewChoices(groups)}

			drawnOnce(t, layoutReviewPicture(groups, view, candidates), candidates)

			picture := buildReviewPicture(groups, view, candidates)
			if picture.Waiting != n {
				t.Fatalf("waiting %d, want %d", picture.Waiting, n)
			}
			total := picture.LeftOut.Count + picture.Trash.Count + picture.CouldNotFile.Count
			ticked := 0
			for _, folder := range picture.Folders {
				total += folder.Count + folder.UnfiledCount
				ticked += folder.Count
				if len(folder.Files) > 5 || len(folder.UnfiledFiles) > 5 {
					t.Fatalf("%s names %d+%d files: a card names five at most", folder.Destination, len(folder.Files), len(folder.UnfiledFiles))
				}
				if folder.Count < len(folder.Files) {
					t.Fatalf("%s names more files than it holds: %+v", folder.Destination, folder)
				}
			}
			loose := 0
			for _, row := range picture.Loose {
				if row.Count != row.LooseCount+row.PlacedCount+row.TrashCount {
					t.Fatalf("row %s does not add up: %+v", row.Key, row)
				}
				if len(row.Singles) > 12 || len(row.Pairs) > 3 || len(row.Bundles) > 3 {
					t.Fatalf("row %s draws too much: %d singles, %d pairs, %d bundles", row.Key, len(row.Singles), len(row.Pairs), len(row.Bundles))
				}
				total += row.LooseCount
				loose += row.LooseCount
			}
			if total != n {
				t.Fatalf("the counts on screen add up to %d, the folder has %d", total, n)
			}
			// What opens ticked is every confident file and none of the questions.
			wantTicked, wantLoose := 0, 0
			for _, name := range matrixNames(n) {
				if len(name) > 4 && name[len(name)-5:] == ".zzzz" {
					wantLoose++
				} else {
					wantTicked++
				}
			}
			if ticked != wantTicked || loose != wantLoose {
				t.Fatalf("ticked %d and loose %d, want %d and %d", ticked, loose, wantTicked, wantLoose)
			}
			if n == 0 && (len(picture.Folders) != 0 || len(picture.Loose) != 0 || picture.Waiting != 0) {
				t.Fatalf("an empty folder draws nothing: %+v", picture)
			}
		})
	}
}
