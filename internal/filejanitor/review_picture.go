package filejanitor

import (
	"context"
	"path"
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
	GroupID        string        `json:"group_id"`
	Key            string        `json:"key"`
	Count          int           `json:"count"`
	PlacedCount    int           `json:"placed_count"`
	PlacedCategory Category      `json:"placed_category,omitempty"` // set when every placed file shares one folder
	TrashCount     int           `json:"trash_count"`
	LooseCount     int           `json:"loose_count"`
	Singles        []PictureFile `json:"singles"`
	More           int           `json:"more"`
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
	ContentRead  string            `json:"content_read"`
	Folders      []PictureFolder   `json:"folders"`
	Loose        []PictureLooseRow `json:"loose"`
	LeftOut      PictureSection    `json:"left_out"`
	Trash        PictureSection    `json:"trash"`
	CouldNotFile PictureSection    `json:"could_not_file"`
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
}

func pictureFileFor(candidate JanitorCandidate, placement string) PictureFile {
	file := PictureFile{ID: candidate.ID, Name: candidate.Display(), Size: candidate.Size, Placement: placement}
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
	}
	choices := make(map[string]ReviewChoiceView, len(view.Choices))
	for _, choice := range view.Choices {
		choices[choice.CandidateID] = choice
	}
	groupOf := make(map[string]ReviewGroup, len(candidates))
	for _, group := range groups.Groups {
		members, _ := groups.Members(group.ID)
		for _, member := range members {
			groupOf[member.Candidate.ID] = group
			if choices[member.Candidate.ID].Operation == OperationMove {
				layout.selected[group.ID]++
			}
		}
	}
	for _, candidate := range candidates {
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
		switch {
		case chosen && choice.Operation == OperationTrash:
			layout.trash = append(layout.trash, pictureFileFor(candidate, PictureTrash))
			if group.Kind == "question" {
				layout.trashBy[group.ID]++
			}
		case chosen:
			file := pictureFileFor(candidate, PictureFiled)
			file.Category = choice.Category
			// The person placed it when Ori did not suggest this folder: a question
			// group's file, or a confident file sent somewhere other than its group's.
			file.YourChoice = group.Kind == "question" || choice.Category != group.Category
			layout.filed[choice.Category] = append(layout.filed[choice.Category], file)
			if group.Kind == "question" {
				layout.placed[group.ID] = append(layout.placed[group.ID], choice.Category)
			}
		case group.Kind == "filing" && layout.selected[group.ID] > 0:
			file := pictureFileFor(candidate, PictureLeftOut)
			file.Category = group.Category
			layout.leftOut = append(layout.leftOut, file)
		case group.Kind == "filing":
			file := pictureFileFor(candidate, PictureUnfiled)
			file.Category = group.Category
			layout.unfiled[group.Category] = append(layout.unfiled[group.Category], file)
		default:
			layout.loose[group.ID] = append(layout.loose[group.ID], pictureFileFor(candidate, PictureLoose))
		}
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
		RemovedStale: view.RemovedStale, ContentRead: reviewContentRead(candidates),
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
			TrashCount: layout.trashBy[group.ID], LooseCount: len(loose), Singles: firstFiles(loose, PictureLooseSingleLimit),
		}
		row.More = row.LooseCount - len(row.Singles)
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
	return picture
}

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
	case PictureLeftOut:
		return l.leftOut, nil
	case PictureTrash:
		return l.trash, nil
	case PictureCouldNotFile:
		return l.couldNot, nil
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
	view := s.revalidatedReviewDraft(state, batchID, binding, groups, root)
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
