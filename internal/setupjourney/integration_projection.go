package setupjourney

import (
	"encoding/json"
	"net/url"
	"strings"
	"unicode"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
)

// IntegrationProjection is the bounded reviewed identity plus the complete
// plugin.Manager trust report. It is response-only and is never serialized to
// setup journey persistence. SourceURL is the registry's reviewed repository,
// so a person can inspect the source before installing; it is https-only.
//
// ExpectedVersion is the version this step installs, replaces with, or has
// verified; MinimumVersion is the reviewed floor. ReleaseChecked is false only
// when the step targets the floor because the latest release could not be
// checked.
type IntegrationProjection struct {
	Key                  string              `json:"key"`
	PluginID             string              `json:"plugin_id"`
	Publisher            string              `json:"publisher"`
	SourceLabel          string              `json:"source_label"`
	SourceURL            string              `json:"source_url,omitempty"`
	ExpectedVersion      string              `json:"expected_version"`
	MinimumVersion       string              `json:"minimum_version,omitempty"`
	ReleaseChecked       bool                `json:"release_checked"`
	InstalledVersion     string              `json:"installed_version,omitempty"`
	Enabled              bool                `json:"enabled"`
	ReleaseReady         bool                `json:"release_ready"`
	DevelopmentCopy      bool                `json:"development_copy,omitempty"`
	Verified             bool                `json:"verified"`
	ReplacementRequired  bool                `json:"replacement_required,omitempty"`
	ExpectedBlueprintID  string              `json:"expected_blueprint_id"`
	ExpectedProgramID    string              `json:"expected_program_id"`
	RequiredHostFeatures []string            `json:"required_host_features"`
	ExpectedProtocol     int                 `json:"expected_protocol"`
	SupportedPlatforms   []string            `json:"supported_platforms"`
	StateRevision        string              `json:"state_revision"`
	Trust                *plugin.TrustReport `json:"trust,omitempty"`

	// reviewedSource is the exact source an install or replacement offer was
	// inspected from. It is never serialized; Commit installs only this source.
	reviewedSource string
}

func validIntegrationProjection(value *IntegrationProjection) bool {
	if value == nil {
		return true
	}
	if !validateStableID(value.Key) || !validateStableID(value.PluginID) ||
		!validateStableID(value.ExpectedBlueprintID) || !validateStableID(value.ExpectedProgramID) ||
		!validateCanonicalRef(value.ExpectedVersion, false) || !validateCanonicalRef(value.MinimumVersion, true) ||
		!validateCanonicalRef(value.InstalledVersion, true) || value.ExpectedProtocol <= 0 ||
		len(value.RequiredHostFeatures) == 0 || len(value.RequiredHostFeatures) > 8 ||
		len(value.SupportedPlatforms) == 0 || len(value.SupportedPlatforms) > 8 ||
		!safeIntegrationLabel(value.Publisher, 100) || !safeIntegrationLabel(value.SourceLabel, 200) ||
		!safeIntegrationSourceURL(value.SourceURL) || !validateDigest(value.StateRevision, false) {
		return false
	}
	if value.Verified && (!value.ReleaseReady || value.DevelopmentCopy || value.ReplacementRequired ||
		value.InstalledVersion != value.ExpectedVersion) {
		return false
	}
	// Every version a step acts on is at or above the reviewed floor.
	if value.MinimumVersion != "" && !reviewedintegration.AtLeast(value.ExpectedVersion, value.MinimumVersion) {
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

// safeIntegrationSourceURL accepts an empty value or an absolute https URL
// with a host and no credentials, query or fragment.
func safeIntegrationSourceURL(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 300 || !safeIntegrationLabel(value, 300) || strings.ContainsAny(value, " \t") {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil &&
		parsed.RawQuery == "" && parsed.Fragment == "" && !strings.HasSuffix(parsed.Host, ".")
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
