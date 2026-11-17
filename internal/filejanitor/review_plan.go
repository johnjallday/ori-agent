package filejanitor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path"
	"sort"
	"strings"
)

// The plan check works out, for the files that are ticked, placed or marked for
// Trash, exactly what filing would do: the destination, the final name (never
// overwriting), and a digest of all of it. It is how the picture shows "renamed"
// tags and "changed since you looked", and how one File press proves it is
// pressing what the picture showed.
//
// It is a read. It issues no approval, creates no folder, opens no file, and
// leaves the saved scan untouched; it only looks at each file's metadata.
const (
	PlanFirstItems      = 5
	PlanChangedLimit    = 50
	PlanItemsPageMax    = 200
	PlanItemsPageNormal = 50
	// PlanMaxOperations is the most one press files (today's limit).
	PlanMaxOperations = MaxReviewChoicesPerBatch
	// PlanTrashDestination names the Trash list in a plan page query.
	PlanTrashDestination = "trash"
)

// ErrReviewPlanChanged means what a press was about to file is not what the
// picture showed: a revision, the plan, or the Trash count differs. Nothing was
// done; the fresh plan accompanies it.
var ErrReviewPlanChanged = errors.New("the plan changed since it was shown")

// ReviewPlanQuery asks for the plan of one saved review. Revision and
// GroupRevision, when set, must name what the caller was looking at (a mismatch is
// ErrReviewConflict, never a plan of different data). Destination, when set to a
// category ID or "trash", also returns one page of that list.
type ReviewPlanQuery struct {
	Revision      string
	GroupRevision string
	Destination   string
	Limit, Offset int
	// CandidateIDs, when set (and no Destination), returns exactly those files'
	// planned lines: how the picture learns the final name of each file it shows,
	// whichever five a card lists. At most PlanItemsPageMax, no duplicates.
	CandidateIDs []string
}

// PlanItemView is one planned operation, path-free.
type PlanItemView struct {
	CandidateID string    `json:"candidate_id"`
	Name        string    `json:"name"`
	Operation   Operation `json:"operation"`
	Category    Category  `json:"category,omitempty"`
	FinalName   string    `json:"final_name,omitempty"`
	Renamed     bool      `json:"renamed,omitempty"`
	Size        int64     `json:"size"`
}

// PlanDestination totals one destination folder.
type PlanDestination struct {
	Category     Category       `json:"category"`
	Destination  string         `json:"destination"`
	Count        int            `json:"count"`
	RenamedCount int            `json:"renamed_count"`
	First        []PlanItemView `json:"first"`
}

// PlanTrash totals the files marked for Trash.
type PlanTrash struct {
	Count int            `json:"count"`
	First []PlanItemView `json:"first"`
}

// PlanChanged names a ticked file that is no longer the file that was ticked.
type PlanChanged struct {
	CandidateID string `json:"candidate_id"`
	Name        string `json:"name"`
	Reason      string `json:"reason"` // "changed" or "missing"
}

// PlanChangedSet is the bounded list of such files with its whole count.
type PlanChangedSet struct {
	Total int           `json:"total"`
	Items []PlanChanged `json:"items"`
}

// ReviewPlan is what filing would do right now. PlanDigest covers every
// operation, so a press that quotes it proves it is pressing what was shown.
type ReviewPlan struct {
	BatchID       string `json:"batch_id"`
	Revision      string `json:"revision"`
	GroupRevision string `json:"group_revision"`
	PlanDigest    string `json:"plan_digest"`
	MoveCount     int    `json:"move_count"`
	TrashCount    int    `json:"trash_count"`
	Total         int    `json:"total"`
	// OverLimit means more than one press may file. The counts are still stated;
	// no file was looked at and there is no digest to quote.
	OverLimit    bool              `json:"over_limit"`
	Destinations []PlanDestination `json:"destinations"`
	Trash        PlanTrash         `json:"trash"`
	Changed      PlanChangedSet    `json:"changed"`
	// Items is one page of a list the query named; ItemsTotal is the whole list.
	Items      []PlanItemView `json:"items,omitempty"`
	ItemsTotal int            `json:"items_total,omitempty"`
	Limit      int            `json:"limit,omitempty"`
	Offset     int            `json:"offset,omitempty"`
}

// computedPlan is the plan with everything a press needs to carry it out in the
// same order it was worked out.
type computedPlan struct {
	plan     ReviewPlan
	lines    []PreviewItem // in apply order: batch candidate order
	items    []PlanItem
	view     ReviewDraftView
	binding  reviewDraftBinding
	settings JanitorSettings
	root     ManagedRoot
}

func planItemView(line PreviewItem) PlanItemView {
	view := PlanItemView{
		CandidateID: line.CandidateID, Name: line.Name, Operation: line.Operation,
		Category: line.Category, Renamed: line.Renamed, Size: line.Size,
	}
	if line.Operation == OperationMove {
		view.FinalName = path.Base(line.Destination)
	}
	return view
}

// computeReviewPlan works the plan out from one saved-state snapshot and the
// revalidated saved choices. It is safe inside UpdateScanState: it takes no lock,
// writes nothing, and only reads metadata.
func (s *Service) computeReviewPlan(owner, workspaceID, batchID string, state ScanState) (computedPlan, error) {
	groups, binding, root, err := s.reviewDraftSnapshot(owner, workspaceID, batchID, state)
	if err != nil {
		return computedPlan{}, err
	}
	view := s.revalidatedReviewDraft(state, batchID, binding, groups, root)
	if view.State == "invalid" || view.State == "stale" {
		return computedPlan{}, ErrReviewDraftInvalid
	}
	settings, err := s.store.LoadSettings(workspaceID)
	if err != nil {
		return computedPlan{}, err
	}
	resolved, err := s.scannerFor().ResolveRoot(settings)
	if err != nil || resolved != root.Path || settings.RootID != root.Generation {
		return computedPlan{}, ErrReviewUnavailable
	}

	out := computedPlan{view: view, binding: binding, settings: settings, root: root}
	out.plan = ReviewPlan{
		BatchID: batchID, Revision: view.Revision, GroupRevision: groups.Revision,
		Destinations: []PlanDestination{}, Trash: PlanTrash{First: []PlanItemView{}},
		Changed: PlanChangedSet{Items: []PlanChanged{}},
	}
	moves, trashes := 0, 0
	for _, choice := range view.Choices {
		if choice.Operation == OperationTrash {
			trashes++
		} else {
			moves++
		}
	}
	out.plan.MoveCount, out.plan.TrashCount, out.plan.Total = moves, trashes, moves+trashes
	if out.plan.Total > PlanMaxOperations {
		out.plan.OverLimit = true
		return out, nil
	}

	choices := make(map[string]ReviewChoiceView, len(view.Choices))
	for _, choice := range view.Choices {
		choices[choice.CandidateID] = choice
	}
	candidates := state.CandidatesFor(batchID)
	names := make(map[string]string, len(candidates))
	for _, candidate := range candidates {
		names[candidate.ID] = candidate.Display()
	}

	var changed []PlanChanged
	for _, removed := range view.removed {
		changed = append(changed, PlanChanged{CandidateID: removed.candidateID, Name: names[removed.candidateID], Reason: removed.reason})
	}
	reserved := map[string]struct{}{}
	for _, candidate := range candidates {
		choice, chosen := choices[candidate.ID]
		if !chosen {
			continue
		}
		line, planned, err := planLine(settings, root.Path, candidate, choice.Operation, string(choice.Category), reserved)
		if err != nil {
			var source planSourceError
			if errors.As(err, &source) {
				// The file changed between the revalidation and now: name it, do not
				// fail the whole plan.
				changed = append(changed, PlanChanged{CandidateID: candidate.ID, Name: candidate.Display(), Reason: source.reason})
				continue
			}
			return computedPlan{}, err
		}
		out.lines = append(out.lines, line)
		out.items = append(out.items, planned)
	}
	// What is left to file is what was worked out, not what was ticked: a changed
	// or missing file is named, never filed and never silently dropped.
	out.plan.MoveCount, out.plan.TrashCount = 0, 0
	byCategory := map[Category]*PlanDestination{}
	for _, line := range out.lines {
		item := planItemView(line)
		if line.Operation == OperationTrash {
			out.plan.TrashCount++
			out.plan.Trash.Count++
			if len(out.plan.Trash.First) < PlanFirstItems {
				out.plan.Trash.First = append(out.plan.Trash.First, item)
			}
			continue
		}
		out.plan.MoveCount++
		destination := byCategory[line.Category]
		if destination == nil {
			destination = &PlanDestination{Category: line.Category, Destination: path.Dir(line.Destination), First: []PlanItemView{}}
			byCategory[line.Category] = destination
		}
		destination.Count++
		if line.Renamed {
			destination.RenamedCount++
		}
		if len(destination.First) < PlanFirstItems {
			destination.First = append(destination.First, item)
		}
	}
	out.plan.Total = out.plan.MoveCount + out.plan.TrashCount
	for _, definition := range CategoryRegistry {
		if destination := byCategory[definition.ID]; destination != nil {
			out.plan.Destinations = append(out.plan.Destinations, *destination)
		}
	}
	sort.SliceStable(changed, func(i, j int) bool {
		if changed[i].Name != changed[j].Name {
			return changed[i].Name < changed[j].Name
		}
		return changed[i].CandidateID < changed[j].CandidateID
	})
	out.plan.Changed.Total = len(changed)
	out.plan.Changed.Items = changed[:min(len(changed), PlanChangedLimit)]
	if out.plan.Changed.Items == nil {
		out.plan.Changed.Items = []PlanChanged{}
	}
	out.plan.PlanDigest = planDigest(workspaceID, batchID, binding.Digest, view.Revision, groups.Revision, out.lines, out.items, changed)
	return out, nil
}

// planDigest covers everything the plan is: the saved scope, both revisions,
// every operation with its destination, saved fingerprint and final name, and
// every file named as changed. Lines are sorted so the order of discovery cannot
// change it. It contains everything PayloadHash covers.
func planDigest(workspaceID, batchID, scope, revision, groupRevision string, lines []PreviewItem, items []PlanItem, changed []PlanChanged) string {
	rows := make([]string, 0, len(lines)+len(changed))
	for i, line := range lines {
		finalName := ""
		if line.Operation == OperationMove {
			finalName = path.Base(line.Destination)
		}
		rows = append(rows, strings.Join([]string{line.CandidateID, string(line.Operation), string(line.Category), items[i].FingerprintKey, finalName}, "|"))
	}
	for _, item := range changed {
		rows = append(rows, strings.Join([]string{item.CandidateID, "changed", item.Reason}, "|"))
	}
	sort.Strings(rows)
	hash := sha256.New()
	for _, part := range append([]string{"file-janitor-plan-v1", workspaceID, batchID, scope, revision, groupRevision}, rows...) {
		hash.Write([]byte(part))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// PlanReview is the read-only plan check behind the picture's button and its
// "renamed" and "changed since you looked" marks.
func (s *Service) PlanReview(ctx context.Context, owner, workspaceID, batchID string, query ReviewPlanQuery) (ReviewPlan, error) {
	if s == nil || s.store == nil || ctx.Err() != nil || batchID == "latest" || !managedRootGeneration.MatchString(batchID) {
		return ReviewPlan{}, ErrReviewUnavailable
	}
	destination := strings.TrimSpace(query.Destination)
	if destination != "" && destination != PlanTrashDestination {
		if _, err := LookupCategory(destination); err != nil {
			return ReviewPlan{}, ErrInvalidAction
		}
	}
	limit := query.Limit
	if limit == 0 {
		limit = PlanItemsPageNormal
	}
	if limit < 1 || limit > PlanItemsPageMax || query.Offset < 0 || len(query.CandidateIDs) > PlanItemsPageMax ||
		(len(query.CandidateIDs) > 0 && destination != "") {
		return ReviewPlan{}, ErrInvalidAction
	}
	wanted := make(map[string]struct{}, len(query.CandidateIDs))
	for _, id := range query.CandidateIDs {
		if _, duplicate := wanted[id]; duplicate || !managedRootGeneration.MatchString(id) {
			return ReviewPlan{}, ErrInvalidAction
		}
		wanted[id] = struct{}{}
	}
	state, err := s.store.loadScanState(workspaceID, true)
	if err != nil {
		return ReviewPlan{}, ErrReviewUnavailable
	}
	computed, err := s.computeReviewPlan(owner, workspaceID, batchID, state)
	if err != nil {
		return ReviewPlan{}, err
	}
	if ctx.Err() != nil {
		return ReviewPlan{}, ErrReviewUnavailable
	}
	if (query.Revision != "" && query.Revision != computed.plan.Revision) ||
		(query.GroupRevision != "" && query.GroupRevision != computed.plan.GroupRevision) {
		return ReviewPlan{}, ErrReviewConflict
	}
	plan := computed.plan
	if len(wanted) > 0 && !plan.OverLimit {
		plan.Items = []PlanItemView{}
		for _, line := range computed.lines {
			if _, asked := wanted[line.CandidateID]; asked {
				plan.Items = append(plan.Items, planItemView(line))
			}
		}
		plan.ItemsTotal = len(plan.Items)
	}
	if destination != "" && !plan.OverLimit {
		var list []PlanItemView
		for _, line := range computed.lines {
			switch {
			case destination == PlanTrashDestination && line.Operation == OperationTrash:
				list = append(list, planItemView(line))
			case destination != PlanTrashDestination && line.Operation == OperationMove && string(line.Category) == destination:
				list = append(list, planItemView(line))
			}
		}
		offset := min(query.Offset, len(list))
		end := offset + min(limit, len(list)-offset)
		plan.Items = append([]PlanItemView{}, list[offset:end]...)
		plan.ItemsTotal, plan.Limit, plan.Offset = len(list), limit, offset
	}
	return plan, nil
}
