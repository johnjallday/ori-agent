package personalassistant

import (
	"fmt"

	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
)

// BuildStaffing keeps the builder's persisted V1 contract over the shared
// bounded advice shape. Other review owners cannot expand an old build's
// authority merely by adding newer evidence fields to its document.
type BuildStaffing rosterstaffing.Advice

type BuildStaffingRole = rosterstaffing.RoleAdvice
type BuildStaffingChoice = rosterstaffing.AdviceChoice

func (s *BuildStaffing) Public() *BuildStaffing {
	return (*BuildStaffing)((*rosterstaffing.Advice)(s).Public())
}

func (s *BuildStaffing) invalidate() {
	if s != nil {
		s.State = rosterstaffing.Stale
	}
}

func (s *BuildStaffing) validate(contextVersion int64) error {
	if s != nil && (s.Version != 1 || (*rosterstaffing.Advice)(s).Validate(contextVersion) != nil) {
		return fmt.Errorf("%w: staffing recommendation", ErrWorkspaceBuildInvalid)
	}
	return nil
}
