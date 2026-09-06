package setupjourney

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/specialist"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type fakeReviewedIntegrationManager struct {
	installed       []plugin.InstalledPlugin
	descriptor      plugin.PluginDescriptor
	report          plugin.TrustReport
	listErr         error
	inspectErr      error
	inspections     int
	installCalls    int
	confirmAccepted bool
}

func (manager *fakeReviewedIntegrationManager) List() ([]plugin.InstalledPlugin, error) {
	return append([]plugin.InstalledPlugin(nil), manager.installed...), manager.listErr
}
func (manager *fakeReviewedIntegrationManager) Inspect(string, plugin.SourceFormat) (plugin.PluginDescriptor, plugin.TrustReport, error) {
	manager.inspections++
	return manager.descriptor, manager.report, manager.inspectErr
}
func (manager *fakeReviewedIntegrationManager) Install(source string, format plugin.SourceFormat, confirm plugin.ConfirmFunc) (plugin.InstalledPlugin, error) {
	manager.installCalls++
	manager.confirmAccepted = confirm != nil && confirm(manager.report)
	if !manager.confirmAccepted {
		return plugin.InstalledPlugin{}, plugin.ErrInstallDeclined
	}
	installed := installedFromFixture(manager.descriptor, source, format, false, 1)
	manager.installed = []plugin.InstalledPlugin{installed}
	return installed, nil
}
func (manager *fakeReviewedIntegrationManager) SetEnabled(name string, enabled bool) error {
	for index := range manager.installed {
		if manager.installed[index].Name == name {
			manager.installed[index].Enabled = enabled
			return nil
		}
	}
	return errors.New("not installed")
}
func (manager *fakeReviewedIntegrationManager) UpdateFromSource(_ string, source string, format plugin.SourceFormat, confirm plugin.ConfirmFunc) (plugin.InstalledPlugin, error) {
	if confirm == nil || !confirm(manager.report) {
		return plugin.InstalledPlugin{}, plugin.ErrInstallDeclined
	}
	enabled := len(manager.installed) == 1 && manager.installed[0].Enabled
	installed := installedFromFixture(manager.descriptor, source, format, enabled, 2)
	manager.installed = []plugin.InstalledPlugin{installed}
	return installed, nil
}

func installedFromFixture(descriptor plugin.PluginDescriptor, source string, format plugin.SourceFormat, enabled bool, generation uint64) plugin.InstalledPlugin {
	return plugin.InstalledPlugin{
		Name: descriptor.Name, Version: descriptor.Version, Source: source, Format: format,
		WorkspaceSurfaces: descriptor.WorkspaceSurfaces, ResolvedBlueprints: descriptor.ResolvedBlueprints,
		ComponentFingerprint: "trusted", Generation: generation, Enabled: enabled,
		InstalledAt: time.Now().UTC(),
	}
}

func readyIntegrationFixture(t *testing.T) (reviewedintegration.Entry, plugin.PluginDescriptor, plugin.TrustReport, ReadScope) {
	t.Helper()
	entry := reviewedintegration.Entry{
		Key: "ori_reaper", PluginID: "reaper-plugin", ExpectedVersion: "0.5.0",
		SourceRepository: "https://github.com/example/reaper-plugin",
		SourceCommit:     strings.Repeat("a", 40), SourceFormat: plugin.FormatClaude,
		PublisherLabel: "Ori", SourceLabel: "example/reaper-plugin",
		ExpectedBlueprintID: "reaper-song", ExpectedBlueprintVersion: 4,
		ExpectedProgramID: "music-producer-assistant", ExpectedProgramSchema: 2,
		RequiredHostFeatures: []string{plugin.HostFeatureAssistantProgramV1, plugin.HostFeatureSpecialistSetupJourneyV1},
		ExpectedProtocol:     1, SupportedPlatforms: []string{"darwin/arm64"}, ReleaseReady: true,
	}
	descriptor := plugin.PluginDescriptor{
		Name: entry.PluginID, Version: entry.ExpectedVersion, SourceLocation: entry.Source(),
		SourceFormat: plugin.FormatClaude, InstallDir: t.TempDir(),
		WorkspaceSurfaces: &plugin.SurfaceContribution{
			Name: entry.PluginID, Version: entry.ExpectedVersion,
			Protocol:             plugin.ProtocolRange{Min: 1, Max: 1},
			RequiresHostFeatures: append([]string(nil), entry.RequiredHostFeatures...),
			Services: []plugin.ContributedService{{
				ID: "reaper-service", Artifacts: []plugin.ContributedArtifact{{
					ID: "service", OS: "darwin", Arch: "arm64", Size: 20,
					SHA256: strings.Repeat("b", 64),
					Source: plugin.ArtifactSource{Kind: "url", URL: "https://example.invalid/reaper-service"},
				}},
			}},
		},
		ResolvedBlueprints: []plugin.ResolvedBlueprint{{
			ID: entry.ExpectedBlueprintID, Version: entry.ExpectedBlueprintVersion,
			Template: projecttemplates.Template{AssistantProgram: &workspace.AssistantProgramDeclaration{
				ID: entry.ExpectedProgramID, SchemaVersion: entry.ExpectedProgramSchema,
			}},
		}},
	}
	report := plugin.BuildTrustReport(descriptor)
	scope := ReadScope{
		IntegrationKey: entry.Key, ExpectedBlueprintID: entry.ExpectedBlueprintID,
		ExpectedAssistantProgramID: entry.ExpectedProgramID,
	}
	return entry, descriptor, report, scope
}

func integrationResolver(entry reviewedintegration.Entry) IntegrationEntryResolver {
	return func(key string) (reviewedintegration.Entry, bool) {
		return entry.Clone(), key == entry.Key
	}
}

func TestReviewedIntegrationReadAbsentSurfacesExactReview(t *testing.T) {
	entry, descriptor, report, scope := readyIntegrationFixture(t)
	manager := &fakeReviewedIntegrationManager{descriptor: descriptor, report: report}
	adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
	read, err := adapter.Read(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if read.Complete || read.BlockedReason != "" || len(read.AvailableActions) != 1 ||
		read.AvailableActions[0] != ActionReviewInstall {
		t.Fatalf("unexpected absent read: %#v", read)
	}
	if read.Integration == nil || read.Integration.Trust == nil ||
		read.Integration.ExpectedVersion != "0.5.0" || read.Integration.StateRevision == "" {
		t.Fatalf("missing reviewed disclosure: %#v", read.Integration)
	}
}

func TestReviewedIntegrationReadInstalledSeparatesEnablement(t *testing.T) {
	entry, descriptor, report, scope := readyIntegrationFixture(t)
	installed := plugin.InstalledPlugin{
		Name: entry.PluginID, Version: entry.ExpectedVersion, Source: entry.Source(),
		Format: entry.SourceFormat, WorkspaceSurfaces: descriptor.WorkspaceSurfaces,
		ResolvedBlueprints: descriptor.ResolvedBlueprints, ComponentFingerprint: "trusted",
		Generation: 7, Enabled: false, InstalledAt: time.Now().UTC(),
	}
	manager := &fakeReviewedIntegrationManager{installed: []plugin.InstalledPlugin{installed}, descriptor: descriptor, report: report}
	adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
	read, err := adapter.Read(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if read.Complete || read.BlockedReason != "" || !containsAction(read.AvailableActions, ActionReviewEnable) {
		t.Fatalf("disabled installation was not independently actionable: %#v", read)
	}
	manager.installed[0].Enabled = true
	read, err = adapter.Read(context.Background(), scope)
	if err != nil || !read.Complete || !containsAction(read.AvailableActions, ActionManageIntegration) {
		t.Fatalf("enabled reviewed installation not complete: %#v err=%v", read, err)
	}
	if read.Result.IntegrationPluginID != entry.PluginID || read.Result.OwnerRevisions[0].Revision != 7 {
		t.Fatalf("canonical plugin receipt missing: %#v", read.Result)
	}
}

func TestReviewedIntegrationReadOffersPinnedUpdateForOlderAcceptedInstall(t *testing.T) {
	entry, descriptor, report, scope := readyIntegrationFixture(t)
	installed := plugin.InstalledPlugin{
		Name: entry.PluginID, Version: "0.4.1",
		Source: entry.SourceRepository + "#sha=" + strings.Repeat("c", 40),
		Format: entry.SourceFormat, Generation: 2, ComponentFingerprint: "older",
	}
	manager := &fakeReviewedIntegrationManager{installed: []plugin.InstalledPlugin{installed}, descriptor: descriptor, report: report}
	adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
	read, err := adapter.Read(context.Background(), scope)
	if err != nil || read.BlockedReason != "" || !containsAction(read.AvailableActions, ActionReviewUpdate) ||
		read.Integration == nil || read.Integration.Trust == nil {
		t.Fatalf("older accepted integration did not offer reviewed replacement: %#v err=%v", read, err)
	}
}

func TestReviewedIntegrationUpdateReviewIsNonMutatingAndCommitUsesPinnedReplacement(t *testing.T) {
	entry, descriptor, report, scope := readyIntegrationFixture(t)
	olderSource := entry.SourceRepository + "#sha=" + strings.Repeat("c", 40)
	manager := &fakeReviewedIntegrationManager{
		installed: []plugin.InstalledPlugin{{
			Name: entry.PluginID, Version: "0.4.1", Source: olderSource,
			Format: entry.SourceFormat, Generation: 1, ComponentFingerprint: "older", Enabled: true,
		}},
		descriptor: descriptor, report: report,
	}
	adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
	reviewed, err := adapter.Review(context.Background(), scope, ActionReviewUpdate, json.RawMessage(`{}`))
	if err != nil || reviewed.CommitAction != ActionUpdate || manager.installed[0].Source != olderSource {
		t.Fatalf("update review mutated canonical owner: %#v err=%v plugins=%#v", reviewed, err, manager.installed)
	}
	prepared, err := adapter.PrepareCommit(context.Background(), scope, ActionUpdate, json.RawMessage(`{}`))
	if err != nil || prepared.DisclosureDigest != reviewed.DisclosureDigest {
		t.Fatalf("update review changed before commit: %#v err=%v", prepared, err)
	}
	result, err := adapter.Commit(context.Background(), scope, ActionUpdate, json.RawMessage(`{}`), prepared)
	if err != nil || result.IntegrationVersion != entry.ExpectedVersion ||
		manager.installed[0].Source != entry.Source() || !manager.installed[0].Enabled {
		t.Fatalf("pinned update did not preserve enablement: %#v err=%v plugins=%#v", result, err, manager.installed)
	}
}

func TestReviewedIntegrationReadFailsClosedForIdentityAndContributionMismatch(t *testing.T) {
	entry, descriptor, report, scope := readyIntegrationFixture(t)
	t.Run("explicit development source can satisfy the prerequisite without claiming a release", func(t *testing.T) {
		developmentEntry := entry
		developmentEntry.ReleaseReady = false
		source := t.TempDir()
		manager := &fakeReviewedIntegrationManager{installed: []plugin.InstalledPlugin{
			installedFromFixture(descriptor, source, entry.SourceFormat, true, 1),
		}}
		adapter := newReviewedIntegrationAdapter(manager, integrationResolver(developmentEntry), "darwin/arm64")
		adapter.developmentSource = normalizedLocalDevelopmentSource(source)
		read, err := adapter.Read(context.Background(), scope)
		if err != nil || !read.Complete || read.BlockedReason != "" || manager.inspections != 0 {
			t.Fatalf("explicit development copy was not accepted: %#v err=%v", read, err)
		}
		if read.Integration == nil || !read.Integration.DevelopmentCopy || read.Integration.ReleaseReady {
			t.Fatalf("development provenance was not retained: %#v", read.Integration)
		}
		encoded, marshalErr := json.Marshal(read.Integration)
		if marshalErr != nil || strings.Contains(string(encoded), source) {
			t.Fatalf("development path leaked into projection: %s err=%v", encoded, marshalErr)
		}
	})
	t.Run("a different local development copy has specific guidance", func(t *testing.T) {
		manager := &fakeReviewedIntegrationManager{installed: []plugin.InstalledPlugin{{
			Name: entry.PluginID, Version: entry.ExpectedVersion,
			Source: t.TempDir(), Format: entry.SourceFormat, Generation: 1,
		}}}
		adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
		adapter.developmentSource = normalizedLocalDevelopmentSource(t.TempDir())
		read, err := adapter.Read(context.Background(), scope)
		if err != nil || read.BlockedReason != ReasonIntegrationLocalUnverified || manager.inspections != 0 {
			t.Fatalf("local copy did not receive bounded guidance: %#v err=%v", read, err)
		}
		if guidance := safeGuidance[read.BlockedReason]; guidance != "Local development copy installed; not release-verified." {
			t.Fatalf("local copy guidance = %q", guidance)
		}
	})
	t.Run("same name wrong source", func(t *testing.T) {
		manager := &fakeReviewedIntegrationManager{installed: []plugin.InstalledPlugin{{
			Name: entry.PluginID, Version: entry.ExpectedVersion,
			Source: "https://github.com/attacker/reaper-plugin#sha=" + strings.Repeat("d", 40),
			Format: entry.SourceFormat, Generation: 1,
		}}}
		read, err := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64").Read(context.Background(), scope)
		if err != nil || read.BlockedReason != ReasonIntegrationIdentityMismatch || manager.inspections != 0 {
			t.Fatalf("identity confusion was not rejected before preview: %#v err=%v", read, err)
		}
	})
	t.Run("candidate missing required host feature", func(t *testing.T) {
		oldDescriptor := descriptor
		contribution := *descriptor.WorkspaceSurfaces
		contribution.RequiresHostFeatures = []string{plugin.HostFeatureAssistantProgramV1}
		oldDescriptor.WorkspaceSurfaces = &contribution
		oldReport := plugin.BuildTrustReport(oldDescriptor)
		manager := &fakeReviewedIntegrationManager{descriptor: oldDescriptor, report: oldReport}
		read, err := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64").Read(context.Background(), scope)
		if err != nil || read.BlockedReason != ReasonIntegrationUnsupported {
			t.Fatalf("old plugin was accepted by new journey host: %#v err=%v", read, err)
		}
	})
	t.Run("candidate missing program", func(t *testing.T) {
		descriptor.ResolvedBlueprints[0].Template.AssistantProgram = nil
		report = plugin.BuildTrustReport(descriptor)
		manager := &fakeReviewedIntegrationManager{descriptor: descriptor, report: report}
		read, err := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64").Read(context.Background(), scope)
		if err != nil || read.BlockedReason != ReasonIntegrationUnsupported {
			t.Fatalf("invalid candidate program was not rejected: %#v err=%v", read, err)
		}
	})
}

func TestReviewedIntegrationUnsupportedPlatformDoesNotInspectOrInstall(t *testing.T) {
	entry, descriptor, report, scope := readyIntegrationFixture(t)
	manager := &fakeReviewedIntegrationManager{descriptor: descriptor, report: report}
	read, err := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "linux/amd64").Read(context.Background(), scope)
	if err != nil || read.BlockedReason != ReasonIntegrationUnsupported || manager.inspections != 0 || len(manager.installed) != 0 {
		t.Fatalf("unsupported platform was not blocked before preview: %#v err=%v", read, err)
	}
}

func TestReviewedIntegrationPendingReleaseNeverResolvesMutableSource(t *testing.T) {
	entry, descriptor, report, scope := readyIntegrationFixture(t)
	entry.ReleaseReady = false
	entry.SourceCommit = ""
	manager := &fakeReviewedIntegrationManager{descriptor: descriptor, report: report}
	read, err := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64").Read(context.Background(), scope)
	if err != nil || read.BlockedReason != ReasonOwnerUnavailable || manager.inspections != 0 ||
		read.Integration == nil || read.Integration.ReleaseReady {
		t.Fatalf("pending release was not inert: %#v err=%v inspections=%d", read, err, manager.inspections)
	}
}

func TestReviewedIntegrationReviewInstallEnableAndReplayThroughService(t *testing.T) {
	entry, descriptor, report, _ := readyIntegrationFixture(t)
	manager := &fakeReviewedIntegrationManager{descriptor: descriptor, report: report}
	adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
	readers := make(map[specialist.SetupStepKind]CanonicalReader, len(actionDefinitionsByKind))
	for kind := range actionDefinitionsByKind {
		if kind == specialist.SetupStepIntegrationInstall {
			readers[kind] = adapter
			continue
		}
		readers[kind] = CanonicalReaderFunc(func(context.Context, ReadScope) (CanonicalStepRead, error) {
			return CanonicalStepRead{}, nil
		})
	}
	registry, err := NewReaderRegistry(readers)
	if err != nil {
		t.Fatal(err)
	}
	_, store := openTestStore(t)
	service, err := NewService(store, &relationshipStub{state: acceptedRelationship()}, registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetActionAdapter(specialist.SetupStepIntegrationInstall, adapter); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	journey, err := service.Read(ctx, "local", "")
	if err != nil || !projectionHasAction(journey, ActionReviewInstall) {
		t.Fatalf("initial journey: %#v err=%v", journey, err)
	}
	review, err := service.Mutate(ctx, "local", journey.RunID, ActionReviewInstall, ActionMutation{
		IfRevision: journey.StateRevision, IdempotencyKey: "review-install-1", Input: json.RawMessage(`{}`),
	})
	if err != nil || review.Review == nil || review.Review.CommitAction != ActionInstall ||
		review.Review.Integration == nil || review.Review.Integration.Trust == nil || len(manager.installed) != 0 {
		t.Fatalf("install review mutated or omitted disclosure: %#v err=%v installed=%d", review, err, len(manager.installed))
	}
	commitRequest := ActionMutation{
		IfRevision: journey.StateRevision, IdempotencyKey: "commit-install-1",
		ReviewToken: review.Review.Token, Input: json.RawMessage(`{}`),
	}
	installedResult, err := service.Mutate(ctx, "local", journey.RunID, ActionInstall, commitRequest)
	if err != nil || len(manager.installed) != 1 || manager.installed[0].Enabled ||
		!projectionHasAction(installedResult.Journey, ActionReviewEnable) {
		t.Fatalf("reviewed install did not remain disabled: %#v err=%v plugins=%#v install_calls=%d confirmed=%v", installedResult, err, manager.installed, manager.installCalls, manager.confirmAccepted)
	}
	// An exact retry uses the terminal receipt even though the run revision has
	// advanced past the original If-Match value.
	replayed, err := service.Mutate(ctx, "local", journey.RunID, ActionInstall, commitRequest)
	if err != nil || replayed.Journey.StateRevision != installedResult.Journey.StateRevision || len(manager.installed) != 1 {
		t.Fatalf("exact install replay was not stable: %#v err=%v", replayed, err)
	}

	enableReview, err := service.Mutate(ctx, "local", journey.RunID, ActionReviewEnable, ActionMutation{
		IfRevision: installedResult.Journey.StateRevision, IdempotencyKey: "review-enable-1", Input: json.RawMessage(`{}`),
	})
	if err != nil || enableReview.Review == nil {
		t.Fatalf("enable review: %#v err=%v", enableReview, err)
	}
	enabledResult, err := service.Mutate(ctx, "local", journey.RunID, ActionEnable, ActionMutation{
		IfRevision: installedResult.Journey.StateRevision, IdempotencyKey: "commit-enable-1",
		ReviewToken: enableReview.Review.Token, Input: json.RawMessage(`{}`),
	})
	if err != nil || !manager.installed[0].Enabled || enabledResult.Journey.Steps[0].Status != StepComplete {
		t.Fatalf("reviewed enable did not complete integration: %#v err=%v", enabledResult, err)
	}
}

func TestReviewedIntegrationCommitRejectsStaleOwnerReviewWithoutClaim(t *testing.T) {
	entry, descriptor, report, _ := readyIntegrationFixture(t)
	installed := installedFromFixture(descriptor, entry.Source(), entry.SourceFormat, false, 3)
	manager := &fakeReviewedIntegrationManager{installed: []plugin.InstalledPlugin{installed}, descriptor: descriptor, report: report}
	adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
	readers := make(map[specialist.SetupStepKind]CanonicalReader, len(actionDefinitionsByKind))
	for kind := range actionDefinitionsByKind {
		if kind == specialist.SetupStepIntegrationInstall {
			readers[kind] = adapter
		} else {
			readers[kind] = CanonicalReaderFunc(func(context.Context, ReadScope) (CanonicalStepRead, error) { return CanonicalStepRead{}, nil })
		}
	}
	registry, _ := NewReaderRegistry(readers)
	_, store := openTestStore(t)
	service, _ := NewService(store, &relationshipStub{state: acceptedRelationship()}, registry)
	if err := service.SetActionAdapter(specialist.SetupStepIntegrationInstall, adapter); err != nil {
		t.Fatal(err)
	}
	journey, _ := service.Read(context.Background(), "local", "")
	review, err := service.Mutate(context.Background(), "local", journey.RunID, ActionReviewEnable, ActionMutation{
		IfRevision: journey.StateRevision, IdempotencyKey: "review-stale-owner", Input: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.installed[0].Generation++
	_, err = service.Mutate(context.Background(), "local", journey.RunID, ActionEnable, ActionMutation{
		IfRevision: journey.StateRevision, IdempotencyKey: "commit-stale-owner",
		ReviewToken: review.Review.Token, Input: json.RawMessage(`{}`),
	})
	var public *Failure
	if !errors.As(err, &public) || public.ReasonCode != ReasonReviewStale || manager.installed[0].Enabled {
		t.Fatalf("stale owner review was not rejected: failure=%#v enabled=%v", public, manager.installed[0].Enabled)
	}
	if _, receiptErr := store.GetOperationReceipt(context.Background(), RunKindRoot, journey.RunID, "commit-stale-owner"); !errors.Is(receiptErr, ErrNotFound) {
		t.Fatalf("stale review created operation claim: %v", receiptErr)
	}
}

func containsAction(actions []ActionID, expected ActionID) bool {
	for _, action := range actions {
		if action == expected {
			return true
		}
	}
	return false
}
