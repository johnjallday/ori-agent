package setupjourney

import (
	"encoding/json"
	"strings"
	"unicode"

	"github.com/johnjallday/ori-agent/internal/plugin"
)

// IntegrationProjection is the bounded reviewed identity plus the complete
// plugin.Manager trust report. It is response-only and is never serialized to
// setup journey persistence.
type IntegrationProjection struct {
	Key                  string              `json:"key"`
	PluginID             string              `json:"plugin_id"`
	Publisher            string              `json:"publisher"`
	SourceLabel          string              `json:"source_label"`
	ExpectedVersion      string              `json:"expected_version"`
	InstalledVersion     string              `json:"installed_version,omitempty"`
	Enabled              bool                `json:"enabled"`
	ReleaseReady         bool                `json:"release_ready"`
	ExpectedBlueprintID  string              `json:"expected_blueprint_id"`
	ExpectedProgramID    string              `json:"expected_program_id"`
	RequiredHostFeatures []string            `json:"required_host_features"`
	ExpectedProtocol     int                 `json:"expected_protocol"`
	SupportedPlatforms   []string            `json:"supported_platforms"`
	StateRevision        string              `json:"state_revision"`
	Trust                *plugin.TrustReport `json:"trust,omitempty"`
}

func validIntegrationProjection(value *IntegrationProjection) bool {
	if value == nil {
		return true
	}
	if !validateStableID(value.Key) || !validateStableID(value.PluginID) ||
		!validateStableID(value.ExpectedBlueprintID) || !validateStableID(value.ExpectedProgramID) ||
		!validateCanonicalRef(value.ExpectedVersion, false) ||
		!validateCanonicalRef(value.InstalledVersion, true) || value.ExpectedProtocol <= 0 ||
		len(value.RequiredHostFeatures) == 0 || len(value.RequiredHostFeatures) > 8 ||
		len(value.SupportedPlatforms) == 0 || len(value.SupportedPlatforms) > 8 ||
		!safeIntegrationLabel(value.Publisher, 100) || !safeIntegrationLabel(value.SourceLabel, 200) ||
		!validateDigest(value.StateRevision, false) {
		return false
	}
	for _, feature := range value.RequiredHostFeatures {
		if !validateStableID(feature) {
			return false
		}
	}
	for _, platform := range value.SupportedPlatforms {
		parts := strings.Split(platform, "/")
		if len(parts) != 2 || !validateStableID(parts[0]) || !validateStableID(parts[1]) {
			return false
		}
	}
	if value.Trust != nil {
		if len(value.Trust.MCPCommands) > 64 || len(value.Trust.Skills) > 64 ||
			len(value.Trust.Surfaces) > 64 || len(value.Trust.Services) > 32 ||
			len(value.Trust.Operations) > 128 || len(value.Trust.Artifacts) > 32 ||
			len(value.Trust.Blueprints) > 32 || len(value.Trust.Warnings) > 64 ||
			len(value.Trust.Unsupported) > 64 {
			return false
		}
		encoded, err := json.Marshal(value.Trust)
		if err != nil || len(encoded) > 128<<10 {
			return false
		}
	}
	return true
}

func safeIntegrationLabel(value string, max int) bool {
	if strings.TrimSpace(value) == "" || len(value) > max {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func cloneIntegrationProjection(source *IntegrationProjection) *IntegrationProjection {
	if source == nil {
		return nil
	}
	clone := *source
	clone.RequiredHostFeatures = append([]string(nil), source.RequiredHostFeatures...)
	clone.SupportedPlatforms = append([]string(nil), source.SupportedPlatforms...)
	clone.Trust = cloneTrustReport(source.Trust)
	return &clone
}

func cloneTrustReport(source *plugin.TrustReport) *plugin.TrustReport {
	if source == nil {
		return nil
	}
	clone := *source
	clone.MCPCommands = append([]string(nil), source.MCPCommands...)
	clone.Skills = append([]string(nil), source.Skills...)
	clone.SurfaceCapabilities = append([]string(nil), source.SurfaceCapabilities...)
	clone.Surfaces = append([]plugin.SurfaceDisclosure(nil), source.Surfaces...)
	clone.Services = append([]plugin.ServiceDisclosure(nil), source.Services...)
	for index := range clone.Services {
		clone.Services[index].Platforms = append([]string(nil), source.Services[index].Platforms...)
	}
	clone.Operations = append([]plugin.OperationDisclosure(nil), source.Operations...)
	for index := range clone.Operations {
		clone.Operations[index].Scopes = append([]string(nil), source.Operations[index].Scopes...)
	}
	clone.Artifacts = append([]plugin.ArtifactDisclosure(nil), source.Artifacts...)
	clone.SymbolicScopes = append([]string(nil), source.SymbolicScopes...)
	clone.Blueprints = append([]string(nil), source.Blueprints...)
	clone.Unsupported = append([]plugin.UnsupportedComponent(nil), source.Unsupported...)
	clone.Warnings = append([]string(nil), source.Warnings...)
	return &clone
}
