package agenthttp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
	tools = append(tools, llm.Tool{Name: "assistant_workspace_discovery", Description: "Discover owned project and Home/group metadata with separate project/group totals. No content reads or permission grants.", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}})
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
		if isFileReader(name) {
			data, err = r.executeFileReader(name, args)
		} else {
			data, err = r.executeReader(ctx, name, args)
		}
		if err != nil {
			return "", errors.New("source unavailable")
		}
		encoded, err := homeToolJSON(data)
		if err != nil {
			return "", errors.New("source unavailable")
		}
		if data["content_read"] != true && !r.ledger.charge(evidenceSize(encoded)) {
			return `{"status":"partial","reason":"evidence_budget_exhausted","content_read":false}`, nil
		}
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

func (s panelWorkspaceStore) Get(id string) (*workspace.Workspace, error) {
	if err := s.handler.revalidateWorkspaceTurn(s.ctx, s.turn); err != nil {
		return nil, err
	}
	if s.turn.projection.Subject != nil && id != s.turn.projection.Subject.ID {
		return nil, errors.New("workspace outside accepted scope")
	}
	ws, err := s.Store.Get(id)
	if err != nil || !workspaceReadable(ws, s.turn.userID) {
		return nil, errors.New("workspace unavailable")
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
	if s.turn.projection.Subject != nil {
		return []string{s.turn.projection.Subject.ID}, nil
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
		sources.Sessions = panelSessionsReader{delegate: sources.Sessions, workspaces: sources.Workspaces}
	} else {
		sources.Sessions = nil
	}
	return sources
}
