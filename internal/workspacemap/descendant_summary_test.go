package workspacemap

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The two listing APIs deliberately disagree so a legacy call cannot silently
// satisfy a test intended to exercise the metadata-only path.
type descendantSummaryLister struct {
	fakeLister
	summaries    []workspace.WorkspaceSummary
	summaryErr   error
	summaryCalls int
}

func (f *descendantSummaryLister) ListActiveSummaries() ([]workspace.WorkspaceSummary, error) {
	f.summaryCalls++
	return f.summaries, f.summaryErr
}

func TestGroupNodeIDs_PrefersNativeSummaries(t *testing.T) {
	source := &descendantSummaryLister{
		fakeLister: fakeLister{workspaces: []*workspace.Workspace{ws("grp", ""), ws("stale", "grp")}},
		summaries: []workspace.WorkspaceSummary{
			{ID: "grp"},
			{ID: "child-a", ParentID: "grp"},
			{ID: "grandchild", ParentID: "child-a"},
			{ID: "child-b", ParentID: "grp"},
			{ID: "outsider"},
		},
	}
	ids, err := NewDescendantResolver(source).GroupNodeIDs(context.Background(), " grp ")
	want := []string{"grp", "child-a", "child-b", "grandchild"}
	if err != nil || !reflect.DeepEqual(ids, want) {
		t.Fatalf("GroupNodeIDs = %v, %v; want %v", ids, err, want)
	}
	if source.summaryCalls != 1 || source.calls != 0 {
		t.Fatalf("listing calls: summaries=%d, legacy=%d; want 1, 0", source.summaryCalls, source.calls)
	}
}

func TestGroupNodeIDs_NativeSummaryErrorDoesNotFallBack(t *testing.T) {
	failure := errors.New("summary source unavailable")
	source := &descendantSummaryLister{
		fakeLister: fakeLister{workspaces: []*workspace.Workspace{ws("grp", ""), ws("child", "grp")}},
		summaries:  []workspace.WorkspaceSummary{{ID: "grp"}},
		summaryErr: failure,
	}
	ids, err := NewDescendantResolver(source).GroupNodeIDs(context.Background(), "grp")
	if !errors.Is(err, failure) || ids != nil {
		t.Fatalf("GroupNodeIDs = %v, %v; want nil and wrapped summary error", ids, err)
	}
	if !strings.Contains(err.Error(), `failed to list workspaces for group "grp"`) {
		t.Fatalf("listing error lost group context: %v", err)
	}
	if source.summaryCalls != 1 || source.calls != 0 {
		t.Fatalf("listing calls: summaries=%d, legacy=%d; want 1, 0", source.summaryCalls, source.calls)
	}
}

func TestGroupNodeIDs_EmptyOrMissingNativeGroupDoesNotFallBack(t *testing.T) {
	for _, tc := range []struct {
		name      string
		summaries []workspace.WorkspaceSummary
	}{
		{name: "nil"},
		{name: "empty", summaries: []workspace.WorkspaceSummary{}},
		{name: "missing", summaries: []workspace.WorkspaceSummary{{ID: "outsider"}}},
		{name: "empty_id", summaries: []workspace.WorkspaceSummary{{ParentID: "grp"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := &descendantSummaryLister{
				fakeLister: fakeLister{workspaces: []*workspace.Workspace{ws("grp", "")}},
				summaries:  tc.summaries,
			}
			ids, err := NewDescendantResolver(source).GroupNodeIDs(context.Background(), "grp")
			if !errors.Is(err, ErrNodeNotFound) || ids != nil {
				t.Fatalf("GroupNodeIDs = %v, %v; want nil, ErrNodeNotFound", ids, err)
			}
			if source.summaryCalls != 1 || source.calls != 0 {
				t.Fatalf("listing calls: summaries=%d, legacy=%d; want 1, 0", source.summaryCalls, source.calls)
			}
		})
	}
}

func descendantListingCases() []struct {
	name string
	make func([]workspace.WorkspaceSummary) WorkspaceLister
} {
	return []struct {
		name string
		make func([]workspace.WorkspaceSummary) WorkspaceLister
	}{
		{name: "native", make: func(summaries []workspace.WorkspaceSummary) WorkspaceLister {
			return &descendantSummaryLister{
				fakeLister: fakeLister{err: errors.New("legacy listing must not be called")},
				summaries:  summaries,
			}
		}},
		{name: "legacy", make: func(summaries []workspace.WorkspaceSummary) WorkspaceLister {
			listed := []*workspace.Workspace{nil}
			for _, summary := range summaries {
				listed = append(listed, ws(summary.ID, summary.ParentID))
			}
			return &fakeLister{workspaces: listed}
		}},
	}
}

func TestGroupNodeIDs_SummaryAndLegacyHierarchyContracts(t *testing.T) {
	for _, source := range descendantListingCases() {
		t.Run(source.name, func(t *testing.T) {
			for _, tc := range []struct {
				name      string
				summaries []workspace.WorkspaceSummary
				want      []string
			}{
				{
					name: "empty_ids_and_orphans",
					summaries: []workspace.WorkspaceSummary{
						{ID: "grp"}, {ParentID: "grp"},
						{ID: "orphan", ParentID: "missing"}, {ID: "child", ParentID: "grp"},
					},
					want: []string{"grp", "child"},
				},
				{
					name: "duplicates_and_cycle",
					summaries: []workspace.WorkspaceSummary{
						{ID: "grp", ParentID: "child"}, {ID: "child", ParentID: "grp"},
						{ID: "child", ParentID: "grp"}, {ID: "grp", ParentID: "child"},
					},
					want: []string{"grp", "child"},
				},
				{name: "group_only", summaries: []workspace.WorkspaceSummary{{ID: "grp"}}, want: []string{"grp"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					ids, err := NewDescendantResolver(source.make(tc.summaries)).GroupNodeIDs(context.Background(), "grp")
					if err != nil || !reflect.DeepEqual(ids, tc.want) {
						t.Fatalf("GroupNodeIDs = %v, %v; want %v", ids, err, tc.want)
					}
				})
			}
		})
	}
}

func TestGroupNodeIDs_SummaryAndLegacyClusterSizeLimit(t *testing.T) {
	for _, source := range descendantListingCases() {
		t.Run(source.name, func(t *testing.T) {
			for _, size := range []int{MaxPositionsPerOperation, MaxPositionsPerOperation + 1} {
				t.Run(fmt.Sprint(size), func(t *testing.T) {
					summaries := []workspace.WorkspaceSummary{{ID: "grp"}}
					for i := 1; i < size; i++ {
						summaries = append(summaries, workspace.WorkspaceSummary{ID: fmt.Sprintf("child-%d", i), ParentID: "grp"})
					}
					// A large listing outside this group does not consume its budget.
					for i := 0; i < MaxPositionsPerOperation; i++ {
						summaries = append(summaries, workspace.WorkspaceSummary{ID: fmt.Sprintf("outside-%d", i)})
					}
					ids, err := NewDescendantResolver(source.make(summaries)).GroupNodeIDs(context.Background(), "grp")
					if size > MaxPositionsPerOperation {
						if !errors.Is(err, ErrPatchTooLarge) || ids != nil {
							t.Fatalf("oversized cluster = %v, %v; want nil, ErrPatchTooLarge", ids, err)
						}
					} else if err != nil || len(ids) != size {
						t.Fatalf("boundary cluster size = %d, %v; want %d", len(ids), err, size)
					}
				})
			}
		})
	}
}

func TestGroupNodeIDs_InvalidIDSkipsBothListings(t *testing.T) {
	for _, id := range []string{"", " \t ", "bad\x00id"} {
		t.Run(fmt.Sprintf("%q", id), func(t *testing.T) {
			source := &descendantSummaryLister{}
			_, err := NewDescendantResolver(source).GroupNodeIDs(context.Background(), id)
			if !errors.Is(err, ErrInvalidNodeID) {
				t.Fatalf("error = %v; want ErrInvalidNodeID", err)
			}
			if source.summaryCalls != 0 || source.calls != 0 {
				t.Fatalf("invalid ID performed listing: summaries=%d, legacy=%d", source.summaryCalls, source.calls)
			}
		})
	}
}

func TestGroupNodeIDs_LegacyErrorsAndRecordsArePreserved(t *testing.T) {
	failure := errors.New("legacy listing unavailable")
	failed := &fakeLister{err: failure}
	if _, err := NewDescendantResolver(failed).GroupNodeIDs(context.Background(), "grp"); !errors.Is(err, failure) {
		t.Fatalf("legacy error = %v; want wrapped %v", err, failure)
	}
	root := ws("grp", "")
	root.Tasks = []workspace.Task{{ID: "task"}}
	root.SharedData = map[string]any{"payload": "preserved"}
	child := ws("child", "grp")
	source := &fakeLister{workspaces: []*workspace.Workspace{nil, ws("", "grp"), root, child}}
	ids, err := NewDescendantResolver(source).GroupNodeIDs(context.Background(), "grp")
	if err != nil || !reflect.DeepEqual(ids, []string{"grp", "child"}) || source.calls != 1 {
		t.Fatalf("legacy resolution = %v, %v; listing calls=%d", ids, err, source.calls)
	}
	if root.ParentID != "" || child.ParentID != "grp" || root.Tasks[0].ID != "task" || root.SharedData["payload"] != "preserved" {
		t.Fatal("read-only resolution changed workspace records")
	}
}

func TestGroupNodeIDs_FollowsLiveStoreHierarchy(t *testing.T) {
	for _, mode := range []string{"memory", "composed", "legacy_wrapper"} {
		t.Run(mode, func(t *testing.T) {
			primary := workspace.NewInMemoryStore()
			var store workspace.Store = primary
			switch mode {
			case "composed":
				store = workspace.NewSyncStore(primary, nil)
			case "legacy_wrapper":
				store = struct{ workspace.Store }{primary}
			}
			for _, record := range []*workspace.Workspace{
				ws("grp", ""), ws("child", "grp"), ws("grandchild", "child"),
				ws("other", ""), ws("outsider", "other"), ws("trashed", "grp"),
			} {
				record.Status = workspace.StatusActive
				if record.ID == "trashed" {
					record.Status = workspace.StatusTrashed
				}
				if err := store.Save(record); err != nil {
					t.Fatal(err)
				}
			}
			resolver := NewDescendantResolver(store)
			before, err := resolver.GroupNodeIDs(context.Background(), "grp")
			if err != nil || !reflect.DeepEqual(before, []string{"grp", "child", "grandchild"}) {
				t.Fatalf("initial hierarchy = %v, %v", before, err)
			}
			for id, parent := range map[string]string{"child": "other", "outsider": "grp"} {
				if err := store.Update(id, func(record *workspace.Workspace) error {
					record.ParentID = parent
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			after, err := resolver.GroupNodeIDs(context.Background(), "grp")
			if err != nil || !reflect.DeepEqual(after, []string{"grp", "outsider"}) {
				t.Fatalf("reparented hierarchy = %v, %v; want current membership", after, err)
			}
		})
	}
}

func TestTranslateGroup_SummaryFailureLeavesLayoutUnchanged(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	seedWorkspace(t, db, "grp", "child")
	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{SetPositions(map[string]Point{
		"grp": {X: 10, Y: 10}, "child": {X: 50, Y: 50},
	})}}); err != nil {
		t.Fatal(err)
	}
	before, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("incomplete summary source")
	source := &descendantSummaryLister{
		fakeLister: fakeLister{workspaces: []*workspace.Workspace{ws("grp", ""), ws("child", "grp")}},
		summaries:  []workspace.WorkspaceSummary{{ID: "grp"}},
		summaryErr: failure,
	}
	store.SetDescendantResolver(NewDescendantResolver(source))
	_, err = store.Apply(ctx, "local", Patch{Operations: []Operation{TranslateGroup("grp", Point{X: 38, Y: 0})}})
	if !errors.Is(err, failure) {
		t.Fatalf("cluster move error = %v; want wrapped summary failure", err)
	}
	after, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || source.calls != 0 {
		t.Fatalf("failed summary read changed the layout or fell back: before=%+v, after=%+v, legacy calls=%d", before, after, source.calls)
	}
}
