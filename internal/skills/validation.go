package skills

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

const MaxSkillFileBytes int64 = 256 * 1024

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

// ValidateSkillMarkdown applies the same metadata rules used by agent-scoped
// skill creation to one bounded SKILL.md value.
func ValidateSkillMarkdown(text, expectedName string) (Skill, error) {
	if int64(len(text)) > MaxSkillFileBytes {
		return Skill{}, fmt.Errorf("SKILL.md must be no larger than %d bytes", MaxSkillFileBytes)
	}
	frontmatter, body := parseFrontmatter(text)
	fm, fmErr := parseSkillFrontmatter(frontmatter)
	name := fm.Name
	if name == "" {
		name = expectedName
	}
	skill := Skill{Name: name, Description: fm.Description, Prompt: strings.TrimSpace(body), AllowedTools: fm.AllowedTools, DisallowedTools: fm.DisallowedTools, RequiredMCPServers: fm.RequiredMCPServers}
	skill.ValidationErrors = validateSkillMetadata(name, fm.Description)
	if fmErr != nil {
		skill.ValidationErrors = append(skill.ValidationErrors, fmt.Sprintf("invalid frontmatter: %v", fmErr))
	}
	if len(skill.ValidationErrors) > 0 {
		return Skill{}, fmt.Errorf("invalid skill: %s", strings.Join(skill.ValidationErrors, "; "))
	}
	if skill.Prompt == "" {
		return Skill{}, fmt.Errorf("prompt is required")
	}
	if skill.Name != expectedName {
		return Skill{}, fmt.Errorf("skill name %q must match folder %q", skill.Name, expectedName)
	}
	return skill, nil
}

// ReadValidatedSkillFile loads one bounded SKILL.md and applies the same
// metadata validation used by agent-scoped skill creation.
func ReadValidatedSkillFile(path, expectedName string) (Skill, error) {
	info, err := os.Stat(path) // #nosec G304 -- callers provide a path already scoped to a trusted skill root
	if err != nil {
		return Skill{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxSkillFileBytes {
		return Skill{}, fmt.Errorf("SKILL.md must be a regular file no larger than %d bytes", MaxSkillFileBytes)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- callers scope path to a trusted skill root before validation
	if err != nil {
		return Skill{}, err
	}
	return ValidateSkillMarkdown(string(data), expectedName)
}

func validateSkillPrompt(prompt string) error {
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("prompt is required")
	}
	if int64(len(prompt)) > MaxSkillFileBytes {
		return fmt.Errorf("prompt must be no larger than %d bytes", MaxSkillFileBytes)
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
