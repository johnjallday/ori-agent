package chathttp

import (
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/skills"
)

type skillInvocation struct {
	Skill    *skills.Skill
	Args     string
	Explicit bool
}

func parseSkillCommand(message string) (string, string, error) {
	message = strings.TrimSpace(message)
	if !strings.HasPrefix(message, "/skill") {
		return "", "", fmt.Errorf("command must start with '/skill'")
	}

	parts := strings.Fields(message)
	if len(parts) < 2 {
		return "", "", fmt.Errorf("skill name is required")
	}

	name := parts[1]
	args := ""
	if len(parts) > 2 {
		args = strings.TrimSpace(strings.Join(parts[2:], " "))
	}

	return name, args, nil
}

func parseImplicitSkillCommand(message string) (string, string, bool) {
	trimmed := strings.TrimSpace(message)
	if !strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "/skill") {
		return "", "", false
	}

	parts := strings.Fields(trimmed)
	if len(parts) == 0 {
		return "", "", false
	}

	name := strings.TrimPrefix(parts[0], "/")
	if name == "" {
		return "", "", false
	}

	args := ""
	if len(parts) > 1 {
		args = strings.TrimSpace(strings.Join(parts[1:], " "))
	}

	return name, args, true
}

func buildSkillPrompt(skill *skills.Skill, args string) string {
	prompt := strings.TrimSpace(skill.Prompt)
	if args == "" {
		return prompt
	}

	if strings.Contains(prompt, "$ARGUMENTS") {
		prompt = strings.ReplaceAll(prompt, "$ARGUMENTS", args)
		return strings.TrimSpace(prompt)
	}

	return strings.TrimSpace(prompt + "\n\nUser input:\n" + args)
}
