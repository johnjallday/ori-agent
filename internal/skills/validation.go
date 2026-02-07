package skills

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	skillNamePattern = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)
	xmlTagPattern    = regexp.MustCompile(`<[^>]+>`)
)

func validateSkillMetadata(name, description string) []string {
	var errs []string

	if err := validateSkillName(name); err != nil {
		errs = append(errs, err.Error())
	}
	if err := validateSkillDescription(description); err != nil {
		errs = append(errs, err.Error())
	}

	return errs
}

func validateSkillName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if len(name) > 64 {
		return fmt.Errorf("name must be 64 characters or fewer")
	}
	if xmlTagPattern.MatchString(name) {
		return fmt.Errorf("name must not contain XML tags")
	}
	if !skillNamePattern.MatchString(name) {
		return fmt.Errorf("name must use lowercase letters, numbers, and hyphens only")
	}
	lower := strings.ToLower(name)
	if strings.Contains(lower, "anthropic") || strings.Contains(lower, "claude") {
		return fmt.Errorf("name must not include reserved words")
	}
	return nil
}

func validateSkillDescription(description string) error {
	description = strings.TrimSpace(description)
	if description == "" {
		return fmt.Errorf("description is required")
	}
	if len(description) > 1024 {
		return fmt.Errorf("description must be 1024 characters or fewer")
	}
	if xmlTagPattern.MatchString(description) {
		return fmt.Errorf("description must not contain XML tags")
	}
	return nil
}
