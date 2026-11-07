package sessionhttp

import (
	"encoding/json"
	"reflect"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
)

// A chosen recommendation does not invalidate advice for every other slot.
// Explicit manual edits/clears invalidate only their affected roles; unknown
// team edits still fail closed. This never refreshes a witness or stale advice.
func staleBuildStaffingTeamEdits(advice *personalassistant.BuildStaffing, before, after personalassistant.BuildDraft) {
	if advice == nil || advice.State == rosterstaffing.Stale {
		return
	}
	decode := func(draft personalassistant.BuildDraft) (map[string]roleStaffingInput, bool) {
		var roles []roleStaffingInput
		if len(draft.RoleStaffing) > 0 && json.Unmarshal(draft.RoleStaffing, &roles) != nil {
			return nil, false
		}
		result, err := normalizeRoleStaffing(roles)
		return result, err == nil
	}
	oldRoles, oldOK := decode(before)
	newRoles, newOK := decode(after)
	if !oldOK || !newOK || reflect.DeepEqual(oldRoles, newRoles) {
		advice.State = rosterstaffing.Stale
		return
	}
	for i := range advice.Roles {
		role := &advice.Roles[i]
		oldFill, newFill := oldRoles[role.RoleID], newRoles[role.RoleID]
		if !reflect.DeepEqual(oldFill, newFill) && newFill.Recommendation == "" {
			role.State = rosterstaffing.Stale
		}
	}
	advice.State = rosterstaffing.Stale
	for _, role := range advice.Roles {
		if role.State == rosterstaffing.Recommended {
			advice.State = rosterstaffing.Recommended
			return
		}
		if role.State == rosterstaffing.NeedsChoice {
			advice.State = rosterstaffing.NeedsChoice
		}
	}
}
