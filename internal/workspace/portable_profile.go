package workspace

import (
	"encoding/json"
	"path"
	"reflect"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// ContinuityProfile is a workspace-scoped definition, not a global roster entry
// or a runtime Agent. In particular it cannot carry keys, live status, statistics,
// evolution rewards, process handles or local permission defaults.
type ContinuityProfile struct {
	Version      int                             `json:"version"`
	WorkspaceID  string                          `json:"workspace_id"`
	Name         string                          `json:"name"`
	InstanceIDs  []string                        `json:"instance_ids"`
	Role         types.AgentRole                 `json:"role"`
	Disabled     bool                            `json:"disabled"`
	Capabilities []string                        `json:"capabilities"`
	Settings     ContinuityProfileSettings       `json:"settings"`
	Metadata     *types.AgentMetadata            `json:"metadata"`
	Appearance   *types.AgentAppearance          `json:"appearance"`
	Toolbox      *types.AgentDefaultToolbox      `json:"toolbox"`
	Setup        *agent.AssistantSetupProvenance `json:"setup"`
}

// Settings are explicit even when empty: absence must not manufacture recovered
// choices. The destination may lack this provider/model; that is local setup.
type ContinuityProfileSettings struct {
	Model            string  `json:"model"`
	Temperature      float64 `json:"temperature"`
	SystemPrompt     string  `json:"system_prompt"`
	Provider         string  `json:"provider"`
	ReasoningEffort  string  `json:"reasoning_effort"`
	MaxOutputTokens  int     `json:"max_output_tokens"`
	FallbackProvider string  `json:"fallback_provider"`
	FallbackModel    string  `json:"fallback_model"`
}

func profileInstanceIDs(ws *Workspace, name string) ([]string, error) {
	if ws == nil || !workspacecontinuity.ValidID(ws.ID) || ws.OwnerUserID != "local" ||
		!workspacecontinuity.ValidID(name) || len(ws.AgentInstances) > workspacecontinuity.MaxFiles {
		return nil, workspacecontinuity.ErrInvalid
	}
	_, slug, err := localAgentPath(name)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	slugNames := map[string]string{}
	ids := []string{}
	for _, instance := range ws.AgentInstances {
		if !workspacecontinuity.ValidID(instance.ID) || seen[instance.ID] || !workspacecontinuity.ValidID(instance.Name) {
			return nil, workspacecontinuity.ErrInvalid
		}
		seen[instance.ID] = true
		_, other, err := localAgentPath(instance.Name)
		if err != nil || other == slug && instance.Name != name {
			return nil, workspacecontinuity.ErrInvalid
		}
		if prior, exists := slugNames[other]; exists && prior != instance.Name {
			return nil, workspacecontinuity.ErrInvalid
		}
		slugNames[other] = instance.Name
		if instance.Name == name {
			ids = append(ids, instance.ID)
		}
	}
	sort.Strings(ids)
	// An unused canonical profile can still be preserved, but it does not create
	// an attachment or make a same-named global profile part of this workspace.
	return ids, nil
}

func validatePortableAppearance(appearance *types.AgentAppearance) error {
	if appearance == nil || appearance.Generated == nil || !types.IsValidAppearanceMode(appearance.Mode) {
		return workspacecontinuity.ErrIncomplete
	}
	normal := appearance.Clone()
	normal.Normalize()
	if !reflect.DeepEqual(normal, appearance) {
		return workspacecontinuity.ErrInvalid
	}
	if image := appearance.UploadedImage(); image != "" && !validPortableImageName(image) {
		return workspacecontinuity.ErrUnsafe
	}
	if appearance.Mode == types.AppearanceModeUploaded && appearance.Uploaded == nil ||
		appearance.Mode == types.AppearanceModeCharacter && appearance.Character == nil {
		return workspacecontinuity.ErrIncomplete
	}
	return nil
}

func validPortableImageName(name string) bool {
	if !workspacecontinuity.ValidID(name) || path.Base(name) != name || strings.ContainsAny(name, "/\\") {
		return false
	}
	// SVG/HTML are never served as uploaded image bytes by this adapter.
	switch strings.ToLower(path.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	default:
		return false
	}
}

// SnapshotContinuityProfile requires the exact canonical scoped profile. The
// collector obtains it from agents/<slug>/config.json, never a roster fallback.
// Appearance asset collection is separate and includes inactive uploaded state.
func SnapshotContinuityProfile(ws *Workspace, name string, profile *agent.Agent) (workspacecontinuity.Record, error) {
	ids, err := profileInstanceIDs(ws, name)
	if err != nil {
		return workspacecontinuity.Record{}, err
	}
	if profile == nil {
		return workspacecontinuity.Record{}, workspacecontinuity.ErrIncomplete
	}
	if err := validatePortableAppearance(profile.Appearance); err != nil {
		return workspacecontinuity.Record{}, err
	}
	s := profile.Settings
	value := ContinuityProfile{
		Version: 1, WorkspaceID: ws.ID, Name: name, InstanceIDs: ids,
		Role: profile.Role, Disabled: profile.Status == types.AgentStatusDisabled, Capabilities: profile.Capabilities,
		Settings: ContinuityProfileSettings{s.Model, s.Temperature, s.SystemPrompt, s.Provider, s.ReasoningEffort,
			s.MaxOutputTokens, s.FallbackProvider, s.FallbackModel},
		Metadata: profile.Metadata, Appearance: profile.Appearance, Toolbox: profile.DefaultToolbox, Setup: profile.AssistantSetup,
	}
	_, id, err := localAgentPath(name)
	if err != nil {
		return workspacecontinuity.Record{}, err
	}
	return workspacecontinuity.EncodeRecord(scopedProfileRecordID(ws.ID, id), value)
}

// ProjectContinuityProfileFile returns the denied workspace profile file an
// import installs in place of the copied agents/<slug>/config.json: the
// reviewed definition without keys, local grants or runtime statistics.
func ProjectContinuityProfileFile(record workspacecontinuity.Record, ws *Workspace) (string, []byte, error) {
	value, profile, err := DecodeContinuityProfile(record, ws)
	if err != nil {
		return "", nil, err
	}
	filePath, _, err := localAgentPath(value.Name)
	if err != nil {
		return "", nil, err
	}
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil || len(data) > workspacecontinuity.MaxRecordBytes {
		return "", nil, workspacecontinuity.ErrLimit
	}
	return filePath, data, nil
}

// Receipt record identities are global within a family, while profile names are
// deliberately workspace-local. Physical siblings may both have a "Guide".
func scopedProfileRecordID(workspaceID, slug string) string {
	return workspacecontinuity.Digest([]byte(workspaceID + "\x00" + slug))
}

// DecodeContinuityProfile binds every instance to the reviewed workspace before
// reconstructing a denied, non-running definition. It never creates an Agent in
// the global library or fills in a different appearance when an asset is absent.
func DecodeContinuityProfile(record workspacecontinuity.Record, ws *Workspace) (ContinuityProfile, *agent.Agent, error) {
	var value ContinuityProfile
	if err := workspacecontinuity.DecodeRecord(record, &value); err != nil {
		return value, nil, err
	}
	if value.Version != 1 {
		return value, nil, workspacecontinuity.ErrVersion
	}
	ids, err := profileInstanceIDs(ws, value.Name)
	if err != nil {
		return value, nil, err
	}
	_, slug, err := localAgentPath(value.Name)
	if err != nil || record.ID != scopedProfileRecordID(ws.ID, slug) || value.WorkspaceID != ws.ID || !reflect.DeepEqual(ids, value.InstanceIDs) {
		return value, nil, workspacecontinuity.ErrInvalid
	}
	// Top-level required decoding intentionally does not recursively require all
	// fields of legacy nested types. The new portable settings object does.
	var envelope struct {
		Settings json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal(record.Data, &envelope); err != nil {
		return value, nil, workspacecontinuity.ErrInvalid
	}
	if err := workspacecontinuity.DecodeRequiredDocument(envelope.Settings, &value.Settings, workspacecontinuity.MaxRecordBytes); err != nil {
		return value, nil, err
	}
	if err := validatePortableAppearance(value.Appearance); err != nil {
		return value, nil, err
	}
	s := value.Settings
	profile := &agent.Agent{
		Role: value.Role, Capabilities: value.Capabilities, Metadata: value.Metadata,
		Appearance: value.Appearance, DefaultToolbox: value.Toolbox, AssistantSetup: value.Setup,
		Settings: types.Settings{Model: s.Model, Temperature: s.Temperature, SystemPrompt: s.SystemPrompt, Provider: s.Provider,
			ReasoningEffort: s.ReasoningEffort, MaxOutputTokens: s.MaxOutputTokens,
			FallbackProvider: s.FallbackProvider, FallbackModel: s.FallbackModel},
	}
	clearAgentLocalConfig(profile)
	if value.Disabled {
		profile.Status = types.AgentStatusDisabled // authored opt-out, never active runtime state
	}
	return value, profile, nil
}
