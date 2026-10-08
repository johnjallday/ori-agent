package personalassistant

import (
	"testing"
)

func TestDestinationPlanDigest_BindsIdentityAndMaterialWitness(t *testing.T) {
	base := FolderSetupDestination{Status: "existing", WorkspaceID: "home-a", Name: "Music Home", Kind: "home", OwnerUserID: "local", RecordVersion: 4, ProgramRevision: 3, ProviderPlugin: "music", ProviderVersion: "1.1.0", ProgramID: "music-program", DeclarationDigest: "declaration-a", CompatibilityHash: "compatibility-a"}
	plan := NewFolderSetupPlan(samplePlanLines())
	plan.Destination = &base
	stamped := plan.Stamped()
	if stamped.Digest == FolderPlanDigest(plan.Lines) || stamped.Intent.Destination == nil {
		t.Fatal("destination not bound")
	}
	for _, mutate := range []func(*FolderSetupDestination){
		func(d *FolderSetupDestination) { d.WorkspaceID = "same-name-home-b" },
		func(d *FolderSetupDestination) { d.Name = "Renamed Home" },
		func(d *FolderSetupDestination) { d.ParentID = "other-group" },
		func(d *FolderSetupDestination) { d.OwnerUserID = "foreign" },
		func(d *FolderSetupDestination) { d.RecordVersion++ },
		func(d *FolderSetupDestination) { d.ProgramRevision++ },
		func(d *FolderSetupDestination) { d.ProviderVersion = "2.0.0" },
		func(d *FolderSetupDestination) { d.CompatibilityHash = "changed-compatibility" },
	} {
		changed := base
		mutate(&changed)
		if DestinationPlanDigest(plan.Lines, &changed) == stamped.Digest {
			t.Fatal("material identity change kept old consent", changed)
		}
	}
	progress := append([]FolderPlanLine(nil), plan.Lines...)
	progress[0].State = FolderLineWorking
	if DestinationPlanDigest(progress, &base) != stamped.Digest {
		t.Fatal("progress changed consent")
	}
	base.Name = "Later name"
	if stamped.Destination.Name != "Music Home" {
		t.Fatal("stamped destination was mutable input alias")
	}
}

func TestDestinationPlanDigest_LegacyAndInvalid(t *testing.T) {
	lines := samplePlanLines()
	if DestinationPlanDigest(lines, nil) != FolderPlanDigest(lines) {
		t.Fatal("legacy plan changed")
	}
	invalid := &FolderSetupDestination{Status: "existing", WorkspaceID: "home", Name: "/private/source", OwnerUserID: "local"}
	if DestinationPlanDigest(lines, invalid) != "" {
		t.Fatal("invalid destination could authorize confirmation")
	}
}
