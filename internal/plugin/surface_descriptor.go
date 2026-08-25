package plugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	OriManifestDir  = ".ori-plugin"
	OriManifestFile = "plugin.json"

	SurfaceSchemaVersion   = 1
	SurfaceProtocolVersion = 1
	maxSurfaceManifestSize = 1 << 20
)

type ContributionErrorCode string

const (
	CodeContributionInvalid       ContributionErrorCode = "contribution_invalid"
	CodeIdentityMismatch          ContributionErrorCode = "plugin_identity_mismatch"
	CodeBaseManifestRequired      ContributionErrorCode = "plugin_base_manifest_required"
	CodeProtocolRangeInvalid      ContributionErrorCode = "protocol_range_invalid"
	CodeProtocolIncompatible      ContributionErrorCode = "protocol_incompatible"
	CodeComponentDuplicate        ContributionErrorCode = "component_id_duplicate"
	CodeComponentUnknown          ContributionErrorCode = "component_reference_unknown"
	CodeAssetPathInvalid          ContributionErrorCode = "asset_path_invalid"
	CodePlacementUnsupported      ContributionErrorCode = "surface_placement_unsupported"
	CodeArtifactInvalid           ContributionErrorCode = "artifact_invalid"
	CodeArtifactPlatformDuplicate ContributionErrorCode = "artifact_platform_duplicate"
	CodeOperationSchemaInvalid    ContributionErrorCode = "operation_schema_invalid"
	CodeOperationPolicyInvalid    ContributionErrorCode = "operation_policy_invalid"
	CodeOperationLimitInvalid     ContributionErrorCode = "operation_limit_invalid"
	CodeScopeUnknown              ContributionErrorCode = "scope_unknown"
)

// ContributionError is safe to project during local plugin validation. It
// identifies the rejected component/field without including filesystem paths,
// command lines, artifact bytes, or raw service errors.
type ContributionError struct {
	Code      ContributionErrorCode
	Component string
	Field     string
	Reason    string
	Err       error
}

func (e *ContributionError) Error() string {
	location := strings.Trim(strings.TrimSpace(e.Component)+"."+strings.TrimSpace(e.Field), ".")
	if location == "" {
		location = "contribution"
	}
	if e.Reason != "" {
		return fmt.Sprintf("plugin surface %s: %s (%s)", location, e.Reason, e.Code)
	}
	return fmt.Sprintf("plugin surface %s is invalid (%s)", location, e.Code)
}

func (e *ContributionError) Unwrap() error { return e.Err }

type ProtocolRange struct {
	Min int `json:"min"`
	Max int `json:"max,omitempty"`
}

type ManifestIdentity struct {
	Format  SourceFormat
	Name    string
	Version string
}

// ValidateContributionIdentity requires one portable Claude/Codex identity and
// rejects confusion between any present host manifests and the Ori contribution.
func ValidateContributionIdentity(contribution *SurfaceContribution, identities []ManifestIdentity) error {
	if contribution == nil || len(identities) == 0 {
		return contributionError(CodeBaseManifestRequired, "manifest", "identity", "an Ori contribution requires a Claude or Codex base manifest", nil)
	}
	wantName := strings.ToLower(strings.TrimSpace(contribution.Name))
	wantVersion := strings.TrimSpace(contribution.Version)
	for _, identity := range identities {
		name := strings.ToLower(strings.TrimSpace(identity.Name))
		version := strings.TrimSpace(identity.Version)
		if name == "" || version == "" || name != wantName || version != wantVersion {
			return contributionError(CodeIdentityMismatch, "manifest", "identity", "Claude, Codex, and Ori name/version must match", nil)
		}
	}
	return nil
}

type SurfaceContribution struct {
	SchemaVersion int                     `json:"schema_version"`
	Name          string                  `json:"name"`
	Version       string                  `json:"version"`
	Protocol      ProtocolRange           `json:"protocol"`
	Capabilities  []ContributedCapability `json:"capabilities,omitempty"`
	Services      []ContributedService    `json:"services,omitempty"`
	Blueprints    []ContributedBlueprint  `json:"blueprints,omitempty"`
}

type ContributedCapability struct {
	ID              string                      `json:"id"`
	Version         int                         `json:"version"`
	Display         ContributionDisplay         `json:"display"`
	ServiceID       string                      `json:"service_id,omitempty"`
	Surfaces        []ContributedSurface        `json:"surfaces,omitempty"`
	RuntimeProvider *ContributedRuntimeProvider `json:"runtime_provider"`
	AgentOperations []string                    `json:"agent_operations,omitempty"`
}

type ContributionDisplay struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type ContributedSurface struct {
	ID              string                  `json:"id"`
	Label           string                  `json:"label"`
	Description     string                  `json:"description,omitempty"`
	Icon            ContributionIcon        `json:"icon"`
	Placement       string                  `json:"placement"`
	EntryAsset      string                  `json:"entry_asset"`
	Modal           ContributionModal       `json:"modal"`
	StatusOperation string                  `json:"status_operation,omitempty"`
	Operations      []string                `json:"operations,omitempty"`
	Polling         ContributionPolling     `json:"polling"`
	HostIntents     ContributionHostIntents `json:"host_intents"`
}

type ContributionIcon struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type ContributionModal struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type ContributionPolling struct {
	MapSeconds  int `json:"map_seconds"`
	OpenSeconds int `json:"open_seconds"`
}

type ContributionHostIntents struct {
	AskOri       ContributionAskOri    `json:"ask_ori"`
	OpenSetup    ContributionOpenSetup `json:"open_setup"`
	Confirmation bool                  `json:"confirmation"`
	State        bool                  `json:"state"`
	Close        bool                  `json:"close"`
}

type ContributionAskOri struct {
	RequiredCapabilities []string `json:"required_capabilities,omitempty"`
}

type ContributionOpenSetup struct {
	ProviderID string `json:"provider_id,omitempty"`
}

type ContributedRuntimeProvider struct {
	ID             string                    `json:"id"`
	RequirementKey string                    `json:"requirement_key"`
	Operations     RuntimeProviderOperations `json:"operations"`
	Scopes         []string                  `json:"scopes,omitempty"`
}

type RuntimeProviderOperations struct {
	Prerequisites string `json:"prerequisites"`
	Readiness     string `json:"readiness"`
	LiveStatus    string `json:"live_status"`
	Verify        string `json:"verify"`
	Repair        string `json:"repair"`
}

type ContributedService struct {
	ID              string                 `json:"id"`
	Transport       string                 `json:"transport"`
	ProtocolVersion int                    `json:"protocol_version"`
	Entrypoint      ServiceEntrypoint      `json:"entrypoint"`
	Artifacts       []ContributedArtifact  `json:"artifacts"`
	Operations      []ContributedOperation `json:"operations"`
}

type ServiceEntrypoint struct {
	ArtifactID string   `json:"artifact_id"`
	Args       []string `json:"args,omitempty"`
}

type ContributedArtifact struct {
	ID     string         `json:"id"`
	OS     string         `json:"os"`
	Arch   string         `json:"arch"`
	Source ArtifactSource `json:"source"`
	SHA256 string         `json:"sha256"`
	Size   int64          `json:"size"`
}

type ArtifactSource struct {
	Kind string `json:"kind"`
	Path string `json:"path,omitempty"`
	URL  string `json:"url,omitempty"`
}

type ContributedOperation struct {
	ID             string          `json:"id"`
	InputSchema    json.RawMessage `json:"input_schema"`
	OutputSchema   json.RawMessage `json:"output_schema"`
	MaxOutputBytes int             `json:"max_output_bytes"`
	TimeoutClass   string          `json:"timeout_class"`
	Policy         string          `json:"policy"`
	Scopes         []string        `json:"scopes,omitempty"`
}

type ContributedBlueprint struct {
	ID           string   `json:"id"`
	Version      int      `json:"version"`
	Manifest     string   `json:"manifest"`
	Skeleton     string   `json:"skeleton"`
	Capabilities []string `json:"capabilities"`
}

// PublicContribution is the executable-free projection suitable for list and
// preview APIs after trust details have been reported separately.
type PublicContribution struct {
	Protocol     ProtocolRange      `json:"protocol"`
	Capabilities []PublicCapability `json:"capabilities,omitempty"`
	Blueprints   []PublicBlueprint  `json:"blueprints,omitempty"`
}

type PublicCapability struct {
	ID       string              `json:"id"`
	Version  int                 `json:"version"`
	Display  ContributionDisplay `json:"display"`
	Surfaces []PublicSurface     `json:"surfaces,omitempty"`
}

type PublicSurface struct {
	ID          string              `json:"id"`
	Label       string              `json:"label"`
	Description string              `json:"description,omitempty"`
	Icon        ContributionIcon    `json:"icon"`
	Placement   string              `json:"placement"`
	Modal       ContributionModal   `json:"modal"`
	Polling     ContributionPolling `json:"polling"`
}

type PublicBlueprint struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

func ParseSurfaceContribution(data []byte) (*SurfaceContribution, error) {
	if len(bytes.TrimSpace(data)) == 0 || len(data) > maxSurfaceManifestSize {
		return nil, contributionError(CodeContributionInvalid, "manifest", "", "manifest is empty or exceeds 1 MiB", nil)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var contribution SurfaceContribution
	if err := decoder.Decode(&contribution); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, contributionError(CodeContributionInvalid, "manifest", "", "manifest contains an unknown field or invalid JSON", err)
	}
	if err := contribution.Validate(); err != nil {
		return nil, err
	}
	return &contribution, nil
}

func (c *SurfaceContribution) Validate() error {
	if c == nil {
		return contributionError(CodeContributionInvalid, "manifest", "", "manifest is required", nil)
	}
	if c.SchemaVersion != SurfaceSchemaVersion {
		return contributionError(CodeContributionInvalid, "manifest", "schema_version", "schema_version must be 1", nil)
	}
	if strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.Version) == "" || c.Name != strings.TrimSpace(c.Name) || c.Version != strings.TrimSpace(c.Version) {
		return contributionError(CodeContributionInvalid, "manifest", "identity", "name and version are required and canonical", nil)
	}
	maximum := c.Protocol.Max
	if maximum == 0 {
		maximum = c.Protocol.Min
		c.Protocol.Max = maximum
	}
	if c.Protocol.Min < 1 || maximum < c.Protocol.Min {
		return contributionError(CodeProtocolRangeInvalid, "manifest", "protocol", "protocol range is invalid", nil)
	}
	if c.Protocol.Min > SurfaceProtocolVersion || maximum < SurfaceProtocolVersion {
		return contributionError(CodeProtocolIncompatible, "manifest", "protocol", "protocol range does not include host v1", nil)
	}
	if len(c.Capabilities) == 0 && len(c.Services) == 0 && len(c.Blueprints) == 0 {
		return contributionError(CodeContributionInvalid, "manifest", "components", "at least one contribution is required", nil)
	}
	if len(c.Capabilities) > 16 || len(c.Services) > 8 || len(c.Blueprints) > 16 {
		return contributionError(CodeContributionInvalid, "manifest", "components", "component count exceeds v1 limits", nil)
	}
	return validateContributionComponents(c)
}

func (c SurfaceContribution) Public() PublicContribution {
	out := PublicContribution{Protocol: c.Protocol}
	for _, capability := range c.Capabilities {
		public := PublicCapability{ID: capability.ID, Version: capability.Version, Display: capability.Display}
		for _, surface := range capability.Surfaces {
			public.Surfaces = append(public.Surfaces, PublicSurface{
				ID: surface.ID, Label: surface.Label, Description: surface.Description,
				Icon: surface.Icon, Placement: surface.Placement, Modal: surface.Modal, Polling: surface.Polling,
			})
		}
		out.Capabilities = append(out.Capabilities, public)
	}
	for _, blueprint := range c.Blueprints {
		out.Blueprints = append(out.Blueprints, PublicBlueprint{ID: blueprint.ID, Version: blueprint.Version})
	}
	return out
}

func validateContributionComponents(c *SurfaceContribution) error {
	services := make(map[string]*ContributedService, len(c.Services))
	for index := range c.Services {
		service := &c.Services[index]
		component := "service:" + service.ID
		if err := canonicalID(component, "id", service.ID); err != nil {
			return err
		}
		if _, duplicate := services[service.ID]; duplicate {
			return contributionError(CodeComponentDuplicate, component, "id", "service id is duplicated", nil)
		}
		services[service.ID] = service
		if err := validateService(service); err != nil {
			return err
		}
	}

	capabilities := make(map[string]struct{}, len(c.Capabilities))
	for index := range c.Capabilities {
		capability := &c.Capabilities[index]
		component := "capability:" + capability.ID
		if err := canonicalID(component, "id", capability.ID); err != nil {
			return err
		}
		if _, duplicate := capabilities[capability.ID]; duplicate {
			return contributionError(CodeComponentDuplicate, component, "id", "capability id is duplicated", nil)
		}
		capabilities[capability.ID] = struct{}{}
		if err := validateCapabilityContribution(capability, services); err != nil {
			return err
		}
	}

	blueprints := make(map[string]struct{}, len(c.Blueprints))
	for index := range c.Blueprints {
		blueprint := &c.Blueprints[index]
		component := "blueprint:" + blueprint.ID
		if err := canonicalID(component, "id", blueprint.ID); err != nil {
			return err
		}
		if _, duplicate := blueprints[blueprint.ID]; duplicate {
			return contributionError(CodeComponentDuplicate, component, "id", "blueprint id is duplicated", nil)
		}
		blueprints[blueprint.ID] = struct{}{}
		if blueprint.Version < 1 || !safeContributionPath(blueprint.Manifest) || !safeContributionPath(blueprint.Skeleton) {
			return contributionError(CodeAssetPathInvalid, component, "path", "blueprint paths or version are invalid", nil)
		}
		for _, capabilityID := range blueprint.Capabilities {
			if _, exists := capabilities[capabilityID]; !exists {
				return contributionError(CodeComponentUnknown, component, "capabilities", "blueprint references an unknown capability", nil)
			}
		}
	}
	return nil
}

func validateService(service *ContributedService) error {
	component := "service:" + service.ID
	if service.Transport != "mcp_stdio" || service.ProtocolVersion != SurfaceProtocolVersion {
		return contributionError(CodeContributionInvalid, component, "transport", "service must use MCP stdio protocol v1", nil)
	}
	if len(service.Artifacts) == 0 || len(service.Artifacts) > 16 || len(service.Operations) == 0 || len(service.Operations) > 128 {
		return contributionError(CodeContributionInvalid, component, "components", "service artifact/operation count is invalid", nil)
	}
	artifacts := make(map[string]struct{}, len(service.Artifacts))
	platforms := make(map[string]struct{}, len(service.Artifacts))
	for _, artifact := range service.Artifacts {
		if err := canonicalID("artifact:"+artifact.ID, "id", artifact.ID); err != nil {
			return err
		}
		if _, duplicate := artifacts[artifact.ID]; duplicate {
			return contributionError(CodeComponentDuplicate, component, "artifacts", "artifact id is duplicated", nil)
		}
		artifacts[artifact.ID] = struct{}{}
		platform := strings.ToLower(strings.TrimSpace(artifact.OS)) + "/" + strings.ToLower(strings.TrimSpace(artifact.Arch))
		if artifact.OS == "" || artifact.Arch == "" {
			return contributionError(CodeArtifactInvalid, "artifact:"+artifact.ID, "platform", "artifact platform is required", nil)
		}
		if _, duplicate := platforms[platform]; duplicate {
			return contributionError(CodeArtifactPlatformDuplicate, component, "artifacts", "artifact platform is duplicated", nil)
		}
		platforms[platform] = struct{}{}
		if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(artifact.SHA256) || artifact.Size < 1 {
			return contributionError(CodeArtifactInvalid, "artifact:"+artifact.ID, "digest", "artifact digest or size is invalid", nil)
		}
		switch artifact.Source.Kind {
		case "bundled":
			if !safeContributionPath(artifact.Source.Path) || artifact.Source.URL != "" {
				return contributionError(CodeAssetPathInvalid, "artifact:"+artifact.ID, "source.path", "bundled artifact path is invalid", nil)
			}
		case "https":
			if !strings.HasPrefix(artifact.Source.URL, "https://") || artifact.Source.Path != "" {
				return contributionError(CodeArtifactInvalid, "artifact:"+artifact.ID, "source.url", "artifact URL must use HTTPS", nil)
			}
		default:
			return contributionError(CodeArtifactInvalid, "artifact:"+artifact.ID, "source.kind", "artifact source kind is unsupported", nil)
		}
	}
	if _, exists := artifacts[service.Entrypoint.ArtifactID]; !exists {
		return contributionError(CodeComponentUnknown, component, "entrypoint.artifact_id", "entrypoint references an unknown artifact", nil)
	}
	if len(service.Entrypoint.Args) > 32 {
		return contributionError(CodeContributionInvalid, component, "entrypoint.args", "too many entrypoint arguments", nil)
	}
	for _, arg := range service.Entrypoint.Args {
		if len(arg) > 256 || strings.ContainsRune(arg, 0) {
			return contributionError(CodeContributionInvalid, component, "entrypoint.args", "entrypoint argument is invalid", nil)
		}
	}
	operations := make(map[string]struct{}, len(service.Operations))
	for index := range service.Operations {
		operation := &service.Operations[index]
		if err := canonicalID("operation:"+operation.ID, "id", operation.ID); err != nil {
			return err
		}
		if _, duplicate := operations[operation.ID]; duplicate {
			return contributionError(CodeComponentDuplicate, component, "operations", "operation id is duplicated", nil)
		}
		operations[operation.ID] = struct{}{}
		if err := validateOperationContribution(operation); err != nil {
			return err
		}
	}
	return nil
}

func validateCapabilityContribution(capability *ContributedCapability, services map[string]*ContributedService) error {
	component := "capability:" + capability.ID
	if capability.Version < 1 || invalidText(capability.Display.Name, 120, false) || invalidText(capability.Display.Description, 500, true) {
		return contributionError(CodeContributionInvalid, component, "display", "capability version or display text is invalid", nil)
	}
	var service *ContributedService
	if capability.ServiceID != "" {
		service = services[capability.ServiceID]
		if service == nil {
			return contributionError(CodeComponentUnknown, component, "service_id", "capability references an unknown service", nil)
		}
	}
	operationSet := make(map[string]struct{})
	if service != nil {
		for _, operation := range service.Operations {
			operationSet[operation.ID] = struct{}{}
		}
	}
	seenSurfaces := make(map[string]struct{}, len(capability.Surfaces))
	for _, surface := range capability.Surfaces {
		if err := canonicalID("surface:"+surface.ID, "id", surface.ID); err != nil {
			return err
		}
		if _, duplicate := seenSurfaces[surface.ID]; duplicate {
			return contributionError(CodeComponentDuplicate, component, "surfaces", "surface id is duplicated", nil)
		}
		seenSurfaces[surface.ID] = struct{}{}
		if err := validateSurfaceContribution(component, surface, operationSet); err != nil {
			return err
		}
	}
	for _, operationID := range capability.AgentOperations {
		if _, exists := operationSet[operationID]; !exists {
			return contributionError(CodeComponentUnknown, component, "agent_operations", "agent operation is unknown", nil)
		}
	}
	if capability.RuntimeProvider != nil {
		if service == nil {
			return contributionError(CodeComponentUnknown, component, "runtime_provider", "runtime provider requires a service", nil)
		}
		provider := capability.RuntimeProvider
		if err := canonicalID("provider:"+provider.ID, "id", provider.ID); err != nil {
			return err
		}
		if err := canonicalID("provider:"+provider.ID, "requirement_key", provider.RequirementKey); err != nil {
			return err
		}
		for _, operationID := range []string{provider.Operations.Prerequisites, provider.Operations.Readiness, provider.Operations.LiveStatus, provider.Operations.Verify, provider.Operations.Repair} {
			if _, exists := operationSet[operationID]; !exists {
				return contributionError(CodeComponentUnknown, "provider:"+provider.ID, "operations", "provider operation is unknown", nil)
			}
		}
		for _, scope := range provider.Scopes {
			if !knownSymbolicScope(scope) {
				return contributionError(CodeScopeUnknown, "provider:"+provider.ID, "scopes", "provider scope is unknown", nil)
			}
		}
	}
	return nil
}

func validateSurfaceContribution(capability string, surface ContributedSurface, operations map[string]struct{}) error {
	component := capability + "/surface:" + surface.ID
	if invalidText(surface.Label, 120, false) || invalidText(surface.Description, 500, true) {
		return contributionError(CodeContributionInvalid, component, "display", "surface text is invalid", nil)
	}
	if surface.Placement != "map_modal" {
		return contributionError(CodePlacementUnsupported, component, "placement", "only map_modal is supported", nil)
	}
	if !safeContributionPath(surface.EntryAsset) || path.Ext(surface.EntryAsset) != ".html" {
		return contributionError(CodeAssetPathInvalid, component, "entry_asset", "entry asset must be a safe relative HTML path", nil)
	}
	if surface.Icon.Kind != "host" || !canonicalIDValue(surface.Icon.Value) {
		return contributionError(CodeContributionInvalid, component, "icon", "host icon token is invalid", nil)
	}
	if surface.Modal.Width < 320 || surface.Modal.Width > 1600 || surface.Modal.Height < 240 || surface.Modal.Height > 1200 {
		return contributionError(CodeContributionInvalid, component, "modal", "modal dimensions are invalid", nil)
	}
	if surface.Polling.MapSeconds < 5 || surface.Polling.MapSeconds > 60 || surface.Polling.OpenSeconds < 1 || surface.Polling.OpenSeconds > 60 {
		return contributionError(CodeContributionInvalid, component, "polling", "polling interval is invalid", nil)
	}
	seen := make(map[string]struct{}, len(surface.Operations))
	for _, operationID := range surface.Operations {
		if _, exists := operations[operationID]; !exists {
			return contributionError(CodeComponentUnknown, component, "operations", "surface operation is unknown", nil)
		}
		if _, duplicate := seen[operationID]; duplicate {
			return contributionError(CodeComponentDuplicate, component, "operations", "surface operation is duplicated", nil)
		}
		seen[operationID] = struct{}{}
	}
	if surface.StatusOperation != "" {
		if _, declared := seen[surface.StatusOperation]; !declared {
			return contributionError(CodeComponentUnknown, component, "status_operation", "status operation is not declared on the surface", nil)
		}
	}
	for _, capabilityID := range surface.HostIntents.AskOri.RequiredCapabilities {
		if !canonicalIDValue(capabilityID) {
			return contributionError(CodeContributionInvalid, component, "host_intents.ask_ori", "Ask Ori capability id is invalid", nil)
		}
	}
	if providerID := surface.HostIntents.OpenSetup.ProviderID; providerID != "" && !canonicalIDValue(providerID) {
		return contributionError(CodeContributionInvalid, component, "host_intents.open_setup", "Setup provider id is invalid", nil)
	}
	return nil
}

func validateOperationContribution(operation *ContributedOperation) error {
	component := "operation:" + operation.ID
	if operation.MaxOutputBytes < 1 || operation.MaxOutputBytes > 256<<10 {
		return contributionError(CodeOperationLimitInvalid, component, "max_output_bytes", "output limit is invalid", nil)
	}
	if operation.TimeoutClass != "fast" && operation.TimeoutClass != "normal" && operation.TimeoutClass != "long" {
		return contributionError(CodeOperationLimitInvalid, component, "timeout_class", "timeout class is invalid", nil)
	}
	if operation.Policy != "read_only" && operation.Policy != "reversible" && operation.Policy != "confirmation_required" {
		return contributionError(CodeOperationPolicyInvalid, component, "policy", "policy class is invalid", nil)
	}
	if err := validateOperationSchema(operation.InputSchema); err != nil {
		return contributionError(CodeOperationSchemaInvalid, component, "input_schema", "input schema is invalid", err)
	}
	if err := validateOperationSchema(operation.OutputSchema); err != nil {
		return contributionError(CodeOperationSchemaInvalid, component, "output_schema", "output schema is invalid", err)
	}
	for _, scope := range operation.Scopes {
		if !knownSymbolicScope(scope) {
			return contributionError(CodeScopeUnknown, component, "scopes", "operation scope is unknown", nil)
		}
	}
	return nil
}

var canonicalContributionID = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)

func canonicalID(component, field, value string) error {
	if !canonicalIDValue(value) {
		return contributionError(CodeContributionInvalid, component, field, "identifier is not canonical", nil)
	}
	return nil
}

func canonicalIDValue(value string) bool {
	return len(value) >= 1 && len(value) <= 64 && canonicalContributionID.MatchString(value)
}

func safeContributionPath(value string) bool {
	if value == "" || strings.ContainsAny(value, `\%`+"\x00") || strings.HasPrefix(value, "/") {
		return false
	}
	clean := path.Clean(value)
	return clean == value && clean != "." && !strings.HasPrefix(clean, "../")
}

func invalidText(value string, maximum int, allowEmpty bool) bool {
	if !utf8.ValidString(value) || value != strings.TrimSpace(value) || len(value) > maximum || (!allowEmpty && value == "") {
		return true
	}
	for _, r := range value {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return true
		}
	}
	return false
}

func knownSymbolicScope(scope string) bool {
	switch scope {
	case "plugin_data_write", "workspace_project_read", "workspace_project_write":
		return true
	default:
		return false
	}
}

func contributionError(code ContributionErrorCode, component, field, reason string, err error) error {
	return &ContributionError{Code: code, Component: component, Field: field, Reason: reason, Err: err}
}

func ContributionErrorIs(err error, code ContributionErrorCode) bool {
	var descriptorErr *ContributionError
	return errors.As(err, &descriptorErr) && descriptorErr.Code == code
}
