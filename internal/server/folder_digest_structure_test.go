package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
)

// warnings records what a host would have logged.
type warnings struct{ lines []logger.Fields }

func (w *warnings) record(_ string, fields logger.Fields) { w.lines = append(w.lines, fields) }

func TestFolderSetupPlan_AnUnverifiableHomeProviderSaysSoAndIsLoggedOnce(t *testing.T) {
	logged := &warnings{}
	provider, ok := reviewedintegration.HomeProviderFor("music-project-management")
	if !ok {
		t.Fatal("the Music Home provider is not reviewed")
	}
	// A builder with no release reader is what a provider that cannot be
	// verified looks like to the preview.
	host := &folderSetupHost{builder: &ServerBuilder{}, warn: logged.record}
	for range 4 {
		_, err := host.providerFacts(t.Context(), provider)
		var reasoned interface {
			SetupPreviewReason() string
			SetupAvailability() string
		}
		if !errors.Is(err, errSetupUnavailable) || !errors.As(err, &reasoned) ||
			reasoned.SetupPreviewReason() != "home_provider_unavailable" ||
			reasoned.SetupAvailability() != personalassistant.FolderAvailabilityProviderUnverified {
			t.Fatalf("provider facts error: %v", err)
		}
		host.notePlan("local", err)
	}
	if len(logged.lines) != 1 {
		t.Fatalf("an unverifiable provider was logged %d times", len(logged.lines))
	}
	line := logged.lines[0]
	if line["reason"] != personalassistant.FolderAvailabilityProviderUnverified || line["provider_or_integration_key"] != provider.Key ||
		line["user_id"] != "local" || line["detail"] != "the reviewed release reader is not available" {
		t.Fatalf("log line: %+v", line)
	}
	for _, value := range line {
		if text := fmt.Sprint(value); strings.ContainsAny(text, "/\\") {
			t.Fatalf("the log line carries a path: %q", text)
		}
	}
	// Another owner is another line.
	_, err := host.providerFacts(t.Context(), provider)
	host.notePlan("someone-else", err)
	if len(logged.lines) != 2 {
		t.Fatalf("%d lines for two owners", len(logged.lines))
	}
}

func TestFolderStructureAvailability_AHomeThatCannotBeReadIsUnavailableWithoutALookup(t *testing.T) {
	logged := &warnings{}
	// No workspace store: the owner's Home cannot be read, and nothing is asked
	// of a release. An unknown provider is not read at all.
	host := &folderSetupHost{builder: &ServerBuilder{}, warn: logged.record}
	for _, key := range []string{"music_project_management", "no_such_provider"} {
		if got := host.HomeAvailability(t.Context(), "local", key); got != personalassistant.FolderAvailabilityPlanUnavailable {
			t.Fatalf("%s availability = %q", key, got)
		}
	}
	if len(logged.lines) != 0 {
		t.Fatalf("an unreadable Home was logged as a plan failure: %+v", logged.lines)
	}
}

func TestFolderStructureAvailability_AnIntegrationIsUsableOnlyWhenNothingNeedsInstalling(t *testing.T) {
	entry, ok := reviewedintegration.Get("ori_reaper")
	if !ok {
		t.Fatal("missing registry fixture")
	}
	for name, test := range map[string]struct {
		read    setupjourney.CanonicalStepRead
		readErr error
		want    string
		logged  int
	}{
		"installed and ready": {
			read: setupjourney.CanonicalStepRead{Complete: true, Integration: &setupjourney.IntegrationProjection{ExpectedVersion: "0.9.0", InstalledVersion: "0.9.0"}},
		},
		"can be installed": {
			read: setupjourney.CanonicalStepRead{AvailableActions: []setupjourney.ActionID{setupjourney.ActionReviewInstall}, Integration: &setupjourney.IntegrationProjection{ExpectedVersion: "0.9.0"}},
			want: personalassistant.FolderAvailabilityIntegrationNotInstalled,
		},
		"installed but switched off": {
			read: setupjourney.CanonicalStepRead{AvailableActions: []setupjourney.ActionID{setupjourney.ActionReviewEnable}, Integration: &setupjourney.IntegrationProjection{ExpectedVersion: "0.9.0", InstalledVersion: "0.9.0"}},
			want: personalassistant.FolderAvailabilityPlanUnavailable,
		},
		"not installed and no reviewed install": {
			read: setupjourney.CanonicalStepRead{Integration: &setupjourney.IntegrationProjection{ExpectedVersion: "0.9.0"}},
			want: personalassistant.FolderAvailabilityIntegrationNotInstalled, logged: 1,
		},
		"unreadable": {
			readErr: errors.New("reader failed"),
			want:    personalassistant.FolderAvailabilityPlanUnavailable, logged: 1,
		},
	} {
		t.Run(name, func(t *testing.T) {
			logged := &warnings{}
			reads := 0
			reader := setupjourney.CanonicalReaderFunc(func(context.Context, setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
				reads++
				return test.read, test.readErr
			})
			host := &folderSetupHost{builder: &ServerBuilder{reviewedIntegrationReader: reader}, warn: logged.record}
			for range 3 {
				if got := host.IntegrationAvailability(t.Context(), "local", entry.Key); got != test.want {
					t.Fatalf("availability = %q, want %q", got, test.want)
				}
			}
			// Described on every turn, read once per short window.
			if reads != 1 || len(logged.lines) != test.logged {
				t.Fatalf("%d reads, %d log lines", reads, len(logged.lines))
			}
			if test.logged == 1 && (logged.lines[0]["provider_or_integration_key"] != entry.Key || logged.lines[0]["reason"] != test.want) {
				t.Fatalf("log line: %+v", logged.lines[0])
			}
		})
	}
}

func TestFolderSetupPlan_AFailedPreviewCarriesItsReason(t *testing.T) {
	logged := &warnings{}
	reader := setupjourney.CanonicalReaderFunc(func(context.Context, setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
		return setupjourney.CanonicalStepRead{Integration: &setupjourney.IntegrationProjection{ExpectedVersion: "0.9.0"}}, nil
	})
	host := &folderSetupHost{builder: &ServerBuilder{reviewedIntegrationReader: reader}, warn: logged.record}
	offer := personalassistant.FolderOffer{
		Verdict: "project",
		Subject: personalassistant.FolderCandidateRecord{Name: "Album-1", Kind: personalassistant.FolderChoiceProject, Shape: "audio", MarkerName: "*.rpp"},
	}
	for range 3 {
		_, err := host.Plan(t.Context(), personalassistant.FolderSetupRequest{UserID: "local", Offer: offer})
		var reasoned interface {
			SetupPreviewReason() string
			SetupAvailability() string
		}
		if !errors.Is(err, errSetupUnavailable) || !errors.As(err, &reasoned) ||
			reasoned.SetupPreviewReason() != "integration_unavailable" ||
			reasoned.SetupAvailability() != personalassistant.FolderAvailabilityIntegrationNotInstalled {
			t.Fatalf("plan error: %v", err)
		}
	}
	if len(logged.lines) != 1 || logged.lines[0]["provider_or_integration_key"] != "ori_reaper" {
		t.Fatalf("a failed plan was logged %d times: %+v", len(logged.lines), logged.lines)
	}
}
