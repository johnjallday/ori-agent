package workspacearrangement

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func contractFixture() (Draft, ValidationView) {
	return Draft{Version: Version, SourceRevision: "revision-1", Goal: "Keep experiments catalog-only", Rationale: "No workspace is needed."},
		ValidationView{Kind: HomeLibrary, Revision: "revision-1", Items: map[string]bool{"item-a": true, "item-b": true},
			Handles: map[string][]string{"selection-ab": {"item-a", "item-b"}}, Collections: map[string]int64{"collection-a": 2}}
}

func TestContract_GoalsCanChangeTypedEffectsWithoutChangingInventory(t *testing.T) {
	leave, view := contractFixture()
	leave.Unchanged = Selection{Handle: "selection-ab"}
	organize, _ := contractFixture()
	organize.Goal = "Put these client projects together; choose one to work on"
	organize.Operations = []Operation{
		{Kind: CreateCollection, Name: "Client work", Members: Selection{Handle: "selection-ab"}},
		{Kind: SelectActivation, Members: Selection{IDs: []string{"item-a"}}},
	}
	for _, draft := range []Draft{leave, organize} {
		raw, err := json.Marshal(draft)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeDraft(string(raw), view); err != nil {
			t.Fatalf("valid intent refused: %s %v", raw, err)
		}
	}
	if len(leave.Operations) != 0 || len(organize.Operations) != 2 {
		t.Fatal("goals changed only prose")
	}
	// Protocol validation is not evidence that a real model chose these goals
	// or that the selected project's activation has been approved.
}

func TestContract_FrozenSelectionCanCoverThousandEntriesWithoutModelIDs(t *testing.T) {
	draft, view := contractFixture()
	view.Items = map[string]bool{}
	var ids []string
	for i := 0; i < 1000; i++ {
		id := fmt.Sprintf("item-%04d", i)
		view.Items[id] = true
		ids = append(ids, id)
	}
	view.Handles = map[string][]string{"selection-all": ids}
	draft.Operations = []Operation{
		{Kind: CreateCollection, Name: "Archive reference", Members: Selection{Handle: "selection-all"}},
		{Kind: CreateCollection, Name: "Possible releases", Members: Selection{Handle: "selection-all"}},
	}
	raw, err := json.Marshal(draft)
	if err != nil || len(raw) >= 1024 {
		t.Fatalf("intent grew with catalog: %d bytes %v", len(raw), err)
	}
	if _, err := DecodeDraft(string(raw), view); err != nil {
		t.Fatal(err)
	}
	view.Revision = "revision-2"
	if _, err := DecodeDraft(string(raw), view); !errors.Is(err, ErrInvalid) {
		t.Fatal("old selector reinterpreted against a fresh catalog")
	}
}

func TestContract_RefusesUnsupportedOrConflictingIntent(t *testing.T) {
	cases := map[string]func(*Draft, *ValidationView){
		"unknown version": func(d *Draft, _ *ValidationView) { d.Version++ },
		"stale source":    func(_ *Draft, v *ValidationView) { v.Revision = "revision-2" },
		"foreign source":  func(_ *Draft, v *ValidationView) { v.Kind = "private_child" },
		"unknown item":    func(d *Draft, _ *ValidationView) { d.Unchanged.IDs = []string{"foreign"} },
		"missing handle":  func(d *Draft, _ *ValidationView) { d.Unchanged.Handle = "expired" },
		"duplicate item":  func(d *Draft, _ *ValidationView) { d.Unchanged.IDs = []string{"item-a", "item-a"} },
		"selector and IDs": func(d *Draft, _ *ValidationView) {
			d.Unchanged = Selection{Handle: "selection-ab", IDs: []string{"item-a"}}
		},
		"bad frozen member": func(d *Draft, v *ValidationView) {
			v.Handles["selection-ab"] = []string{"foreign"}
			d.Unchanged.Handle = "selection-ab"
		},
		"too many explicit IDs": func(d *Draft, _ *ValidationView) { d.Unchanged.IDs = make([]string, MaxExplicitItems+1) },
		"raw path": func(d *Draft, _ *ValidationView) {
			d.Operations = []Operation{{Kind: CreateCollection, Name: "/Users/owner/Music"}}
		},
		"windows path": func(d *Draft, _ *ValidationView) {
			d.Operations = []Operation{{Kind: CreateCollection, Name: `C:\Music`}}
		},
		"relative path": func(d *Draft, _ *ValidationView) {
			d.Operations = []Operation{{Kind: CreateCollection, Name: "../elsewhere"}}
		},
		"move files":  func(d *Draft, _ *ValidationView) { d.Operations = []Operation{{Kind: "move_files"}} },
		"create Home": func(d *Draft, _ *ValidationView) { d.Operations = []Operation{{Kind: "create_home", Name: "Home"}} },
		"absent library": func(d *Draft, v *ValidationView) {
			v.Kind = Attachment
			d.Operations = []Operation{{Kind: CreateCollection, Name: "Music"}}
		},
		"empty group": func(d *Draft, v *ValidationView) {
			v.Kind = Attachment
			d.Operations = []Operation{{Kind: CreateGroup, Name: "Projects"}}
		},
		"group from catalog": func(d *Draft, _ *ValidationView) {
			d.Operations = []Operation{{Kind: CreateGroup, Name: "Projects", Members: Selection{IDs: []string{"item-a"}}}}
		},
		"activate attachment": func(d *Draft, v *ValidationView) {
			v.Kind = Attachment
			d.Operations = []Operation{{Kind: SelectActivation, Members: Selection{IDs: []string{"item-a"}}}}
		},
		"collection as member cycle": func(d *Draft, _ *ValidationView) {
			d.Operations = []Operation{{Kind: SetMembers, CollectionID: "collection-a", CollectionRevision: 2, Members: Selection{IDs: []string{"collection-a"}}}}
		},
		"collection revision": func(d *Draft, _ *ValidationView) {
			d.Operations = []Operation{{Kind: RenameCollection, Name: "New", CollectionID: "collection-a", CollectionRevision: 1}}
		},
		"rename with hidden members": func(d *Draft, _ *ValidationView) {
			d.Operations = []Operation{{Kind: RenameCollection, Name: "New", CollectionID: "collection-a", CollectionRevision: 2, Members: Selection{IDs: []string{"item-a"}}}}
		},
		"two edits one collection": func(d *Draft, _ *ValidationView) {
			d.Operations = []Operation{{Kind: RemoveCollection, CollectionID: "collection-a", CollectionRevision: 2}, {Kind: RemoveCollection, CollectionID: "collection-a", CollectionRevision: 2}}
		},
		"unchanged but activated": func(d *Draft, _ *ValidationView) {
			d.Unchanged.IDs = []string{"item-a"}
			d.Operations = []Operation{{Kind: SelectActivation, Members: Selection{IDs: []string{"item-a"}}}}
		},
		"uncertain but activated": func(d *Draft, _ *ValidationView) {
			d.NeedsReview = []Recommendation{{Items: Selection{IDs: []string{"item-a"}}, Reason: "Which version?"}}
			d.Operations = []Operation{{Kind: SelectActivation, Members: Selection{IDs: []string{"item-a"}}}}
		},
		"duplicate activation": func(d *Draft, _ *ValidationView) {
			op := Operation{Kind: SelectActivation, Members: Selection{IDs: []string{"item-a"}}}
			d.Operations = []Operation{op, op}
		},
		"too many operations": func(d *Draft, _ *ValidationView) { d.Operations = make([]Operation, MaxOperations+1) },
		"control character":   func(d *Draft, _ *ValidationView) { d.Goal = "goal\x00override" },
		"hidden direction":    func(d *Draft, _ *ValidationView) { d.Goal = "goal\u202eoverride" },
		"too long rationale":  func(d *Draft, _ *ValidationView) { d.Rationale = strings.Repeat("x", 2001) },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			d, v := contractFixture()
			change(&d, &v)
			if !errors.Is(d.Validate(v), ErrInvalid) {
				t.Fatalf("invalid intent accepted: %+v", d)
			}
		})
	}
}

func TestContract_DecoderRejectsAuthorityAndExpressionFields(t *testing.T) {
	draft, view := contractFixture()
	raw, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	prefix := strings.TrimSuffix(string(raw), "}")
	for name, input := range map[string]string{
		"token":          prefix + `,"confirmation_token":"yes"}`,
		"path":           prefix + `,"path":"/tmp/music"}`,
		"expression":     prefix + `,"query":"eval(all)"}`,
		"provider":       prefix + `,"provider":"music-project-management"}`,
		"completed":      prefix + `,"completed":true}`,
		"duplicate":      prefix + `,"version":1}`,
		"case alias":     strings.Replace(string(raw), `"goal"`, `"Goal"`, 1),
		"trailing":       string(raw) + `{}`,
		"null":           "null",
		"array":          "[]",
		"nested unknown": strings.Replace(string(raw), `"unchanged":{}`, `"unchanged":{"command":"create"}`, 1),
		"oversized":      strings.Repeat(" ", MaxDraftBytes) + string(raw),
		"deep":           prefix + `,"extra":` + strings.Repeat("[", 20) + `0` + strings.Repeat("]", 20) + `}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeDraft(input, view); !errors.Is(err, ErrInvalid) {
				t.Fatalf("untrusted authority accepted: %s", input)
			}
		})
	}
}

func TestContract_CreatorLimitDoesNotTruncateCatalogSelections(t *testing.T) {
	d, v := contractFixture()
	v.Kind = Attachment
	for i := 0; i < MaxGroupMembers+1; i++ {
		id := fmt.Sprintf("item-%d", i)
		v.Items[id] = true
		d.Unchanged.IDs = append(d.Unchanged.IDs, id)
	}
	if err := d.Validate(v); err != nil {
		t.Fatal("catalog-only selection was limited by creator")
	}
	d.Operations = []Operation{{Kind: CreateGroup, Name: "Plain projects", Members: d.Unchanged}}
	d.Unchanged = Selection{}
	if !errors.Is(d.Validate(v), ErrInvalid) {
		t.Fatal("oversized creation accepted or truncated")
	}
	d.Operations[0].Members.IDs = d.Operations[0].Members.IDs[:MaxGroupMembers]
	if err := d.Validate(v); err != nil {
		t.Fatal(err)
	}
}

func TestContract_BoundsExpandedMembershipIncludingRecommendations(t *testing.T) {
	d, v := contractFixture()
	v.Items = map[string]bool{}
	ids := make([]string, MaxSourceItems)
	for i := range ids {
		ids[i] = fmt.Sprintf("item-%d", i)
		v.Items[ids[i]] = true
	}
	v.Handles = map[string][]string{"all": ids}
	for i := 0; i < MaxMemberships/MaxSourceItems; i++ {
		d.NeedsReview = append(d.NeedsReview, Recommendation{Items: Selection{Handle: "all"}, Reason: "Purpose is unknown"})
	}
	if err := d.Validate(v); err != nil {
		t.Fatal(err)
	}
	d.Unchanged = Selection{IDs: []string{ids[0]}}
	if !errors.Is(d.Validate(v), ErrInvalid) {
		t.Fatal("recommendations escaped expanded membership bound")
	}
	d.NeedsReview, d.Unchanged = nil, Selection{}
	for i := 0; i < MaxMemberships/MaxSourceItems; i++ {
		d.Operations = append(d.Operations, Operation{Kind: CreateCollection, Name: fmt.Sprintf("Collection %d", i), Members: Selection{Handle: "all"}})
	}
	if err := d.Validate(v); err != nil {
		t.Fatal(err)
	}
	d.Operations = append(d.Operations, Operation{Kind: SelectActivation, Members: Selection{IDs: []string{ids[0]}}})
	if !errors.Is(d.Validate(v), ErrInvalid) {
		t.Fatal("operation membership overflow accepted")
	}
}
