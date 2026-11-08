package server

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/store"
)

func (g serverStaffingToolGrants) PrepareStaffingCapabilities(agentName string, names []string, personal bool, witness string) (setupjourney.StaffingCapabilityGuard, error) {
	if g.builder == nil || g.builder.skillsManager == nil || witness == "" {
		return nil, skills.ErrProfileSkillStateUnavailable
	}
	inspection, err := g.builder.skillsManager.PrepareProfileSkillInspection(agentName)
	if err != nil {
		return nil, err
	}
	current, err := inspection.Read()
	if err != nil || current.Witness != witness {
		return nil, skills.ErrProfileSkillsChanged
	}
	for _, name := range names {
		if !g.staffingSkillMetadataAvailable(name, personal) || !current.Enabled(name) {
			return nil, skills.ErrProfileSkillStateUnavailable
		}
	}
	return func(apply func() error) error { return inspection.WithUnchanged(witness, apply) }, nil
}

func (g serverStaffingToolGrants) staffingSkillMetadataAvailable(name string, personal bool) bool {
	if g.builder == nil || g.builder.skillsManager == nil {
		return false
	}
	if personal {
		_, found, err := g.builder.skillsManager.ResolvePluginSkillMetadataByName(name)
		return err == nil && found
	}
	_, found, err := g.builder.skillsManager.ResolveSkillMetadataByName(name)
	return err == nil && found
}

func (g serverStaffingToolGrants) ReadStaffingSkills(ctx context.Context, selected store.StaffingSelection, names []string, personal bool) rosterstaffing.SkillMembership {
	unknown := rosterstaffing.SkillMembership{}
	if g.builder == nil || g.builder.skillsManager == nil || g.builder.st == nil || ctx.Err() != nil || len(names) > rosterstaffing.MaxCapabilities {
		return unknown
	}
	// Resolve source paths and skill catalog metadata before acquiring a profile
	// fence. The callback below cannot re-enter any profile directory resolver.
	inspection, err := g.builder.skillsManager.PrepareProfileSkillInspection(selected.Name)
	if err != nil {
		return unknown
	}
	available := make(map[string]bool, len(names))
	complete := true
	for _, name := range names {
		available[name] = g.staffingSkillMetadataAvailable(name, personal)
		if !available[name] {
			complete = false
		}
	}
	var out rosterstaffing.SkillMembership
	err = store.WithStaffingProfiles(ctx, g.builder.st, []store.StaffingSelection{selected}, func(map[string]*agent.Agent) error {
		snapshot, readErr := inspection.Read()
		if readErr != nil {
			return readErr
		}
		out = rosterstaffing.SkillMembership{SourceWitness: selected.Witness, Witness: snapshot.Witness, Complete: complete}
		for _, name := range names {
			if available[name] && snapshot.Enabled(name) {
				out.Enabled = append(out.Enabled, name)
			}
		}
		return nil
	})
	if err != nil {
		return unknown
	}
	return out
}
