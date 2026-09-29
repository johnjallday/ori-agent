package trigger

import "testing"

// A workspace imported after startup: its definitions load once imported, but
// its watch and webhook token start only when its routines are turned on here,
// and stop again when they are turned off — all without a restart.
func TestReconcileWorkspaceFollowsImportAndActivationWithoutRestart(t *testing.T) {
	seed, source := triggerAdmissionFixture(t, "imported")
	watched := t.TempDir()
	watch, err := seed.Create(Trigger{ID: "imported-watch", WorkspaceID: "imported", Name: "Copied watch", Type: TypeFileWatch, Enabled: true,
		Action: Action{Kind: ActionDomainScan, Domain: "fixture"}, FileWatch: &FileWatchConfig{Path: watched}})
	triggerAdmissionMust(t, err)
	hook := webhookTrigger()
	hook.ID, hook.WorkspaceID, hook.Webhook = "imported-hook", "imported", &WebhookConfig{Token: "synthetic-imported-token"}
	_, err = seed.Create(hook)
	triggerAdmissionMust(t, err)

	// Not yet imported when the app starts: nothing is loaded.
	source.set("imported", false, false)
	service, err := NewService(ServiceConfig{Source: source})
	triggerAdmissionMust(t, err)
	triggerAdmissionMust(t, service.Start())
	t.Cleanup(service.Close)
	key := runtimeTriggerKey("imported", watch.ID)
	if len(service.List("imported")) != 0 {
		t.Fatal("an unimported folder's triggers loaded at startup")
	}

	// Imported, routines off: definitions readable, nothing running.
	source.set("imported", true, false)
	triggerAdmissionMust(t, service.ReconcileWorkspace("imported"))
	if len(service.List("imported")) != 2 {
		t.Fatal("imported definitions did not load")
	}
	if service.watch.watcher.IsWatching(key) {
		t.Fatal("an inactive import registered a watch")
	}
	if _, ok := service.store.GetByToken("synthetic-imported-token"); ok {
		t.Fatal("an inactive import's webhook token was accepted")
	}

	// Routines on here: watch and token start without a restart.
	source.set("imported", true, true)
	triggerAdmissionMust(t, service.ReconcileWorkspace("imported"))
	if !service.watch.watcher.IsWatching(key) {
		t.Fatal("enabling routines did not start the watch")
	}
	if _, ok := service.store.GetByToken("synthetic-imported-token"); !ok {
		t.Fatal("enabling routines did not accept the webhook token")
	}

	// Routines off again: both stop.
	source.set("imported", true, false)
	triggerAdmissionMust(t, service.ReconcileWorkspace("imported"))
	if service.watch.watcher.IsWatching(key) {
		t.Fatal("turning routines off left the watch running")
	}
	if _, ok := service.store.GetByToken("synthetic-imported-token"); ok {
		t.Fatal("turning routines off left the webhook token accepted")
	}
}
