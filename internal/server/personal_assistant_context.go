package server

import (
	"context"
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/sensitive"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// personalAssistantContextAdapter is the only bridge that supplies PAF-owned
// context to the work handler. Ori Guide is built elsewhere and never receives
// this adapter or any of its stores.
type personalAssistantContextAdapter struct {
	relationship *personalassistant.Service
	profiles     userprofile.UserStore
	workspaces   workspace.Store
}

func (a personalAssistantContextAdapter) ResolvePersonalAssistantContext(ctx context.Context, userID string) (*agenthttp.PersonalAssistantWorkContext, error) {
	if a.relationship == nil {
		return nil, errors.New("personal assistant relationship service is unavailable")
	}
	projection, err := a.relationship.Get(ctx, strings.TrimSpace(userID))
	if err != nil {
		return nil, err
	}
	out := &agenthttp.PersonalAssistantWorkContext{
		State:        string(projection.State),
		StateVersion: projection.StateVersion,
		Role:         "Personal Assistant",
		Sources:      map[string]agenthttp.PersonalAssistantContextSource{},
	}
	out.Sources["relationship"] = agenthttp.PersonalAssistantContextSource{Status: "available"}
	if projection.State != personalassistant.APIStateActive && projection.State != personalassistant.APIStatePaused {
		return out, nil
	}

	out.DisplayName = projection.DisplayName
	out.HQWorkspaceID = projection.HQWorkspaceID
	out.Mandate = projection.Mandate
	out.FocusAreas = make([]string, 0, len(projection.FocusAreas))
	for _, area := range projection.FocusAreas {
		out.FocusAreas = append(out.FocusAreas, string(area))
	}
	if sensitive.ContainsSecretLikeText(out.Mandate) {
		out.Mandate = ""
		out.Sources["working_agreement"] = agenthttp.PersonalAssistantContextSource{Status: "rejected", Reason: "secret_like_text"}
	} else if strings.TrimSpace(out.Mandate) == "" && len(out.FocusAreas) == 0 {
		out.Sources["working_agreement"] = agenthttp.PersonalAssistantContextSource{Status: "healthy_empty"}
	} else {
		out.Sources["working_agreement"] = agenthttp.PersonalAssistantContextSource{Status: "available"}
	}

	a.loadProfile(ctx, userID, out)
	a.loadMemory(out)
	return out, nil
}

func (a personalAssistantContextAdapter) loadProfile(ctx context.Context, userID string, out *agenthttp.PersonalAssistantWorkContext) {
	if a.profiles == nil {
		out.Sources["user_profile"] = agenthttp.PersonalAssistantContextSource{Status: "unavailable", Reason: "store_unavailable"}
		return
	}
	profile, err := a.profiles.Get(ctx, strings.TrimSpace(userID))
	if errors.Is(err, userprofile.ErrNotFound) {
		out.Sources["user_profile"] = agenthttp.PersonalAssistantContextSource{Status: "healthy_empty"}
		return
	}
	if err != nil {
		out.Sources["user_profile"] = agenthttp.PersonalAssistantContextSource{Status: "unavailable", Reason: "read_failed"}
		return
	}
	rendered := userprofile.RenderUserProfileSection(profile)
	if sensitive.ContainsSecretLikeText(rendered) {
		out.Sources["user_profile"] = agenthttp.PersonalAssistantContextSource{Status: "rejected", Reason: "secret_like_text"}
		return
	}
	out.UserProfile = rendered
	status := "available"
	if strings.TrimSpace(rendered) == "" {
		status = "healthy_empty"
	}
	out.Sources["user_profile"] = agenthttp.PersonalAssistantContextSource{Status: status}
}

func (a personalAssistantContextAdapter) loadMemory(out *agenthttp.PersonalAssistantWorkContext) {
	resolver, ok := a.workspaces.(workspace.FolderResolver)
	if !ok || resolver == nil {
		out.Sources["personal_hq_memory"] = agenthttp.PersonalAssistantContextSource{Status: "unavailable", Reason: "store_unavailable"}
		return
	}
	doc, err := workspace.NewMemoryStore(resolver).Read(out.HQWorkspaceID)
	if err != nil {
		out.Sources["personal_hq_memory"] = agenthttp.PersonalAssistantContextSource{Status: "unavailable", Reason: "read_failed"}
		return
	}
	rendered := workspace.RenderMemoryPromptSection(doc, false)
	if sensitive.ContainsSecretLikeText(rendered) {
		out.Sources["personal_hq_memory"] = agenthttp.PersonalAssistantContextSource{Status: "rejected", Reason: "secret_like_text"}
		return
	}
	out.HQMemory = rendered
	status := "available"
	if strings.TrimSpace(rendered) == "" {
		status = "healthy_empty"
	}
	out.Sources["personal_hq_memory"] = agenthttp.PersonalAssistantContextSource{Status: status}
}
