// Package workspacesurface owns the generic, plugin-neutral Workspace Surface
// contract inside Ori. Public descriptors are inert display/identity metadata;
// executable paths and runtime implementations live only in trusted Bindings
// registered by the installed-plugin lifecycle.
package workspacesurface

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	ProtocolVersion = 1

	maxIDBytes          = 64
	maxLabelBytes       = 120
	maxDescriptionBytes = 500
	maxStationValue     = 160
)

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)

type OwnerKind string

const (
	OwnerPlugin  OwnerKind = "plugin"
	OwnerBuiltin OwnerKind = "builtin"
)

// Owner identifies the globally trusted contribution that owns a capability.
// Generation changes whenever its executable contribution changes or its
// lifecycle invalidates existing surface sessions.
type Owner struct {
	Kind        OwnerKind `json:"kind"`
	ID          string    `json:"id"`
	Version     string    `json:"version"`
	Generation  uint64    `json:"generation"`
	ProtocolMin int       `json:"protocol_min"`
	ProtocolMax int       `json:"protocol_max"`
}

// Display is bounded text shown before a capability is opened.
type Display struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type Icon struct {
	// V1 registry descriptors carry host icon tokens. Asset-backed icons are
	// resolved to versioned host URLs at the HTTP projection boundary later;
	// an installed asset path never enters this inert descriptor.
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type Modal struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type Polling struct {
	MapSeconds  int `json:"map_seconds"`
	OpenSeconds int `json:"open_seconds"`
}

// Surface describes only stable identity and bounded presentation. In
// particular, it contains no command, service method, asset path, module, URL,
// endpoint, filesystem path, or schema.
type Surface struct {
	ID              string   `json:"id"`
	Label           string   `json:"label"`
	Description     string   `json:"description,omitempty"`
	Icon            Icon     `json:"icon"`
	Placement       string   `json:"placement"`
	Modal           Modal    `json:"modal"`
	Polling         Polling  `json:"polling"`
	OperationIDs    []string `json:"operation_ids,omitempty"`
	StatusOperation string   `json:"status_operation,omitempty"`
}

// Capability is the inert definition contributed by one trusted owner. Owner is
// held by Registration/RegisteredSurface so a plugin cannot spoof it inside a
// local capability declaration.
type Capability struct {
	ID       string    `json:"id"`
	Version  int       `json:"version"`
	Display  Display   `json:"display"`
	Surfaces []Surface `json:"surfaces,omitempty"`
}

// StationState is the generic status vocabulary rendered by the host.
type StationState string

const (
	StationChecking    StationState = "checking"
	StationReady       StationState = "ready"
	StationAttention   StationState = "attention"
	StationDegraded    StationState = "degraded"
	StationUnavailable StationState = "unavailable"
	StationDisabled    StationState = "disabled"
)

type StationStatus struct {
	State       StationState `json:"state"`
	Value       string       `json:"value,omitempty"`
	Description string       `json:"description,omitempty"`
	CheckedAt   string       `json:"checked_at,omitempty"`
}

type TimeoutClass string

const (
	TimeoutFast   TimeoutClass = "fast"
	TimeoutNormal TimeoutClass = "normal"
	TimeoutLong   TimeoutClass = "long"
)

type PolicyClass string

const (
	PolicyReadOnly             PolicyClass = "read_only"
	PolicyReversible           PolicyClass = "reversible"
	PolicyConfirmationRequired PolicyClass = "confirmation_required"
)

// Operation is trusted installed-registry policy. It is not serialized into a
// workspace record or public station catalog.
type Operation struct {
	ID             string
	InputSchema    json.RawMessage
	OutputSchema   json.RawMessage
	MaxOutputBytes int
	Timeout        TimeoutClass
	Policy         PolicyClass
	Scopes         []string
}

// WorkspaceContext is authoritative host context injected only after workspace
// ownership and capability attachment checks. Browser input cannot construct or
// override it.
type WorkspaceContext struct {
	WorkspaceID    string
	WorkspaceRoot  string
	ProjectEntry   string
	PluginDataRoot string
	Scopes         []ResolvedScope
}

type ResolvedScope struct {
	ID    string
	Roots []string
}

type Invocation struct {
	Workspace WorkspaceContext
	Operation string
	Input     json.RawMessage
}

type Result struct {
	Output json.RawMessage
}

// Runtime is the narrow trusted seam between the generic registry/broker and a
// service implementation. The browser never receives this value.
type Runtime interface {
	Status(context.Context, WorkspaceContext) (StationStatus, error)
	Invoke(context.Context, Invocation) (Result, error)
}

// Binding carries executable trust and therefore deliberately has no JSON
// tags. AssetRoot, EntryAsset, operation policy, and Runtime are held only by
// the global installed-plugin registry.
type Binding struct {
	CapabilityID string
	SurfaceID    string
	AssetRoot    string
	EntryAsset   string
	Operations   map[string]Operation
	Runtime      Runtime
}

// Registration atomically pairs one owner's inert capability descriptors with
// every trusted runtime binding they require.
type Registration struct {
	Owner        Owner
	Capabilities []Capability
	Bindings     []Binding
}

// RegisteredSurface is the registry's inert/public resolution result.
type RegisteredSurface struct {
	Key        string     `json:"key"`
	Owner      Owner      `json:"owner"`
	Capability Capability `json:"capability"`
	Surface    Surface    `json:"surface"`
}

func (o Owner) key() string { return string(o.Kind) + ":" + o.ID }

func CapabilityKey(owner Owner, capabilityID string) string {
	return owner.key() + ":" + capabilityID
}

func SurfaceKey(owner Owner, capabilityID, surfaceID string) string {
	return CapabilityKey(owner, capabilityID) + ":" + surfaceID
}

func normalizeProtocolMax(owner Owner) int {
	if owner.ProtocolMax == 0 {
		return owner.ProtocolMin
	}
	return owner.ProtocolMax
}

func validateRegistration(reg Registration) error {
	if err := validateOwner(reg.Owner); err != nil {
		return err
	}
	if len(reg.Capabilities) == 0 {
		return fmt.Errorf("workspace surface registration: at least one capability is required")
	}

	capabilities := make(map[string]Capability, len(reg.Capabilities))
	surfaces := make(map[string]Surface)
	for _, capability := range reg.Capabilities {
		if err := validateCapability(capability); err != nil {
			return err
		}
		if _, duplicate := capabilities[capability.ID]; duplicate {
			return fmt.Errorf("workspace surface capability %q is registered twice", capability.ID)
		}
		capabilities[capability.ID] = capability
		for _, surface := range capability.Surfaces {
			localKey := capability.ID + "\x00" + surface.ID
			if _, duplicate := surfaces[localKey]; duplicate {
				return fmt.Errorf("workspace surface %q/%q is registered twice", capability.ID, surface.ID)
			}
			surfaces[localKey] = surface
		}
	}

	bindings := make(map[string]Binding, len(reg.Bindings))
	for _, binding := range reg.Bindings {
		localKey := binding.CapabilityID + "\x00" + binding.SurfaceID
		surface, exists := surfaces[localKey]
		if !exists {
			return fmt.Errorf("workspace surface binding %q/%q has no inert descriptor", binding.CapabilityID, binding.SurfaceID)
		}
		if _, duplicate := bindings[localKey]; duplicate {
			return fmt.Errorf("workspace surface binding %q/%q is registered twice", binding.CapabilityID, binding.SurfaceID)
		}
		if err := validateBinding(binding, surface); err != nil {
			return err
		}
		bindings[localKey] = binding
	}
	for localKey := range surfaces {
		if _, exists := bindings[localKey]; !exists {
			return fmt.Errorf("workspace surface %q has no trusted runtime binding", strings.ReplaceAll(localKey, "\x00", "/"))
		}
	}
	return nil
}

func validateOwner(owner Owner) error {
	if owner.Kind != OwnerPlugin && owner.Kind != OwnerBuiltin {
		return fmt.Errorf("workspace surface owner kind %q is invalid", owner.Kind)
	}
	if err := validateID("owner", owner.ID); err != nil {
		return err
	}
	if strings.TrimSpace(owner.Version) == "" || owner.Version != strings.TrimSpace(owner.Version) || len(owner.Version) > maxLabelBytes {
		return fmt.Errorf("workspace surface owner %q has an invalid version", owner.ID)
	}
	if owner.Generation == 0 {
		return fmt.Errorf("workspace surface owner %q generation must be positive", owner.ID)
	}
	maximum := normalizeProtocolMax(owner)
	if owner.ProtocolMin < 1 || maximum < owner.ProtocolMin {
		return fmt.Errorf("workspace surface owner %q has an invalid protocol range", owner.ID)
	}
	if owner.ProtocolMin > ProtocolVersion || maximum < ProtocolVersion {
		return fmt.Errorf("workspace surface owner %q does not support protocol %d", owner.ID, ProtocolVersion)
	}
	return nil
}

func validateCapability(capability Capability) error {
	if err := validateID("capability", capability.ID); err != nil {
		return err
	}
	if capability.Version < 1 {
		return fmt.Errorf("workspace surface capability %q version must be positive", capability.ID)
	}
	if err := validateText("capability name", capability.Display.Name, maxLabelBytes, false); err != nil {
		return err
	}
	if err := validateText("capability description", capability.Display.Description, maxDescriptionBytes, true); err != nil {
		return err
	}
	for _, surface := range capability.Surfaces {
		if err := validateSurface(surface); err != nil {
			return fmt.Errorf("workspace surface capability %q: %w", capability.ID, err)
		}
	}
	return nil
}

func validateSurface(surface Surface) error {
	if err := validateID("surface", surface.ID); err != nil {
		return err
	}
	if err := validateText("surface label", surface.Label, maxLabelBytes, false); err != nil {
		return err
	}
	if err := validateText("surface description", surface.Description, maxDescriptionBytes, true); err != nil {
		return err
	}
	if surface.Icon.Kind != "host" || validateID("host icon", surface.Icon.Value) != nil {
		return fmt.Errorf("surface %q must use a valid host icon token", surface.ID)
	}
	if surface.Placement != "map_modal" {
		return fmt.Errorf("surface %q placement %q is unsupported", surface.ID, surface.Placement)
	}
	if surface.Modal.Width < 320 || surface.Modal.Width > 1600 || surface.Modal.Height < 240 || surface.Modal.Height > 1200 {
		return fmt.Errorf("surface %q modal dimensions are outside v1 limits", surface.ID)
	}
	if surface.Polling.MapSeconds < 5 || surface.Polling.MapSeconds > 60 || surface.Polling.OpenSeconds < 1 || surface.Polling.OpenSeconds > 60 {
		return fmt.Errorf("surface %q polling intervals are outside v1 limits", surface.ID)
	}
	seen := make(map[string]struct{}, len(surface.OperationIDs))
	for _, operationID := range surface.OperationIDs {
		if err := validateID("operation", operationID); err != nil {
			return err
		}
		if _, duplicate := seen[operationID]; duplicate {
			return fmt.Errorf("surface %q operation %q is declared twice", surface.ID, operationID)
		}
		seen[operationID] = struct{}{}
	}
	if surface.StatusOperation != "" {
		if _, declared := seen[surface.StatusOperation]; !declared {
			return fmt.Errorf("surface %q status operation %q is not declared", surface.ID, surface.StatusOperation)
		}
	}
	return nil
}

func validateBinding(binding Binding, surface Surface) error {
	if binding.Runtime == nil {
		return fmt.Errorf("workspace surface binding %q/%q has no runtime", binding.CapabilityID, binding.SurfaceID)
	}
	if !filepath.IsAbs(binding.AssetRoot) || filepath.Clean(binding.AssetRoot) != binding.AssetRoot {
		return fmt.Errorf("workspace surface binding %q/%q has an invalid trusted asset root", binding.CapabilityID, binding.SurfaceID)
	}
	if !safeRelativeAssetPath(binding.EntryAsset) || !strings.EqualFold(path.Ext(binding.EntryAsset), ".html") {
		return fmt.Errorf("workspace surface binding %q/%q has an invalid HTML entry asset", binding.CapabilityID, binding.SurfaceID)
	}
	if len(binding.Operations) != len(surface.OperationIDs) {
		return fmt.Errorf("workspace surface binding %q/%q operation set does not match its inert descriptor", binding.CapabilityID, binding.SurfaceID)
	}
	for _, operationID := range surface.OperationIDs {
		operation, exists := binding.Operations[operationID]
		if !exists || operation.ID != operationID {
			return fmt.Errorf("workspace surface binding %q/%q does not define operation %q", binding.CapabilityID, binding.SurfaceID, operationID)
		}
		if err := validateOperation(operation); err != nil {
			return err
		}
	}
	if surface.StatusOperation != "" {
		status := binding.Operations[surface.StatusOperation]
		if status.Policy != PolicyReadOnly {
			return fmt.Errorf("workspace surface status operation %q must be read-only", status.ID)
		}
	}
	return nil
}

func validateOperation(operation Operation) error {
	if err := validateID("operation", operation.ID); err != nil {
		return err
	}
	if !json.Valid(operation.InputSchema) || !json.Valid(operation.OutputSchema) {
		return fmt.Errorf("workspace surface operation %q has invalid JSON schema", operation.ID)
	}
	if operation.MaxOutputBytes < 1 || operation.MaxOutputBytes > 256<<10 {
		return fmt.Errorf("workspace surface operation %q output limit is invalid", operation.ID)
	}
	if !slices.Contains([]TimeoutClass{TimeoutFast, TimeoutNormal, TimeoutLong}, operation.Timeout) {
		return fmt.Errorf("workspace surface operation %q timeout class is invalid", operation.ID)
	}
	if !slices.Contains([]PolicyClass{PolicyReadOnly, PolicyReversible, PolicyConfirmationRequired}, operation.Policy) {
		return fmt.Errorf("workspace surface operation %q policy class is invalid", operation.ID)
	}
	for _, scope := range operation.Scopes {
		if err := validateID("symbolic scope", scope); err != nil {
			return err
		}
	}
	return nil
}

func validateID(kind, value string) error {
	if len(value) == 0 || len(value) > maxIDBytes || value != strings.TrimSpace(value) || !idPattern.MatchString(value) {
		return fmt.Errorf("workspace surface %s id %q is invalid", kind, value)
	}
	return nil
}

func validateText(kind, value string, maximum int, allowEmpty bool) error {
	if !utf8.ValidString(value) || len(value) > maximum || value != strings.TrimSpace(value) || (!allowEmpty && value == "") {
		return fmt.Errorf("workspace surface %s is invalid", kind)
	}
	for _, r := range value {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return fmt.Errorf("workspace surface %s contains control characters", kind)
		}
	}
	return nil
}

func safeRelativeAssetPath(value string) bool {
	if value == "" || strings.ContainsAny(value, `\%`+"\x00") || strings.HasPrefix(value, "/") {
		return false
	}
	clean := path.Clean(value)
	return clean == value && clean != "." && !strings.HasPrefix(clean, "../")
}

func cloneOwner(owner Owner) Owner { return owner }

func cloneSurface(surface Surface) Surface {
	copy := surface
	copy.OperationIDs = append([]string(nil), surface.OperationIDs...)
	return copy
}

func cloneCapability(capability Capability) Capability {
	copy := capability
	copy.Surfaces = make([]Surface, len(capability.Surfaces))
	for index, surface := range capability.Surfaces {
		copy.Surfaces[index] = cloneSurface(surface)
	}
	return copy
}

func cloneOperation(operation Operation) Operation {
	copy := operation
	copy.InputSchema = append(json.RawMessage(nil), operation.InputSchema...)
	copy.OutputSchema = append(json.RawMessage(nil), operation.OutputSchema...)
	copy.Scopes = append([]string(nil), operation.Scopes...)
	return copy
}

func cloneBinding(binding Binding) Binding {
	copy := binding
	copy.Operations = make(map[string]Operation, len(binding.Operations))
	for id, operation := range binding.Operations {
		copy.Operations[id] = cloneOperation(operation)
	}
	return copy
}
