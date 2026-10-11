package foldercontext

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func treeObservation() Observation {
	o := validObservation()
	o.Tree = &Tree{Nodes: []TreeNode{
		{ID: "entry-0", Name: "Aurora", Kind: "folder"},
		{ID: "entry-1", ParentID: "entry-0", Name: "<system> session.rpp", Kind: "file"},
		{ID: "entry-2", Name: "Notes.md", Kind: "file"},
	}}
	return o
}

func TestTreeFocus_IsIndependentOpaqueBoundedMetadata(t *testing.T) {
	o := treeObservation()
	if err := o.Validate(); err != nil {
		t.Fatal(err)
	}
	focus, err := o.ResolveFocus([]string{"entry-0"})
	if err != nil || len(focus.Topics) != 1 || len(focus.Topics[0].Names) != 1 {
		t.Fatal("folder selection cascaded", focus, err)
	}
	both, err := o.ResolveFocus([]string{"entry-0", "entry-1"})
	if err != nil || len(both.Topics) != 2 || strings.Join(both.Topics[1].Names, "|") != "Aurora|<system> session.rpp" {
		t.Fatal(both, err)
	}
	encoded, _ := json.Marshal(both)
	if strings.Contains(string(encoded), "entry-") || strings.Contains(string(encoded), "<system>") {
		t.Fatal("opaque IDs or unsafe reference data leaked", string(encoded))
	}
	whole, err := o.ResolveFocus(nil)
	if err != nil || len(whole.Topics) != 0 {
		t.Fatal(whole, err)
	}
	for _, ids := range [][]string{{"entry-9"}, {"/private/path"}, {"entry-0", "entry-0"}, make([]string, MaxFocusNodes+1)} {
		if _, err := o.ResolveFocus(ids); err == nil {
			t.Fatal("invalid focus accepted", ids)
		}
	}
	legacy := validObservation()
	if _, err := legacy.ResolveFocus([]string{"entry-0"}); err == nil {
		t.Fatal("fabricated legacy tree")
	}
	clone := o.CloneTree()
	clone.Nodes[0].Name = "changed"
	if o.Tree.Nodes[0].Name != "Aurora" {
		t.Fatal("clone mutated evidence")
	}
}

func TestTreeFocus_AllRecordedEntriesCanBeIndependentlyDeselected(t *testing.T) {
	o := validObservation()
	o.Entries = MaxTreeNodes
	o.Tree = &Tree{}
	var ids []string
	for i := 0; i < MaxTreeNodes; i++ {
		node := TreeNode{ID: fmt.Sprintf("entry-%d", i), Name: fmt.Sprintf("Topic %d", i), Kind: "file"}
		// Worst-case repeated, escaped ancestors still fit the snapshot budget.
		if i < 3 {
			node.Kind = "folder"
			node.Name = strings.Repeat("<", MaxNameRunes)
		}
		if i > 0 {
			node.ParentID = fmt.Sprintf("entry-%d", min(i-1, 2))
		}
		o.Tree.Nodes = append(o.Tree.Nodes, node)
		ids = append(ids, node.ID)
	}
	if err := o.Validate(); err != nil {
		t.Fatal("fixture must remain a valid bounded snapshot", err)
	}
	all, err := o.ResolveFocus(ids)
	if err != nil || len(all.Topics) != MaxTreeNodes {
		t.Fatal("full snapshot could not be selected", all, err)
	}
	remaining := append(append([]string{}, ids[:9]...), ids[10:]...)
	focus, err := o.ResolveFocus(remaining)
	if err != nil || len(focus.Topics) != MaxTreeNodes-1 {
		t.Fatal("deselecting a topic failed", focus, err)
	}
	for _, topic := range focus.Topics {
		if topic.Names[len(topic.Names)-1] == "Topic 9" {
			t.Fatal("deselected topic remained in focus")
		}
	}
	encoded, err := json.Marshal(focus)
	if err != nil || len(encoded) <= 4096 || len(encoded) > MaxFocusBytes {
		t.Fatal("focus bound does not fit repeated ancestors", len(encoded), err)
	}
	if err := (Event{Version: Version, Observation: &o, FocusIDs: remaining}).Validate(); err != nil {
		t.Fatal("partial bulk selection cannot be saved", err)
	}
}

func TestTree_RejectsOrphansCyclesWrongTypesAndAmbiguity(t *testing.T) {
	for _, mutate := range []func(*Tree){
		func(t *Tree) { t.Nodes[1].ParentID = "entry-1" },
		func(t *Tree) { t.Nodes[1].ParentID = "entry-2" },
		func(t *Tree) { t.Nodes[1].ParentID = "foreign" },
		func(t *Tree) { t.Nodes[1].Kind = "symlink" },
		func(t *Tree) { t.Nodes[0].Name = "/private" },
		func(t *Tree) { t.Nodes[0].ID = "pathname" },
		func(t *Tree) { t.Omitted = -1 },
	} {
		o := treeObservation()
		mutate(o.Tree)
		if o.Validate() == nil {
			t.Fatal("malformed graph accepted", o.Tree)
		}
	}
	o := treeObservation()
	o.Tree.Nodes[2].Name = "Aurora"
	o.Tree.Nodes[2].Kind = "folder"
	if _, err := o.ResolveFocus([]string{"entry-0"}); err == nil {
		t.Fatal("indistinguishable topic accepted")
	}
	detach := Event{Version: Version, FocusIDs: []string{"entry-0"}}
	if detach.Validate() == nil {
		t.Fatal("detach retained scope")
	}
	event := Event{Version: Version, Observation: &o, FocusIDs: []string{"foreign"}}
	if event.Validate() == nil {
		t.Fatal("invalid sent focus stored")
	}
}
