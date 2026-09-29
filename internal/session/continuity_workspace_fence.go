package session

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// continuityMirrorDifferences reports, by column name only, where the SQL
// mirror's copy of folder-owned content differs from workspace.json, in the
// same shared SQL read view as registration capture. The folder is what
// travels, so a difference never selects SQL content; the caller records it.
// Existing writers leave such differences behind on real installations (a
// setup stash or provisioning marker saved only to SQL, a canonicalized agent
// node saved only to the file), so refusing them would keep those workspaces
// from ever becoming ready. Identity fields are checked separately and strictly.
func continuityMirrorDifferences(ctx context.Context, q workspacecontinuity.Queryer, ws *workspace.Workspace) ([]string, error) {
	expected := (&WorkspaceStoreAdapter{}).toSessionWorkspace(ws)
	f := serializeWorkspaceFields(expected)
	columns := []struct {
		name    string
		want    []byte
		missing string
	}{
		{"agent_instances", f.agentInstances, "[]"}, {"tags", f.tags, "[]"}, {"shared_data", f.sharedData, "{}"}, {"layout", f.layout, "null"},
		{"messages_json", f.messages, "[]"}, {"tasks_json", f.tasks, "[]"}, {"attachments_json", f.attachments, "[]"},
		{"folders_json", f.folders, "[]"}, {"scheduled_tasks_json", f.scheduledTasks, "[]"}, {"store_nodes_json", f.storeNodes, "[]"},
		{"workflows_json", f.workflows, "{}"}, {"directory_references_json", f.directoryReferences, "[]"},
		{"mcp_bindings_json", f.mcpBindings, "[]"}, {"agent_mcp_access_json", f.agentMCPAccess, "[]"},
		{"skill_bindings_json", f.skillBindings, "[]"}, {"agent_skill_access_json", f.agentSkillAccess, "[]"},
		{"opportunities_json", f.opportunities, "[]"}, {"installed_capabilities_json", f.installedCapabilities, "[]"},
		{"toolbox_state_json", f.toolboxState, "null"}, {"mission_state_json", f.missionState, "null"},
		{"assistant_program_json", f.assistantProgram, "null"},
	}
	names, lengths := make([]string, len(columns)), make([]string, len(columns))
	values, destinations := make([][]byte, len(columns)), make([]any, len(columns))
	byName := make(map[string]int, len(columns))
	for index, column := range columns {
		byName[column.name] = index
		names[index] = column.name
		lengths[index] = "COALESCE(length(CAST(" + column.name + " AS BLOB)),0)"
		destinations[index] = &values[index]
	}
	// Column names are the fixed literals above, never record fields. Check the
	// complete row bound before materializing it within the SAME read snapshot.
	var size int64
	if err := q.QueryRowContext(ctx, "SELECT "+strings.Join(lengths, "+")+" FROM workspaces WHERE id=?", ws.ID).Scan(&size); err != nil {
		return nil, err
	}
	if size < 0 || size > 2*workspacecontinuity.MaxChunkBytes {
		return nil, workspacecontinuity.ErrLimit
	}
	if err := q.QueryRowContext(ctx, "SELECT "+strings.Join(names, ",")+" FROM workspaces WHERE id=?", ws.ID).Scan(destinations...); err != nil {
		return nil, err
	}
	for index, column := range columns {
		if len(values[index]) == 0 || bytes.Equal(bytes.TrimSpace(values[index]), []byte("null")) {
			values[index] = []byte(column.missing)
		}
	}
	var differs []string
	// Private binding config differs by design: compare only the definitions
	// that the canonical denied file is allowed to contain.
	mcpIndex, skillIndex := byName["mcp_bindings_json"], byName["skill_bindings_json"]
	if mcp, skills, ok := projectedBindingColumns(values[mcpIndex], values[skillIndex]); ok {
		values[mcpIndex], values[skillIndex] = mcp, skills
	} else {
		differs = append(differs, "mcp_bindings_json", "skill_bindings_json")
	}
	wants := make([][]byte, len(columns))
	for index, column := range columns {
		wants[index] = column.want
	}
	if err := normalizeReadColumns(byName, wants); err != nil {
		return nil, err
	}
	if err := normalizeReadColumns(byName, values); err != nil {
		differs = append(differs, "agent_instances", "tasks_json")
	}
	for index, column := range columns {
		if slices.Contains(differs, column.name) {
			continue
		}
		want := wants[index]
		if len(want) == 0 || bytes.Equal(bytes.TrimSpace(want), []byte("null")) {
			want = []byte(column.missing)
		}
		actual := values[index]
		if len(actual) == 0 || bytes.Equal(bytes.TrimSpace(actual), []byte("null")) {
			actual = []byte(column.missing)
		}
		if sameContinuityJSON(actual, want) != nil {
			differs = append(differs, column.name)
		}
	}
	// order_index is SQL-assigned display order, carried in the record's SQL
	// metadata; the file's copy is not authoritative for it.
	var kind, description, status sql.NullString
	var sequence int64
	var migrated int
	if err := q.QueryRowContext(ctx, `SELECT kind,description,status,ticket_sequence,ticket_migration_version
		FROM workspaces WHERE id=?`, ws.ID).Scan(&kind, &description, &status, &sequence, &migrated); err != nil {
		return nil, err
	}
	if kind.String != string(expected.Kind) || description.String != expected.Description || status.String != string(f.status) ||
		sequence != expected.TicketSequence || migrated != expected.TicketMigrationVersion {
		differs = append(differs, "scalars")
	}
	return differs, nil
}

// projectedBindingColumns reduces the SQL binding columns to the definitions
// the denied workspace.json may contain; ok is false for an unreadable mirror.
func projectedBindingColumns(mcpValue, skillValue []byte) (mcpJSON, skillJSON []byte, ok bool) {
	var mcp []workspace.MCPBinding
	var skills []workspace.SkillBinding
	if workspacecontinuity.DecodeDocument(mcpValue, &mcp, workspacecontinuity.MaxChunkBytes) != nil ||
		workspacecontinuity.DecodeDocument(skillValue, &skills, workspacecontinuity.MaxChunkBytes) != nil {
		return nil, nil, false
	}
	mcp, skills, err := workspace.ProjectContinuityBindingDefinitions(mcp, skills)
	if err != nil {
		return nil, nil, false
	}
	if mcpJSON, err = json.Marshal(mcp); err != nil {
		return nil, nil, false
	}
	if skillJSON, err = json.Marshal(skills); err != nil {
		return nil, nil, false
	}
	return mcpJSON, skillJSON, true
}

// readNormalizedColumns are the fields every workspace.json read migrates
// (workspace.FromJSON): agent instances collapse to one canonical node per
// profile with task/store references remapped, legacy scheduled tasks become
// tasks, capabilities are canonicalized. A FileStore save persists the migrated
// form while the SQL mirror keeps what its writer stored, so both copies are
// compared after the same migration rather than refused forever as a split.
var readNormalizedColumns = []struct{ column, key string }{
	{"agent_instances", "agent_instances"}, {"tasks_json", "tasks"}, {"scheduled_tasks_json", "scheduled_tasks"},
	{"store_nodes_json", "store_nodes"}, {"installed_capabilities_json", "installed_capabilities"}, {"folders_json", "folders"},
}

func normalizeReadColumns(byName map[string]int, values [][]byte) error {
	doc := make(map[string]json.RawMessage, len(readNormalizedColumns))
	for _, c := range readNormalizedColumns {
		value := bytes.TrimSpace(values[byName[c.column]])
		if len(value) > 0 && !bytes.Equal(value, []byte("null")) {
			doc[c.key] = value
		}
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return workspacecontinuity.ErrInvalid
	}
	ws, err := workspace.FromJSON(data)
	if err != nil {
		return workspacecontinuity.ErrInvalid
	}
	f := serializeWorkspaceFields((&WorkspaceStoreAdapter{}).toSessionWorkspace(ws))
	for column, value := range map[string][]byte{"agent_instances": f.agentInstances, "tasks_json": f.tasks,
		"scheduled_tasks_json": f.scheduledTasks, "store_nodes_json": f.storeNodes,
		"installed_capabilities_json": f.installedCapabilities, "folders_json": f.folders} {
		values[byName[column]] = value
	}
	return nil
}

func sameContinuityJSON(actual, expected []byte) error {
	canonicalize := func(data []byte) ([]byte, error) {
		var validated json.RawMessage
		if err := workspacecontinuity.DecodeDocument(data, &validated, workspacecontinuity.MaxChunkBytes); err != nil {
			return nil, err
		}
		var value any
		decoder := json.NewDecoder(bytes.NewReader(validated))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return nil, workspacecontinuity.ErrInvalid
		}
		return json.Marshal(value)
	}
	a, err := canonicalize(actual)
	if err != nil {
		return err
	}
	b, err := canonicalize(expected)
	if err != nil {
		return err
	}
	if !bytes.Equal(a, b) {
		return workspacecontinuity.ErrChanged
	}
	return nil
}
