package setupjourney

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/specialist"
)

func TestReviewedIntegrationOfficialSourcesRequireReviewedReplacement(t *testing.T) {
	entry, descriptor, report, scope := readyIntegrationFixture(t)
	for _, source := range []string{
		entry.SourceRepository,
		entry.SourceRepository + ".git",
		entry.SourceRepository + "#sha=" + strings.Repeat("b", 40),
	} {
		for _, enabled := range []bool{false, true} {
			t.Run(source+"/enabled="+boolString(enabled), func(t *testing.T) {
				manager := &fakeReviewedIntegrationManager{
					installed:  []plugin.InstalledPlugin{installedFromFixture(descriptor, source, entry.SourceFormat, enabled, 1)},
					descriptor: descriptor, report: report,
				}
				adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
				read, err := adapter.Read(context.Background(), scope)
				if err != nil || read.Complete || read.BlockedReason != "" || read.Integration.Verified ||
					!read.Integration.ReplacementRequired || !containsAction(read.AvailableActions, ActionReviewUpdate) {
					t.Fatalf("same-version installation bypassed replacement review: %#v err=%v", read, err)
				}
				if adapter.ConsequenceObserved(ActionUpdate, read) || adapter.ConsequenceObserved(ActionInstall, read) {
					t.Fatal("same version at an unreviewed source counted as an observed installation")
				}
				review, err := adapter.Review(context.Background(), scope, ActionReviewUpdate, json.RawMessage(`{}`))
				if err != nil || review.Integration.Trust == nil || manager.updateCalls != 0 || manager.installed[0].Source != source {
					t.Fatalf("review changed installation or omitted disclosure: %#v err=%v", review, err)
				}
				for _, inspected := range manager.inspectSources {
					if inspected != entry.Source() {
						t.Fatalf("inspected mutable installed source instead of host pin: %q", inspected)
					}
				}
				if _, err := adapter.Commit(context.Background(), scope, ActionUpdate, json.RawMessage(`{}`), review); err != nil {
					t.Fatal(err)
				}
				after, err := adapter.Read(context.Background(), scope)
				if err != nil || after.Complete != enabled || !after.Integration.Verified || after.Integration.ReplacementRequired ||
					manager.installed[0].Source != entry.Source() || manager.installed[0].Enabled != enabled || manager.updateCalls != 1 {
					t.Fatalf("replacement did not preserve enablement and verify exact pin: %#v err=%v", after, err)
				}
				if !enabled && !containsAction(after.AvailableActions, ActionReviewEnable) {
					t.Fatal("disabled replacement lost its separate enable review")
				}
			})
		}
	}
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func TestReviewedIntegrationReplacementRefusesUntrustedSourcesAndDowngrades(t *testing.T) {
	entry, descriptor, report, scope := readyIntegrationFixture(t)
	cases := []struct {
		name, source, version string
		format                plugin.SourceFormat
	}{
		{"lookalike repository", entry.SourceRepository + "-other.git", entry.ExpectedVersion, entry.SourceFormat},
		{"other publisher", "https://github.com/attacker/reaper-plugin.git", entry.ExpectedVersion, entry.SourceFormat},
		{"URL credentials", "https://github.com@attacker.invalid/example/reaper-plugin.git", entry.ExpectedVersion, entry.SourceFormat},
		{"mutable ref", entry.SourceRepository + "#ref=main", entry.ExpectedVersion, entry.SourceFormat},
		{"query", entry.SourceRepository + "?sha=" + strings.Repeat("a", 40), entry.ExpectedVersion, entry.SourceFormat},
		{"subdirectory", entry.SourceRepository + "/nested", entry.ExpectedVersion, entry.SourceFormat},
		{"wrong format", entry.SourceRepository + ".git", entry.ExpectedVersion, plugin.FormatCodex},
		{"newer unpinned", entry.SourceRepository + ".git", "0.6.0", entry.SourceFormat},
		{"unknown version", entry.SourceRepository + ".git", "unknown", entry.SourceFormat},
		{"newer pin", entry.SourceRepository + "#sha=" + strings.Repeat("b", 40), "0.6.0", entry.SourceFormat},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			installed := installedFromFixture(descriptor, item.source, item.format, true, 1)
			installed.Version = item.version
			manager := &fakeReviewedIntegrationManager{installed: []plugin.InstalledPlugin{installed}, descriptor: descriptor, report: report}
			adapter := newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64")
			read, err := adapter.Read(context.Background(), scope)
			if err != nil || read.Complete || read.BlockedReason != ReasonIntegrationIdentityMismatch ||
				containsAction(read.AvailableActions, ActionReviewUpdate) || manager.inspections != 0 {
				t.Fatalf("untrusted source/downgrade became reviewable: %#v err=%v", read, err)
			}
		})
	}
}

func TestReviewedIntegrationReplacementStillRequiresAvailableCompatibleCandidate(t *testing.T) {
	entry, descriptor, report, scope := readyIntegrationFixture(t)
	installed := installedFromFixture(descriptor, entry.SourceRepository+".git", entry.SourceFormat, true, 1)
	for _, unavailable := range []bool{false, true} {
		t.Run("unpublished="+boolString(unavailable), func(t *testing.T) {
			candidate := entry.Clone()
			manager := &fakeReviewedIntegrationManager{installed: []plugin.InstalledPlugin{installed}, descriptor: descriptor, report: report}
			want := ReasonIntegrationUnsupported
			if unavailable {
				candidate.ReleaseReady = false
				want = ReasonIntegrationReleaseNotReady
			} else {
				manager.descriptor.WorkspaceSurfaces = nil
				manager.report = plugin.BuildTrustReport(manager.descriptor)
			}
			adapter := newReviewedIntegrationAdapter(manager, integrationResolver(candidate), "darwin/arm64")
			read, err := adapter.Read(context.Background(), scope)
			if err != nil || read.Complete || read.BlockedReason != want || containsAction(read.AvailableActions, ActionReviewUpdate) {
				t.Fatalf("unavailable candidate became actionable: %#v err=%v", read, err)
			}
			if unavailable && manager.inspections != 0 {
				t.Fatal("unpublished candidate triggered inspection")
			}
		})
	}
}

func TestReviewedIntegrationFailedReplacementCannotSettleFromSameVersion(t *testing.T) {
	entry, descriptor, report, _ := readyIntegrationFixture(t)
	source := entry.SourceRepository + ".git"
	manager := &fakeReviewedIntegrationManager{
		installed:  []plugin.InstalledPlugin{installedFromFixture(descriptor, source, entry.SourceFormat, true, 1)},
		descriptor: descriptor, report: report, updateErr: errors.New("download failed"),
	}
	service := integrationServiceForReplacementTest(t, newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64"))
	ctx := context.Background()
	journey, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	review, err := service.Mutate(ctx, "local", journey.RunID, ActionReviewUpdate, ActionMutation{
		IfRevision: journey.StateRevision, IdempotencyKey: "failed-replacement-review", Input: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := ActionMutation{
		IfRevision: journey.StateRevision, IdempotencyKey: "failed-replacement",
		ReviewToken: review.Review.Token, Input: json.RawMessage(`{}`),
	}
	for attempt := 0; attempt < 2; attempt++ {
		_, err := service.Mutate(ctx, "local", journey.RunID, ActionUpdate, request)
		var failure *Failure
		if !errors.As(err, &failure) || failure.ReasonCode != ReasonOperationFailed {
			t.Fatalf("failed same-version replacement was reported successful on attempt %d: %v", attempt, err)
		}
	}
	if manager.updateCalls != 1 || manager.installed[0].Source != source {
		t.Fatal("failed replacement retry re-executed or mutated the installed source")
	}
}

func integrationServiceForReplacementTest(t *testing.T, adapter *ReviewedIntegrationAdapter) *Service {
	t.Helper()
	readers := make(map[specialist.SetupStepKind]CanonicalReader, len(actionDefinitionsByKind))
	for kind := range actionDefinitionsByKind {
		readers[kind] = CanonicalReaderFunc(func(context.Context, ReadScope) (CanonicalStepRead, error) { return CanonicalStepRead{}, nil })
	}
	readers[specialist.SetupStepIntegrationInstall] = adapter
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
	return service
}

func TestReviewedIntegrationReplacementConsentAndReplay(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run("stale="+boolString(stale), func(t *testing.T) {
			entry, descriptor, report, _ := readyIntegrationFixture(t)
			source := entry.SourceRepository + ".git"
			manager := &fakeReviewedIntegrationManager{
				installed:  []plugin.InstalledPlugin{installedFromFixture(descriptor, source, entry.SourceFormat, true, 1)},
				descriptor: descriptor, report: report,
			}
			service := integrationServiceForReplacementTest(t, newReviewedIntegrationAdapter(manager, integrationResolver(entry), "darwin/arm64"))
			ctx := context.Background()
			journey, err := service.Read(ctx, "local", "")
			if err != nil {
				t.Fatal(err)
			}
			// Neither a read nor an unreviewed commit may replace the installation.
			_, err = service.Mutate(ctx, "local", journey.RunID, ActionUpdate, ActionMutation{
				IfRevision: journey.StateRevision, IdempotencyKey: "unreviewed", Input: json.RawMessage(`{}`),
			})
			if err == nil || manager.updateCalls != 0 {
				t.Fatal("unreviewed replacement was accepted")
			}
			review, err := service.Mutate(ctx, "local", journey.RunID, ActionReviewUpdate, ActionMutation{
				IfRevision: journey.StateRevision, IdempotencyKey: "replacement-review", Input: json.RawMessage(`{}`),
			})
			if err != nil || review.Review == nil || !review.Review.Integration.ReplacementRequired || manager.updateCalls != 0 {
				t.Fatalf("replacement review: %#v err=%v", review, err)
			}
			request := ActionMutation{
				IfRevision: journey.StateRevision, IdempotencyKey: "replacement-commit", ReviewToken: review.Review.Token, Input: json.RawMessage(`{}`),
			}
			if stale {
				manager.installed[0].ComponentFingerprint = "changed-after-review"
			}
			result, err := service.Mutate(ctx, "local", journey.RunID, ActionUpdate, request)
			if stale {
				var failure *Failure
				if !errors.As(err, &failure) || failure.ReasonCode != ReasonReviewStale || manager.updateCalls != 0 {
					t.Fatalf("stale replacement consent was accepted: %#v err=%v", result, err)
				}
				return
			}
			if err != nil || result.Journey.Steps[0].Status != StepComplete || result.Journey.CurrentStepID == "integration" {
				t.Fatalf("replacement did not advance setup: %#v err=%v", result, err)
			}
			if _, err := service.Mutate(ctx, "local", journey.RunID, ActionUpdate, request); err != nil || manager.updateCalls != 1 {
				t.Fatalf("lost-response replay repeated replacement: calls=%d err=%v", manager.updateCalls, err)
			}
		})
	}
}
