package personalassistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/backuprecovery"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestBackupReadinessRelationshipScopeAndNoWriteAuthority(t *testing.T) {
	for _, state := range []RelationshipStatus{StatusNotHired, StatusActive, StatusPaused} {
		t.Run(string(state), func(t *testing.T) {
			relationships, _, _, briefs, _ := serviceMatrixFixture(state)
			relationships.models = fakeModelReader{availability: SourceAvailability{Status: AvailabilityNotConfigured}}
			briefs.config.Scope = dailybrief.ScopeSelected
			briefs.config.SelectedWorkspaceIDs = []string{"backup"}
			store := workspace.NewInMemoryStore()
			ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "PRIVATE"})
			ws.ID = "backup"
			ws.FolderSlug = "canonical-backup"
			ws.OwnerUserID = "local"
			ws.CreatedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			if err := store.Save(ws); err != nil {
				t.Fatal(err)
			}
			reader := &todayTestBackupReader{read: backuprecovery.SummaryRead{Complete: true, Sources: []backuprecovery.SummaryFacts{{WorkspaceID: "backup", Generation: "gen", Connection: "configured", Monitoring: "manual_only", RecoveryState: "not_tested"}, {WorkspaceID: "foreign", Generation: "gen", Route: "https://evil.example"}}}}
			capabilities := NewCapabilityService(relationships, store, nil)
			capabilities.SetBackupReader(reader, store)
			got, err := capabilities.Get(context.Background(), "local")
			if err != nil {
				t.Fatal(err)
			}
			if state == StatusNotHired {
				if len(got.Cards) != 0 || reader.calls != 0 {
					t.Fatal(got)
				}
				return
			}
			var card CapabilityCard
			for _, candidate := range got.Cards {
				if candidate.Key == "backup-recovery" {
					card = candidate
				}
			}
			if card.ActionRoute != "/workspaces/canonical-backup?panel=backup-recovery&tab=overview" || card.MappedWrite || !strings.Contains(card.RequiresConfirmation, "separate user controls") || strings.Contains(card.CanRead, "PRIVATE") {
				t.Fatal(card)
			}
			if len(ws.Tasks) != 0 || len(ws.DirectoryReferences) != 0 || len(ws.GetMCPBindings()) != 0 {
				t.Fatal("readiness gained write authority")
			}
			reader.read = backuprecovery.SummaryRead{Complete: true}
			got, err = capabilities.Get(context.Background(), "local")
			if err != nil {
				t.Fatal(err)
			}
			card = got.Cards[len(got.Cards)-1]
			if card.Status != CapabilityNotConfigured || card.ActionRoute != "/?create=1&blueprint=backup-and-recovery" {
				t.Fatal(card)
			}
		})
	}
}
