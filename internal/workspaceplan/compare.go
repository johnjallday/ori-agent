package workspaceplan

import (
	"slices"
	"sort"
)

// Version comparison (FR-35, FR-36).
//
// The comparison exists so a reviewer can answer one question before approving
// a revision: what changed since the version I already looked at? That means it
// has to distinguish four things that a naive text diff blurs together —
// something was added, something was removed, something moved, and something's
// content changed — because they carry different risk. A reordered item is a
// changed execution order; a removed item is work that will not happen.
//
// Comparison is done over stable ids, so it survives reordering: an item that
// moved is reported as moved, not as one removal plus one addition.

// ChangeKind classifies one difference between two versions.
type ChangeKind string

const (
	ChangeAdded    ChangeKind = "added"
	ChangeRemoved  ChangeKind = "removed"
	ChangeMoved    ChangeKind = "moved"
	ChangeModified ChangeKind = "modified"
)

// VersionDiff is the full comparison between two retained versions.
type VersionDiff struct {
	From int `json:"from"`
	To   int `json:"to"`
	// Identical is true when nothing approval-relevant differs. Two versions
	// can differ in prose and still be identical here, which is the point:
	// prose changes do not need re-approval (FR-34).
	Identical bool `json:"identical"`

	Objective  *FieldChange  `json:"objective,omitempty"`
	InScope    []ListChange  `json:"in_scope,omitempty"`
	NonGoals   []ListChange  `json:"non_goals,omitempty"`
	Groups     []GroupChange `json:"groups,omitempty"`
	Items      []ItemChange  `json:"items,omitempty"`
	Artifacts  []EntryChange `json:"artifacts,omitempty"`
	Validation []EntryChange `json:"validations,omitempty"`
	Execution  *FieldChange  `json:"execution,omitempty"`
	// Preconditions changes are separated from the execution mode because
	// they are different decisions: one is how work runs, the other is what
	// must be true before it may (FR-33).
	Preconditions []ListChange `json:"preconditions,omitempty"`
}

// FieldChange is a single-value difference.
type FieldChange struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

// ListChange is one entry added to or removed from a list of plain values.
type ListChange struct {
	Kind  ChangeKind `json:"kind"`
	Value string     `json:"value"`
}

// GroupChange describes what happened to one task group.
type GroupChange struct {
	Kind  ChangeKind `json:"kind"`
	ID    string     `json:"id"`
	Title string     `json:"title"`
	// Fields names the group fields that differ, for a modified group.
	Fields []string `json:"fields,omitempty"`
	// FromIndex and ToIndex are set for a moved group.
	FromIndex int `json:"from_index,omitempty"`
	ToIndex   int `json:"to_index,omitempty"`
}

// ItemChange describes what happened to one task item.
type ItemChange struct {
	Kind        ChangeKind `json:"kind"`
	ID          string     `json:"id"`
	Description string     `json:"description"`
	GroupID     string     `json:"group_id,omitempty"`
	// Fields names the item fields that differ, for a modified item. Assignee
	// and dependency changes are called out by name because they change who
	// does the work and in what order (FR-36).
	Fields []string `json:"fields,omitempty"`
	// FromGroupID is set when an item moved between groups.
	FromGroupID string `json:"from_group_id,omitempty"`
	FromIndex   int    `json:"from_index,omitempty"`
	ToIndex     int    `json:"to_index,omitempty"`
}

// EntryChange describes an added, removed, or modified artifact or validation.
type EntryChange struct {
	Kind   ChangeKind `json:"kind"`
	ID     string     `json:"id"`
	Label  string     `json:"label"`
	Fields []string   `json:"fields,omitempty"`
}

// CompareVersions reports what differs between two Plan versions.
//
// It compares the canonical (approval-relevant) form, so two versions that
// differ only in prose compare as identical — a reviewer should not be asked to
// re-read a plan because someone rewrote a paragraph of explanation.
func CompareVersions(from, to *Version) VersionDiff {
	diff := VersionDiff{}
	if from == nil || to == nil {
		return diff
	}
	diff.From = from.Number
	diff.To = to.Number

	before := Canonicalize(from.Objective, from.Content, from.PolicySnapshot)
	after := Canonicalize(to.Objective, to.Content, to.PolicySnapshot)

	if before.Objective != after.Objective {
		diff.Objective = &FieldChange{Before: before.Objective, After: after.Objective}
	}
	diff.InScope = compareLists(before.InScope, after.InScope)
	diff.NonGoals = compareLists(before.NonGoals, after.NonGoals)
	if before.Execution.Mode != after.Execution.Mode {
		diff.Execution = &FieldChange{
			Before: string(before.Execution.Mode),
			After:  string(after.Execution.Mode),
		}
	}
	diff.Preconditions = compareLists(before.Execution.Preconditions, after.Execution.Preconditions)

	diff.Groups, diff.Items = compareStructure(before.Groups, after.Groups)
	diff.Artifacts = compareArtifacts(before.Artifacts, after.Artifacts)
	diff.Validation = compareValidations(before.Validations, after.Validations)

	diff.Identical = from.ContentHash != "" && from.ContentHash == to.ContentHash
	if !diff.Identical {
		diff.Identical = diff.empty()
	}
	return diff
}

func (d VersionDiff) empty() bool {
	return d.Objective == nil && d.Execution == nil &&
		len(d.InScope) == 0 && len(d.NonGoals) == 0 &&
		len(d.Groups) == 0 && len(d.Items) == 0 &&
		len(d.Artifacts) == 0 && len(d.Validation) == 0 &&
		len(d.Preconditions) == 0
}

func compareLists(before, after []string) []ListChange {
	var changes []ListChange
	for _, value := range before {
		if !slices.Contains(after, value) {
			changes = append(changes, ListChange{Kind: ChangeRemoved, Value: value})
		}
	}
	for _, value := range after {
		if !slices.Contains(before, value) {
			changes = append(changes, ListChange{Kind: ChangeAdded, Value: value})
		}
	}
	return changes
}

// compareStructure diffs the task hierarchy over stable ids, so an element that
// moved is reported as moved rather than as a removal plus an addition.
func compareStructure(before, after []canonicalGroup) ([]GroupChange, []ItemChange) {
	var groupChanges []GroupChange
	var itemChanges []ItemChange

	beforeGroups := indexGroups(before)
	afterGroups := indexGroups(after)

	for id, prior := range beforeGroups {
		if _, exists := afterGroups[id]; !exists {
			groupChanges = append(groupChanges, GroupChange{
				Kind: ChangeRemoved, ID: id, Title: prior.group.Title,
			})
		}
	}
	for id, next := range afterGroups {
		prior, exists := beforeGroups[id]
		if !exists {
			groupChanges = append(groupChanges, GroupChange{
				Kind: ChangeAdded, ID: id, Title: next.group.Title,
			})
			continue
		}
		if fields := changedGroupFields(prior.group, next.group); len(fields) > 0 {
			groupChanges = append(groupChanges, GroupChange{
				Kind: ChangeModified, ID: id, Title: next.group.Title, Fields: fields,
			})
		}
		if prior.index != next.index {
			groupChanges = append(groupChanges, GroupChange{
				Kind: ChangeMoved, ID: id, Title: next.group.Title,
				FromIndex: prior.index, ToIndex: next.index,
			})
		}
	}

	beforeItems := indexItems(before)
	afterItems := indexItems(after)

	for id, prior := range beforeItems {
		if _, exists := afterItems[id]; !exists {
			itemChanges = append(itemChanges, ItemChange{
				Kind: ChangeRemoved, ID: id,
				Description: prior.item.Description, GroupID: prior.groupID,
			})
		}
	}
	for id, next := range afterItems {
		prior, exists := beforeItems[id]
		if !exists {
			itemChanges = append(itemChanges, ItemChange{
				Kind: ChangeAdded, ID: id,
				Description: next.item.Description, GroupID: next.groupID,
			})
			continue
		}
		if fields := changedItemFields(prior.item, next.item); len(fields) > 0 {
			itemChanges = append(itemChanges, ItemChange{
				Kind: ChangeModified, ID: id,
				Description: next.item.Description, GroupID: next.groupID, Fields: fields,
			})
		}
		if prior.groupID != next.groupID || prior.index != next.index {
			itemChanges = append(itemChanges, ItemChange{
				Kind: ChangeMoved, ID: id,
				Description: next.item.Description, GroupID: next.groupID,
				FromGroupID: prior.groupID, FromIndex: prior.index, ToIndex: next.index,
			})
		}
	}

	sortGroupChanges(groupChanges)
	sortItemChanges(itemChanges)
	return groupChanges, itemChanges
}

type indexedGroup struct {
	group canonicalGroup
	index int
}

type indexedItem struct {
	item    canonicalItem
	groupID string
	index   int
}

func indexGroups(groups []canonicalGroup) map[string]indexedGroup {
	out := make(map[string]indexedGroup, len(groups))
	for i, group := range groups {
		out[group.ID] = indexedGroup{group: group, index: i}
	}
	return out
}

func indexItems(groups []canonicalGroup) map[string]indexedItem {
	out := map[string]indexedItem{}
	for _, group := range groups {
		for i, item := range group.Items {
			out[item.ID] = indexedItem{item: item, groupID: group.ID, index: i}
		}
	}
	return out
}

func changedGroupFields(before, after canonicalGroup) []string {
	var fields []string
	if before.Title != after.Title {
		fields = append(fields, "title")
	}
	if before.Outcome != after.Outcome {
		fields = append(fields, "outcome")
	}
	if !slices.Equal(before.DependsOn, after.DependsOn) {
		fields = append(fields, "depends_on")
	}
	return fields
}

// changedItemFields names each differing field. Assignee and dependency
// changes are named explicitly because they decide who does the work and in
// what order (FR-36).
func changedItemFields(before, after canonicalItem) []string {
	var fields []string
	if before.Description != after.Description {
		fields = append(fields, "description")
	}
	if before.Details != after.Details {
		fields = append(fields, "details")
	}
	if before.Assignee != after.Assignee || before.AssigneeNodeID != after.AssigneeNodeID {
		fields = append(fields, "assignee")
	}
	if !slices.Equal(before.RequiredCapabilities, after.RequiredCapabilities) {
		fields = append(fields, "required_capabilities")
	}
	if !slices.Equal(before.DependsOn, after.DependsOn) {
		fields = append(fields, "depends_on")
	}
	if before.ExpectedResult != after.ExpectedResult {
		fields = append(fields, "expected_result")
	}
	if before.Priority != after.Priority {
		fields = append(fields, "priority")
	}
	if before.ReferenceURL != after.ReferenceURL {
		fields = append(fields, "reference_url")
	}
	return fields
}

func compareArtifacts(before, after []canonicalArtifact) []EntryChange {
	beforeByID := map[string]canonicalArtifact{}
	for _, artifact := range before {
		beforeByID[artifact.ID] = artifact
	}
	afterByID := map[string]canonicalArtifact{}
	for _, artifact := range after {
		afterByID[artifact.ID] = artifact
	}

	var changes []EntryChange
	for id, prior := range beforeByID {
		if _, exists := afterByID[id]; !exists {
			changes = append(changes, EntryChange{Kind: ChangeRemoved, ID: id, Label: prior.Path})
		}
	}
	for id, next := range afterByID {
		prior, exists := beforeByID[id]
		if !exists {
			changes = append(changes, EntryChange{Kind: ChangeAdded, ID: id, Label: next.Path})
			continue
		}
		var fields []string
		if prior.Path != next.Path {
			fields = append(fields, "path")
		}
		if prior.Kind != next.Kind {
			fields = append(fields, "kind")
		}
		// Enabled decides whether a file is written at all, so it is the one
		// artifact change a reviewer most needs to see.
		if prior.Enabled != next.Enabled {
			fields = append(fields, "enabled")
		}
		if len(fields) > 0 {
			changes = append(changes, EntryChange{
				Kind: ChangeModified, ID: id, Label: next.Path, Fields: fields,
			})
		}
	}
	sortEntryChanges(changes)
	return changes
}

func compareValidations(before, after []canonicalValidation) []EntryChange {
	beforeByID := map[string]canonicalValidation{}
	for _, checkpoint := range before {
		beforeByID[checkpoint.ID] = checkpoint
	}
	afterByID := map[string]canonicalValidation{}
	for _, checkpoint := range after {
		afterByID[checkpoint.ID] = checkpoint
	}

	var changes []EntryChange
	for id, prior := range beforeByID {
		if _, exists := afterByID[id]; !exists {
			changes = append(changes, EntryChange{Kind: ChangeRemoved, ID: id, Label: prior.Title})
		}
	}
	for id, next := range afterByID {
		prior, exists := beforeByID[id]
		if !exists {
			changes = append(changes, EntryChange{Kind: ChangeAdded, ID: id, Label: next.Title})
			continue
		}
		var fields []string
		if prior.Title != next.Title {
			fields = append(fields, "title")
		}
		if prior.Expectation != next.Expectation {
			fields = append(fields, "expectation")
		}
		if prior.Required != next.Required {
			fields = append(fields, "required")
		}
		if !slices.Equal(prior.AppliesTo, next.AppliesTo) {
			fields = append(fields, "applies_to")
		}
		if len(fields) > 0 {
			changes = append(changes, EntryChange{
				Kind: ChangeModified, ID: id, Label: next.Title, Fields: fields,
			})
		}
	}
	sortEntryChanges(changes)
	return changes
}

// The sorts below make a comparison read the same on every run. A diff that
// reshuffles between refreshes is one a reviewer cannot trust.
func sortGroupChanges(changes []GroupChange) {
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].ID != changes[j].ID {
			return changes[i].ID < changes[j].ID
		}
		return changes[i].Kind < changes[j].Kind
	})
}

func sortItemChanges(changes []ItemChange) {
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].ID != changes[j].ID {
			return changes[i].ID < changes[j].ID
		}
		return changes[i].Kind < changes[j].Kind
	})
}

func sortEntryChanges(changes []EntryChange) {
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].ID != changes[j].ID {
			return changes[i].ID < changes[j].ID
		}
		return changes[i].Kind < changes[j].Kind
	})
}
