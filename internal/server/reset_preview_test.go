package server

import (
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/settingshttp"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
)

func TestResetPreviewUsesLateBoundOwnersAndCapturedInstallation(t *testing.T) {
	f := resetfixture.NewSeeded(t)
	p := f.Paths()
	builder := &ServerBuilder{resetHandler: settingshttp.NewResetHandler(nil, nil, p.DataDir)}
	resolve := builder.resetPreviewOwners
	if early := resolve(); early.Config != nil || early.Database != nil {
		t.Fatal("missing owners were reconstructed")
	}
	builder.configManager = config.NewManagerWithSecretStore(filepath.Join(p.WorkDir, "actual-settings.json"), f.Secrets())
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(p.DataDir, "sessions.db"), WALMode: true})
	requireResetNoError(t, err)
	builder.sessionStore = session.NewHybridStoreWithDB(db, 10)
	t.Cleanup(func() { requireResetNoError(t, builder.sessionStore.Close()) })
	// A later environment change must not substitute another installation for
	// the data directory already attached to this handler/runtime.
	t.Setenv("ORI_DATA_DIR", p.WorkDir)
	owners := resolve()
	if owners.DataDir != p.DataDir || owners.Config != builder.configManager || owners.Database != db {
		t.Fatal("preview did not use the actual late-bound owners")
	}
	if owners.CheckLifecycle != nil {
		t.Fatal("builder advertised unsupported restart admission")
	}
}
