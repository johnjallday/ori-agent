package skills

import (
	"errors"
	"fmt"
)

var (
	ErrSkillExists             = errors.New("skill already exists")
	ErrSkillNotFound           = errors.New("skill not found")
	ErrSkillRenameNotSupported = errors.New("renaming skills is not supported")
)

// SkillConflict represents a duplicate skill name across discovery paths.
type SkillConflict struct {
	Name    string   `json:"name"`
	Paths   []string `json:"paths"`
	Sources []string `json:"sources"`
}

// SkillConflictError is returned when duplicate skill names are found.
type SkillConflictError struct {
	Conflicts []SkillConflict `json:"conflicts"`
}

func (e *SkillConflictError) Error() string {
	if e == nil || len(e.Conflicts) == 0 {
		return "duplicate skill names found"
	}
	return fmt.Sprintf("duplicate skill names found: %d conflict(s)", len(e.Conflicts))
}
