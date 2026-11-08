package sessionhttp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// reviewedHomeAssignments discloses only observed exact-role bindings whose
// original private review still proves the source. It is intentionally partial:
// ordinary/legacy/manual assignments have no comparable durable source witness.
// Names, customized copies and entry-point metadata never supply that proof.
// No workspace Get, filesystem/Trash traversal, prompts or history are read.
func (h *Handler) reviewedHomeAssignments(ctx context.Context, owner string) ([]rosterstaffing.Assignment, bool) {
	if h == nil || h.groupRequirements == nil || h.store == nil || h.store.DB() == nil || strings.TrimSpace(owner) == "" {
		return nil, false
	}
	rows, err := h.store.DB().QueryContext(ctx, `
		SELECT r.token, o.idempotency_key, w.id, w.name,
			json_object('key', json_extract(w.assistant_program_json, '$.state.key'),
				'group_template', json_extract(w.assistant_program_json, '$.state.group_template'),
				'home_bindings', json_extract(w.assistant_program_json, '$.state.home_bindings'),
				'staffing_receipts', json_extract(w.assistant_program_json, '$.state.staffing_receipts')),
			(SELECT json_group_array(json_object('id', json_extract(i.value, '$.id'),
				'name', json_extract(i.value, '$.name'), 'role_id', json_extract(i.value, '$.role_id'),
				'role_source', json_extract(i.value, '$.role_source'))) FROM json_each(w.agent_instances) i)
		FROM group_requirement_operations o
		JOIN group_requirement_reviews r ON json_extract(r.payload_json, '$.review_digest') = o.review_digest
		JOIN workspaces w ON w.id = json_extract(o.payload_json, '$.home_workspace_id')
		WHERE o.owner_user_id = ? AND o.operation_kind = 'prepare_home'
			AND w.owner_user_id = ? AND (w.status = 'active' OR w.status = '')
			AND json_extract(o.payload_json, '$.status') = 'succeeded'
			AND json_type(r.payload_json, '$.home_staffing') = 'object'
		ORDER BY o.updated_at DESC, r.token LIMIT 128`, owner, owner)
	if err != nil {
		return nil, false
	}
	type record struct{ token, key, id, label, state, instances string }
	var records []record
	for rows.Next() {
		var row record
		if err := rows.Scan(&row.token, &row.key, &row.id, &row.label, &row.state, &row.instances); err != nil {
			_ = rows.Close()
			return nil, false
		}
		if len(row.state) > 128*1024 || len(row.instances) > 64*1024 {
			continue
		}
		records = append(records, row)
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil || closeErr != nil {
		return nil, false
	}
	// Detach SQL rows before re-entering the existing receipt owner; a database
	// with one connection must not wait on its own active read cursor.
	var out []rosterstaffing.Assignment
	seen := map[string]bool{}
	for _, row := range records {
		if ctx.Err() != nil {
			return nil, false
		}
		var state workspace.AssistantProgramState
		var instances []struct {
			ID, Name   string
			RoleID     string `json:"role_id"`
			RoleSource string `json:"role_source"`
		}
		if json.Unmarshal([]byte(row.state), &state) != nil || json.Unmarshal([]byte(row.instances), &instances) != nil {
			continue
		}
		proof, err := h.groupRequirements.ObservedHomeTeam(ctx, owner, row.token, row.key, row.id, &state)
		if err != nil || proof.HomeWorkspaceID != row.id || proof.Team == nil {
			continue
		}
		for _, role := range proof.Team.Roles {
			if role.Mode != "assign" || role.SourceWitness == "" || role.Scope != "home" {
				continue
			}
			receipt, found, err := workspace.HomeStaffingReceipt(&state, role.RoleID)
			if err != nil || !found || receipt.Action != "assign" || receipt.ChoiceDigest != role.StepDigest(proof.ReviewDigest, row.id) || receipt.AgentName != role.Name {
				continue
			}
			bindings, roleBindings := 0, 0
			for _, binding := range state.HomeBindings.Bindings {
				if binding.RoleID == role.RoleID {
					roleBindings++
					if binding.AgentName == role.Name && binding.AgentInstanceID == receipt.AgentInstanceID {
						bindings++
					}
				}
			}
			matches, occupied := 0, 0
			for _, instance := range instances {
				if instance.RoleID == role.RoleID {
					occupied++
					if instance.ID == receipt.AgentInstanceID && instance.Name == role.Name && instance.RoleSource == workspace.RoleSourceAssigned {
						matches++
					}
				}
			}
			key := row.id + "\x00" + role.RoleID
			if bindings != 1 || roleBindings != 1 || matches != 1 || occupied != 1 || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, rosterstaffing.Assignment{Witness: role.SourceWitness, RoleID: role.RoleID, WorkspaceLabel: row.label})
		}
	}
	return out, false // coverage includes only this exact observed review path
}
