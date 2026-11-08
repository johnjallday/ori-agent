package sessionhttp

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
	"github.com/johnjallday/ori-agent/internal/store"
)

// StaffingSkillReader reads only source-fenced collection membership and
// declaration availability. It must neither grant skills nor resolve source
// directories inside its fence. Unknown evidence returns an incomplete result.
type StaffingSkillReader func(context.Context, store.StaffingSelection, []string, bool) rosterstaffing.SkillMembership

func (h *Handler) SetStaffingSkillReader(read StaffingSkillReader) {
	h.staffingSkillReader = read
}
