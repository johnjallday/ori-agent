package workspacerun

import (
	"errors"
	"fmt"
	"strings"
)

const (
	ProfileGeneral     = "general"
	ProfileEngineering = "engineering"
)

var ErrProfileNotFound = errors.New("workspace run profile not found")

type Profile struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`

	RequiredArtifacts []ArtifactKind `json:"required_artifacts,omitempty"`
	OptionalArtifacts []ArtifactKind `json:"optional_artifacts,omitempty"`

	Validation    ValidationContract `json:"validation"`
	DefaultPolicy Policy             `json:"default_policy,omitempty"`
}

type ValidationContract struct {
	RequiredChecks []string `json:"required_checks,omitempty"`
	OptionalChecks []string `json:"optional_checks,omitempty"`
	AllowedToFail  []string `json:"allowed_to_fail,omitempty"`
}

type ProfileRegistry struct {
	profiles map[string]Profile
}

func NewProfileRegistry(profiles ...Profile) *ProfileRegistry {
	r := &ProfileRegistry{profiles: make(map[string]Profile)}
	if len(profiles) == 0 {
		profiles = DefaultProfiles()
	}
	for _, profile := range profiles {
		r.Register(profile)
	}
	return r
}

func DefaultProfiles() []Profile {
	return []Profile{
		{
			ID:          ProfileGeneral,
			Version:     "1",
			Name:        "General",
			Description: "Default workspace run contract with no required artifacts or checks.",
			DefaultPolicy: Policy{
				Mutation:        PolicyMutationDenied,
				Approval:        PolicyApprovalNone,
				ExternalEffects: PolicyExternalEffectsDenied,
			},
		},
		{
			ID:                ProfileEngineering,
			Version:           "1",
			Name:              "Engineering",
			Description:       "Software-development run contract requiring change evidence and validation output when validation is requested.",
			RequiredArtifacts: []ArtifactKind{ArtifactChangedFiles},
			OptionalArtifacts: []ArtifactKind{ArtifactDiff, ArtifactTestOutput, ArtifactLog},
			Validation: ValidationContract{
				RequiredChecks: []string{"change_evidence_present"},
				OptionalChecks: []string{"validation_output_present"},
			},
			DefaultPolicy: Policy{
				Mutation:        PolicyMutationAllowed,
				Approval:        PolicyApprovalFinalOnly,
				ExternalEffects: PolicyExternalEffectsDenied,
			},
		},
	}
}

func (r *ProfileRegistry) Register(profile Profile) {
	if r.profiles == nil {
		r.profiles = make(map[string]Profile)
	}
	id := normalizeProfileID(profile.ID)
	profile.ID = id
	r.profiles[id] = cloneProfile(profile)
}

func (r *ProfileRegistry) Snapshot(profileID string) (Profile, error) {
	if r == nil {
		return Profile{}, fmt.Errorf("profile registry is nil")
	}
	id := normalizeProfileID(profileID)
	if id == "" {
		id = ProfileGeneral
	}
	profile, ok := r.profiles[id]
	if !ok {
		return Profile{}, fmt.Errorf("%w: %q", ErrProfileNotFound, profileID)
	}
	return cloneProfile(profile), nil
}

func MergePolicy(defaultPolicy, requestPolicy Policy) Policy {
	merged := defaultPolicy
	if strings.TrimSpace(requestPolicy.Mutation) != "" {
		merged.Mutation = requestPolicy.Mutation
	}
	if strings.TrimSpace(requestPolicy.Approval) != "" {
		merged.Approval = requestPolicy.Approval
	}
	if strings.TrimSpace(requestPolicy.ExternalEffects) != "" {
		merged.ExternalEffects = requestPolicy.ExternalEffects
	}
	if requestPolicy.ToolAllow != nil {
		merged.ToolAllow = cloneStrings(requestPolicy.ToolAllow)
	} else {
		merged.ToolAllow = cloneStrings(defaultPolicy.ToolAllow)
	}
	if requestPolicy.ToolDeny != nil {
		merged.ToolDeny = cloneStrings(requestPolicy.ToolDeny)
	} else {
		merged.ToolDeny = cloneStrings(defaultPolicy.ToolDeny)
	}
	return merged
}

func normalizeProfileID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

func cloneProfile(profile Profile) Profile {
	profile.RequiredArtifacts = append([]ArtifactKind(nil), profile.RequiredArtifacts...)
	profile.OptionalArtifacts = append([]ArtifactKind(nil), profile.OptionalArtifacts...)
	profile.Validation.RequiredChecks = cloneStrings(profile.Validation.RequiredChecks)
	profile.Validation.OptionalChecks = cloneStrings(profile.Validation.OptionalChecks)
	profile.Validation.AllowedToFail = cloneStrings(profile.Validation.AllowedToFail)
	profile.DefaultPolicy.ToolAllow = cloneStrings(profile.DefaultPolicy.ToolAllow)
	profile.DefaultPolicy.ToolDeny = cloneStrings(profile.DefaultPolicy.ToolDeny)
	return profile
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}
