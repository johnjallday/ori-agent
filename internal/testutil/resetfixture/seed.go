package resetfixture

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	AgentName   = "Fixture Assistant"
	WorkspaceID = "reset-fixture-workspace"
	SessionID   = "reset-fixture-session"
)

// NewSeeded creates the minimal populated installation through real persistence
// APIs. No providers, schedulers, watchers, native secrets, or HTTP servers are
// started. All seed handles are closed before return. Reopen these same paths
// to test relaunch; do not call NewSeeded again to verify a reset.
//
// This is intentionally not the full Start Fresh inventory: retained vault
// sentinels are opaque bytes, not yet a decryptable vault or legacy migration.
func NewSeeded(t testing.TB) *Fixture {
	t.Helper()
	f := New(t)
	p := f.Paths()
	cfg := config.NewManagerWithSecretStore(filepath.Join(p.DataDir, "settings.json"), f.Secrets())
	must(t, cfg.Load())
	must(t, cfg.SetWorkspaceRoot(p.Workspaces))
	must(t, cfg.SetVaultRoot(p.Vaults))
	must(t, cfg.SetTemplatesRoot(p.Templates))
	must(t, cfg.Save())

	onboardingMgr := onboarding.NewManager(filepath.Join(p.DataDir, "app_state.json"))
	must(t, onboardingMgr.SetNames("Fixture User", AgentName))
	must(t, onboardingMgr.CompleteOnboarding())

	agents, err := store.NewFileStore(filepath.Join(p.DataDir, "agents.json"), types.Settings{Model: "fixture-model"})
	must(t, err)
	must(t, agents.CreateAgent(AgentName, nil))

	folders, err := workspace.NewFileStore(p.Workspaces)
	must(t, err)
	defer func() { must(t, folders.Close()) }()
	now := time.Now().UTC()
	must(t, folders.Save(&workspace.Workspace{
		ID: WorkspaceID, Name: "Fixture Project", Status: workspace.StatusActive,
		CreatedAt: now, UpdatedAt: now, SharedData: map[string]any{},
	}))

	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{Path: filepath.Join(p.DataDir, "sessions.db"), WALMode: true})
	must(t, err)
	defer func() { must(t, db.Close()) }()
	sessions := session.NewSQLiteStore(db)
	must(t, sessions.CreateSession(ctx, &session.Session{
		ID: SessionID, Title: "Fixture Conversation", AgentName: AgentName,
		CreatedAt: now, UpdatedAt: now,
	}))
	must(t, sessions.AddMessage(ctx, SessionID, &session.Message{
		ID: "reset-fixture-message", Role: session.RoleUser,
		Content: "Synthetic reset fixture history", CreatedAt: now,
	}))
	must(t, f.WriteFile("data/session_files/fixture-upload.txt", []byte("Synthetic upload\n")))
	return f
}
