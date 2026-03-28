package server

import (
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// skillResolverAdapter adapts skills.Manager to satisfy workspace.SkillResolver.
type skillResolverAdapter struct {
	manager *skills.Manager
}

func newSkillResolverAdapter(m *skills.Manager) *skillResolverAdapter {
	return &skillResolverAdapter{manager: m}
}

func (a *skillResolverAdapter) ResolveSkillsByNames(names []string) ([]workspace.ResolvedSkill, []string, error) {
	resolved, unresolved, err := a.manager.ResolveSkillsByNames(names)
	if err != nil {
		return nil, nil, err
	}
	out := make([]workspace.ResolvedSkill, len(resolved))
	for i, s := range resolved {
		out[i] = skillToResolvedSkill(s)
	}
	return out, unresolved, nil
}

func (a *skillResolverAdapter) ListEnabledAgentSkills(agentName string) ([]workspace.ResolvedSkill, error) {
	enabled, err := a.manager.ListEnabledSkillsWithPrompts(agentName)
	if err != nil {
		return nil, err
	}
	out := make([]workspace.ResolvedSkill, len(enabled))
	for i, s := range enabled {
		out[i] = skillToResolvedSkill(s)
	}
	return out, nil
}

func skillToResolvedSkill(s skills.Skill) workspace.ResolvedSkill {
	return workspace.ResolvedSkill{
		Name:               s.Name,
		Description:        s.Description,
		Prompt:             s.Prompt,
		Source:             s.Source,
		AllowedTools:       s.AllowedTools,
		DisallowedTools:    s.DisallowedTools,
		RequiredMCPServers: s.RequiredMCPServers,
		PlanningProfile:    s.PlanningProfile,
		Model:              s.Model,
		Color:              s.Color,
		Enabled:            s.Enabled,
		Trusted:            s.Trusted,
	}
}
