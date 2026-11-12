package folderagent

import (
	"fmt"
	"path"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// GroupMaxMembers is how many member workspaces one Set it up creates (the
// structure recipe's own limit); further projects are added later.
const GroupMaxMembers = 24

// Plan line kinds of the Group stage.
const (
	FolderPlanPromote  = "promote"
	FolderPlanExisting = "existing_member"
	FolderPlanMore     = "more_members"
	FolderPlanSoftware = "software_members"
)

// GroupMember is one project below the fed folder as the Group stage treats
// it: created as an anchored member, or included because a workspace already
// anchors it.
type GroupMember struct {
	Path     string        `json:"path"`
	Name     string        `json:"name"`
	Shape    string        `json:"shape,omitempty"`
	Existing *WorkspaceRef `json:"existing,omitempty"`
}

// GroupMembers are the members one Set it up handles, in walk order, at most
// GroupMaxMembers, and how many more the map holds.
func GroupMembers(rec Record) ([]GroupMember, int) {
	var out []GroupMember
	for _, p := range rec.Facts.Members {
		if len(out) == GroupMaxMembers {
			return out, len(rec.Facts.Members) - GroupMaxMembers
		}
		member := GroupMember{Path: p.Path, Name: path.Base(p.Path), Shape: string(p.Shape)}
		if ref, ok := rec.ExistingMembers[p.Path]; ok {
			existing := ref
			member.Existing = &existing
		}
		out = append(out, member)
	}
	return out, 0
}

// groupLines are the Group stage's plan lines (PRD FR34, FR36).
func groupLines(rec Record) []personalassistant.FolderPlanLine {
	members, more := GroupMembers(rec)
	turns := "Turns " + rec.WorkspaceName + " into a group"
	lines := []personalassistant.FolderPlanLine{{Kind: FolderPlanPromote, Name: turns, Outcome: turns,
		Detail: "Same workspace, same place on the map; " + rec.AgentName + " becomes its Group Manager."}}
	created := 0
	for _, m := range members {
		if m.Existing == nil {
			created++
		}
	}
	// One outcome item for every workspace the run creates.
	creates := fmt.Sprintf("Creates %d %s", created, plural(created, "workspace", "workspaces"))
	software := 0
	for _, m := range members {
		if m.Existing != nil {
			lines = append(lines, personalassistant.FolderPlanLine{Kind: FolderPlanExisting, Name: m.Existing.Name,
				Detail: "already a workspace · included as a member"})
			continue
		}
		detail := "linked to its own folder"
		if m.Shape == string(folderdigest.ShapeCode) {
			software++
		}
		lines = append(lines, personalassistant.FolderPlanLine{Kind: personalassistant.FolderPlanMember,
			Name: "Creates " + m.Name + ", " + detail, Outcome: creates})
	}
	if software > 0 {
		lines = append(lines, personalassistant.FolderPlanLine{Kind: FolderPlanSoftware,
			Name:   fmt.Sprintf("%d software %s set up when opened", software, plural(software, "project is", "projects are")),
			Detail: "Each one gets its own reviewed setup the first time you open it."})
	}
	if more > 0 {
		lines = append(lines, personalassistant.FolderPlanLine{Kind: FolderPlanMore,
			Name: fmt.Sprintf("%d more, add them later", more)})
	}
	return lines
}
