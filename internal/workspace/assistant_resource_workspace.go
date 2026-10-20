package workspace

import (
	"errors"
	"strings"
)

// ErrResourceWorkspaceExists means the Home already has an active resource
// workspace for that attachment. v1 allows one per Home.
var ErrResourceWorkspaceExists = errors.New("this Home already has that resource workspace")

// ResourceWorkspaceExistsError names the existing workspace, so a caller can
// open it instead of creating a second one.
type ResourceWorkspaceExistsError struct {
	WorkspaceID string
}

func (e *ResourceWorkspaceExistsError) Error() string { return ErrResourceWorkspaceExists.Error() }

func (e *ResourceWorkspaceExistsError) Unwrap() error { return ErrResourceWorkspaceExists }

// SameResourceAttachment reports whether two resource provenances come from
// the same provider blueprint and team, which is what one attachment allows.
func SameResourceAttachment(left, right *AssistantProjectProviderOwner) bool {
	return left != nil && right != nil && left.IsResource() && right.IsResource() &&
		strings.EqualFold(left.PluginID, right.PluginID) && left.BlueprintID == right.BlueprintID &&
		left.ProjectTeamID == right.ProjectTeamID
}

// LinkedSongProjects is LinkedProjects without resource workspaces: every
// count, stage, reflection and learning rule that means "songs" uses it.
// LinkedProjects stays the membership list.
func (service *AssistantProgramStore) LinkedSongProjects(stationID string) ([]*Workspace, error) {
	linked, err := service.LinkedProjects(stationID)
	if err != nil {
		return nil, err
	}
	songs := linked[:0]
	for _, project := range linked {
		if !AssistantProjectLinkIsResource(project.GetAssistantProjectLink()) {
			songs = append(songs, project)
		}
	}
	return songs, nil
}

// ActiveResourceWorkspace returns the Home's active linked resource workspace
// for the attachment owner describes, or nil when it has none.
func (service *AssistantProgramStore) ActiveResourceWorkspace(stationID string, owner *AssistantProjectProviderOwner) (*Workspace, error) {
	linked, err := service.LinkedProjects(stationID)
	if err != nil {
		return nil, err
	}
	for _, project := range linked {
		if link := project.GetAssistantProjectLink(); link != nil && SameResourceAttachment(link.ProjectProvider, owner) {
			return project, nil
		}
	}
	return nil, nil
}

// SongProjectIDs filters a Home's linked IDs to the ones that are not
// resource workspaces. A child that cannot be read is kept, which matches how
// the existing counts treat it; only a positively marked resource is removed.
func SongProjectIDs(store Store, ids []string) []string {
	songs := make([]string, 0, len(ids))
	for _, id := range ids {
		if store != nil {
			if project, err := store.Get(id); err == nil && project != nil && AssistantProjectLinkIsResource(project.GetAssistantProjectLink()) {
				continue
			}
		}
		songs = append(songs, id)
	}
	return songs
}
