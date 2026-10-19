//go:build darwin || linux

package backuprecovery

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// timeMachineServiceFixture connects a Time Machine configuration for the
// fixture workspace through the real Review and Connect paths, with an
// invented reader that answers from the checked-in fixtures.
func timeMachineServiceFixture(t *testing.T, sources []string, sticky []string, denied bool) (*Service, *serviceFixtureProvider) {
	t.Helper()
	s, restic, credentials := newBackupServiceFixture(t)
	reader := &serviceFixtureProvider{}
	reader.readFn = func(ctx context.Context, cfg localConfiguration, use backupPasswordUse) (providerReadResult, error) {
		if use(ctx, func([]byte) error { return nil }) == nil {
			t.Error("the Time Machine reader was handed a usable credential")
		}
		helper := timeMachineHelperResult{DestinationInfo: timeMachineFixture(t, "invented_destinationinfo.plist"), Status: timeMachineFixture(t, "status_idle.plist"), Sticky: sticky}
		if denied {
			helper.PreferencesDenied = true
		} else {
			helper.Preferences = timeMachineFixture(t, "invented_preferences.bplist")
		}
		return timeMachineReadResult(cfg, helper, time.Now().UTC())
	}
	s.timeMachine = reader
	status, err := s.GetStatus(t.Context(), "local", localFixtureWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	tokens := []string{}
	for _, source := range sources {
		token, err := s.selections.IssueFor(source, selectionScope("local", localFixtureWorkspace, "source"))
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, token)
	}
	review, err := s.Review(t.Context(), "local", localFixtureWorkspace, ReviewRequest{Generation: status.Generation, Revision: status.Revision, Provider: providerTimeMachine, SourceSelections: tokens})
	if err != nil {
		t.Fatal("review", err)
	}
	if review.Provider != providerTimeMachine || review.VolumePath != "" || review.RepositoryPath != "" || review.Credential != (CredentialReference{}) {
		t.Fatal(review)
	}
	result, err := s.Connect(t.Context(), "local", localFixtureWorkspace, ConnectRequest{Generation: review.Generation, Revision: review.Revision, OperationID: uuid.NewString(), ReviewToken: review.Token, AllowMetadataRead: true})
	if err != nil || result.Outcome != "read_ok" {
		t.Fatal("connect", result, err)
	}
	if restic.calls.Load() != 0 || credentials.describes.Load() != 0 || credentials.uses.Load() != 0 {
		t.Fatal("a Time Machine connect touched restic or the Vault")
	}
	return s, reader
}

func TestTimeMachineConnectIsOneCredentialFreeReadWithCoverageAndHealth(t *testing.T) {
	s, reader := timeMachineServiceFixture(t, []string{"/Invented/Users/demo/Music/Songs", "/Invented/Users/demo/Music/Scratch/Take"}, []string{timeMachineIncluded, timeMachineIncluded}, false)
	if reader.calls.Load() != 1 {
		t.Fatal("connect was not exactly one read", reader.calls.Load())
	}
	status, err := s.GetStatus(t.Context(), "local", localFixtureWorkspace)
	if err != nil || status.Connection != "configured" || status.Provider != providerTimeMachine || !status.EvidenceCurrent || status.Destination != "present" {
		t.Fatal(status, err)
	}
	if status.SnapshotCount == nil || *status.SnapshotCount != 3 || status.LastCompletedAt == nil || status.Capacity == nil || status.Coverage != nil {
		t.Fatal("completed backups, capacity or declared coverage mapped wrongly", status)
	}
	if tm := status.TimeMachine; tm == nil || tm.PermissionRequired || tm.Destinations != 2 || tm.MountedDestinations != 1 || tm.ExcludedSources != 1 {
		t.Fatal(status.TimeMachine)
	}
	if !strings.Contains(strings.Join(status.Health.Problems, ","), "source_excluded") || strings.Contains(strings.Join(status.Health.Problems, ","), "permission_required") {
		t.Fatal(status.Health)
	}
	// The read was a week after the invented backups: overdue, never "verified".
	encoded, _ := json.Marshal(status)
	if strings.Contains(string(encoded), "verified") || strings.Contains(string(encoded), "/Invented") || strings.Contains(string(encoded), "UNTRUSTED") {
		t.Fatal("status carried a path, a label or a verification claim", string(encoded))
	}

	ids := []string{localFixtureWorkspace, "song", "take", "sibling"}
	read, err := s.ReadFolderCoverage(t.Context(), "local", ids, []AdoptedFolder{
		{WorkspaceID: "song", Path: "/Invented/Users/demo/Music/Songs/Night Drive"},
		{WorkspaceID: "take", Path: "/Invented/Users/demo/Music/Scratch/Take"},
		{WorkspaceID: "sibling", Path: "/Invented/Users/demo/Music/Songs Archive"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rows := coverageByWorkspace(t, read)
	// Observed more than the expected age after the latest backup the health is
	// overdue, but coverage only needs current evidence with a completion.
	if song := rows["song"]; song.State != CoverageCovered || song.Provider != providerTimeMachine || song.LastCompletedAt == nil {
		t.Fatal("an included folder inside a Time Machine source is not covered", song)
	}
	if take := rows["take"]; take.State != CoverageNotCovered || take.Reason != "source_excluded" {
		t.Fatal("an excluded folder read as covered", take)
	}
	if sibling := rows["sibling"]; sibling.State != CoverageNotCovered || sibling.Reason != "not_in_any_source" {
		t.Fatal(sibling)
	}
	summary, err := s.ReadBackupSummaries(t.Context(), "local", []string{localFixtureWorkspace})
	if err != nil || summary.Sources[0].Provider != providerTimeMachine || strings.Join(summary.Sources[0].SourceCoverage, ",") != "included,source_excluded" {
		t.Fatal("the summary lost the provider or per-source coverage", summary, err)
	}
	advisor, err := s.ReadBackupAdvisor(t.Context(), "local", localFixtureWorkspace)
	if err != nil || advisor.Provider != providerTimeMachine || len(advisor.SourceCoverage) != 2 {
		t.Fatal(advisor, err)
	}
	settings, err := s.GetSettings(t.Context(), "local", localFixtureWorkspace)
	if err != nil || settings.Provider != providerTimeMachine || settings.Credential != nil || settings.VolumePath != "" || len(settings.Providers) != 2 || settings.Providers[0].ID != providerTimeMachine || !settings.Providers[0].Available {
		t.Fatal(settings, err)
	}
}

func TestTimeMachineWithoutFullDiskAccessIsPermissionRequiredNeverCovered(t *testing.T) {
	s, _ := timeMachineServiceFixture(t, []string{"/Invented/Users/demo/Music/Songs"}, []string{timeMachineIncluded}, true)
	status, err := s.GetStatus(t.Context(), "local", localFixtureWorkspace)
	if err != nil || status.Connection != "configured" || status.SnapshotCount != nil || status.LastCompletedAt != nil || status.Capacity != nil {
		t.Fatal("degraded mode reported completions or capacity", status, err)
	}
	problems := strings.Join(status.Health.Problems, ",")
	if !strings.Contains(problems, "permission_required") || strings.Contains(problems, "not_configured") || strings.Contains(problems, "no_completed_snapshot") {
		t.Fatal(status.Health)
	}
	read, err := s.ReadFolderCoverage(t.Context(), "local", []string{localFixtureWorkspace, "song"}, []AdoptedFolder{{WorkspaceID: "song", Path: "/Invented/Users/demo/Music/Songs/Night Drive"}})
	if err != nil || read.Folders[0].State != CoverageUnknown || read.Folders[0].Reason != "permission_required" {
		t.Fatal("degraded evidence made a coverage claim", read, err)
	}
}

func TestTimeMachineReviewRefusesResticAuthority(t *testing.T) {
	s, _, _ := newBackupServiceFixture(t)
	s.timeMachine = &serviceFixtureProvider{}
	status, err := s.GetStatus(t.Context(), "local", localFixtureWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	token, _ := s.selections.IssueFor("/Invented/Songs", selectionScope("local", localFixtureWorkspace, "source"))
	cfg := inventedLocalConfiguration()
	base := ReviewRequest{Generation: status.Generation, Revision: status.Revision, Provider: providerTimeMachine, SourceSelections: []string{token}}
	for name, mutate := range map[string]func(*ReviewRequest){
		"credential": func(r *ReviewRequest) {
			r.Credential = CredentialReference{cfg.Credential.VaultID, cfg.Credential.RecordID, cfg.Credential.Revision}
		},
		"volume":           func(r *ReviewRequest) { r.VolumeSelection = "token" },
		"repository":       func(r *ReviewRequest) { r.RepositorySelection = "token" },
		"no source":        func(r *ReviewRequest) { r.SourceSelections = nil },
		"saved restic":     func(r *ReviewRequest) { r.SourceSelections, r.UseSavedSelection = nil, true },
		"unknown provider": func(r *ReviewRequest) { r.Provider = "/bin/sh" },
	} {
		request := base
		mutate(&request)
		if _, err := s.Review(t.Context(), "local", localFixtureWorkspace, request); err == nil {
			t.Fatal(name, "was accepted")
		}
	}
	other, _ := s.selections.IssueFor("/Invented/Other", selectionScope("local", "another-workspace", "source"))
	base.SourceSelections = []string{other}
	if _, err := s.Review(t.Context(), "local", localFixtureWorkspace, base); err != ErrReviewExpired {
		t.Fatal("another workspace's selection was accepted", err)
	}
}

func TestTimeMachineNotSetUpIsClaimedOnlyFromReadablePreferences(t *testing.T) {
	s, reader := timeMachineServiceFixture(t, []string{"/Invented/Users/demo/Notes"}, []string{timeMachineIncluded}, false)
	reader.readFn = func(ctx context.Context, cfg localConfiguration, _ backupPasswordUse) (providerReadResult, error) {
		return timeMachineReadResult(cfg, timeMachineHelperResult{DestinationInfo: timeMachineFixture(t, "destinationinfo_none.plist"), Status: timeMachineFixture(t, "status_idle.plist"),
			Preferences: timeMachineFixture(t, "invented_preferences_no_destination.bplist"), Sticky: []string{timeMachineIncluded}}, time.Now().UTC())
	}
	refresh := refreshBackupServiceFixture(t, s)
	if result, err := s.Refresh(t.Context(), "local", localFixtureWorkspace, refresh); err != nil || result.Outcome != "read_ok" {
		t.Fatal(result, err)
	}
	status, err := s.GetStatus(t.Context(), "local", localFixtureWorkspace)
	if err != nil || status.Destination != "not_configured" || !strings.Contains(strings.Join(status.Health.Problems, ","), "not_configured") {
		t.Fatal(status, err)
	}
}
