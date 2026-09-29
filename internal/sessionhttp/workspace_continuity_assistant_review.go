package sessionhttp

import (
	"context"
	"encoding/json"
	"path"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

type continuityAssistantCandidate struct {
	WorkspaceID      string                               `json:"workspace_id"`
	AssistantID      string                               `json:"assistant_id"`
	DisplayName      string                               `json:"display_name"`
	EntryInstanceID  string                               `json:"entry_instance_id"`
	ProfileName      string                               `json:"profile_name"`
	SourceStatus     personalassistant.RelationshipStatus `json:"source_status"`
	AppearanceStatus string                               `json:"appearance_status"` // no_upload, present or missing after owned-byte review
}

// inspectModernAssistant requires a typed agreement, exact entry-instance
// profile and the same denied canonical definition. It never resolves a global
// roster name, hydrates a copied local slot, or makes an adoption decision.
func inspectModernAssistant(ctx context.Context, dir string, inspected workspacecontinuity.Inspection, ws *agentworkspace.Workspace) (*continuityAssistantCandidate, error) {
	var assistants, agents *workspacecontinuity.Component
	for i := range inspected.Manifest.Components {
		switch inspected.Manifest.Components[i].Domain {
		case "assistant":
			assistants = &inspected.Manifest.Components[i]
		case "agents":
			agents = &inspected.Manifest.Components[i]
		}
	}
	if assistants == nil || agents == nil || ws == nil {
		return nil, workspacecontinuity.ErrIncomplete
	}
	marked := session.NormalizeWorkspaceDesignation(ws.Designation) == session.WorkspaceDesignationPersonalHQ
	for key := range ws.SharedData {
		if strings.EqualFold(key, "personal_assistant_presentation") {
			marked = true
		}
	}
	if assistants.Availability != workspacecontinuity.Present {
		if assistants.Availability == workspacecontinuity.Empty && marked {
			return nil, workspacecontinuity.ErrIncomplete
		}
		return nil, nil // unavailable source agreement is reported separately; not fabricated empty
	}
	if agents.Availability != workspacecontinuity.Present {
		return nil, workspacecontinuity.ErrIncomplete
	}
	entry := ""
	entryID := ""
	instances := make([]session.AgentInstance, 0, len(ws.AgentInstances))
	for _, instance := range ws.AgentInstances {
		instances = append(instances, session.AgentInstance{ID: instance.ID, Name: instance.Name, EntryPoint: instance.EntryPoint})
		if instance.EntryPoint {
			if entry != "" {
				return nil, workspacecontinuity.ErrInvalid
			}
			entry, entryID = instance.Name, instance.ID
		}
	}
	if entry == "" {
		return nil, workspacecontinuity.ErrIncomplete
	}
	files := make(map[string]workspacecontinuity.Fingerprint, len(inspected.Manifest.Files))
	for _, file := range inspected.Manifest.Files {
		files[file.Path] = file
	}
	var entryProfile *agent.Agent
	var entryValue agentworkspace.ContinuityProfile
	seen := map[string]bool{}
	err := workspacecontinuity.ReadComponentRecords(ctx, dir, inspected, "agents", func(chunk workspacecontinuity.Chunk) error {
		if chunk.Family != "profiles" {
			return nil
		} // appearance assets have their own typed review
		for _, record := range chunk.Records {
			var hint struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(record.Data, &hint); err != nil {
				return workspacecontinuity.ErrInvalid
			}
			filename := path.Join(agentworkspace.WorkspaceAgentsDir, agentworkspace.Slugify(hint.Name), agentworkspace.WorkspaceAgentConfigFile)
			fingerprint, ok := files[filename]
			if !ok {
				return workspacecontinuity.ErrIncomplete
			}
			value, profile, err := agentworkspace.VerifyContinuityProfileEvidence(ctx, dir, ws, record, fingerprint)
			if err != nil {
				return err
			}
			if seen[value.Name] {
				return workspacecontinuity.ErrInvalid
			}
			seen[value.Name] = true
			if value.Name == entry {
				entryProfile, entryValue = profile, value
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if entryProfile == nil {
		return nil, workspacecontinuity.ErrIncomplete
	}
	binding, err := personalassistant.NewContinuityBinding(&session.Workspace{
		ID: ws.ID, OwnerUserID: ws.OwnerUserID, AgentInstances: instances, SharedData: ws.SharedData,
	}, entryProfile)
	if err != nil {
		return nil, err
	}
	appearanceStatus, err := agentworkspace.VerifyContinuityAppearanceEvidence(ctx, dir, inspected, entryValue)
	if err != nil {
		return nil, err
	}
	var candidate *continuityAssistantCandidate
	err = workspacecontinuity.ReadComponentRecords(ctx, dir, inspected, "assistant", func(chunk workspacecontinuity.Chunk) error {
		if chunk.Family != "agreements" || candidate != nil || len(chunk.Records) != 1 {
			return workspacecontinuity.ErrInvalid
		}
		agreement, err := personalassistant.VerifyContinuityAgreement(chunk.Records[0], binding)
		if err != nil {
			return err
		}
		candidate = &continuityAssistantCandidate{WorkspaceID: ws.ID, AssistantID: agreement.AssistantID,
			DisplayName: agreement.DisplayName, EntryInstanceID: entryID, ProfileName: agreement.ProfileName,
			SourceStatus: agreement.SourceStatus, AppearanceStatus: appearanceStatus}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if candidate == nil {
		return nil, workspacecontinuity.ErrIncomplete
	}
	return candidate, nil
}
