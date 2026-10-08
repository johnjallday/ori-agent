package agenthttp

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Each turn owns its registry and sources. No workspace Tools() bundle,
// project-agent principal, credential, shell or mutation is available here.
type modelToolRegistry interface {
	Definitions() []llm.Tool
	Execute(context.Context, string, string) (string, error)
}

type panelToolRegistry struct {
	handler *HomeAssistantAskHandler
	turn    *assistantWorkspaceTurn
	home    *homeToolRegistry
	// ledger is the turn's one evidence budget and source record, shared by
	// every tool call in every round.
	ledger *evidenceLedger
}

func (r *panelToolRegistry) Definitions() []llm.Tool {
	tools := r.home.Definitions()
	discovery := "Discover owned project and Home/group metadata with separate project/group totals. No content reads or permission grants."
	if r.turn != nil && r.turn.projection.Subject != nil {
		// With a workspace pinned, the workspace listing is that workspace's
		// own: for a Home or group, its projects; for a project, only itself,
		// which the overview already carries, so the tool is not offered there
		// (a model sent to it gets nothing new and spends a full model call).
		// Its wording says what it lists here, so "what do I have" asked on a
		// Home is answered with the Home's projects, not the whole app.
		home := r.turn.projection.Subject.Kind == "group" || r.turn.projection.Subject.Kind == "home"
		tools = slices.DeleteFunc(tools, func(tool llm.Tool) bool { return tool.Name == "home_workspaces" && !home })
		for i := range tools {
			if tools[i].Name == "home_workspaces" {
				tools[i].Description = "List the projects in the current Home or group, the workspace this turn is about, with each one's status, agent count and open tasks. It does not list the rest of the app. Read-only."
			}
		}
		discovery = "List projects and Homes/groups across the whole app, beyond the current workspace, with separate totals. Use it only when the user asks about another workspace or about everything they have in Ori: the current workspace, and for a Home its own projects, are already in the overview. No content reads or permission grants."
	}
	tools = append(tools, llm.Tool{Name: "assistant_workspace_discovery", Description: discovery, Parameters: map[string]any{"type": "object", "properties": map[string]any{}}})
	return append(append(tools, r.readerDefinitions()...), r.fileReaderDefinitions()...)
}

func (r *panelToolRegistry) Execute(ctx context.Context, name, arguments string) (string, error) {
	if err := r.handler.revalidateWorkspaceTurn(ctx, r.turn); err != nil {
		return "", err
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(arguments), &args); err != nil && strings.TrimSpace(arguments) != "" {
		return "", errors.New("invalid tool arguments")
	}
	if args == nil {
		args = map[string]any{}
	}
	// A model may name records within the pinned scope, never change that scope.
	if id := homeToolString(args, "workspace_id"); id != "" {
		if r.turn.projection.Subject != nil && id != r.turn.projection.Subject.ID {
			return "", errors.New("workspace outside accepted scope")
		}
		ws, err := r.handler.WorkspaceContext.Source.Get(id)
		if err != nil || !workspaceReadable(ws, r.turn.userID) {
			return "", errors.New("workspace unavailable")
		}
	}
	if isReader(name) || isFileReader(name) {
		// Readers bound, filter and charge their own output: a note or file
		// body is longer than a metadata field and is recorded as a source.
		var data map[string]any
		var err error
		started := time.Now()
		if isFileReader(name) {
			data, err = r.executeFileReader(name, args)
		} else {
			data, err = r.executeReader(ctx, name, args)
		}
		if err != nil {
			r.ledger.observe("failed", time.Since(started))
			return "", errors.New("source unavailable")
		}
		encoded, err := homeToolJSON(data)
		if err != nil {
			r.ledger.observe("failed", time.Since(started))
			return "", errors.New("source unavailable")
		}
		if data["content_read"] != true {
			// A listing is for finding a source, so it may not use up the budget
			// needed to read one. Too long, it is shortened rather than withheld.
			limit := min(assistantcontext.ListingLimit, r.ledger.remaining())
			if evidenceSize(encoded) > limit {
				shorter, fits := fitListing(data, limit)
				if !fits || !r.ledger.charge(evidenceSize(shorter)) {
					r.ledger.observe("over_budget", time.Since(started))
					return `{"status":"partial","reason":"evidence_budget_exhausted","content_read":false}`, nil
				}
				r.ledger.observe(string(assistantcontext.Partial), time.Since(started))
				return shorter, nil
			}
			if !r.ledger.charge(evidenceSize(encoded)) {
				r.ledger.observe("over_budget", time.Since(started))
				return `{"status":"partial","reason":"evidence_budget_exhausted","content_read":false}`, nil
			}
		}
		outcome, _ := data["status"].(assistantcontext.Availability)
		r.ledger.observe(string(outcome), time.Since(started))
		return encoded, nil
	}
	args["limit"] = assistantcontext.PreviewLimit
	var result string
	var err error
	if name == "assistant_workspace_discovery" {
		data := r.handler.WorkspaceContext.Resolve(ctx, r.turn.userID, "", &HomeAssistantRouteContext{})
		data.Overview, data.Location, data.Subject = nil, nil, nil
		result, err = homeToolJSON(data)
	} else {
		encoded, marshalErr := json.Marshal(args)
		if marshalErr != nil {
			return "", errors.New("invalid tool arguments")
		}
		result, err = r.home.Execute(ctx, name, string(encoded))
	}
	if err != nil {
		return "", errors.New("source unavailable")
	}
	// JSON strings are reference data. Bound/sanitize before provider input;
	// unknown tool errors and source errors never expose filesystem paths.
	var data any
	if json.Unmarshal([]byte(result), &data) != nil {
		return "", errors.New("source unavailable")
	}
	result, err = homeToolJSON(sanitizePanelData(data))
	if err != nil {
		return "", errors.New("source unavailable")
	}
	if !r.ledger.charge(utf8.RuneCountInString(result)) {
		return `{"status":"partial","reason":"evidence_budget_exhausted","content_read":false}`, nil
	}
	return result, nil
}

func sanitizePanelData(value any) any {
	switch item := value.(type) {
	case string:
		return workspaceContextText(item, 1200)
	case []any:
		for i := range item {
			item[i] = sanitizePanelData(item[i])
		}
		return item
	case map[string]any:
		for key, nested := range item {
			item[key] = sanitizePanelData(nested)
		}
		return item
	default:
		return value
	}
}

// Embedded Store is not advertised to the model. All reads used by the home
// snapshot/registry are overridden and freshly checked; mutations remain in
// the separate existing explicit-confirmation path, never this adapter.
type panelWorkspaceStore struct {
	workspace.Store
	handler *HomeAssistantAskHandler
	turn    *assistantWorkspaceTurn
	ctx     context.Context
}

// pinnedHome reports whether the turn is about a Home or group. A Home is about
// its own projects as well as itself, so with one pinned the listings (the
// workspaces, their agents, their task titles and states) cover the Home and its
// direct projects. A pinned project covers only itself. Reading a note, a task's
// detail or a file stays on the pinned workspace in every case.
func (s panelWorkspaceStore) pinnedHome() bool {
	subject := s.turn.projection.Subject
	return subject != nil && (subject.Kind == "group" || subject.Kind == "home")
}

// childIDs lists the pinned Home's direct projects that belong to this user.
func (s panelWorkspaceStore) childIDs(parent string) ([]string, error) {
	listed, err := workspace.ListActiveSummaries(s.Store)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, row := range listed {
		if row.ParentID == parent && row.ID != parent && normalizedWorkspaceOwner(row.OwnerUserID) == s.turn.userID &&
			row.Status != workspace.StatusTrashed && row.Status != workspace.StatusMissing {
			ids = append(ids, row.ID)
		}
	}
	slices.Sort(ids)
	return ids, nil
}

func (s panelWorkspaceStore) Get(id string) (*workspace.Workspace, error) {
	if err := s.handler.revalidateWorkspaceTurn(s.ctx, s.turn); err != nil {
		return nil, err
	}
	subject := s.turn.projection.Subject
	if subject != nil && id != subject.ID && !s.pinnedHome() {
		return nil, errors.New("workspace outside accepted scope")
	}
	ws, err := s.Store.Get(id)
	if err != nil || !workspaceReadable(ws, s.turn.userID) {
		return nil, errors.New("workspace unavailable")
	}
	// Read fresh: a project is the pinned Home's only while it is still its
	// direct child. A sibling Home's project, or one moved away, is not.
	if subject != nil && id != subject.ID && ws.ParentID != subject.ID {
		return nil, errors.New("workspace outside accepted scope")
	}
	// Construct metadata only; do not copy the Workspace mutex or retain file,
	// runtime, toolbox or MCP configuration in this read-only projection.
	copy := &workspace.Workspace{ID: ws.ID, Name: workspaceContextText(ws.Name, 120), Description: workspaceContextText(ws.Description, 800),
		FolderSlug: ws.FolderSlug, Kind: ws.Kind, ParentID: ws.ParentID, OwnerUserID: ws.OwnerUserID,
		Status: ws.Status, Version: ws.Version, CreatedAt: ws.CreatedAt, UpdatedAt: ws.UpdatedAt,
		AgentInstances: ws.AgentInstances}
	for _, task := range ws.Tasks {
		if task.WorkspaceID != "" && task.WorkspaceID != ws.ID {
			continue
		}
		task.Description = workspaceContextText(task.Description, 320)
		copy.Tasks = append(copy.Tasks, task)
	}
	return copy, nil
}
func (s panelWorkspaceStore) List() ([]string, error) {
	if err := s.handler.revalidateWorkspaceTurn(s.ctx, s.turn); err != nil {
		return nil, err
	}
	if subject := s.turn.projection.Subject; subject != nil {
		ids := []string{subject.ID}
		if s.pinnedHome() {
			children, err := s.childIDs(subject.ID)
			if err != nil {
				// A Home whose projects could not be listed is not a Home without any.
				return nil, errors.New("workspace listing unavailable")
			}
			ids = append(ids, children...)
		}
		return ids, nil
	}
	ids, err := s.Store.List()
	if err != nil {
		return nil, errors.New("workspace listing unavailable")
	}
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, err := s.Get(id); err == nil {
			result = append(result, id)
		}
	}
	return result, nil
}
func (s panelWorkspaceStore) ListActive() ([]*workspace.Workspace, error) {
	ids, err := s.List()
	if err != nil {
		return nil, err
	}
	result := make([]*workspace.Workspace, 0, len(ids))
	for _, id := range ids {
		ws, err := s.Get(id)
		if err == nil && ws.Status == workspace.StatusActive {
			result = append(result, ws)
		}
	}
	return result, nil
}
func (s panelWorkspaceStore) ListActiveSummaries() ([]workspace.WorkspaceSummary, error) {
	rows, err := s.ListActive()
	if err != nil {
		return nil, err
	}
	result := make([]workspace.WorkspaceSummary, 0, len(rows))
	for _, ws := range rows {
		result = append(result, workspace.WorkspaceSummary{ID: ws.ID, Name: ws.Name, Kind: ws.Kind, Description: ws.Description, FolderSlug: ws.FolderSlug, ParentID: ws.ParentID, OwnerUserID: ws.OwnerUserID, Status: ws.Status, Version: ws.Version, UpdatedAt: ws.UpdatedAt})
	}
	return result, nil
}

type panelSessionsReader struct {
	delegate   homeRecentSessionsReader
	workspaces workspace.Store
	// only, when set, is the pinned workspace: its own conversations are listed,
	// not those of a Home's projects.
	only string
}

func (r panelSessionsReader) RecentSessions(ctx context.Context, limit int) ([]HomeSessionSummary, error) {
	rows, err := r.delegate.RecentSessions(ctx, limit)
	if err != nil {
		return nil, errors.New("session listing unavailable")
	}
	result := make([]HomeSessionSummary, 0, len(rows))
	for _, row := range rows {
		if row.WorkspaceID == "" {
			continue
		} // Legacy root sessions have no verified workspace owner.
		if r.only != "" && row.WorkspaceID != r.only {
			continue
		}
		if _, err := r.workspaces.Get(row.WorkspaceID); err != nil {
			continue
		}
		row.Title = workspaceContextText(row.Title, 120)
		result = append(result, row)
	}
	return result, nil
}

func boundedPanelSnapshot(snapshot HomeSnapshot) HomeSnapshot {
	limit := assistantcontext.PreviewLimit
	if len(snapshot.Workspaces) > limit {
		snapshot.Workspaces = snapshot.Workspaces[:limit]
		snapshot.Meta.Truncated = append(snapshot.Meta.Truncated, "workspaces")
	}
	if len(snapshot.Tasks) > limit {
		snapshot.Tasks = snapshot.Tasks[:limit]
		snapshot.Meta.Truncated = append(snapshot.Meta.Truncated, "tasks")
	}
	if len(snapshot.Sessions) > limit {
		snapshot.Sessions = snapshot.Sessions[:limit]
		snapshot.Meta.Truncated = append(snapshot.Meta.Truncated, "sessions")
	}
	if len(snapshot.Opportunities) > limit {
		snapshot.Opportunities = snapshot.Opportunities[:limit]
		snapshot.Meta.Truncated = append(snapshot.Meta.Truncated, "opportunities")
	}
	if len(snapshot.Agents) > limit {
		snapshot.Agents = snapshot.Agents[:limit]
		snapshot.Meta.Truncated = append(snapshot.Meta.Truncated, "agents")
	}
	encoded, err := json.Marshal(snapshot)
	var data any
	if err != nil || json.Unmarshal(encoded, &data) != nil {
		return HomeSnapshot{Meta: HomeSnapshotMeta{Degraded: []string{"snapshot"}}}
	}
	encoded, err = json.Marshal(sanitizePanelData(data))
	if err != nil || json.Unmarshal(encoded, &snapshot) != nil {
		return HomeSnapshot{Meta: HomeSnapshotMeta{Degraded: []string{"snapshot"}}}
	}
	return snapshot
}

func (h *HomeAssistantAskHandler) scopedPanelSources(ctx context.Context, sources HomeSnapshotSources, turn *assistantWorkspaceTurn) HomeSnapshotSources {
	if turn == nil {
		return sources
	}
	if sources.Workspaces != nil {
		sources.Workspaces = panelWorkspaceStore{Store: sources.Workspaces, handler: h, turn: turn, ctx: ctx}
	}
	if sources.Sessions != nil && sources.Workspaces != nil {
		reader := panelSessionsReader{delegate: sources.Sessions, workspaces: sources.Workspaces}
		if subject := turn.projection.Subject; subject != nil {
			reader.only = subject.ID
		}
		sources.Sessions = reader
	} else {
		sources.Sessions = nil
	}
	return sources
}
