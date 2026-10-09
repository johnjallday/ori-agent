package foldercontext

import (
	"encoding/json"
	"fmt"
)

const MaxTreeNodes = 64
const MaxFocusNodes = 8
const MaxFocusBytes = 4096

// Tree is an optional, bounded projection of genuine observed relationships.
// It is not a full inventory, a pathname resolver, or a source of read authority.
type Tree struct {
	Nodes   []TreeNode `json:"nodes"`
	Omitted int        `json:"omitted"`
}

type TreeNode struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id,omitempty"`
	Name     string `json:"name"`
	Kind     string `json:"kind"` // folder or file; no symlinks
}

// Focus names are data segments, not filesystem paths. Empty Topics means whole
// folder discussion, never permission or exclusion of other observed metadata.
type Focus struct {
	Folder string       `json:"folder"`
	Topics []FocusTopic `json:"topics"`
}

type FocusTopic struct {
	Names []string `json:"names"`
	Kind  string   `json:"kind"`
}

func (t Tree) Validate() error {
	if len(t.Nodes) > MaxTreeNodes || !count(t.Omitted) {
		return ErrInvalid
	}
	seen := map[string]TreeNode{}
	depths := map[string]int{"": 0}
	for index, node := range t.Nodes {
		if node.ID != fmt.Sprintf("entry-%d", index) || !name(node.Name) || (node.Kind != "folder" && node.Kind != "file") {
			return ErrInvalid
		}
		if node.ParentID != "" && seen[node.ParentID].Kind != "folder" {
			return ErrInvalid
		}
		depth := depths[node.ParentID] + 1
		if depth > 4 {
			return ErrInvalid
		}
		seen[node.ID], depths[node.ID] = node, depth
	}
	return nil
}

func (o Observation) ResolveFocus(ids []string) (*Focus, error) {
	if len(ids) > MaxFocusNodes {
		return nil, ErrInvalid
	}
	focus := &Focus{Folder: o.Folder, Topics: []FocusTopic{}}
	if len(ids) == 0 {
		return focus, nil
	}
	if o.Tree == nil || o.Tree.Validate() != nil {
		return nil, ErrInvalid
	}
	nodes := map[string]TreeNode{}
	paths := map[string][]string{}
	labels := map[string]int{}
	for _, node := range o.Tree.Nodes {
		nodes[node.ID] = node
		paths[node.ID] = append(append([]string{}, paths[node.ParentID]...), node.Name)
		encoded, _ := json.Marshal(paths[node.ID])
		labels[string(encoded)]++
	}
	seen := map[string]bool{}
	for _, id := range ids {
		node, exists := nodes[id]
		encoded, _ := json.Marshal(paths[id])
		if !exists || seen[id] || labels[string(encoded)] != 1 {
			return nil, ErrInvalid
		}
		seen[id] = true
		focus.Topics = append(focus.Topics, FocusTopic{Names: append([]string{}, paths[id]...), Kind: node.Kind})
	}
	encoded, err := json.Marshal(focus)
	if err != nil || len(encoded) > MaxFocusBytes {
		return nil, ErrInvalid
	}
	return focus, nil
}

// CloneTree avoids exposing mutable held evidence to clients of the service.
func (o Observation) CloneTree() *Tree {
	if o.Tree == nil {
		return nil
	}
	return &Tree{Nodes: append([]TreeNode{}, o.Tree.Nodes...), Omitted: o.Tree.Omitted}
}
