package setupjourney

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
)

// ReviewedIntegrationManager is the canonical plugin lifecycle subset used by
// the closed integration adapter. Mutations continue to delegate to
// plugin.Manager rather than reproducing plugin state.
type ReviewedIntegrationManager interface {
	List() ([]plugin.InstalledPlugin, error)
	Inspect(source string, prefer plugin.SourceFormat) (plugin.PluginDescriptor, plugin.TrustReport, error)
	Install(source string, prefer plugin.SourceFormat, confirm plugin.ConfirmFunc) (plugin.InstalledPlugin, error)
	SetEnabled(name string, enabled bool) error
	UpdateFromSource(name, source string, prefer plugin.SourceFormat, confirm plugin.ConfirmFunc) (plugin.InstalledPlugin, error)
}

type IntegrationEntryResolver func(string) (reviewedintegration.Entry, bool)

// ReviewedIntegrationAdapter is the only setup adapter for integration_install.
// Its registry resolver is host-owned and declarations can select only a key.
type ReviewedIntegrationAdapter struct {
	manager           ReviewedIntegrationManager
	resolve           IntegrationEntryResolver
	platform          string
	developmentSource string
}

func NewReviewedIntegrationAdapter(manager ReviewedIntegrationManager) *ReviewedIntegrationAdapter {
	return newReviewedIntegrationAdapter(manager, reviewedintegration.Get, runtime.GOOS+"/"+runtime.GOARCH)
}

// NewReviewedIntegrationAdapterForDevelopment permits one process-configured
// local source to satisfy the install prerequisite after the same identity and
// contribution validation as a release. It never makes the copy release-ready
// and never exposes or persists the configured path in journey state.
func NewReviewedIntegrationAdapterForDevelopment(manager ReviewedIntegrationManager, source string) *ReviewedIntegrationAdapter {
	adapter := NewReviewedIntegrationAdapter(manager)
	adapter.developmentSource = normalizedLocalDevelopmentSource(source)
	return adapter
}

func newReviewedIntegrationAdapter(manager ReviewedIntegrationManager, resolve IntegrationEntryResolver, platform string) *ReviewedIntegrationAdapter {
	return &ReviewedIntegrationAdapter{manager: manager, resolve: resolve, platform: platform}
}

func (adapter *ReviewedIntegrationAdapter) Read(ctx context.Context, scope ReadScope) (CanonicalStepRead, error) {
	_ = ctx
	if adapter == nil || adapter.manager == nil || adapter.resolve == nil {
		return CanonicalStepRead{BlockedReason: ReasonOwnerUnavailable}, nil
	}
	entry, ok := adapter.resolve(scope.IntegrationKey)
	if !ok || !entryMatchesScope(entry, scope) {
		return CanonicalStepRead{BlockedReason: ReasonIntegrationIdentityMismatch}, nil
	}
	projection := integrationProjection(entry)
	if !contains(entry.SupportedPlatforms, adapter.platform) {
		projection.StateRevision = integrationStateDigest(entry, nil, nil)
		return CanonicalStepRead{BlockedReason: ReasonIntegrationUnsupported, Integration: projection}, nil
	}
	installed, err := adapter.manager.List()
	if err != nil {
		projection.StateRevision = integrationStateDigest(entry, nil, nil)
		return CanonicalStepRead{BlockedReason: ReasonOwnerUnavailable, Integration: projection}, nil
	}
	var current *plugin.InstalledPlugin
	for index := range installed {
		if installed[index].Name == entry.PluginID {
			copy := installed[index]
			current = &copy
			break
		}
	}
	if current != nil {
		projection.InstalledVersion = current.Version
		projection.Enabled = current.Enabled
		if adapter.acceptsDevelopmentSource(current.Source) {
			projection.DevelopmentCopy = true
			projection.StateRevision = integrationStateDigest(entry, current, nil)
			if reason := validateDevelopmentIntegration(entry, *current, adapter.platform); reason != "" {
				return CanonicalStepRead{
					BlockedReason: reason, AvailableActions: []ActionID{ActionManageIntegration},
					Integration: projection, Result: integrationResult(current),
				}, nil
			}
			if !current.Enabled {
				return CanonicalStepRead{
					BlockedReason: ReasonIntegrationDisabled, AvailableActions: []ActionID{ActionManageIntegration},
					Integration: projection, Result: integrationResult(current),
				}, nil
			}
			return CanonicalStepRead{
				Complete: true, AvailableActions: []ActionID{ActionManageIntegration},
				Integration: projection, Result: integrationResult(current),
			}, nil
		}
		if localIntegrationSource(current.Source) {
			projection.StateRevision = integrationStateDigest(entry, current, nil)
			return CanonicalStepRead{
				BlockedReason:    ReasonIntegrationLocalUnverified,
				AvailableActions: []ActionID{ActionManageIntegration}, Integration: projection,
				Result: integrationResult(current),
			}, nil
		}
		if !acceptedPinnedSource(entry, current.Source) || current.Format != entry.SourceFormat {
			projection.StateRevision = integrationStateDigest(entry, current, nil)
			return CanonicalStepRead{
				BlockedReason:    ReasonIntegrationIdentityMismatch,
				AvailableActions: []ActionID{ActionManageIntegration}, Integration: projection,
				Result: integrationResult(current),
			}, nil
		}
	}
	if !entry.ReleaseReady || entry.Source() == "" {
		projection.StateRevision = integrationStateDigest(entry, current, nil)
		return CanonicalStepRead{BlockedReason: ReasonOwnerUnavailable, Integration: projection}, nil
	}
	if current == nil {
		descriptor, report, inspectErr := adapter.manager.Inspect(entry.Source(), entry.SourceFormat)
		if inspectErr != nil {
			projection.StateRevision = integrationStateDigest(entry, nil, nil)
			return CanonicalStepRead{BlockedReason: ReasonOwnerUnavailable, Integration: projection}, nil
		}
		if reason := validateReviewedDescriptor(entry, descriptor, report, adapter.platform); reason != "" {
			projection.StateRevision = integrationStateDigest(entry, nil, &report)
			projection.Trust = cloneTrustReport(&report)
			return CanonicalStepRead{BlockedReason: reason, Integration: projection}, nil
		}
		projection.Trust = cloneTrustReport(&report)
		projection.StateRevision = integrationStateDigest(entry, nil, &report)
		return CanonicalStepRead{AvailableActions: []ActionID{ActionReviewInstall}, Integration: projection}, nil
	}

	if current.Version != entry.ExpectedVersion || current.Source != entry.Source() {
		if compareVersions(current.Version, entry.ExpectedVersion) >= 0 {
			projection.StateRevision = integrationStateDigest(entry, current, nil)
			return CanonicalStepRead{
				BlockedReason:    ReasonIntegrationIdentityMismatch,
				AvailableActions: []ActionID{ActionManageIntegration}, Integration: projection,
				Result: integrationResult(current),
			}, nil
		}
		descriptor, report, inspectErr := adapter.manager.Inspect(entry.Source(), entry.SourceFormat)
		if inspectErr != nil {
			projection.StateRevision = integrationStateDigest(entry, current, nil)
			return CanonicalStepRead{BlockedReason: ReasonOwnerUnavailable, Integration: projection, Result: integrationResult(current)}, nil
		}
		if reason := validateReviewedDescriptor(entry, descriptor, report, adapter.platform); reason != "" {
			projection.StateRevision = integrationStateDigest(entry, current, &report)
			projection.Trust = cloneTrustReport(&report)
			return CanonicalStepRead{BlockedReason: reason, Integration: projection, Result: integrationResult(current)}, nil
		}
		projection.Trust = cloneTrustReport(&report)
		projection.StateRevision = integrationStateDigest(entry, current, &report)
		return CanonicalStepRead{
			AvailableActions: []ActionID{ActionReviewUpdate, ActionManageIntegration},
			Integration:      projection, Result: integrationResult(current),
		}, nil
	}
	if reason := validateInstalledIntegration(entry, *current, adapter.platform); reason != "" {
		projection.StateRevision = integrationStateDigest(entry, current, nil)
		return CanonicalStepRead{
			BlockedReason: reason, AvailableActions: []ActionID{ActionManageIntegration},
			Integration: projection, Result: integrationResult(current),
		}, nil
	}
	projection.StateRevision = integrationStateDigest(entry, current, nil)
	if !current.Enabled {
		return CanonicalStepRead{
			AvailableActions: []ActionID{ActionReviewEnable, ActionManageIntegration},
			Integration:      projection, Result: integrationResult(current),
		}, nil
	}
	return CanonicalStepRead{
		Complete: true, AvailableActions: []ActionID{ActionManageIntegration},
		Integration: projection, Result: integrationResult(current),
	}, nil
}

func (adapter *ReviewedIntegrationAdapter) InputDigest(actionID ActionID, input json.RawMessage) (string, error) {
	if _, review := integrationCommitForReview(actionID); !review {
		if _, commit := integrationReviewForCommit(actionID); !commit {
			return "", ErrInvalid
		}
	}
	return emptyIntegrationInputDigest(input)
}

func (adapter *ReviewedIntegrationAdapter) Review(ctx context.Context, scope ReadScope, actionID ActionID, input json.RawMessage) (ActionReviewMaterial, error) {
	commitAction, ok := integrationCommitForReview(actionID)
	if !ok {
		return ActionReviewMaterial{}, ErrInvalid
	}
	return adapter.reviewMaterial(ctx, scope, actionID, commitAction, input)
}

func (adapter *ReviewedIntegrationAdapter) PrepareCommit(ctx context.Context, scope ReadScope, actionID ActionID, input json.RawMessage) (ActionReviewMaterial, error) {
	reviewAction, ok := integrationReviewForCommit(actionID)
	if !ok {
		return ActionReviewMaterial{}, ErrInvalid
	}
	return adapter.reviewMaterial(ctx, scope, reviewAction, actionID, input)
}

func (adapter *ReviewedIntegrationAdapter) reviewMaterial(ctx context.Context, scope ReadScope, reviewAction, commitAction ActionID, input json.RawMessage) (ActionReviewMaterial, error) {
	inputDigest, err := emptyIntegrationInputDigest(input)
	if err != nil {
		return ActionReviewMaterial{}, err
	}
	state, err := adapter.Read(ctx, scope)
	if err != nil || state.BlockedReason != "" || !containsActionID(state.AvailableActions, reviewAction) || state.Integration == nil {
		return ActionReviewMaterial{}, ErrConflict
	}
	// Enabling is separate from install, but its review still discloses the
	// complete exact candidate trust material.
	if state.Integration.Trust == nil {
		entry, ok := adapter.resolve(scope.IntegrationKey)
		if !ok || entry.Source() == "" {
			return ActionReviewMaterial{}, ErrConflict
		}
		descriptor, report, inspectErr := adapter.manager.Inspect(entry.Source(), entry.SourceFormat)
		if inspectErr != nil || validateReviewedDescriptor(entry, descriptor, report, adapter.platform) != "" {
			return ActionReviewMaterial{}, ErrConflict
		}
		state.Integration.Trust = cloneTrustReport(&report)
	}
	type disclosure struct {
		CommitAction ActionID               `json:"commit_action"`
		Integration  *IntegrationProjection `json:"integration"`
	}
	encoded, err := json.Marshal(disclosure{CommitAction: commitAction, Integration: state.Integration})
	if err != nil {
		return ActionReviewMaterial{}, ErrInvalid
	}
	return ActionReviewMaterial{
		CommitAction: commitAction, InputDigest: inputDigest,
		OwnerRevisionDigest: state.Integration.StateRevision,
		DisclosureDigest:    Digest(encoded), Integration: cloneIntegrationProjection(state.Integration),
	}, nil
}

func (adapter *ReviewedIntegrationAdapter) Commit(ctx context.Context, scope ReadScope, actionID ActionID, input json.RawMessage, reviewed ActionReviewMaterial) (CanonicalResult, error) {
	_ = ctx
	inputDigest, inputErr := emptyIntegrationInputDigest(input)
	if inputErr != nil || inputDigest != reviewed.InputDigest ||
		reviewed.CommitAction != actionID || reviewed.Integration == nil {
		return CanonicalResult{}, ErrInvalid
	}
	entry, ok := adapter.resolve(scope.IntegrationKey)
	if !ok || !entryMatchesScope(entry, scope) || entry.Source() == "" {
		return CanonicalResult{}, ErrConflict
	}
	confirm := func(report plugin.TrustReport) bool {
		return reviewed.Integration.Trust != nil &&
			trustReportDigest(report) == trustReportDigest(*reviewed.Integration.Trust)
	}
	var installed plugin.InstalledPlugin
	var err error
	switch actionID {
	case ActionInstall:
		installed, err = adapter.manager.Install(entry.Source(), entry.SourceFormat, confirm)
	case ActionEnable:
		err = adapter.manager.SetEnabled(entry.PluginID, true)
	case ActionUpdate:
		installed, err = adapter.manager.UpdateFromSource(entry.PluginID, entry.Source(), entry.SourceFormat, confirm)
	default:
		return CanonicalResult{}, ErrInvalid
	}
	if err != nil {
		return CanonicalResult{}, err
	}
	if actionID == ActionEnable {
		plugins, listErr := adapter.manager.List()
		if listErr != nil {
			return CanonicalResult{}, listErr
		}
		for index := range plugins {
			if plugins[index].Name == entry.PluginID {
				installed = plugins[index]
				break
			}
		}
	}
	if installed.Name != entry.PluginID {
		return CanonicalResult{}, ErrConflict
	}
	return integrationResult(&installed), nil
}

func (adapter *ReviewedIntegrationAdapter) ConsequenceObserved(actionID ActionID, read CanonicalStepRead) bool {
	switch actionID {
	case ActionInstall, ActionUpdate:
		return read.Result.IntegrationPluginID != "" && read.Integration != nil &&
			read.Result.IntegrationVersion == read.Integration.ExpectedVersion &&
			read.BlockedReason == "" && acceptedPinnedSourceForProjection(read.Integration)
	case ActionEnable:
		return read.Complete && read.Integration != nil && read.Integration.Enabled
	default:
		return false
	}
}

func acceptedPinnedSourceForProjection(projection *IntegrationProjection) bool {
	// Read validates the private exact source before producing an unblocked
	// installed projection; this helper deliberately cannot reconstruct it from
	// browser-visible fields.
	return projection != nil && projection.ReleaseReady
}

func emptyIntegrationInputDigest(input json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(input))
	if trimmed == "" {
		trimmed = "{}"
	}
	if trimmed != "{}" {
		return "", ErrInvalid
	}
	return Digest([]byte("{}")), nil
}

func integrationCommitForReview(action ActionID) (ActionID, bool) {
	switch action {
	case ActionReviewInstall:
		return ActionInstall, true
	case ActionReviewEnable:
		return ActionEnable, true
	case ActionReviewUpdate:
		return ActionUpdate, true
	default:
		return "", false
	}
}

func integrationReviewForCommit(action ActionID) (ActionID, bool) {
	switch action {
	case ActionInstall:
		return ActionReviewInstall, true
	case ActionEnable:
		return ActionReviewEnable, true
	case ActionUpdate:
		return ActionReviewUpdate, true
	default:
		return "", false
	}
}

func integrationProjection(entry reviewedintegration.Entry) *IntegrationProjection {
	return &IntegrationProjection{
		Key: entry.Key, PluginID: entry.PluginID, Publisher: entry.PublisherLabel,
		SourceLabel: entry.SourceLabel, ExpectedVersion: entry.ExpectedVersion,
		ReleaseReady: entry.ReleaseReady, ExpectedBlueprintID: entry.ExpectedBlueprintID,
		ExpectedProgramID:    entry.ExpectedProgramID,
		RequiredHostFeatures: append([]string(nil), entry.RequiredHostFeatures...),
		ExpectedProtocol:     entry.ExpectedProtocol,
		SupportedPlatforms:   append([]string(nil), entry.SupportedPlatforms...),
	}
}

func entryMatchesScope(entry reviewedintegration.Entry, scope ReadScope) bool {
	return entry.Key == scope.IntegrationKey && entry.ExpectedBlueprintID == scope.ExpectedBlueprintID &&
		entry.ExpectedProgramID == scope.ExpectedAssistantProgramID
}

func localIntegrationSource(source string) bool {
	source = strings.TrimSpace(source)
	if source == "" {
		return false
	}
	return !strings.HasPrefix(source, "https://") &&
		!strings.HasPrefix(source, "http://") &&
		!strings.HasPrefix(source, "git@") &&
		!strings.HasSuffix(source, ".git")
}

func normalizedLocalDevelopmentSource(source string) string {
	source = strings.TrimSpace(source)
	if !localIntegrationSource(source) || !filepath.IsAbs(source) {
		return ""
	}
	return filepath.Clean(source)
}

func (adapter *ReviewedIntegrationAdapter) acceptsDevelopmentSource(source string) bool {
	if adapter == nil || adapter.developmentSource == "" {
		return false
	}
	return normalizedLocalDevelopmentSource(source) == adapter.developmentSource
}

func acceptedPinnedSource(entry reviewedintegration.Entry, source string) bool {
	source = strings.TrimSpace(source)
	prefix := entry.SourceRepository + "#sha="
	if !strings.HasPrefix(source, prefix) || len(source) != len(prefix)+40 {
		return false
	}
	for _, char := range source[len(prefix):] {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func validateReviewedDescriptor(entry reviewedintegration.Entry, descriptor plugin.PluginDescriptor, report plugin.TrustReport, platform string) ReasonCode {
	if descriptor.Name != entry.PluginID || descriptor.Version != entry.ExpectedVersion ||
		descriptor.SourceLocation != entry.Source() || descriptor.SourceFormat != entry.SourceFormat ||
		report.Name != entry.PluginID || report.Format != entry.SourceFormat ||
		trustReportDigest(report) != trustReportDigest(plugin.BuildTrustReport(descriptor)) {
		return ReasonIntegrationIdentityMismatch
	}
	if reason := validateContribution(entry, descriptor.WorkspaceSurfaces, descriptor.ResolvedBlueprints, platform); reason != "" {
		return reason
	}
	if len(report.Unsupported) != 0 {
		return ReasonIntegrationUnsupported
	}
	return ""
}

func validateInstalledIntegration(entry reviewedintegration.Entry, installed plugin.InstalledPlugin, platform string) ReasonCode {
	if installed.Source != entry.Source() {
		return ReasonIntegrationIdentityMismatch
	}
	return validateDevelopmentIntegration(entry, installed, platform)
}

func validateDevelopmentIntegration(entry reviewedintegration.Entry, installed plugin.InstalledPlugin, platform string) ReasonCode {
	if installed.Name != entry.PluginID || installed.Version != entry.ExpectedVersion ||
		installed.Format != entry.SourceFormat || strings.TrimSpace(installed.ComponentFingerprint) == "" ||
		installed.Generation == 0 || installed.Generation > math.MaxInt64 {
		return ReasonIntegrationIdentityMismatch
	}
	return validateContribution(entry, installed.WorkspaceSurfaces, installed.ResolvedBlueprints, platform)
}

func validateContribution(entry reviewedintegration.Entry, contribution *plugin.SurfaceContribution, blueprints []plugin.ResolvedBlueprint, platform string) ReasonCode {
	if contribution == nil || contribution.Name != entry.PluginID || contribution.Version != entry.ExpectedVersion ||
		contribution.Protocol.Min > entry.ExpectedProtocol ||
		(contribution.Protocol.Max != 0 && contribution.Protocol.Max < entry.ExpectedProtocol) ||
		!containsAll(contribution.RequiresHostFeatures, entry.RequiredHostFeatures) {
		return ReasonIntegrationUnsupported
	}
	blueprintFound := false
	platformFound := false
	for _, blueprint := range blueprints {
		if blueprint.ID != entry.ExpectedBlueprintID {
			continue
		}
		if blueprintFound || blueprint.Version != entry.ExpectedBlueprintVersion ||
			blueprint.Template.AssistantProgram == nil ||
			blueprint.Template.AssistantProgram.ID != entry.ExpectedProgramID ||
			blueprint.Template.AssistantProgram.SchemaVersion != entry.ExpectedProgramSchema {
			return ReasonIntegrationUnsupported
		}
		blueprintFound = true
	}
	for _, service := range contribution.Services {
		for _, artifact := range service.Artifacts {
			if artifact.OS+"/"+artifact.Arch == platform {
				platformFound = true
			}
		}
	}
	if !blueprintFound || (contains(entry.SupportedPlatforms, platform) && !platformFound) {
		return ReasonIntegrationUnsupported
	}
	return ""
}

func containsAll(values, required []string) bool {
	for _, expected := range required {
		if !contains(values, expected) {
			return false
		}
	}
	return true
}

func containsActionID(actions []ActionID, expected ActionID) bool {
	for _, action := range actions {
		if action == expected {
			return true
		}
	}
	return false
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func integrationResult(installed *plugin.InstalledPlugin) CanonicalResult {
	if installed == nil {
		return CanonicalResult{}
	}
	result := CanonicalResult{
		IntegrationPluginID: installed.Name, IntegrationVersion: installed.Version,
	}
	revision, err := strconv.ParseInt(strconv.FormatUint(installed.Generation, 10), 10, 64)
	if err == nil && revision > 0 {
		result.OwnerRevisions = []OwnerRevision{{Owner: OwnerPlugin, Revision: revision}}
	}
	return result
}

func trustReportDigest(report plugin.TrustReport) string {
	// Plugin trust semantics do not distinguish an absent list from an empty
	// list. Canonicalize that representation so cloning for an HTTP disclosure
	// cannot make a reviewed confirmation spuriously stale.
	if len(report.MCPCommands) == 0 {
		report.MCPCommands = nil
	}
	if len(report.Skills) == 0 {
		report.Skills = nil
	}
	if len(report.SurfaceCapabilities) == 0 {
		report.SurfaceCapabilities = nil
	}
	if len(report.Surfaces) == 0 {
		report.Surfaces = nil
	}
	if len(report.Services) == 0 {
		report.Services = nil
	}
	for index := range report.Services {
		if len(report.Services[index].Platforms) == 0 {
			report.Services[index].Platforms = nil
		}
	}
	if len(report.Operations) == 0 {
		report.Operations = nil
	}
	for index := range report.Operations {
		if len(report.Operations[index].Scopes) == 0 {
			report.Operations[index].Scopes = nil
		}
	}
	if len(report.Artifacts) == 0 {
		report.Artifacts = nil
	}
	if len(report.SymbolicScopes) == 0 {
		report.SymbolicScopes = nil
	}
	if len(report.Blueprints) == 0 {
		report.Blueprints = nil
	}
	if len(report.Unsupported) == 0 {
		report.Unsupported = nil
	}
	if len(report.Warnings) == 0 {
		report.Warnings = nil
	}
	encoded, _ := json.Marshal(report)
	return Digest(encoded)
}

func integrationStateDigest(entry reviewedintegration.Entry, installed *plugin.InstalledPlugin, report *plugin.TrustReport) string {
	type state struct {
		RegistryRevision int
		EntryKey         string
		ExpectedVersion  string
		SourceCommit     string
		InstalledVersion string
		InstalledSource  string
		Fingerprint      string
		Generation       uint64
		Enabled          bool
		Trust            *plugin.TrustReport
	}
	value := state{RegistryRevision: reviewedintegration.RegistryRevision, EntryKey: entry.Key,
		ExpectedVersion: entry.ExpectedVersion, SourceCommit: entry.SourceCommit, Trust: report}
	if installed != nil {
		value.InstalledVersion = installed.Version
		value.InstalledSource = installed.Source
		value.Fingerprint = installed.ComponentFingerprint
		value.Generation = installed.Generation
		value.Enabled = installed.Enabled
	}
	encoded, _ := json.Marshal(value)
	return Digest(encoded)
}

func compareVersions(left, right string) int {
	parse := func(value string) ([3]int, bool) {
		var parsed [3]int
		parts := strings.SplitN(strings.SplitN(value, "-", 2)[0], "+", 2)
		segments := strings.Split(parts[0], ".")
		if len(segments) != 3 {
			return parsed, false
		}
		for index, segment := range segments {
			number, err := strconv.Atoi(segment)
			if err != nil || number < 0 {
				return parsed, false
			}
			parsed[index] = number
		}
		return parsed, true
	}
	leftParts, leftOK := parse(left)
	rightParts, rightOK := parse(right)
	if !leftOK || !rightOK {
		return 0
	}
	for index := range leftParts {
		if leftParts[index] < rightParts[index] {
			return -1
		}
		if leftParts[index] > rightParts[index] {
			return 1
		}
	}
	return 0
}

var _ CanonicalReader = (*ReviewedIntegrationAdapter)(nil)
