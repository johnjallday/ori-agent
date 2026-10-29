package server

import (
	"context"
	"net/url"
	"strings"

	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// handoffReceipts is a single song's receipt (FR17), read back from canonical
// state once the run finished: the Home, its Portfolio Manager, the song listed
// in the Home's library, the song's workspace the manager opened, the shared
// assistant on it, and its first task. Nothing is copied from the plan.
type handoffReceipts struct {
	host *folderSetupHost
	// song is the song folder's name; label the blueprint's name for one song.
	song  string
	label string
	// opener kept the first-task row its seeder read back.
	opener *handoffOpener
}

func (r handoffReceipts) Receipt(_ context.Context, homeID string, facts foldersetup.PortfolioReceiptFacts) ([]personalassistant.FolderReceiptRow, error) {
	b := r.host.builder
	home, err := b.workspaceStore.Get(homeID)
	if err != nil || home == nil || !workspace.IsCanonicalWorkspaceSlug(home.FolderSlug) {
		return nil, errSetupUnavailable
	}
	detail, operation := "your Home", "reuse"
	if facts.HomeCreated {
		detail, operation = "created", "create"
	}
	rows := []personalassistant.FolderReceiptRow{{
		Kind: "home", Name: home.Name, Detail: detail, Operation: operation,
		Route: "/workspaces/" + url.PathEscape(home.FolderSlug) + "/assistant#projectLibraryPanel",
	}}
	manager := managerName(home)
	if name := homeManagerAgent(home); name != "" {
		added := facts.HomeCreated
		for _, agent := range facts.AddedAgents {
			added = added || strings.EqualFold(agent, name)
		}
		row := personalassistant.FolderReceiptRow{Kind: "agent", Name: name, Detail: manager + ", already there", Assigned: true}
		if added {
			row.Detail = manager + ", added"
		}
		if _, model, found := b.sessionHandler.SavedAgentModel(name); found {
			row.Model = personalassistant.FolderReceiptModel(model)
		}
		rows = append(rows, row)
	}
	rows = append(rows, personalassistant.FolderReceiptRow{Kind: "library", Name: r.song + " listed in the Home's library",
		Detail: "names and project files only"})
	if home.GetAssistantProgramState().GetSongDetailsConsent().Active() {
		rows[len(rows)-1].Detail = "names, project files, and its tempo, length and track count"
	}
	if opened := facts.Opened; opened != nil {
		// The song is read where the verifier reads it: its own folder record.
		song, err := b.workspaceFileStore.Get(opened.WorkspaceID)
		if err != nil || song == nil || song.ParentID != homeID || !workspace.IsCanonicalWorkspaceSlug(song.FolderSlug) {
			return nil, errSetupUnavailable
		}
		label := strings.TrimSpace(r.label)
		if label == "" {
			label = "project"
		}
		songOperation := "reuse"
		if opened.Created {
			songOperation = "create"
		}
		rows = append(rows, personalassistant.FolderReceiptRow{Kind: "workspace", Name: song.Name,
			Detail: label + " workspace, opened by your " + manager, Operation: songOperation,
			Route: "/workspaces/" + url.PathEscape(song.FolderSlug)})
		if name := strings.TrimSpace(opened.AgentName); name != "" {
			row := personalassistant.FolderReceiptRow{Kind: "agent", Name: name, Assigned: true,
				Detail: "already on it"}
			switch opened.Staffing {
			case "added":
				row.Detail = "added, shared with the songs you open in this Home"
			case "joined":
				row.Detail = "joined, shared with the songs you open in this Home"
			}
			if _, model, found := b.sessionHandler.SavedAgentModel(name); found {
				row.Model = personalassistant.FolderReceiptModel(model)
			}
			rows = append(rows, row)
		}
		if task := r.firstTask(opened.WorkspaceID); task != nil {
			rows = append(rows, *task)
		}
	}
	if row, ok := profileReceiptRow(home); ok {
		rows = append(rows, row)
	}
	return rows, nil
}

// firstTask is the song's first-task row as the seeder read it back, or as the
// workspace holds it now.
func (r handoffReceipts) firstTask(songID string) *personalassistant.FolderReceiptRow {
	if r.opener != nil && r.opener.task != nil {
		task := *r.opener.task
		return &task
	}
	rows, err := r.host.builder.sessionHandler.FolderOfferWorkspaceReceipt(songID, true)
	if err != nil {
		return nil
	}
	for _, row := range rows {
		if row.Kind == "task" {
			return &row
		}
	}
	return nil
}

// homeManagerAgent is the agent filling the Home's primary Home role.
func homeManagerAgent(home *workspace.Workspace) string {
	state := home.GetAssistantProgramState()
	if state == nil || state.Declaration == nil {
		return ""
	}
	for _, role := range state.Declaration.Roles {
		if role.Scope != workspace.AssistantRoleScopeHome || !role.Primary {
			continue
		}
		for _, binding := range state.HomeBindings.Bindings {
			if binding.RoleID == role.ID && strings.TrimSpace(binding.AgentName) != "" {
				return strings.TrimSpace(binding.AgentName)
			}
		}
	}
	return ""
}
