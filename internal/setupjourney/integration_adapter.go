package setupjourney

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/johnjallday/ori-agent/internal/integrationrelease"
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

// IntegrationReleaseResolver chooses the releases a reviewed integration may be
// installed or replaced with. It never fails: when the latest release cannot be
// resolved it reports the entry's floor with Fallback set.
//
// Candidates is ordered newest first and always ends with the floor. Resolving
// which of them this build can actually load is the adapter's job, not the
// resolver's — only the adapter can inspect a release.
type IntegrationReleaseResolver interface {
	Resolve(context.Context, reviewedintegration.Entry) integrationrelease.Resolution
	Candidates(context.Context, reviewedintegration.Entry) []integrationrelease.Resolution
}

// floorReleases always targets the compiled floor. It is the release resolver
// of an adapter constructed without one.
type floorReleases struct{}

func (floorReleases) Resolve(_ context.Context, entry reviewedintegration.Entry) integrationrelease.Resolution {
	return integrationrelease.Floor(entry)
}

func (releases floorReleases) Candidates(ctx context.Context, entry reviewedintegration.Entry) []integrationrelease.Resolution {
	return []integrationrelease.Resolution{releases.Resolve(ctx, entry)}
}

// ReviewedIntegrationAdapter is the only setup adapter for integration_install.
// Its registry resolver is host-owned and declarations can select only a key.
type ReviewedIntegrationAdapter struct {
	manager           ReviewedIntegrationManager
	resolve           IntegrationEntryResolver
	releases          IntegrationReleaseResolver
	platform          string
	developmentSource string
}

// NewReviewedIntegrationAdapter builds the adapter over the built-in registry.
// releases chooses the release to install; nil targets each entry's floor.
func NewReviewedIntegrationAdapter(manager ReviewedIntegrationManager, releases IntegrationReleaseResolver) *ReviewedIntegrationAdapter {
	adapter := newReviewedIntegrationAdapter(manager, reviewedintegration.Get, runtime.GOOS+"/"+runtime.GOARCH)
	if releases != nil {
		adapter.releases = releases
	}
	return adapter
}

// NewReviewedIntegrationAdapterForDevelopment permits one process-configured
// local source to satisfy the install prerequisite after the same identity and
// contribution validation as a release. It never makes the copy release-ready
// and never exposes or persists the configured path in journey state.
func NewReviewedIntegrationAdapterForDevelopment(manager ReviewedIntegrationManager, releases IntegrationReleaseResolver, source string) *ReviewedIntegrationAdapter {
	adapter := NewReviewedIntegrationAdapter(manager, releases)
	adapter.developmentSource = normalizedLocalDevelopmentSource(source)
	return adapter
}

func newReviewedIntegrationAdapter(manager ReviewedIntegrationManager, resolve IntegrationEntryResolver, platform string) *ReviewedIntegrationAdapter {
	return &ReviewedIntegrationAdapter{manager: manager, resolve: resolve, releases: floorReleases{}, platform: platform}
}

// installedIntegrationSource classifies an installed plugin's recorded source
// against one registry entry. Only an official exact commit can be verified.
type installedIntegrationSource int

const (
	installedSourceUnrecognized installedIntegrationSource = iota
	installedSourceDevelopment
	installedSourceLocal
	installedSourceOfficialPinned
	installedSourceOfficialMutable
)

func (adapter *ReviewedIntegrationAdapter) classifyInstalledSource(entry reviewedintegration.Entry, source string) installedIntegrationSource {
	switch {
	case adapter.acceptsDevelopmentSource(source):
		return installedSourceDevelopment
	case localIntegrationSource(source):
		return installedSourceLocal
	case acceptedPinnedSource(entry, source):
		return installedSourceOfficialPinned
	case entry.IsUnpinnedOfficialSource(source):
		// The official repository's legacy unpinned URLs permit a review of the
		// host-selected replacement; they never prove the installed bytes.
		return installedSourceOfficialMutable
	default:
		return installedSourceUnrecognized
	}
}

func (adapter *ReviewedIntegrationAdapter) Read(ctx context.Context, scope ReadScope) (CanonicalStepRead, error) {
	if adapter == nil || adapter.manager == nil || adapter.resolve == nil || adapter.releases == nil {
		return CanonicalStepRead{BlockedReason: ReasonOwnerUnavailable}, nil
	}
	entry, ok := adapter.resolve(scope.IntegrationKey)
	if !ok || !entryMatchesScope(entry, scope) {
		return CanonicalStepRead{BlockedReason: ReasonIntegrationIdentityMismatch}, nil
	}
	projection := integrationProjection(entry)
	if !contains(entry.SupportedPlatforms, adapter.platform) {
		projection.StateRevision = integrationStateDigest(entry, nil, nil, nil, "")
		return CanonicalStepRead{BlockedReason: ReasonIntegrationUnsupported, Integration: projection}, nil
	}
	installed, err := adapter.manager.List()
	if err != nil {
		projection.StateRevision = integrationStateDigest(entry, nil, nil, nil, "")
		return CanonicalStepRead{BlockedReason: ReasonOwnerUnavailable, Integration: projection}, nil
	}
	current := findInstalledIntegration(installed, entry.PluginID)
	manage := func(reason ReasonCode) CanonicalStepRead {
		projection.StateRevision = integrationStateDigest(entry, nil, current, nil, "")
		return CanonicalStepRead{
			BlockedReason: reason, AvailableActions: []ActionID{ActionManageIntegration},
			Integration: projection, Result: integrationResult(current),
		}
	}
	source := installedSourceUnrecognized
	if current != nil {
		projection.InstalledVersion = current.Version
		projection.Enabled = current.Enabled
		source = adapter.classifyInstalledSource(entry, current.Source)
		switch source {
		case installedSourceDevelopment:
			projection.DevelopmentCopy = true
			if reason := validateInstalledRelease(entry, *current, adapter.platform); reason != "" {
				return manage(reason), nil
			}
			if !current.Enabled {
				return manage(ReasonIntegrationDisabled), nil
			}
			projection.StateRevision = integrationStateDigest(entry, nil, current, nil, "")
			return CanonicalStepRead{
				Complete: true, AvailableActions: []ActionID{ActionManageIntegration},
				Integration: projection, Result: integrationResult(current),
			}, nil
		case installedSourceLocal:
			return manage(ReasonIntegrationLocalUnverified), nil
		case installedSourceUnrecognized:
			return manage(ReasonIntegrationIdentityMismatch), nil
		}
		if current.Format != entry.SourceFormat {
			return manage(ReasonIntegrationIdentityMismatch), nil
		}
	}
	if !entry.ReleaseReady || entry.FallbackSource() == "" {
		return manage(ReasonIntegrationReleaseNotReady), nil
	}
	if current == nil {
		return adapter.offerRelease(ctx, entry, projection, nil), nil
	}
	if source == installedSourceOfficialPinned && reviewedintegration.AtLeast(current.Version, entry.MinimumVersion) {
		// An exact official commit at or above the floor is verified against its
		// own version. The latest release is not consulted: a verified step needs
		// no network, never offers a downgrade, and stays silent about updates.
		if reason := validateInstalledRelease(entry, *current, adapter.platform); reason != "" {
			return manage(reason), nil
		}
		projection.ExpectedVersion = current.Version
		projection.Verified = true
		projection.StateRevision = integrationStateDigest(entry, nil, current, nil, "")
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
	// An older exact commit, or the official mutable URL at any version, is
	// eligible for replacement by the reviewed release, never acceptance. An
	// unrecognized version is never compared as if it were the floor.
	if _, comparable := reviewedintegration.CompareVersions(current.Version, entry.MinimumVersion); !comparable {
		return manage(ReasonIntegrationIdentityMismatch), nil
	}
	return adapter.offerRelease(ctx, entry, projection, current), nil
}

// offerRelease inspects the resolved releases, newest first, and offers the
// reviewed install — or the reviewed replacement of current — for the first one
// this build can actually load. The state digest binds the offer to that exact
// release, so a review goes stale if the choice changes before commit.
//
// The walk exists because the newest release is not always loadable: it can
// require a host feature, protocol, platform, or blueprint version this build
// does not have. Refusing the step in that case would strand a working setup
// behind a release the user never asked for, so an incompatible release is
// stepped over and the next one down is offered instead, ending at the floor
// this build was reviewed against.
//
// Only a compatibility refusal is stepped over. A failed inspection, a trust or
// identity mismatch, or a source that is not an exact official commit still
// blocks: those say something is wrong, and sliding quietly to an older release
// would hide it.
func (adapter *ReviewedIntegrationAdapter) offerRelease(ctx context.Context, entry reviewedintegration.Entry, projection *IntegrationProjection, current *plugin.InstalledPlugin) CanonicalStepRead {
	candidates := adapter.releases.Candidates(ctx, entry)
	if len(candidates) == 0 {
		candidates = []integrationrelease.Resolution{adapter.releases.Resolve(ctx, entry)}
	}
	var newestUnsupported string
	var lastUnsupported CanonicalStepRead
	for index, target := range candidates {
		if index >= integrationrelease.MaxCandidates {
			break
		}
		read, outcome := adapter.offerOneRelease(entry, projection, current, target, newestUnsupported)
		if outcome != releaseUnsupported {
			return read
		}
		if newestUnsupported == "" {
			newestUnsupported = target.Version
		}
		lastUnsupported = read
	}
	// Every candidate, the floor included, was refused as unloadable. The
	// floor's refusal is the honest answer: this build cannot run the
	// integration at all.
	return lastUnsupported
}

// releaseNeedsANewerHost reports whether an inspection was refused because the
// release asks for more than this build offers, rather than because something
// went wrong reading it.
func releaseNeedsANewerHost(err error) bool {
	return plugin.ContributionErrorIs(err, plugin.CodeHostFeatureUnsupported) ||
		plugin.ContributionErrorIs(err, plugin.CodeProtocolIncompatible)
}

// releaseOutcome classifies one candidate: whether the walk may continue.
type releaseOutcome int

const (
	// releaseSettled means the read is final, whether it offers or blocks.
	releaseSettled releaseOutcome = iota
	// releaseUnsupported means this build cannot load this release, and an
	// older one is worth trying.
	releaseUnsupported
)

func (adapter *ReviewedIntegrationAdapter) offerOneRelease(
	entry reviewedintegration.Entry, projection *IntegrationProjection,
	current *plugin.InstalledPlugin, target integrationrelease.Resolution, newestUnsupported string,
) (CanonicalStepRead, releaseOutcome) {
	result := integrationResult(current)
	// Each attempt starts from a clean projection: a field left over from a
	// candidate that was refused would describe the wrong release.
	projection.Trust = nil
	projection.reviewedSource = ""
	projection.ReplacementRequired = false
	projection.ReleaseNote = ""
	projection.NewestUnsupportedVersion = ""
	if !acceptedPinnedSource(entry, target.Source) || !reviewedintegration.AtLeast(target.Version, entry.MinimumVersion) {
		// A resolver must name an exact official commit at or above the floor.
		projection.ExpectedVersion = target.Version
		projection.StateRevision = integrationStateDigest(entry, nil, current, nil, "")
		return CanonicalStepRead{BlockedReason: ReasonOwnerUnavailable, Integration: projection, Result: result}, releaseSettled
	}
	projection.ExpectedVersion = target.Version
	projection.ReleaseChecked = !target.Fallback
	if newestUnsupported != "" {
		projection.ReleaseNote = ReasonIntegrationNewerReleaseUnsupported
		projection.NewestUnsupportedVersion = newestUnsupported
	}
	if current != nil {
		if order, _ := reviewedintegration.CompareVersions(current.Version, target.Version); order > 0 {
			// Never offer a downgrade. A newer unpinned install cannot be verified
			// either, so it needs managing until the latest release catches up.
			projection.StateRevision = integrationStateDigest(entry, &target, current, nil, newestUnsupported)
			return CanonicalStepRead{
				BlockedReason:    ReasonIntegrationIdentityMismatch,
				AvailableActions: []ActionID{ActionManageIntegration}, Integration: projection, Result: result,
			}, releaseSettled
		}
	}
	descriptor, report, inspectErr := adapter.manager.Inspect(target.Source, entry.SourceFormat)
	if inspectErr != nil {
		projection.StateRevision = integrationStateDigest(entry, &target, current, nil, newestUnsupported)
		// A release whose manifest names a host feature or protocol this build
		// does not have is refused while it is being read, before any
		// descriptor exists to validate. That is the same "cannot load this
		// release" answer as a failed contribution check, and it is the case
		// the newest reviewed release hits first after a plugin adopts a new
		// host feature. Everything else — an unreachable source, a bad clone,
		// a malformed manifest — is a real failure and still blocks.
		if releaseNeedsANewerHost(inspectErr) {
			return CanonicalStepRead{BlockedReason: ReasonIntegrationUnsupported, Integration: projection, Result: result}, releaseUnsupported
		}
		return CanonicalStepRead{BlockedReason: ReasonOwnerUnavailable, Integration: projection, Result: result}, releaseSettled
	}
	projection.Trust = cloneTrustReport(&report)
	projection.StateRevision = integrationStateDigest(entry, &target, current, &report, newestUnsupported)
	if reason := validateReviewedDescriptor(entry, target.Version, target.Source, descriptor, report, adapter.platform); reason != "" {
		outcome := releaseSettled
		if reason == ReasonIntegrationUnsupported {
			outcome = releaseUnsupported
		}
		return CanonicalStepRead{BlockedReason: reason, Integration: projection, Result: result}, outcome
	}
	projection.reviewedSource = target.Source
	if current == nil {
		return CanonicalStepRead{AvailableActions: []ActionID{ActionReviewInstall}, Integration: projection}, releaseSettled
	}
	projection.ReplacementRequired = true
	return CanonicalStepRead{
		AvailableActions: []ActionID{ActionReviewUpdate, ActionManageIntegration},
		Integration:      projection, Result: result,
	}, releaseSettled
}

func findInstalledIntegration(installed []plugin.InstalledPlugin, pluginID string) *plugin.InstalledPlugin {
	for index := range installed {
		if installed[index].Name == pluginID {
			copy := installed[index]
			return &copy
		}
	}
	return nil
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
	// complete trust material of the exact release that is installed.
	if state.Integration.Trust == nil {
		report, ok := adapter.installedReleaseTrust(scope, state.Integration)
		if !ok {
			return ActionReviewMaterial{}, ErrConflict
		}
		state.Integration.Trust = &report
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

// installedReleaseTrust inspects the installed plugin's own recorded exact
// commit, never the latest or fallback release, and validates it against the
// installed version the read verified.
func (adapter *ReviewedIntegrationAdapter) installedReleaseTrust(scope ReadScope, verified *IntegrationProjection) (plugin.TrustReport, bool) {
	entry, ok := adapter.resolve(scope.IntegrationKey)
	if !ok || entry.FallbackSource() == "" {
		return plugin.TrustReport{}, false
	}
	installed, err := adapter.manager.List()
	if err != nil {
		return plugin.TrustReport{}, false
	}
	current := findInstalledIntegration(installed, entry.PluginID)
	if current == nil || !acceptedPinnedSource(entry, current.Source) || current.Version != verified.InstalledVersion {
		return plugin.TrustReport{}, false
	}
	descriptor, report, inspectErr := adapter.manager.Inspect(current.Source, entry.SourceFormat)
	if inspectErr != nil || validateReviewedDescriptor(entry, current.Version, current.Source, descriptor, report, adapter.platform) != "" {
		return plugin.TrustReport{}, false
	}
	return report, true
}

func (adapter *ReviewedIntegrationAdapter) Commit(ctx context.Context, scope ReadScope, actionID ActionID, input json.RawMessage, reviewed ActionReviewMaterial) (CanonicalResult, error) {
	_ = ctx
	inputDigest, inputErr := emptyIntegrationInputDigest(input)
	if inputErr != nil || inputDigest != reviewed.InputDigest ||
		reviewed.CommitAction != actionID || reviewed.Integration == nil {
		return CanonicalResult{}, ErrInvalid
	}
	entry, ok := adapter.resolve(scope.IntegrationKey)
	if !ok || !entryMatchesScope(entry, scope) || entry.FallbackSource() == "" {
		return CanonicalResult{}, ErrConflict
	}
	confirm := func(report plugin.TrustReport) bool {
		return reviewed.Integration.Trust != nil &&
			trustReportDigest(report) == trustReportDigest(*reviewed.Integration.Trust)
	}
	// Install and replacement use exactly the release the reviewed material was
	// inspected from, never a fresh resolution.
	source := reviewed.Integration.reviewedSource
	var installed plugin.InstalledPlugin
	var err error
	switch actionID {
	case ActionInstall:
		if !acceptedPinnedSource(entry, source) {
			return CanonicalResult{}, ErrConflict
		}
		installed, err = adapter.manager.Install(source, entry.SourceFormat, confirm)
	case ActionEnable:
		err = adapter.manager.SetEnabled(entry.PluginID, true)
	case ActionUpdate:
		if !acceptedPinnedSource(entry, source) {
			return CanonicalResult{}, ErrConflict
		}
		installed, err = adapter.manager.UpdateFromSource(entry.PluginID, source, entry.SourceFormat, confirm)
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
		if current := findInstalledIntegration(plugins, entry.PluginID); current != nil {
			installed = *current
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
		return read.Complete && read.Integration != nil && read.Integration.Enabled && read.Integration.Verified
	default:
		return false
	}
}

func acceptedPinnedSourceForProjection(projection *IntegrationProjection) bool {
	// A reviewable same-version replacement is unblocked but not verified.
	// Only an installed exact official commit can settle a successful receipt;
	// release availability alone is not proof that replacement happened.
	return projection != nil && projection.ReleaseReady && projection.Verified
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

// integrationProjection starts from the floor. Reads that offer an install or
// replacement, or verify an installation, replace ExpectedVersion with the
// release they act on.
func integrationProjection(entry reviewedintegration.Entry) *IntegrationProjection {
	return &IntegrationProjection{
		Key: entry.Key, PluginID: entry.PluginID, Publisher: entry.PublisherLabel,
		SourceLabel: entry.SourceLabel, SourceURL: entry.SourceRepository,
		ExpectedVersion: entry.MinimumVersion, MinimumVersion: entry.MinimumVersion, ReleaseChecked: true,
		ReleaseReady: entry.ReleaseReady, ExpectedBlueprintID: entry.ExpectedBlueprintID,
		ExpectedProgramID:    entry.ExpectedProgramID,
		RequiredHostFeatures: append([]string(nil), entry.RequiredHostFeatures...),
		ExpectedProtocol:     entry.ExpectedProtocol,
		SupportedPlatforms:   append([]string(nil), entry.SupportedPlatforms...),
	}
}

func entryMatchesScope(entry reviewedintegration.Entry, scope ReadScope) bool {
	if entry.Key != scope.IntegrationKey {
		return false
	}
	if scope.QuestSource == QuestSourceUserTemplate {
		// The reviewed key owns software identity only. A user quest's bound local
		// template owns its blueprint/program target identity independently.
		return scope.UserTemplateID == scope.ExpectedBlueprintID
	}
	return entry.ExpectedBlueprintID == scope.ExpectedBlueprintID && entry.ExpectedProgramID == scope.ExpectedAssistantProgramID
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
	return entry.IsPinnedSource(source)
}

// validateReviewedDescriptor checks an inspected release against the entry and
// the exact version and source it was inspected as.
func validateReviewedDescriptor(entry reviewedintegration.Entry, version, source string, descriptor plugin.PluginDescriptor, report plugin.TrustReport, platform string) ReasonCode {
	if descriptor.Name != entry.PluginID || descriptor.Version != version ||
		descriptor.SourceLocation != source || descriptor.SourceFormat != entry.SourceFormat ||
		report.Name != entry.PluginID || report.Format != entry.SourceFormat ||
		trustReportDigest(report) != trustReportDigest(plugin.BuildTrustReport(descriptor)) {
		return ReasonIntegrationIdentityMismatch
	}
	if reason := validateContribution(entry, version, descriptor.WorkspaceSurfaces, descriptor.ResolvedBlueprints, platform); reason != "" {
		return reason
	}
	if len(report.Unsupported) != 0 {
		return ReasonIntegrationUnsupported
	}
	return ""
}

// validateInstalledRelease checks an installed record against the floor and
// its own version. Callers classify the recorded source first.
func validateInstalledRelease(entry reviewedintegration.Entry, installed plugin.InstalledPlugin, platform string) ReasonCode {
	if installed.Name != entry.PluginID || !reviewedintegration.AtLeast(installed.Version, entry.MinimumVersion) ||
		installed.Format != entry.SourceFormat || strings.TrimSpace(installed.ComponentFingerprint) == "" ||
		installed.Generation == 0 || installed.Generation > math.MaxInt64 {
		return ReasonIntegrationIdentityMismatch
	}
	return validateContribution(entry, installed.Version, installed.WorkspaceSurfaces, installed.ResolvedBlueprints, platform)
}

// validateContribution checks a release's surface contribution. The plugin
// version is exact for the release being checked; the blueprint version is a
// floor; program, protocol, host features and platform describe what this host
// can run.
func validateContribution(entry reviewedintegration.Entry, version string, contribution *plugin.SurfaceContribution, blueprints []plugin.ResolvedBlueprint, platform string) ReasonCode {
	if contribution == nil || contribution.Name != entry.PluginID || contribution.Version != version ||
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
		programID, programSchema := "", 0
		if blueprint.Template.AssistantProgram != nil {
			programID = blueprint.Template.AssistantProgram.ID
			programSchema = blueprint.Template.AssistantProgram.SchemaVersion
		} else if blueprint.Template.AssistantProject != nil {
			programID = blueprint.Template.AssistantProject.Home.ProgramID
			programSchema = blueprint.Template.AssistantProject.Home.HomeSchemaVersion
		}
		if blueprintFound || blueprint.Version < entry.MinimumBlueprintVersion ||
			programID != entry.ExpectedProgramID || programSchema != entry.ExpectedProgramSchema {
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

// integrationStateDigest binds a review to the state it disclosed. target is
// set only for reads that offer an install or replacement, so a review of one
// release goes stale if the latest release changes before commit. A verified
// read never depends on the latest release, so a background refresh cannot
// stale its enable review or churn a completed step.
func integrationStateDigest(entry reviewedintegration.Entry, target *integrationrelease.Resolution, installed *plugin.InstalledPlugin, report *plugin.TrustReport, newestUnsupported string) string {
	type state struct {
		RegistryRevision int
		EntryKey         string
		MinimumVersion   string
		FallbackCommit   string
		TargetVersion    string
		TargetCommit     string
		InstalledVersion string
		InstalledSource  string
		Fingerprint      string
		Generation       uint64
		Enabled          bool
		Trust            *plugin.TrustReport
		// NewestUnsupported is part of the state because it is part of what the
		// review disclosed. A newer release appearing that this build still
		// cannot load changes what the user is told, so the review goes stale
		// even though the offered release is unchanged.
		NewestUnsupported string
	}
	value := state{RegistryRevision: reviewedintegration.RegistryRevision, EntryKey: entry.Key,
		MinimumVersion: entry.MinimumVersion, FallbackCommit: entry.FallbackCommit, Trust: report,
		NewestUnsupported: newestUnsupported}
	if target != nil {
		value.TargetVersion = target.Version
		value.TargetCommit = target.Commit
	}
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

var _ CanonicalReader = (*ReviewedIntegrationAdapter)(nil)
