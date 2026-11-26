package filejanitor

import (
	"context"
	"path"
	"regexp"
	"sort"
	"strings"
)

// The picture is the review drawn as the folder will look after filing. These
// are the first-screenful bounds (R4, R64): a folder card names five files, a
// loose row twelve, and a section twelve; the rest is reached by paging.
const (
	PictureFolderFileLimit  = 5
	PictureLooseSingleLimit = 12
	PictureSectionFileLimit = 12
	PictureFilesPageMax     = 200
	// A loose row shows at most three pairs of six files and three bundles of twelve
	// (R64); the rest of the row is reached by paging.
	PicturePairLimit       = 3
	PicturePairFileLimit   = 6
	PictureBundleLimit     = 3
	PictureBundleFileLimit = 12
)

// Where a waiting file is drawn. Every pending member of a batch is in exactly
// one of these, so the on-screen counts add up to the scan's waiting total (R8).
const (
	PictureFiled        = "filed"          // inside a destination folder's card
	PictureUnfiled      = "unfiled"        // a folder that is not ticked (dashed card)
	PictureLoose        = "loose"          // still in a question row
	PictureLeftOut      = "left_out"       // taken out of a ticked folder
	PictureTrash        = "trash"          // marked for Trash, one file at a time
	PictureCouldNotFile = "could_not_file" // failed or changed in an earlier attempt
	// With a connection on: a file that will leave for another workspace, and a
	// file of a kind a connection carries whose card is not ticked.
	PictureLeaving = "leaving"
	PictureNotSent = "not_sent"
)

// PictureLeavingFileLimit is how many names a Leaving card shows before "and N
// more" (FR 21).
const PictureLeavingFileLimit = 3

// PictureLeavingCard is one receiving workspace's card in "Leaving": the files of
// the kinds its connection carries. GroupState is the card's tick: ticked when
// every such file is going, unticked when none is. KeptCount counts files of
// those kinds the person kept in this folder's Filed folders instead (FR 23).
type PictureLeavingCard struct {
	ConnectionID        string        `json:"connection_id"`
	ReceiverWorkspaceID string        `json:"receiver_workspace_id"`
	ReceiverName        string        `json:"receiver_name"`
	Kinds               []Category    `json:"kinds"`
	GroupState          string        `json:"group_state"` // ticked | partial | unticked | none
	Count               int           `json:"count"`
	Files               []PictureFile `json:"files"`
	NotSentCount        int           `json:"not_sent_count"`
	NotSentFiles        []PictureFile `json:"not_sent_files"`
	KeptCount           int           `json:"kept_count"`
}

// Two more lists the paging read serves, keyed by a pair's or bundle's ID: every
// member of that card, wherever it is now.
const (
	PictureSectionFamily = "family"
	PictureSectionBundle = "bundle"
)

// PictureFile is one file as the browser draws it: path-free, with the display
// name, never a fingerprint or a choice list.
type PictureFile struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Size       int64    `json:"size"`
	ModifiedAt string   `json:"modified_at,omitempty"`
	Category   Category `json:"category,omitempty"`
	YourChoice bool     `json:"your_choice,omitempty"`
	Placement  string   `json:"placement"`
	Viewer     string   `json:"viewer,omitempty"`
	// FamilyID, Ending and BundleID place a question-row file with its siblings: the
	// files of one name apart from a copy or version ending (a pair card) or of one
	// name apart from the extension (a bundle card). Opaque; they name nothing.
	FamilyID string `json:"family_id,omitempty"`
	Ending   string `json:"ending,omitempty"`
	BundleID string `json:"bundle_id,omitempty"`
	// State, Reason and CanRetry are set only for a file that could not be filed.
	State    string `json:"state,omitempty"`
	Reason   string `json:"reason,omitempty"`
	CanRetry bool   `json:"can_retry,omitempty"`
}

// PictureFolder is one destination folder's card. Count is what the folder will
// hold after filing; UnfiledFiles are the group's files when the folder is not
// ticked, drawn in the same card with a dashed border.
type PictureFolder struct {
	Category     Category      `json:"category"`
	Destination  string        `json:"destination"`
	GroupID      string        `json:"group_id,omitempty"`
	GroupState   string        `json:"group_state"` // ticked | partial | unticked | none
	Count        int           `json:"count"`
	Files        []PictureFile `json:"files"`
	UnfiledCount int           `json:"unfiled_count"`
	UnfiledFiles []PictureFile `json:"unfiled_files"`
}

// PictureLooseRow is one question group in "still loose". Count is the whole
// group; LooseCount is what is still in the row (not placed in a folder, not
// marked for Trash), so the row always adds up.
type PictureLooseRow struct {
	GroupID        string   `json:"group_id"`
	Key            string   `json:"key"`
	Count          int      `json:"count"`
	PlacedCount    int      `json:"placed_count"`
	PlacedCategory Category `json:"placed_category,omitempty"` // set when every placed file shares one folder
	TrashCount     int      `json:"trash_count"`
	LooseCount     int      `json:"loose_count"`
	// SentCount is how many of the row's files the person sent to another
	// workspace from the row's menu; they are not in PlacedCount.
	SentCount int `json:"sent_count,omitempty"`
	// Singles are the loose files that sit with no sibling (at most twelve). Pairs
	// and Bundles draw the rest of a possible-copies or music row as cards.
	Singles []PictureFile   `json:"singles"`
	Pairs   []PicturePair   `json:"pairs"`
	Bundles []PictureBundle `json:"bundles"`
	// More is how many loose files the row holds beyond everything drawn above.
	More int `json:"more"`
}

// PicturePair is a family of files whose names match apart from a copy or version
// ending and whose extensions match. Files lists the one with no ending first, then
// the others, and includes a member that is not loose any more (marked for Trash, or
// placed in a folder) so its line can say where it went. Count is the whole family.
type PicturePair struct {
	FamilyID string        `json:"family_id"`
	Count    int           `json:"count"`
	Files    []PictureFile `json:"files"`
	More     int           `json:"more"`
}

// PictureBundle is a set of audio files that share a name apart from the extension.
// Name is the shared name, for the card's heading; no extension, no path.
type PictureBundle struct {
	BundleID string        `json:"bundle_id"`
	Name     string        `json:"name"`
	Count    int           `json:"count"`
	Files    []PictureFile `json:"files"`
	More     int           `json:"more"`
	// Where the whole bundle is now, counted over every member (the card shows at
	// most twelve), so its menu and its "split" note are true however many files it
	// holds. PlacedCategory is set only when every placed file shares one folder.
	LooseCount     int      `json:"loose_count"`
	PlacedCount    int      `json:"placed_count"`
	PlacedCategory Category `json:"placed_category,omitempty"`
	TrashCount     int      `json:"trash_count"`
	SentCount      int      `json:"sent_count,omitempty"`
}

// PictureSection is a bounded list with its whole count.
type PictureSection struct {
	Count int           `json:"count"`
	Files []PictureFile `json:"files"`
}

// ReviewPicture is the one bounded read that draws the first screenful. It is a
// projection of saved state and revalidated saved choices: it writes nothing,
// creates no folder, issues no approval and reads no file contents.
type ReviewPicture struct {
	BatchID       string            `json:"batch_id"`
	Revision      string            `json:"revision"`
	DraftRevision string            `json:"draft_revision"`
	DraftState    string            `json:"draft_state"`
	RuleVersion   int               `json:"rule_version"`
	Counts        ReviewGroupCounts `json:"counts"`
	Waiting       int               `json:"waiting"`
	RemovedStale  int               `json:"removed_stale"`
	// ContentRead says whether any file in the scan was sorted by reading inside
	// it: "none", "some", or "unknown" when a saved file has no provenance. The
	// lead line's third sentence must be true for the scan.
	ContentRead string `json:"content_read"`
	// Changed is how many waiting files are drawn under "could not be filed"
	// because they changed or went missing since the scan. The sections add up to
	// Waiting once these are counted.
	Changed      int               `json:"changed"`
	Folders      []PictureFolder   `json:"folders"`
	Loose        []PictureLooseRow `json:"loose"`
	LeftOut      PictureSection    `json:"left_out"`
	Trash        PictureSection    `json:"trash"`
	CouldNotFile PictureSection    `json:"could_not_file"`
	// Leaving has one card per connection that is on (FR 20, 21). Absent with no
	// connection, so the picture is then exactly what it was before sends.
	Leaving []PictureLeavingCard `json:"leaving,omitempty"`
}

// PictureFilesQuery names one list to page: the section, and the folder category
// or group ID for the sections that have one.
type PictureFilesQuery struct {
	Section       string
	Key           string
	Revision      string
	DraftRevision string
	Limit, Offset int
}

// PictureFilesPage is one page of a section. Total is the whole list.
type PictureFilesPage struct {
	BatchID       string        `json:"batch_id"`
	Revision      string        `json:"revision"`
	DraftRevision string        `json:"draft_revision"`
	Section       string        `json:"section"`
	Key           string        `json:"key,omitempty"`
	Total         int           `json:"total"`
	Limit         int           `json:"limit"`
	Offset        int           `json:"offset"`
	Files         []PictureFile `json:"files"`
}

// pictureLayout holds every list in full. The first screenful truncates them and
// the paging read slices them, so both come from one placement and cannot
// disagree about where a file is.
type pictureLayout struct {
	filed    map[Category][]PictureFile
	unfiled  map[Category][]PictureFile
	loose    map[string][]PictureFile
	placed   map[string][]Category // question group ID to the folders its files were placed in
	trashBy  map[string]int        // question group ID to its files marked for Trash
	leftOut  []PictureFile
	trash    []PictureFile
	couldNot []PictureFile
	selected map[string]int // group ID to members with a move choice
	// family and bundle hold every waiting member of a pair or bundle with where it
	// is now (loose, in Trash, placed), in batch order; familyOrder and bundleOrder
	// give the first-seen order of the IDs within a row.
	family      map[string][]PictureFile
	bundle      map[string][]PictureFile
	familyOrder map[string][]string
	bundleOrder map[string][]string
	// changed counts the waiting files drawn under "could not be filed" because
	// the file itself changed or went missing since the scan; they are still
	// waiting, so they are part of the scan's waiting total.
	changed int
	// leaving and notSent are keyed by connection ID; sentBy counts each question
	// row's files sent from its menu; kept counts, per connection, the files of a
	// carried kind the person kept in Filed instead.
	leaving map[string][]PictureFile
	notSent map[string][]PictureFile
	sentBy  map[string]int
	kept    map[string]int
	// leftOutSend counts, per connection, files of a carried kind taken out of a
	// card one at a time, so the Leaving card reads partial rather than ticked.
	leftOutSend map[string]int
}

// Why a ticked file that is no longer the file that was ticked is not filed.
const (
	pictureReasonChanged = "This file changed since you looked, so Ori left it alone."
	pictureReasonMissing = "This file is no longer in the folder."
)

func pictureFileFor(candidate JanitorCandidate, placement string) PictureFile {
	file := PictureFile{ID: candidate.ID, Name: candidate.Display(), Size: candidate.Size, Placement: placement}
	if placement != PictureCouldNotFile {
		file.Viewer, _ = ReviewInspectionViewer(candidate)
	}
	if !candidate.ModifiedAt.IsZero() {
		file.ModifiedAt = candidate.ModifiedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	return file
}

// layoutReviewPicture is the single placement of every file in the batch. A file
// still waiting goes to exactly one place; a file that failed or changed goes to
// "could not be filed"; handled files (kept, filed) are only counted.
func layoutReviewPicture(groups ReviewGroups, view ReviewDraftView, candidates []JanitorCandidate) pictureLayout {
	layout := pictureLayout{
		filed: map[Category][]PictureFile{}, unfiled: map[Category][]PictureFile{}, loose: map[string][]PictureFile{},
		placed: map[string][]Category{}, trashBy: map[string]int{}, selected: map[string]int{},
		family: map[string][]PictureFile{}, bundle: map[string][]PictureFile{},
		familyOrder: map[string][]string{}, bundleOrder: map[string][]string{},
		leaving: map[string][]PictureFile{}, notSent: map[string][]PictureFile{}, sentBy: map[string]int{}, kept: map[string]int{},
		leftOutSend: map[string]int{},
	}
	routes := view.routes
	choices := make(map[string]ReviewChoiceView, len(view.Choices))
	for _, choice := range view.Choices {
		choices[choice.CandidateID] = choice
	}
	groupOf := make(map[string]ReviewGroup, len(candidates))
	siblings := make(map[string]ReviewGroupMember, len(candidates))
	for _, group := range groups.Groups {
		members, _ := groups.Members(group.ID)
		for _, member := range members {
			groupOf[member.Candidate.ID] = group
			if member.Family != "" || member.Bundle != "" {
				siblings[member.Candidate.ID] = member
			}
			if choices[member.Candidate.ID].Operation == OperationMove {
				layout.selected[group.ID]++
			}
		}
	}
	removed := make(map[string]string, len(view.removed))
	for _, gone := range view.removed {
		removed[gone.candidateID] = gone.reason
	}
	for _, candidate := range candidates {
		if reason, gone := removed[candidate.ID]; gone && proposalPending(candidate) {
			// Ticked, but the file is no longer the file that was ticked: it is
			// not filed, and it is not "left out by you" either.
			file := pictureFileFor(candidate, PictureCouldNotFile)
			file.State, file.Reason = reason, pictureReasonChanged
			if reason == "missing" {
				file.Reason = pictureReasonMissing
			}
			layout.couldNot = append(layout.couldNot, file)
			layout.changed++
			continue
		}
		if !proposalPending(candidate) {
			if candidate.State == CandidateFailed || candidate.State == CandidateStale {
				file := pictureFileFor(candidate, PictureCouldNotFile)
				file.State, file.Reason, file.CanRetry = string(candidate.State), candidate.StateReason, candidate.State == CandidateFailed
				layout.couldNot = append(layout.couldNot, file)
			}
			continue
		}
		group, grouped := groupOf[candidate.ID]
		if !grouped {
			continue // not a member of any card: nothing to draw, nothing to promise
		}
		choice, chosen := choices[candidate.ID]
		// A file that sits with siblings carries their IDs wherever it is drawn, and is
		// also kept in its pair or bundle with where it is now, so a card can say "in
		// Trash" or "in Filed/Audio" for a member that has left the row.
		decorate := func(file PictureFile) PictureFile {
			if sibling, found := siblings[candidate.ID]; found {
				file.FamilyID, file.Ending, file.BundleID = sibling.Family, sibling.Ending, sibling.Bundle
			}
			return file
		}
		track := func(file PictureFile) {
			if file.FamilyID != "" {
				if _, seen := layout.family[file.FamilyID]; !seen {
					layout.familyOrder[group.ID] = append(layout.familyOrder[group.ID], file.FamilyID)
				}
				layout.family[file.FamilyID] = append(layout.family[file.FamilyID], file)
			}
			if file.BundleID != "" {
				if _, seen := layout.bundle[file.BundleID]; !seen {
					layout.bundleOrder[group.ID] = append(layout.bundleOrder[group.ID], file.BundleID)
				}
				layout.bundle[file.BundleID] = append(layout.bundle[file.BundleID], file)
			}
		}
		// The connection that carries this file's kind, if any: a confident file of
		// that kind is drawn as leaving or not sent, never as an unticked Filed card.
		carrying, carried := sendRoute{}, false
		if group.Kind == "filing" {
			carrying, carried = routes.carrying(group.Category)
		}
		switch {
		case chosen && choice.Operation == OperationTrash:
			file := decorate(pictureFileFor(candidate, PictureTrash))
			layout.trash = append(layout.trash, file)
			track(file)
			if group.Kind == "question" {
				layout.trashBy[group.ID]++
			}
		case chosen && choice.ConnectionID != "":
			file := decorate(pictureFileFor(candidate, PictureLeaving))
			file.Category = choice.Category
			file.YourChoice = group.Kind == "question"
			layout.leaving[choice.ConnectionID] = append(layout.leaving[choice.ConnectionID], file)
			track(file)
			if group.Kind == "question" {
				layout.sentBy[group.ID]++
			}
		case chosen && carried && choice.Category == group.Category:
			// A file of a carried kind the person kept here: drawn in Filed, and
			// counted on the Leaving card so it can offer to send them again.
			layout.kept[carrying.ConnectionID]++
			file := decorate(pictureFileFor(candidate, PictureFiled))
			file.Category = choice.Category
			layout.filed[choice.Category] = append(layout.filed[choice.Category], file)
			track(file)
		case chosen:
			file := pictureFileFor(candidate, PictureFiled)
			file.Category = choice.Category
			// The person placed it when Ori did not suggest this folder: a question
			// group's file, or a confident file sent somewhere other than its group's.
			file.YourChoice = group.Kind == "question" || choice.Category != group.Category
			file = decorate(file)
			layout.filed[choice.Category] = append(layout.filed[choice.Category], file)
			track(file)
			if group.Kind == "question" {
				layout.placed[group.ID] = append(layout.placed[group.ID], choice.Category)
			}
		case group.Kind == "filing" && layout.selected[group.ID] > 0:
			file := pictureFileFor(candidate, PictureLeftOut)
			file.Category = group.Category
			layout.leftOut = append(layout.leftOut, file)
			if carried {
				layout.leftOutSend[carrying.ConnectionID]++
			}
		case carried:
			file := pictureFileFor(candidate, PictureNotSent)
			file.Category = group.Category
			layout.notSent[carrying.ConnectionID] = append(layout.notSent[carrying.ConnectionID], file)
		case group.Kind == "filing":
			file := pictureFileFor(candidate, PictureUnfiled)
			file.Category = group.Category
			layout.unfiled[group.Category] = append(layout.unfiled[group.Category], file)
		default:
			file := decorate(pictureFileFor(candidate, PictureLoose))
			layout.loose[group.ID] = append(layout.loose[group.ID], file)
			track(file)
		}
	}
	for id, files := range layout.family {
		// The file with no ending is listed first: it is the one a copy is a copy of.
		sort.SliceStable(files, func(i, j int) bool { return files[i].Ending == EndingNone && files[j].Ending != EndingNone })
		layout.family[id] = files
	}
	for category := range layout.filed {
		files := layout.filed[category]
		sort.SliceStable(files, func(i, j int) bool { return files[i].YourChoice && !files[j].YourChoice })
	}
	return layout
}

func firstFiles(files []PictureFile, limit int) []PictureFile {
	if len(files) > limit {
		files = files[:limit]
	}
	return append([]PictureFile{}, files...)
}

func pictureSection(files []PictureFile) PictureSection {
	return PictureSection{Count: len(files), Files: firstFiles(files, PictureSectionFileLimit)}
}

// reviewContentRead reports, from the saved classification provenance of every
// file in the scan, whether any was sorted by reading inside it.
func reviewContentRead(candidates []JanitorCandidate) string {
	unknown := false
	for _, candidate := range candidates {
		switch candidate.Classifier {
		case ClassifierContent:
			return "some"
		case ClassifierMetadata, ClassifierModel, ClassifierFallback:
		default:
			unknown = true
		}
	}
	if unknown {
		return "unknown"
	}
	return "none"
}

// buildReviewPicture draws the first screenful from one snapshot's groups,
// revalidated draft view and candidates. It is pure: no IO, no clock.
func buildReviewPicture(groups ReviewGroups, view ReviewDraftView, candidates []JanitorCandidate) ReviewPicture {
	layout := layoutReviewPicture(groups, view, candidates)
	picture := ReviewPicture{
		BatchID: groups.BatchID, Revision: groups.Revision, DraftRevision: view.Revision, DraftState: view.State,
		RuleVersion: groups.RuleVersion, Counts: groups.Counts, Waiting: groups.Counts.Filing + groups.Counts.Questions,
		RemovedStale: view.RemovedStale, ContentRead: reviewContentRead(candidates), Changed: layout.changed,
		Folders: []PictureFolder{}, Loose: []PictureLooseRow{},
		LeftOut: pictureSection(layout.leftOut), Trash: pictureSection(layout.trash), CouldNotFile: pictureSection(layout.couldNot),
	}
	filingGroup := make(map[Category]ReviewGroup, len(groups.Groups))
	for _, group := range groups.Groups {
		if group.Kind == "filing" {
			filingGroup[group.Category] = group
		}
	}
	for _, definition := range CategoryRegistry {
		filed, unfiled := layout.filed[definition.ID], layout.unfiled[definition.ID]
		if len(filed) == 0 && len(unfiled) == 0 {
			continue
		}
		folder := PictureFolder{
			Category: definition.ID, Destination: path.Join(DefaultFilingRootName, definition.FolderName), GroupState: "none",
			Count: len(filed), Files: firstFiles(filed, PictureFolderFileLimit),
			UnfiledCount: len(unfiled), UnfiledFiles: firstFiles(unfiled, PictureFolderFileLimit),
		}
		if group, found := filingGroup[definition.ID]; found {
			folder.GroupID = group.ID
			switch selected := layout.selected[group.ID]; {
			case selected == 0:
				folder.GroupState = "unticked"
			case selected < group.Count:
				folder.GroupState = "partial"
			default:
				folder.GroupState = "ticked"
			}
		}
		picture.Folders = append(picture.Folders, folder)
	}
	for _, group := range groups.Groups {
		if group.Kind != "question" {
			continue
		}
		loose := layout.loose[group.ID]
		row := PictureLooseRow{
			GroupID: group.ID, Key: group.Key, Count: group.Count, PlacedCount: len(layout.placed[group.ID]),
			TrashCount: layout.trashBy[group.ID], LooseCount: len(loose),
		}
		// Files that sit with siblings are drawn as cards; the others are singles.
		alone := make([]PictureFile, 0, len(loose))
		for _, file := range loose {
			if file.FamilyID == "" && file.BundleID == "" {
				alone = append(alone, file)
			}
		}
		row.Singles = firstFiles(alone, PictureLooseSingleLimit)
		row.Pairs, row.Bundles = layout.cards(group.ID)
		shown := len(row.Singles)
		for _, pair := range row.Pairs {
			shown += looseIn(pair.Files)
		}
		for _, bundle := range row.Bundles {
			shown += looseIn(bundle.Files)
		}
		row.More = row.LooseCount - shown
		row.SentCount = layout.sentBy[group.ID]
		if placed := layout.placed[group.ID]; len(placed) > 0 {
			same := true
			for _, category := range placed {
				same = same && category == placed[0]
			}
			if same {
				row.PlacedCategory = placed[0]
			}
		}
		picture.Loose = append(picture.Loose, row)
	}
	for _, route := range view.routes {
		leaving, notSent := layout.leaving[route.ConnectionID], layout.notSent[route.ConnectionID]
		card := PictureLeavingCard{
			ConnectionID: route.ConnectionID, ReceiverWorkspaceID: route.ReceiverWorkspaceID, ReceiverName: route.ReceiverName,
			Kinds: append([]Category(nil), route.Kinds...), GroupState: "none",
			Count: len(leaving), Files: firstFiles(leaving, PictureLeavingFileLimit),
			NotSentCount: len(notSent), NotSentFiles: firstFiles(notSent, PictureLeavingFileLimit),
			KeptCount: layout.kept[route.ConnectionID],
		}
		switch {
		case len(leaving) == 0 && len(notSent) > 0:
			card.GroupState = "unticked"
		case len(leaving) > 0 && len(notSent) == 0 && layout.leftOutSend[route.ConnectionID] == 0:
			card.GroupState = "ticked"
		case len(leaving) > 0:
			card.GroupState = "partial"
		}
		picture.Leaving = append(picture.Leaving, card)
	}
	return picture
}

func looseIn(files []PictureFile) int {
	loose := 0
	for _, file := range files {
		if file.Placement == PictureLoose {
			loose++
		}
	}
	return loose
}

// drawn reports whether a card still has something to show: a file that is loose, or
// one the person placed in a folder (so the placement can be changed back from the
// card, as a placed row can). A card whose files are all in Trash is not drawn: the
// Trash box holds them.
func drawn(files []PictureFile) bool {
	for _, file := range files {
		if file.Placement == PictureLoose || file.Placement == PictureFiled || file.Placement == PictureLeaving {
			return true
		}
	}
	return false
}

// cards builds the pair and bundle cards of one question row: the first three
// families and the first three bundles that are still drawn, each with the first
// files of the card. A card stays whole while it is drawn, so a copy sent to Trash
// reads "in Trash" on its own line.
func (l pictureLayout) cards(groupID string) ([]PicturePair, []PictureBundle) {
	pairs, bundles := []PicturePair{}, []PictureBundle{}
	for _, id := range l.familyOrder[groupID] {
		files := l.family[id]
		if !drawn(files) {
			continue
		}
		if len(pairs) == PicturePairLimit {
			break
		}
		shown := firstFiles(files, PicturePairFileLimit)
		pairs = append(pairs, PicturePair{FamilyID: id, Count: len(files), Files: shown, More: len(files) - len(shown)})
	}
	for _, id := range l.bundleOrder[groupID] {
		files := l.bundle[id]
		if !drawn(files) {
			continue
		}
		if len(bundles) == PictureBundleLimit {
			break
		}
		shown := firstFiles(files, PictureBundleFileLimit)
		bundle := PictureBundle{BundleID: id, Name: bundleName(files[0].Name), Count: len(files), Files: shown, More: len(files) - len(shown)}
		var placedIn []Category
		for _, file := range files {
			switch file.Placement {
			case PictureLoose:
				bundle.LooseCount++
			case PictureTrash:
				bundle.TrashCount++
			case PictureLeaving:
				bundle.SentCount++
			case PictureFiled:
				bundle.PlacedCount++
				placedIn = append(placedIn, file.Category)
			}
		}
		if len(placedIn) > 0 {
			bundle.PlacedCategory = placedIn[0]
			for _, category := range placedIn {
				if category != placedIn[0] {
					bundle.PlacedCategory = ""
				}
			}
		}
		bundles = append(bundles, bundle)
	}
	return pairs, bundles
}

// bundleName is the name the files of a bundle share: the first file's name without
// its extension. It is display text for the card heading, not an identifier.
func bundleName(name string) string {
	return strings.TrimSuffix(name, path.Ext(name))
}

var pictureSiblingKey = regexp.MustCompile(`^(?:fam|bun)-[0-9a-f]{24}$`)

// pictureList returns one whole list by section and key, for the paging read.
func (l pictureLayout) pictureList(section, key string) ([]PictureFile, error) {
	switch section {
	case PictureFiled, PictureUnfiled:
		definition, err := LookupCategory(key)
		if err != nil {
			return nil, ErrInvalidAction
		}
		if section == PictureFiled {
			return l.filed[definition.ID], nil
		}
		return l.unfiled[definition.ID], nil
	case PictureLoose:
		if !managedRootGeneration.MatchString(key) {
			return nil, ErrInvalidAction
		}
		return l.loose[key], nil
	case PictureSectionFamily, PictureSectionBundle:
		// The whole family or bundle, wherever each member is now, for a card that
		// holds more files than it shows.
		if !pictureSiblingKey.MatchString(key) {
			return nil, ErrInvalidAction
		}
		if section == PictureSectionFamily {
			return l.family[key], nil
		}
		return l.bundle[key], nil
	case PictureLeftOut:
		return l.leftOut, nil
	case PictureTrash:
		return l.trash, nil
	case PictureCouldNotFile:
		return l.couldNot, nil
	case PictureLeaving, PictureNotSent:
		// A Leaving card's full list, keyed by its connection.
		if !validConnectionID(key) {
			return nil, ErrInvalidAction
		}
		if section == PictureLeaving {
			return l.leaving[key], nil
		}
		return l.notSent[key], nil
	}
	return nil, ErrInvalidAction
}

// pictureSnapshot is everything one picture read needs, taken from a single
// saved scan state so the groups, the choices and the files cannot disagree.
type pictureSnapshot struct {
	batchID    string
	groups     ReviewGroups
	view       ReviewDraftView
	candidates []JanitorCandidate
}

// latestPendingBatch is the newest retained batch that still has work waiting or
// running. It is a navigation convenience only: edits always name a concrete batch.
func latestPendingBatch(state ScanState) (JanitorBatch, bool) {
	// Index once: finding a retained pending batch must not repeatedly build a map
	// of every retained candidate for every resolved batch.
	pending := make(map[string]bool, len(state.Batches))
	for _, candidate := range state.Candidates {
		if candidate.Actionable() || candidate.State == CandidateApplying {
			pending[candidate.BatchID] = true
		}
	}
	for i := len(state.Batches) - 1; i >= 0; i-- {
		if pending[state.Batches[i].ID] {
			return state.Batches[i], true
		}
	}
	return JanitorBatch{}, false
}

func (s *Service) readPictureSnapshot(ctx context.Context, owner, workspaceID, batchID string) (pictureSnapshot, error) {
	if ctx.Err() != nil || s == nil || s.store == nil {
		return pictureSnapshot{}, ErrReviewUnavailable
	}
	state, err := s.store.loadScanState(workspaceID, true)
	if err != nil {
		return pictureSnapshot{}, ErrReviewUnavailable
	}
	batchID = strings.TrimSpace(batchID)
	if batchID == "latest" {
		batch, found := latestPendingBatch(state)
		if !found {
			// A known empty review is distinct from unavailable access: prove the
			// grant and the folder first, as the group read does.
			if _, applies, err := s.reviewInstall(owner, workspaceID); err != nil || !applies {
				return pictureSnapshot{}, ErrReviewUnavailable
			}
			if _, err := s.StoredManagedRoot(owner, workspaceID); err != nil {
				return pictureSnapshot{}, ErrReviewUnavailable
			}
			return pictureSnapshot{}, nil
		}
		batchID = batch.ID
	}
	groups, binding, root, err := s.reviewDraftSnapshot(owner, workspaceID, batchID, state)
	if err != nil {
		return pictureSnapshot{}, err
	}
	view := s.revalidatedReviewDraftWith(state, batchID, binding, groups, root, s.liveSendRoutes(owner, workspaceID))
	// Owner, grant or folder changes while the draft was revalidated must not lend
	// the old scan's choices to a replacement scope.
	_, freshBinding, freshRoot, err := s.reviewDraftSnapshot(owner, workspaceID, batchID, state)
	if err != nil || freshRoot != root || freshBinding != binding || ctx.Err() != nil {
		return pictureSnapshot{}, ErrReviewUnavailable
	}
	return pictureSnapshot{batchID: batchID, groups: groups, view: view, candidates: state.CandidatesFor(batchID)}, nil
}

// ReadReviewPicture is the one bounded read that draws the review's first
// screenful. It resolves "latest" and always reports the concrete batch.
func (s *Service) ReadReviewPicture(ctx context.Context, owner, workspaceID, batchID string) (ReviewPicture, error) {
	snapshot, err := s.readPictureSnapshot(ctx, owner, workspaceID, batchID)
	if err != nil {
		return ReviewPicture{}, err
	}
	if snapshot.batchID == "" {
		return ReviewPicture{
			RuleVersion: ReviewGroupRuleVersion, DraftState: "empty", ContentRead: "none",
			Folders: []PictureFolder{}, Loose: []PictureLooseRow{},
			LeftOut: pictureSection(nil), Trash: pictureSection(nil), CouldNotFile: pictureSection(nil),
		}, nil
	}
	return buildReviewPicture(snapshot.groups, snapshot.view, snapshot.candidates), nil
}

// ReadReviewPictureFiles pages one list of the picture ("and N more", a row's
// full list). The revisions pin the page to what the person was looking at: a
// scan or a saved choice that changed since is ErrReviewConflict, never a page
// drawn from different data.
func (s *Service) ReadReviewPictureFiles(ctx context.Context, owner, workspaceID, batchID string, query PictureFilesQuery) (PictureFilesPage, error) {
	if query.Limit < 1 || query.Limit > PictureFilesPageMax || query.Offset < 0 {
		return PictureFilesPage{}, ErrInvalidAction
	}
	snapshot, err := s.readPictureSnapshot(ctx, owner, workspaceID, batchID)
	if err != nil {
		return PictureFilesPage{}, err
	}
	if snapshot.batchID == "" {
		return PictureFilesPage{}, ErrBatchNotFound
	}
	if (query.Revision != "" && query.Revision != snapshot.groups.Revision) || (query.DraftRevision != "" && query.DraftRevision != snapshot.view.Revision) {
		return PictureFilesPage{}, ErrReviewConflict
	}
	list, err := layoutReviewPicture(snapshot.groups, snapshot.view, snapshot.candidates).pictureList(query.Section, query.Key)
	if err != nil {
		return PictureFilesPage{}, err
	}
	offset := min(query.Offset, len(list))
	end := offset + min(query.Limit, len(list)-offset)
	return PictureFilesPage{
		BatchID: snapshot.batchID, Revision: snapshot.groups.Revision, DraftRevision: snapshot.view.Revision,
		Section: query.Section, Key: query.Key, Total: len(list), Limit: query.Limit, Offset: offset,
		Files: append([]PictureFile{}, list[offset:end]...),
	}, nil
}
