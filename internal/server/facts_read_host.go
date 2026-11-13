package server

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/projectstaffing"
)

// wireFactsRead makes the Home library's completed scans hand the songs an
// installed app plugin can read to the Home's shared assistant
// (assistant-reads-projects group 5), then closes reads a previous process
// left started. Every part of the host is looked up when a read runs, so the
// order of the builder's phases does not matter.
func (b *ServerBuilder) wireFactsRead() {
	if b.sessionHandler == nil {
		return
	}
	runner := b.sessionHandler.SetFactsReadHost(projectlibrary.FactsReadHost{
		Team:      b.factsReadTeam,
		Assistant: b.factsReadAssistant,
		Model: func(_ context.Context, _, agentName string) (projectlibrary.ManagerChat, string, error) {
			chat, ref, err := b.rootAgentToolModel(agentName)
			if err != nil {
				return nil, "", err
			}
			return chat, ref.Model, nil
		},
		Reader: func(ctx context.Context, installed plugin.InstalledPlugin, input json.RawMessage) (json.RawMessage, error) {
			if b.projectFactsCall == nil {
				return nil, plugin.ErrProjectFactsUnavailable
			}
			return b.projectFactsCall(ctx, installed, input)
		},
	})
	if runner == nil {
		return
	}
	if closed := runner.Sweep(); closed > 0 {
		logger.Info("Closed song reads a previous process left started", logger.Fields{"count": closed})
	}
}

// factsReadTeam is the Home's song team, read from the installed app plugin as
// the Home opens songs with it.
func (b *ServerBuilder) factsReadTeam(scope projectlibrary.Scope) (projectlibrary.FactsReadTeam, bool) {
	if b.folderSetupHost == nil || b.workspaceStore == nil {
		return projectlibrary.FactsReadTeam{}, false
	}
	home, err := b.workspaceStore.Get(scope.HomeID)
	if err != nil || home == nil {
		return projectlibrary.FactsReadTeam{}, false
	}
	team, ok := b.folderSetupHost.projectTeam(home)
	if !ok || len(team.Roles) == 0 {
		return projectlibrary.FactsReadTeam{}, false
	}
	return projectlibrary.FactsReadTeam{PluginID: team.PluginID, BlueprintID: team.BlueprintID, Label: team.Roles[0].Label}, true
}

// factsReadAssistant is the Home's recorded shared assistant, whether or not
// its switch adds it to songs (the two switches are independent, FR15).
func (b *ServerBuilder) factsReadAssistant(homeID string, team projectlibrary.FactsReadTeam) (string, string) {
	if b.projectStaffing == nil {
		return "", projectlibrary.FactsReadNoAssistant
	}
	name, err := b.projectStaffing.HomeAssistant(homeID, team.PluginID, team.BlueprintID)
	switch {
	case err == nil:
		return name, ""
	case errors.Is(err, projectstaffing.ErrAssistantMissing):
		return "", projectlibrary.FactsReadAssistantMissing
	}
	return "", projectlibrary.FactsReadNoAssistant
}
